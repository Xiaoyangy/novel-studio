package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestChapterDeliveryAuthorizedOverrunPreservesOriginalTiming(t *testing.T) {
	st := NewStore(t.TempDir())
	g := chapterDeliveryTestGeneration(t, st, "authorized-continuation", true)
	start := chapterDeliveryTestNow()
	if err := st.ArmChapterDeliveryBudget(g, start); err != nil {
		t.Fatal(err)
	}
	original, err := st.BeginChapterDelivery(g, 4, start)
	if err != nil {
		t.Fatal(err)
	}
	late := start.Add(time.Hour)
	if err := st.CheckChapterDeliveryBudget(g.GenerationID, late); !errors.Is(err, ErrChapterDeliveryDeadline) {
		t.Fatalf("strict default lost: %v", err)
	}
	if err := st.AuthorizeChapterDeliveryOverrun(g, "User permits this generation to continue while preserving its original twenty-minute overrun", late); err != nil {
		t.Fatalf("explicit host authorization failed: %v", err)
	}
	if err := NewStore(st.Dir()).CheckChapterDeliveryBudget(g.GenerationID, late.Add(time.Minute)); err != nil {
		t.Fatalf("authorized overrun still hard-stopped: %v", err)
	}
	resumed, err := st.BeginChapterDelivery(g, 4, late.Add(2*time.Minute))
	if err != nil || !reflect.DeepEqual(original, resumed) {
		t.Fatalf("authorization reset original timing: %+v %v", resumed, err)
	}
	if next, err := st.BeginChapterDelivery(g, 5, late.Add(3*time.Minute)); err != nil || next == nil {
		t.Fatalf("authorized generation could not start its next chapter: %v", err)
	}
	path := filepath.Join(st.Dir(), chapterDeliveryLedgerPath)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AuthorizeChapterDeliveryOverrun(g, "User permits this generation to continue while preserving its original twenty-minute overrun", late.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("idempotent authorization rewrote audit time/history")
	}
	if err := st.AuthorizeChapterDeliveryOverrun(g, "different authorization reason", late.Add(5*time.Minute)); err == nil {
		t.Fatal("new reason overwrote historical authorization")
	}
}

func TestChapterDeliveryContinuationOwnLeaseIsNarrow(t *testing.T) {
	for _, mode := range []string{"own", "default-active", "other-owner", "foreign-pid", "other-mode", "other-chapter", "expired-own", "missing-own"} {
		t.Run(mode, func(t *testing.T) {
			st := NewStore(t.TempDir())
			g := chapterDeliveryTestGeneration(t, st, "lease-"+mode, true)
			now := time.Now().UTC()
			start := now.Add(-time.Hour)
			if err := st.ArmChapterDeliveryBudget(g, start); err != nil {
				t.Fatal(err)
			}
			if _, err := st.BeginChapterDelivery(g, g.FirstProjectedChapter, start); err != nil {
				t.Fatal(err)
			}
			if mode != "missing-own" {
				if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionProjectAll, TargetChapter: g.FirstProjectedChapter, Owner: "own-cli", ExpiresAt: now.Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
				var lock domain.PipelineExecutionLock
				if err := st.readChapterDeliveryProofJSON(pipelineExecutionPath, &lock); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "foreign-pid":
					lock.ProcessID++
				case "other-mode":
					lock.Mode = domain.PipelineExecutionFoundation
				case "other-chapter":
					lock.TargetChapter++
				case "expired-own":
					lock.ExpiresAt = now.Add(-time.Minute)
				}
				raw, _ := json.Marshal(lock)
				if err := os.WriteFile(filepath.Join(st.Dir(), pipelineExecutionPath), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(st.Dir(), chapterDeliveryLedgerPath))
			leaseBefore, _ := os.ReadFile(filepath.Join(st.Dir(), pipelineExecutionPath))
			owners := []string{"own-cli"}
			if mode == "default-active" {
				owners = nil
			}
			if mode == "other-owner" {
				owners = []string{"other-cli"}
			}
			err := st.AuthorizeChapterDeliveryOverrun(g, "explicit generation continuation", now, owners...)
			if (err == nil) != (mode == "own") {
				t.Fatalf("lease authorization mode=%s err=%v", mode, err)
			}
			after, _ := os.ReadFile(filepath.Join(st.Dir(), chapterDeliveryLedgerPath))
			leaseAfter, _ := os.ReadFile(filepath.Join(st.Dir(), pipelineExecutionPath))
			if !bytes.Equal(leaseBefore, leaseAfter) || mode != "own" && !bytes.Equal(before, after) {
				t.Fatal("lease rejection/authorization rewrote original lease or rejected ledger")
			}
		})
	}
}

