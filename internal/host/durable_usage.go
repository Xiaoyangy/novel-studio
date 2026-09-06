package host

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/models"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type usageAuditRecord struct {
	Version    int                    `json:"version"`
	Kind       string                 `json:"kind"`
	ID         string                 `json:"id,omitempty"`
	Agent      string                 `json:"agent,omitempty"`
	Usage      *agentcore.Usage       `json:"usage,omitempty"`
	Metadata   map[string]any         `json:"metadata,omitempty"`
	Baseline   *domain.UsageState     `json:"baseline,omitempty"`
	Accounting *domain.UsageState     `json:"accounting,omitempty"`
	CallStart  *domain.UsageCallStart `json:"call_start,omitempty"`
	Digest     string                 `json:"digest"`
}

// DurableUsageMeter is the sole audited accounting writer for a live book.
// WAL records contain no messages, prompts, tool arguments or reasoning.
// Tracker is exposed for totals/budget observers; new calls must use Record.
type DurableUsageMeter struct {
	mu      sync.Mutex
	store   *store.Store
	tracker *UsageTracker
	// Tests can model a failure after a durable WAL append without corrupting it.
	saveSnapshot func(*store.UsageAuditTransaction, domain.UsageState) error
}

func NewDurableUsageMeter(st *store.Store) (*DurableUsageMeter, error) {
	if st == nil || st.Usage == nil {
		return nil, fmt.Errorf("durable usage meter requires a live store")
	}
	m := &DurableUsageMeter{store: st, tracker: NewUsageTracker(nil, st)}
	m.saveSnapshot = func(tx *store.UsageAuditTransaction, state domain.UsageState) error { return tx.Save(state) }
	err := st.Usage.WithAuditTransaction(func(tx *store.UsageAuditTransaction) error {
		loaded, err := m.tracker.LoadFromStore()
		if err != nil {
			return err
		}
		if err := validateDurableUsageState(m.tracker.Snapshot()); err != nil {
			return err
		}
		state := m.tracker.Snapshot()
		raw, end, err := tx.Read(state.AuditOffset)
		if err != nil {
			return err
		}
		if end == 0 {
			if !loaded {
				if _, err := m.tracker.ReplaySessions(st.Dir()); err != nil {
					return err
				}
			}
			baseline := m.tracker.Snapshot()
			if err := validateDurableUsageState(baseline); err != nil {
				return err
			}
			record := usageAuditRecord{Version: 1, Kind: "baseline", Baseline: &baseline}
			encoded, err := finalizeUsageAuditRecord(record)
			if err != nil {
				return err
			}
			offset, err := tx.Append(0, encoded)
			if err != nil {
				return err
			}
			m.tracker.mu.Lock()
			m.tracker.auditOffset = offset
			m.tracker.mu.Unlock()
		} else if len(raw) > 0 {
			if _, err := m.replay(raw, state.AuditOffset); err != nil {
				return err
			}
		}
		// With a baseline journal, old sessions have already been accounted for
		// exactly once. Replaying them alongside the WAL would double-count them.
		return m.saveSnapshot(tx, m.tracker.Snapshot())
	})
	if err != nil {
		return nil, fmt.Errorf("load durable usage meter: %w", err)
	}
	return m, nil
}

func (m *DurableUsageMeter) Tracker() *UsageTracker {
	if m == nil {
		return nil
	}
	return m.tracker
}

func (m *DurableUsageMeter) Has(id string) bool {
	if m == nil {
		return false
	}
	m.tracker.mu.Lock()
	defer m.tracker.mu.Unlock()
	_, ok := m.tracker.accountedUsageIDs[id]
	return ok
}

// Covered distinguishes a per-call-accounted group from an imported aggregate.
// Aggregates must still pass Record's payload-conflict check on every import.
func (m *DurableUsageMeter) Covered(id string) bool {
	if m == nil {
		return false
	}
	want := usageAuditIdentityDigest(usageAuditRecord{Version: 1, Kind: "cover", ID: id})
	m.tracker.mu.Lock()
	defer m.tracker.mu.Unlock()
	return m.tracker.accountedUsageIDs[id] == want
}

