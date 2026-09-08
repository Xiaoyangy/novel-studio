package domain_test

import (
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestChapterActivationCyclesPreserveChapterAndExactStateSequence(t *testing.T) {
	first := testutil.CharacterCycle(t, 1, "", nil, 0)
	session, err := domain.NewCharacterActivationSession(first.GenerationID, 1, first.ChapterContextDigest, *first.Evidence.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	original := session
	session, err = domain.AppendCharacterActivationCycle(session, first)
	if err != nil {
		t.Fatal(err)
	}
	if original.Phase != "collecting" || len(original.CycleDigests) != 0 {
		t.Fatal("append mutated source cursor")
	}
	if session.Phase != "assessing" || session.Chapter != 1 {
		t.Fatal("cycle was treated as a new chapter or auto-ready")
	}
	if _, err := domain.AppendCharacterActivationCycle(session, first); err == nil {
		t.Fatal("unassessed cycle advanced")
	}
	session, err = domain.ApplyCharacterChapterReadiness(session, testutil.CycleReadiness(t, first, "continue"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := domain.ApplyArbitrationPhysicalStateV2(first.Evidence.Arbitrations[0], first.Evidence.Stimulus, first.Evidence.Proposals...)
	if err != nil {
		t.Fatal(err)
	}
	second := testutil.CharacterCycle(t, 2, first.Digest, &after, first.EndDay)
	session, err = domain.AppendCharacterActivationCycle(session, second)
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.ApplyCharacterChapterReadiness(session, testutil.CycleReadiness(t, second, "ready_for_plan"))
	if err != nil {
		t.Fatal(err)
	}
	if session.Phase != "ready" || session.Chapter != 1 || len(session.CycleDigests) != 2 || session.CurrentDay != second.EndDay {
		t.Fatal("invalid completed chapter session")
	}
	if _, err := domain.AppendCharacterActivationCycle(session, second); err == nil {
		t.Fatal("closed chapter advanced")
	}
}

func TestChapterActivationCycleRejectsRelabelledAndTamperedProof(t *testing.T) {
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
	for name, edit := range map[string]func(*domain.CharacterActivationCycle){
		"chapter":    func(c *domain.CharacterActivationCycle) { c.Chapter = 2 },
		"generation": func(c *domain.CharacterActivationCycle) { c.GenerationID = "pg2_other" },
		"index":      func(c *domain.CharacterActivationCycle) { c.Index = 2; c.PreviousDigest = c.Digest },
		"physical":   func(c *domain.CharacterActivationCycle) { c.AfterPhysicalRoot = c.BeforePhysicalRoot },
		"time":       func(c *domain.CharacterActivationCycle) { c.EndDay = 10 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := cycle
			edit(&changed)
			if err := domain.ValidateCharacterActivationCycle(changed); err == nil {
				t.Fatal("tampered cycle accepted")
			}
		})
	}
	session, err := domain.NewCharacterActivationSession(cycle.GenerationID, 1, cycle.ChapterContextDigest, *cycle.Evidence.Stimulus.PhysicalState, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.AppendCharacterActivationCycle(session, cycle)
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.ApplyCharacterChapterReadiness(session, testutil.CycleReadiness(t, cycle, "continue"))
	if err != nil {
		t.Fatal(err)
	}
	before := session
	if _, err := domain.AppendCharacterActivationCycle(session, cycle); err == nil {
		t.Fatal("limit treated as permission to continue")
	}
	if !reflect.DeepEqual(before, session) || session.Phase != "collecting" {
		t.Fatal("limit falsely marked chapter complete or mutated it")
	}
}

func TestChapterActivationCannotSpinOrOverrideHardConflict(t *testing.T) {
	for _, hard := range []bool{false, true} {
		cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
		evidence := cycle.Evidence
		receipt := evidence.Arbitrations[0]
		receipt.StoryTime = &domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: 0, EndDay: 0}
		receipt.ResourceSettlements = nil
		if hard {
			receipt.HardContractStatus = "infeasible"
			receipt.Finalized = false
			receipt.HardContractConflicts = []string{"硬义务不可能兑现"}
		}
		var err error
		receipt, err = domain.FinalizeWorldArbitrationReceipt(receipt, evidence.Stimulus, evidence.Activation, evidence.Proposals, 1)
		if err != nil {
			t.Fatal(err)
		}
		evidence.Arbitrations = []domain.WorldArbitrationReceipt{receipt}
		if hard {
			evidence.Version = ""
			evidence, err = domain.FinalizeCharacterHardConflictEvidenceBundle(evidence)
			if err == nil && domain.ValidateCharacterAgentEvidenceBundle(evidence) == nil {
				t.Fatal("hard-conflict evidence became sealable legacy evidence")
			}
		} else {
			evidence, err = domain.FinalizeCharacterAgentEvidenceBundle(evidence)
		}
		if err != nil {
			t.Fatal(err)
		}
		cycle.Evidence = evidence
		cycle, err = domain.FinalizeCharacterActivationCycle(cycle)
		if err != nil {
			t.Fatal(err)
		}
		session, err := domain.NewCharacterActivationSession(cycle.GenerationID, 1, cycle.ChapterContextDigest, *cycle.Evidence.Stimulus.PhysicalState, 0, 4)
		if err != nil {
			t.Fatal(err)
		}
		session, err = domain.AppendCharacterActivationCycle(session, cycle)
		if !hard {
			if err == nil {
				t.Fatal("zero-time/unchanged-world cycle can spin")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := domain.ApplyCharacterChapterReadiness(session, testutil.CycleReadiness(t, cycle, "ready_for_plan")); err == nil {
			t.Fatal("Planner readiness overrode an arbitrated hard conflict")
		}
		closed, err := domain.ApplyCharacterChapterReadiness(session, testutil.CycleReadiness(t, cycle, "hard_conflict"))
		if err != nil || closed.Phase != "hard_conflict" {
			t.Fatalf("hard conflict not retained: %v", err)
		}
	}
}
