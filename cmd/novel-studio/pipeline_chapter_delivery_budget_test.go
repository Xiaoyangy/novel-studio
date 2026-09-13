package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func TestChapterDeliveryBudgetCLIIdentityAndFirstDispatchSurviveResume(t *testing.T) {
	opts, st, old := continuationProducerCLIFixture(t)
	cfg, bundle, err := loadCfgBundle(opts)
	publicationArtifactMust(t, err)
	progress, err := st.Progress.Load()
	publicationArtifactMust(t, err)
	cfg.Budget.ChapterDeliverySeconds = 1200
	before, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	if _, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress); err == nil {
		t.Fatal("legacy generation acquired a new deadline")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	if before != after {
		t.Fatal("rejected retrofit changed old evidence")
	}
	_, err = writePipelinePlanningJSON(filepath.Join(st.Dir(), pipelineProjectAllAttemptPath), pipelineProjectAllAttempt{Version: "project-all-attempt.v2", Nonce: "explicit-new-delivery-budget"})
	publicationArtifactMust(t, err)
	fresh, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress)
	publicationArtifactMust(t, err)
	if fresh.Generation.GenerationID == old.Generation.GenerationID || fresh.Generation.ChapterDeliveryBudget == nil || fresh.Generation.ChapterDeliveryBudget.LimitSeconds != 1200 {
		t.Fatal("fresh identity did not bind its budget")
	}
	raw, err := json.Marshal(cfg)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, os.WriteFile(opts.ConfigPath, raw, 0600))
	stop := errors.New("fake detail dispatch complete; no provider used")
	original := pipelineProjectedChapterPlanner
	t.Cleanup(func() { pipelineProjectedChapterPlanner = original })
	var first *store.ChapterDeliveryTimingV1
	calls := 0
	pipelineProjectedChapterPlanner = func(_ context.Context, _ bootstrap.Config, _ assets.Bundle, _ string, chapter int, _, _ string, _ agents.ProjectedArcBoundary, hooks ...agents.ProjectedPlanningAccounting) (*agents.ProjectedChapterArtifacts, error) {
		calls++
		got, err := st.LoadChapterDeliveryTiming(fresh.Generation.GenerationID, chapter)
		publicationArtifactMust(t, err)
		if got == nil || got.StartedAt.IsZero() || got.DeadlineAt.Sub(got.StartedAt).Seconds() != 1200 {
			t.Fatal("detail dispatched before durable first-start binding")
		}
		if first == nil {
			first = got
		} else if got.StartedAt != first.StartedAt || got.DeadlineAt != first.DeadlineAt {
			t.Fatal("resume restarted chapter wall budget")
		}
		if len(hooks) != 1 || hooks[0].StartCall == nil {
			t.Fatal("detail lost its per-provider guard")
		}
		publicationArtifactMust(t, hooks[0].BeforeAgent())
		return nil, stop
	}
	for i := 0; i < 2; i++ {
		if err := pipelineProjectAllOnce(opts, pipelineFlags{Start: 1, End: 3}); !errors.Is(err, stop) {
			t.Fatalf("budgeted detail/recovery failed before fake boundary: %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("fake dispatches=%d", calls)
	}
	cfg.Budget.ChapterDeliverySeconds = 0
	resumed, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress)
	publicationArtifactMust(t, err)
	if resumed.Generation.GenerationID != fresh.Generation.GenerationID || resumed.Generation.ChapterDeliveryBudget == nil {
		t.Fatal("omitting config disabled an already frozen budget")
	}
	cfg.Budget.ChapterDeliverySeconds = 1201
	if _, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress); err == nil {
		t.Fatal("resume changed the frozen wall limit")
	}
}

func TestChapterDeliveryBudgetAccountingChecksEachAttemptWithoutCancelling(t *testing.T) {
	live, shadow := projectAccountingStores(t)
	a, err := newPipelineProjectAllAccounting(context.Background(), bootstrap.Config{}, live, shadow, "pg2_deadline_fixture")
	publicationArtifactMust(t, err)
	expired := false
	a.deliveryGuard = &pipelineProviderCallGuard{check: func() error {
		if expired {
			return store.ErrChapterDeliveryDeadline
		}
		return nil
	}}
	publicationArtifactMust(t, a.startCall("first-permitted-attempt", "writer"))
	a.record("writer", projectUsageMessage("first-permitted-attempt", 0))
	expired = true
	if err := a.startCall("blocked-retry", "writer"); !errors.Is(err, store.ErrChapterDeliveryDeadline) {
		t.Fatalf("retry escaped budget: %v", err)
	}
	if context.Cause(a.ctx) != nil {
		t.Fatal("deadline cancelled the in-flight accounting context")
	}
	expired = false
	if err := a.startCall("must-stay-stopped", "writer"); !errors.Is(err, store.ErrChapterDeliveryDeadline) {
		t.Fatal("a refused run reopened its provider gate")
	}
	if err := a.afterAgent(); !errors.Is(err, store.ErrChapterDeliveryDeadline) {
		t.Fatal("agent-loop error conversion lost durable deadline cause")
	}
	if err := a.close(); !errors.Is(err, store.ErrChapterDeliveryDeadline) {
		t.Fatal("accounting close hid the deadline")
	}
}