// StartCall durably records dispatch intent before the model is invoked.
// Repeating a pending identity preserves the original StartedAt timestamp.
func (m *DurableUsageMeter) StartCall(id, agent, generationID string, pid int, processStart string) error {
	if m == nil || !validUsageAuditLabel(id, 512) {
		return fmt.Errorf("usage call start requires a bounded id and meter")
	}
	start := domain.UsageCallStart{Agent: agentRoleName(agent), ProcessID: pid, ProcessStart: processStart, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), GenerationID: generationID}
	if err := validateUsageCallStart(start); err != nil {
		return err
	}
	return m.write(usageAuditRecord{Version: 1, Kind: "started", ID: id, CallStart: &start})
}

// SkipCall closes an explicitly started request that never reached the model.
// A durable skipped marker prevents retry from silently reopening the same ID.
func (m *DurableUsageMeter) SkipCall(id string) error {
	if m == nil || !validUsageAuditLabel(id, 512) {
		return fmt.Errorf("usage skip requires a bounded started id and meter")
	}
	return m.write(usageAuditRecord{Version: 1, Kind: "skipped", ID: id})
}

// PendingCalls returns an independent snapshot. Flush first when the caller
// needs to observe starts written by another meter/process since its last use.
func (m *DurableUsageMeter) PendingCalls() map[string]domain.UsageCallStart {
	if m == nil {
		return nil
	}
	return m.tracker.Snapshot().PendingUsageCalls
}

func (m *DurableUsageMeter) Record(id, agentName string, msg agentcore.Message) error {
	if m == nil {
		return fmt.Errorf("durable usage meter is nil")
	}
	if !validUsageAuditLabel(id, 512) || !validUsageAuditLabel(agentName, 256) {
		return fmt.Errorf("usage audit id and agent must be nonempty bounded identifiers")
	}
	record := usageAuditRecord{Version: 1, Kind: "record", ID: id, Agent: agentRoleName(agentName)}
	if msg.Usage != nil {
		usage := *msg.Usage
		if usage.Cost != nil {
			cost := *usage.Cost
			usage.Cost = &cost
		}
		record.Usage = &usage
	}
	metadata, err := sanitizeUsageAuditMetadata(msg.Metadata)
	if err != nil {
		return err
	}
	if supplied, ok := metadata["usage_audit_id"]; ok && supplied != id {
		return fmt.Errorf("usage_audit_id does not match Record id")
	}
	record.Metadata = metadata
	if err := validateUsageAuditUsage(record.Usage); err != nil {
		return err
	}
	if record.Usage == nil {
		record.Usage = &agentcore.Usage{}
		if record.Metadata == nil {
			record.Metadata = make(map[string]any)
		}
		record.Metadata["codex_usage_breakdown"] = []*models.TokenUsageRecord{nil}
		record.Metadata["codex_usage_breakdown_complete"] = false
	}
	// Freeze the price chosen now; replay must not re-price historical tokens
	// against a registry that may have changed since the original call.
	quote := NewUsageTracker(nil, nil)
	quote.loggedMissingUsage = true
	quote.Record(record.Agent, agentcore.Message{Role: agentcore.RoleAssistant, Usage: record.Usage, Metadata: record.Metadata})
	accounting := quote.Snapshot()
	accounting.UpdatedAt = time.Time{}
	record.Accounting = &accounting
	return m.write(record)
}

func (m *DurableUsageMeter) Cover(id string) error {
	if m == nil || !validUsageAuditLabel(id, 512) {
		return fmt.Errorf("usage cover requires a nonempty bounded id")
	}
	return m.write(usageAuditRecord{Version: 1, Kind: "cover", ID: id})
}

