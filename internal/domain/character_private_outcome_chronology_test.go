package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Exercise the actual physical evaluation kernel twice with the unchanged
// original choices. This is a private-result regression, not a substitute for
// the global continuation-authorization verifier owned by the cycle runtime.
func privateChronologyRepeatingProposalFixture(t *testing.T, receive ...bool) (physicalProtocolFixture, WorldArbitrationReceipt, WorldPhysicalStateV2) {
	t.Helper()
	f, _ := newSelfChronologyFixture(t)
	for i := range f.proposals {
		f.proposals[i].SelfTasks = []CharacterSelfTaskV2{{TaskID: "inspection", Kind: "work", Action: f.proposals[i].Character + "的本人检查", ProgressTarget: physicalTestNumber(3), ProgressUnit: "minute", KnowledgeRefs: f.proposals[i].KnowledgeRefs}}
		f.receipt.Resolutions[i].Outcome, f.receipt.Resolutions[i].CompletionState = "success", "in_progress"
		f.receipt.Resolutions[i].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "inspection", Status: "in_progress", StartDay: selfTestDay(0), EndDay: selfTestDay(1)}}
		if len(receive) > 1 && receive[1] {
			f.receipt.Resolutions[i].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "inspection", Status: "not_started"}}
		}
	}
	f.receipt.StoryTime.EndDay = *selfTestDay(1)
	if len(receive) > 0 && receive[0] {
		f.proposals[1].Communications = []CharacterCommunicationV2{{ID: "real-reminder", ToCharacter: f.proposals[0].Character, Kind: "information", Text: "本次确已收到的作业提醒", KnowledgeRefs: f.proposals[1].KnowledgeRefs}}
	}
	rebindPhysicalTestStimulus(t, &f)
	first, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(first, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	originalProposals, _ := json.Marshal(f.proposals)
	f.stimulus.PhysicalState = &state
	context := *f.stimulus.SelfEvaluationContext
	context.Cycle, context.CurrentDay = 2, first.StoryTime.EndDay
	context.PreviousCycleDigest = first.Digest
	context.BeforePhysicalRoot, err = CharacterPhysicalRootForCycle(state)
	if err != nil {
		t.Fatal(err)
	}
	context.Digest, err = selfEvaluationContextDigestV1(context)
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.SelfEvaluationContext = &context
	clock := *f.stimulus.StoryClock
	clock.CurrentDay = context.CurrentDay
	clock, err = FinalizeStoryClockContext(clock)
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.StoryClock = &clock
	for i, source := range f.stimulus.Sources {
		if strings.HasPrefix(source, CharacterActivationCycleSourcePrefix) {
			f.stimulus.Sources[i], err = CharacterActivationCycleSourceToken(context.GenerationID, context.Chapter, 2, context.ChapterContextDigest, context.PreviousCycleDigest)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	f.stimulus, err = FinalizeWorldStimulusPacket(f.stimulus)
	if err != nil {
		t.Fatal(err)
	}
	f.receipt.Digest, f.receipt.StimulusDigest = "", f.stimulus.Digest
	f.receipt.StoryTime = &StoryTimeChapterSchedule{Chapter: 1, StartDay: *selfTestDay(1), EndDay: *selfTestDay(2)}
	for i := range f.receipt.Resolutions {
		post := state.Actors[i]
		f.receipt.Resolutions[i].PostState = &post
		f.receipt.Resolutions[i].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "inspection", Status: "in_progress", StartDay: selfTestDay(1), EndDay: selfTestDay(2)}}
		if len(receive) > 1 && receive[1] {
			f.receipt.Resolutions[i].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "inspection", Status: "not_started"}}
		}
	}
	if len(receive) > 0 && receive[0] {
		f.receipt.Resolutions[0].PostState.ReceivedFacts = []CharacterReceivedFactV2{{Kind: "information", Text: "本次确已收到的作业提醒", SourceType: "communication", SourceID: "real-reminder", SourceProposalDigest: f.proposals[1].Digest, FromAgentID: f.proposals[1].AgentID, Chapter: 1}}
	}
	second, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(second, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, _ := json.Marshal(f.proposals)
	if string(originalProposals) != string(unchanged) {
		t.Fatal("continuation fixture re-signed the original independent choices")
	}
	return f, second, after
}

