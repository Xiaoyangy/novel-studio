package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func rehearsalReviewDeltaForTest(body domain.ArcRehearsalBody) arcRehearsalReviewDelta {
	d := arcRehearsalReviewDelta{Summary: body.Summary, Chapters: body.Chapters, CharacterConflicts: body.CharacterConflicts, ContractChecks: body.ContractChecks, UnresolvedItems: body.UnresolvedItems}
	for _, m := range body.MaterialChecks {
		d.MaterialChecks = append(d.MaterialChecks, arcRehearsalMaterialReview{Operation: m.Operation, RequiresReadable: m.RequiresReadable, ResourceRefs: m.ResourceRefs, Status: m.Status, Explanation: m.Explanation, AdditionalCapabilityRequirements: []domain.ArcRehearsalCapabilityRequirementV1{}})
	}
	return d
}

// The fake selects the wire actually advertised by the tool. Deliberately
// changed old dependencies stay a forbidden full-body overwrite attempt;
// they are never silently normalized into an innocent inherited delta.
func rehearsalFakeWireForSchema(body domain.ArcRehearsalBody, draft *domain.ArcRehearsalDraft, spec agentcore.ToolSpec) ([]byte, error) {
	parameters, ok := spec.Parameters.(map[string]any)
	if !ok {
		return json.Marshal(body)
	}
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		return json.Marshal(body)
	}
	material, ok := properties["material_checks"].(map[string]any)
	if !ok {
		return json.Marshal(body)
	}
	items, ok := material["items"].(map[string]any)
	if !ok {
		return json.Marshal(body)
	}
	props, ok := items["properties"].(map[string]any)
	if !ok || props["additional_capability_requirements"] == nil {
		return json.Marshal(body)
	}
	if draft == nil {
		return nil, fmt.Errorf("new review schema lacks host draft")
	}
	delta := rehearsalReviewDeltaForTest(body)
	originals := map[string]domain.ArcRehearsalMaterialCheck{}
	for _, m := range draft.Body.MaterialChecks {
		originals[m.Operation] = m
	}
	for i, m := range body.MaterialChecks {
		prior, exists := originals[m.Operation]
		if !exists {
			delta.MaterialChecks[i].AdditionalCapabilityRequirements = m.CapabilityRequirements
			continue
		}
		keys := map[string]bool{}
		for _, r := range prior.CapabilityRequirements {
			found := false
			for _, current := range m.CapabilityRequirements {
				if current.Key == r.Key && sameCharacterCycleValue(current, r) {
					found = true
					break
				}
			}
			if !found {
				return json.Marshal(body)
			}
			keys[r.Key] = true
		}
		for _, r := range m.CapabilityRequirements {
			if !keys[r.Key] {
				delta.MaterialChecks[i].AdditionalCapabilityRequirements = append(delta.MaterialChecks[i].AdditionalCapabilityRequirements, r)
			}
		}
	}
	return json.Marshal(delta)
}

func rehearsalDeltaFixture(t *testing.T) (*store.Store, domain.ArcRehearsalInput, bootstrap.Config, domain.ArcRehearsalDraft) {
	t.Helper()
	st, input, cfg := rehearsalCapabilityFixture(t)
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: rehearsalCapabilityBody(input), Call: rehearsalReviewSubmitDraft(t, input).Call})
	selectionMust(t, err)
	return st, input, cfg, draft
}

