package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestChapterDeliveryExecutionLockExemptionRequiresEmptyFile(t *testing.T) {
	for _, nonempty := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal-empty-lock", true: "nonempty-unreadable-lock"}[nonempty], func(t *testing.T) {
			st, g := chapterDeliveryFreshExecutionFixture(t, true)
			execution := NewStore(t.TempDir())
			verifiedStoreMust(t, execution.WithCharacterActivationExecution(context.Background(), g.GenerationID, 1, func() error { return nil }))
			root, err := characterActivationSessionDir(g.GenerationID, 1)
			verifiedStoreMust(t, err)
			lock := filepath.Join(execution.Dir(), root, "execution.lock")
			info, err := os.Stat(lock)
			verifiedStoreMust(t, err)
			if info.Size() != 0 {
				t.Fatal("normal execution wrapper wrote lock contents")
			}
			const privateContent = "PRIVATE_LOCK_BODY_MUST_NOT_ENTER_DIAGNOSTICS"
			if nonempty {
				verifiedStoreMust(t, os.WriteFile(lock, []byte(privateContent), 0o600))
				verifiedStoreMust(t, os.Chmod(lock, 0))
				t.Cleanup(func() { _ = os.Chmod(lock, 0o600) })
			}
			ledger := filepath.Join(st.Dir(), chapterDeliveryLedgerPath)
			before, err := os.ReadFile(ledger)
			verifiedStoreMust(t, err)
			timing, beginErr := st.BeginChapterDeliveryNow(g, 1, execution)
			if !nonempty {
				verifiedStoreMust(t, beginErr)
				if timing == nil || timing.StartedAt.IsZero() {
					t.Fatal("ordinary empty execution lock blocked a fresh start")
				}
				return
			}
			if beginErr == nil || timing != nil {
				t.Fatal("nonempty execution lock was incorrectly exempted from prior evidence")
			}
			if strings.Contains(beginErr.Error(), privateContent) {
				t.Fatal("lock contents leaked into diagnostics")
			}
			after, err := os.ReadFile(ledger)
			verifiedStoreMust(t, err)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected nonempty lock changed the original ledger")
			}
		})
	}
}

func TestChapterDeliveryBeginRejectsArmedBackupAfterActualProposal(t *testing.T) {
	for _, isolated := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "isolated-execution"}[isolated], func(t *testing.T) {
			chapterDeliveryArmedBackupAfterProposal(t, isolated)
		})
	}
}

func chapterDeliveryArmedBackupAfterProposal(t *testing.T, isolated bool) {
	t.Helper()
	f := newPlanningWindowStoreFixture(t, 6, true)
	f.generation.ChapterDeliveryBudget = &domain.ChapterDeliveryBudgetV1{Policy: domain.ChapterDeliveryBudgetPolicyV1, LimitSeconds: 1200}
	planningWindowStoreRebind(t, &f)
	verifiedStoreMust(t, f.st.ProjectedV2().CreateBuildingGeneration(f.generation, f.source, f.registry))
	verifiedStoreMust(t, f.st.ArmChapterDeliveryBudgetNow(f.generation))
	path := filepath.Join(f.st.Dir(), chapterDeliveryLedgerPath)
	armed, err := os.ReadFile(path)
	verifiedStoreMust(t, err)
	original, err := f.st.BeginChapterDeliveryNow(f.generation, 1)
	verifiedStoreMust(t, err)
	if original == nil || original.StartedAt.IsZero() {
		t.Fatal("fixture lacks its legitimate original start")
	}
	execution := f.st
	if isolated {
		execution = NewStore(t.TempDir())
	}
	chapterDeliverySaveActualProposal(t, execution, f.generation.GenerationID)
	resumed, err := NewStore(f.st.Dir()).BeginChapterDeliveryNow(f.generation, 1, execution)
	verifiedStoreMust(t, err)
	if !reflect.DeepEqual(original, resumed) {
		t.Fatal("normal resume changed the original timing")
	}
	// Model partial backup restoration in this disposable fixture only. The
	// earlier armed snapshot is validly signed; persisted execution survives.
	verifiedStoreMust(t, os.WriteFile(path, armed, 0o644))
	timing, err := NewStore(f.st.Dir()).BeginChapterDeliveryNow(f.generation, 1, execution)
	if err == nil || timing != nil {
		t.Fatalf("missing original start was invented after an actual proposal: original=%+v replacement=%+v err=%v", original, timing, err)
	}
	after, readErr := os.ReadFile(path)
	verifiedStoreMust(t, readErr)
	if !bytes.Equal(armed, after) {
		t.Fatal("rejected recovery rewrote the armed backup")
	}
}

