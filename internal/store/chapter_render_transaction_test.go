package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func chapterRenderStoreDigest(seed string) string {
	return domain.ComputeArcArtifactSHA256([]byte(seed))
}

func chapterRenderStoreFixture(t *testing.T) (
	string,
	*ChapterRenderTransactionStore,
	domain.ChapterRenderBodyIdentity,
	[]byte,
) {
	t.Helper()
	base := t.TempDir()
	outputDir := filepath.Join(base, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("# 第一章\n\n许知夏把回执翻到背面，那里只剩一道被雨水洇开的笔迹。")
	plan := domain.ChapterRenderPlanIdentity{
		Version:                domain.ChapterRenderPlanIdentityVersion,
		ProtocolVersion:        "sealed-chapter-render.v1",
		GenerationID:           "pg2_store-fixture",
		Chapter:                1,
		PlanDigest:             chapterRenderStoreDigest("plan"),
		PlanCheckpointSeq:      8,
		ProjectedBundleDigest:  chapterRenderStoreDigest("bundle"),
		PromotionReceiptDigest: chapterRenderStoreDigest("promotion"),
		PipelineRunInputDigest: chapterRenderStoreDigest("run-input"),
		RenderContextSHA256:    chapterRenderStoreDigest("render-context"),
	}
	identity, err := domain.NewChapterRenderBodyIdentity(
		plan,
		domain.ComputeChapterRenderBodySHA256(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	return outputDir, NewChapterRenderTransactionStore(outputDir), identity, body
}

func bodyReadyEvidence(identity domain.ChapterRenderBodyIdentity) domain.ChapterRenderPhaseEvidence {
	return domain.ChapterRenderPhaseEvidence{
		BodyCheckpointSeq:    9,
		BodyCheckpointDigest: identity.BodySHA256,
	}
}

func committedEvidence(identity domain.ChapterRenderBodyIdentity) domain.ChapterRenderPhaseEvidence {
	return domain.ChapterRenderPhaseEvidence{
		CommitCheckpointSeq: 10,
		CommitDigest:        identity.BodySHA256,
		CandidateRoot:       chapterRenderStoreDigest("candidate-root"),
	}
}

func TestChapterRenderTransactionStorePersistsMonotonicReceiptChain(t *testing.T) {
	outputDir, transactions, identity, body := chapterRenderStoreFixture(t)
	wantParent := filepath.Join(filepath.Dir(outputDir), chapterRenderTransactionRootName)
	if filepath.Dir(transactions.Root()) != wantParent || filepath.Base(transactions.Root()) == "" {
		t.Fatalf("transaction root=%q want namespaced sibling under %q", transactions.Root(), wantParent)
	}

	bodyReceipt, err := transactions.BeginBody(identity, body, bodyReadyEvidence(identity))
	if err != nil {
		t.Fatal(err)
	}
	commitReceipt, err := transactions.Advance(identity, domain.ChapterRenderPhaseCommitted, committedEvidence(identity))
	if err != nil {
		t.Fatal(err)
	}
	reviewReceipt, err := transactions.Advance(identity, domain.ChapterRenderPhaseFormalAccepted, domain.ChapterRenderPhaseEvidence{
		ReviewVerdict:     "accept",
		ReviewDisposition: "否",
		ReviewArtifacts: []domain.ChapterRenderArtifactBinding{
			{Path: "reviews/01.md", Digest: chapterRenderStoreDigest("review-md")},
			{Path: "reviews/01.json", Digest: chapterRenderStoreDigest("review-json")},
		},
		EditorCacheKey:   strings.Repeat("a", 64),
		DeepSeekCacheKey: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	actualReceipt, err := transactions.Advance(identity, domain.ChapterRenderPhaseActualMatched, domain.ChapterRenderPhaseEvidence{
		ActualMatchDigest: chapterRenderStoreDigest("actual-match"),
	})
	if err != nil {
		t.Fatal(err)
	}
	publishReceipt, err := transactions.Advance(identity, domain.ChapterRenderPhasePublished, domain.ChapterRenderPhaseEvidence{
		DirectoryPublishID:     "render-ch0001-test",
		DirectoryPublishDigest: chapterRenderStoreDigest("directory-publish"),
	})
	if err != nil {
		t.Fatal(err)
	}
	outcomeReceipt, err := transactions.Advance(identity, domain.ChapterRenderPhaseOutcomeAccepted, domain.ChapterRenderPhaseEvidence{
		OutcomeReceiptDigest: chapterRenderStoreDigest("outcome"),
	})
	if err != nil {
		t.Fatal(err)
	}
	acceptanceReceipt, err := transactions.Advance(identity, domain.ChapterRenderPhaseChapterAccepted, domain.ChapterRenderPhaseEvidence{
		ChapterAcceptanceDigest: chapterRenderStoreDigest("acceptance"),
	})
	if err != nil {
		t.Fatal(err)
	}
	completedReceipt, err := transactions.Advance(identity, domain.ChapterRenderPhaseCompleted, domain.ChapterRenderPhaseEvidence{
		RenderReceiptDigest: chapterRenderStoreDigest("render-receipt"),
	})
	if err != nil {
		t.Fatal(err)
	}

	chain := []*domain.ChapterRenderPhaseReceipt{
		bodyReceipt, commitReceipt, reviewReceipt, actualReceipt,
		publishReceipt, outcomeReceipt, acceptanceReceipt, completedReceipt,
	}
	for i := 1; i < len(chain); i++ {
		if chain[i].PreviousReceiptDigest != chain[i-1].ReceiptDigest {
			t.Fatalf("receipt %d does not bind predecessor: %+v", i, chain[i])
		}
	}
	receipts, err := transactions.LoadReceipts(identity)
	if err != nil || len(receipts) != len(chain) {
		t.Fatalf("load receipt chain: count=%d err=%v", len(receipts), err)
	}
	latest, err := transactions.LoadLatest(identity)
	if err != nil || latest == nil || latest.Phase != domain.ChapterRenderPhaseCompleted ||
		latest.ReceiptDigest != completedReceipt.ReceiptDigest {
		t.Fatalf("latest receipt=%+v err=%v", latest, err)
	}
	loadedBody, err := transactions.LoadBody(identity)
	if err != nil || string(loadedBody) != string(body) {
		t.Fatalf("load immutable body=%q err=%v", loadedBody, err)
	}
	if _, err := transactions.Advance(identity, domain.ChapterRenderPhaseStructurallyBlocked, domain.ChapterRenderPhaseEvidence{Reason: "late block"}); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("completed transaction advanced: %v", err)
	}
}

func TestChapterRenderTransactionSiblingOutputsAreIsolated(t *testing.T) {
	base := t.TempDir()
	firstOutput := filepath.Join(base, "novel")
	secondOutput := filepath.Join(base, "novel-clone")
	for _, outputDir := range []string{firstOutput, secondOutput} {
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, _, identity, body := chapterRenderStoreFixture(t)
	first := NewChapterRenderTransactionStore(firstOutput)
	second := NewChapterRenderTransactionStore(secondOutput)
	if first.Root() == second.Root() {
		t.Fatalf("sibling outputs share transaction namespace %q", first.Root())
	}
	if filepath.Dir(first.Root()) != filepath.Dir(second.Root()) {
		t.Fatalf("sibling namespaces do not share the durable container: first=%q second=%q", first.Root(), second.Root())
	}
	if _, err := first.BeginBody(identity, body, bodyReadyEvidence(identity)); err != nil {
		t.Fatal(err)
	}
	firstReceipts, err := first.LoadReceipts(identity)
	if err != nil || len(firstReceipts) != 1 {
		t.Fatalf("first output receipts=%+v err=%v", firstReceipts, err)
	}
	secondReceipts, err := second.LoadReceipts(identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondReceipts) != 0 {
		t.Fatalf("clone adopted sibling receipts: %+v", secondReceipts)
	}
	listed, err := second.ListPlanBodies(identity.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("clone discovered sibling bodies: %+v", listed)
	}
	if _, err := second.BeginBody(identity, body, bodyReadyEvidence(identity)); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Advance(identity, domain.ChapterRenderPhaseCommitted, committedEvidence(identity)); err != nil {
		t.Fatal(err)
	}
	secondLatest, err := second.LoadLatest(identity)
	if err != nil || secondLatest == nil || secondLatest.Phase != domain.ChapterRenderPhaseCommitted {
		t.Fatalf("second output independent chain=%+v err=%v", secondLatest, err)
	}
	firstLatest, err := first.LoadLatest(identity)
	if err != nil || firstLatest == nil || firstLatest.Phase != domain.ChapterRenderPhaseBodyReady {
		t.Fatalf("second output mutated first chain=%+v err=%v", firstLatest, err)
	}
}

func TestChapterRenderTransactionSamePhaseIsIdempotentAndConflictFails(t *testing.T) {
	_, transactions, identity, body := chapterRenderStoreFixture(t)
	firstBody, err := transactions.BeginBody(identity, body, bodyReadyEvidence(identity))
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := transactions.BeginBody(identity, append([]byte(nil), body...), bodyReadyEvidence(identity))
	if err != nil || secondBody.ReceiptDigest != firstBody.ReceiptDigest || secondBody.CreatedAt != firstBody.CreatedAt {
		t.Fatalf("idempotent body changed receipt: first=%+v second=%+v err=%v", firstBody, secondBody, err)
	}
	evidence := committedEvidence(identity)
	firstCommit, err := transactions.Advance(identity, domain.ChapterRenderPhaseCommitted, evidence)
	if err != nil {
		t.Fatal(err)
	}
	secondCommit, err := transactions.Advance(identity, domain.ChapterRenderPhaseCommitted, evidence)
	if err != nil || secondCommit.ReceiptDigest != firstCommit.ReceiptDigest || secondCommit.CreatedAt != firstCommit.CreatedAt {
		t.Fatalf("idempotent commit changed receipt: first=%+v second=%+v err=%v", firstCommit, secondCommit, err)
	}
	conflict := evidence
	conflict.CandidateRoot = chapterRenderStoreDigest("different-root")
	if _, err := transactions.Advance(identity, domain.ChapterRenderPhaseCommitted, conflict); err == nil || !strings.Contains(err.Error(), "different evidence") {
		t.Fatalf("same phase accepted different evidence: %v", err)
	}
}

func TestChapterRenderTransactionBodyOnlyCrashOrphanIsResumable(t *testing.T) {
	_, transactions, identity, body := chapterRenderStoreFixture(t)
	bodyPath := filepath.Join(transactions.transactionDir(identity), chapterRenderTransactionBodyName)
	if err := writeChapterRenderFileNoReplace(bodyPath, body); err != nil {
		t.Fatal(err)
	}
	listed, err := transactions.ListPlanBodies(identity.Plan)
	if err != nil {
		t.Fatalf("body-only crash orphan blocked plan scan: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("body-only orphan acquired phase authority: %+v", listed)
	}
	receipt, err := transactions.BeginBody(identity, body, bodyReadyEvidence(identity))
	if err != nil {
		t.Fatalf("BeginBody did not complete body-only crash orphan: %v", err)
	}
	if receipt == nil || receipt.Phase != domain.ChapterRenderPhaseBodyReady {
		t.Fatalf("unexpected resumed receipt: %+v", receipt)
	}
	listed, err = transactions.ListPlanBodies(identity.Plan)
	if err != nil || len(listed) != 1 || listed[0] != identity {
		t.Fatalf("resumed orphan was not discoverable: listed=%+v err=%v", listed, err)
	}
}

func TestChapterRenderTransactionRejectsSkippedPhaseAndBodyDrift(t *testing.T) {
	_, transactions, identity, body := chapterRenderStoreFixture(t)
	if _, err := transactions.BeginBody(identity, body, bodyReadyEvidence(identity)); err != nil {
		t.Fatal(err)
	}
	if _, err := transactions.Advance(identity, domain.ChapterRenderPhaseFormalAccepted, domain.ChapterRenderPhaseEvidence{
		ReviewVerdict: "accept", ReviewDisposition: "否",
		ReviewArtifacts: []domain.ChapterRenderArtifactBinding{{Path: "reviews/01.json", Digest: chapterRenderStoreDigest("review")}},
	}); err == nil || !strings.Contains(err.Error(), "cannot advance") {
		t.Fatalf("skipped commit phase: %v", err)
	}

	bodyPath := filepath.Join(transactions.transactionDir(identity), chapterRenderTransactionBodyName)
	if err := os.WriteFile(bodyPath, []byte("mutated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := transactions.LoadBody(identity); err == nil || !strings.Contains(err.Error(), "sha drift") {
		t.Fatalf("mutated content-addressed body loaded: %v", err)
	}
	if _, err := transactions.BeginBody(identity, body, bodyReadyEvidence(identity)); err == nil || !strings.Contains(err.Error(), "different bytes") {
		t.Fatalf("body no-replace did not fail closed: %v", err)
	}
}

func TestChapterRenderTransactionRejectsValidReceiptWithBrokenDigestChain(t *testing.T) {
	_, transactions, identity, body := chapterRenderStoreFixture(t)
	if _, err := transactions.BeginBody(identity, body, bodyReadyEvidence(identity)); err != nil {
		t.Fatal(err)
	}
	if _, err := transactions.Advance(identity, domain.ChapterRenderPhaseCommitted, committedEvidence(identity)); err != nil {
		t.Fatal(err)
	}
	commitPath := filepath.Join(
		transactions.transactionDir(identity),
		chapterRenderPhaseFileName(domain.ChapterRenderPhaseCommitted),
	)
	raw, err := os.ReadFile(commitPath)
	if err != nil {
		t.Fatal(err)
	}
	var commit domain.ChapterRenderPhaseReceipt
	if err := json.Unmarshal(raw, &commit); err != nil {
		t.Fatal(err)
	}
	commit.PreviousReceiptDigest = chapterRenderStoreDigest("unrelated-predecessor")
	commit.ReceiptDigest = ""
	commit, err = domain.SignChapterRenderPhaseReceipt(commit)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.MarshalIndent(commit, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(commitPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := transactions.LoadReceipts(identity); err == nil || !strings.Contains(err.Error(), "does not bind its predecessor") {
		t.Fatalf("valid standalone receipt with broken chain loaded: %v", err)
	}
}

func TestChapterRenderTransactionConcurrentSameEvidenceConverges(t *testing.T) {
	outputDir, _, identity, body := chapterRenderStoreFixture(t)
	const workers = 16
	var wg sync.WaitGroup
	bodyDigests := make(chan string, workers)
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, err := NewChapterRenderTransactionStore(outputDir).BeginBody(
				identity,
				append([]byte(nil), body...),
				bodyReadyEvidence(identity),
			)
			if err != nil {
				errs <- err
				return
			}
			bodyDigests <- receipt.ReceiptDigest
		}()
	}
	wg.Wait()
	close(errs)
	close(bodyDigests)
	for err := range errs {
		t.Errorf("concurrent BeginBody: %v", err)
	}
	unique := map[string]struct{}{}
	for digest := range bodyDigests {
		unique[digest] = struct{}{}
	}
	if len(unique) != 1 {
		t.Fatalf("concurrent BeginBody produced %d receipts: %v", len(unique), unique)
	}

	commitDigests := make(chan string, workers)
	errs = make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, err := NewChapterRenderTransactionStore(outputDir).Advance(
				identity,
				domain.ChapterRenderPhaseCommitted,
				committedEvidence(identity),
			)
			if err != nil {
				errs <- err
				return
			}
			commitDigests <- receipt.ReceiptDigest
		}()
	}
	wg.Wait()
	close(errs)
	close(commitDigests)
	for err := range errs {
		t.Errorf("concurrent Advance: %v", err)
	}
	unique = map[string]struct{}{}
	for digest := range commitDigests {
		unique[digest] = struct{}{}
	}
	if len(unique) != 1 {
		t.Fatalf("concurrent Advance produced %d receipts: %v", len(unique), unique)
	}
}

func TestChapterRenderTransactionDifferentBodiesShareOnlyPlanLevel(t *testing.T) {
	_, transactions, first, body := chapterRenderStoreFixture(t)
	secondBody := []byte("# 第一章\n\n这是语义拒绝后生成的另一份正文。")
	second, err := domain.NewChapterRenderBodyIdentity(first.Plan, domain.ComputeChapterRenderBodySHA256(secondBody))
	if err != nil {
		t.Fatal(err)
	}
	if first.PlanAttemptID != second.PlanAttemptID || first.TransactionID == second.TransactionID {
		t.Fatalf("two-level identity collapsed: first=%+v second=%+v", first, second)
	}
	if _, err := transactions.BeginBody(first, body, bodyReadyEvidence(first)); err != nil {
		t.Fatal(err)
	}
	if _, err := transactions.BeginBody(second, secondBody, bodyReadyEvidence(second)); err != nil {
		t.Fatal(err)
	}
	if _, err := transactions.LoadBody(first); err != nil {
		t.Fatal(err)
	}
	if got, err := transactions.LoadBody(second); err != nil || string(got) != string(secondBody) {
		t.Fatalf("second body=%q err=%v", got, err)
	}
}
