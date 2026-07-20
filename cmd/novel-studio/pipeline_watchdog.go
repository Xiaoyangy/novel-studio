package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	pipelineWatchdogStatePath      = "meta/runtime/pipeline_watchdog.json"
	pipelineWatchdogDiagnosticPath = "meta/runtime/pipeline_watchdog_diagnostics"

	pipelineWatchdogDefaultHeartbeatInterval = 15 * time.Second
	pipelineWatchdogDefaultStallAfter        = 5 * time.Minute
	pipelineWatchdogDefaultDiagnosticAfter   = 25 * time.Minute
)

const (
	pipelineWatchdogRunning = "running"
	pipelineWatchdogStalled = "stalled"
	pipelineWatchdogStopped = "stopped"
)

var pipelineWatchdogSafeToken = regexp.MustCompile(`^[A-Za-z0-9._:/@+\-=]+$`)

// pipelineWatchdogState is the current, atomically rewritten control-plane
// view. HeartbeatAt proves only that the owner process is alive. Progress is a
// separate signal and must be advanced explicitly by Progress; otherwise a
// live but wedged process would hide behind its own heartbeat forever.
type pipelineWatchdogState struct {
	Schema              string `json:"schema"`
	InvocationID        string `json:"invocation_id"`
	RunIdentity         string `json:"run_identity,omitempty"`
	Stage               string `json:"stage"`
	Chapter             int    `json:"chapter,omitempty"`
	PlanDigest          string `json:"plan_digest,omitempty"`
	BodySHA256          string `json:"body_sha256,omitempty"`
	Status              string `json:"status"`
	StartedAt           string `json:"started_at"`
	HeartbeatAt         string `json:"heartbeat_at"`
	LastProgressAt      string `json:"last_progress_at"`
	LastProgressKind    string `json:"last_progress_kind,omitempty"`
	ProgressSeq         int64  `json:"progress_seq"`
	StallAfterMS        int64  `json:"stall_after_ms"`
	DiagnosticAfterMS   int64  `json:"diagnostic_after_ms"`
	DiagnosticEmittedAt string `json:"diagnostic_emitted_at,omitempty"`
	DiagnosticRelPath   string `json:"diagnostic_rel_path,omitempty"`
	StoppedAt           string `json:"stopped_at,omitempty"`
}

// pipelineWatchdogDiagnostic intentionally contains identifiers, hashes and
// timings only. It has no prompt, content, text, summary, detail or error field
// into which novel prose could accidentally leak.
type pipelineWatchdogDiagnostic struct {
	Schema            string `json:"schema"`
	InvocationID      string `json:"invocation_id"`
	RunIdentity       string `json:"run_identity,omitempty"`
	Stage             string `json:"stage"`
	Chapter           int    `json:"chapter,omitempty"`
	PlanDigest        string `json:"plan_digest,omitempty"`
	BodySHA256        string `json:"body_sha256,omitempty"`
	Status            string `json:"status"`
	StartedAt         string `json:"started_at"`
	ObservedAt        string `json:"observed_at"`
	ElapsedMS         int64  `json:"elapsed_ms"`
	HeartbeatAt       string `json:"heartbeat_at"`
	LastProgressAt    string `json:"last_progress_at"`
	NoProgressMS      int64  `json:"no_progress_ms"`
	LastProgressKind  string `json:"last_progress_kind,omitempty"`
	ProgressSeq       int64  `json:"progress_seq"`
	StallAfterMS      int64  `json:"stall_after_ms"`
	DiagnosticAfterMS int64  `json:"diagnostic_after_ms"`
}

type pipelineWatchdogConfig struct {
	OutputDir         string
	InvocationID      string
	RunIdentity       string
	Stage             string
	Chapter           int
	PlanDigest        string
	BodySHA256        string
	StartedAt         time.Time
	HeartbeatInterval time.Duration
	StallAfter        time.Duration
	DiagnosticAfter   time.Duration
	Now               func() time.Time
}

