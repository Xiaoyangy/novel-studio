package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type projectAllCharacterAuthorityOverlay struct {
	CurrentLocation   string
	CurrentStatus     string
	CurrentGoal       string
	CurrentAction     string
	CurrentPressure   string
	Resources         []string
	KnowledgeBoundary string
	DecisionModel     string
	ResourceBoundary  bool
	Sources           []string
}

type projectAllAuthorityInputs struct {
	Context                   *domain.ProjectedPlanningContextV2
	WorkspaceManifest         projectAllAuthorityWorkspaceManifest
	InitialDynamics           map[string]domain.CharacterSimulationState
	Continuity                map[string]projectAllCharacterAuthorityOverlay
	InitialDynamicsSHA256     string
	PriorWorldDeltaSHA256     string
	StateChangesSHA256        string
	Lock                      *domain.PipelineExecutionLock
	Outline                   *domain.OutlineEntry
	InitialDynamicsSeedActive bool
}

type projectAllAuthorityWorkspaceManifest struct {
	Version                string `json:"version"`
	GenerationID           string `json:"generation_id"`
	SourceOutput           string `json:"source_output"`
	BaseChapter            int    `json:"base_chapter"`
	Workspace              string `json:"workspace"`
	IsolatedWrites         bool   `json:"isolated_writes"`
	FoundationSnapshotRoot string `json:"foundation_snapshot_root"`
	RAGSnapshotRoot        string `json:"rag_snapshot_root"`
}