func TestArcRehearsalReviewDeltaInheritsAndAddsWithoutMutatingDraft(t *testing.T) {
	st, input, _, draft := rehearsalDeltaFixture(t)
	before, err := json.Marshal(draft)
	selectionMust(t, err)
	delta := rehearsalReviewDeltaForTest(draft.Body)
	actor := input.CharacterObservations[0].AgentID
	addition := domain.ArcRehearsalCapabilityRequirementV1{Key: "extra_ledger_use", Kind: "resource_use", ActorRef: actor, ResourceRefs: []string{"res_1111111111111111"}}
	delta.MaterialChecks[0].AdditionalCapabilityRequirements = []domain.ArcRehearsalCapabilityRequirementV1{addition}
	// Original false -> true and extra resource coverage remain expressible.
	read := domain.ArcRehearsalCapabilityRequirementV1{Key: "extra_read", Kind: "resource_read", ActorRef: actor, ResourceRefs: []string{"res_1111111111111111"}}
	delta.MaterialChecks[1].RequiresReadable = true
	delta.MaterialChecks[1].ResourceRefs = []string{"res_1111111111111111"}
	delta.MaterialChecks[1].AdditionalCapabilityRequirements = []domain.ArcRehearsalCapabilityRequirementV1{read}
	inserted := arcRehearsalMaterialReview{Operation: "inserted_local_work", Status: "available", Explanation: "在合法依赖之后插入，不限末尾", AdditionalCapabilityRequirements: []domain.ArcRehearsalCapabilityRequirementV1{{Key: "extra_self_work", Kind: "self_work", ActorRef: actor, DependsOn: []string{draft.Body.MaterialChecks[0].CapabilityRequirements[0].Key}}}}
	delta.MaterialChecks = append(delta.MaterialChecks[:1], append([]arcRehearsalMaterialReview{inserted}, delta.MaterialChecks[1:]...)...)
	raw, err := json.Marshal(delta)
	selectionMust(t, err)
	tool := &submitArcRehearsalTool{input: input, draft: &draft, deltaReview: true}
	_, err = tool.Execute(context.Background(), raw)
	selectionMust(t, err)
	want := rehearsalCapabilityClone(t, draft.Body)
	want.MaterialChecks[0].CapabilityRequirements = append(want.MaterialChecks[0].CapabilityRequirements, addition)
	want.MaterialChecks[1].RequiresReadable = true
	want.MaterialChecks[1].ResourceRefs = []string{"res_1111111111111111"}
	want.MaterialChecks[1].CapabilityRequirements = append(want.MaterialChecks[1].CapabilityRequirements, read)
	newMaterial := domain.ArcRehearsalMaterialCheck{Operation: inserted.Operation, Status: inserted.Status, Explanation: inserted.Explanation, CapabilityRequirements: inserted.AdditionalCapabilityRequirements}
	want.MaterialChecks = append(want.MaterialChecks[:1], append([]domain.ArcRehearsalMaterialCheck{newMaterial}, want.MaterialChecks[1:]...)...)
	actual, err := json.Marshal(tool.body)
	selectionMust(t, err)
	expected, err := json.Marshal(want)
	selectionMust(t, err)
	if !bytes.Equal(actual, expected) {
		t.Fatal("Host inheritance changed fields, order, or legal read upgrades")
	}
	selectionMust(t, domain.ValidateArcRehearsalReviewBody(input, draft.Body, *tool.body))
	call := domain.ArcRehearsalCall{Role: "world_arbiter", Provider: "fixture", Model: "fixture", UsageIDs: []string{"delta-review"}, ToolCallID: "delta-submit", ResponseDigest: "sha256:" + strings.Repeat("b", 64)}
	report, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: *tool.body, Call: call})
	selectionMust(t, err)
	selectionMust(t, st.SaveArcRehearsalReport(input, draft, report))
	loaded, _, err := store.NewStore(st.Dir()).LoadVerifiedArcRehearsal(report.ReportDigest)
	selectionMust(t, err)
	if loaded == nil || loaded.DraftDigest != draft.DraftDigest || !reflect.DeepEqual(loaded.Body, *tool.body) {
		t.Fatal("delta report lost original draft/receipt binding on reload")
	}
	tool.body.MaterialChecks[0].CapabilityRequirements[0].ResourceRefs[0] = "mutated-after-accept"
	after, _ := json.Marshal(draft)
	if !bytes.Equal(before, after) {
		t.Fatal("compiled result aliases host draft")
	}
}