type pipelineWatchdog struct {
	mu sync.Mutex

	controlRoot       string
	heartbeatInterval time.Duration
	stallAfter        time.Duration
	diagnosticAfter   time.Duration
	now               func() time.Time
	state             pipelineWatchdogState

	started    bool
	stopped    bool
	pauseDepth int
	asyncErr   error
	stopCh     chan struct{}
	doneCh     chan struct{}
	errCh      chan error
}

func newPipelineWatchdog(cfg pipelineWatchdogConfig) (*pipelineWatchdog, error) {
	outputDir := strings.TrimSpace(cfg.OutputDir)
	if outputDir == "" {
		return nil, fmt.Errorf("pipeline watchdog output directory is empty")
	}
	invocationID := pipelineWatchdogOpaqueToken(cfg.InvocationID)
	stage := pipelineWatchdogOpaqueToken(cfg.Stage)
	if invocationID == "" || stage == "" {
		return nil, fmt.Errorf("pipeline watchdog requires invocation id and stage")
	}
	if cfg.Chapter < 0 {
		return nil, fmt.Errorf("pipeline watchdog chapter must not be negative")
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	heartbeatInterval := cfg.HeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = pipelineWatchdogDefaultHeartbeatInterval
	}
	stallAfter := cfg.StallAfter
	if stallAfter <= 0 {
		stallAfter = pipelineWatchdogDefaultStallAfter
	}
	diagnosticAfter := cfg.DiagnosticAfter
	if diagnosticAfter <= 0 {
		diagnosticAfter = pipelineWatchdogDefaultDiagnosticAfter
	}
	if diagnosticAfter < stallAfter {
		return nil, fmt.Errorf("pipeline watchdog diagnostic threshold must not precede stall threshold")
	}

	now := nowFn().UTC()
	startedAt := cfg.StartedAt.UTC()
	if startedAt.IsZero() {
		startedAt = now
	}
	controlRoot, err := pipelineWatchdogControlRoot(outputDir)
	if err != nil {
		return nil, err
	}
	w := &pipelineWatchdog{
		controlRoot:       controlRoot,
		heartbeatInterval: heartbeatInterval,
		stallAfter:        stallAfter,
		diagnosticAfter:   diagnosticAfter,
		now:               nowFn,
		stopCh:            make(chan struct{}),
		doneCh:            make(chan struct{}),
		errCh:             make(chan error, 1),
		state: pipelineWatchdogState{
			Schema:            "pipeline-watchdog.v1",
			InvocationID:      invocationID,
			RunIdentity:       pipelineWatchdogOpaqueToken(cfg.RunIdentity),
			Stage:             stage,
			Chapter:           cfg.Chapter,
			PlanDigest:        pipelineWatchdogOpaqueToken(cfg.PlanDigest),
			BodySHA256:        pipelineWatchdogOpaqueToken(cfg.BodySHA256),
			Status:            pipelineWatchdogRunning,
			StartedAt:         startedAt.Format(time.RFC3339Nano),
			HeartbeatAt:       now.Format(time.RFC3339Nano),
			LastProgressAt:    startedAt.Format(time.RFC3339Nano),
			StallAfterMS:      stallAfter.Milliseconds(),
			DiagnosticAfterMS: diagnosticAfter.Milliseconds(),
		},
	}
	if err := w.restoreSameInvocation(); err != nil {
		return nil, err
	}
	return w, nil
}

// Start persists a running heartbeat immediately, then keeps evaluating the
// watchdog on a small independent ticker. Tests can avoid sleeping entirely by
// calling EvaluateAt with a fake clock.
func (w *pipelineWatchdog) Start() error {
	if w == nil {
		return fmt.Errorf("pipeline watchdog is nil")
	}
	w.mu.Lock()
	if w.stopped || w.state.Status == pipelineWatchdogStopped {
		w.mu.Unlock()
		return fmt.Errorf("pipeline watchdog has stopped")
	}
	if w.started {
		w.mu.Unlock()
		return nil
	}
	err := w.evaluateLocked(w.now().UTC())
	if err == nil {
		w.started = true
	}
	w.mu.Unlock()
	if err != nil {
		return err
	}
	go w.run()
	return nil
}

