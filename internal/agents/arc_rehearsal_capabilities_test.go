package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

const rehearsalFuelID = "res_2222222222222222"
const rehearsalDeviceID = "res_3333333333333333"
const rehearsalPaperID = "res_4444444444444444"

func rehearsalCapabilityFixture(t *testing.T) (*store.Store, domain.ArcRehearsalInput, bootstrap.Config) {
	t.Helper()
	st, binding := arcRehearsalTestInput(t)
	characters, err := st.Characters.Load()
	selectionMust(t, err)
	number := func(n float64) *float64 { return &n }
	characters[0].InitialState.ResourceBalances = append(characters[0].InitialState.ResourceBalances,
		domain.InitialCharacterResourceV2{ResourceID: rehearsalFuelID, Name: "船油", Unit: "升", ActualAmount: number(80), PerceivedName: "船油", PerceivedLabel: "船油", PerceivedUnit: "升", Access: "shared", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}},
		domain.InitialCharacterResourceV2{ResourceID: rehearsalDeviceID, Name: "船用电源", PerceivedName: "电源", PerceivedLabel: "电源", Access: "shared", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}},
		domain.InitialCharacterResourceV2{ResourceID: rehearsalPaperID, Name: "交接纸", Unit: "张", ActualAmount: number(6), PerceivedName: "纸", PerceivedLabel: "纸", PerceivedUnit: "张", Access: "shared", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}},
	)
	characters = append(characters, domain.Character{Name: "乙", Role: "接收员", Tier: "core", InitialState: &domain.CharacterInitialState{Location: "值班室", CurrentGoal: "只签本人所见", Pressure: "有限时间", KnownFacts: []string{"只签本人所见"}}})
	selectionMust(t, st.Characters.Save(characters))
	selectionMust(t, st.SaveWorldCodex(domain.WorldCodex{Mechanisms: []domain.CodexMechanism{{ID: "M_SAIL", Name: "安全检查", Visibility: "formal", CharacterView: &domain.CharacterMechanismView{Name: "安全检查"}}}}))
	cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", ExecutionPolicy: "v3", MaxActivationCycles: 32}}
	input, err := BuildArcRehearsalInput(st, binding, cfg)
	selectionMust(t, err)
	return st, input, cfg
}

func rehearsalCapabilityBody(input domain.ArcRehearsalInput) domain.ArcRehearsalBody {
	b := arcRehearsalTestBody(input, false)
	a, recipient := input.CharacterObservations[0].AgentID, input.CharacterObservations[1].AgentID
	add := func(r domain.ArcRehearsalCapabilityRequirementV1, read bool) {
		b.MaterialChecks = append(b.MaterialChecks, domain.ArcRehearsalMaterialCheck{Operation: r.Key, RequiresReadable: read, Status: "available", Explanation: "仅确认现有协议有入口；后续选择、实际权限、工时、资料和结果仍须真实成立", CapabilityRequirements: []domain.ArcRehearsalCapabilityRequirementV1{r}})
	}
	add(domain.ArcRehearsalCapabilityRequirementV1{Key: "fuel_measure", Kind: "resource_measurement", ActorRef: a, ResourceRefs: []string{rehearsalFuelID}, MechanismRefs: []string{"M_SAIL"}}, false)
	add(domain.ArcRehearsalCapabilityRequirementV1{Key: "power_check", Kind: "operational_observation", ActorRef: a, ResourceRefs: []string{rehearsalDeviceID}, MechanismRefs: []string{"M_SAIL"}}, false)
	add(domain.ArcRehearsalCapabilityRequirementV1{Key: "draft_note", Kind: "artifact_write", ActorRef: a, MaterialInputs: []domain.CharacterWorkMaterialInputV1{{ResourceID: rehearsalPaperID, Amount: 1}}}, false)
	add(domain.ArcRehearsalCapabilityRequirementV1{Key: "correct_note", Kind: "artifact_write", ActorRef: a, ArtifactRef: "draft_note", DependsOn: []string{"draft_note"}}, false)
	add(domain.ArcRehearsalCapabilityRequirementV1{Key: "deliver_note", Kind: "resource_delivery", ActorRef: a, RecipientRef: recipient, ArtifactRef: "correct_note", DependsOn: []string{"correct_note"}}, false)
	add(domain.ArcRehearsalCapabilityRequirementV1{Key: "read_note", Kind: "artifact_read", ActorRef: recipient, ArtifactRef: "correct_note", DependsOn: []string{"correct_note", "deliver_note"}}, true)
	add(domain.ArcRehearsalCapabilityRequirementV1{Key: "sign_note", Kind: "artifact_sign", ActorRef: recipient, ArtifactRef: "correct_note", DependsOn: []string{"correct_note", "deliver_note", "read_note"}}, false)
	return b
}