func (m *DurableUsageMeter) write(record usageAuditRecord) error {
	encoded, err := finalizeUsageAuditRecord(record)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, &record); err != nil {
		return err
	}
	m.mu.Lock()
	changed := false
	beforeMissing := m.tracker.MissingAssistantUsage()
	err = m.store.Usage.WithAuditTransaction(func(tx *store.UsageAuditTransaction) error {
		state := m.tracker.Snapshot()
		raw, _, err := tx.Read(state.AuditOffset)
		if err != nil {
			return err
		}
		if len(raw) > 0 {
			changed, err = m.replay(raw, state.AuditOffset)
			if err != nil {
				return err
			}
		}
		state = m.tracker.Snapshot()
		duplicate, err := validateUsageAuditTransition(state, record, false)
		if err != nil {
			return err
		}
		if duplicate {
			return m.saveSnapshot(tx, state)
		}
		offset, err := tx.Append(state.AuditOffset, encoded)
		if err != nil {
			return err
		}
		if err := m.apply(record, offset); err != nil {
			return err
		}
		changed = changed || record.Kind == "record"
		return m.saveSnapshot(tx, m.tracker.Snapshot())
	})
	m.mu.Unlock()
	if changed {
		m.notify(beforeMissing)
	}
	return err
}

func (m *DurableUsageMeter) Flush() error {
	if m == nil {
		return fmt.Errorf("durable usage meter is nil")
	}
	m.mu.Lock()
	changed, beforeMissing := false, m.tracker.MissingAssistantUsage()
	err := m.store.Usage.WithAuditTransaction(func(tx *store.UsageAuditTransaction) error {
		offset := m.tracker.Snapshot().AuditOffset
		raw, _, err := tx.Read(offset)
		if err != nil {
			return err
		}
		if len(raw) > 0 {
			changed, err = m.replay(raw, offset)
			if err != nil {
				return err
			}
		}
		return m.saveSnapshot(tx, m.tracker.Snapshot())
	})
	m.mu.Unlock()
	if changed {
		m.notify(beforeMissing)
	}
	return err
}

func (m *DurableUsageMeter) notify(beforeMissing int) {
	// External callbacks run after BOTH tracker and journal locks are released.
	total, _, _, _, _ := m.tracker.Totals()
	if m.tracker.onCost != nil {
		m.tracker.onCost(total)
	}
	if beforeMissing == 0 && m.tracker.MissingAssistantUsage() > 0 && m.tracker.onMissingUsage != nil {
		m.tracker.onMissingUsage()
	}
}

func (m *DurableUsageMeter) replay(raw []byte, start int64) (bool, error) {
	changed, offset := false, start
	for _, line := range bytes.Split(raw, []byte{'\n'})[:bytes.Count(raw, []byte{'\n'})] {
		if len(line) == 0 {
			return changed, fmt.Errorf("usage audit has an empty record at offset %d", offset)
		}
		var record usageAuditRecord
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return changed, fmt.Errorf("usage audit at offset %d: %w", offset, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return changed, fmt.Errorf("usage audit has trailing JSON at offset %d", offset)
		}
		canonical, err := finalizeUsageAuditRecord(record)
		if err != nil {
			return changed, err
		}
		var signed usageAuditRecord
		if err := json.Unmarshal(canonical, &signed); err != nil || signed.Digest != record.Digest {
			return changed, fmt.Errorf("usage audit digest mismatch at offset %d", offset)
		}
		offset += int64(len(line) + 1)
		if start == 0 && record.Kind != "baseline" {
			return changed, fmt.Errorf("usage audit must begin with its accounting baseline")
		}
		if record.Kind == "baseline" && start != 0 {
			return changed, fmt.Errorf("usage audit baseline cannot appear after committed records")
		}
		if err := m.apply(record, offset); err != nil {
			return changed, err
		}
		changed = changed || record.Kind == "record"
		start = offset
	}
	return changed, nil
}