func TestPrivateChronologyRequiresExactReceiptAndPreservesNewReceivedKnowledge(t *testing.T) {
	f, receipt, state := privateChronologyRepeatingProposalFixture(t, true)
	proposal, resolution := f.proposals[0], receipt.Resolutions[0]
	if _, err := CharacterPrivateOutcomeV2(proposal, resolution, state); err == nil {
		t.Fatal("chronology guessed the current evaluation without a receipt")
	}
	changedChoice := proposal
	changedChoice.IntendedAction = "未经角色选择的替代行动"
	if _, err := CharacterPrivateOutcomeV2(changedChoice, resolution, state, receipt); err == nil {
		t.Fatal("private outcome accepted a rewritten original choice")
	}
	text, err := CharacterPrivateOutcomeV2(proposal, resolution, state, receipt)
	if err != nil || !strings.Contains(text, "收到的information：本次确已收到的作业提醒") {
		t.Fatalf("filtering repeated work hid actually received information: %s (%v)", text, err)
	}
	for _, name := range []string{"source", "unhashed", "execution", "different_owner", "missing_current"} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(receipt)
			var bad WorldArbitrationReceipt
			_ = json.Unmarshal(raw, &bad)
			badResolution := bad.Resolutions[0]
			switch name {
			case "source":
				bad.StimulusDigest = "sha256:" + strings.Repeat("f", 64)
			case "unhashed":
				bad.Digest = ""
			case "execution":
				bad.Resolutions[0].SelfExecutions[0].EndDay = selfTestDay(1.5)
				badResolution = bad.Resolutions[0]
			case "different_owner":
				badResolution = bad.Resolutions[1]
			case "missing_current":
				bad.Resolutions[0].SelfExecutions = nil
				badResolution = bad.Resolutions[0]
			}
			if name != "unhashed" {
				bad.Digest, err = ComputeWorldArbitrationReceiptDigest(bad)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := CharacterPrivateOutcomeV2(proposal, badResolution, state, bad); err == nil {
				t.Fatal("wrong or incomplete current receipt supplied private outcome authority")
			}
		})
	}
}

func TestPrivateChronologyLegacyFormatterRemainsByteIdentical(t *testing.T) {
	f := newSelfExperienceFixture(t)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	text, err := CharacterPrivateOutcomeV2(f.proposals[0], receipt.Resolutions[0], state)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := CharacterPrivateOutcomeV2(f.proposals[0], receipt.Resolutions[0], state, receipt)
	if err != nil || bound != text {
		t.Fatal("supplying the current receipt changed legacy private outcome bytes")
	}
	const legacySHA256 = "526a86b1490220be6ec5612ad0788ec77e9fb84e2085e7f76daacf1274b0ef28"
	if fmt.Sprintf("%x", sha256.Sum256([]byte(text))) != legacySHA256 {
		t.Fatal("pre-chronology private formatter bytes changed")
	}
}

func TestPrivateChronologyRepeatedUnstartedTaskUsesEvaluationNotExecutionTime(t *testing.T) {
	f, receipt, state := privateChronologyRepeatingProposalFixture(t, false, true)
	for i, proposal := range f.proposals {
		text, err := CharacterActivationPrivateOutcome(proposal, receipt.Resolutions[i], state, receipt)
		if err != nil || strings.Count(text, "本人经历（") != 1 || !strings.Contains(text, "第2次评估") || strings.Contains(text, "第1次评估") || strings.Contains(text, "实际区间") {
			t.Fatalf("unstarted execution was duplicated or assigned invented work time: %s (%v)", text, err)
		}
	}
}

func TestPrivateChronologyContinuationOnlyFormatsCurrentOwnerEvaluation(t *testing.T) {
	f, receipt, state := privateChronologyRepeatingProposalFixture(t)
	before, _ := json.Marshal(state)
	for i, proposal := range f.proposals {
		text, err := CharacterActivationPrivateOutcome(proposal, receipt.Resolutions[i], state, receipt)
		if err != nil {
			t.Fatal(err)
		}
		current := state.Actors[i].SelfExperiences[1]
		if current.Evaluation.Cycle != 2 || current.Evaluation.StimulusDigest != receipt.StimulusDigest {
			t.Fatal("fixture lacks real second-cycle evaluation")
		}
		if strings.Count(text, "本人经历（") != 1 || strings.Contains(text, "实际区间T+0至T+1分钟") {
			t.Fatalf("old same-proposal execution repeated in the current private result: %s", text)
		}
		if !strings.Contains(text, "累计有效工时2分钟") || !strings.Contains(text, "尚余1分钟") {
			t.Fatalf("current cumulative work disappeared: %s", text)
		}
		other := f.proposals[1-i].Character + "的本人检查"
		if strings.Contains(text, other) || strings.Contains(text, "秘密余额") || strings.Contains(text, "秘密真值") {
			t.Fatal("another owner or world-side secret entered private memory")
		}
	}
	after, _ := json.Marshal(state)
	if string(before) != string(after) || len(state.Actors[0].SelfExperiences) != 2 || len(state.Actors[1].SelfExperiences) != 2 {
		t.Fatal("formatting deleted or rewrote real history")
	}
}
