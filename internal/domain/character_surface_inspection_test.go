package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func surfaceFixture(t *testing.T) physicalProtocolFixture {
	t.Helper()
	f := newOperationalObservationFixture(t)
	f.stimulus.Sources = append(f.stimulus.Sources, CharacterSurfaceInspectionPolicyV1)
	for i := range f.stimulus.PhysicalState.Resources {
		if f.stimulus.PhysicalState.Resources[i].ResourceID == operationalPowerTestID {
			f.stimulus.PhysicalState.Resources[i].InspectableSurfaces = []string{CharacterSurfaceContainerExteriorV1, CharacterSurfaceSealExteriorV1}
			f.stimulus.PhysicalState.Resources[i].ReadableFacts = []ResourceReadableFactV2{{ID: "outer-list", Text: "SECRET_MANIFEST_DO_NOT_READ_BY_INSPECTION"}}
		}
	}
	f.proposals[0].SelfTasks[2].ResourceIDs = append(f.proposals[0].SelfTasks[2].ResourceIDs, operationalPowerTestID)
	f.proposals[0].SelfTasks[2].ObservationRequests[0].Surface = CharacterSurfaceSealExteriorV1
	f.proposals[0].SelfTasks[2].ObservationRequests[0].Purpose = "想确认从未开启以及内部数量，申请用途本身不能变真"
	f.receipt.Resolutions[0].SelfExecutions[2].ObservationResults[0].Surface = CharacterSurfaceSealExteriorV1
	f.receipt.Resolutions[0].SelfExecutions[2].ObservationResults[0].Result = "no_visible_damage"
	var err error
	f.stimulus, err = FinalizeWorldStimulusPacket(f.stimulus)
	if err != nil {
		t.Fatal(err)
	}
	f.receipt.StimulusDigest = f.stimulus.Digest
	for i := range f.observations {
		o := &f.observations[i]
		o.Sources = append(o.Sources, CharacterSurfaceInspectionPolicyV1)
		o.StimulusDigest = f.stimulus.Digest
		o.ResourceViews, err = BuildCharacterResourceViewsForSourcesV2(*f.stimulus.PhysicalState, o.AgentID, o.Sources)
		if err != nil {
			t.Fatal(err)
		}
		*o, err = FinalizeCharacterObservationPacket(*o)
		if err != nil {
			t.Fatal(err)
		}
		f.proposals[i].ObservationDigest, f.activation.Entries[i].ObservationDigest = o.Digest, o.Digest
	}
	f.activation, err = FinalizeCharacterAgentActivation(f.activation)
	if err != nil {
		t.Fatal(err)
	}
	f.receipt.ActivationDigest = f.activation.Digest
	rebindPhysicalTestProposals(t, &f)
	settlePhysicalFuel(&f)
	return f
}

