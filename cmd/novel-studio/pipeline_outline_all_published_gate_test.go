package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const outlineAllGateDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func outlineAllGateOutlines() ([]domain.VolumeOutline, []domain.OutlineEntry) {
	compass := outlineAllGateCompass()
	refs := domain.BuildStoryContractRegistry(compass)
	for index := range refs {
		refs[index].PlannedPayoffChapter = 8
		refs[index].PlannedResolution = []string{
			"林澈召集居民完成公开表决并把青山县最终治理权交给居民议会",
			"沈知遥公开全部补偿账本并确保普通商户获得足额返还和常设席位",
			"审计小组公开冻结款完整流向并由涉事商会退回全部占用资金",
		}[index]
	}
	flat := make([]domain.OutlineEntry, 0, 8)
	for chapter := 1; chapter <= 8; chapter++ {
		entry := domain.OutlineEntry{
			Chapter: chapter, Title: fmt.Sprintf("第%d次现场核验", chapter),
			CoreEvent: fmt.Sprintf("林澈在第%d处现场遭遇商会阻断后重排证据顺序并让一笔冻结款项恢复可追踪状态", chapter),
			Hook:      fmt.Sprintf("第%d份新账单迫使居民在下一次会议前执行具体补偿", chapter),
			Scenes: []string{
				fmt.Sprintf("林澈在第%d处柜台核对票据和签名", chapter),
				fmt.Sprintf("沈知遥在第%d次会议指出执行边界", chapter),
				fmt.Sprintf("居民代表用第%d份回执确认状态变化", chapter),
			},
		}
		if chapter == 8 {
			entry.CoreEvent += "；" + refs[0].PlannedResolution
			entry.Scenes = append(entry.Scenes, refs[1].PlannedResolution, refs[2].PlannedResolution)
			entry.ContractRefs = refs
		}
		flat = append(flat, entry)
	}
	return []domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Theme: "验证规则",
		Arcs: []domain.ArcOutline{{
			Index:    1,
			Title:    "起步弧",
			Goal:     "完成第一次真实改变",
			Chapters: flat, ContractRefs: refs,
		}},
	}}, flat
}

func outlineAllGateCompass() domain.StoryCompass {
	return domain.StoryCompass{
		EndingDirection: "青山县居民通过公开表决掌握最终治理权",
		NonNegotiables:  []string{"普通商户必须得到足额补偿和常设议事席位"},
		OpenThreads:     []string{"旧账本背后的冻结款去向必须公开结清"},
		EstimatedScale:  "1-1卷，8-8章",
	}
}

func outlineAllGateAttemptID() string {
	identity := domain.OutlineAllModelIdentity{
		CoordinatorProvider: "test", CoordinatorModel: "coordinator", CoordinatorReasoning: "high",
		ArchitectProvider: "test", ArchitectModel: "architect", ArchitectReasoning: "high",
	}
	modelDigest, _ := domain.ComputeOutlineAllModelIdentityDigest(identity)
	return outlineAllAttemptID(outlineAllGateDigest, modelDigest+"\n"+outlineAllGateDigest)
}

func outlineAllGateLiveDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "output", "novel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeOutlineAllGateCompleteReceipt(
	t *testing.T,
	outputDir string,
	candidateDir string,
	expectedLiveRoot string,
) domain.OutlineAllExecutionReceipt {
	t.Helper()
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SavePremise("林澈与沈知遥在青山县用公开账本夺回居民治理权，并让每次补偿都能被现场核验。"); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Description: "负责核验票据并承担公开决策代价", Arc: "从个人核验走向居民共治", Traits: []string{"谨慎", "负责"}, Tier: "core"},
		{Name: "沈知遥", Role: "搭档", Description: "负责补偿边界和商户代表沟通", Arc: "从独立审计走向共同承担", Traits: []string{"直接", "克制"}, Tier: "core"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{{Category: "ledger", Rule: "任何补偿和治理表决必须留下公开票据与签名", Boundary: "口头承诺不能替代回执"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveBookWorld(domain.BookWorld{Version: 1, Name: "青山县", Summary: "围绕公开账本和居民议事权运转的县城"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorldCodex(zeroInitTestWorldCodex()); err != nil {
		t.Fatal(err)
	}
	compass := outlineAllGateCompass()
	if err := st.Outline.SaveCompass(compass); err != nil {
		t.Fatal(err)
	}
	if err := activatePipelineSealedTwoPassModeAtOutput(outputDir); err != nil {
		t.Fatal(err)
	}
	volumes, flat := outlineAllGateOutlines()
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline(flat); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{
		GenerationID:  "gate-generation",
		TotalChapters: 8,
	}); err != nil {
		t.Fatal(err)
	}
	readiness := assessArchitectReadiness(outputDir)
	if !readiness.Ready {
		t.Fatalf("gate architect readiness: missing=%v issues=%v", readiness.Missing, readiness.Issues)
	}
	if err := writeArchitectReadiness(outputDir, readiness); err != nil {
		t.Fatal(err)
	}
	layeredDigest, err := domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		t.Fatal(err)
	}
	flatDigest, err := domain.ComputeFlatOutlineDigest(flat)
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
	now := time.Now().UTC()
	mode, err := st.LoadWritingPipelineMode()
	if err != nil || mode == nil {
		t.Fatalf("load gate writing mode: %+v %v", mode, err)
	}
	compassDigest, err := domain.ComputeStoryCompassDigest(compass)
	if err != nil {
		t.Fatal(err)
	}
	protectedRoot, err := pipelineOutlineAllProtectedCanonRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	stableRoot, err := pipelineOutlineAllStableProgressRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := loadPipelineOutlineAllFrozenFoundation(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	readinessJSON, err := pipelineRequiredFileSHA(outputDir, "meta/architect_readiness.json")
	if err != nil {
		t.Fatal(err)
	}
	readinessMD, err := pipelineRequiredFileSHA(outputDir, "meta/architect_readiness.md")
	if err != nil {
		t.Fatal(err)
	}
	attemptID := filepath.Base(filepath.Dir(filepath.Dir(filepath.Clean(candidateDir))))
	receipt := domain.OutlineAllExecutionReceipt{
		Version: domain.OutlineAllExecutionReceiptVersion, Mode: domain.OutlineAllExecutionMode,
		Status: domain.OutlineAllExecutionComplete, BaseCanonChapter: 0, GenerationID: "gate-generation",
		WritingMode: mode.Mode, WritingModeReceiptDigest: mode.ReceiptDigest, CompassDigest: compassDigest,
		EstimatedScale: compass.EstimatedScale, EndingDirection: compass.EndingDirection,
		NonNegotiables: compass.NonNegotiables,
		MinVolumes:     1, MaxVolumes: 1, MinChapters: 8, MaxChapters: 8,
		TargetVolumes: 1, TargetChapters: 8,
		SourceSnapshotRoot: outlineAllGateDigest, ProtectedCanonRoot: protectedRoot,
		StableProgressRoot: stableRoot, FoundationContextRoot: foundation.Root,
		AttemptID: attemptID, CandidateDir: candidateDir,
		CoordinatorProvider: identity.CoordinatorProvider, CoordinatorModel: identity.CoordinatorModel,
		CoordinatorReasoning: identity.CoordinatorReasoning,
		ArchitectProvider:    identity.ArchitectProvider, ArchitectModel: identity.ArchitectModel,
		ArchitectReasoning:  identity.ArchitectReasoning,
		ModelIdentityDigest: modelDigest, PromptProtocolDigest: outlineAllGateDigest,
		FinalLayeredDigest: layeredDigest, FinalFlatDigest: flatDigest,
		ArchitectReadinessJSONDigest: readinessJSON,
		ArchitectReadinessMDDigest:   readinessMD,
		ExpectedLiveDirectoryRoot:    expectedLiveRoot,
		LockVersion:                  1, LockMode: domain.PipelineExecutionOutlineAll, LockTargetChapter: 1,
		LockOwner: "outline-all-gate-test", LockProcessID: 1,
		LockAcquiredAt: now, LockExpiresAt: now.Add(time.Hour),
		StartedAt: now, UpdatedAt: now,
	}
	receipt, err = domain.SignOutlineAllExecutionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOutlineAllExecutionReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func publishOutlineAllGateFixture(t *testing.T, finalize bool) (string, *store.Store) {
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
	attemptID := outlineAllGateAttemptID()
	candidateDir := pipelineOutlineAllCandidatePath(liveDir, attemptID)
	writeOutlineAllGateCompleteReceipt(t, candidateDir, candidateDir, expectedLiveRoot)
	publisher := store.NewDirectoryPublishStore(pipelineOutlineAllPublishRoot(liveDir))
	published, err := publisher.PublishDirectory(store.PublishDirectoryRequest{
		TransactionID: attemptID, LiveDir: liveDir, CandidateDir: candidateDir,
		ExpectedLiveRoot: expectedLiveRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if finalize {
		if err := publisher.FinalizeDirectoryPublish(attemptID); err != nil {
			t.Fatal(err)
		}
	}
	live := store.NewStore(liveDir)
	receipt, err := live.LoadOutlineAllExecutionReceipt()
	if err != nil || receipt == nil {
		t.Fatalf("load promoted outline-all receipt: %+v %v", receipt, err)
	}
	if _, err := live.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		current.PublishedCandidateRoot = published.CandidateRoot
		current.DirectoryPublishReceiptDigest = published.ReceiptDigest
		current.UpdatedAt = time.Now().UTC()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return liveDir, live
}

func TestRequirePublishedOutlineAllIfPresentAllowsLegacyProjectWithoutReceipt(t *testing.T) {
	dir := outlineAllGateLiveDir(t)
	if err := RequirePublishedOutlineAllIfPresent(dir); err != nil {
		t.Fatalf("legacy project was blocked: %v", err)
	}
}

func TestRequirePublishedOutlineAllIfPresentRejectsPersistentRequirementWithoutLiveReceipt(t *testing.T) {
	dir := outlineAllGateLiveDir(t)
	if err := ensurePipelineOutlineAllRequirement(dir); err != nil {
		t.Fatal(err)
	}
	if err := RequirePublishedOutlineAllIfPresent(dir); err == nil || !strings.Contains(err.Error(), "persistently required") {
		t.Fatalf("persistent requirement without receipt passed: %v", err)
	}
}

func TestEnsurePipelineOutlineAllRequirementRepairsEitherMissingMirror(t *testing.T) {
	dir := outlineAllGateLiveDir(t)
	stable := filepath.Join(pipelineOutlineAllControlRoot(dir), pipelineOutlineAllRunRequirementName)
	live := filepath.Join(dir, filepath.FromSlash(pipelineOutlineAllRequirementPath))
	if err := ensurePipelineOutlineAllRequirement(dir); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{live, stable} {
		if err := os.Remove(missing); err != nil {
			t.Fatal(err)
		}
		if err := ensurePipelineOutlineAllRequirement(dir); err != nil {
			t.Fatalf("repair %s: %v", missing, err)
		}
		for _, path := range []string{stable, live} {
			if exists, err := validatePipelineOutlineAllRequirementAt(path); err != nil || !exists {
				t.Fatalf("mirror %s not healed: exists=%v err=%v", path, exists, err)
			}
		}
	}
}

func TestRequirePublishedOutlineAllIfPresentReadsStableRequirementWhileLiveIsMoved(t *testing.T) {
	dir := outlineAllGateLiveDir(t)
	if err := ensurePipelineOutlineAllRequirement(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, dir+".archived"); err != nil {
		t.Fatal(err)
	}
	if err := RequirePublishedOutlineAllIfPresent(dir); err == nil || !strings.Contains(err.Error(), "persistently required") {
		t.Fatalf("stable run-root requirement did not protect the live-moved window: %v", err)
	}
}

func TestRequirePublishedOutlineAllIfPresentRejectsActiveLiveLockWithoutReceipt(t *testing.T) {
	dir := outlineAllGateLiveDir(t)
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	const owner = "outline-all-live-gate-test"
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionOutlineAll, TargetChapter: 1,
		Owner: owner, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil {
			t.Errorf("release live outline-all lock: %v", err)
		}
	}()
	if err := RequirePublishedOutlineAllIfPresent(dir); err == nil || !strings.Contains(err.Error(), "live execution lock") {
		t.Fatalf("active outline-all lock without receipt passed: %v", err)
	}
}

func TestRequirePublishedOutlineAllIfPresentRejectsCandidateReceiptWithoutLiveReceipt(t *testing.T) {
	runRoot := t.TempDir()
	liveDir := filepath.Join(runRoot, "output", "novel")
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidateDir := pipelineOutlineAllCandidatePath(liveDir, "oa-gate-test")
	writeOutlineAllGateCompleteReceipt(t, candidateDir, candidateDir, outlineAllGateDigest)
	if err := RequirePublishedOutlineAllIfPresent(liveDir); err == nil || !strings.Contains(err.Error(), "is complete but is not published") {
		t.Fatalf("candidate receipt without live receipt passed: %v", err)
	}
}

func TestRequirePublishedOutlineAllIfPresentRejectsUnfinishedPublishWithoutLiveReceipt(t *testing.T) {
	runRoot := t.TempDir()
	liveDir := filepath.Join(runRoot, "output", "novel")
	candidateDir := pipelineOutlineAllCandidatePath(liveDir, "oa-gate-test")
	for _, dir := range []string{liveDir, candidateDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(liveDir, "before.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "candidate.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expectedLiveRoot, err := store.DirectoryContentRoot(liveDir)
	if err != nil {
		t.Fatal(err)
	}
	publisher := store.NewDirectoryPublishStore(pipelineOutlineAllPublishRoot(liveDir))
	if _, err := publisher.PublishDirectory(store.PublishDirectoryRequest{
		TransactionID: "oa-gate-test", LiveDir: liveDir, CandidateDir: candidateDir,
		ExpectedLiveRoot: expectedLiveRoot,
	}); err != nil {
		t.Fatal(err)
	}
	if err := RequirePublishedOutlineAllIfPresent(liveDir); err == nil || !strings.Contains(err.Error(), "directory transaction") {
		t.Fatalf("unfinished publish without live receipt passed: %v", err)
	}
}

func TestRequirePublishedOutlineAllIfPresentRejectsCompleteUnpublishedReceipt(t *testing.T) {
	runRoot := t.TempDir()
	liveDir := filepath.Join(runRoot, "output", "novel")
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidateDir := pipelineOutlineAllCandidatePath(liveDir, "oa-gate-test")
	writeOutlineAllGateCompleteReceipt(t, liveDir, candidateDir, outlineAllGateDigest)
	if err := RequirePublishedOutlineAllIfPresent(liveDir); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("complete but unpublished receipt passed: %v", err)
	}
}

func TestRequirePublishedOutlineAllIfPresentValidatesFinalizedTransaction(t *testing.T) {
	liveDir, _ := publishOutlineAllGateFixture(t, true)
	if err := RequirePublishedOutlineAllIfPresent(liveDir); err != nil {
		t.Fatalf("valid finalized outline-all publish was blocked: %v", err)
	}
}

func TestRequirePublishedOutlineAllIfPresentRejectsUnfinalizedTransaction(t *testing.T) {
	liveDir, _ := publishOutlineAllGateFixture(t, false)
	if err := RequirePublishedOutlineAllIfPresent(liveDir); err == nil {
		t.Fatal("receipt-written outline-all transaction passed as finalized")
	}
}

func TestRequirePublishedOutlineAllIfPresentRejectsOutlineAndBindingDrift(t *testing.T) {
	t.Run("flat outline", func(t *testing.T) {
		liveDir, live := publishOutlineAllGateFixture(t, true)
		_, flat := outlineAllGateOutlines()
		flat[0].Hook = "被篡改的后续行动"
		if err := live.Outline.SaveOutline(flat); err != nil {
			t.Fatal(err)
		}
		if err := RequirePublishedOutlineAllIfPresent(liveDir); err == nil || !strings.Contains(err.Error(), "flat outline digest drift") {
			t.Fatalf("flat outline drift passed: %v", err)
		}
	})
	t.Run("candidate namespace", func(t *testing.T) {
		liveDir, live := publishOutlineAllGateFixture(t, true)
		receipt, err := live.LoadOutlineAllExecutionReceipt()
		if err != nil || receipt == nil {
			t.Fatal(err)
		}
		if _, err := live.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
			current.CandidateDir = filepath.Join(t.TempDir(), "output", "novel")
			current.UpdatedAt = time.Now().UTC()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := RequirePublishedOutlineAllIfPresent(liveDir); err == nil || !strings.Contains(err.Error(), "candidate directory") {
			t.Fatalf("candidate namespace drift passed: %v", err)
		}
	})
	t.Run("publish receipt digest", func(t *testing.T) {
		liveDir, live := publishOutlineAllGateFixture(t, true)
		receipt, err := live.LoadOutlineAllExecutionReceipt()
		if err != nil || receipt == nil {
			t.Fatal(err)
		}
		if _, err := live.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
			current.DirectoryPublishReceiptDigest = outlineAllGateDigest
			current.UpdatedAt = time.Now().UTC()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := RequirePublishedOutlineAllIfPresent(liveDir); err == nil || !strings.Contains(err.Error(), "roots or receipt digest") {
			t.Fatalf("publish receipt digest drift passed: %v", err)
		}
	})
	t.Run("progress generation", func(t *testing.T) {
		liveDir, live := publishOutlineAllGateFixture(t, true)
		progress, err := live.Progress.Load()
		if err != nil || progress == nil {
			t.Fatal(err)
		}
		progress.GenerationID = "drifted-generation"
		if err := live.Progress.Save(progress); err != nil {
			t.Fatal(err)
		}
		if err := RequirePublishedOutlineAllIfPresent(liveDir); err == nil || !strings.Contains(err.Error(), "generation_id") {
			t.Fatalf("progress generation drift passed: %v", err)
		}
	})
	t.Run("progress total", func(t *testing.T) {
		liveDir, live := publishOutlineAllGateFixture(t, true)
		progress, err := live.Progress.Load()
		if err != nil || progress == nil {
			t.Fatal(err)
		}
		progress.TotalChapters = 2
		if err := live.Progress.Save(progress); err != nil {
			t.Fatal(err)
		}
		if err := RequirePublishedOutlineAllIfPresent(liveDir); err == nil || !strings.Contains(err.Error(), "total_chapters") {
			t.Fatalf("progress total drift passed: %v", err)
		}
	})
}

func TestPublishedOutlineAllChapterZeroGateRejectsCanonProgress(t *testing.T) {
	liveDir, live := publishOutlineAllGateFixture(t, true)
	progress, err := live.Progress.Load()
	if err != nil || progress == nil {
		t.Fatal(err)
	}
	progress.CurrentChapter = 1
	progress.CompletedChapters = []int{1}
	progress.TotalWordCount = 1234
	progress.ChapterWordCounts = map[int]int{1: 1234}
	if err := live.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, "meta", "ch01_zero_init_plan.md"), []byte("zero-init completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, release, err := acquirePublishedOutlineAllStageAtOutput(liveDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if err := requirePublishedOutlineAllChapterZeroProgressWithControlHeld(liveDir); err == nil || !strings.Contains(err.Error(), "before chapter 1") {
		t.Fatalf("chapter-zero gate allowed canonical progress: %v", err)
	}
}

func TestAcquirePublishedOutlineAllStageRetainsExclusiveControlUntilRelease(t *testing.T) {
	liveDir := outlineAllGateLiveDir(t)
	_, releaseOuter, err := acquirePublishedOutlineAllStageAtOutput(liveDir)
	if err != nil {
		t.Fatal(err)
	}
	_, releaseNested, err := acquirePublishedOutlineAllStageAtOutput(liveDir)
	if err != nil {
		t.Fatalf("nested downstream exclusive lease self-blocked: %v", err)
	}
	lockPath := filepath.Join(pipelineOutlineAllControlRoot(liveDir), "control.lock")
	pipelineOutlineAllControlMu.Lock()
	held := pipelineOutlineAllHeldControls[lockPath]
	if held == nil || !held.exclusive || held.refs != 2 {
		pipelineOutlineAllControlMu.Unlock()
		t.Fatalf("exclusive lease refs=%+v", held)
	}
	pipelineOutlineAllControlMu.Unlock()
	if err := releaseNested(); err != nil {
		t.Fatal(err)
	}
	if err := releaseOuter(); err != nil {
		t.Fatal(err)
	}
	pipelineOutlineAllControlMu.Lock()
	held = pipelineOutlineAllHeldControls[lockPath]
	pipelineOutlineAllControlMu.Unlock()
	if held != nil {
		t.Fatalf("exclusive control remained after final release: %+v", held)
	}
}

func TestPublishedOutlineAllGateIsWiredIntoEveryDownstreamEntryAndVerifier(t *testing.T) {
	runRoot := t.TempDir()
	outputDir := filepath.Join(runRoot, "output", "novel")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(runRoot, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "provider": "ollama",
  "model": "outline-all-gate-test",
  "providers": {"ollama": {"type": "openai", "base_url": "http://127.0.0.1:11434/v1"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := cliOptions{ConfigPath: configPath, Dir: runRoot}
	sentinel := errors.New("published outline-all gate sentinel")
	original := requirePublishedOutlineAllIfPresent
	requirePublishedOutlineAllIfPresent = func(got string) error {
		if filepath.Clean(got) != filepath.Clean(outputDir) {
			t.Fatalf("gate output dir=%s want=%s", got, outputDir)
		}
		lockPath := filepath.Join(pipelineOutlineAllControlRoot(got), "control.lock")
		pipelineOutlineAllControlMu.Lock()
		held := pipelineOutlineAllHeldControls[lockPath]
		pipelineOutlineAllControlMu.Unlock()
		if held == nil || !held.exclusive {
			t.Fatal("downstream gate did not retain an exclusive outline-all control lease")
		}
		return sentinel
	}
	defer func() { requirePublishedOutlineAllIfPresent = original }()

	entries := []struct {
		name string
		run  func() error
	}{
		{"zero-init-command", func() error { return zeroInitPipeline(opts, []string{"--dir", outputDir, "--check"}) }},
		{"zero-init-stage", func() error { return pipelineZeroInit(opts, pipelineFlags{}, &domain.PipelineState{}) }},
		{"preplan", func() error { return pipelinePreplan(opts, pipelineFlags{}) }},
		{"project-all", func() error { return pipelineProjectAll(opts, pipelineFlags{}) }},
		{"seal", func() error { return pipelineSeal(opts, pipelineFlags{}) }},
		{"promote", func() error { return pipelinePromote(opts, pipelineFlags{}) }},
		{"render", func() error { return pipelineRender(opts, pipelineFlags{}, &domain.PipelineState{}) }},
	}
	for _, tc := range entries {
		t.Run("entry/"+tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, sentinel) {
				t.Fatalf("%s did not stop at published outline-all gate: %v", tc.name, err)
			}
		})
	}
	for _, stage := range []string{"zero-init", "preplan", "project-all", "seal", "promote", "render"} {
		t.Run("verifier/"+stage, func(t *testing.T) {
			if _, err := verifyPipelineStage(stage, outputDir, pipelineFlags{}, &domain.PipelineState{}); !errors.Is(err, sentinel) {
				t.Fatalf("%s verifier did not stop at published outline-all gate: %v", stage, err)
			}
		})
	}
}

func TestRunPipelineDownstreamOuterControlPrecedesRenderRecoveryAndConfigLoader(t *testing.T) {
	runRoot := t.TempDir()
	outputDir := filepath.Join(runRoot, "output", "novel")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(runRoot, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "provider": "ollama",
  "model": "outline-all-outer-control-test",
  "providers": {"ollama": {"type": "openai", "base_url": "http://127.0.0.1:11434/v1"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	controlRoot := pipelineOutlineAllControlRoot(outputDir)
	if err := os.MkdirAll(controlRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	lockFile, err := os.OpenFile(filepath.Join(controlRoot, "control.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	err = runPipelineWithStages(
		cliOptions{ConfigPath: configPath, Dir: runRoot},
		pipelineFlags{Stages: "render"},
		[]string{"render"},
		"",
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "control is busy") {
		t.Fatalf("downstream pipeline did not stop at outer exclusive control: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outputDir, "meta", "prompt_manifest.json")); !os.IsNotExist(statErr) {
		t.Fatalf("config loader wrote prompt_manifest before outer exclusive control: %v", statErr)
	}
}

func TestDownstreamPublishedGateRecoversRebaseLiveArchivedBeforeConfigLoad(t *testing.T) {
	fixture := newPipelineRebaseRecoveryFixture(t, store.DirectoryPublishLiveArchived)
	if _, err := os.Lstat(fixture.live); !os.IsNotExist(err) {
		t.Fatalf("fixture live should be absent before recovery: %v", err)
	}
	_, release, err := acquirePublishedOutlineAllStageForInvocation(fixture.opts)
	if err != nil {
		t.Fatalf("downstream pre-load gate did not recover rebase: %v", err)
	}
	defer func() { _ = release() }()
	state, err := fixture.publisher.LoadDirectoryPublishState(fixture.transactionID)
	if err != nil || state == nil || state.Phase != store.DirectoryPublishFinalized {
		t.Fatalf("rebase transaction not finalized before downstream load: state=%+v err=%v", state, err)
	}
	body, err := os.ReadFile(filepath.Join(fixture.live, "canon.txt"))
	if err != nil || string(body) != "chapter zero canon\n" {
		t.Fatalf("restored live body=%q err=%v", body, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.live, "meta", "prompt_manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("pre-load recovery fabricated prompt_manifest: %v", err)
	}
}
