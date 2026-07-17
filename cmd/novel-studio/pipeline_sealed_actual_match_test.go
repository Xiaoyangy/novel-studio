package main

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type pipelineSealedActualTestFixture struct {
	Store     *store.Store
	Bundle    domain.ProjectedChapterBundle
	Candidate domain.ChapterWorldDelta
	Body      string
}

func TestMatchPipelineSealedRenderActualDeltaRequiresIndependentEvidenceForEveryCategory(t *testing.T) {
	fixture := newPipelineSealedActualTestFixture(t)

	got, err := matchPipelineSealedRenderActualDelta(
		fixture.Store,
		&fixture.Bundle,
		&fixture.Candidate,
		fixture.Body,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete || !got.ProjectionMatch || len(got.MismatchReasons) != 0 {
		t.Fatalf("independently evidenced render did not match: %+v", got)
	}
	projectedDigest, err := domain.ComputeProjectedDeltaV2Digest(fixture.Bundle.ProjectedDelta)
	if err != nil {
		t.Fatal(err)
	}
	actualDigest, err := domain.ComputeProjectedDeltaV2Digest(got.ActualDelta)
	if err != nil {
		t.Fatal(err)
	}
	if actualDigest != projectedDigest {
		t.Fatalf("canonical actual delta digest = %s want %s", actualDigest, projectedDigest)
	}
	evidenced := make(map[string]bool)
	for _, item := range got.Evidence {
		evidenced[item.Category] = true
	}
	for _, category := range []string{
		"timeline",
		"character_state",
		"relationship",
		"resource",
		"knowledge",
		"location",
		"foreshadow",
		"obligation",
		"required_beat",
	} {
		if !evidenced[category] {
			t.Fatalf("successful match has no independently recorded %s evidence: %+v", category, got.Evidence)
		}
	}
}

func TestMatchPipelineSealedRenderActualDeltaFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *pipelineSealedActualTestFixture)
		want   string
	}{
		{
			name: "missing structured location",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Candidate.CharacterDeltas[0].Location = ""
			},
			want: "location[",
		},
		{
			name: "after contradiction",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Candidate.CharacterDeltas[0].Status = "仍然完全不相信这份票据"
			},
			want: "after mismatch",
		},
		{
			name: "before contradiction",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.ProjectedDelta.CharacterState[0].Before = "完全未知"
				fixture.Candidate.WorldDeltas = append(
					fixture.Candidate.WorldDeltas,
					domain.WorldChapterDelta{
						Kind:   "state",
						Entity: "主角.state",
						Change: "已经确信 -> 从猜测转为有限确认",
					},
				)
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "before contradiction",
		},
		{
			name: "unplanned hard state mutation",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Candidate.WorldDeltas = append(
					fixture.Candidate.WorldDeltas,
					domain.WorldChapterDelta{
						Kind:   "state",
						Entity: "主角.death_state",
						Change: "alive -> 突然死亡",
					},
				)
			},
			want: "unplanned hard actual mutation",
		},
		{
			name: "required beat absent from body",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Body = "他进了旧街，只在路口停了一会儿，随后空手离开。"
			},
			want: "hard required beat has no locatable body evidence",
		},
		{
			name: "preserved negative continuity contradicted",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.HardRenderContract.MustPreserve = []string{"摊主不得离开青山县旧街"}
				fixture.Body += "结账后，摊主离开青山县旧街，去了县城另一头。"
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "hard continuity contract was contradicted",
		},
		{
			name: "positive preserved fact explicitly negated",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.HardRenderContract.MustPreserve = []string{"盖章票据归主角持有"}
				fixture.Body += "但那张盖章票据并非归主角持有，他当场还了回去。"
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "hard preserved fact was explicitly negated",
		},
		{
			name: "reveal budget exceeded",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.HardRenderContract.RevealBudget = []domain.RevealBudgetItemV2{{
					FactID: "reveal:test:hidden-owner",
					Action: "limit",
					Limit:  "不揭示后台老板已经决定放行",
				}}
				fixture.Body += "同一刻，后台老板已经决定放行，只等把批条送来。"
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "hard reveal budget was exceeded",
		},
		{
			name: "positive reveal slogan is not mechanically enforceable",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.HardRenderContract.RevealBudget = []domain.RevealBudgetItemV2{{
					FactID: "reveal:test:positive-slogan",
					Action: "limit",
					Limit:  "只允许主角知道票据存在",
				}}
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "hard reveal budget is not mechanically enforceable",
		},
		{
			name: "empty negative reveal probe is not mechanically enforceable",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.HardRenderContract.RevealBudget = []domain.RevealBudgetItemV2{{
					FactID: "reveal:test:empty-probe",
					Action: "limit",
					Limit:  "不解释",
				}}
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "hard reveal budget is not mechanically enforceable",
		},
		{
			name: "visible resource result contradicted in body",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Body = "午前，主角走到青山县旧街做完交易，拿到盖章票据后却当场交还摊主。"
			},
			want: "no locatable semantic body evidence for visible result",
		},
		{
			name: "visible resource result later returned",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Body += "出了旧街，主角又把盖章票据交还摊主。"
			},
			want: "terminal body transfer contradicts projected result",
		},
		{
			name: "visible resource result later handed to third party",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Body += "出了旧街，主角又把盖章票据交给周舟保管。"
			},
			want: "terminal body transfer contradicts projected result",
		},
		{
			name: "unrelated negation does not hide later resource transfer",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Body += "主角没有犹豫就把盖章票据交给周舟保管。"
			},
			want: "terminal body transfer contradicts projected result",
		},
		{
			name: "projected shadow metadata",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Candidate.Sources = []string{"project-all sealed projection"}
			},
			want: "projected shadow artifact",
		},
		{
			name: "wrong generation metadata",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Candidate.GenerationID = "pg2_wrong_generation"
			},
			want: "commit metadata generation mismatch",
		},
		{
			name: "opaque consumed obligation",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				obligation := fixture.Bundle.ProjectedDelta.Obligations[0]
				obligation.Operation = "consume"
				obligation.After = "satisfied"
				fixture.Bundle.ProjectedDelta.Obligations = []domain.StateMutationV2{obligation}
				fixture.Bundle.ObligationsCreated = nil
				fixture.Bundle.ObligationsConsumed = []string{obligation.Subject}
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "only an opaque id is available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPipelineSealedActualTestFixture(t)
			tt.mutate(t, &fixture)
			if err := fixture.Store.SaveChapterWorldDelta(fixture.Candidate); err != nil {
				t.Fatal(err)
			}

			got, err := matchPipelineSealedRenderActualDelta(
				fixture.Store,
				&fixture.Bundle,
				&fixture.Candidate,
				fixture.Body,
			)
			if err != nil {
				t.Fatalf("fail-closed mismatch returned an operational error: %v", err)
			}
			if got.ProjectionMatch {
				t.Fatalf("counterexample was accepted: %+v", got)
			}
			if !pipelineSealedActualTestContains(got.MismatchReasons, tt.want) {
				t.Fatalf("mismatch reasons %q do not contain %q", got.MismatchReasons, tt.want)
			}
		})
	}
}

