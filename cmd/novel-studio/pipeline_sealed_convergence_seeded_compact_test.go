package main

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

type seededCompactRecordingPlanDetailsTool struct {
	schema map[string]any
	calls  int
	args   []json.RawMessage
}

func (t *seededCompactRecordingPlanDetailsTool) Name() string { return "plan_details" }
func (t *seededCompactRecordingPlanDetailsTool) Description() string {
	return "recording plan_details"
}
func (t *seededCompactRecordingPlanDetailsTool) Schema() map[string]any { return t.schema }
func (t *seededCompactRecordingPlanDetailsTool) Execute(
	_ context.Context,
	args json.RawMessage,
) (json.RawMessage, error) {
	t.calls++
	t.args = append(t.args, append(json.RawMessage(nil), args...))
	return json.RawMessage(`{"delegated":true}`), nil
}

func TestSealedConvergenceSeededCompactAllowlistRejectsBeforeDelegate(t *testing.T) {
	inner := &seededCompactRecordingPlanDetailsTool{
		schema: tools.NewPlanDetailsTool(store.NewStore(t.TempDir())).Schema(),
	}
	wrapped := newPipelineSealedConvergenceMutablePlanDetailsTool(
		inner,
		5,
		pipelineSealedConvergenceSeededMutableKeys,
	)

	invalid := []struct {
		name string
		args string
	}{
		{
			name: "context_sources is Host seeded",
			args: `{"chapter":5,"causal_simulation":{"context_sources":["planning-context-access:v1:secret"]},"finalize":true}`,
		},
		{
			name: "world simulation identity is Host seeded",
			args: `{"chapter":5,"causal_simulation":{"world_simulation_id":"sim-forged"},"finalize":true}`,
		},
		{
			name: "initial state is Host seeded",
			args: `{"chapter":5,"causal_simulation":{"initial_state":[{"character":"程野"}]},"finalize":true}`,
		},
		{
			name: "unknown top-level seed envelope",
			args: `{"chapter":5,"causal_simulation":{"decision_points":["选择"]},"seeded":true,"finalize":true}`,
		},
		{
			name: "wrong chapter",
			args: `{"chapter":4,"causal_simulation":{"decision_points":["选择"]},"finalize":true}`,
		},
		{
			name: "finalize false",
			args: `{"chapter":5,"causal_simulation":{"decision_points":["选择"]},"finalize":false}`,
		},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			before := inner.calls
			if _, err := wrapped.Execute(context.Background(), json.RawMessage(tc.args)); err == nil {
				t.Fatal("invalid seeded compact request passed its Host allowlist")
			}
			if inner.calls != before {
				t.Fatalf("invalid request reached delegate: calls=%d want=%d", inner.calls, before)
			}
		})
	}

	valid := json.RawMessage(`{"chapter":5,"causal_simulation":{"decision_points":["程野选择当面叫破身份"]},"finalize":true}`)
	result, err := wrapped.Execute(context.Background(), valid)
	if err != nil {
		t.Fatalf("allowlisted exact finalize was rejected: %v", err)
	}
	if string(result) != `{"delegated":true}` || inner.calls != 1 ||
		len(inner.args) != 1 || string(inner.args[0]) != string(valid) {
		t.Fatalf("valid request was not delegated exactly once: result=%s calls=%d args=%q", result, inner.calls, inner.args)
	}
}

