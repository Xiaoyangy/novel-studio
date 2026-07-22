package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSealedShortChapterWordBoundsUseAcceptedActualCumulative(t *testing.T) {
	st := sealedShortWordBudgetFixture(t, domain.PlanningTierShort, true)

	bounds, err := InspectSealedShortChapterWordBounds(st, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !bounds.Active || bounds.Min != 2444 || bounds.Max != 2600 ||
		bounds.PriorAcceptedRunes != 9398 || bounds.BookMin != 28000 || bounds.BookMax != 30000 {
		t.Fatalf("unexpected chapter 5 accepted-prose bounds: %+v", bounds)
	}

	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	err = requireChapterWordContract(st, 5, strings.Repeat("她", 2100))
	if err == nil || !strings.Contains(err.Error(), "动态要求 2444-2600 字") ||
		!strings.Contains(err.Error(), "已验收累计 9398 字") ||
		!strings.Contains(err.Error(), "全书合同 28000-30000 字") {
		t.Fatalf("under-budget exact body was not blocked with its precise range: %v", err)
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("word-boundary rejection mutated the candidate tree: before=%s after=%s", before, after)
	}
	if err := requireChapterWordContract(st, 5, strings.Repeat("她", 2444)); err != nil {
		t.Fatalf("lower-bound exact body was rejected: %v", err)
	}
}

func TestSealedShortRenderWordBudgetOverlaysReturnedContextOnly(t *testing.T) {
	st := sealedShortWordBudgetFixture(t, domain.PlanningTierShort, true)
	raw := json.RawMessage(`{
		"_context_profile":"draft",
		"working_memory":{
			"render_packet":{
				"version":11,
				"chapter":5,
				"word_budget":{
					"unit":"unicode_characters_including_title",
					"hard_min":2200,
					"hard_max":2600,
					"submission_target_min":2333,
					"submission_target_max":2467,
					"exact_boundary":true
				}
			},
			"user_rules":{"structured":{"chapter_words":{"min":2200,"max":2600}}}
		}
	}`)
	original := string(raw)
	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}

	overlaid, err := NewContextTool(st, References{}, "default").attachSealedShortRenderWordBudget(raw, 5)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(overlaid, &payload); err != nil {
		t.Fatal(err)
	}
	working, _ := payload["working_memory"].(map[string]any)
	packet, _ := working["render_packet"].(map[string]any)
	budget, _ := packet["word_budget"].(map[string]any)
	if budget["hard_min"] != float64(2444) || budget["hard_max"] != float64(2600) ||
		budget["submission_target_min"] != float64(2522) || budget["submission_target_max"] != float64(2561) ||
		budget["exact_boundary"] != true {
		t.Fatalf("effective render word budget = %#v, want hard 2444-2600 target 2522-2561", budget)
	}
	userRules, _ := working["user_rules"].(map[string]any)
	structured, _ := userRules["structured"].(map[string]any)
	chapterWords, _ := structured["chapter_words"].(map[string]any)
	if chapterWords["min"] != float64(2200) || chapterWords["max"] != float64(2600) {
		t.Fatalf("execution overlay mutated durable user rules: %#v", chapterWords)
	}
	if string(raw) != original {
		t.Fatal("execution overlay mutated the caller's frozen payload bytes")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("execution overlay mutated the frozen candidate tree: before=%s after=%s", before, after)
	}
}

func TestSealedShortChapterWordBoundsDoNotAffectOtherPipelines(t *testing.T) {
	t.Run("non-short", func(t *testing.T) {
		st := sealedShortWordBudgetFixture(t, domain.PlanningTierMid, true)
		bounds, err := InspectSealedShortChapterWordBounds(st, 5)
		if err != nil || bounds.Active {
			t.Fatalf("mid-length project activated sealed-short bounds: bounds=%+v err=%v", bounds, err)
		}
		if err := requireChapterWordContract(st, 5, strings.Repeat("她", 2400)); err != nil {
			t.Fatalf("mid-length project inherited dynamic short gate: %v", err)
		}
	})

	t.Run("non-sealed", func(t *testing.T) {
		st := sealedShortWordBudgetFixture(t, domain.PlanningTierShort, false)
		bounds, err := InspectSealedShortChapterWordBounds(st, 5)
		if err != nil || bounds.Active {
			t.Fatalf("ordinary short project activated sealed candidate bounds: bounds=%+v err=%v", bounds, err)
		}
		if err := requireChapterWordContract(st, 5, strings.Repeat("她", 2400)); err != nil {
			t.Fatalf("ordinary short project inherited sealed dynamic gate: %v", err)
		}
		planningBounds, err := InspectShortChapterWordBoundsFromAcceptedProse(st, 5)
		if err != nil || !planningBounds.Active || planningBounds.Min != 2444 || planningBounds.Max != 2600 {
			t.Fatalf("read-only live planning bounds = %+v err=%v, want active 2444-2600", planningBounds, err)
		}
	})
}

