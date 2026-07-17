package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPipelineOutlineRepairCandidateAppliesSparsePatchWithoutTouchingLive(t *testing.T) {
	live, before := pipelineOutlineRepairTestFixture(t)
	beforeFiles := pipelineOutlineRepairReadViews(t, live)
	sourceRoot, err := pipelineOutlineAllSourceSnapshotRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := "oa-outline-repair-test"
	candidateDir := pipelineOutlineAllCandidatePath(live, attemptID)
	if err := preparePipelineOutlineAllCandidate(live, candidateDir, attemptID); err != nil {
		t.Fatal(err)
	}
	candidate := store.NewStore(candidateDir)
	digest, err := domain.ComputeLayeredOutlineDigest(before)
	if err != nil {
		t.Fatal(err)
	}
	newGoal := "在公开复盘中锁定第一弧的可验证成果与下一弧成本"
	manifest := pipelineOutlineRepairManifest{
		Version:               pipelineOutlineRepairManifestVersion,
		Reason:                "修正第一弧边界，并替换单个越界章节合同",
		ExpectedLayeredDigest: digest,
		Arcs: []pipelineOutlineArcRepair{
			{
				Volume: 1, Arc: 1, ExpectedStartChapter: 1, ExpectedEndChapter: 2,
				NewGoal: &newGoal,
				ChapterReplacements: []pipelineOutlineChapterReplacement{{
					Chapter: 2, Title: "第二章 新边界",
					CoreEvent: "林澈在公开复盘中拿出结算记录，主动删去越界方案并留下可核验的新承诺。",
					Hook:      "复盘刚结束，新的成本单已经压到桌角。",
					Scenes:    []string{"核对原始结算记录", "公开删去越界方案", "写下下一弧可核验承诺"},
				}},
			},
		},
	}
	manifestDigest, err := pipelineProjectAllDigestE(manifest)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := applyPipelineOutlineRepairCandidate(
		candidate, attemptID, sourceRoot, manifest, manifestDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.AppliedArcCount != 1 || receipt.ReplacedChapterCount != 1 ||
		receipt.BeforeLayeredDigest != digest || receipt.AfterLayeredDigest == digest {
		t.Fatalf("unexpected repair receipt: %+v", receipt)
	}
	if got := pipelineOutlineRepairReadViews(t, live); !reflect.DeepEqual(got, beforeFiles) {
		t.Fatal("candidate operation 0 changed live outline before directory publish")
	}
	after, err := candidate.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	if after[0].Title != before[0].Title || after[0].Arcs[0].Title != before[0].Arcs[0].Title ||
		after[0].Arcs[0].Goal != newGoal ||
		!reflect.DeepEqual(after[0].Arcs[0].ContractRefs, before[0].Arcs[0].ContractRefs) ||
		!reflect.DeepEqual(after[0].Arcs[0].Chapters[1].ContractRefs, before[0].Arcs[0].Chapters[1].ContractRefs) ||
		after[0].Arcs[0].Chapters[1].Chapter != before[0].Arcs[0].Chapters[1].Chapter {
		t.Fatalf("repair did not preserve structural identity/contracts: before=%+v after=%+v", before, after)
	}
	if !reflect.DeepEqual(after[0].Arcs[0].Chapters[0], before[0].Arcs[0].Chapters[0]) ||
		!reflect.DeepEqual(after[0].Arcs[1], before[0].Arcs[1]) {
		t.Fatal("repair changed a non-target chapter or arc")
	}
	flat, err := candidate.Outline.LoadOutline()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(flat, domain.FlattenOutline(after)) || flat[1].Chapter != 2 {
		t.Fatalf("flat outline was not synchronized: %+v", flat)
	}
	for _, rel := range []string{"layered_outline.md", "outline.md"} {
		raw, err := os.ReadFile(filepath.Join(candidateDir, rel))
		if err != nil || !strings.Contains(string(raw), "新边界") {
			t.Fatalf("%s was not synchronized: err=%v body=%s", rel, err, raw)
		}
	}
	if replay, err := applyPipelineOutlineRepairCandidate(
		candidate, attemptID, sourceRoot, manifest, manifestDigest,
	); err != nil || replay.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("idempotent operation 0 replay failed: receipt=%+v err=%v", replay, err)
	}
}