func TestArcRehearsalReviewDeltaRejectsOverwriteOmissionAndMergedLimits(t *testing.T) {
	_, input, _, draft := rehearsalDeltaFixture(t)
	original, _ := json.Marshal(draft)
	for _, tc := range []struct {
		name   string
		mutate func(*arcRehearsalReviewDelta)
	}{
		{"same-key-change", func(d *arcRehearsalReviewDelta) {
			r := draft.Body.MaterialChecks[0].CapabilityRequirements[0]
			r.Kind = "self_work"
			d.MaterialChecks[0].AdditionalCapabilityRequirements = []domain.ArcRehearsalCapabilityRequirementV1{r}
		}},
		{"same-key-identical", func(d *arcRehearsalReviewDelta) {
			d.MaterialChecks[0].AdditionalCapabilityRequirements = draft.Body.MaterialChecks[0].CapabilityRequirements
		}},
		{"omit-material", func(d *arcRehearsalReviewDelta) { d.MaterialChecks = d.MaterialChecks[1:] }},
		{"duplicate-material", func(d *arcRehearsalReviewDelta) { d.MaterialChecks[1].Operation = d.MaterialChecks[0].Operation }},
		{"rename-material", func(d *arcRehearsalReviewDelta) { d.MaterialChecks[0].Operation = "renamed_original" }},
		{"downgrade-material", func(d *arcRehearsalReviewDelta) { d.MaterialChecks[0].Status = "not_required" }},
		{"downgrade-readable", func(d *arcRehearsalReviewDelta) { d.MaterialChecks[0].RequiresReadable = false }},
		{"future-dependency", func(d *arcRehearsalReviewDelta) {
			d.MaterialChecks[0].AdditionalCapabilityRequirements = []domain.ArcRehearsalCapabilityRequirementV1{{Key: "too_early", Kind: "self_work", ActorRef: input.CharacterObservations[0].AgentID, DependsOn: []string{"sign_note"}}}
		}},
		{"merged-capability-count", func(d *arcRehearsalReviewDelta) {
			for i := 0; i < 16; i++ {
				d.MaterialChecks[0].AdditionalCapabilityRequirements = append(d.MaterialChecks[0].AdditionalCapabilityRequirements, domain.ArcRehearsalCapabilityRequirementV1{Key: fmt.Sprintf("add_%d", i), Kind: "self_work", ActorRef: input.CharacterObservations[0].AgentID})
			}
		}},
		{"merged-material-count", func(d *arcRehearsalReviewDelta) {
			for i := 0; i < 64; i++ {
				d.MaterialChecks = append(d.MaterialChecks, arcRehearsalMaterialReview{Operation: fmt.Sprint(i), AdditionalCapabilityRequirements: []domain.ArcRehearsalCapabilityRequirementV1{}})
			}
		}},
		{"merged-global-capability-count", func(d *arcRehearsalReviewDelta) {
			for i := 0; i < 41; i++ {
				m := arcRehearsalMaterialReview{Operation: fmt.Sprintf("extra_material_%d", i), Status: "available", Explanation: "bounded additions"}
				for j := 0; j < 3; j++ {
					m.AdditionalCapabilityRequirements = append(m.AdditionalCapabilityRequirements, domain.ArcRehearsalCapabilityRequirementV1{Key: fmt.Sprintf("extra_%d_%d", i, j), Kind: "self_work", ActorRef: input.CharacterObservations[0].AgentID})
				}
				d.MaterialChecks = append(d.MaterialChecks, m)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := rehearsalReviewDeltaForTest(draft.Body)
			tc.mutate(&d)
			raw, err := json.Marshal(d)
			selectionMust(t, err)
			tool := &submitArcRehearsalTool{input: input, draft: &draft, deltaReview: true}
			if _, err := tool.Execute(t.Context(), raw); err == nil || tool.body != nil {
				t.Fatal("invalid delta accepted or retained")
			}
			after, _ := json.Marshal(draft)
			if !bytes.Equal(original, after) {
				t.Fatal("rejection mutated draft")
			}
		})
	}
	valid, _ := json.Marshal(rehearsalReviewDeltaForTest(draft.Body))
	for _, raw := range [][]byte{append(valid, []byte(` {}`)...), bytes.ReplaceAll(valid, []byte(`"additional_capability_requirements"`), []byte(`"capability_requirements"`)), bytes.Replace(valid, []byte(`"status"`), []byte(`"unknown_status"`), 1)} {
		tool := &submitArcRehearsalTool{input: input, draft: &draft, deltaReview: true}
		if _, err := tool.Execute(t.Context(), raw); err == nil || tool.body != nil {
			t.Fatal("unknown legacy/new fields or trailing JSON accepted")
		}
	}
}

func TestArcRehearsalReviewDeltaProtocolAndLegacyResume(t *testing.T) {
	legacy, err := LegacyArcRehearsalProtocolDigest()
	selectionMust(t, err)
	if legacy != "sha256:772e4669ca1b47e3931b333fdd4f8920c7d6a907f5bd25f6696f985492ffa3ee" {
		t.Fatalf("legacy 6756012 schema/prompt/protocol changed: %s", legacy)
	}
	current, err := ArcRehearsalProtocolDigest()
	selectionMust(t, err)
	if current == legacy {
		t.Fatal("new review schema/prompt did not change input protocol")
	}
	legacySchema, _ := json.Marshal((&submitArcRehearsalTool{}).Schema())
	newSchema, _ := json.Marshal((&submitArcRehearsalTool{deltaReview: true}).Schema())
	if bytes.Contains(legacySchema, []byte("additional_capability_requirements")) || !bytes.Contains(newSchema, []byte("additional_capability_requirements")) || bytes.Contains(newSchema, []byte(`"capability_requirements":`)) {
		t.Fatal("new and legacy submission contracts mixed")
	}
	for _, oldMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", oldMode), func(t *testing.T) {
			st, input := arcRehearsalTestInput(t)
			if oldMode {
				input.ProtocolDigest = legacy
				input, err = domain.FinalizeArcRehearsalInput(input)
				selectionMust(t, err)
			}
			hooks := ProjectedPlanningAccounting{RecordUsage: func(string, agentcore.AgentMessage) {}}
			first := &arcRehearsalFakeModel{failReview: true}
			models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", first)}
			if _, err := RunArcRehearsal(t.Context(), bootstrap.Config{}, models, st, input, hooks); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected saved-draft interruption: %v", err)
			}
			draft, bound, err := st.LoadArcRehearsalDraftForInput(input.InputDigest)
			selectionMust(t, err)
			if draft == nil || bound == nil || first.calls != 2 {
				t.Fatal("first stage did not preserve its exact draft")
			}
			original, _ := json.Marshal(draft)
			second := &arcRehearsalFakeModel{}
			models = &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "fake", second)}
			report, err := RunArcRehearsal(t.Context(), bootstrap.Config{}, models, store.NewStore(st.Dir()), input, hooks)
			selectionMust(t, err)
			if report == nil || report.DraftDigest != draft.DraftDigest || second.calls != 1 || !reflect.DeepEqual(second.roles, []string{"world_arbiter"}) {
				t.Fatal("resume reran Architect, changed old protocol, or lost draft binding")
			}
			after, _, err := store.NewStore(st.Dir()).LoadArcRehearsalDraftForInput(input.InputDigest)
			selectionMust(t, err)
			raw, _ := json.Marshal(after)
			if !bytes.Equal(original, raw) {
				t.Fatal("review transport rewrote original draft bytes")
			}
		})
	}
}