func TestChapterDeliveryBudgetNilKeepsHistoricalJSONAndDependency(t *testing.T) {
	g := domain.PlanningGenerationV2{}
	raw, err := json.Marshal(g)
	publicationArtifactMust(t, err)
	var fields map[string]any
	publicationArtifactMust(t, json.Unmarshal(raw, &fields))
	if _, exists := fields["chapter_delivery_budget"]; exists {
		t.Fatal("legacy generation JSON acquired a new field")
	}
	base := "sha256:unchanged-legacy-dependency"
	if pipelineChapterDeliveryDependencyRoot(base, nil) != base {
		t.Fatal("legacy dependency was rehashed")
	}
	b := &domain.ChapterDeliveryBudgetV1{Policy: domain.ChapterDeliveryBudgetPolicyV1, LimitSeconds: 1200}
	if pipelineChapterDeliveryDependencyRoot(base, b) == base {
		t.Fatal("enabled policy is absent from identity")
	}
	if _, err := resolvePipelineChapterDeliveryBudget(1200, &g); err == nil {
		t.Fatal("legacy nil policy was retrofitted")
	}
}

func TestChapterDeliveryBudgetStopPreservesExistingRenderCandidate(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	frozen, plan := pipelineRenderV3StyleEpochTestFrozen(t, live)
	frozen.EffectiveStyleProtocol = ""
	id, err := pipelineRenderTransactionID(frozen)
	publicationArtifactMust(t, err)
	candidate, err := prepareFreshPipelineRenderCandidateForStyleEpoch(live, frozen, id, filepath.Join(pipelineRenderCandidateRoot(live), id), false)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, rewritePipelineRenderCandidateAsLegacyV2(candidate.OutputDir, frozen))
	st := store.NewStore(live)
	const owner = "deadline-candidate-preservation-test"
	publicationArtifactMust(t, st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionRender, TargetChapter: 1, PlanDigest: frozen.PlanDigest, Owner: owner, ExpiresAt: time.Now().UTC().Add(time.Hour)}))
	defer func() { _ = st.Runtime.ReleasePipelineExecution(owner) }()
	guard := &pipelineProviderCallGuard{check: func() error { return store.ErrChapterDeliveryDeadline }}
	if !errors.Is(guard.Check(), store.ErrChapterDeliveryDeadline) {
		t.Fatal("fixture did not expire")
	}
	_, _, err = runPipelineSealedRenderCandidate(cliOptions{providerCallGuard: guard}, pipelineFlags{}, &domain.PipelineState{}, bootstrap.Config{OutputDir: live}, assets.Bundle{}, frozen, plan, nil, true)
	if !errors.Is(err, store.ErrChapterDeliveryDeadline) {
		t.Fatalf("deadline cause lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(candidate.OutputDir, "meta", "planning", "render_candidate.json")); err != nil {
		t.Fatalf("deadline retired the preserved candidate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(candidate.OutputDir, "drafts", "01.plan.json")); err != nil {
		t.Fatalf("deadline lost the frozen work: %v", err)
	}
	ledger, err := pipelineRenderDispatchLedgerPath(live, id)
	publicationArtifactMust(t, err)
	if _, err := os.Stat(ledger); !os.IsNotExist(err) {
		t.Fatal("test unexpectedly dispatched a provider")
	}
}

type chapterDeliveryDispatchProbe struct{ calls int }

func (*chapterDeliveryDispatchProbe) SupportsTools() bool { return true }
func (m *chapterDeliveryDispatchProbe) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("temporary fake provider response")}, Usage: &agentcore.Usage{Input: 1, Output: 1}}}, nil
}
func (*chapterDeliveryDispatchProbe) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, errors.New("unexpected stream")
}