func (m *DurableUsageMeter) apply(record usageAuditRecord, offset int64) error {
	if record.Kind == "baseline" {
		state := *record.Baseline
		state.AuditOffset = offset
		m.tracker.applyState(state)
		return nil
	}
	state := m.tracker.Snapshot()
	duplicate, err := validateUsageAuditTransition(state, record, true)
	if err != nil {
		return err
	}
	if duplicate {
		m.tracker.mu.Lock()
		m.tracker.auditOffset = offset
		m.tracker.mu.Unlock()
		return nil
	}
	if record.Kind == "started" {
		m.tracker.mu.Lock()
		defer m.tracker.mu.Unlock()
		if m.tracker.pendingUsageCalls == nil {
			m.tracker.pendingUsageCalls = make(map[string]domain.UsageCallStart)
		}
		m.tracker.pendingUsageCalls[record.ID] = *record.CallStart
		m.tracker.auditOffset = offset
		return nil
	}
	delta := NewUsageTracker(nil, nil)
	if record.Kind == "record" {
		delta.applyState(*record.Accounting)
		pushSample(&delta.overall, record.Usage.CacheRead, record.Usage.Input)
		for _, totals := range delta.perAgent {
			pushSample(totals, record.Usage.CacheRead, record.Usage.Input)
		}
		for _, totals := range delta.perModel {
			pushSample(totals, record.Usage.CacheRead, record.Usage.Input)
		}
	}
	m.tracker.mu.Lock()
	defer m.tracker.mu.Unlock()
	mergeAuditedUsageTotals(&m.tracker.overall, &delta.overall)
	for key, values := range delta.perAgent {
		if m.tracker.perAgent[key] == nil {
			m.tracker.perAgent[key] = &agentTotals{}
		}
		mergeAuditedUsageTotals(m.tracker.perAgent[key], values)
	}
	for key, values := range delta.perModel {
		if m.tracker.perModel[key] == nil {
			m.tracker.perModel[key] = &agentTotals{}
		}
		mergeAuditedUsageTotals(m.tracker.perModel[key], values)
	}
	m.tracker.missingAssistantUsage += delta.missingAssistantUsage
	if m.tracker.accountedUsageIDs == nil {
		m.tracker.accountedUsageIDs = make(map[string]string)
	}
	m.tracker.accountedUsageIDs[record.ID] = usageAuditIdentityDigest(record)
	if record.Kind == "record" || record.Kind == "skipped" {
		delete(m.tracker.pendingUsageCalls, record.ID)
	}
	m.tracker.auditOffset = offset
	return nil
}

func mergeAuditedUsageTotals(target, delta *agentTotals) {
	target.Input += delta.Input
	target.Output += delta.Output
	target.CacheRead += delta.CacheRead
	target.CacheWrite += delta.CacheWrite
	target.Cost += delta.Cost
	target.Saved += delta.Saved
	target.CacheCapable = target.CacheCapable || delta.CacheCapable
	for _, sample := range delta.samples {
		pushSample(target, sample.CacheRead, sample.Input)
	}
}

func finalizeUsageAuditRecord(record usageAuditRecord) ([]byte, error) {
	if record.Version != 1 {
		return nil, fmt.Errorf("unsupported usage audit version")
	}
	switch record.Kind {
	case "baseline":
		if record.Baseline == nil || record.Accounting != nil || record.ID != "" || record.Usage != nil || len(record.Metadata) != 0 || record.Agent != "" || record.Baseline.AuditOffset != 0 || record.CallStart != nil {
			return nil, fmt.Errorf("invalid usage audit baseline")
		}
		if err := validateDurableUsageState(*record.Baseline); err != nil {
			return nil, err
		}
	case "record", "cover":
		if !validUsageAuditLabel(record.ID, 512) || record.Baseline != nil || record.CallStart != nil {
			return nil, fmt.Errorf("invalid usage audit identity")
		}
		if record.Kind == "cover" && (record.Usage != nil || record.Agent != "" || len(record.Metadata) != 0 || record.Accounting != nil) {
			return nil, fmt.Errorf("usage cover cannot contain usage")
		}
		if record.Kind == "record" {
			if record.Usage == nil || !validUsageAuditLabel(record.Agent, 256) {
				return nil, fmt.Errorf("usage record requires agent and usage")
			}
			if err := validateUsageAuditUsage(record.Usage); err != nil {
				return nil, err
			}
			if record.Accounting == nil {
				return nil, fmt.Errorf("usage audit record lacks frozen accounting quote")
			}
			if err := validateDurableUsageState(*record.Accounting); err != nil {
				return nil, err
			}
			quote := record.Accounting
			if quote.AuditOffset != 0 || len(quote.AccountedUsageIDs) != 0 || len(quote.PendingUsageCalls) != 0 || quote.Overall.Input != record.Usage.Input || quote.Overall.Output != record.Usage.Output || quote.Overall.CacheRead != record.Usage.CacheRead || quote.Overall.CacheWrite != record.Usage.CacheWrite || len(quote.PerAgent) != 1 || quote.PerAgent[record.Agent] != quote.Overall {
				return nil, fmt.Errorf("usage audit quote differs from recorded usage")
			}
			metadata, err := sanitizeUsageAuditMetadata(record.Metadata)
			if err != nil {
				return nil, err
			}
			before, _ := json.Marshal(record.Metadata)
			after, _ := json.Marshal(metadata)
			if !bytes.Equal(before, after) {
				return nil, fmt.Errorf("usage audit contains unsupported metadata")
			}
		}
	case "started", "skipped":
		if !validUsageAuditLabel(record.ID, 512) || record.Agent != "" || record.Usage != nil || len(record.Metadata) != 0 || record.Baseline != nil || record.Accounting != nil {
			return nil, fmt.Errorf("usage call lifecycle record cannot contain usage, quotes or message metadata")
		}
		if record.Kind == "started" {
			if record.CallStart == nil {
				return nil, fmt.Errorf("started usage call lacks process provenance")
			}
			if err := validateUsageCallStart(*record.CallStart); err != nil {
				return nil, err
			}
		} else if record.CallStart != nil {
			return nil, fmt.Errorf("skipped usage call cannot replace its start provenance")
		}
	default:
		return nil, fmt.Errorf("unsupported usage audit kind %q", record.Kind)
	}
	record.Digest = ""
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	record.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return json.Marshal(record)
}