func TestChapterDeliveryContinuationRejectsInvalidRequestsWithoutWrites(t *testing.T) {
	for _, mode := range []string{"blank", "unknown-generation", "missing-ledger", "corrupt-ledger", "legacy", "ambiguous-owner"} {
		t.Run(mode, func(t *testing.T) {
			st := NewStore(t.TempDir())
			g := chapterDeliveryTestGeneration(t, st, "invalid-"+mode, mode != "legacy")
			now := chapterDeliveryTestNow()
			if mode != "legacy" {
				if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
					t.Fatal(err)
				}
			}
			reason := "explicit generation continuation"
			var owners []string
			switch mode {
			case "blank":
				reason = "   "
			case "unknown-generation":
				g.GenerationID = "pg2_unknown"
				g.GenerationDigest, _ = domain.ComputePlanningGenerationV2Digest(g)
			case "missing-ledger":
				if err := os.Remove(filepath.Join(st.Dir(), chapterDeliveryLedgerPath)); err != nil {
					t.Fatal(err)
				}
			case "corrupt-ledger":
				if err := os.WriteFile(filepath.Join(st.Dir(), chapterDeliveryLedgerPath), []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ambiguous-owner":
				owners = []string{"one", "two"}
			}
			before, err := DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			if err := st.AuthorizeChapterDeliveryOverrun(g, reason, now, owners...); err == nil {
				t.Fatal("invalid authorization was accepted")
			}
			after, err := DirectoryContentRoot(st.Dir())
			if err != nil || before != after {
				t.Fatalf("invalid request wrote files: %v", err)
			}
		})
	}
}