func TestPipelineSealedResourceTransferAwayScopesNegationToTransfer(t *testing.T) {
	tests := []struct {
		name    string
		segment string
		want    bool
	}{
		{name: "explicitly not returned", segment: "主角没有把盖章票据交还摊主", want: false},
		{name: "refuses handoff", segment: "主角拒绝把盖章票据交给周舟", want: false},
		{name: "returned to projected owner", segment: "摊主把盖章票据交给主角", want: false},
		{name: "unrelated negation then handoff", segment: "主角没有犹豫就把盖章票据交给周舟", want: true},
		{name: "ordinary return", segment: "主角把盖章票据交还摊主", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pipelineSealedResourceTransferAway(tt.segment, "主角"); got != tt.want {
				t.Fatalf("pipelineSealedResourceTransferAway(%q)=%v want %v", tt.segment, got, tt.want)
			}
		})
	}
}

func TestMatchPipelineSealedActualFactsLocatesConsumedObligationInBody(t *testing.T) {
	const obligationID = "obl:rule:1:consume-proof"
	projected := domain.ProjectedDelta{
		Version: domain.ProjectedDeltaV2Version,
		Timeline: []domain.StateMutationV2{{
			StableID:  "sealed-test:timeline",
			Subject:   "chapter",
			Field:     "outcome",
			Operation: "advance",
			After:     "柜员完成复核",
			Cause:     "票据被提交",
		}},
		Obligations: []domain.StateMutationV2{{
			StableID:  "sealed-test:obligation",
			Subject:   obligationID,
			Field:     "state",
			Operation: "consume",
			After:     "satisfied",
			Cause:     "本章是密封计划指定的兑现章",
		}},
	}
	facts := []pipelineSealedActualFact{{
		Category: "timeline",
		Subject:  "chapter",
		Field:    "outcome",
		After:    "柜员完成复核",
		Locator:  "chapter_world_delta.world_deltas[0]",
		Hard:     true,
	}}
	requirements := pipelineSealedActualRequirements{
		Obligations: map[string]domain.ObligationV2{
			obligationID: {
				ID:       obligationID,
				Contract: "主角把盖章票据交给柜员复核",
				Hardness: domain.ObligationHardV2,
			},
		},
	}

	got := matchPipelineSealedActualFacts(
		projected,
		facts,
		"排到窗口后，主角把盖章票据交给柜员复核。柜员对照存根，点了点头。",
		requirements,
	)
	if !got.ProjectionMatch || len(got.ObligationsSatisfied) != 1 ||
		got.ObligationsSatisfied[0] != obligationID {
		t.Fatalf("body-locatable hard obligation was not matched: %+v", got)
	}

	missing := matchPipelineSealedActualFacts(
		projected,
		facts,
		"主角在窗口外等到天黑，最后仍旧没有递出手里的票据。",
		requirements,
	)
	if missing.ProjectionMatch ||
		!pipelineSealedActualTestContains(missing.MismatchReasons, "consume has no locatable body evidence") {
		t.Fatalf("unrealized hard obligation was accepted: %+v", missing)
	}
}