func usageAuditIdentityDigest(record usageAuditRecord) string {
	// Provider usage defines call identity. A retry after a pricing update must
	// remain idempotent; the first WAL record's separately hashed quote wins.
	record.Digest, record.Accounting = "", nil
	raw, _ := json.Marshal(record)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sanitizeUsageAuditMetadata(metadata map[string]any) (map[string]any, error) {
	var out map[string]any
	put := func(key string, value any) {
		if out == nil {
			out = make(map[string]any)
		}
		out[key] = value
	}
	for _, key := range []string{"codex_usage_source", "usage_audit_id", "usage_audit_agent", "usage_group_id", "generation_id", "usage_accounting_gap", "stage"} {
		if value, ok := metadata[key]; ok {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("invalid usage audit metadata %s", key)
			}
			if text == "" {
				if key == "codex_usage_source" {
					text = "unknown"
				} else {
					continue
				}
			}
			if !validUsageAuditLabel(text, 1024) {
				return nil, fmt.Errorf("invalid usage audit metadata %s", key)
			}
			put(key, text)
		}
	}
	if value, ok := metadata["chapter"]; ok {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var chapter int
		if err := json.Unmarshal(raw, &chapter); err != nil || chapter < 0 {
			return nil, fmt.Errorf("invalid usage audit chapter")
		}
		put("chapter", chapter)
	}
	if value, ok := metadata["codex_usage_breakdown_complete"]; ok {
		complete, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("invalid usage audit breakdown completeness")
		}
		put("codex_usage_breakdown_complete", complete)
	}
	if value, ok := metadata["codex_usage_breakdown"]; ok {
		calls, err := models.DecodeTokenUsageBreakdown(value)
		if err != nil {
			return nil, err
		}
		for _, call := range calls {
			if call == nil {
				continue
			}
			usage := agentcore.Usage{Provider: call.Provider, Model: call.Model, Input: call.Input, Output: call.Output, CacheRead: call.CacheRead, CacheWrite: call.CacheWrite}
			if call.Cost != nil {
				usage.Cost = &agentcore.Cost{Total: call.Cost.Total}
			}
			if err := validateUsageAuditUsage(&usage); err != nil {
				return nil, err
			}
		}
		put("codex_usage_breakdown", calls)
	}
	return out, nil
}

func validateUsageAuditUsage(usage *agentcore.Usage) error {
	if usage == nil {
		return nil
	}
	if usage.Input < 0 || usage.Output < 0 || usage.CacheRead < 0 || usage.CacheWrite < 0 || usage.TotalTokens < 0 {
		return fmt.Errorf("usage audit token counts must be nonnegative")
	}
	for _, name := range []string{usage.Provider, usage.Model} {
		if name != "" && !validUsageAuditLabel(name, 256) {
			return fmt.Errorf("invalid usage audit provider/model")
		}
	}
	if usage.Cost != nil {
		for _, value := range []float64{usage.Cost.Input, usage.Cost.Output, usage.Cost.CacheRead, usage.Cost.CacheWrite, usage.Cost.Total} {
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				return fmt.Errorf("usage audit cost must be finite and nonnegative")
			}
		}
	}
	return nil
}

