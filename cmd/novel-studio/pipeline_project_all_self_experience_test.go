package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func projectAllSelfExperienceArtifacts(t *testing.T, generation domain.PlanningGenerationV2) (*agents.ProjectedChapterArtifacts, domain.OutlineEntry, domain.CharacterAgentRegistry) {
	t.Helper()
	artifacts, outline, registry := projectAllPhysicalArtifacts(t, generation)
	e := *artifacts.CharacterAgentEvidence
	e.Stimulus.Sources = append(e.Stimulus.Sources, domain.CharacterSelfExperiencePolicyV2, domain.CharacterSourceRefPolicyV2)
	for i := range e.Stimulus.PhysicalState.Actors {
		actor := &e.Stimulus.PhysicalState.Actors[i]
		for j := range actor.Resources {
			if actor.Resources[j].ResourceID == physicalUnknownID {
				actor.Resources[j].PerceivedName = "棚内正在整理的检修灯"
				actor.Resources[j].PerceivedLabel = "检修灯（只能用于既有检修工作）"
			}
		}
	}
	var err error
	e.Stimulus, err = domain.FinalizeWorldStimulusPacket(e.Stimulus)
	if err != nil {
		t.Fatal(err)
	}
	for i := range e.Observations {
		o := &e.Observations[i]
		o.StimulusDigest = e.Stimulus.Digest
		o.Sources = []string{domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2}
		for j := range o.KnownFacts {
			o.KnownFacts[j].Source = domain.CharacterSourceRefV2(o.AgentID, o.KnownFacts[j].Source)
		}
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(*e.Stimulus.PhysicalState, o.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		o.Resources = nil
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		if err != nil {
			t.Fatal(err)
		}
		for j := range e.Activation.Entries {
			if e.Activation.Entries[j].AgentID == o.AgentID {
				e.Activation.Entries[j].ObservationDigest = o.Digest
			}
		}
		for j := range e.Proposals {
			p := &e.Proposals[j]
			if p.AgentID != o.AgentID {
				continue
			}
			p.ObservationDigest = o.Digest
			p.SelfTasks = []domain.CharacterSelfTaskV2{{TaskID: "own-work", Kind: "work", Action: "完成本人现场验证", ProgressUnit: "minute", KnowledgeRefs: []string{"known-route"}}}
			if p.Character == "主角" {
				p.SelfTasks = []domain.CharacterSelfTaskV2{
					{TaskID: "carry-light", Kind: "carry", Action: "携检修灯前往B", ResourceIDs: []string{physicalUnknownID}, KnowledgeRefs: []string{"known-route"}},
					{TaskID: "inspection", Kind: "work", Action: "完成本次规定检查", ProgressUnit: "minute", ProgressTarget: projectAllPhysicalNumber(35), ResourceIDs: []string{physicalUnknownID}, KnowledgeRefs: []string{"known-route"}},
				}
			}
			*p, err = domain.FinalizeCharacterDecisionProposal(*p, *o)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	e.Activation, err = domain.FinalizeCharacterAgentActivation(e.Activation)
	if err != nil {
		t.Fatal(err)
	}
	r := e.Arbitrations[0]
	r.Digest, r.StimulusDigest, r.ActivationDigest, r.ProposalDigests = "", e.Stimulus.Digest, e.Activation.Digest, nil
	r.StoryTime = &domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: 0, EndDay: 5.0 / 1440}
	for i := range r.ResourceSettlements {
		r.ResourceSettlements[i].EvidenceRefs = []string{e.Stimulus.Digest}
	}
	for _, p := range e.Proposals {
		r.ProposalDigests = append(r.ProposalDigests, p.Digest)
		for i := range r.Resolutions {
			resolution := &r.Resolutions[i]
			if resolution.AgentID != p.AgentID {
				continue
			}
			resolution.ProposalDigest = p.Digest
			for _, before := range e.Stimulus.PhysicalState.Actors {
				if before.AgentID == p.AgentID {
					copy := before
					copy.Location = "B"
					resolution.PostState = &copy
				}
			}
			resolution.SelfExecutions = []domain.CharacterSelfExecutionV2{{TaskID: "own-work", Status: "completed", StartDay: projectAllPhysicalNumber(0), EndDay: projectAllPhysicalNumber(5.0 / 1440)}}
			if p.Character == "主角" {
				resolution.Outcome, resolution.CompletionState = "partial", "in_progress"
				resolution.SelfExecutions = []domain.CharacterSelfExecutionV2{
					{TaskID: "carry-light", Status: "completed", StartDay: projectAllPhysicalNumber(0), EndDay: projectAllPhysicalNumber(4.0 / 1440)},
					{TaskID: "inspection", Status: "in_progress", StartDay: projectAllPhysicalNumber(4.0 / 1440), EndDay: projectAllPhysicalNumber(5.0 / 1440)},
				}
			}
		}
	}
	r, err = domain.FinalizeWorldArbitrationReceipt(r, e.Stimulus, e.Activation, e.Proposals, 1)
	if err != nil {
		t.Fatal(err)
	}
	e.Arbitrations = []domain.WorldArbitrationReceipt{r}
	e, err = domain.FinalizeCharacterAgentEvidenceBundle(e)
	if err != nil {
		t.Fatal(err)
	}
	after, err := domain.ApplyArbitrationPhysicalStateV2(r, e.Stimulus, e.Proposals...)
	if err != nil {
		t.Fatal(err)
	}
	artifacts.CharacterAgentEvidence = &e
	sim := artifacts.WorldSimulation
	sim.PhysicalState, sim.StoryTime = &after, r.StoryTime
	sim.CharacterDecisions, err = r.CharacterDecisions(e.Proposals, after)
	if err != nil {
		t.Fatal(err)
	}
	protocol := sim.CharacterAgentProtocol
	protocol.StimulusDigest, protocol.ActivationDigest, protocol.ArbitrationDigest = e.Stimulus.Digest, e.Activation.Digest, r.Digest
	protocol.ProposalDigests, protocol.ObservationDigests = nil, nil
	for _, p := range e.Proposals {
		protocol.ProposalDigests = append(protocol.ProposalDigests, p.Digest)
	}
	for _, o := range e.Observations {
		protocol.ObservationDigests = append(protocol.ObservationDigests, o.Digest)
	}
	var payload map[string]any
	if err := json.Unmarshal(artifacts.RenderContext, &payload); err != nil {
		t.Fatal(err)
	}
	mirror := map[string]any{"self_experiences": after.Actors[0].SelfExperiences, "task_progress": after.Actors[0].TaskProgress}
	payload["accidental_owner_history"] = mirror
	encoded, _ := json.Marshal(mirror)
	payload["encoded_owner_history"] = string(encoded)
	artifacts.RenderContext, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return artifacts, outline, registry
}

func TestProjectAllSelfExperienceFlowsThroughDeltaShadowAndOwnerObservation(t *testing.T) {
	generation, obligations := projectAllCmdTestGenerationAndRegistry(t, 2)
	generation.CharacterAgentProtocol = domain.CharacterAgentDecisionProtocolV2Version
	generation.GenerationDigest, _ = domain.ComputePlanningGenerationV2Digest(generation)
	artifacts, outline, registry := projectAllSelfExperienceArtifacts(t, generation)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, obligations, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	bundle, obligations, err := buildPipelineProjectedChapterBundle(generation, outline, genesis, generation.BaseStateRoot, artifacts, obligations)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"self_experiences", "self_executions", "task_progress", "known_placement"} {
		if strings.Contains(string(bundle.RenderContext), key) {
			t.Fatalf("sealed prose received private typed history: %s", key)
		}
	}
	context, err := domain.DeriveProjectedPlanningContextV2(generation, []domain.ProjectedChapterBundle{bundle}, obligations, 2)
	if err != nil {
		t.Fatal(err)
	}
	state, err := domain.ProjectedPhysicalStateV2(context)
	if err != nil || state == nil {
		t.Fatalf("projected self experience missing: %v", err)
	}
	shadow := store.NewStore(t.TempDir())
	if err := shadow.Init(); err != nil {
		t.Fatal(err)
	}
	if err := shadow.Progress.Init("self-experience", 2); err != nil {
		t.Fatal(err)
	}
	if err := shadow.CharacterAgents.SaveRegistry(registry); err != nil {
		t.Fatal(err)
	}
	if err := shadow.Characters.Save([]domain.Character{{Name: "主角", Role: "protagonist", Tier: "core"}, {Name: "同伴", Role: "配角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := shadow.Outline.SaveLayeredOutline([]domain.VolumeOutline{{Index: 1, Arcs: []domain.ArcOutline{{Index: 1, Chapters: []domain.OutlineEntry{outline, {Chapter: 2, Title: "继续检查", CoreEvent: "主角继续本人检查"}}}}}}); err != nil {
		t.Fatal(err)
	}
	if err := advancePipelineProjectAllWorkspace(shadow, generation.GenerationID, 1, artifacts.WorldSimulation, artifacts.Plan, bundle.ProjectedDelta); err != nil {
		t.Fatal(err)
	}
	before, _ := store.DirectoryContentRoot(shadow.Dir())
	observations, err := agents.BuildCharacterObservationsForProjectedState(shadow, generation.GenerationID, 2, context)
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := store.DirectoryContentRoot(shadow.Dir()); before != after {
		t.Fatal("owner observation builder wrote to shadow")
	}
	for _, o := range observations {
		if o.Location != "B" {
			t.Fatal("self observation returned to proposal origin")
		}
		text := strings.Join(domain.FormatCharacterSelfExperiencesV2(o.SelfExperiences, o.TaskProgress), "；")
		if o.Character == "主角" {
			if !strings.Contains(text, "累计有效工时1分钟") || !strings.Contains(text, "尚余34分钟") {
				t.Fatalf("C2 lost 1/35 task progress: %s", text)
			}
			for _, v := range o.ResourceViews {
				if v.ResourceID == physicalUnknownID && (v.KnownPlacement == nil || v.KnownPlacement.Location != "B" || v.KnownPlacement.Kind != "with_actor") {
					t.Fatal("C2 lost explicitly carried tool placement")
				}
			}
		} else if strings.Contains(text, "规定检查") {
			t.Fatal("another owner received private task history")
		}
		raw, _ := json.Marshal(o)
		raw = testutil.CharacterObservationPrivacyJSON(t, raw)
		for _, secret := range []string{"作者私有实量", "作者秘密泵量", "棚内正在整理", "11.8"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("owner self projection leaked %s", secret)
			}
		}
	}
}

func TestProjectAllSelfExperienceAcceptedMemoryRemainsReceiptBound(t *testing.T) {
	opts, st, _ := projectAllCmdTestInstallThreeChapterCLIProjectionWithMutator(t, func(chapter int, artifacts *agents.ProjectedChapterArtifacts) {
		if chapter != 1 {
			return
		}
		generation, _ := projectAllCmdTestGenerationAndRegistry(t, 3)
		generation.GenerationID = artifacts.WorldSimulation.GenerationID
		updated, _, _ := projectAllSelfExperienceArtifacts(t, generation)
		*artifacts = *updated
	}, "v2")
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatal(err)
	}
	if err := pipelinePromote(opts, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatal(err)
	}
	frozen, _, err := loadAndVerifyPipelineFrozenPlan(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := validatePipelineSealedRenderBinding(st, frozen, false)
	if err != nil {
		t.Fatal(err)
	}
	body := "主角开始整理工具。\n\n主角携检修灯来到B，实际完成检查一分钟，距离本人三十五分钟目标尚余三十四分钟，尚未认定结果合格。\n\n主角停下核对。从整理工具到停下核对，一共用了五分钟。"
	projectAllCmdTestWriteFile(t, filepath.Join(st.Dir(), "chapters/01.md"), body)
	bodySHA, err := pipelineRequiredFileSHA(st.Dir(), "chapters/01.md")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := st.Checkpoints.Append(domain.ChapterScope(1), "commit", "chapters/01.md", bodySHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, len([]rune(body)), "choice", "main"); err != nil {
		t.Fatal(err)
	}
	clock, err := derivePipelineSealedStoryClockEvidence(&binding.Bundle, body)
	if err != nil {
		t.Fatal(err)
	}
	match := &pipelineSealedActualDeltaMatch{ActualDelta: binding.Bundle.ProjectedDelta, ProjectionMatch: true, Complete: true, ObligationsSatisfied: binding.Bundle.ObligationsConsumed, StoryClockEvidence: clock}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	canon, err := pipelineProjectAllLiveCanonRoot(st.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := acceptPipelineSealedRenderOutcome(st, binding, commit, bodySHA, canon, match)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range binding.Bundle.CharacterAgentEvidence.Registry.Entries {
		memory, err := st.CharacterAgents.LoadCanonicalMemory(actor.AgentID)
		if err != nil || memory == nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(memory)
		raw = testutil.CharacterMemoryPrivacyJSON(t, raw)
		if actor.Character == "主角" && (!strings.Contains(string(raw), "累计有效工时1分钟") || !strings.Contains(string(raw), "尚余34分钟")) {
			t.Fatal("accepted memory did not preserve actually executed self progress")
		}
		if actor.Character != "主角" && strings.Contains(string(raw), "尚余34分钟") {
			t.Fatal("accepted memory crossed owner knowledge boundary")
		}
		if strings.Contains(string(raw), "11.8") || strings.Contains(string(raw), "棚内正在整理") {
			t.Fatal("accepted experience memory contains author truth/stale current description")
		}
		if len(memory.Facts) != 1 || memory.Facts[0].SourceDigest != outcome.ReceiptDigest {
			t.Fatal("accepted experience lost exact outcome provenance")
		}
	}
	if err := applyPipelineAcceptedCharacterMemoryPublication(st, binding.Bundle, *outcome); err != nil {
		t.Fatalf("accepted self memory restart was not idempotent: %v", err)
	}
}