func TestSurfaceInspectionV1SameDocumentResourceOwnerReceiptAndNextView(t *testing.T) {
	for _, value := range []string{"no_visible_damage", "visible_damage", "indeterminate"} {
		t.Run(value, func(t *testing.T) {
			f := surfaceFixture(t)
			f.receipt.Resolutions[0].SelfExecutions[2].ObservationResults[0].Result = value
			before, _ := json.Marshal(f.stimulus)
			r, err := finalizePhysicalFixture(f)
			if err != nil {
				t.Fatal(err)
			}
			state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
			if err != nil {
				t.Fatal(err)
			}
			owner := state.Actors[0]
			if len(owner.OperationalObservations) != 1 || len(state.Actors[1].OperationalObservations) != 0 || len(owner.ReceivedFacts) != 0 {
				t.Fatal("inspection leaked/read contents or synchronized another owner")
			}
			observation := owner.OperationalObservations[0]
			if observation.Surface != CharacterSurfaceSealExteriorV1 || observation.Result != value || observation.SourceProposalDigest != f.proposals[0].Digest || *observation.ObservedAtDay != *selfTestDay(4.5) {
				t.Fatal("receipt lost exact request/source/time")
			}
			text := strings.Join(FormatCharacterOperationalObservationsV1(owner.OperationalObservations), "\n")
			if strings.Contains(text, "SECRET_MANIFEST") || !strings.Contains(text, "不证明从未开启") || !strings.Contains(text, "申请用途仍是本人意图") {
				t.Fatal("surface semantics leaked text or promoted intention/permission")
			}
			for _, resource := range state.Resources {
				if resource.ResourceID == operationalPowerTestID && (len(resource.ReadableFacts) != 1 || len(resource.InspectableSurfaces) != 2) {
					t.Fatal("inspection rewrote resource/document definition")
				}
			}
			next := f.stimulus
			next.Chapter, next.PhysicalState = 2, &state
			next, err = FinalizeWorldStimulusPacket(next)
			if err != nil {
				t.Fatal(err)
			}
			o := f.observations[0]
			o.Chapter, o.StimulusDigest, o.Location = 2, next.Digest, owner.Location
			o.ResourceViews, err = BuildCharacterResourceViewsForSourcesV2(state, o.AgentID, o.Sources)
			if err != nil {
				t.Fatal(err)
			}
			o.SelfExperiences, o.TaskProgress, err = BuildCharacterSelfObservationV2(state, o.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			o.OperationalObservations, err = BuildCharacterOperationalObservationsV1(state, o.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			o, err = FinalizeCharacterObservationPacket(o)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateCharacterResourceViewsAgainstStimulusV2(next, o); err != nil {
				t.Fatal(err)
			}
			if _, ok := o.AllowedFactIDs()[observation.ID]; !ok || o.OperationalObservations[0].Chapter != 1 {
				t.Fatal("next view lost source or refreshed old observation")
			}
			for _, tamper := range []string{"value", "time", "owner", "source", "policy", "facet"} {
				raw, _ := json.Marshal(o)
				var bad CharacterObservationPacket
				_ = json.Unmarshal(raw, &bad)
				switch tamper {
				case "value":
					if value == "visible_damage" {
						bad.OperationalObservations[0].Result = "indeterminate"
					} else {
						bad.OperationalObservations[0].Result = "visible_damage"
					}
				case "time":
					bad.OperationalObservations[0].ObservedAtDay = selfTestDay(4.75)
				case "owner":
					bad.AgentID = "ca_b"
				case "source":
					bad.OperationalObservations[0].SourceProposalDigest = "sha256:" + strings.Repeat("f", 64)
				case "policy":
					bad.Sources = []string{CharacterSourceRefPolicyV2, CharacterSelfExperiencePolicyV2, CharacterOperationalAvailabilityPolicyV1}
				case "facet":
					bad.OperationalObservations[0].Surface = CharacterSurfaceContainerExteriorV1
				}
				bad.OperationalObservations[0].ID = CharacterOperationalObservationIDV1(bad.OperationalObservations[0])
				if err := ValidateCharacterResourceViewsAgainstStimulusV2(next, bad); err == nil {
					t.Fatalf("tampered %s accepted", tamper)
				}
			}
			after, _ := json.Marshal(f.stimulus)
			if !bytes.Equal(before, after) {
				t.Fatal("inspection mutated the frozen input")
			}
		})
	}
}

func TestSurfaceInspectionV1RejectsUnboundTargetsAndResults(t *testing.T) {
	for _, mode := range []string{"no-task-target", "unknown-facet", "no-policy", "inaccessible", "undeclared-facet"} {
		t.Run(mode, func(t *testing.T) {
			f := surfaceFixture(t)
			p, o := f.proposals[0], f.observations[0]
			switch mode {
			case "no-task-target":
				p.SelfTasks[2].ResourceIDs = []string{physicalPaperTestID}
			case "unknown-facet":
				p.SelfTasks[2].ObservationRequests[0].Surface = "hidden_contents"
			case "no-policy":
				o.Sources = []string{CharacterOperationalAvailabilityPolicyV1, CharacterSelfExperiencePolicyV2}
			case "inaccessible", "undeclared-facet":
				for i := range o.ResourceViews {
					if o.ResourceViews[i].ResourceID == operationalPowerTestID {
						if mode == "inaccessible" {
							o.ResourceViews[i].Access = "none"
						} else {
							o.ResourceViews[i].InspectableSurfaces = nil
						}
					}
				}
			}
			if err := ValidateCharacterOperationalObservationIntentV1(p, o); err == nil {
				t.Fatal("unbound surface intent accepted")
			}
		})
	}
	for _, mode := range []string{"old-result", "missing-surface", "different-surface", "future-time", "wrong-request", "unexecuted"} {
		t.Run(mode, func(t *testing.T) {
			f := surfaceFixture(t)
			e := &f.receipt.Resolutions[0].SelfExecutions[2]
			switch mode {
			case "old-result":
				e.ObservationResults[0].Result = "available"
			case "missing-surface":
				e.ObservationResults[0].Surface = ""
				e.ObservationResults[0].Result = "available"
			case "different-surface":
				e.ObservationResults[0].Surface = CharacterSurfaceContainerExteriorV1
			case "future-time":
				e.ObservationResults[0].ObservedAtDay = selfTestDay(6)
			case "wrong-request":
				e.ObservationResults[0].RequestID = "other-owner-request"
			case "unexecuted":
				e.Status = "not_started"
				e.StartDay, e.EndDay = nil, nil
			}
			if _, err := finalizePhysicalFixture(f); err == nil {
				t.Fatal("unbound surface result accepted")
			}
		})
	}
	f := surfaceFixture(t)
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	owner := state.Actors[0]
	for i := range owner.SelfExperiences {
		if owner.SelfExperiences[i].ID == owner.OperationalObservations[0].SourceExperienceID {
			owner.SelfExperiences[i].ResourceIDs = nil
		}
	}
	if err := validateCharacterOperationalStateV1(owner, nil); err == nil {
		t.Fatal("stored surface receipt used unrelated work without target")
	}
}

func TestSurfaceInspectionV1DefinitionsAndLegacyProjectionStaySeparate(t *testing.T) {
	for _, surfaces := range [][]string{{"intact"}, {"hidden_contents"}, {CharacterSurfaceSealExteriorV1, CharacterSurfaceSealExteriorV1}} {
		if err := ValidateInspectableSurfacesV1(surfaces); err == nil {
			t.Fatal("invalid capability definition accepted")
		}
		character := Character{Name: "甲", InitialState: &CharacterInitialState{Location: "值班室", CurrentGoal: "核对", Pressure: "有限时间", KnownFacts: []string{"旧接班记忆"}, ResourceBalances: []InitialCharacterResourceV2{{ResourceID: operationalPowerTestID, Name: "封袋", Access: "exclusive", Perception: ResourcePerceptionV2{Kind: "unknown"}, InspectableSurfaces: surfaces}}}}
		if err := ValidateCharacterInitialState(character); err == nil {
			t.Fatal("initial-state save boundary accepted invalid surfaces")
		}
	}
	f := surfaceFixture(t)
	old, err := BuildCharacterResourceViewsV2(*f.stimulus.PhysicalState, "ca_a")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := BuildCharacterResourceViewsForSourcesV2(*f.stimulus.PhysicalState, "ca_a", nil)
	if err != nil || !samePhysicalValueV2(old, legacy) {
		t.Fatal("legacy builder semantics changed")
	}
	raw, _ := json.Marshal(old)
	if bytes.Contains(raw, []byte("inspectable_surfaces")) || bytes.Contains(raw, []byte("SECRET_MANIFEST")) {
		t.Fatal("legacy view leaked new capability or readable content")
	}
	modern, err := BuildCharacterResourceViewsForSourcesV2(*f.stimulus.PhysicalState, "ca_a", f.stimulus.Sources)
	if err != nil {
		t.Fatal(err)
	}
	for i := range modern {
		if modern[i].ResourceID == operationalPowerTestID {
			modern[i].InspectableSurfaces[0] = "mutated"
		}
	}
	for _, resource := range f.stimulus.PhysicalState.Resources {
		if resource.ResourceID == operationalPowerTestID && resource.InspectableSurfaces[0] == "mutated" {
			t.Fatal("view capability aliases host source")
		}
	}
	previous, current := ProjectedChapterBundle{}, ProjectedChapterBundle{}
	current.ChapterWorldSimulation.Sources = []string{CharacterSurfaceInspectionPolicyV1}
	if err := validateGenerationContinuationHistoryPredecessor(previous, current); err == nil {
		t.Fatal("surface marker entered an existing generation")
	}
}