func loadProjectAllAuthorityInputs(st *store.Store, chapter int) (*projectAllAuthorityInputs, error) {
	if st == nil || chapter <= 0 {
		return nil, nil
	}
	context, _, err := loadProjectAllStateForExecution(st, chapter)
	if err != nil || context == nil {
		return nil, err
	}
	if context.ThroughChapter != chapter-1 {
		return nil, fmt.Errorf(
			"project-all authority context through_chapter=%d, want %d",
			context.ThroughChapter,
			chapter-1,
		)
	}
	if rewriteSource, _, _, rewriteErr := loadChapterRewriteSource(st, chapter); rewriteErr != nil {
		return nil, rewriteErr
	} else if rewriteSource != nil {
		return nil, fmt.Errorf("project-all grounded authority is forbidden for rewrite chapters")
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return nil, err
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionProjectAll ||
		lock.TargetChapter != chapter {
		return nil, fmt.Errorf("project-all grounded authority requires the exact active execution lock")
	}
	inputs := &projectAllAuthorityInputs{Context: context, Lock: lock}
	manifestRaw, err := os.ReadFile(filepath.Join(
		st.Dir(),
		"meta",
		"project_all_workspace_manifest.json",
	))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(manifestRaw, &inputs.WorkspaceManifest); err != nil {
		return nil, err
	}
	if inputs.WorkspaceManifest.Version != "project-all-workspace.v3" ||
		inputs.WorkspaceManifest.GenerationID != context.GenerationID ||
		inputs.WorkspaceManifest.BaseChapter < 0 ||
		inputs.WorkspaceManifest.BaseChapter > context.ThroughChapter ||
		strings.TrimSpace(inputs.WorkspaceManifest.SourceOutput) == "" ||
		filepath.Clean(inputs.WorkspaceManifest.Workspace) != filepath.Clean(st.Dir()) ||
		!inputs.WorkspaceManifest.IsolatedWrites ||
		!validSimulationAuthorityDigest(inputs.WorkspaceManifest.FoundationSnapshotRoot) ||
		!validSimulationAuthorityDigest(inputs.WorkspaceManifest.RAGSnapshotRoot) {
		return nil, fmt.Errorf("project-all grounded authority workspace manifest is invalid")
	}
	inputs.Outline, _ = st.Outline.GetChapterOutline(chapter)
	inputs.InitialDynamics, inputs.InitialDynamicsSHA256, err =
		loadInitialCharacterDynamicsWithDigest(st)
	if err != nil {
		return nil, err
	}
	if context.ThroughChapter == 0 {
		inputs.InitialDynamicsSeedActive = true
		return inputs, nil
	}
	inputs.Continuity, inputs.PriorWorldDeltaSHA256, inputs.StateChangesSHA256, err =
		loadProjectAllCharacterContinuity(st, context, inputs.WorkspaceManifest.BaseChapter)
	if err != nil {
		return nil, err
	}
	return inputs, nil
}

func loadInitialCharacterDynamicsWithDigest(
	st *store.Store,
) (map[string]domain.CharacterSimulationState, string, error) {
	result := map[string]domain.CharacterSimulationState{}
	if st == nil {
		return result, "", fmt.Errorf("initial dynamics requires store")
	}
	path := filepath.Join(st.Dir(), "meta", "initial_character_dynamics.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return result, "", err
	}
	var document struct {
		Version    int                               `json:"version"`
		Scope      string                            `json:"scope"`
		Chapter    int                               `json:"chapter"`
		Characters []domain.CharacterSimulationState `json:"characters"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return result, "", err
	}
	if document.Version != 1 || document.Scope != "zero_chapter" ||
		document.Chapter != 1 || len(document.Characters) == 0 {
		return result, "", fmt.Errorf("initial_character_dynamics identity is invalid")
	}
	for _, state := range document.Characters {
		name := strings.TrimSpace(state.Character)
		if name == "" {
			return result, "", fmt.Errorf("initial_character_dynamics contains an empty character")
		}
		if _, duplicate := result[name]; duplicate {
			return result, "", fmt.Errorf("initial_character_dynamics duplicates character %s", name)
		}
		result[name] = state
	}
	return result, simulationAuthorityRawDigest(raw), nil
}

func loadProjectAllCharacterContinuity(
	st *store.Store,
	context *domain.ProjectedPlanningContextV2,
	baseChapter int,
) (map[string]projectAllCharacterAuthorityOverlay, string, string, error) {
	result := map[string]projectAllCharacterAuthorityOverlay{}
	if st == nil || context == nil || context.ThroughChapter <= 0 {
		return result, "", "", fmt.Errorf("project-all continuity requires a prior projected chapter")
	}
	through := context.ThroughChapter
	prior, err := st.LoadChapterWorldDelta(through)
	if err != nil {
		return nil, "", "", err
	}
	if prior == nil || prior.Chapter != through {
		return nil, "", "", fmt.Errorf(
			"project-all continuity missing exact chapter %d world delta for generation %s",
			through,
			context.GenerationID,
		)
	}
	if through >= baseChapter+1 &&
		prior.GenerationID != context.GenerationID {
		return nil, "", "", fmt.Errorf(
			"project-all continuity chapter %d belongs to generation %s, want %s",
			through,
			prior.GenerationID,
			context.GenerationID,
		)
	}
	priorRaw, err := os.ReadFile(filepath.Join(
		st.Dir(), "meta", "chapter_world_deltas", fmt.Sprintf("%03d.json", through),
	))
	if err != nil {
		return nil, "", "", err
	}
	priorDigest := simulationAuthorityRawDigest(priorRaw)
	for _, delta := range prior.CharacterDeltas {
		name := strings.TrimSpace(delta.Character)
		if name == "" {
			continue
		}
		overlay := result[name]
		overlay.CurrentLocation = nonSentinelAuthorityText(delta.Location)
		overlay.CurrentStatus = nonSentinelAuthorityText(delta.Status)
		overlay.CurrentAction = nonSentinelAuthorityText(delta.CurrentAction)
		overlay.KnowledgeBoundary = nonSentinelAuthorityText(delta.KnowledgeBoundary)
		if overlay.CurrentLocation != "" || overlay.CurrentStatus != "" ||
			overlay.CurrentAction != "" || overlay.KnowledgeBoundary != "" {
			overlay.Sources = append(overlay.Sources,
				fmt.Sprintf("meta/chapter_world_deltas/%03d.json:%s", through, name),
			)
			result[name] = overlay
		}
	}

	statePath := filepath.Join(st.Dir(), "meta", "state_changes.json")
	stateRaw, err := os.ReadFile(statePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, "", "", err
		}
		stateRaw = []byte("[]")
	}
	changes, err := st.World.LoadStateChanges()
	if err != nil {
		return nil, "", "", err
	}
	filtered := make([]domain.StateChange, 0, len(changes))
	for _, change := range changes {
		if change.Chapter <= 0 || change.Chapter > through ||
			strings.TrimSpace(change.Entity) == "" ||
			nonSentinelAuthorityText(change.NewValue) == "" {
			continue
		}
		filtered = append(filtered, change)
	}
	stateDigest := simulationAuthorityRawDigest(stateRaw)
	latest := domain.LatestFactValues(filtered)
	latestKeys := make([]string, 0, len(latest))
	for key := range latest {
		latestKeys = append(latestKeys, key)
	}
	sort.Strings(latestKeys)
	for _, key := range latestKeys {
		change := latest[key]
		name := strings.TrimSpace(change.Entity)
		value := nonSentinelAuthorityText(change.NewValue)
		if name == "" || value == "" {
			continue
		}
		overlay := result[name]
		switch strings.TrimSpace(change.Field) {
		case "location":
			overlay.CurrentLocation = value
		case "status", "state":
			overlay.CurrentStatus = value
		case "goal", "current_goal":
			overlay.CurrentGoal = value
		case "action", "current_action", "action_tendency":
			overlay.CurrentAction = value
		case "pressure":
			overlay.CurrentPressure = value
		case "knowledge", "knowledge_boundary":
			overlay.KnowledgeBoundary = value
		case "decision_frame":
			overlay.DecisionModel = value
		case "resource", "resources":
			overlay.Resources = appendAuthorityUnique(overlay.Resources, value)
			overlay.ResourceBoundary = true
		default:
			continue
		}
		overlay.Sources = append(overlay.Sources, "meta/state_changes.json:"+name+":"+change.Field)
		result[name] = overlay
	}

	// The projected context is derived from the durable bundle chain and is the
	// highest-priority current state. Shadow deltas above supply fields (goal,
	// pressure, action) that the compact projected state does not carry.
	for _, fact := range context.CumulativeState {
		if fact.ThroughChapter > through {
			continue
		}
		name := strings.TrimSpace(fact.Subject)
		value := nonSentinelAuthorityText(fact.Value)
		if name == "" || value == "" {
			continue
		}
		overlay := result[name]
		category := strings.TrimSpace(fact.Category)
		field := strings.TrimSpace(fact.Field)
		switch {
		case category == "location" && field == "location":
			overlay.CurrentLocation = value
		case category == "character_state" && (field == "state" || field == "status"):
			overlay.CurrentStatus = value
		case category == "character_state" && (field == "goal" || field == "current_goal"):
			overlay.CurrentGoal = value
		case category == "character_state" && (field == "action" || field == "current_action"):
			overlay.CurrentAction = value
		case category == "character_state" && field == "pressure":
			overlay.CurrentPressure = value
		case category == "knowledge" && (field == "knowledge" || field == "knowledge_boundary"):
			overlay.KnowledgeBoundary = value
		case category == "resource" && (field == "resource" || field == "resources"):
			resource := strings.TrimSpace(fact.Object)
			if resource == "" {
				resource = strings.TrimSpace(fact.StableID)
			}
			overlay.Resources = appendAuthorityUnique(overlay.Resources, resource)
			overlay.ResourceBoundary = true
		default:
			continue
		}
		overlay.Sources = append(overlay.Sources,
			projectAllStateContextPath+":"+context.ContextDigest+":"+fact.StableID,
		)
		result[name] = overlay
	}
	for name, overlay := range result {
		// An exact continuity snapshot also authorizes an explicit empty
		// resource set. It never reintroduces zero-chapter pseudo-resources.
		overlay.ResourceBoundary = true
		overlay.Resources = compactStrings(overlay.Resources)
		overlay.Sources = compactStrings(overlay.Sources)
		result[name] = overlay
	}
	return result, priorDigest, stateDigest, nil
}

func completeProjectAllCharacterContinuity(overlay projectAllCharacterAuthorityOverlay) bool {
	return strings.TrimSpace(overlay.CurrentGoal) != "" &&
		strings.TrimSpace(overlay.CurrentPressure) != "" &&
		strings.TrimSpace(overlay.CurrentAction) != "" &&
		strings.TrimSpace(overlay.KnowledgeBoundary) != "" &&
		strings.TrimSpace(overlay.DecisionModel) != "" &&
		overlay.ResourceBoundary
}

func nonSentinelAuthorityText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || simulationAuthorityDecisionPlaceholder(value) {
		return ""
	}
	return value
}

func simulationAuthorityRawDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return domain.PlanningV2DigestPrefix + hex.EncodeToString(sum[:])
}

func simulationAuthorityObjectDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return simulationAuthorityRawDigest(raw), nil
}

func normalizeAuthorityReceiptCharacters(values []string) []string {
	values = compactStrings(values)
	sort.Strings(values)
	if values == nil {
		return []string{}
	}
	return values
}

func computeSimulationAuthorityReceiptDigest(
	receipt domain.SimulationAuthorityReceipt,
) (string, error) {
	receipt.GroundedCharacters = normalizeAuthorityReceiptCharacters(receipt.GroundedCharacters)
	receipt.HoldBaselineCharacters = normalizeAuthorityReceiptCharacters(receipt.HoldBaselineCharacters)
	receipt.ReceiptDigest = ""
	return simulationAuthorityObjectDigest(receipt)
}

func computeGroundedDecisionRoot(
	decisions []domain.CharacterWorldDecision,
	grounded []string,
) (string, error) {
	groundedSet := make(map[string]bool, len(grounded))
	for _, name := range grounded {
		groundedSet[strings.TrimSpace(name)] = true
	}
	selected := make([]domain.CharacterWorldDecision, 0, len(grounded))
	for _, decision := range decisions {
		if groundedSet[strings.TrimSpace(decision.Character)] {
			selected = append(selected, decision)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		return strings.TrimSpace(selected[i].Character) < strings.TrimSpace(selected[j].Character)
	})
	if len(selected) != len(groundedSet) {
		return "", fmt.Errorf("grounded decision root missing grounded actors")
	}
	return simulationAuthorityObjectDigest(selected)
}

func buildProjectAllAuthorityReceiptBinding(
	st *store.Store,
	chapter int,
	authority []simulationCharacterAuthority,
) (*domain.SimulationAuthorityReceipt, error) {
	var grounded []string
	var hold []string
	var packets []simulationCharacterAuthority
	for _, entry := range authority {
		switch {
		case entry.projectAllGrounded:
			grounded = append(grounded, entry.Character)
			entry.SimulationStatus = "required_missing"
			entry.AuthorityMode = domain.SimulationAuthorityModeGrounded
			entry.Blocking = false
			entry.DecisionPolicy = ""
			entry.HoldBaselineContract = nil
			entry.RewriteSourceEvidence = nil
			entry.RewriteSourceOnlyContract = nil
			packets = append(packets, entry)
		case entry.baseBlocking:
			hold = append(hold, entry.Character)
		}
	}
	grounded = normalizeAuthorityReceiptCharacters(grounded)
	hold = normalizeAuthorityReceiptCharacters(hold)
	if len(grounded) == 0 {
		return nil, nil
	}
	inputs, err := loadProjectAllAuthorityInputs(st, chapter)
	if err != nil || inputs == nil {
		return nil, err
	}
	sort.Slice(packets, func(i, j int) bool {
		return packets[i].Character < packets[j].Character
	})
	foundationRoot := inputs.WorkspaceManifest.FoundationSnapshotRoot
	if !validSimulationAuthorityDigest(foundationRoot) {
		return nil, fmt.Errorf("project-all source snapshot has invalid foundation root")
	}
	inputRoot, err := simulationAuthorityObjectDigest(struct {
		GenerationID           string                               `json:"generation_id"`
		Chapter                int                                  `json:"chapter"`
		ThroughChapter         int                                  `json:"through_chapter"`
		ContextDigest          string                               `json:"context_digest"`
		StateRoot              string                               `json:"state_root"`
		FoundationSnapshotRoot string                               `json:"foundation_snapshot_root"`
		InitialDynamicsSHA256  string                               `json:"initial_dynamics_sha256"`
		PriorWorldDeltaSHA256  string                               `json:"prior_world_delta_sha256"`
		StateChangesSHA256     string                               `json:"state_changes_sha256"`
		WorkspaceIdentity      projectAllAuthorityWorkspaceManifest `json:"workspace_identity"`
		Outline                *domain.OutlineEntry                 `json:"outline"`
		GroundedPackets        []simulationCharacterAuthority       `json:"grounded_packets"`
		HoldCharacters         []string                             `json:"hold_characters"`
	}{
		GenerationID:           inputs.Context.GenerationID,
		Chapter:                chapter,
		ThroughChapter:         inputs.Context.ThroughChapter,
		ContextDigest:          inputs.Context.ContextDigest,
		StateRoot:              inputs.Context.StateRoot,
		FoundationSnapshotRoot: foundationRoot,
		InitialDynamicsSHA256:  inputs.InitialDynamicsSHA256,
		PriorWorldDeltaSHA256:  inputs.PriorWorldDeltaSHA256,
		StateChangesSHA256:     inputs.StateChangesSHA256,
		WorkspaceIdentity:      inputs.WorkspaceManifest,
		Outline:                inputs.Outline,
		GroundedPackets:        packets,
		HoldCharacters:         hold,
	})
	if err != nil {
		return nil, err
	}
	return &domain.SimulationAuthorityReceipt{
		Version:                domain.SimulationAuthorityReceiptVersion,
		Mode:                   domain.SimulationAuthorityModeGrounded,
		GenerationID:           inputs.Context.GenerationID,
		Chapter:                chapter,
		ThroughChapter:         inputs.Context.ThroughChapter,
		PlanningContextDigest:  inputs.Context.ContextDigest,
		ProjectedStateRoot:     inputs.Context.StateRoot,
		FoundationSnapshotRoot: foundationRoot,
		AuthorityInputRoot:     inputRoot,
		InitialDynamicsSHA256:  inputs.InitialDynamicsSHA256,
		PriorWorldDeltaSHA256:  inputs.PriorWorldDeltaSHA256,
		StateChangesSHA256:     inputs.StateChangesSHA256,
		GroundedCharacters:     grounded,
		HoldBaselineCharacters: hold,
		RewriteSourceAbsent:    true,
		LockOwner:              strings.TrimSpace(inputs.Lock.Owner),
		LockProcessID:          inputs.Lock.ProcessID,
		LockAcquiredAt:         inputs.Lock.AcquiredAt.UTC(),
	}, nil
}

func sameSimulationAuthorityBinding(
	left, right *domain.SimulationAuthorityReceipt,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftCopy, rightCopy := *left, *right
	leftCopy.GroundedDecisionRoot = ""
	leftCopy.ContextAccessReceiptDigest = ""
	leftCopy.ReceiptDigest = ""
	rightCopy.GroundedDecisionRoot = ""
	rightCopy.ContextAccessReceiptDigest = ""
	rightCopy.ReceiptDigest = ""
	leftDigest, leftErr := simulationAuthorityObjectDigest(leftCopy)
	rightDigest, rightErr := simulationAuthorityObjectDigest(rightCopy)
	return leftErr == nil && rightErr == nil && leftDigest == rightDigest
}

func sameSimulationAuthorityContinuityBinding(
	left, right *domain.SimulationAuthorityReceipt,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftCopy, rightCopy := *left, *right
	leftCopy.GroundedDecisionRoot = ""
	leftCopy.ContextAccessReceiptDigest = ""
	leftCopy.ReceiptDigest = ""
	leftCopy.LockOwner = ""
	leftCopy.LockProcessID = 0
	leftCopy.LockAcquiredAt = time.Time{}
	rightCopy.GroundedDecisionRoot = ""
	rightCopy.ContextAccessReceiptDigest = ""
	rightCopy.ReceiptDigest = ""
	rightCopy.LockOwner = ""
	rightCopy.LockProcessID = 0
	rightCopy.LockAcquiredAt = time.Time{}
	leftDigest, leftErr := simulationAuthorityObjectDigest(leftCopy)
	rightDigest, rightErr := simulationAuthorityObjectDigest(rightCopy)
	return leftErr == nil && rightErr == nil && leftDigest == rightDigest
}

func prepareSimulationAuthorityBinding(
	st *store.Store,
	simulation *domain.ChapterWorldSimulation,
) error {
	if st == nil || simulation == nil {
		return nil
	}
	authority := buildSimulationCharacterAuthority(st, simulation.Chapter)
	binding, err := buildProjectAllAuthorityReceiptBinding(st, simulation.Chapter, authority)
	if err != nil {
		return err
	}
	if simulation.AuthorityReceipt != nil &&
		!sameSimulationAuthorityBinding(simulation.AuthorityReceipt, binding) {
		if simulation.AuthorityReceipt.ReceiptDigest != "" ||
			!sameSimulationAuthorityContinuityBinding(simulation.AuthorityReceipt, binding) {
			return fmt.Errorf("project-all grounded authority inputs changed during staged simulation")
		}
	}
	simulation.AuthorityReceipt = binding
	return nil
}

func finalizeSimulationAuthorityReceipt(
	st *store.Store,
	simulation *domain.ChapterWorldSimulation,
) error {
	if st == nil || simulation == nil || simulation.AuthorityReceipt == nil {
		return nil
	}
	authority := buildSimulationCharacterAuthority(st, simulation.Chapter)
	current, err := buildProjectAllAuthorityReceiptBinding(st, simulation.Chapter, authority)
	if err != nil {
		return err
	}
	if !sameSimulationAuthorityBinding(simulation.AuthorityReceipt, current) {
		return fmt.Errorf("project-all grounded authority binding drifted before finalize")
	}
	receipt := *current
	receipt.GroundedDecisionRoot, err = computeGroundedDecisionRoot(
		simulation.CharacterDecisions,
		receipt.GroundedCharacters,
	)
	if err != nil {
		return err
	}
	access, err := st.Runtime.LoadPlanningContextAccessReceipt(
		domain.PlanningContextAccessSimulate,
	)
	if err != nil {
		return err
	}
	if access == nil || access.ConsumedAt.IsZero() ||
		access.GenerationID != receipt.GenerationID ||
		access.Chapter != receipt.Chapter ||
		access.PlanningContextDigest != receipt.PlanningContextDigest ||
		access.LockOwner != receipt.LockOwner ||
		access.LockProcessID != receipt.LockProcessID ||
		!access.LockAcquiredAt.Equal(receipt.LockAcquiredAt) {
		return fmt.Errorf("project-all grounded authority lacks the exact consumed simulate context receipt")
	}
	receipt.ContextAccessReceiptDigest = access.ReceiptDigest
	receipt.ReceiptDigest, err = computeSimulationAuthorityReceiptDigest(receipt)
	if err != nil {
		return err
	}
	simulation.AuthorityReceipt = &receipt
	return nil
}

func validateSimulationAuthorityReceiptSchema(
	simulation domain.ChapterWorldSimulation,
) error {
	receipt := simulation.AuthorityReceipt
	if receipt == nil {
		return fmt.Errorf("missing authority receipt")
	}
	if receipt.Version != domain.SimulationAuthorityReceiptVersion ||
		receipt.Mode != domain.SimulationAuthorityModeGrounded ||
		receipt.GenerationID != simulation.GenerationID ||
		receipt.Chapter != simulation.Chapter ||
		receipt.ThroughChapter != simulation.Chapter-1 ||
		!receipt.RewriteSourceAbsent || simulation.RewriteSource != nil ||
		strings.TrimSpace(receipt.LockOwner) == "" ||
		receipt.LockProcessID <= 0 || receipt.LockAcquiredAt.IsZero() ||
		len(receipt.GroundedCharacters) == 0 {
		return fmt.Errorf("authority receipt identity is incomplete")
	}
	for _, digest := range []string{
		receipt.PlanningContextDigest,
		receipt.ProjectedStateRoot,
		receipt.FoundationSnapshotRoot,
		receipt.AuthorityInputRoot,
		receipt.InitialDynamicsSHA256,
		receipt.GroundedDecisionRoot,
		receipt.ContextAccessReceiptDigest,
		receipt.ReceiptDigest,
	} {
		if !validSimulationAuthorityDigest(digest) {
			return fmt.Errorf("authority receipt contains an invalid digest")
		}
	}
	if receipt.ThroughChapter > 0 {
		if !validSimulationAuthorityDigest(receipt.PriorWorldDeltaSHA256) ||
			!validSimulationAuthorityDigest(receipt.StateChangesSHA256) {
			return fmt.Errorf("authority receipt lacks prior continuity digests")
		}
	} else if receipt.PriorWorldDeltaSHA256 != "" ||
		receipt.StateChangesSHA256 != "" {
		return fmt.Errorf("chapter-one authority receipt unexpectedly binds prior continuity")
	}
	if !slicesEqualStrings(
		receipt.GroundedCharacters,
		normalizeAuthorityReceiptCharacters(receipt.GroundedCharacters),
	) || !slicesEqualStrings(
		receipt.HoldBaselineCharacters,
		normalizeAuthorityReceiptCharacters(receipt.HoldBaselineCharacters),
	) {
		return fmt.Errorf("authority receipt actor sets are not sorted and unique")
	}
	groundedSet := make(map[string]bool, len(receipt.GroundedCharacters))
	for _, name := range receipt.GroundedCharacters {
		groundedSet[name] = true
	}
	for _, name := range receipt.HoldBaselineCharacters {
		if groundedSet[name] {
			return fmt.Errorf("authority receipt actor sets overlap")
		}
	}
	token, err := domain.ProjectedPlanningContextSourceTokenV2(
		receipt.PlanningContextDigest,
	)
	if err != nil || !projectAllStateSourcesContain(simulation.Sources, token) {
		return fmt.Errorf("authority receipt lacks exact project-all context source binding")
	}
	decisionRoot, err := computeGroundedDecisionRoot(
		simulation.CharacterDecisions,
		receipt.GroundedCharacters,
	)
	if err != nil || decisionRoot != receipt.GroundedDecisionRoot {
		return fmt.Errorf("authority receipt grounded decision root mismatch")
	}
	want, err := computeSimulationAuthorityReceiptDigest(*receipt)
	if err != nil || want != receipt.ReceiptDigest {
		return fmt.Errorf("authority receipt digest mismatch")
	}
	return nil
}

func validSimulationAuthorityDigest(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, domain.PlanningV2DigestPrefix) {
		return false
	}
	raw := strings.TrimPrefix(value, domain.PlanningV2DigestPrefix)
	if len(raw) != 64 || strings.ToLower(raw) != raw {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil
}

func slicesEqualStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func validateStoredSimulationAuthorityReceipt(
	st *store.Store,
	simulation domain.ChapterWorldSimulation,
) error {
	if strings.TrimSpace(simulation.SimulationID) == "" ||
		simulation.SimulationID != chapterWorldSimulationID(simulation) {
		return fmt.Errorf("grounded simulation_id does not match its final payload")
	}
	if err := validateSimulationAuthorityReceiptSchema(simulation); err != nil {
		return err
	}
	receipt := simulation.AuthorityReceipt
	if lock, err := st.Runtime.LoadPipelineExecution(); err != nil {
		return err
	} else if lock != nil && lock.Mode == domain.PipelineExecutionProjectAll &&
		lock.TargetChapter == simulation.Chapter {
		authority := buildSimulationCharacterAuthority(st, simulation.Chapter)
		current, buildErr := buildProjectAllAuthorityReceiptBinding(
			st,
			simulation.Chapter,
			authority,
		)
		if buildErr != nil || !sameSimulationAuthorityContinuityBinding(receipt, current) {
			return fmt.Errorf("active project-all authority binding no longer matches receipt")
		}
		return validateReceiptActorContracts(simulation)
	}
	bundles, err := st.LoadProjectedChapterBundlesV2(receipt.GenerationID)
	if err != nil {
		return err
	}
	if len(bundles) == 0 {
		var manifest projectAllAuthorityWorkspaceManifest
		raw, readErr := os.ReadFile(filepath.Join(
			st.Dir(),
			"meta",
			"project_all_workspace_manifest.json",
		))
		if readErr == nil && json.Unmarshal(raw, &manifest) == nil &&
			strings.TrimSpace(manifest.SourceOutput) != "" {
			bundles, err = store.NewStore(
				filepath.Clean(manifest.SourceOutput),
			).LoadProjectedChapterBundlesV2(receipt.GenerationID)
			if err != nil {
				return err
			}
		}
	}
	for _, bundle := range bundles {
		if bundle.Chapter != simulation.Chapter {
			continue
		}
		left, leftErr := simulationAuthorityObjectDigest(bundle.ChapterWorldSimulation)
		right, rightErr := simulationAuthorityObjectDigest(simulation)
		if leftErr != nil || rightErr != nil || left != right {
			return fmt.Errorf("stored grounded simulation differs from projected generation bundle")
		}
		return validateReceiptActorContracts(simulation)
	}
	return fmt.Errorf("grounded simulation has no exact projected generation bundle")
}

func validateReceiptActorContracts(simulation domain.ChapterWorldSimulation) error {
	receipt := simulation.AuthorityReceipt
	grounded := make(map[string]bool, len(receipt.GroundedCharacters))
	hold := make(map[string]bool, len(receipt.HoldBaselineCharacters))
	for _, name := range receipt.GroundedCharacters {
		grounded[name] = true
	}
	for _, name := range receipt.HoldBaselineCharacters {
		hold[name] = true
	}
	for _, decision := range simulation.CharacterDecisions {
		name := strings.TrimSpace(decision.Character)
		switch {
		case grounded[name]:
			if err := validateGroundedDecisionSentinelFree(decision); err != nil {
				return fmt.Errorf("%s grounded decision: %w", name, err)
			}
		case hold[name]:
			if err := validateHoldBaselineDecision(
				simulation.Chapter,
				decision,
			); err != nil {
				return fmt.Errorf("%s hold-baseline decision: %w", name, err)
			}
		default:
			if simulationAuthorityDecisionPlaceholder(decision.Decision) {
				return fmt.Errorf("%s has an unreceipted authority sentinel", name)
			}
		}
	}
	for _, name := range receipt.HoldBaselineCharacters {
		found := false
		for _, decision := range simulation.CharacterDecisions {
			if strings.TrimSpace(decision.Character) == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s hold-baseline receipt actor is missing", name)
		}
	}
	return nil
}

func validateGroundedDecisionSentinelFree(
	decision domain.CharacterWorldDecision,
) error {
	var values []string
	values = append(values,
		decision.Location,
		decision.CurrentGoal,
		decision.Pressure,
		decision.KnowledgeBoundary,
		decision.Decision,
		decision.DecisionReason,
		decision.Action,
		decision.ActionDuration,
		decision.ImmediateResult,
		decision.StateAfter,
	)
	values = append(values, decision.Resources...)
	values = append(values, decision.AvailableOptions...)
	for _, effect := range decision.ButterflyEffects {
		values = append(values,
			effect.Effect,
			effect.TransmissionPath,
			effect.ProtagonistImpact,
		)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" ||
			simulationAuthorityDecisionPlaceholder(value) {
			return fmt.Errorf("contains empty or authority-sentinel text")
		}
	}
	if !containsExactString(decision.AvailableOptions, decision.Decision) {
		return fmt.Errorf("decision is not one of available_options")
	}
	return nil
}

func validateProjectAllGroundedDecision(
	st *store.Store,
	chapter int,
	entry simulationCharacterAuthority,
	decision domain.CharacterWorldDecision,
) error {
	if err := validateGroundedDecisionSentinelFree(decision); err != nil {
		return err
	}
	var violations []string
	if strings.TrimSpace(decision.CurrentGoal) != strings.TrimSpace(entry.CurrentGoal) {
		violations = append(violations, "current_goal must equal grounded authority")
	}
	switch entry.CurrentPressurePolicy {
	case "exact_continuity":
		if strings.TrimSpace(decision.Pressure) != strings.TrimSpace(entry.CurrentPressure) {
			violations = append(violations, "pressure must equal projected continuity authority")
		}
	case "outline_authorized_concise":
		if !projectAllGroundedPressureAuthorized(
			st,
			chapter,
			entry.CurrentPressure,
			decision.Pressure,
		) {
			violations = append(violations, "pressure must be a concise current-outline-authorized pressure")
		}
	default:
		violations = append(violations, "grounded authority has no pressure policy")
	}
	resourcesAuthorized := sameAuthorityStringSet(decision.Resources, entry.Resources)
	if !resourcesAuthorized {
		violations = append(violations, "resources must equal the grounded authority set")
	}
	expectedBoundary := mergedAuthorityKnowledgeBoundary(
		strings.TrimSpace(entry.KnowledgeBoundary),
		entry.RequiredKnowledgeBoundary,
	)
	if rewriteFactIdentity(decision.KnowledgeBoundary) !=
		rewriteFactIdentity(expectedBoundary) {
		violations = append(violations, "knowledge_boundary must exactly equal grounded authority plus required locks")
	}
	if !projectAllGroundedLocationAuthorized(st, chapter, entry, decision.Location) {
		violations = append(violations, "location is not an exact compact anchor from prior state, current outline, or book world")
	}
	if strings.TrimSpace(decision.Decision) == strings.TrimSpace(decision.CurrentGoal) ||
		strings.TrimSpace(decision.Action) == strings.TrimSpace(decision.CurrentGoal) {
		violations = append(violations, "decision/action must be a concrete current move, not a copy of current_goal")
	}
	decisionAuthorized := projectAllGroundedActionAuthorized(st, chapter, entry, decision.Decision)
	actionAuthorized := projectAllGroundedActionAuthorized(st, chapter, entry, decision.Action)
	if !decisionAuthorized || !actionAuthorized {
		violations = append(violations, "decision/action is not an exact grounded input; copy a concrete substring from current_action or the current chapter outline")
	}
	// Downstream projected-output checks may only derive authority from fields
	// that already passed their exact-input guards. Otherwise an invented action
	// could smuggle the same invented target into the temporary corpus and hide a
	// second actionable error until the next model turn.
	groundedCorpusDecision := decision
	if !decisionAuthorized {
		groundedCorpusDecision.Decision = ""
	}
	if !actionAuthorized {
		groundedCorpusDecision.Action = ""
	}
	if !resourcesAuthorized {
		groundedCorpusDecision.Resources = append([]string(nil), entry.Resources...)
	}
	if err := validateProjectAllGroundedProjectedOutput(
		st,
		chapter,
		entry,
		groundedCorpusDecision,
		"decision_reason",
		decision.DecisionReason,
	); err != nil {
		violations = append(violations, err.Error())
	}
	for i, option := range decision.AvailableOptions {
		if option == decision.Decision {
			continue
		}
		if err := validateProjectAllGroundedProjectedOutput(
			st,
			chapter,
			entry,
			groundedCorpusDecision,
			fmt.Sprintf("available_options[%d]", i),
			option,
		); err != nil {
			violations = append(violations, err.Error())
		}
	}
	for _, output := range []struct {
		path string
		text string
	}{
		{path: "immediate_result", text: decision.ImmediateResult},
		{path: "state_after", text: decision.StateAfter},
	} {
		if err := validateProjectAllGroundedProjectedOutput(
			st,
			chapter,
			entry,
			groundedCorpusDecision,
			output.path,
			output.text,
		); err != nil {
			violations = append(violations, err.Error())
		}
	}
	for i, effect := range decision.ButterflyEffects {
		if err := validateProjectAllGroundedProjectedOutput(
			st,
			chapter,
			entry,
			groundedCorpusDecision,
			fmt.Sprintf("butterfly_effects[%d].effect", i),
			effect.Effect,
		); err != nil {
			violations = append(violations, err.Error())
		}
		if err := validateProjectAllGroundedProjectedOutput(
			st,
			chapter,
			entry,
			groundedCorpusDecision,
			fmt.Sprintf("butterfly_effects[%d].protagonist_impact", i),
			effect.ProtagonistImpact,
		); err != nil {
			violations = append(violations, err.Error())
		}
		for _, target := range effect.Targets {
			if !projectAllGroundedOutputTargetAuthorized(
				st,
				chapter,
				entry,
				groundedCorpusDecision,
				target,
			) {
				violations = append(violations, fmt.Sprintf(
					"butterfly_effects[%d].targets contains unauthorized target %q",
					i,
					target,
				))
			}
		}
	}
	if len(violations) > 0 {
		return fmt.Errorf("%s", strings.Join(compactStrings(violations), "; "))
	}
	return nil
}

var (
	projectAllGroundedNumberPattern = regexp.MustCompile(
		`[0-9０-９一二三四五六七八九十百千万亿两]+(?:元|万元|亿元|笔|家|人|辆|份|天|小时|分钟|%)`,
	)
	projectAllGroundedTitledEntityPattern = regexp.MustCompile(
		`[\p{Han}]{1,6}(?:总|老板|主任|局长|经理|医生|警官|律师|老师|记者|负责人)`,
	)
	projectAllGroundedOrganizationPattern = regexp.MustCompile(
		`[\p{Han}]{2,12}(?:公司|集团|组织|协会|帮派|委员会|研究所|实验室|中心|部门|团队|工会)`,
	)
)

var projectAllGroundedNoveltyTerms = []string{
	"地下实验室", "实验室", "地下室", "密室", "暗门", "密码", "密钥",
	"秘密", "真相", "身世", "血缘", "幕后", "内鬼", "叛徒", "阴谋",
	"监控", "录音", "录像", "芯片", "枪", "炸弹", "绑架", "失踪", "死亡",
	"直升机", "飞机", "合同", "协议", "股权", "股份", "账户", "银行卡",
	"贷款", "债务", "房产", "土地", "专利", "钥匙", "仓库", "工厂",
	"医院", "警局", "酒店", "机场", "码头", "山洞", "新身份", "陌生人",
	"投资人", "线索", "系统真相", "后台规则",
	"时空", "裂缝", "异能", "超能力", "法术", "魔法", "鬼", "怪物",
	"病毒", "丧尸", "穿越", "重生", "预言", "天灾", "爆炸", "火灾",
	"车祸", "谋杀", "尸体", "血迹", "黑客", "加密",
}

var projectAllGroundedAnchorStop = map[string]bool{
	"本章": true, "当前": true, "行动": true, "结果": true, "状态": true,
	"角色": true, "选择": true, "现场": true, "继续": true, "完成": true,
	"影响": true, "主角": true, "信息": true, "资源": true, "关系": true,
	"压力": true, "已经": true, "随后": true, "因此": true, "形成": true,
	"留下": true, "下一": true, "一步": true, "没有": true, "仍然": true,
	"开始": true, "结束": true, "决定": true, "进行": true, "推进": true,
}

func validateProjectAllGroundedProjectedOutput(
	st *store.Store,
	chapter int,
	entry simulationCharacterAuthority,
	decision domain.CharacterWorldDecision,
	path string,
	text string,
) error {
	text = strings.TrimSpace(text)
	if text == "" || simulationAuthorityDecisionPlaceholder(text) {
		return fmt.Errorf("%s is empty or an authority sentinel", path)
	}
	if len([]rune(text)) > 240 {
		return fmt.Errorf("%s exceeds the grounded projected-output budget", path)
	}
	corpus := projectAllGroundedOutputAuthorityCorpus(
		st,
		chapter,
		entry,
		decision,
	)
	if !projectAllGroundedOutputHasCausalAnchor(corpus, text) {
		return fmt.Errorf("%s has no grounded causal anchor", path)
	}
	if novelty := projectAllGroundedUnauthorizedNovelty(corpus, text); novelty != "" {
		return fmt.Errorf("%s introduces unauthorized novelty %q", path, novelty)
	}
	return nil
}

func projectAllGroundedOutputAuthorityCorpus(
	st *store.Store,
	chapter int,
	entry simulationCharacterAuthority,
	decision domain.CharacterWorldDecision,
) string {
	var authority strings.Builder
	for _, value := range []string{
		entry.Character,
		entry.Role,
		entry.Description,
		entry.CurrentLocation,
		entry.CurrentStatus,
		entry.CurrentGoal,
		entry.CurrentAction,
		entry.CurrentPressure,
		entry.KnowledgeBoundary,
		entry.DecisionModel,
		decision.Decision,
		decision.Action,
	} {
		if strings.TrimSpace(value) != "" {
			authority.WriteString(value)
			authority.WriteString("\n")
		}
	}
	for _, values := range [][]string{
		entry.Aliases,
		entry.Resources,
		entry.Relationships,
		entry.RequiredKnowledgeBoundary,
		decision.Resources,
	} {
		authority.WriteString(strings.Join(values, "\n"))
		authority.WriteString("\n")
	}
	if outline, err := st.Outline.GetChapterOutline(chapter); err == nil && outline != nil {
		authority.WriteString(outline.Title)
		authority.WriteString("\n")
		authority.WriteString(outline.CoreEvent)
		authority.WriteString("\n")
		authority.WriteString(outline.Hook)
		authority.WriteString("\n")
		authority.WriteString(strings.Join(outline.Scenes, "\n"))
	}
	if characters, err := st.Characters.Load(); err == nil {
		for _, character := range characters {
			authority.WriteString(character.Name)
			authority.WriteString("\n")
			authority.WriteString(strings.Join(character.Aliases, "\n"))
			authority.WriteString("\n")
		}
	}
	if world, err := st.World.LoadBookWorld(); err == nil && world != nil {
		if raw, marshalErr := json.Marshal(world); marshalErr == nil {
			authority.Write(raw)
		}
	}
	if rules, err := st.World.LoadWorldRules(); err == nil {
		if raw, marshalErr := json.Marshal(rules); marshalErr == nil {
			authority.Write(raw)
		}
	}
	return authority.String()
}

func projectAllGroundedOutputHasCausalAnchor(corpus, text string) bool {
	corpus = projectAllGroundedAnchorText(corpus)
	candidate := projectAllGroundedAnchorText(text)
	runes := []rune(candidate)
	if len(runes) < 2 {
		return false
	}
	anchors := map[string]bool{}
	for size := 2; size <= 4; size++ {
		for start := 0; start+size <= len(runes); start++ {
			anchor := string(runes[start : start+size])
			if projectAllGroundedAnchorStop[anchor] ||
				len([]rune(strings.TrimSpace(anchor))) < size ||
				!strings.Contains(corpus, anchor) {
				continue
			}
			anchors[anchor] = true
		}
	}
	required := 2
	if len(runes) <= 6 {
		required = 1
	}
	return len(anchors) >= required
}

func projectAllGroundedAnchorText(text string) string {
	var out strings.Builder
	for _, r := range strings.TrimSpace(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func projectAllGroundedUnauthorizedNovelty(corpus, text string) string {
	for _, term := range projectAllGroundedNoveltyTerms {
		if strings.Contains(text, term) && !strings.Contains(corpus, term) {
			return term
		}
	}
	for _, quantity := range projectAllGroundedNumberPattern.FindAllString(text, -1) {
		if !strings.Contains(corpus, quantity) {
			return quantity
		}
	}
	for _, titled := range projectAllGroundedTitledEntityPattern.FindAllString(text, -1) {
		if !strings.Contains(corpus, titled) {
			return titled
		}
	}
	for _, organization := range projectAllGroundedOrganizationPattern.FindAllString(text, -1) {
		if !strings.Contains(corpus, organization) {
			return organization
		}
	}
	return ""
}

func projectAllGroundedOutputTargetAuthorized(
	st *store.Store,
	chapter int,
	entry simulationCharacterAuthority,
	decision domain.CharacterWorldDecision,
	target string,
) bool {
	target = strings.TrimSpace(target)
	if target == "" || simulationAuthorityDecisionPlaceholder(target) {
		return false
	}
	corpus := projectAllGroundedOutputAuthorityCorpus(
		st,
		chapter,
		entry,
		decision,
	)
	return len([]rune(target)) <= 80 && strings.Contains(corpus, target)
}

func projectAllGroundedProjectionGaps(
	st *store.Store,
	simulation domain.ChapterWorldSimulation,
) []string {
	receipt := simulation.AuthorityReceipt
	protagonist := strings.TrimSpace(simulation.ProtagonistProjection.Protagonist)
	if receipt == nil ||
		!simulationAuthorityReceiptGroundsCharacter(receipt, protagonist) {
		return nil
	}
	var protagonistDecision *domain.CharacterWorldDecision
	entry := simulationCharacterAuthority{Character: protagonist}
	for i := range simulation.CharacterDecisions {
		decision := &simulation.CharacterDecisions[i]
		if strings.TrimSpace(decision.Character) == protagonist {
			protagonistDecision = decision
			entry.CurrentLocation = decision.Location
			entry.CurrentStatus = decision.StateAfter
			entry.CurrentGoal = decision.CurrentGoal
			entry.CurrentAction = decision.Action
			entry.CurrentPressure = decision.Pressure
			entry.Resources = append([]string(nil), decision.Resources...)
			entry.KnowledgeBoundary = decision.KnowledgeBoundary
			entry.DecisionModel = decision.DecisionReason
		}
		for _, value := range []string{
			nonSentinelAuthorityText(decision.Decision),
			nonSentinelAuthorityText(decision.Action),
			nonSentinelAuthorityText(decision.ImmediateResult),
			nonSentinelAuthorityText(decision.StateAfter),
		} {
			if value != "" {
				entry.Relationships = append(entry.Relationships, value)
			}
		}
		for _, effect := range decision.ButterflyEffects {
			for _, value := range []string{
				nonSentinelAuthorityText(effect.Effect),
				nonSentinelAuthorityText(effect.ProtagonistImpact),
			} {
				if value != "" {
					entry.Relationships = append(entry.Relationships, value)
				}
			}
		}
	}
	if protagonistDecision == nil {
		return []string{"grounded protagonist projection has no character decision"}
	}
	projection := simulation.ProtagonistProjection
	var gaps []string
	if !containsExactString(projection.AvailableOptions, protagonistDecision.Decision) {
		gaps = append(gaps, "grounded protagonist projection available_options omit chosen decision")
	}
	for i, option := range projection.AvailableOptions {
		if err := validateProjectAllGroundedProjectedOutput(
			st,
			simulation.Chapter,
			entry,
			*protagonistDecision,
			fmt.Sprintf("protagonist_projection.available_options[%d]", i),
			option,
		); err != nil {
			gaps = append(gaps, err.Error())
		}
	}
	if err := validateProjectAllGroundedProjectedOutput(
		st,
		simulation.Chapter,
		entry,
		*protagonistDecision,
		"protagonist_projection.decision_reason",
		projection.DecisionReason,
	); err != nil {
		gaps = append(gaps, err.Error())
	}
	for _, group := range []struct {
		name   string
		values []string
	}{
		{name: "observable_effects", values: projection.ObservableEffects},
		{name: "hidden_pressures", values: projection.HiddenPressures},
		{name: "causal_chain", values: projection.CausalChain},
	} {
		for i, value := range group.values {
			if err := validateProjectAllGroundedProjectedOutput(
				st,
				simulation.Chapter,
				entry,
				*protagonistDecision,
				fmt.Sprintf("protagonist_projection.%s[%d]", group.name, i),
				value,
			); err != nil {
				gaps = append(gaps, err.Error())
			}
		}
	}
	for i, constraint := range projection.PlanConstraints {
		constraint = strings.TrimSpace(constraint)
		if constraint == "" || len([]rune(constraint)) > 240 ||
			simulationAuthorityDecisionPlaceholder(constraint) {
			gaps = append(gaps, fmt.Sprintf(
				"protagonist_projection.plan_constraints[%d] is invalid",
				i,
			))
		}
	}
	return compactStrings(gaps)
}

func projectAllGroundedActionAuthorized(
	st *store.Store,
	chapter int,
	entry simulationCharacterAuthority,
	action string,
) bool {
	action = strings.TrimSpace(action)
	if action == "" || simulationAuthorityDecisionPlaceholder(action) {
		return false
	}
	var authority strings.Builder
	authority.WriteString(entry.CurrentAction)
	authority.WriteString("\n")
	authority.WriteString(entry.CurrentGoal)
	if outline, err := st.Outline.GetChapterOutline(chapter); err == nil && outline != nil {
		authority.WriteString("\n")
		authority.WriteString(outline.CoreEvent)
		authority.WriteString("\n")
		authority.WriteString(outline.Hook)
		authority.WriteString("\n")
		authority.WriteString(strings.Join(outline.Scenes, "\n"))
	}
	return len([]rune(action)) <= 160 && strings.Contains(authority.String(), action)
}

func projectAllGroundedPressureAuthorized(
	st *store.Store,
	chapter int,
	seedPressure string,
	pressure string,
) bool {
	pressure = strings.TrimSpace(pressure)
	if pressure == "" || simulationAuthorityDecisionPlaceholder(pressure) {
		return false
	}
	var authority strings.Builder
	authority.WriteString(seedPressure)
	if outline, err := st.Outline.GetChapterOutline(chapter); err == nil && outline != nil {
		authority.WriteString("\n")
		authority.WriteString(outline.Title)
		authority.WriteString("\n")
		authority.WriteString(outline.CoreEvent)
		authority.WriteString("\n")
		authority.WriteString(outline.Hook)
		authority.WriteString("\n")
		authority.WriteString(strings.Join(outline.Scenes, "\n"))
	}
	return len([]rune(pressure)) <= 160 &&
		strings.Contains(authority.String(), pressure)
}

func sameAuthorityStringSet(left, right []string) bool {
	left = compactStrings(left)
	right = compactStrings(right)
	sort.Strings(left)
	sort.Strings(right)
	return slicesEqualStrings(left, right)
}

func projectAllGroundedLocationAuthorized(
	st *store.Store,
	chapter int,
	entry simulationCharacterAuthority,
	location string,
) bool {
	location = strings.TrimSpace(location)
	if location == "" || simulationAuthorityDecisionPlaceholder(location) {
		return false
	}
	// A location is a compact spatial anchor, not a copied scene synopsis. Long
	// event sentences used to pass because they were literal substrings of the
	// outline, then leaked planning prose into every character record.
	if len([]rune(location)) > 32 || strings.ContainsAny(location, "，。；！？\n\r") {
		return false
	}
	if current := nonSentinelAuthorityText(entry.CurrentLocation); current != "" &&
		current == location {
		return true
	}
	var authorityText strings.Builder
	if outline, err := st.Outline.GetChapterOutline(chapter); err == nil && outline != nil {
		authorityText.WriteString(outline.Title)
		authorityText.WriteString("\n")
		authorityText.WriteString(outline.CoreEvent)
		authorityText.WriteString("\n")
		authorityText.WriteString(outline.Hook)
		authorityText.WriteString("\n")
		authorityText.WriteString(strings.Join(outline.Scenes, "\n"))
	}
	if world, err := st.World.LoadBookWorld(); err == nil && world != nil {
		for _, place := range world.Places {
			for _, value := range []string{place.Name, place.Description} {
				if strings.TrimSpace(value) != "" {
					authorityText.WriteString("\n")
					authorityText.WriteString(value)
				}
			}
		}
	}
	text := authorityText.String()
	return len([]rune(location)) <= 80 && strings.Contains(text, location)
}

func containsExactString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}