func rehearsalCapabilityClone(t *testing.T, body domain.ArcRehearsalBody) domain.ArcRehearsalBody {
	t.Helper()
	raw, err := json.Marshal(body)
	selectionMust(t, err)
	var copy domain.ArcRehearsalBody
	selectionMust(t, json.Unmarshal(raw, &copy))
	return copy
}

func TestArcRehearsalCapabilitiesRejectBeforeSubmission(t *testing.T) {
	st, input, _ := rehearsalCapabilityFixture(t)
	baseline := rehearsalCapabilityBody(input)
	before := arcRehearsalSourceFiles(t, st.Dir())
	for name, change := range map[string]func(*domain.ArcRehearsalBody){
		"water-no-resource-or-capability": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1] = domain.ArcRehearsalMaterialCheck{Operation: "水位实测", Status: "available", Explanation: "无resource_id，故采用M_SAIL现场观察"}
		},
		"measurement-no-target": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[1].CapabilityRequirements[0].ResourceRefs = nil },
		"ruler-name-not-resource": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].ResourceRefs = []string{"岸边水位尺"}
		},
		"qualitative-cannot-measure": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].ResourceRefs = []string{rehearsalDeviceID}
		},
		"document-cannot-operational": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[2].CapabilityRequirements[0].ResourceRefs = []string{"res_1111111111111111"}
		},
		"missing-mechanism": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[2].CapabilityRequirements[0].MechanismRefs = nil },
		"background-safety-actor": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[2].CapabilityRequirements[0].ActorRef = "背景当班船员"
		},
		"background-recipient": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[5].CapabilityRequirements[0].RecipientRef = "背景签收岗位"
		},
		"future-id-in-world-refs": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[6].CapabilityRequirements[0].ResourceRefs = []string{"res_9999999999999999"}
		},
		"future-read-before-write": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[3], b.MaterialChecks[6] = b.MaterialChecks[6], b.MaterialChecks[3]
		},
		"cycle": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[3].CapabilityRequirements[0].DependsOn = []string{"sign_note"}
		},
		"unknown-future-version": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[6].CapabilityRequirements[0].ArtifactRef = "not_written"
		},
		"read-without-delivery": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[6].CapabilityRequirements[0].DependsOn = []string{"correct_note"}
		},
		"sign-without-reading": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[7].CapabilityRequirements[0].DependsOn = []string{"correct_note", "deliver_note"}
		},
		"sign-wrong-version": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[7].CapabilityRequirements[0].ArtifactRef = "draft_note"
		},
		"rewrite-other-author": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[4].CapabilityRequirements[0].ActorRef = input.CharacterObservations[1].AgentID
		},
		"rewrite-reallocates": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[4].CapabilityRequirements[0].MaterialInputs = []domain.CharacterWorkMaterialInputV1{{ResourceID: rehearsalPaperID, Amount: 1}}
		},
		"write-without-material": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[3].CapabilityRequirements[0].MaterialInputs = nil },
		"overspend-material": func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[3].CapabilityRequirements[0].MaterialInputs[0].Amount = 7
		},
		"unsupported-as-available":      func(b *domain.ArcRehearsalBody) { b.MaterialChecks[2].CapabilityRequirements[0].Kind = "unsupported" },
		"not-required-hides-dependency": func(b *domain.ArcRehearsalBody) { b.MaterialChecks[2].Status = "not_required" },
		"unresolved-upstream":           func(b *domain.ArcRehearsalBody) { b.MaterialChecks[3].Status = "unclear" },
	} {
		t.Run(name, func(t *testing.T) {
			b := rehearsalCapabilityClone(t, baseline)
			change(&b)
			tool := &submitArcRehearsalTool{input: input}
			raw, err := json.Marshal(b)
			selectionMust(t, err)
			if _, err := tool.Execute(context.Background(), raw); err == nil || tool.body != nil {
				t.Fatal("invalid capability was accepted")
			}
			if !reflect.DeepEqual(before, arcRehearsalSourceFiles(t, st.Dir())) {
				t.Fatal("rejected capability changed source/canon")
			}
			if draft, savedInput, err := st.LoadArcRehearsalDraftForInput(input.InputDigest); err != nil || draft != nil || savedInput != nil {
				t.Fatal("rejection wrote a draft/input")
			}
		})
	}
}

