package domain

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func bindStoryClockTestBundle(t *testing.T, bundle *ProjectedChapterBundle, start, end float64) {
	t.Helper()
	decision := bundle.ChapterWorldSimulation.CharacterDecisions[0]
	registry, err := FinalizeCharacterAgentRegistry(CharacterAgentRegistry{Entries: []CharacterAgentRecord{{
		AgentID: "ca_clock", Character: decision.Character, Tier: "core", Status: CharacterAgentActive, MemoryVersion: 1,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	clock := storyClockForTest(t, start)
	stimulus, err := FinalizeWorldStimulusPacket(WorldStimulusPacket{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, TimeWindow: "当前实际时间", StoryClock: &clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := FinalizeCharacterObservationPacket(CharacterObservationPacket{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, AgentID: "ca_clock", Character: decision.Character,
		CurrentGoal: decision.CurrentGoal, Pressure: decision.Pressure, StimulusDigest: stimulus.Digest, MemoryRoot: "sha256:memory",
		KnownFacts: []CharacterAgentFact{{ID: "known-1", Kind: "known", Text: "门尚未关闭"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := FinalizeCharacterAgentActivation(CharacterAgentActivation{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, RegistryRoot: registry.RegistryRoot,
		Entries: []CharacterAgentActivationEntry{{AgentID: observation.AgentID, Character: observation.Character, Tier: "core", State: CharacterAgentActive, Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := FinalizeCharacterDecisionProposal(CharacterDecisionProposal{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, AgentID: observation.AgentID, Character: observation.Character,
		ObservationDigest: observation.Digest, Location: decision.Location, CurrentGoal: decision.CurrentGoal, Pressure: decision.Pressure,
		AvailableOptions: decision.AvailableOptions, Decision: decision.Decision, DecisionReason: decision.DecisionReason,
		IntendedAction: decision.Action, ActionDuration: decision.ActionDuration, KnowledgeRefs: []string{"known-1"},
	}, observation)
	if err != nil {
		t.Fatal(err)
	}
	arbitration := validCharacterArbitrationForTest(stimulus, activation, proposal)
	arbitration.StoryTime = &StoryTimeChapterSchedule{Chapter: bundle.Chapter, StartDay: start, EndDay: end}
	arbitration, err = FinalizeWorldArbitrationReceipt(arbitration, stimulus, activation, []CharacterDecisionProposal{proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	protocol := "sha256:" + strings.Repeat("c", 64)
	evidence, err := FinalizeCharacterAgentEvidenceBundle(CharacterAgentEvidenceBundle{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Registry: registry, Stimulus: stimulus, Activation: activation,
		Observations: []CharacterObservationPacket{observation}, Proposals: []CharacterDecisionProposal{proposal},
		Arbitrations: []WorldArbitrationReceipt{arbitration}, MemoryRoots: []string{observation.MemoryRoot}, ProtocolDigest: protocol,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle.CharacterAgentEvidence = &evidence
	bundle.ChapterWorldSimulation.Version = 2
	bundle.ChapterWorldSimulation.StoryTime = arbitration.StoryTime
	bundle.ChapterWorldSimulation.CharacterDecisions, err = arbitration.CharacterDecisions([]CharacterDecisionProposal{proposal})
	if err != nil {
		t.Fatal(err)
	}
	bundle.ChapterWorldSimulation.ProtagonistProjection = arbitration.ProtagonistProjection
	bundle.ChapterWorldSimulation.CharacterAgentProtocol = &CharacterAgentProtocolReceipt{
		Version: CharacterAgentDecisionProtocolVersion, RegistryRoot: registry.RegistryRoot, StimulusDigest: stimulus.Digest,
		ActivationDigest: activation.Digest, ObservationDigests: []string{observation.Digest}, ProposalDigests: []string{proposal.Digest},
		ArbitrationRound: 1, ArbitrationDigest: arbitration.Digest, MemoryRoots: evidence.MemoryRoots, ProtocolDigest: protocol,
	}
	bundle.ProjectedDelta.Timeline = append(bundle.ProjectedDelta.Timeline, StateMutationV2{
		StableID: "clock-test-world-day", Subject: "world", Field: "story_day", Operation: "advance",
		Before: strconv.FormatFloat(start, 'g', -1, 64), After: strconv.FormatFloat(end, 'g', -1, 64), Cause: "world arbitration",
	})
	rebindStoryClockTestBundle(t, bundle)
}

func rebindStoryClockTestBundle(t *testing.T, bundle *ProjectedChapterBundle) {
	t.Helper()
	var err error
	bundle.ProjectedDelta = NormalizeProjectedDeltaV2(bundle.ProjectedDelta)
	bundle.ProjectedPostStateRoot, err = DeriveProjectedPostStateRootV2(bundle.ProjectedPreStateRoot, bundle.ProjectedDelta)
	if err != nil {
		t.Fatal(err)
	}
	planningV2RebindRenderContext(t, bundle)
	bundle.BundleDigest = planningV2MustBundleDigest(t, *bundle)
}

func TestStoryClockSealedBundleProjectsActualEndIntoNextChapter(t *testing.T) {
	generation, registry, bundles := planningV2TestChain(t, 2)
	bundle := bundles[0]
	const end = 61.0 / 86400
	bindStoryClockTestBundle(t, &bundle, 0, end)
	if err := ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatalf("actual clock bundle rejected: %v", err)
	}
	context, err := DeriveProjectedPlanningContextV2(generation, []ProjectedChapterBundle{bundle}, registry, bundle.Chapter+1)
	if err != nil {
		t.Fatal(err)
	}
	var clocks []ProjectedPlanningStateFactV2
	for _, fact := range context.CumulativeState {
		if fact.Category == "timeline" && fact.Subject == "world" && fact.Field == "story_day" {
			clocks = append(clocks, fact)
		}
	}
	if len(clocks) != 1 || clocks[0].ThroughChapter != bundle.Chapter || clocks[0].Value != strconv.FormatFloat(end, 'g', -1, 64) {
		t.Fatalf("next chapter lost actual clock end: %+v", clocks)
	}
}

func TestStoryClockSealedBundleRejectsForgedProjectionEvenWithNewHashes(t *testing.T) {
	_, _, bundles := planningV2TestChain(t, 1)
	base := bundles[0]
	bindStoryClockTestBundle(t, &base, 0, 61.0/86400)
	for _, tc := range []struct {
		name string
		edit func(*ProjectedChapterBundle)
		want string
	}{
		{"receipt mismatch", func(bundle *ProjectedChapterBundle) { bundle.ChapterWorldSimulation.StoryTime.EndDay += 1.0 / 86400 }, "differs from sealed arbitration"},
		{"missing projection", func(bundle *ProjectedChapterBundle) {
			bundle.ProjectedDelta.Timeline = bundle.ProjectedDelta.Timeline[1:]
		}, "exactly one"},
		{"duplicate projection", func(bundle *ProjectedChapterBundle) {
			clock := bundle.ProjectedDelta.Timeline[0]
			clock.StableID += "-duplicate"
			bundle.ProjectedDelta.Timeline = append(bundle.ProjectedDelta.Timeline, clock)
		}, "exactly one"},
		{"wrong start", func(bundle *ProjectedChapterBundle) { bundle.ProjectedDelta.Timeline[0].Before = "0.0001" }, "differs from simulation"},
		{"wrong end", func(bundle *ProjectedChapterBundle) { bundle.ProjectedDelta.Timeline[0].After = "2" }, "differs from simulation"},
		{"nonfinite end", func(bundle *ProjectedChapterBundle) { bundle.ProjectedDelta.Timeline[0].After = "NaN" }, "differs from simulation"},
		{"ambiguous object", func(bundle *ProjectedChapterBundle) { bundle.ProjectedDelta.Timeline[0].Object = "shadow-clock" }, "differs from simulation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var changed ProjectedChapterBundle
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			tc.edit(&changed)
			rebindStoryClockTestBundle(t, &changed)
			if err := ValidateProjectedChapterBundle(changed); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("forged clock accepted or wrong failure: %v (want %s)", err, tc.want)
			}
		})
	}
}

func TestStoryClockLegacyBundleCannotMintWorldDay(t *testing.T) {
	_, _, bundles := planningV2TestChain(t, 1)
	bundle := bundles[0]
	if err := ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatalf("unchanged legacy bundle rejected: %v", err)
	}
	bundle.ProjectedDelta.Timeline = append(bundle.ProjectedDelta.Timeline, StateMutationV2{
		StableID: "forged-world-clock", Subject: "world", Field: "story_day", Operation: "advance", Before: "0", After: "1", Cause: "nominal estimate",
	})
	rebindStoryClockTestBundle(t, &bundle)
	if err := ValidateProjectedChapterBundle(bundle); err == nil || !strings.Contains(err.Error(), "requires simulation story_time") {
		t.Fatalf("clockless bundle invented actual world time: %v", err)
	}
}
