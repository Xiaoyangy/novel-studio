package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

const rehearsalSurfaceResourceID = "res_1111111111111111"

func surfaceRehearsalFixture(t *testing.T) (*store.Store, domain.ArcRehearsalInput, bootstrap.Config) {
	t.Helper()
	st, binding, cfg := rehearsalCapabilityFixture(t)
	characters, err := st.Characters.Load()
	selectionMust(t, err)
	// A mixed document/container keeps the original readable material and ID.
	characters[0].InitialState.ResourceBalances[0].InspectableSurfaces = []string{"container_exterior", "seal_exterior"}
	selectionMust(t, st.Characters.Save(characters))
	input, err := BuildArcRehearsalInput(st, binding, cfg)
	selectionMust(t, err)
	if input.ExecutionCapabilities.Policy != domain.ArcRehearsalCapabilityPolicyV2 || !slices.Contains(input.ExecutionCapabilities.ActionKinds, "surface_inspection") {
		t.Fatal("current surface producer did not bind a new rehearsal profile")
	}
	return st, input, cfg
}

func surfaceRehearsalBody(input domain.ArcRehearsalInput) domain.ArcRehearsalBody {
	b := arcRehearsalTestBody(input, false)
	for _, surface := range []string{"container_exterior", "seal_exterior"} {
		b.MaterialChecks = append(b.MaterialChecks, domain.ArcRehearsalMaterialCheck{
			Operation: surface, Status: "available", Explanation: "只验证同一现有对象有外表面检查入口，不声明当前状态或内部内容",
			ResourceRefs:           []string{rehearsalSurfaceResourceID},
			CapabilityRequirements: []domain.ArcRehearsalCapabilityRequirementV1{{Key: surface, Kind: "surface_inspection", ActorRef: input.CharacterObservations[0].AgentID, ResourceRefs: []string{rehearsalSurfaceResourceID}, MechanismRefs: []string{"M_SAIL"}, Surface: surface}},
		})
	}
	return b
}

func TestSurfaceRehearsalCapabilitiesActualRunnerPreservesMixedSource(t *testing.T) {
	st, input, cfg := surfaceRehearsalFixture(t)
	before := arcRehearsalSourceFiles(t, st.Dir())
	resourceCount := len(input.WorldState.Resources)
	for _, resource := range input.WorldState.Resources {
		if resource.ResourceID == rehearsalSurfaceResourceID && (len(resource.ReadableFacts) != 1 || resource.ReadableFacts[0].Text != "既有记录原文" || len(resource.InspectableSurfaces) != 2) {
			t.Fatal("inspection replaced the existing document or manufactured another resource")
		}
	}
	for _, o := range input.CharacterObservations {
		if !domain.HasCharacterSurfaceInspectionPolicyV1(o.Sources) {
			t.Fatal("surface observation omitted its source policy")
		}
	}
	for _, actor := range input.WorldState.Actors {
		if len(actor.OperationalObservations) != 0 {
			t.Fatal("inspectability manufactured a current observation")
		}
	}
	model := &arcRehearsalFakeModel{bodyFactory: surfaceRehearsalBody}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", model)}
	hooks := ProjectedPlanningAccounting{RecordUsage: func(string, agentcore.AgentMessage) {}}
	report, err := RunArcRehearsal(context.Background(), cfg, models, st, input, hooks)
	selectionMust(t, err)
	if !report.ReadyForDetail || model.calls != 2 {
		t.Fatal("typed exterior inspection did not survive actual Architect/Arbiter submission")
	}
	loaded, original, err := store.NewStore(st.Dir()).LoadVerifiedArcRehearsal(report.ReportDigest)
	selectionMust(t, err)
	if !sameCharacterCycleValue(loaded, report) || !sameCharacterCycleValue(original, &input) || len(original.WorldState.Resources) != resourceCount {
		t.Fatal("surface report lost its exact frozen input")
	}
	if !reflect.DeepEqual(before, arcRehearsalSourceFiles(t, st.Dir())) {
		t.Fatal("rehearsal modified mixed-resource foundation or canon")
	}
}