func (w *pipelineWatchdog) run() {
	ticker := time.NewTicker(w.heartbeatInterval)
	defer ticker.Stop()
	defer close(w.doneCh)
	for {
		select {
		case now := <-ticker.C:
			if err := w.EvaluateAt(now); err != nil {
				w.mu.Lock()
				w.asyncErr = errors.Join(w.asyncErr, err)
				w.mu.Unlock()
				select {
				case w.errCh <- err:
				default:
				}
			}
		case <-w.stopCh:
			return
		}
	}
}

// Progress is the only operation that advances LastProgressAt/ProgressSeq.
// kind is treated as an opaque code; unsafe or prose-like values are replaced
// by a digest before persistence.
func (w *pipelineWatchdog) Progress(kind string) error {
	return w.progress(kind, "", false)
}

// ProgressBody advances progress and installs the exact opaque body identity
// in one state rewrite. It never persists chapter bytes; unsafe values are
// reduced to a one-way digest by pipelineWatchdogOpaqueToken.
func (w *pipelineWatchdog) ProgressBody(kind, bodySHA256 string) error {
	return w.progress(kind, bodySHA256, true)
}

func (w *pipelineWatchdog) progress(kind, bodySHA256 string, updateBody bool) error {
	if w == nil {
		return fmt.Errorf("pipeline watchdog is nil")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.state.Status == pipelineWatchdogStopped {
		return fmt.Errorf("pipeline watchdog has stopped")
	}
	now := w.monotonicNowLocked(w.now().UTC())
	w.state.HeartbeatAt = now.Format(time.RFC3339Nano)
	w.state.LastProgressAt = now.Format(time.RFC3339Nano)
	w.state.LastProgressKind = pipelineWatchdogOpaqueToken(kind)
	if updateBody {
		w.state.BodySHA256 = pipelineWatchdogOpaqueToken(bodySHA256)
	}
	w.state.ProgressSeq++
	w.state.Status = pipelineWatchdogRunning
	if w.pauseDepth > 0 {
		return nil
	}
	return w.persistStateLocked()
}

// SetBodySHA256 updates only the opaque body identity. It deliberately does
// not count as progress; the caller must separately record the durable event
// that made the new hash authoritative.
func (w *pipelineWatchdog) SetBodySHA256(bodySHA256 string) error {
	if w == nil {
		return fmt.Errorf("pipeline watchdog is nil")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.state.Status == pipelineWatchdogStopped {
		return fmt.Errorf("pipeline watchdog has stopped")
	}
	w.state.BodySHA256 = pipelineWatchdogOpaqueToken(bodySHA256)
	if w.pauseDepth > 0 {
		return nil
	}
	return w.persistStateLocked()
}

// Pause quiesces every watchdog filesystem write while a directory publisher
// temporarily moves the live output root out of place. State changes made by
// Progress during the pause remain in memory and are flushed by the outermost
// Resume after the new live root is installed.
func (w *pipelineWatchdog) Pause() error {
	if w == nil {
		return fmt.Errorf("pipeline watchdog is nil")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.state.Status == pipelineWatchdogStopped {
		return fmt.Errorf("pipeline watchdog has stopped")
	}
	w.pauseDepth++
	return nil
}

// Resume ends one nested pause and, at the outer boundary, atomically writes
// the accumulated state into the newly installed live directory.
func (w *pipelineWatchdog) Resume() error {
	if w == nil {
		return fmt.Errorf("pipeline watchdog is nil")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.state.Status == pipelineWatchdogStopped {
		return fmt.Errorf("pipeline watchdog has stopped")
	}
	if w.pauseDepth == 0 {
		return fmt.Errorf("pipeline watchdog is not paused")
	}
	w.pauseDepth--
	if w.pauseDepth > 0 {
		return nil
	}
	return w.evaluateLocked(w.now().UTC())
}

func (w *pipelineWatchdog) Evaluate() error {
	if w == nil {
		return fmt.Errorf("pipeline watchdog is nil")
	}
	return w.EvaluateAt(w.now().UTC())
}

// EvaluateAt is the deterministic policy boundary used by both the ticker and
// tests. A heartbeat is always refreshed, but progress timestamps are never
// touched here. The 25-minute diagnostic path is deterministic and therefore
// remains a single snapshot across repeated evaluations and process recovery.
func (w *pipelineWatchdog) EvaluateAt(now time.Time) error {
	if w == nil {
		return fmt.Errorf("pipeline watchdog is nil")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.state.Status == pipelineWatchdogStopped {
		return nil
	}
	if w.pauseDepth > 0 {
		return nil
	}
	return w.evaluateLocked(now.UTC())
}

func (w *pipelineWatchdog) evaluateLocked(now time.Time) error {
	now = w.monotonicNowLocked(now)
	startedAt, err := time.Parse(time.RFC3339Nano, w.state.StartedAt)
	if err != nil {
		return fmt.Errorf("pipeline watchdog started_at is invalid: %w", err)
	}
	lastProgressAt, err := time.Parse(time.RFC3339Nano, w.state.LastProgressAt)
	if err != nil {
		return fmt.Errorf("pipeline watchdog last_progress_at is invalid: %w", err)
	}
	wasStalled := w.state.Status == pipelineWatchdogStalled
	w.state.HeartbeatAt = now.Format(time.RFC3339Nano)
	if now.Sub(lastProgressAt) >= w.stallAfter {
		w.state.Status = pipelineWatchdogStalled
	} else {
		w.state.Status = pipelineWatchdogRunning
	}
	if !wasStalled && w.state.Status == pipelineWatchdogStalled {
		fmt.Fprintf(
			os.Stderr,
			"[pipeline:watchdog] stage=%s chapter=%d 已连续 %s 无持久进展；进程心跳仍存活，等待后续脱敏诊断快照\n",
			w.state.Stage,
			w.state.Chapter,
			now.Sub(lastProgressAt).Round(time.Second),
		)
	}

	var diagnosticErr error
	if now.Sub(startedAt) >= w.diagnosticAfter && w.state.DiagnosticRelPath == "" {
		diagnosticErr = w.emitDiagnosticLocked(now, startedAt, lastProgressAt)
	}
	persistErr := w.persistStateLocked()
	return errors.Join(diagnosticErr, persistErr)
}

// Stop is idempotent. Once stopped, later ticker races or explicit EvaluateAt
// calls cannot turn the persisted state back to running.
func (w *pipelineWatchdog) Stop() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	if w.stopped || w.state.Status == pipelineWatchdogStopped {
		w.stopped = true
		err := w.asyncErr
		w.mu.Unlock()
		return err
	}
	w.stopped = true
	now := w.monotonicNowLocked(w.now().UTC())
	w.state.Status = pipelineWatchdogStopped
	w.state.HeartbeatAt = now.Format(time.RFC3339Nano)
	w.state.StoppedAt = now.Format(time.RFC3339Nano)
	err := w.persistStateLocked()
	started := w.started
	if started {
		close(w.stopCh)
	}
	w.mu.Unlock()
	if started {
		<-w.doneCh
	}
	w.mu.Lock()
	asyncErr := w.asyncErr
	w.mu.Unlock()
	return errors.Join(err, asyncErr)
}

// Errors exposes asynchronous persistence failures without coupling the
// watchdog to a logger or to the pipeline runner that will eventually own it.
func (w *pipelineWatchdog) Errors() <-chan error {
	if w == nil {
		return nil
	}
	return w.errCh
}

func (w *pipelineWatchdog) restoreSameInvocation() error {
	path := filepath.Join(w.controlRoot, filepath.FromSlash(pipelineWatchdogStatePath))
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load pipeline watchdog state: %w", err)
	}
	var stored pipelineWatchdogState
	if err := json.Unmarshal(raw, &stored); err != nil {
		return fmt.Errorf("decode pipeline watchdog state: %w", err)
	}
	if stored.Schema != "pipeline-watchdog.v1" || stored.InvocationID != w.state.InvocationID ||
		stored.Status == pipelineWatchdogStopped {
		return nil
	}
	if err := validatePipelineWatchdogState(stored); err != nil {
		return fmt.Errorf("validate pipeline watchdog state: %w", err)
	}
	// Thresholds are part of the current executable policy, while the original
	// timeline and one-shot diagnostic receipt survive process recovery.
	stored.StallAfterMS = w.stallAfter.Milliseconds()
	stored.DiagnosticAfterMS = w.diagnosticAfter.Milliseconds()
	w.state = stored
	w.stopped = stored.Status == pipelineWatchdogStopped
	return nil
}

func validatePipelineWatchdogState(state pipelineWatchdogState) error {
	if state.Schema != "pipeline-watchdog.v1" || state.InvocationID == "" || state.Stage == "" {
		return fmt.Errorf("identity is incomplete")
	}
	if state.Chapter < 0 || state.ProgressSeq < 0 {
		return fmt.Errorf("numeric state is invalid")
	}
	switch state.Status {
	case pipelineWatchdogRunning, pipelineWatchdogStalled, pipelineWatchdogStopped:
	default:
		return fmt.Errorf("status %q is invalid", state.Status)
	}
	for label, value := range map[string]string{
		"started_at": state.StartedAt, "heartbeat_at": state.HeartbeatAt,
		"last_progress_at": state.LastProgressAt,
	} {
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	if state.DiagnosticEmittedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, state.DiagnosticEmittedAt); err != nil {
			return fmt.Errorf("diagnostic_emitted_at: %w", err)
		}
	}
	if state.StoppedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, state.StoppedAt); err != nil {
			return fmt.Errorf("stopped_at: %w", err)
		}
	}
	for label, value := range map[string]string{
		"invocation_id":      state.InvocationID,
		"run_identity":       state.RunIdentity,
		"stage":              state.Stage,
		"plan_digest":        state.PlanDigest,
		"body_sha256":        state.BodySHA256,
		"last_progress_kind": state.LastProgressKind,
	} {
		if value != "" && pipelineWatchdogOpaqueToken(value) != value {
			return fmt.Errorf("%s is not an opaque token", label)
		}
	}
	return nil
}