func TestMatchPipelineSealedActualFactsRejectsInsufficientIdentitySchema(t *testing.T) {
	projected := domain.ProjectedDelta{
		Version: domain.ProjectedDeltaV2Version,
		Timeline: []domain.StateMutationV2{{
			StableID:  "sealed-test:timeline",
			Subject:   "chapter",
			Field:     "outcome",
			Operation: "advance",
			After:     "交易完成",
			Cause:     "现场交割",
		}},
		Resources: []domain.StateMutationV2{{
			StableID:  "sealed-test:resource",
			Subject:   "主角",
			Field:     "resource",
			Operation: "update",
			After:     "booked",
			Cause:     "交易完成",
		}},
	}
	facts := []pipelineSealedActualFact{
		{
			Category: "timeline",
			Subject:  "chapter",
			Field:    "outcome",
			After:    "交易完成",
			Locator:  "chapter_world_delta.world_deltas[0]",
			Hard:     true,
		},
		{
			Category: "resource",
			Subject:  "主角",
			Object:   "盖章票据",
			Field:    "resource",
			After:    "booked",
			Locator:  "resource_ledger.json#chapter=1,index=0",
			Hard:     true,
		},
	}

	got := matchPipelineSealedActualFacts(
		projected,
		facts,
		"主角完成交易并收好盖章票据。",
		pipelineSealedActualRequirements{},
	)
	if got.ProjectionMatch ||
		!pipelineSealedActualTestContains(got.MismatchReasons, "lacks a stable resource object") {
		t.Fatalf("resource without independently matchable identity was accepted: %+v", got)
	}
}