func TestSurfaceRehearsalCapabilitiesRejectWrongTypedClaims(t *testing.T) {
	st, input, _ := surfaceRehearsalFixture(t)
	baseline := surfaceRehearsalBody(input)
	before := arcRehearsalSourceFiles(t, st.Dir())
	for name, change := range map[string]func(*domain.ArcRehearsalBody){
		"missing-surface": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[1].CapabilityRequirements[0].Surface = "" },
		"interior":        func(b *domain.ArcRehearsalBody) { b.MaterialChecks[1].CapabilityRequirements[0].Surface = "contents" },
		"current-intact":  func(b *domain.ArcRehearsalBody) { b.MaterialChecks[1].CapabilityRequirements[0].Surface = "intact" },
		"no-defined-facet": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].ResourceRefs = []string{rehearsalDeviceID}
		},
		"invented-ruler": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].ResourceRefs = []string{"水位尺"}
		},
		"no-resource": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[1].CapabilityRequirements[0].ResourceRefs = nil },
		"multiple-resources": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].ResourceRefs = []string{rehearsalSurfaceResourceID, rehearsalDeviceID}
		},
		"no-mechanism": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[1].CapabilityRequirements[0].MechanismRefs = nil },
		"background-executor": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].ActorRef = "背景船员"
		},
		"new-permission": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].Kind = "departure_permission"
		},
		"surface-as-reading": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[1].RequiresReadable = true },
		"document-via-operational": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].Kind = "operational_observation"
			b.MaterialChecks[1].CapabilityRequirements[0].Surface = ""
		},
		"surface-on-self-work": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[1].CapabilityRequirements[0].Kind = "self_work" },
		"surface-as-quantity": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].Kind = "resource_measurement"
			b.MaterialChecks[1].CapabilityRequirements[0].Surface = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := rehearsalCapabilityClone(t, baseline)
			change(&body)
			raw, err := json.Marshal(body)
			selectionMust(t, err)
			tool := &submitArcRehearsalTool{input: input}
			if _, err := tool.Execute(context.Background(), raw); err == nil || tool.body != nil {
				t.Fatal("false typed surface claim was accepted")
			}
		})
	}
	if !reflect.DeepEqual(before, arcRehearsalSourceFiles(t, st.Dir())) {
		t.Fatal("surface rejection changed source bytes")
	}
	missing := rehearsalCapabilityClone(t, baseline)
	missing.MaterialChecks[1].Status = "missing"
	missing.MaterialChecks[1].ResourceRefs = []string{rehearsalDeviceID}
	missing.MaterialChecks[1].CapabilityRequirements[0].ResourceRefs = []string{rehearsalDeviceID}
	selectionMust(t, domain.ValidateArcRehearsalBody(input, missing))
	call := domain.ArcRehearsalCall{Role: "architect", Provider: "test", Model: "fake", UsageIDs: []string{"a"}, ToolCallID: "a", ResponseDigest: "sha256:" + strings.Repeat("a", 64)}
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: missing, Call: call})
	selectionMust(t, err)
	call.Role, call.ToolCallID, call.UsageIDs = "world_arbiter", "b", []string{"b"}
	report, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: missing, Call: call})
	selectionMust(t, err)
	if report.ReadyForDetail {
		t.Fatal("missing exterior definition authorized detail")
	}
}