func TestSealedConvergenceSeededCompactSchemaExposesOnlyMutableFields(t *testing.T) {
	inner := &seededCompactRecordingPlanDetailsTool{
		schema: tools.NewPlanDetailsTool(store.NewStore(t.TempDir())).Schema(),
	}
	wrapped := newPipelineSealedConvergenceMutablePlanDetailsTool(
		inner,
		5,
		pipelineSealedConvergenceSeededMutableKeys,
	)
	schema := wrapped.Schema()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("wrapper schema has no object properties: %#v", schema)
	}
	gotTop := sortedSeededCompactTestMapKeys(properties)
	wantTop := []string{"causal_simulation", "chapter", "finalize"}
	if !reflect.DeepEqual(gotTop, wantTop) {
		t.Fatalf("top-level schema widened: got=%v want=%v", gotTop, wantTop)
	}
	causal, ok := properties["causal_simulation"].(map[string]any)
	if !ok {
		t.Fatalf("wrapper schema has no causal_simulation object: %#v", properties["causal_simulation"])
	}
	causalProperties, ok := causal["properties"].(map[string]any)
	if !ok {
		t.Fatalf("wrapper schema has no causal_simulation properties: %#v", causal)
	}
	gotMutable := sortedSeededCompactTestMapKeys(causalProperties)
	wantMutable := append([]string(nil), pipelineSealedConvergenceSeededMutableKeys...)
	sort.Strings(wantMutable)
	if !reflect.DeepEqual(gotMutable, wantMutable) {
		t.Fatalf("causal schema is not the exact mutable allowlist: got=%v want=%v", gotMutable, wantMutable)
	}
	for _, forbidden := range []string{"context_sources", "initial_state", "world_simulation_id", "protagonist_decision"} {
		if _, present := causalProperties[forbidden]; present {
			t.Fatalf("Host-seeded field %q leaked into delegate schema", forbidden)
		}
	}
	if required, ok := causal["required"].([]any); ok {
		allowed := make(map[string]struct{}, len(wantMutable))
		for _, key := range wantMutable {
			allowed[key] = struct{}{}
		}
		for _, value := range required {
			key, _ := value.(string)
			if _, present := allowed[key]; !present {
				t.Fatalf("non-mutable field %q remained schema-required", key)
			}
		}
	}
}

func TestSealedConvergenceSeededCompactPromptOmitsAccessTokenAndOldProse(t *testing.T) {
	accessToken := domain.PlanningContextAccessTokenPrefix + strings.Repeat("e", 64)
	oldProseMarker := "旧正文绝密标记-程野在废弃仓库重复解释身份。"
	oldProse := strings.Repeat(oldProseMarker, 3000)
	partialRaw, err := json.Marshal(map[string]any{
		"structure": map[string]any{
			"chapter":  5,
			"title":    "叫名",
			"goal":     "让排伪命中转成当面叫名",
			"conflict": "程野仍有退路",
			"hook":     "叫名后必须承担策略后果",
			"notes":    oldProse,
		},
		"causal_simulation": map[string]any{
			"context_sources": []string{accessToken},
			"old_body":        oldProse,
		},
		"old_prose": oldProse,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(partialRaw), accessToken) || !strings.Contains(string(partialRaw), oldProseMarker) {
		t.Fatal("test fixture did not contain the sensitive source material")
	}

	intent := pipelineSealedConvergenceReplanIntent{
		SourceFrozen: pipelineFrozenPlan{Chapter: 5},
		Diagnostics: pipelineSealedConvergenceDiagnostics{
			BlockingDimensions: []string{"因果收口"},
			IssueClasses:       []string{"late_reveal"},
			MechanicalRules:    []string{"叫名不得落在末场"},
			RevisionFocus:      []string{"排伪、命中、叫名、后果"},
			EvidenceSources:    []string{"sealed_review"},
		},
		ChapterWordBounds: tools.SealedShortChapterWordBounds{
			Active: true, Chapter: 5, Min: 2444, Max: 2600,
			ChapterMin: 2400, ChapterMax: 2800, TargetWords: 12500, TargetChapters: 5,
		},
	}
	eligibility := &pipelineSealedConvergenceReplanEligibility{
		Plan: domain.ChapterPlan{CausalSimulation: domain.ChapterCausalSimulation{
			ArcTransition: domain.ArcChapterTransitionContract{
				ConsumedByCause: "程野先排除伪身份，证据命中后当面叫出贺铎",
			},
		}},
		Simulation: domain.ChapterWorldSimulation{
			ProtagonistProjection: domain.ProtagonistDecisionProjection{
				Protagonist:       "程野",
				ObservableEffects: []string{"证据命中"},
				AvailableOptions:  []string{"继续装作不知", "当面叫名"},
				ChosenDecision:    "当面叫出贺铎",
				DecisionReason:    "继续回避会把主动权让给对方",
				PlanConstraints:   []string{"叫名后保留独立后果场"},
				CausalChain:       []string{"排伪", "命中", "叫名", "策略后果"},
			},
		},
	}
	recovery := &pipelineSealedConvergenceSeededCompactFinalize{
		AllowedMutableKeys: append([]string(nil), pipelineSealedConvergenceSeededMutableKeys...),
		SeedPartialSHA256:  "sha256:" + strings.Repeat("a", 64),
	}
	prompt, err := pipelineSealedConvergenceBuildSeededCompactPrompt(partialRaw, intent, eligibility, recovery)
	if err != nil {
		t.Fatal(err)
	}
	if runes := utf8.RuneCountInString(prompt); runes >= 20000 {
		t.Fatalf("compact prompt is not below 20k runes: %d", runes)
	}
	for _, forbidden := range []string{accessToken, domain.PlanningContextAccessTokenPrefix, oldProseMarker} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("compact prompt leaked sensitive staged material %q", forbidden)
		}
	}
	for _, required := range []string{"2444", "2600", "当面叫出贺铎", "required_exact_consumed_by_cause"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("compact prompt dropped required authority %q", required)
		}
	}
}