func sealedShortWordBudgetFixture(
	t *testing.T,
	tier domain.PlanningTier,
	sealed bool,
) *store.Store {
	t.Helper()
	root := t.TempDir()
	candidateID := "render-ch0005-word-budget"
	sourceOutput := filepath.Join(root, "live", "output")
	st := store.NewStore(filepath.Join(
		filepath.Dir(sourceOutput), ".render-candidates", candidateID, "output",
	))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.Save(domain.RunMeta{PlanningTier: tier}); err != nil {
		t.Fatal(err)
	}
	if err := st.UserRules.Save(&rules.Snapshot{Structured: rules.Structured{
		ChapterWords: &rules.WordRange{Min: 2200, Max: 2600},
	}}); err != nil {
		t.Fatal(err)
	}

	counts := map[int]int{1: 2350, 2: 2350, 3: 2350, 4: 2348}
	for chapter := 1; chapter <= 4; chapter++ {
		if err := st.Drafts.SaveFinalChapter(chapter, strings.Repeat("她", counts[chapter])); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Progress.Save(&domain.Progress{
		NovelName:         "动态字数门禁测试",
		Phase:             domain.PhaseWriting,
		CurrentChapter:    5,
		TotalChapters:     12,
		CompletedChapters: []int{1, 2, 3, 4},
		TotalWordCount:    9398,
		ChapterWordCounts: counts,
	}); err != nil {
		t.Fatal(err)
	}

	receipt := sealedShortOutlineReceipt(t, root)
	if err := st.SaveOutlineAllExecutionReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if !sealed {
		return st
	}
	if err := os.MkdirAll(sourceOutput, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(
		filepath.Dir(sourceOutput), ".render-candidates", "convergence", candidateID,
	), 0o755); err != nil {
		t.Fatal(err)
	}

	plan := domain.ChapterPlan{Chapter: 5, Title: "旧账翻到第五页"}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(5),
		"plan",
		"drafts/05.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest := toolRenderCandidateManifest{
		Version:                toolRenderCandidatePreviousManifestVersion,
		CandidateID:            candidateID,
		GenerationID:           "short-word-generation",
		Chapter:                5,
		PlanDigest:             checkpoint.Digest,
		PlanCheckpointSeq:      checkpoint.Seq,
		ProjectedBundleDigest:  "sha256:word-budget-bundle",
		PromotionReceiptDigest: "sha256:word-budget-promotion",
		SourceOutputDir:        sourceOutput,
	}
	writeSealedShortWordJSON(t, filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"), manifest)
	marker := sealedV2FrozenPlanMarker{
		Version:                "pipeline-planning.v1",
		Chapter:                5,
		PlanDigest:             checkpoint.Digest,
		PlanCheckpointSeq:      checkpoint.Seq,
		RenderContextSHA256:    "sha256:word-budget-context",
		PlanningGenerationID:   manifest.GenerationID,
		ProjectionBinding:      sealedV2ProjectionBinding,
		ProjectedPlanSHA256:    "sha256:word-budget-plan",
		ProjectedBundleDigest:  manifest.ProjectedBundleDigest,
		PromotionReceiptDigest: manifest.PromotionReceiptDigest,
	}
	writeSealedShortWordJSON(t, filepath.Join(st.Dir(), filepath.FromSlash(currentFrozenPlanMarkerPath)), marker)
	return st
}

func sealedShortOutlineReceipt(t *testing.T, root string) domain.OutlineAllExecutionReceipt {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	digest := func(r string) string { return domain.PlanningV2DigestPrefix + strings.Repeat(r, 64) }
	receipt := domain.OutlineAllExecutionReceipt{
		Version:                  domain.OutlineAllExecutionReceiptVersion,
		Mode:                     domain.OutlineAllExecutionMode,
		Status:                   domain.OutlineAllExecutionBuilding,
		BaseCanonChapter:         0,
		WritingMode:              domain.WritingPipelineModeSealedTwoPassV2,
		WritingModeReceiptDigest: digest("a"),
		CompassDigest:            digest("b"),
		EstimatedScale:           "1-1卷，12-12章；正文2.8万—3万字",
		EndingDirection:          "两位女主共同公开旧案并承担选择",
		NonNegotiables:           []string{"双女主都必须完成主动选择"},
		MinVolumes:               1,
		MaxVolumes:               1,
		MinChapters:              12,
		MaxChapters:              12,
		TargetVolumes:            1,
		TargetChapters:           12,
		TargetWords:              29000,
		TargetWordsPerChapter:    2417,
		SourceSnapshotRoot:       digest("c"),
		ProtectedCanonRoot:       digest("d"),
		StableProgressRoot:       digest("1"),
		FoundationContextRoot:    digest("2"),
		AttemptID:                "sealed-short-word-budget-test",
		CandidateDir:             filepath.Join(root, "outline-candidate"),
		CoordinatorProvider:      "provider-a",
		CoordinatorModel:         "coordinator",
		CoordinatorReasoning:     "high",
		ArchitectProvider:        "provider-b",
		ArchitectModel:           "architect",
		ArchitectReasoning:       "high",
		PromptProtocolDigest:     digest("e"),
		LockVersion:              1,
		LockMode:                 domain.PipelineExecutionOutlineAll,
		LockTargetChapter:        1,
		LockOwner:                "sealed-short-word-budget-test",
		LockProcessID:            7,
		LockAcquiredAt:           now,
		LockExpiresAt:            now.Add(time.Hour),
		StartedAt:                now,
		UpdatedAt:                now,
	}
	receipt.ModelIdentityDigest, _ = domain.ComputeOutlineAllModelIdentityDigest(receipt.ModelIdentity())
	signed, err := domain.SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func writeSealedShortWordJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