func TestChapterDeliveryContinuationIsGenerationLocalAndKeepsReplacementGuard(t *testing.T) {
	st := NewStore(t.TempDir())
	first := chapterDeliveryTestGeneration(t, st, "authorized-first", true)
	now := chapterDeliveryTestNow()
	if err := st.ArmChapterDeliveryBudget(first, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginChapterDelivery(first, 4, now); err != nil {
		t.Fatal(err)
	}
	second, source, registry, _ := projectedStoreV2FixtureWithAttempt(t, 3, "strict-second")
	second.BaseCanonChapter, source.BaseCanonChapter = 6, 6
	second.FirstProjectedChapter, second.LastProjectedChapter = 7, 9
	registry.FirstChapter, registry.LastChapter = 7, 9
	registry.RegistryRoot, _ = domain.ComputeObligationRegistryV2Root(registry)
	second.ObligationRegistryRoot = registry.RegistryRoot
	second.ChapterDeliveryBudget = &domain.ChapterDeliveryBudgetV1{Policy: domain.ChapterDeliveryBudgetPolicyV1, LimitSeconds: 1200}
	second.GenerationDigest, _ = domain.ComputePlanningGenerationV2Digest(second)
	source.SnapshotDigest, _ = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err := st.ProjectedV2().CreateBuildingGeneration(second, source, registry); err != nil {
		t.Fatal(err)
	}
	if err := st.ArmChapterDeliveryBudget(second, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginChapterDelivery(second, 7, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.AuthorizeChapterDeliveryOverrun(first, "only the first generation", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.CheckChapterDeliveryBudget(first.GenerationID, now.Add(time.Hour)); !errors.Is(err, ErrChapterDeliveryDeadline) {
		t.Fatalf("authorization disabled another generation's deadline: %v", err)
	}
	ledger, err := st.readChapterDeliveryLedger()
	if err != nil {
		t.Fatal(err)
	}
	if err := chapterDeliveryRejectReplacement(ledger, "a-new-attempt", 4); err == nil {
		t.Fatal("observe_only released original generation replacement guard")
	}
}

func TestChapterDeliveryContinuationKeepsLateAcceptanceProof(t *testing.T) {
	st, g, acceptances := chapterDeliveryAcceptedFixture(t)
	late := chapterDeliveryTestNow().Add(time.Hour)
	if err := st.AuthorizeChapterDeliveryOverrun(g, "continue and report the original overrun", late); err != nil {
		t.Fatal(err)
	}
	fake := acceptances[0]
	fake.AcceptedAt = late.Format(time.RFC3339Nano)
	fake, _ = domain.SignChapterAcceptanceReceipt(fake)
	if _, err := st.CompleteChapterDelivery(g, fake, late); err == nil {
		t.Fatal("authorization weakened persisted acceptance authentication")
	}
	timing, err := st.CompleteChapterDelivery(g, acceptances[0], late)
	if err != nil || timing == nil || !timing.RecoveredAfterDeadline || timing.Recovered || timing.TimingUnknown || !timing.DeadlineAt.Equal(chapterDeliveryTestNow().Add(20*time.Minute)) {
		t.Fatalf("late truthful close changed: %+v %v", timing, err)
	}
}

func TestChapterDeliveryContinuationConcurrentStoresKeepOneAuthorization(t *testing.T) {
	st := NewStore(t.TempDir())
	g := chapterDeliveryTestGeneration(t, st, "concurrent-authorization", true)
	now := time.Now().UTC()
	if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionProjectAll, TargetChapter: g.FirstProjectedChapter, Owner: "own-cli"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures <- NewStore(st.Dir()).AuthorizeChapterDeliveryOverrunNow(g, "same explicit user grant", "own-cli")
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := st.readChapterDeliveryLedger()
	if err != nil {
		t.Fatal(err)
	}
	grant := ledger.Generations[g.GenerationID].Continuation
	if grant == nil || grant.AuthorizationDigest == "" {
		t.Fatal("concurrent grant missing")
	}
	before, _ := os.ReadFile(filepath.Join(st.Dir(), chapterDeliveryLedgerPath))
	if err := st.AuthorizeChapterDeliveryOverrunNow(g, "same explicit user grant", "own-cli"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(st.Dir(), chapterDeliveryLedgerPath))
	if !bytes.Equal(before, after) {
		t.Fatal("concurrent idempotent grant changed audit bytes")
	}
}

func TestChapterDeliveryContinuationRejectsTamperedAuthorization(t *testing.T) {
	for _, mode := range []string{"mode", "generation", "reason", "time", "original-limit", "digest"} {
		t.Run(mode, func(t *testing.T) {
			st := NewStore(t.TempDir())
			g := chapterDeliveryTestGeneration(t, st, "tampered-"+mode, true)
			now := chapterDeliveryTestNow()
			if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
				t.Fatal(err)
			}
			if err := st.AuthorizeChapterDeliveryOverrun(g, "explicit grant", now); err != nil {
				t.Fatal(err)
			}
			ledger, err := st.readChapterDeliveryLedger()
			if err != nil {
				t.Fatal(err)
			}
			grant := ledger.Generations[g.GenerationID].Continuation
			switch mode {
			case "mode":
				grant.Mode = "disabled"
			case "generation":
				grant.GenerationID = "pg2_foreign"
			case "reason":
				grant.Reason = "changed"
			case "time":
				grant.AuthorizedAt = now.Add(time.Hour)
			case "original-limit":
				grant.OriginalLimitSeconds++
			case "digest":
				grant.AuthorizationDigest = "sha256:wrong"
			}
			ledger.Digest = ""
			raw, _ := json.Marshal(ledger)
			ledger.Digest = domain.ComputeArcArtifactSHA256(raw)
			raw, _ = json.Marshal(ledger)
			path := filepath.Join(st.Dir(), chapterDeliveryLedgerPath)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := st.CheckChapterDeliveryBudget(g.GenerationID, now.Add(time.Hour)); err == nil {
				t.Fatal("tampered authorization bypassed strict read validation")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(raw, after) {
				t.Fatal("failed authorization validation rewrote ledger")
			}
		})
	}
}

func TestChapterDeliveryLedgerLegacyWireGolden(t *testing.T) {
	st := NewStore(t.TempDir())
	g := chapterDeliveryTestGeneration(t, st, "legacy-wire-golden", true)
	now := chapterDeliveryTestNow()
	if err := st.ArmChapterDeliveryBudget(g, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginChapterDelivery(g, g.FirstProjectedChapter, now); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), chapterDeliveryLedgerPath))
	if err != nil {
		t.Fatal(err)
	}
	// Captured by this exact fixture using the actual 04f7350 budget source
	// and no continuation implementation, through go test -overlay.
	const oldSHA = "sha256:ac5f3970bfd382a7020adf3fd6142dbec0d82604fa8f1798e5e5701af82aa954"
	if len(raw) != 1551 || domain.ComputeArcArtifactSHA256(raw) != oldSHA || bytes.Contains(raw, []byte(`"continuation"`)) {
		t.Fatalf("legacy ledger bytes changed: sha=%s bytes=%d", domain.ComputeArcArtifactSHA256(raw), len(raw))
	}
}