func chapterDeliveryFreshExecutionFixture(t *testing.T, budget bool) (*Store, domain.PlanningGenerationV2) {
	t.Helper()
	f := newPlanningWindowStoreFixture(t, 6, true)
	if budget {
		f.generation.ChapterDeliveryBudget = &domain.ChapterDeliveryBudgetV1{Policy: domain.ChapterDeliveryBudgetPolicyV1, LimitSeconds: 1200}
		planningWindowStoreRebind(t, &f)
	}
	verifiedStoreMust(t, f.st.ProjectedV2().CreateBuildingGeneration(f.generation, f.source, f.registry))
	if budget {
		verifiedStoreMust(t, f.st.ArmChapterDeliveryBudgetNow(f.generation))
	}
	return f.st, f.generation
}

func TestChapterDeliveryFreshStartScopesExecutionEvidence(t *testing.T) {
	for _, kind := range []string{"empty", "empty-directories", "execution-lock", "foreign-generation", "other-chapter", "other-usage"} {
		t.Run(kind, func(t *testing.T) {
			st, g := chapterDeliveryFreshExecutionFixture(t, true)
			execution := NewStore(t.TempDir())
			root, err := characterActivationSessionDir(g.GenerationID, 1)
			verifiedStoreMust(t, err)
			switch kind {
			case "empty-directories":
				verifiedStoreMust(t, os.MkdirAll(filepath.Join(execution.Dir(), root, "work", "000001", "proof"), 0o755))
			case "execution-lock":
				verifiedStoreMust(t, os.MkdirAll(filepath.Join(execution.Dir(), root), 0o755))
				verifiedStoreMust(t, os.WriteFile(filepath.Join(execution.Dir(), root, "execution.lock"), nil, 0o600))
			case "foreign-generation", "other-chapter":
				cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
				gen, chapter := g.GenerationID, 2
				if kind == "foreign-generation" {
					gen, chapter = "pg2_other_generation", 1
				}
				session, err := domain.NewCharacterActivationSession(gen, chapter, cycle.ChapterContextDigest, *cycle.Evidence.Stimulus.PhysicalState, 0, 4)
				verifiedStoreMust(t, err)
				verifiedStoreMust(t, execution.CreateCharacterActivationSession(session))
			case "other-usage":
				verifiedStoreMust(t, execution.CharacterAgents.AppendUsage(domain.CharacterAgentUsage{GenerationID: g.GenerationID, Chapter: 2, AgentID: "ca_other", Role: "character"}))
				verifiedStoreMust(t, execution.CharacterAgents.AppendUsage(domain.CharacterAgentUsage{GenerationID: "pg2_other_generation", Chapter: 1, AgentID: "ca_other", Role: "character"}))
			}
			got, err := st.BeginChapterDeliveryNow(g, 1, execution)
			verifiedStoreMust(t, err)
			if got == nil || got.StartedAt.IsZero() || got.DeadlineAt.Sub(got.StartedAt).Seconds() != 1200 {
				t.Fatalf("unrelated/pre-dispatch data blocked a fresh chapter: %+v", got)
			}
		})
	}
}

func TestChapterDeliveryFreshStartRejectsPartialOrUnreadableExecution(t *testing.T) {
	for _, kind := range []string{"session-before-proposal", "residual-cycle", "legacy-proof", "corrupt-proof", "unreadable-directory", "unsafe-parent", "unsafe-lock", "usage", "corrupt-usage"} {
		t.Run(kind, func(t *testing.T) {
			st, g := chapterDeliveryFreshExecutionFixture(t, true)
			execution := NewStore(t.TempDir())
			root, err := characterActivationSessionDir(g.GenerationID, 1)
			verifiedStoreMust(t, err)
			rel := filepath.Join(root, "cycles", "000001.json")
			switch kind {
			case "unreadable-directory":
				path := filepath.Join(execution.Dir(), root)
				verifiedStoreMust(t, os.MkdirAll(path, 0o755))
				verifiedStoreMust(t, os.Chmod(path, 0))
				t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
				if _, err := os.ReadDir(path); err == nil {
					t.Skip("privileged test process bypasses directory permissions")
				}
			case "session-before-proposal":
				cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
				session, err := domain.NewCharacterActivationSession(g.GenerationID, 1, cycle.ChapterContextDigest, *cycle.Evidence.Stimulus.PhysicalState, 0, 4)
				verifiedStoreMust(t, err)
				verifiedStoreMust(t, execution.CreateCharacterActivationSession(session))
			case "usage":
				verifiedStoreMust(t, execution.CharacterAgents.AppendUsage(domain.CharacterAgentUsage{GenerationID: g.GenerationID, Chapter: 1, AgentID: "ca_owner", Role: "character", Attempts: 1}))
			case "corrupt-usage":
				rel = filepath.Join(characterAgentRoot, "usage.jsonl")
				verifiedStoreMust(t, os.MkdirAll(filepath.Dir(filepath.Join(execution.Dir(), rel)), 0o755))
				verifiedStoreMust(t, os.WriteFile(filepath.Join(execution.Dir(), rel), []byte("{truncated"), 0o644))
			case "unsafe-parent", "unsafe-lock":
				target := t.TempDir()
				if kind == "unsafe-lock" {
					root = filepath.Join(root, "execution.lock")
				}
				verifiedStoreMust(t, os.MkdirAll(filepath.Dir(filepath.Join(execution.Dir(), root)), 0o755))
				verifiedStoreMust(t, os.Symlink(target, filepath.Join(execution.Dir(), root)))
			default:
				if kind == "legacy-proof" {
					rel = characterAgentProposalPath(g.GenerationID, 1, 1, "ca_owner")
				} else if kind == "corrupt-proof" {
					rel = filepath.Join(root, "work", "000001", "proof", "proposals", "round-01", "ca_owner.json")
				}
				verifiedStoreMust(t, os.MkdirAll(filepath.Dir(filepath.Join(execution.Dir(), rel)), 0o755))
				verifiedStoreMust(t, os.WriteFile(filepath.Join(execution.Dir(), rel), []byte("{incomplete"), 0o644))
			}
			path := filepath.Join(st.Dir(), chapterDeliveryLedgerPath)
			before, err := os.ReadFile(path)
			verifiedStoreMust(t, err)
			got, err := st.BeginChapterDeliveryNow(g, 1, execution)
			if err == nil || got != nil {
				t.Fatal("partial/unreadable execution allowed a replacement first start")
			}
			after, err := os.ReadFile(path)
			verifiedStoreMust(t, err)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected first start modified timing bytes")
			}
		})
	}
}