func TestSealedConvergenceSeededCompactJournalFailsClosedOnImpossibleStates(t *testing.T) {
	base := seededCompactValidJournalFixture(t)
	attachValidSeededCompactJournal(t, &base)
	if !validPipelineSealedConvergencePlannerJournal(base) {
		t.Fatal("valid fully-bound seeded compact journal fixture was rejected")
	}

	tests := []struct {
		name   string
		mutate func(*pipelineSealedConvergenceReplanIntent)
	}{
		{
			name: "binary-zero proof records assistant side effect",
			mutate: func(intent *pipelineSealedConvergenceReplanIntent) {
				intent.PlannerContinuation.Replacement.BinaryFailover.ZeroSideEffectOutcome.AssistantRows = 1
			},
		},
		{
			name: "seeded state exists without binary-zero proof",
			mutate: func(intent *pipelineSealedConvergenceReplanIntent) {
				intent.PlannerContinuation.Replacement.BinaryFailover.ZeroSideEffectOutcome = nil
			},
		},
		{
			name: "seeded allowlist widens to context_sources",
			mutate: func(intent *pipelineSealedConvergenceReplanIntent) {
				recovery := intent.PlannerContinuation.Replacement.BinaryFailover.SeededCompactFinalize
				recovery.AllowedMutableKeys = append(recovery.AllowedMutableKeys, "context_sources")
			},
		},
		{
			name: "model dispatch exists before seeded partial",
			mutate: func(intent *pipelineSealedConvergenceReplanIntent) {
				recovery := intent.PlannerContinuation.Replacement.BinaryFailover.SeededCompactFinalize
				recovery.SeedPartialSHA256 = ""
				recovery.SeededAt = ""
			},
		},
		{
			name: "zero seed dispatch retains successor state",
			mutate: func(intent *pipelineSealedConvergenceReplanIntent) {
				intent.PlannerContinuation.Replacement.BinaryFailover.SeededCompactFinalize.SeedToolDispatches = 0
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			intent := cloneSeededCompactTestIntent(t, base)
			tc.mutate(&intent)
			intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
			if validPipelineSealedConvergencePlannerJournal(intent) {
				t.Fatal("impossible binary-zero/seeded journal state passed fail-closed validation")
			}
		})
	}
}