func TestArcRehearsalCapabilitiesRealSubmitStoreRoundTrip(t *testing.T) {
	st, input, _ := rehearsalCapabilityFixture(t)
	body := rehearsalCapabilityBody(input)
	before := arcRehearsalSourceFiles(t, st.Dir())
	raw, err := json.Marshal(body)
	selectionMust(t, err)
	tool := &submitArcRehearsalTool{input: input}
	_, err = tool.Execute(context.Background(), raw)
	selectionMust(t, err)
	call := func(role, id string) domain.ArcRehearsalCall {
		return domain.ArcRehearsalCall{Role: role, Provider: "test", Model: "fake", UsageIDs: []string{id}, ToolCallID: id, ResponseDigest: "sha256:" + strings.Repeat("a", 64)}
	}
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: *tool.body, Call: call("architect", "a")})
	selectionMust(t, err)
	report, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: body, Call: call("world_arbiter", "b")})
	selectionMust(t, err)
	selectionMust(t, st.SaveArcRehearsalReport(input, draft, report))
	loaded, bound, err := store.NewStore(st.Dir()).LoadVerifiedArcRehearsal(report.ReportDigest)
	selectionMust(t, err)
	if loaded == nil || !loaded.ReadyForDetail || bound == nil || !reflect.DeepEqual(bound.ExecutionCapabilities, input.ExecutionCapabilities) {
		t.Fatal("capability-bound report did not round-trip")
	}
	loadedBody, _ := json.Marshal(loaded.Body)
	if !bytes.Equal(raw, loadedBody) {
		t.Fatal("expected artifact dependency graph changed on replay")
	}
	if !reflect.DeepEqual(before, arcRehearsalSourceFiles(t, st.Dir())) {
		t.Fatal("forecast created a real resource, memory or canon delta")
	}
	for _, mutate := range []func(*domain.ArcRehearsalBody){
		func(b *domain.ArcRehearsalBody) { b.MaterialChecks[2].CapabilityRequirements[0].Kind = "self_work" },
		func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks = append(b.MaterialChecks[:2], b.MaterialChecks[3:]...)
		},
		func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[2].Status = "not_required"
			b.MaterialChecks[2].CapabilityRequirements = nil
		},
	} {
		changed := rehearsalCapabilityClone(t, body)
		mutate(&changed)
		if _, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: changed, Call: call("world_arbiter", "c")}); err == nil {
			t.Fatal("review discarded or reclassified an existing execution dependency")
		}
	}
	missing := arcRehearsalTestBody(input, false)
	missing.MaterialChecks = append(missing.MaterialChecks, domain.ArcRehearsalMaterialCheck{Operation: "背景水位检查", Status: "missing", Explanation: "有水位尺文字，但没有执行资源/背景适配器", CapabilityRequirements: []domain.ArcRehearsalCapabilityRequirementV1{{Key: "water", Kind: "unsupported", ActorRef: "背景船员"}}})
	d, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: missing, Call: call("architect", "m1")})
	selectionMust(t, err)
	r, err := domain.FinalizeArcRehearsalReport(input, d, domain.ArcRehearsalReport{Body: missing, Call: call("world_arbiter", "m2")})
	selectionMust(t, err)
	if r.ReadyForDetail {
		t.Fatal("honest capability gap became detail authorization")
	}
}

func TestArcRehearsalCapabilityProfileFrozenBeforeModel(t *testing.T) {
	st, input, cfg := rehearsalCapabilityFixture(t)
	for _, variant := range []bootstrap.Config{
		{},
		{CharacterAgents: bootstrap.CharacterAgentsConfig{ExecutionPolicy: "v4", MaxActivationCycles: 32}},
		{CharacterAgents: bootstrap.CharacterAgentsConfig{ExecutionPolicy: "unknown", MaxActivationCycles: 32}},
		{CharacterAgents: bootstrap.CharacterAgentsConfig{ExecutionPolicy: "v3", MaxActivationCycles: 32, FrozenActivationProducer: "sha256:" + strings.Repeat("f", 64)}},
	} {
		model := &arcRehearsalFakeModel{}
		models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", model)}
		if _, err := RunArcRehearsal(context.Background(), variant, models, st, input); err == nil || model.calls != 0 {
			t.Fatal("configuration drift reached a provider or accepted a different profile")
		}
	}
	for _, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		selected := cfg
		selected.CharacterAgents.FrozenActivationProducer = producer
		profile, err := ArcRehearsalExecutionCapabilities(selected)
		selectionMust(t, err)
		if profile.ProducerDigest != producer || !strings.Contains(strings.Join(profile.ActionKinds, ","), "artifact_sign") {
			t.Fatal("lost an existing frozen producer/artifact capability")
		}
	}
	copy := *input.ExecutionCapabilities
	copy.RecipientKinds = append([]string{"background"}, copy.RecipientKinds...)
	input.ExecutionCapabilities = &copy
	if _, err := domain.FinalizeArcRehearsalInput(input); err == nil {
		t.Fatal("model/caller could add a recipient capability")
	}
}