func (w *pipelineWatchdog) emitDiagnosticLocked(now, startedAt, lastProgressAt time.Time) error {
	rel := filepath.ToSlash(filepath.Join(
		pipelineWatchdogDiagnosticPath,
		pipelineWatchdogFileToken(w.state.InvocationID)+fmt.Sprintf("-ch%04d-25m.json", w.state.Chapter),
	))
	diagnostic := pipelineWatchdogDiagnostic{
		Schema:            "pipeline-watchdog-diagnostic.v1",
		InvocationID:      w.state.InvocationID,
		RunIdentity:       w.state.RunIdentity,
		Stage:             w.state.Stage,
		Chapter:           w.state.Chapter,
		PlanDigest:        w.state.PlanDigest,
		BodySHA256:        w.state.BodySHA256,
		Status:            w.state.Status,
		StartedAt:         w.state.StartedAt,
		ObservedAt:        now.Format(time.RFC3339Nano),
		ElapsedMS:         now.Sub(startedAt).Milliseconds(),
		HeartbeatAt:       w.state.HeartbeatAt,
		LastProgressAt:    w.state.LastProgressAt,
		NoProgressMS:      now.Sub(lastProgressAt).Milliseconds(),
		LastProgressKind:  w.state.LastProgressKind,
		ProgressSeq:       w.state.ProgressSeq,
		StallAfterMS:      w.stallAfter.Milliseconds(),
		DiagnosticAfterMS: w.diagnosticAfter.Milliseconds(),
	}
	raw, err := json.MarshalIndent(diagnostic, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pipeline watchdog diagnostic: %w", err)
	}
	raw = append(raw, '\n')
	abs := filepath.Join(w.controlRoot, filepath.FromSlash(rel))
	if _, err := os.Stat(abs); err == nil {
		// A previous process may have created the deterministic snapshot before it
		// could persist the state pointer. Adopt it instead of generating another.
		w.state.DiagnosticRelPath = rel
		w.state.DiagnosticEmittedAt = now.Format(time.RFC3339Nano)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect pipeline watchdog diagnostic: %w", err)
	}
	if err := atomicWriteRewriteFile(abs, raw, 0o644); err != nil {
		return fmt.Errorf("write pipeline watchdog diagnostic: %w", err)
	}
	w.state.DiagnosticRelPath = rel
	w.state.DiagnosticEmittedAt = now.Format(time.RFC3339Nano)
	fmt.Fprintf(
		os.Stderr,
		"[pipeline:watchdog] stage=%s chapter=%d 已达 %s 诊断阈值；脱敏快照=%s\n",
		w.state.Stage,
		w.state.Chapter,
		w.diagnosticAfter,
		abs,
	)
	return nil
}