func validateDurableUsageState(state domain.UsageState) error {
	if state.Schema != domain.UsageSchemaVersion || state.AuditOffset < 0 || state.MissingUsage < 0 {
		return fmt.Errorf("invalid durable usage state identity")
	}
	check := func(t domain.AgentUsageTotals) error {
		if t.Input < 0 || t.Output < 0 || t.CacheRead < 0 || t.CacheWrite < 0 || t.Cost < 0 || t.Saved < 0 || math.IsNaN(t.Cost) || math.IsInf(t.Cost, 0) || math.IsNaN(t.Saved) || math.IsInf(t.Saved, 0) {
			return fmt.Errorf("invalid durable usage totals")
		}
		return nil
	}
	if err := check(state.Overall); err != nil {
		return err
	}
	for _, totals := range state.PerAgent {
		if err := check(totals); err != nil {
			return err
		}
	}
	for _, totals := range state.PerModel {
		if err := check(totals); err != nil {
			return err
		}
	}
	for id, digest := range state.AccountedUsageIDs {
		if !validUsageAuditLabel(id, 512) || !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
			return fmt.Errorf("invalid accounted usage id/digest")
		}
	}
	for id, start := range state.PendingUsageCalls {
		if !validUsageAuditLabel(id, 512) {
			return fmt.Errorf("invalid pending usage call id")
		}
		if _, closed := state.AccountedUsageIDs[id]; closed {
			return fmt.Errorf("usage call cannot be both pending and closed")
		}
		if err := validateUsageCallStart(start); err != nil {
			return err
		}
	}
	return nil
}

var usageProcessStartPattern = regexp.MustCompile(`^[A-Za-z0-9_.:+/\-]{0,128}$`)

func validateUsageCallStart(start domain.UsageCallStart) error {
	if !validUsageAuditLabel(start.Agent, 256) || start.Agent != agentRoleName(start.Agent) || start.ProcessID <= 0 || !usageProcessStartPattern.MatchString(start.ProcessStart) || (start.GenerationID != "" && !validUsageAuditLabel(start.GenerationID, 256)) {
		return fmt.Errorf("usage call start requires bounded agent/generation, positive pid and opaque process-start identity")
	}
	if _, err := time.Parse(time.RFC3339Nano, start.StartedAt); err != nil {
		return fmt.Errorf("usage call start requires an RFC3339Nano timestamp")
	}
	return nil
}

func validateUsageAuditTransition(state domain.UsageState, record usageAuditRecord, replay bool) (bool, error) {
	if existing, closed := state.AccountedUsageIDs[record.ID]; closed {
		if record.Kind == "started" || (record.Kind != "cover" && existing != usageAuditIdentityDigest(record)) {
			return false, fmt.Errorf("usage audit id %q conflicts with an already closed record", record.ID)
		}
		return true, nil
	}
	pending, started := state.PendingUsageCalls[record.ID]
	switch record.Kind {
	case "started":
		if started {
			wanted := *record.CallStart
			if !replay {
				wanted.StartedAt = pending.StartedAt
			}
			if wanted != pending {
				return false, fmt.Errorf("usage call start %q conflicts with its durable process/agent/generation identity", record.ID)
			}
			return true, nil
		}
	case "skipped":
		if !started {
			return false, fmt.Errorf("cannot skip usage call %q without a durable start", record.ID)
		}
	case "cover":
		if started {
			return false, fmt.Errorf("usage group cover cannot replace pending call %q", record.ID)
		}
	case "record":
		if started {
			if record.Agent != pending.Agent {
				return false, fmt.Errorf("usage record agent differs from its pending call")
			}
			if generation, supplied := record.Metadata["generation_id"]; supplied && generation != pending.GenerationID {
				return false, fmt.Errorf("usage record generation differs from its pending call")
			}
		}
	}
	return false, nil
}

func validUsageAuditLabel(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= limit && strings.IndexFunc(value, unicode.IsControl) < 0
}