func TestChapterDeliveryEvidenceGuardLeavesLegacyAndConcurrentFreshStartsUnchanged(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		st, g := chapterDeliveryFreshExecutionFixture(t, false)
		chapterDeliverySaveActualProposal(t, st, g.GenerationID)
		got, err := st.BeginChapterDeliveryNow(g, 1, st)
		verifiedStoreMust(t, err)
		if got != nil {
			t.Fatal("legacy generation acquired a timing policy")
		}
		if _, err := os.Stat(filepath.Join(st.Dir(), chapterDeliveryRoot)); !os.IsNotExist(err) {
			t.Fatalf("legacy generation created budget metadata: %v", err)
		}
	})
	t.Run("concurrent-fresh", func(t *testing.T) {
		st, g := chapterDeliveryFreshExecutionFixture(t, true)
		execution := NewStore(t.TempDir())
		var wg sync.WaitGroup
		results := make(chan *ChapterDeliveryTimingV1, 4)
		errors := make(chan error, 4)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := NewStore(st.Dir()).BeginChapterDeliveryNow(g, 1, NewStore(execution.Dir()))
				results <- got
				errors <- err
			}()
		}
		wg.Wait()
		close(results)
		close(errors)
		for err := range errors {
			verifiedStoreMust(t, err)
		}
		var first *ChapterDeliveryTimingV1
		for got := range results {
			if first == nil {
				first = got
			}
			if got == nil || !reflect.DeepEqual(first, got) {
				t.Fatal("concurrent first dispatch established multiple starts")
			}
		}
	})
}

// Persist genuine typed source/proposal evidence through Store APIs, without
// a provider call or a fabricated evidence file in an arbitrary directory.
func chapterDeliverySaveActualProposal(t *testing.T, st *Store, generation string) {
	t.Helper()
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
	e := cycle.Evidence
	session, err := domain.NewCharacterActivationSession(generation, 1, cycle.ChapterContextDigest, *e.Stimulus.PhysicalState, 0, 4)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, st.CreateCharacterActivationSession(session))
	proof, err := st.CharacterAgents.ForActivationCycle(session)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, proof.SaveRegistrySnapshot(generation, 1, e.Registry))
	e.Stimulus.GenerationID = generation
	e.Stimulus.Sources = []string{proof.ActivationCycleSourceToken()}
	e.Stimulus, err = domain.FinalizeWorldStimulusPacket(e.Stimulus)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, proof.SaveStimulus(e.Stimulus))
	o := e.Observations[0]
	o.GenerationID, o.StimulusDigest = generation, e.Stimulus.Digest
	o, err = domain.FinalizeCharacterObservationPacket(o)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, proof.SaveObservation(o))
	e.Activation.GenerationID = generation
	e.Activation.Entries[0].ObservationDigest = o.Digest
	e.Activation, err = domain.FinalizeCharacterAgentActivation(e.Activation)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, proof.SaveActivation(e.Activation))
	p := e.Proposals[0]
	p.GenerationID, p.ObservationDigest = generation, o.Digest
	p, err = domain.FinalizeCharacterDecisionProposal(p, o)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, proof.SaveProposal(p, o))
	stored, err := proof.LoadProposal(generation, 1, 1, p.AgentID)
	verifiedStoreMust(t, err)
	if stored == nil || stored.Digest != p.Digest {
		t.Fatal("fixture did not persist a source-bound owner proposal")
	}
}