func TestArcRehearsalReviewDeltaChecksMergedSizeAndDraftIntegrity(t *testing.T) {
	_, input, _, draft := rehearsalDeltaFixture(t)
	large := rehearsalCapabilityClone(t, draft.Body)
	for i := range large.MaterialChecks {
		for len(large.MaterialChecks[i].CapabilityRequirements) < 16 {
			n := len(large.MaterialChecks[i].CapabilityRequirements)
			large.MaterialChecks[i].CapabilityRequirements = append(large.MaterialChecks[i].CapabilityRequirements, domain.ArcRehearsalCapabilityRequirementV1{Key: fmt.Sprintf("filled_%s_%d_%d", strings.Repeat("k", 100), i, n), Kind: "self_work", ActorRef: input.CharacterObservations[0].AgentID})
		}
	}
	largeDraft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: large, Call: draft.Call})
	selectionMust(t, err)
	delta := rehearsalReviewDeltaForTest(large)
	delta.Summary = strings.Repeat("x", 245000)
	raw, err := json.Marshal(delta)
	selectionMust(t, err)
	if len(raw) >= 256*1024 {
		t.Fatal("fixture must fit wire but exceed merged body")
	}
	tool := &submitArcRehearsalTool{input: input, draft: &largeDraft, deltaReview: true}
	if _, err := tool.Execute(t.Context(), raw); err == nil || !strings.Contains(err.Error(), "merged rehearsal review exceeds") || tool.body != nil {
		t.Fatalf("merged limit bypassed: %v", err)
	}
	valid, _ := json.Marshal(rehearsalReviewDeltaForTest(draft.Body))
	for _, kind := range []string{"digest", "input", "call", "protocol"} {
		t.Run(kind, func(t *testing.T) {
			copy := draft
			bound := input
			switch kind {
			case "digest":
				copy.DraftDigest = "sha256:" + strings.Repeat("f", 64)
			case "input":
				copy.InputDigest = "sha256:" + strings.Repeat("f", 64)
			case "call":
				copy.Call.Role = "world_arbiter"
			case "protocol":
				bound.ProtocolDigest, _ = LegacyArcRehearsalProtocolDigest()
			}
			tool := &submitArcRehearsalTool{input: bound, draft: &copy, deltaReview: true}
			if _, err := tool.Execute(t.Context(), valid); err == nil || tool.body != nil {
				t.Fatal("unverified/legacy draft granted new transport")
			}
		})
	}
}