func (w *pipelineWatchdog) persistStateLocked() error {
	if err := validatePipelineWatchdogState(w.state); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(w.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pipeline watchdog state: %w", err)
	}
	raw = append(raw, '\n')
	path := filepath.Join(w.controlRoot, filepath.FromSlash(pipelineWatchdogStatePath))
	if err := atomicWriteRewriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write pipeline watchdog state: %w", err)
	}
	return nil
}

// pipelineWatchdogControlRoot is deliberately outside outputDir. The output
// directory is copied and atomically replaced by render/outline/rebase flows;
// writing heartbeats inside it would mutate DirectoryContentRoot, cause false
// CAS drift, or recreate an archived live root during crash recovery.
func pipelineWatchdogControlRoot(outputDir string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(strings.TrimSpace(outputDir)))
	if err != nil {
		return "", fmt.Errorf("resolve pipeline watchdog output directory: %w", err)
	}
	if strings.TrimSpace(outputDir) == "" {
		return "", fmt.Errorf("pipeline watchdog output directory is empty")
	}
	sum := sha256.Sum256([]byte(abs))
	projectToken := pipelineWatchdogFileToken(filepath.Base(abs))
	return filepath.Join(
		filepath.Dir(abs),
		".pipeline-runtime",
		projectToken+"-"+hex.EncodeToString(sum[:8]),
	), nil
}

func (w *pipelineWatchdog) monotonicNowLocked(now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	for _, value := range []string{w.state.StartedAt, w.state.HeartbeatAt, w.state.LastProgressAt} {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil && now.Before(parsed) {
			now = parsed
		}
	}
	return now
}

// pipelineWatchdogOpaqueToken prevents callers from smuggling story text into
// runtime state or diagnostics. Ordinary identifiers and sha256 digests remain
// readable; anything containing whitespace, non-ASCII prose or excessive text
// is represented only by a one-way digest.
func pipelineWatchdogOpaqueToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 256 && pipelineWatchdogSafeToken.MatchString(value) {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func pipelineWatchdogFileToken(value string) string {
	value = pipelineWatchdogOpaqueToken(value)
	value = strings.TrimPrefix(value, "sha256:")
	value = strings.NewReplacer("/", "-", ":", "-", "@", "-", "+", "-", "=", "-").Replace(value)
	if value == "" {
		return "unknown"
	}
	if len(value) > 96 {
		value = value[:96]
	}
	return value
}