func TestPipelineOutlineRepairCASFailureLeavesCandidateViewsUntouched(t *testing.T) {
	live, _ := pipelineOutlineRepairTestFixture(t)
	sourceRoot, err := pipelineOutlineAllSourceSnapshotRoot(live)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := "oa-outline-repair-cas"
	candidateDir := pipelineOutlineAllCandidatePath(live, attemptID)
	if err := preparePipelineOutlineAllCandidate(live, candidateDir, attemptID); err != nil {
		t.Fatal(err)
	}
	beforeFiles := pipelineOutlineRepairReadViews(t, candidateDir)
	newGoal := "不会写入的目标"
	manifest := pipelineOutlineRepairManifest{
		Version: pipelineOutlineRepairManifestVersion, Reason: "验证 CAS 拒绝",
		ExpectedLayeredDigest: "sha256:" + strings.Repeat("a", 64),
		Arcs: []pipelineOutlineArcRepair{{
			Volume: 1, Arc: 1, ExpectedStartChapter: 1, ExpectedEndChapter: 2, NewGoal: &newGoal,
		}},
	}
	manifestDigest, err := pipelineProjectAllDigestE(manifest)
	if err != nil {
		t.Fatal(err)
	}
	_, err = applyPipelineOutlineRepairCandidate(
		store.NewStore(candidateDir), attemptID, sourceRoot, manifest, manifestDigest,
	)
	if err == nil || !strings.Contains(err.Error(), "layered CAS failed") {
		t.Fatalf("expected layered CAS rejection, got %v", err)
	}
	if got := pipelineOutlineRepairReadViews(t, candidateDir); !reflect.DeepEqual(got, beforeFiles) {
		t.Fatal("rejected repair changed an outline view")
	}
	for _, rel := range []string{pipelineOutlineRepairIntentPath, pipelineOutlineRepairReceiptPath} {
		if _, err := os.Lstat(filepath.Join(candidateDir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("rejected repair persisted %s: %v", rel, err)
		}
	}
}

func TestPipelineOutlineRepairRejectsBoundaryDrift(t *testing.T) {
	_, before := pipelineOutlineRepairTestFixture(t)
	digest, err := domain.ComputeLayeredOutlineDigest(before)
	if err != nil {
		t.Fatal(err)
	}
	newGoal := "边界错误时不得应用"
	manifest := pipelineOutlineRepairManifest{
		Version: pipelineOutlineRepairManifestVersion, Reason: "验证弧边界 CAS",
		ExpectedLayeredDigest: digest,
		Arcs: []pipelineOutlineArcRepair{{
			Volume: 1, Arc: 1, ExpectedStartChapter: 2, ExpectedEndChapter: 3, NewGoal: &newGoal,
		}},
	}
	_, _, err = buildPipelineOutlineRepairResult(before, manifest)
	if err == nil || !strings.Contains(err.Error(), "boundary CAS failed") {
		t.Fatalf("expected arc boundary rejection, got %v", err)
	}
}

func TestLoadPipelineOutlineRepairManifestIsStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repair.json")
	body := `{
  "version":"chapter-zero-outline-repair.v1",
  "reason":"strict parser",
  "expected_layered_digest":"sha256:` + strings.Repeat("b", 64) + `",
  "arcs":[{"volume":1,"arc":1,"expected_start_chapter":1,"expected_end_chapter":1,"new_goal":"new"}],
  "unexpected":true
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPipelineOutlineRepairManifest(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("strict manifest accepted unknown field: %v", err)
	}
}

func TestValidatePipelineOutlineRepairLiveEntryRejectsProseAndActiveLock(t *testing.T) {
	t.Run("committed prose", func(t *testing.T) {
		live, _ := pipelineOutlineRepairTestFixture(t)
		if err := os.WriteFile(filepath.Join(live, "chapters", "01.md"), []byte("正文"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := validatePipelineOutlineRepairLiveEntry(store.NewStore(live)); err == nil || !strings.Contains(err.Error(), "committed chapter") {
			t.Fatalf("repair accepted existing prose: %v", err)
		}
	})
	t.Run("active execution lock", func(t *testing.T) {
		live, _ := pipelineOutlineRepairTestFixture(t)
		st := store.NewStore(live)
		owner := "outline-repair-active-test"
		if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
			Mode: domain.PipelineExecutionFoundation, TargetChapter: 1,
			Owner: owner, ExpiresAt: time.Now().UTC().Add(time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Runtime.ReleasePipelineExecution(owner) }()
		if err := validatePipelineOutlineRepairLiveEntry(st); err == nil || !strings.Contains(err.Error(), "active execution lock") {
			t.Fatalf("repair accepted active lock: %v", err)
		}
	})
	t.Run("existing operation zero intent", func(t *testing.T) {
		live, _ := pipelineOutlineRepairTestFixture(t)
		path := filepath.Join(live, filepath.FromSlash(pipelineOutlineRepairIntentPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := validatePipelineOutlineRepairLiveEntry(store.NewStore(live)); err == nil ||
			!strings.Contains(err.Error(), "existing operation 0 intent") {
			t.Fatalf("repair accepted existing live operation 0 intent: %v", err)
		}
	})
}

func TestChapterZeroRebaseRecognizesPublishedOutlineAllAsRestartState(t *testing.T) {
	live, _ := pipelineOutlineRepairTestFixture(t)
	path := filepath.Join(live, filepath.FromSlash(store.OutlineAllExecutionReceiptPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	has, err := pipelineChapterZeroHasRestartState(live)
	if err != nil || !has {
		t.Fatalf("published outline-all was not treated as chapter-zero restart state: has=%v err=%v", has, err)
	}
}

func TestPipelineRunIdentityBindsOutlineRepairManifestDigest(t *testing.T) {
	base := pipelineRunIdentityDigest(pipelineFlags{Start: 1, End: 12})
	withRepair := pipelineRunIdentityDigest(pipelineFlags{
		Start: 1, End: 12, OutlineRepairDigest: "sha256:" + strings.Repeat("c", 64),
	})
	if withRepair == base {
		t.Fatal("outline repair manifest digest did not change pipeline run identity")
	}
}

func TestParsePipelineFlagsSupportsOutlineRepairFile(t *testing.T) {
	flags, extra, err := parsePipelineFlags([]string{
		"--outline-repair-file", "repairs/arc-one.json", "--stages", "outline-all",
	})
	if err != nil || len(extra) != 0 || flags.OutlineRepairFile != "repairs/arc-one.json" {
		t.Fatalf("outline repair flag parse failed: flags=%+v extra=%v err=%v", flags, extra, err)
	}
}

func TestPipelineRejectsOutlineRepairWithoutOutlineAllStage(t *testing.T) {
	err := pipelinePipeline(cliOptions{}, []string{
		"--outline-repair-file", filepath.Join(t.TempDir(), "missing.json"),
		"--stages", "preplan",
	})
	if err == nil || !strings.Contains(err.Error(), "仅可用于包含 outline-all") {
		t.Fatalf("pipeline accepted repair without outline-all stage: %v", err)
	}
}

func pipelineOutlineRepairTestFixture(t *testing.T) (string, []domain.VolumeOutline) {
	t.Helper()
	runRoot := t.TempDir()
	live := filepath.Join(runRoot, "output", "novel")
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	chapterRef := domain.StoryContractRef{
		ID: "chapter-contract", Kind: "non_negotiable", SourceDigest: "sha256:" + strings.Repeat("1", 64),
		PlannedPayoffChapter: 2, PlannedResolution: "第二章留下公开记录",
	}
	arcRef := domain.StoryContractRef{
		ID: "arc-contract", Kind: "open_thread", SourceDigest: "sha256:" + strings.Repeat("2", 64),
		PlannedPayoffChapter: 2, PlannedResolution: "第一弧完成可核验闭环",
	}
	volumes := []domain.VolumeOutline{{
		Index: 1, Title: "第一卷 原标题", Theme: "验证承诺",
		Arcs: []domain.ArcOutline{
			{
				Index: 1, Title: "第一弧 原标题", Goal: "原第一弧目标", ContractRefs: []domain.StoryContractRef{arcRef},
				Chapters: []domain.OutlineEntry{
					{Chapter: 101, Title: "第一章", CoreEvent: "林澈核对第一份记录并作出选择。", Hook: "旧账仍有缺口。", Scenes: []string{"核对记录", "作出选择"}},
					{Chapter: 102, Title: "第二章", CoreEvent: "林澈完成原有第二章行动。", Hook: "下一项成本浮现。", Scenes: []string{"完成行动", "发现成本"}, ContractRefs: []domain.StoryContractRef{chapterRef}},
				},
			},
			{
				Index: 2, Title: "第二弧 不可修改", Goal: "第二弧保持原样",
				Chapters: []domain.OutlineEntry{{Title: "第三章", CoreEvent: "林澈进入第二弧并保留旧合同。", Hook: "新的阻力出现。", Scenes: []string{"进入现场", "遭遇阻力"}}},
			},
		},
	}}
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SavePremise("一个关于承诺与验证的长篇故事。"); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "林澈", Role: "主角", Description: "负责验证承诺"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{{Category: "society", Rule: "承诺必须留痕", Boundary: "不得伪造记录"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveBookWorld(domain.BookWorld{Version: 1, Name: "测试县城", Summary: "公开记录决定信任"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "完成公开验证", NonNegotiables: []string{"承诺必须可核验"}, EstimatedScale: "1卷3章",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("outline-repair-test", domain.TotalChapters(volumes)); err != nil {
		t.Fatal(err)
	}
	if err := activatePipelineSealedTwoPassModeAtOutput(live); err != nil {
		t.Fatal(err)
	}
	if err := ensurePipelineOutlineAllRequirement(live); err != nil {
		t.Fatal(err)
	}
	return live, volumes
}

func pipelineOutlineRepairReadViews(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	for _, rel := range []string{"layered_outline.json", "layered_outline.md", "outline.json", "outline.md"} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		result[rel] = append([]byte(nil), raw...)
	}
	return result
}

func TestPipelineOutlineRepairManifestJSONExampleRoundTrips(t *testing.T) {
	goal := "新的弧目标"
	manifest := pipelineOutlineRepairManifest{
		Version: pipelineOutlineRepairManifestVersion, Reason: "示例",
		ExpectedLayeredDigest: "sha256:" + strings.Repeat("d", 64),
		Arcs: []pipelineOutlineArcRepair{{
			Volume: 1, Arc: 1, ExpectedStartChapter: 1, ExpectedEndChapter: 12, NewGoal: &goal,
			ChapterReplacements: []pipelineOutlineChapterReplacement{{
				Chapter: 12, Title: "弧尾", CoreEvent: "主角完成弧尾选择并留下可见后果。",
				Hook: "下一弧成本出现。", Scenes: []string{"完成选择", "留下后果"},
			}},
		}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "repair.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, digest, err := loadPipelineOutlineRepairManifest(path)
	if err != nil || !reflect.DeepEqual(loaded, manifest) || digest == "" {
		t.Fatalf("manifest round trip failed: loaded=%+v digest=%s err=%v", loaded, digest, err)
	}
}

func TestPublishedOutlineRepairAttemptReplaysThroughVerifier(t *testing.T) {
	liveDir := publishOutlineAllRepairGateFixture(t)
	if err := RequirePublishedOutlineAllIfPresent(liveDir); err != nil {
		t.Fatalf("published repair outline-all was blocked: %v", err)
	}
	artifacts, err := verifyPipelineOutlineAllReceiptAndArtifactsWithControlHeld(liveDir)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(artifacts, "\n")
	for _, rel := range []string{pipelineOutlineRepairIntentPath, pipelineOutlineRepairReceiptPath} {
		if !strings.Contains(joined, rel) {
			t.Fatalf("published verifier omitted operation 0 artifact %s: %v", rel, artifacts)
		}
	}
	st := store.NewStore(liveDir)
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil || receipt == nil {
		t.Fatalf("load published receipt: receipt=%+v err=%v", receipt, err)
	}
	repair, err := loadPipelineOutlineRepairEvidence(st, receipt.AttemptID, receipt.SourceSnapshotRoot)
	if err != nil || repair == nil {
		t.Fatalf("load published operation 0: repair=%+v err=%v", repair, err)
	}
	if receipt.SourceSnapshotRoot != outlineAllGateDigest || repair.SourceSnapshotRoot != receipt.SourceSnapshotRoot ||
		repair.AfterLayeredDigest != receipt.FinalLayeredDigest {
		t.Fatalf("repair/source/chain semantics drifted: outline=%+v repair=%+v", receipt, repair)
	}
}

func TestPublishedOutlineRepairVerifierRejectsMissingOperationZeroReceipt(t *testing.T) {
	liveDir := publishOutlineAllRepairGateFixture(t)
	if err := os.Remove(filepath.Join(liveDir, filepath.FromSlash(pipelineOutlineRepairReceiptPath))); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPipelineOutlineAllReceiptAndArtifactsWithControlHeld(liveDir); err == nil ||
		!strings.Contains(err.Error(), "incomplete operation 0") {
		t.Fatalf("verifier accepted missing operation 0 receipt: %v", err)
	}
}

func publishOutlineAllRepairGateFixture(t *testing.T) string {
	t.Helper()
	runRoot := t.TempDir()
	liveDir := filepath.Join(runRoot, "output", "novel")
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, "before.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectedLiveRoot, err := store.DirectoryContentRoot(liveDir)
	if err != nil {
		t.Fatal(err)
	}
	manifest := pipelineOutlineRepairGateManifest()
	manifestDigest, err := pipelineProjectAllDigestE(manifest)
	if err != nil {
		t.Fatal(err)
	}
	identity := domain.OutlineAllModelIdentity{
		CoordinatorProvider: "test", CoordinatorModel: "coordinator", CoordinatorReasoning: "high",
		ArchitectProvider: "test", ArchitectModel: "architect", ArchitectReasoning: "high",
	}
	modelDigest, err := domain.ComputeOutlineAllModelIdentityDigest(identity)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := outlineAllAttemptID(
		outlineAllGateDigest,
		modelDigest+"\n"+outlineAllGateDigest+"\noutline-repair="+manifestDigest,
	)
	candidateDir := pipelineOutlineAllCandidatePath(liveDir, attemptID)
	writeOutlineAllGateCompleteReceipt(t, candidateDir, candidateDir, expectedLiveRoot)
	writePipelineOutlineRepairGateEvidence(t, candidateDir, attemptID, manifest, manifestDigest)
	publisher := store.NewDirectoryPublishStore(pipelineOutlineAllPublishRoot(liveDir))
	published, err := publisher.PublishDirectory(store.PublishDirectoryRequest{
		TransactionID: attemptID, LiveDir: liveDir, CandidateDir: candidateDir,
		ExpectedLiveRoot: expectedLiveRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.FinalizeDirectoryPublish(attemptID); err != nil {
		t.Fatal(err)
	}
	live := store.NewStore(liveDir)
	receipt, err := live.LoadOutlineAllExecutionReceipt()
	if err != nil || receipt == nil {
		t.Fatalf("load promoted repair receipt: receipt=%+v err=%v", receipt, err)
	}
	if _, err := live.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		current.PublishedCandidateRoot = published.CandidateRoot
		current.DirectoryPublishReceiptDigest = published.ReceiptDigest
		current.UpdatedAt = time.Now().UTC()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return liveDir
}

func pipelineOutlineRepairGateManifest() pipelineOutlineRepairManifest {
	goal := "完成第一次真实改变，并把可验证后果交给下一弧"
	return pipelineOutlineRepairManifest{
		Version:               pipelineOutlineRepairManifestVersion,
		Reason:                "发布验证测试中的确定性 operation 0",
		ExpectedLayeredDigest: outlineAllGateDigest,
		Arcs: []pipelineOutlineArcRepair{{
			Volume: 1, Arc: 1, ExpectedStartChapter: 1, ExpectedEndChapter: 8, NewGoal: &goal,
		}},
	}
}

func writePipelineOutlineRepairGateEvidence(
	t *testing.T,
	candidateDir, attemptID string,
	manifest pipelineOutlineRepairManifest,
	manifestDigest string,
) {
	t.Helper()
	st := store.NewStore(candidateDir)
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	flat, err := st.Outline.LoadOutline()
	if err != nil {
		t.Fatal(err)
	}
	afterLayered, err := domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		t.Fatal(err)
	}
	afterFlat, err := domain.ComputeFlatOutlineDigest(flat)
	if err != nil {
		t.Fatal(err)
	}
	beforeFlat := "sha256:" + strings.Repeat("e", 64)
	intent, err := signPipelineOutlineRepairIntent(pipelineOutlineRepairIntent{
		Version: pipelineOutlineRepairIntentVersion, Operation: 0,
		AttemptID: attemptID, SourceSnapshotRoot: outlineAllGateDigest,
		Manifest: manifest, ManifestDigest: manifestDigest,
		BeforeLayeredDigest: manifest.ExpectedLayeredDigest, BeforeFlatDigest: beforeFlat,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writePipelinePlanningJSON(
		filepath.Join(candidateDir, filepath.FromSlash(pipelineOutlineRepairIntentPath)), intent,
	); err != nil {
		t.Fatal(err)
	}
	receipt, err := signPipelineOutlineRepairReceipt(pipelineOutlineRepairReceipt{
		Version: pipelineOutlineRepairReceiptVersion, Operation: 0,
		AttemptID: attemptID, SourceSnapshotRoot: outlineAllGateDigest,
		ManifestDigest: manifestDigest, IntentDigest: intent.IntentDigest,
		BeforeLayeredDigest: manifest.ExpectedLayeredDigest, BeforeFlatDigest: beforeFlat,
		AfterLayeredDigest: afterLayered, AfterFlatDigest: afterFlat,
		AppliedArcCount: len(manifest.Arcs), ReplacedChapterCount: pipelineOutlineRepairReplacementCount(manifest),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writePipelinePlanningJSON(
		filepath.Join(candidateDir, filepath.FromSlash(pipelineOutlineRepairReceiptPath)), receipt,
	); err != nil {
		t.Fatal(err)
	}
}