func chapterDeliverySealedDispatchFixture(t *testing.T, started bool) (*store.Store, domain.PlanningGenerationV2) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	publicationArtifactMust(t, st.Init())
	g, registry := projectAllCmdTestGenerationAndRegistry(t, 3)
	g.ChapterDeliveryBudget = &domain.ChapterDeliveryBudgetV1{Policy: domain.ChapterDeliveryBudgetPolicyV1, LimitSeconds: 1200}
	var err error
	g.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(g)
	publicationArtifactMust(t, err)
	source := domain.PlanningSourceSnapshotV2{Version: domain.PlanningSourceSnapshotV2Version, GenerationID: g.GenerationID, BaseCanonChapter: g.BaseCanonChapter, BaseCanonRoot: g.BaseCanonRoot, BaseStateRoot: g.BaseStateRoot, StableOutlineRoot: g.StableOutlineRoot, PlanningDependencyRoot: g.PlanningDependencyRoot, RandomSeedContractRoot: g.RandomSeedContractRoot, FoundationSnapshotRoot: projectAllCmdTestDigest("delivery-foundation"), RAGSnapshotRoot: projectAllCmdTestDigest("delivery-rag"), CapturedAt: g.CreatedAt}
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	publicationArtifactMust(t, err)
	p := st.ProjectedV2()
	publicationArtifactMust(t, p.CreateBuildingGeneration(g, source, registry))
	publicationArtifactMust(t, st.ArmChapterDeliveryBudgetNow(g))
	if started {
		_, err := st.BeginChapterDeliveryNow(g, 1)
		publicationArtifactMust(t, err)
	}
	cursor, err := p.InitializeProjectionCursor(g.GenerationID)
	publicationArtifactMust(t, err)
	var bundles []domain.ProjectedChapterBundle
	previous, preState, err := pipelineProjectAllTail(g, nil)
	publicationArtifactMust(t, err)
	for chapter := 1; chapter <= 3; chapter++ {
		artifacts, outline := projectAllCmdTestArtifacts(t, g.GenerationID, chapter)
		projectAllCmdTestBindPlanningContext(t, artifacts, g, bundles, registry, chapter)
		bundle, next, err := buildPipelineProjectedChapterBundle(g, outline, previous, preState, artifacts, registry)
		publicationArtifactMust(t, err)
		cursor, err = p.ProjectChapterAndAdvance(g.GenerationDigest, g.ChainTailRoot, g.ObligationRegistryRoot, *cursor, bundle, next)
		publicationArtifactMust(t, err)
		current, err := p.LoadBuildingGeneration(g.GenerationID)
		publicationArtifactMust(t, err)
		g, registry, previous, preState = *current, next, bundle.BundleDigest, bundle.ProjectedPostStateRoot
		bundles = append(bundles, bundle)
	}
	_, err = p.SealGeneration(g.GenerationID)
	publicationArtifactMust(t, err)
	sealed, err := p.LoadSealedGeneration(g.GenerationID)
	publicationArtifactMust(t, err)
	return st, *sealed
}

func TestChapterDeliveryBudgetRenderRefusesMissingOriginalStartBeforeProvider(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "armed-only-sealed", true: "original-start-sealed"}[started], func(t *testing.T) {
			st, g := chapterDeliverySealedDispatchFixture(t, started)
			// A general check intentionally permits unstarted slots. Render has
			// the stronger current-chapter contract, including every later call.
			publicationArtifactMust(t, pipelineGenerationDeliveryGuard(st, g).Check())
			guard := pipelineGenerationDeliveryGuard(st, g, 1)
			probe := &chapterDeliveryDispatchProbe{}
			scope, err := host.NewScopedUsageAccounting(context.Background(), st, g.GenerationID, bootstrap.BudgetConfig{}, guard.Check)
			publicationArtifactMust(t, err)
			model := scope.Decorate(scope.Context(), "drafter", "test", "test", probe)
			_, callErr := model.Generate(context.Background(), nil, nil)
			if started {
				publicationArtifactMust(t, callErr)
				if probe.calls != 1 {
					t.Fatal("original start could not resume its provider")
				}
			} else {
				if callErr == nil || !strings.Contains(callErr.Error(), "original first-detail start") || probe.calls != 0 {
					t.Fatalf("missing start reached provider: calls=%d err=%v", probe.calls, callErr)
				}
				if _, err := st.BeginChapterDeliveryNow(g, 1); err == nil {
					t.Fatal("sealed projection backfilled a new start")
				}
				if timing, err := st.LoadChapterDeliveryTiming(g.GenerationID, 1); err != nil || timing != nil {
					t.Fatalf("failed render invented a start: %+v %v", timing, err)
				}
			}
			_ = scope.Finish(callErr)
		})
	}
}