func TestSurfaceRehearsalProfilePreservesEveryHistoricalProducer(t *testing.T) {
	st, input, cfg := surfaceRehearsalFixture(t)
	for index, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		selected := cfg
		selected.CharacterAgents.FrozenActivationProducer = producer
		profile, err := ArcRehearsalExecutionCapabilities(selected)
		selectionMust(t, err)
		if index == 0 {
			if profile.Policy != domain.ArcRehearsalCapabilityPolicyV2 || !slices.Contains(profile.ActionKinds, "surface_inspection") {
				t.Fatal("new producer lost its explicit surface capability")
			}
			continue
		}
		old, err := domain.BuildArcRehearsalExecutionCapabilitiesV1(domain.CharacterAgentDecisionProtocolV2Version, domain.CharacterActivationCyclePolicyV3, producer)
		selectionMust(t, err)
		if !reflect.DeepEqual(profile, old) || slices.Contains(profile.ActionKinds, "surface_inspection") {
			t.Fatal("historical producer was silently granted surface capability")
		}
		legacyInput, err := BuildArcRehearsalInput(st, input, selected)
		selectionMust(t, err)
		for _, o := range legacyInput.CharacterObservations {
			if domain.HasCharacterSurfaceInspectionPolicyV1(o.Sources) {
				t.Fatal("legacy producer observation was upgraded")
			}
			for _, view := range o.ResourceViews {
				if len(view.InspectableSurfaces) != 0 {
					t.Fatal("surface metadata leaked into legacy observation")
				}
			}
		}
		if err := domain.ValidateArcRehearsalBody(legacyInput, surfaceRehearsalBody(legacyInput)); err == nil {
			t.Fatal("old profile accepted the new typed inspection")
		}
		body := arcRehearsalTestBody(legacyInput, false)
		body.MaterialChecks[0].CapabilityRequirements[0].Surface = "seal_exterior"
		if err := domain.ValidateArcRehearsalBody(legacyInput, body); err == nil {
			t.Fatal("old profile accepted a new field on an old action")
		}
		model := &arcRehearsalFakeModel{}
		models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", model)}
		if _, err := RunArcRehearsal(context.Background(), selected, models, st, input); err == nil || model.calls != 0 {
			t.Fatal("new frozen profile ran against a historical producer")
		}
	}
}

func TestSurfaceRehearsalLegacyProfileWireAndReportRemainFrozen(t *testing.T) {
	profile, err := domain.BuildArcRehearsalExecutionCapabilitiesV1(domain.CharacterAgentDecisionProtocolV2Version, domain.CharacterActivationCyclePolicyV3, "sha256:"+strings.Repeat("a", 64))
	selectionMust(t, err)
	raw, err := json.Marshal(profile)
	selectionMust(t, err)
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != "5672ddf604428a3150162bf13ec6220d4f79220fc2e7af254b8d41ece02863ac" {
		t.Fatalf("pre-surface capability profile bytes changed: %s", got)
	}
	dependency := domain.ArcRehearsalCapabilityRequirementV1{Key: "read", Kind: "resource_read", ActorRef: "owner", ResourceRefs: []string{rehearsalSurfaceResourceID}}
	raw, err = json.Marshal(dependency)
	selectionMust(t, err)
	if string(raw) != `{"key":"read","kind":"resource_read","actor_ref":"owner","resource_refs":["res_1111111111111111"]}` {
		t.Fatal("new surface field changed an old dependency's wire bytes")
	}
	st, input := arcRehearsalTestInput(t)
	// Historical verification is content-addressed, never reconstructed using
	// today's execution profile or today's schema/prompt digest.
	input.ExecutionCapabilities = &profile
	input.ProtocolDigest = "sha256:" + strings.Repeat("b", 64)
	input, err = domain.FinalizeArcRehearsalInput(input)
	selectionMust(t, err)
	body := arcRehearsalTestBody(input, false)
	call := domain.ArcRehearsalCall{Role: "architect", Provider: "test", Model: "historical", UsageIDs: []string{"a"}, ToolCallID: "a", ResponseDigest: "sha256:" + strings.Repeat("a", 64)}
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: body, Call: call})
	selectionMust(t, err)
	call.Role, call.ToolCallID, call.UsageIDs = "world_arbiter", "b", []string{"b"}
	report, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: body, Call: call})
	selectionMust(t, err)
	selectionMust(t, st.SaveArcRehearsalReport(input, draft, report))
	before, err := store.DirectoryContentRoot(st.Dir())
	selectionMust(t, err)
	loaded, original, err := st.LoadVerifiedArcRehearsal(report.ReportDigest)
	selectionMust(t, err)
	if !sameCharacterCycleValue(loaded, &report) || !sameCharacterCycleValue(original, &input) || original.ExecutionCapabilities.Policy != domain.ArcRehearsalCapabilityPolicyV1 {
		t.Fatal("historical report/input was reinterpreted or upgraded")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	selectionMust(t, err)
	if before != after {
		t.Fatal("historical verification rewrote stored report/input")
	}
}
