package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func bindPipelineActualClockTestBundle(t *testing.T, bundle *domain.ProjectedChapterBundle, startSeconds, elapsedSeconds float64) {
	t.Helper()
	decision := bundle.ChapterWorldSimulation.CharacterDecisions[0]
	registry, err := domain.FinalizeCharacterAgentRegistry(domain.CharacterAgentRegistry{Entries: []domain.CharacterAgentRecord{{
		AgentID: "ca_clock", Character: decision.Character, Tier: "core", Status: domain.CharacterAgentActive, MemoryVersion: 1,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := domain.DeriveStoryTimeContract("3章，10天", 3)
	if err != nil {
		t.Fatal(err)
	}
	clock, err := domain.FinalizeStoryClockContext(domain.StoryClockContext{
		CurrentDay: startSeconds / 86400, TimeContractCoreDigest: contract.CoreDigest,
		DurationDaysMin: contract.DurationDaysMin, DurationDaysMax: contract.DurationDaysMax,
	})
	if err != nil {
		t.Fatal(err)
	}
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, TimeWindow: "当前现实时间", StoryClock: &clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := domain.FinalizeCharacterObservationPacket(domain.CharacterObservationPacket{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, AgentID: "ca_clock", Character: decision.Character,
		CurrentGoal: decision.CurrentGoal, Pressure: decision.Pressure, StimulusDigest: stimulus.Digest, MemoryRoot: "sha256:memory",
		KnownFacts: []domain.CharacterAgentFact{{ID: "known-1", Kind: "known", Text: "本人亲历和收到的票据"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := domain.FinalizeCharacterAgentActivation(domain.CharacterAgentActivation{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, RegistryRoot: registry.RegistryRoot,
		Entries: []domain.CharacterAgentActivationEntry{{AgentID: observation.AgentID, Character: observation.Character, Tier: "core", State: domain.CharacterAgentActive, Reasons: []string{"protagonist"}, ObservationDigest: observation.Digest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := domain.FinalizeCharacterDecisionProposal(domain.CharacterDecisionProposal{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, AgentID: observation.AgentID, Character: observation.Character,
		ObservationDigest: observation.Digest, Location: decision.Location, CurrentGoal: decision.CurrentGoal, Pressure: decision.Pressure,
		AvailableOptions: decision.AvailableOptions, Decision: decision.Decision, DecisionReason: decision.DecisionReason,
		IntendedAction: decision.Action, ActionDuration: decision.ActionDuration, KnowledgeRefs: []string{"known-1"},
	}, observation)
	if err != nil {
		t.Fatal(err)
	}
	arbitration, err := domain.FinalizeWorldArbitrationReceipt(domain.WorldArbitrationReceipt{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Round: 1, Finalized: true, HardContractStatus: "feasible",
		StimulusDigest: stimulus.Digest, ActivationDigest: activation.Digest, ProposalDigests: []string{proposal.Digest},
		StoryTime: &domain.StoryTimeChapterSchedule{Chapter: bundle.Chapter, StartDay: startSeconds / 86400, EndDay: (startSeconds + elapsedSeconds) / 86400},
		Resolutions: []domain.CharacterDecisionResolution{{
			AgentID: proposal.AgentID, Character: proposal.Character, ProposalDigest: proposal.Digest,
			Decision: proposal.Decision, IntendedAction: proposal.IntendedAction, ActionOrder: 1, Outcome: "success",
			CompletionState: decision.CompletionState, ImmediateResult: decision.ImmediateResult, StateAfter: decision.StateAfter,
			ButterflyEffects: []domain.DecisionButterflyEffect{{Effect: "票据可供复核", TransmissionPath: "携带票据", ArrivalChapter: bundle.Chapter, Visibility: "visible", ProtagonistImpact: "可以继续核查"}},
		}},
		ProtagonistProjection: bundle.ChapterWorldSimulation.ProtagonistProjection,
	}, stimulus, activation, []domain.CharacterDecisionProposal{proposal}, 1)
	if err != nil {
		t.Fatal(err)
	}
	protocol := "sha256:" + strings.Repeat("c", 64)
	evidence, err := domain.FinalizeCharacterAgentEvidenceBundle(domain.CharacterAgentEvidenceBundle{
		GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, Registry: registry, Stimulus: stimulus, Activation: activation,
		Observations: []domain.CharacterObservationPacket{observation}, Proposals: []domain.CharacterDecisionProposal{proposal},
		Arbitrations: []domain.WorldArbitrationReceipt{arbitration}, MemoryRoots: []string{observation.MemoryRoot}, ProtocolDigest: protocol,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle.CharacterAgentEvidence = &evidence
	bundle.ChapterWorldSimulation.Version = 2
	bundle.ChapterWorldSimulation.StoryTime = arbitration.StoryTime
	bundle.ChapterWorldSimulation.TimeWindow = "已裁决的连续现实时间"
	bundle.ChapterWorldSimulation.CharacterDecisions, err = arbitration.CharacterDecisions([]domain.CharacterDecisionProposal{proposal})
	if err != nil {
		t.Fatal(err)
	}
	bundle.ChapterWorldSimulation.CharacterAgentProtocol = &domain.CharacterAgentProtocolReceipt{
		Version: domain.CharacterAgentDecisionProtocolVersion, RegistryRoot: registry.RegistryRoot, StimulusDigest: stimulus.Digest,
		ActivationDigest: activation.Digest, ObservationDigests: []string{observation.Digest}, ProposalDigests: []string{proposal.Digest},
		ArbitrationRound: 1, ArbitrationDigest: arbitration.Digest, MemoryRoots: evidence.MemoryRoots, ProtocolDigest: protocol,
	}
	bundle.ProjectedDelta.Timeline = append(bundle.ProjectedDelta.Timeline, domain.StateMutationV2{
		StableID: "actual-clock-fixture", Subject: "world", Field: "story_day", Operation: "advance", Cause: "world arbitration",
		Before: strconv.FormatFloat(arbitration.StoryTime.StartDay, 'g', -1, 64), After: strconv.FormatFloat(arbitration.StoryTime.EndDay, 'g', -1, 64),
	})
	rebindPipelineSealedActualTestBundle(t, bundle)
}

func TestPipelineSealedStoryClockNaturalBodyEvidence(t *testing.T) {
	const middle = "\n\n他把信封摊在桌上，逐页检查。门外响起脚步，他护住纸页，直到对方拿出钥匙才让开。\n\n"
	for _, tc := range []struct {
		name, start, end string
		elapsed          float64
		valid            bool
	}{
		{"second precision", "他看手机，屏幕显示23:00:00。", "他又看手机，屏幕显示23:01:01。", 61, true},
		{"Chinese clock", "墙上的钟指向七点零三分。", "她看表，七点二十一分。", 18 * 60, true},
		{"Chinese no minute suffix", "墙上的钟指向七点零三。", "她看表，七点二十一。", 18 * 60, true},
		{"natural deadline", "距封航还剩七十二分钟。", "距离封航只剩五十四分钟。", 18 * 60, true},
		{"two consistent deadlines", "距封航还剩七十二分钟。距闭馆还剩三十分钟。", "距离封航只剩五十四分钟。距离闭馆只剩十二分钟。", 18 * 60, true},
		{"named countdown", "封航倒计时显示01:12:00。", "封航倒计时显示00:54:00。", 18 * 60, true},
		{"bounded elapsed", "他进入档案室，把门推开。", "从进入档案室到走出门，整整过去了六十一秒。", 61, true},
		{"plain elapsed without device", "他开始核账，一页一页翻着。", "从核账到回到柜台共5分钟。", 5 * 60, true},
		{"plain elapsed no comma", "他开始核账，一页一页翻着。", "从核账到回到柜台用了五分钟。", 5 * 60, true},
		{"plain elapsed absent opening event", "他在门边站着等钥匙。", "从核账到回到柜台共5分钟。", 5 * 60, false},
		{"plain elapsed negated opening event", "他没有核账，只在门边等钥匙。", "从核账到回到柜台共5分钟。", 5 * 60, false},
		{"plain elapsed imagined", "他开始核账，一页一页翻着。", "如果从核账到回到柜台共5分钟，就能赶上封航。", 5 * 60, false},
		{"explicit midnight", "他看手机，屏幕显示23:59:30。", "越过午夜，他看手机，屏幕显示00:00:31。", 61, true},
		{"explicit next day", "墙上的钟指向七点零三分。", "第二天，她看表，七点二十一分。", 86400 + 18*60, true},
		{"explicit two days", "墙上的钟指向七点零三分。", "两天后，她看表，七点二十一分。", 2*86400 + 18*60, true},
		{"explicit ordinal days", "第一天，墙上的钟指向七点零三分。", "第三天，她看表，七点二十一分。", 2*86400 + 18*60, true},
		{"repeated ordinal is same day", "墙上的钟指向七点零三分。第二天终于等来钥匙。", "第二天，她看表，七点二十一分。", 86400 + 18*60, true},
		{"wrong elapsed", "墙上的钟指向七点零三分。", "她看表，七点二十二分。", 18 * 60, false},
		{"different deadline", "距封航还剩七十二分钟。", "距离闭馆只剩五十四分钟。", 18 * 60, false},
		{"ambiguous elapsed", "他进入档案室。", "过了一会儿，他走出了门。", 61, false},
		{"parallel not summed", "他检查文件用了三分钟，同伴同时检索也用了三分钟。", "两人一起出了门。", 6 * 60, false},
		{"hypothetical readings", "如果墙上的钟指向七点零三分。", "如果她看表，七点二十一分，就该离开。", 18 * 60, false},
		{"old footage", "录像里墙上的钟指向七点零三分。", "旧影像里她看表，七点二十一分。", 18 * 60, false},
		{"written schedule is not clock", "表格显示07:03。", "表格显示07:21。", 18 * 60, false},
		{"different stopwatches", "他的秒表显示01:00。", "她的秒表显示02:01。", 61, false},
		{"approximate clock", "墙上的钟大约指向七点零三分。", "她看表，差不多七点二十一分。", 18 * 60, false},
		{"unproved midnight", "他看手机，屏幕显示23:59:30。", "他看手机，屏幕显示00:00:31。", 61, false},
		{"hypothetical day", "他看手机，屏幕显示23:59:30。", "如果撑过午夜就好了。他看手机，屏幕显示00:00:31。", 61, false},
		{"hypothetical span", "他进入档案室，把门推开。", "如果从进入档案室到走出门，整整过去了六十一秒，就能赶上。", 61, false},
		{"contradictory clocks", "他看手机，屏幕显示23:00:00。距封航还剩七十二分钟。", "他又看手机，屏幕显示23:01:01。距离封航只剩七十分钟。", 61, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPipelineSealedActualTestFixture(t)
			bindPipelineActualClockTestBundle(t, &fixture.Bundle, 0, tc.elapsed)
			body := tc.start + middle + tc.end
			evidence, err := derivePipelineSealedStoryClockEvidence(&fixture.Bundle, body)
			if (err == nil && evidence != nil) != tc.valid {
				t.Fatalf("valid=%t evidence=%+v err=%v anchors=%+v", tc.valid, evidence, err, collectPipelineStoryClockAnchors(body))
			}
			if tc.valid && (evidence.BodySHA256 != pipelineStoryClockBodySHA(body) || math.Abs(evidence.ElapsedSeconds-tc.elapsed) > 1e-6 || evidence.StartRune >= evidence.EndRune) {
				t.Fatalf("clock evidence lost body/numeric identity: %+v", evidence)
			}
		})
	}
}

func TestPipelineSealedActualClockDoesNotReplaceOrdinaryTimelineEvidence(t *testing.T) {
	fixture := newPipelineSealedActualTestFixture(t)
	bindPipelineActualClockTestBundle(t, &fixture.Bundle, 0, 61)
	body := "他看手机，屏幕显示23:00:00。\n\n" + fixture.Body + "\n\n他又看手机，屏幕显示23:01:01。"
	match, err := matchPipelineSealedRenderActualDelta(fixture.Store, &fixture.Bundle, &fixture.Candidate, body)
	if err != nil || !match.ProjectionMatch || match.StoryClockEvidence == nil {
		t.Fatalf("body-grounded actual clock did not integrate: %+v, %v", match, err)
	}
	if got, err := matchPipelineSealedRenderActualDelta(fixture.Store, &fixture.Bundle, &fixture.Candidate, fixture.Body); err != nil || got.ProjectionMatch || !pipelineSealedActualTestContains(got.MismatchReasons, "story_clock") {
		t.Fatalf("matching planned metadata supplied a missing body clock: %+v, %v", got, err)
	}
	clockOnly := "他看手机，屏幕显示23:00:00。\n\n" + strings.Repeat("他站在门前等着，始终没有交易。", 12) + "\n\n他又看手机，屏幕显示23:01:01。"
	if got, err := matchPipelineSealedRenderActualDelta(fixture.Store, &fixture.Bundle, &fixture.Candidate, clockOnly); err != nil || got.ProjectionMatch || got.StoryClockEvidence == nil {
		t.Fatalf("numeric clock bypassed ordinary timeline/hard beats: %+v, %v", got, err)
	}
	fixture.Candidate.WorldDeltas = append(fixture.Candidate.WorldDeltas, domain.WorldChapterDelta{Kind: "timeline", Entity: "world:story_day", Change: "0 -> 1", Evidence: body})
	if err := fixture.Store.SaveChapterWorldDelta(fixture.Candidate); err != nil {
		t.Fatal(err)
	}
	if got, err := matchPipelineSealedRenderActualDelta(fixture.Store, &fixture.Bundle, &fixture.Candidate, body); err != nil || got.ProjectionMatch || !pipelineSealedActualTestContains(got.MismatchReasons, "contradicts exact-body") {
		t.Fatalf("contradictory commit clock ignored: %+v, %v", got, err)
	}
}

func TestPipelineSealedClockAcceptanceRechecksExactBodyAndWitness(t *testing.T) {
	fixture := newPipelineSealedActualTestFixture(t)
	bindPipelineActualClockTestBundle(t, &fixture.Bundle, 0, 61)
	body := "他看手机，屏幕显示23:00:00。\n\n" + fixture.Body + "\n\n他又看手机，屏幕显示23:01:01。"
	match, err := matchPipelineSealedRenderActualDelta(fixture.Store, &fixture.Bundle, &fixture.Candidate, body)
	if err != nil || !match.ProjectionMatch {
		t.Fatalf("fixture did not independently match: %+v, %v", match, err)
	}
	path := filepath.Join(fixture.Store.Dir(), "chapters", "01.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	commit := &domain.Checkpoint{Seq: 1, Scope: domain.ChapterScope(1), Step: "commit", Artifact: "chapters/01.md", Digest: pipelineStoryClockBodySHA(body)}
	if err := validatePipelineSealedStoryClockMatch(fixture.Store, &fixture.Bundle, commit, commit.Digest, &match); err != nil {
		t.Fatalf("exact body and witness rejected: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*pipelineSealedActualDeltaMatch)
	}{
		{"missing witness", func(m *pipelineSealedActualDeltaMatch) { m.StoryClockEvidence = nil }},
		{"forged locator", func(m *pipelineSealedActualDeltaMatch) { m.StoryClockEvidence.StartRune++ }},
		{"forged hash", func(m *pipelineSealedActualDeltaMatch) {
			m.StoryClockEvidence.BodySHA256 = "sha256:" + strings.Repeat("a", 64)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(match)
			var changed pipelineSealedActualDeltaMatch
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			tc.edit(&changed)
			if _, err := acceptPipelineSealedRenderOutcome(fixture.Store, &pipelineSealedRenderBinding{Bundle: fixture.Bundle}, commit, commit.Digest, "sha256:canon", &changed); err == nil || !strings.Contains(err.Error(), "story_clock") {
				t.Fatalf("forged match reached outcome acceptance: %v", err)
			}
		})
	}
	changedBody := strings.Replace(body, "23:01:01", "23:02:01", 1)
	if err := os.WriteFile(path, []byte(changedBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineSealedStoryClockMatch(fixture.Store, &fixture.Bundle, commit, commit.Digest, &match); err == nil {
		t.Fatal("body edit retained acceptance proof")
	}
	commit.Digest = pipelineStoryClockBodySHA(changedBody)
	match.StoryClockEvidence.BodySHA256 = commit.Digest
	if err := validatePipelineSealedStoryClockMatch(fixture.Store, &fixture.Bundle, commit, commit.Digest, &match); err == nil {
		t.Fatal("refreshing hashes hid a contradictory actual body time")
	}
}

func TestPipelineSealedClockContractReachesFrozenDrafterEnvelope(t *testing.T) {
	fixture := newPipelineSealedActualTestFixture(t)
	bindPipelineActualClockTestBundle(t, &fixture.Bundle, 61, 18*60)
	var payload map[string]any
	if err := json.Unmarshal(fixture.Bundle.RenderContext, &payload); err != nil {
		t.Fatal(err)
	}
	contract, ok := payload["story_time_render_contract"].(map[string]any)
	if !ok || math.Abs(contract["elapsed_seconds"].(float64)-18*60) > 1e-6 || !strings.Contains(contract["guidance"].(string), "剩余时间") {
		t.Fatalf("Drafter lacks natural numeric time obligation: %+v", contract)
	}
	envelope, _, err := aigc.BuildProseRenderPrimingEnvelope(1, "sha256:"+strings.Repeat("d", 64), fixture.Bundle.RenderContext)
	if err != nil || !strings.Contains(envelope, "story_time_render_contract") || !strings.Contains(envelope, "并行动作") {
		t.Fatalf("frozen provider priming dropped the time contract: %v", err)
	}
	delete(payload, "story_time_render_contract")
	fixture.Bundle.RenderContext, _ = json.Marshal(payload)
	fixture.Bundle.RenderContextSHA256, _ = domain.ComputePlanningV2JSONDigest(fixture.Bundle.RenderContext)
	fixture.Bundle.BundleDigest, _ = domain.ComputeProjectedChapterBundleDigest(fixture.Bundle)
	if err := domain.ValidateProjectedChapterBundle(fixture.Bundle); err == nil || !strings.Contains(err.Error(), "story_time_render_contract") {
		t.Fatalf("new clock gate was hidden from Drafter by a refreshed context hash: %v", err)
	}
}

func TestPipelineAcceptedStoryClockReplaysSealedOutcomeAgainstDurableBody(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	source := domain.PlanningSourceSnapshotV2{
		Version: domain.PlanningSourceSnapshotV2Version, GenerationID: generation.GenerationID,
		BaseCanonChapter: generation.BaseCanonChapter, BaseCanonRoot: generation.BaseCanonRoot, BaseStateRoot: generation.BaseStateRoot,
		StableOutlineRoot: generation.StableOutlineRoot, PlanningDependencyRoot: generation.PlanningDependencyRoot,
		RandomSeedContractRoot: generation.RandomSeedContractRoot, FoundationSnapshotRoot: projectAllCmdTestDigest("clock-foundation"),
		RAGSnapshotRoot: projectAllCmdTestDigest("clock-rag"), CapturedAt: "2026-07-17T00:00:00Z",
	}
	var err error
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	projected := st.ProjectedV2()
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	bundle, nextRegistry, err := buildPipelineProjectedChapterBundle(generation, outline, genesis, generation.BaseStateRoot, artifacts, registry)
	if err != nil {
		t.Fatal(err)
	}
	bindPipelineActualClockTestBundle(t, &bundle, 0, 18*60)
	if err := projected.SaveObligationRegistry(generation.GenerationID, nextRegistry); err != nil {
		t.Fatal(err)
	}
	if err := projected.SaveProjectedChapterBundle(bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := projected.SealGeneration(generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	body := "墙上的钟指向七点零三分。\n\n" + strings.Repeat("他把票据折好，再次核对上面的编号。", 6) + "\n\n她看表，七点二十一分。"
	path := filepath.Join(st.Dir(), "chapters", "01.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome := &domain.ActualOutcomeReceiptV2{
		GenerationID: generation.GenerationID, Chapter: 1, ChapterBodySHA256: pipelineStoryClockBodySHA(body),
		ActualDelta: bundle.ProjectedDelta,
	}
	if err := validatePipelineAcceptedStoryClock(st, outcome); err != nil {
		t.Fatalf("accepted numeric clock did not replay from exact sealed body: %v", err)
	}
	changed := *outcome
	changed.ActualDelta = domain.NormalizeProjectedDeltaV2(outcome.ActualDelta)
	for i := range changed.ActualDelta.Timeline {
		if pipelineSealedStoryClockMutation("timeline", changed.ActualDelta.Timeline[i]) {
			changed.ActualDelta.Timeline[i].After = "0.02"
		}
	}
	if err := validatePipelineAcceptedStoryClock(st, &changed); err == nil {
		t.Fatal("canon accepted an actual clock different from adjudication and body")
	}
	changedBody := strings.Replace(body, "七点二十一分", "七点二十二分", 1)
	if err := os.WriteFile(path, []byte(changedBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineAcceptedStoryClock(st, outcome); err == nil || !strings.Contains(err.Error(), "body hash") {
		t.Fatalf("canon lost exact body identity: %v", err)
	}
	outcome.ChapterBodySHA256 = pipelineStoryClockBodySHA(changedBody)
	if err := validatePipelineAcceptedStoryClock(st, outcome); err == nil || !strings.Contains(err.Error(), "实际经过") {
		t.Fatalf("refreshing body/outcome hashes turned planned time into actual evidence: %v", err)
	}
	if err := validatePipelineAcceptedStoryClock(st, &domain.ActualOutcomeReceiptV2{}); err != nil {
		t.Fatalf("clockless legacy outcome acquired new body requirements: %v", err)
	}
}