func TestArcRehearsalCapabilitiesAccountedRunnerAndHistoricalGoldens(t *testing.T) {
	t.Run("real-tool-loop-new-profile", func(t *testing.T) {
		st, input, cfg := rehearsalCapabilityFixture(t)
		before := arcRehearsalSourceFiles(t, st.Dir())
		model := &arcRehearsalFakeModel{bodyFactory: rehearsalCapabilityBody}
		models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", model)}
		var usages []string
		hooks := ProjectedPlanningAccounting{RecordUsage: func(role string, _ agentcore.AgentMessage) { usages = append(usages, role) }}
		report, err := RunArcRehearsal(context.Background(), cfg, models, st, input, hooks)
		selectionMust(t, err)
		if report == nil || !report.ReadyForDetail || model.calls != 2 || len(usages) == 0 {
			t.Fatal("new profile did not use both actual fake-model/tool/accounting stages")
		}
		loaded, bound, err := store.NewStore(st.Dir()).LoadVerifiedArcRehearsal(report.ReportDigest)
		selectionMust(t, err)
		if loaded == nil || bound == nil || bound.InputDigest != input.InputDigest || loaded.ReportDigest != report.ReportDigest {
			t.Fatal("real new-policy runner result did not reload")
		}
		_, err = RunArcRehearsal(context.Background(), cfg, models, st, input, hooks)
		selectionMust(t, err)
		if model.calls != 2 || !reflect.DeepEqual(before, arcRehearsalSourceFiles(t, st.Dir())) {
			t.Fatal("recovery reran models or forecast mutated actual world")
		}
	})
	t.Run("pre-capability-013794e7-goldens", func(t *testing.T) {
		st, input := arcRehearsalTestInput(t)
		input.ExecutionCapabilities = nil
		input.ProtocolDigest = "sha256:877452adbf4647cdb40f9f34d28507d7efe9a0e89f08df8dd7fc6934db4298a1"
		input, err := domain.FinalizeArcRehearsalInput(input)
		selectionMust(t, err)
		call := func(role, id string) domain.ArcRehearsalCall {
			return domain.ArcRehearsalCall{Role: role, Provider: "fixture", Model: "fixture", UsageIDs: []string{id}, ToolCallID: id, ResponseDigest: "sha256:" + strings.Repeat("a", 64)}
		}
		body := arcRehearsalTestBody(input, false)
		draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: body, Call: call("architect", "a")})
		selectionMust(t, err)
		report, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: body, Call: call("world_arbiter", "b")})
		selectionMust(t, err)
		if input.InputDigest != "sha256:bfd8118da3a7e08520507a0d71312fb6c0adc2f886b7b4fdc89c2ac15a7ab53f" || draft.DraftDigest != "sha256:5ca01a525b9ee0407eb44a7ab010bbf29f908a3fb87e0b775b1d2857798dc325" || report.ReportDigest != "sha256:30072244578f3e8d3dcd382edecf529de27c3428141c4107cb0a351e63f486e1" {
			t.Fatalf("historical bytes/digests changed: %s / %s / %s", input.InputDigest, draft.DraftDigest, report.ReportDigest)
		}
		selectionMust(t, st.SaveArcRehearsalReport(input, draft, report))
		loaded, original, err := store.NewStore(st.Dir()).LoadVerifiedArcRehearsal(report.ReportDigest)
		selectionMust(t, err)
		if loaded == nil || original == nil || original.ExecutionCapabilities != nil || loaded.ReportDigest != report.ReportDigest {
			t.Fatal("historical report was migrated during read")
		}
		raw, _ := json.Marshal(original)
		if bytes.Contains(raw, []byte("execution_capabilities")) {
			t.Fatal("optional profile changed legacy serialization")
		}
	})
}