func newPipelineSealedActualTestFixture(t *testing.T) pipelineSealedActualTestFixture {
	t.Helper()
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	bundle, _, err := buildPipelineProjectedChapterBundle(
		generation,
		outline,
		genesis,
		generation.BaseStateRoot,
		artifacts,
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}

	const obligationID = "obl:rule:1:sealed-test"
	bundle.ProjectedDelta = domain.NormalizeProjectedDeltaV2(domain.ProjectedDelta{
		Version: domain.ProjectedDeltaV2Version,
		Timeline: []domain.StateMutationV2{{
			StableID:  "sealed-test:timeline",
			Subject:   "chapter",
			Field:     "outcome",
			Operation: "advance",
			After:     "完成小额验证",
			Cause:     "主角选择可撤回交易",
		}},
		CharacterState: []domain.StateMutationV2{{
			StableID:  "sealed-test:character",
			Subject:   "主角",
			Field:     "state",
			Operation: "update",
			After:     "从猜测转为有限确认",
			Cause:     "拿到可复核票据",
		}},
		Relationships: []domain.StateMutationV2{{
			StableID:  "sealed-test:relationship",
			Subject:   "主角",
			Object:    "摊主",
			Field:     "relationship",
			Operation: "update",
			After:     "从试探转为初步信任",
			Cause:     "摊主当面写清票据",
		}},
		Resources: []domain.StateMutationV2{{
			StableID:  "sealed-test:resource",
			Subject:   "主角",
			Object:    "盖章票据",
			Field:     "resource",
			Operation: "update",
			After:     "票据归主角持有",
			Cause:     "交易完成并留下凭证",
		}},
		Knowledge: []domain.StateMutationV2{{
			StableID:  "sealed-test:knowledge",
			Subject:   "主角",
			Field:     "knowledge_boundary",
			Operation: "set",
			After:     "只知道自己亲历和收到的票据",
			Cause:     "主角没有接触后台信息",
		}},
		Locations: []domain.StateMutationV2{{
			StableID:  "sealed-test:location",
			Subject:   "主角",
			Field:     "location",
			Operation: "set",
			After:     "青山县旧街",
			Cause:     "主角到旧街完成交易",
		}},
		Foreshadows: []domain.StateMutationV2{{
			StableID:  "sealed-test:foreshadow",
			Subject:   "摊主",
			Object:    "receipt-origin",
			Field:     "evidence_return",
			Operation: "advance",
			After:     "票据来源留下后续核查入口",
			Cause:     "票据来源留下后续核查入口",
		}},
		Obligations: []domain.StateMutationV2{{
			StableID:  "sealed-test:obligation",
			Subject:   obligationID,
			Field:     "state",
			Operation: "create",
			After:     "planned",
			Cause:     "密封计划创建后续规则义务",
		}},
	})
	bundle.ObligationsConsumed = nil
	bundle.ObligationsCreated = []string{obligationID}
	bundle.ObligationsCarried = nil
	rebindPipelineSealedActualTestBundle(t, &bundle)

	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(bundle.ChapterPlan); err != nil {
		t.Fatal(err)
	}
	candidate := domain.ChapterWorldDelta{
		Version:      1,
		Chapter:      bundle.Chapter,
		GenerationID: bundle.GenerationID,
		Summary:      "主角完成小额验证，拿到票据并留下后续核查入口。",
		CharacterDeltas: []domain.CharacterChapterDelta{{
			Character:         "主角",
			Location:          "青山县旧街",
			Status:            "从猜测转为有限确认",
			KnowledgeBoundary: "只知道自己亲历和收到的票据",
			DeathState:        "alive",
		}, {
			Character:  "摊主",
			Status:     "存活",
			DeathState: "alive",
		}},
		WorldDeltas: []domain.WorldChapterDelta{
			{
				Kind:     "timeline",
				Change:   "完成小额验证",
				Evidence: "主角完成小额验证并拿到票据",
			},
			{
				Kind:     "timeline",
				Change:   "摊主逐项写清票据",
				Evidence: "摊主当面写清金额与时间",
			},
			{
				Kind:     "relationship",
				Entity:   "主角|摊主",
				Change:   "从试探转为初步信任",
				Evidence: "摊主当面写清金额与时间",
			},
			{
				Kind:     "resource_booked",
				Entity:   "盖章票据",
				Change:   "票据归主角持有",
				Evidence: "票据盖章后被主角收进内袋",
			},
			{
				Kind:     "foreshadow",
				Entity:   "receipt-origin",
				Change:   "票据来源留下后续核查入口",
				Evidence: "票据角落印着陌生编号",
			},
		},
		Sources: []string{
			"commit_chapter",
			"character_stage_records",
			"timeline/resource/relationship/state deltas",
		},
	}
	if err := st.SaveChapterWorldDelta(candidate); err != nil {
		t.Fatal(err)
	}
	if err := st.ResourceLedger.Save(domain.ResourceLedger{
		Version: 1,
		Claims: []domain.ResourceClaim{{
			ID:       "receipt-1",
			Name:     "盖章票据",
			Owner:    "主角",
			Status:   "票据归主角持有",
			Evidence: "",
			Chapter:  bundle.Chapter,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return pipelineSealedActualTestFixture{
		Store:     st,
		Bundle:    bundle,
		Candidate: candidate,
		Body:      "午前，主角走到青山县旧街，先做了一笔小额验证。摊主当面写清金额与时间，盖章后把票据递了过来。盖章票据归主角持有，他把它收进内袋。票据角落印着陌生编号，给来源留下后续核查入口。后台怎么处理，他没看见，也只认自己亲手拿到的凭证。摊主见他按规矩办事，语气里的试探淡了些。",
	}
}

func rebindPipelineSealedActualTestBundle(
	t *testing.T,
	bundle *domain.ProjectedChapterBundle,
) {
	t.Helper()
	var err error
	bundle.ProjectedDelta = domain.NormalizeProjectedDeltaV2(bundle.ProjectedDelta)
	bundle.ProjectedPostStateRoot, err = domain.DeriveProjectedPostStateRootV2(
		bundle.ProjectedPreStateRoot,
		bundle.ProjectedDelta,
	)
	if err != nil {
		t.Fatal(err)
	}
	canonicalContract := pipelineHardRenderContractV2(
		bundle.ChapterPlan,
		bundle.ChapterWorldSimulation,
		bundle.ProjectedDelta,
	)
	bundle.HardRenderContract.ForeshadowChanges = canonicalContract.ForeshadowChanges
	bundle.HardRenderContract.ResourceChanges = canonicalContract.ResourceChanges
	bundle.HardRenderContract.RelationshipChanges = canonicalContract.RelationshipChanges
	bundle.HardRenderContract.KnowledgeChanges = canonicalContract.KnowledgeChanges
	bundle.RenderContext, err = augmentPipelineProjectAllRenderContext(bundle.RenderContext, *bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RenderContextSHA256, err = domain.ComputePlanningV2JSONDigest(bundle.RenderContext)
	if err != nil {
		t.Fatal(err)
	}
	bundle.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(*bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedChapterBundle(*bundle); err != nil {
		t.Fatalf("invalid sealed actual matcher fixture bundle: %v", err)
	}
}

func pipelineSealedActualTestContains(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