func seededCompactValidJournalFixture(t *testing.T) pipelineSealedConvergenceReplanIntent {
	t.Helper()
	st, intent, partialSHA, _ := sealedConvergenceBinaryFailoverFixture(t)
	replacement := intent.PlannerContinuation.Replacement
	replacementOutcome, err := pipelineSealedConvergenceCaptureReplacementZeroSideEffect(st, intent, partialSHA)
	if err != nil {
		t.Fatal(err)
	}
	replacement.ReplacementZeroSideEffect = replacementOutcome
	base, err := time.Parse(time.RFC3339Nano, replacementOutcome.RecordedAt)
	if err != nil {
		t.Fatal(err)
	}
	failoverPromptSHA := seededCompactTestDigest("6")
	failoverAccessSHA := seededCompactTestDigest("7")
	failover := &pipelineSealedConvergenceBinaryFailoverDispatch{
		Version:                       pipelineSealedConvergenceBinaryFailoverVersion,
		SessionIdentity:               "convergence_planner_continuation_binary_failover-ch05",
		Dispatches:                    1,
		ReservedAt:                    base.Add(time.Nanosecond).Format(time.RFC3339Nano),
		BinaryPath:                    "/opt/novel-studio/codex-cli",
		BinaryFileSHA256:              seededCompactTestDigest("5"),
		BinaryVersionOutput:           "codex-cli fixture-v1",
		BinaryHealthProbeSucceeded:    true,
		BinaryHealthProbeTimeoutMS:    int(pipelineSealedConvergenceBinaryProbeTimeout / time.Millisecond),
		BinaryHealthProbedAt:          base.Add(2 * time.Nanosecond).Format(time.RFC3339Nano),
		PrefetchLockOwner:             "released-binary-lock",
		PrefetchLockProcessID:         99997,
		PrefetchStartedAt:             base.Add(3 * time.Nanosecond).Format(time.RFC3339Nano),
		PrefetchBaselineReceiptDigest: replacementOutcome.AccessReceiptDigest,
		AccessReceiptDigest:           failoverAccessSHA,
		PromptSHA256:                  failoverPromptSHA,
		DispatchedAt:                  base.Add(4 * time.Nanosecond).Format(time.RFC3339Nano),
	}
	failover.ZeroSideEffectOutcome = &pipelineSealedConvergenceBinaryZeroSideEffect{
		Version:                    pipelineSealedConvergenceBinaryZeroOutcomeVersion,
		Classification:             pipelineSealedConvergenceOutcomeBinaryZero,
		BinaryDispatch:             1,
		SessionIdentity:            failover.SessionIdentity,
		SessionSHA256:              seededCompactTestDigest("8"),
		SessionRows:                1,
		UserRows:                   1,
		AssistantRows:              0,
		ToolRows:                   0,
		ToolCallBlocks:             0,
		UserPromptSHA256:           failoverPromptSHA,
		PartialSHA256:              partialSHA,
		FormalPlanPresent:          false,
		AccessReceiptDigest:        failoverAccessSHA,
		AccessReceiptConsumed:      false,
		AccessReceiptLockOwner:     failover.PrefetchLockOwner,
		AccessReceiptLockProcessID: failover.PrefetchLockProcessID,
		RecordedAt:                 base.Add(5 * time.Nanosecond).Format(time.RFC3339Nano),
	}
	replacement.BinaryFailover = failover
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("valid binary-zero journal fixture was rejected")
	}
	return intent
}

func attachValidSeededCompactJournal(t *testing.T, intent *pipelineSealedConvergenceReplanIntent) {
	t.Helper()
	failover := intent.PlannerContinuation.Replacement.BinaryFailover
	base, err := time.Parse(time.RFC3339Nano, failover.ZeroSideEffectOutcome.RecordedAt)
	if err != nil {
		t.Fatal(err)
	}
	failover.SeededCompactFinalize = &pipelineSealedConvergenceSeededCompactFinalize{
		Version:                       pipelineSealedConvergenceSeededCompactVersion,
		SessionIdentity:               "convergence_planner_seeded_compact_finalize-ch05",
		AllowedMutableKeys:            append([]string(nil), pipelineSealedConvergenceSeededMutableKeys...),
		ReservedAt:                    base.Add(time.Nanosecond).Format(time.RFC3339Nano),
		InitialPartialSHA256:          intent.PlannerContinuation.InitialPartialSHA256,
		ImmutableSeedDigest:           seededCompactTestDigest("9"),
		PrefetchLockOwner:             "seeded-compact-lock",
		PrefetchLockProcessID:         99996,
		PrefetchStartedAt:             base.Add(2 * time.Nanosecond).Format(time.RFC3339Nano),
		PrefetchBaselineReceiptDigest: failover.AccessReceiptDigest,
		AccessReceiptDigest:           seededCompactTestDigest("a"),
		SeedArgsSHA256:                seededCompactTestDigest("b"),
		SeedToolDispatches:            1,
		SeedInvokedAt:                 base.Add(3 * time.Nanosecond).Format(time.RFC3339Nano),
		SeedPartialSHA256:             seededCompactTestDigest("c"),
		SeededAt:                      base.Add(4 * time.Nanosecond).Format(time.RFC3339Nano),
		PromptSHA256:                  seededCompactTestDigest("d"),
		PromptRunes:                   4096,
		ModelDispatches:               1,
		DispatchedAt:                  base.Add(5 * time.Nanosecond).Format(time.RFC3339Nano),
	}
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(*intent)
}

func cloneSeededCompactTestIntent(
	t *testing.T,
	intent pipelineSealedConvergenceReplanIntent,
) pipelineSealedConvergenceReplanIntent {
	t.Helper()
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	var clone pipelineSealedConvergenceReplanIntent
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func seededCompactTestDigest(char string) string {
	return "sha256:" + strings.Repeat(char, 64)
}

func sortedSeededCompactTestMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var _ agentcore.Tool = (*seededCompactRecordingPlanDetailsTool)(nil)
