package domain_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func readinessCodecFixture(t *testing.T) (domain.CharacterReadinessReviewInput, domain.CharacterReadinessVerdict, *domain.CharacterReadinessModelCodecV1) {
	t.Helper()
	_, _, _, input := testutil.CharacterReadiness(t, false)
	verdict := testutil.ReadyVerdict(input)
	codec, err := domain.NewCharacterReadinessModelCodecV1(input)
	if err != nil {
		t.Fatal(err)
	}
	return input, verdict, codec
}

func TestReadinessModelCodecKeepsRequirementsOnceAndPreservesCanonicalFacts(t *testing.T) {
	input, verdict, codec := readinessCodecFixture(t)
	before, _ := json.Marshal(input)
	oldReceipt, err := domain.FinalizeCharacterReadinessReview(input, verdict)
	if err != nil {
		t.Fatal(err)
	}
	view := codec.ModelView()
	if view.ViewPolicy != domain.CharacterReadinessModelViewPolicyV1 || view.SchemaPolicy != domain.CharacterReadinessGroupedSchemaPolicyV1 || len(view.Requirements) != len(input.Requirements) || view.RemainingCycles != input.RemainingCycles {
		t.Fatal("view changed required coverage or its explicit model policy")
	}
	if _, duplicate := view.Context["hard_contracts"]; duplicate {
		t.Fatal("model view repeated the canonical hard-contract array")
	}
	if _, falseDigest := view.Context["digest"]; falseDigest {
		t.Fatal("projected context pretended to retain the original canonical digest")
	}
	raw, _ := json.Marshal(view)
	for _, requirement := range input.Requirements {
		contractJSON, _ := json.Marshal(requirement.Contract)
		if strings.Count(string(raw), `"contract":`+string(contractJSON)) != 1 || strings.Contains(string(raw), requirement.ID) {
			t.Fatal("contract text was dropped/duplicated or original high-entropy ID remained")
		}
	}
	var obligations []map[string]json.RawMessage
	if err := json.Unmarshal(view.Context["obligations"], &obligations); err != nil {
		t.Fatal(err)
	}
	if len(obligations) != 1 || obligations[0]["contract_alias"] == nil || obligations[0]["hardness"] == nil || obligations[0]["due_window"] == nil || obligations[0]["consumer_chapters"] == nil {
		t.Fatal("hard obligation metadata was lost instead of linked to its requirement")
	}
	var cycles []map[string]json.RawMessage
	if err := json.Unmarshal(view.Trace["cycles"], &cycles); err != nil {
		t.Fatal(err)
	}
	var actions []map[string]json.RawMessage
	if err := json.Unmarshal(cycles[0]["actions"], &actions); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"story_time": input.Trace.Cycles[0].StoryTime, "hard_contract_status": input.Trace.Cycles[0].HardContractStatus} {
		encoded, _ := json.Marshal(want)
		if string(cycles[0][key]) != string(encoded) {
			t.Fatalf("actual cycle %s changed", key)
		}
	}
	for key, want := range map[string]string{"decision": input.Trace.Cycles[0].Actions[0].Decision, "intended_action": input.Trace.Cycles[0].Actions[0].IntendedAction, "immediate_result": input.Trace.Cycles[0].Actions[0].ImmediateResult, "state_after": input.Trace.Cycles[0].Actions[0].StateAfter} {
		encoded, _ := json.Marshal(want)
		if string(actions[0][key]) != string(encoded) {
			t.Fatalf("actor %s changed", key)
		}
	}
	for key, want := range map[string]any{"actors": input.Trace.Actors, "world_resources": input.Trace.Resources} {
		encoded, _ := json.Marshal(want)
		if string(view.Trace[key]) != string(encoded) {
			t.Fatalf("actual world %s changed", key)
		}
	}
	grouped, err := codec.EncodeVerdict(verdict)
	if err != nil {
		t.Fatal(err)
	}
	groupedRaw, _ := json.Marshal(grouped)
	for _, forbidden := range []string{"input_digest", "view_digest", "schema_policy", "sha256:", input.Requirements[0].ID} {
		if strings.Contains(string(groupedRaw), forbidden) {
			t.Fatal("model reply must not echo/select Host bindings or long canonical references")
		}
	}
	receipt, err := codec.FinalizeGrouped(codec.Binding(), groupedRaw)
	if err != nil || !reflect.DeepEqual(receipt, oldReceipt) {
		t.Fatalf("grouped reply did not reach original canonical finalizer unchanged: %v", err)
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("codec changed canonical input")
	}
	if err := domain.ValidateCharacterReadinessReviewAudit(domain.CharacterReadinessReviewAudit{Input: input, Receipt: oldReceipt}); err != nil {
		t.Fatalf("old receipt stopped validating: %v", err)
	}
}

func TestReadinessModelCodecBindingAndCopiesCannotReinterpretAliases(t *testing.T) {
	input, verdict, codec := readinessCodecFixture(t)
	grouped, err := codec.EncodeVerdict(verdict)
	if err != nil {
		t.Fatal(err)
	}
	input.RemainingCycles++
	other, err := domain.NewCharacterReadinessModelCodecV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if codec.Binding().InputDigest == other.Binding().InputDigest || codec.Binding().ViewDigest == other.Binding().ViewDigest {
		t.Fatal("input/model-view binding ignored a changed canonical source")
	}
	if _, err := codec.ExpandVerdict(other.Binding(), grouped); err == nil {
		t.Fatal("same short names from another canonical input were accepted")
	}
	for _, change := range []func(*domain.CharacterReadinessModelBindingV1){
		func(b *domain.CharacterReadinessModelBindingV1) { b.InputDigest = other.Binding().InputDigest },
		func(b *domain.CharacterReadinessModelBindingV1) { b.ViewDigest = other.Binding().ViewDigest },
		func(b *domain.CharacterReadinessModelBindingV1) { b.ViewPolicy = "old" },
		func(b *domain.CharacterReadinessModelBindingV1) { b.SchemaPolicy = "old" },
	} {
		binding := codec.Binding()
		change(&binding)
		if _, err := codec.ExpandVerdict(binding, grouped); err == nil {
			t.Fatal("altered private binding was accepted")
		}
	}
	before, _ := json.Marshal(codec.ModelView())
	view := codec.ModelView()
	view.Requirements[0].Contract = "mutated view is not authority"
	view.Trace["actors"][0] = 'x'
	view.Context["pov_character"] = json.RawMessage(`"different"`)
	input.Trace.Cycles[0].Actions[0].Decision = "caller mutation"
	after, _ := json.Marshal(codec.ModelView())
	if string(before) != string(after) {
		t.Fatal("caller-owned input/view mutated the immutable codec")
	}
	if _, err := codec.ExpandVerdict(codec.Binding(), grouped); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessModelCodecRejectsUnknownDuplicateOmittedAndInvalidChecks(t *testing.T) {
	input, verdict, codec := readinessCodecFixture(t)
	original, err := codec.EncodeVerdict(verdict)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"omitted", "nil-groups", "empty-group", "duplicate-within", "duplicate-across", "unknown-contract", "unknown-evidence", "raw-reference", "proposal-only", "invalid-status", "pending-due", "limit-is-hard-conflict"} {
		t.Run(mode, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var grouped domain.CharacterReadinessGroupedVerdictV1
			_ = json.Unmarshal(raw, &grouped)
			switch mode {
			case "omitted":
				grouped.ContractGroups = grouped.ContractGroups[:1]
			case "nil-groups":
				grouped.ContractGroups = nil
			case "empty-group":
				grouped.ContractGroups[0].ContractAliases = nil
			case "duplicate-within":
				grouped.ContractGroups[0].ContractAliases = append(grouped.ContractGroups[0].ContractAliases, grouped.ContractGroups[0].ContractAliases[0])
			case "duplicate-across":
				grouped.ContractGroups = append(grouped.ContractGroups, grouped.ContractGroups[0])
			case "unknown-contract":
				grouped.ContractGroups[0].ContractAliases[0] = "c999"
			case "unknown-evidence":
				grouped.ContractGroups[0].EvidenceRefs = []string{"e999"}
			case "raw-reference":
				grouped.EvidenceRefs = []string{input.Trace.FinalPhysicalRoot}
			case "proposal-only":
				var cycles []struct {
					Actions []struct {
						ProposalRef string `json:"proposal_ref"`
					} `json:"actions"`
				}
				_ = json.Unmarshal(codec.ModelView().Trace["cycles"], &cycles)
				grouped.EvidenceRefs = []string{cycles[0].Actions[0].ProposalRef}
			case "invalid-status":
				grouped.ContractGroups[0].Status = "inherited"
			case "pending-due":
				grouped.ContractGroups[1].Status = "pending"
			case "limit-is-hard-conflict":
				grouped.Decision = "hard_conflict"
			}
			if _, err := codec.ExpandVerdict(codec.Binding(), grouped); err == nil {
				t.Fatal("invalid compact assessment bypassed canonical validation")
			}
		})
	}
	raw, _ := json.Marshal(original)
	for _, invalid := range []string{string(raw) + `{}`, `{"default_status":"pending"}`, strings.TrimSuffix(string(raw), "}") + `,"input_digest":"model-cannot-select-input"}`, strings.Replace(string(raw), `"status":`, `"unknown":true,"status":`, 1)} {
		if _, err := codec.DecodeVerdict(codec.Binding(), json.RawMessage(invalid)); err == nil {
			t.Fatal("unknown field/trailing document bypassed the compact codec")
		}
	}
}

func TestReadinessModelCodecGroupsOnlyCompletelyIdenticalOrderedReferences(t *testing.T) {
	input, verdict, _ := readinessCodecFixture(t)
	// Add a third real hard requirement before creating the codec; this is a
	// new test input, not an edit to any stored receipt or generation.
	input.Context.HardContracts = append(input.Context.HardContracts, "第三项独立约束")
	var err error
	input.Context, err = domain.FinalizeCharacterReadinessContext(input.Context)
	if err != nil {
		t.Fatal(err)
	}
	// Build through the normal context/session constructor for exact binding.
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0, input.Context.Digest)
	session, _ := domain.NewCharacterActivationSession(cycle.GenerationID, 1, input.Context.Digest, *cycle.Evidence.Stimulus.PhysicalState, 0, 4)
	session, err = domain.AppendCharacterActivationCycle(session, cycle)
	if err != nil {
		t.Fatal(err)
	}
	input, err = domain.NewCharacterReadinessReviewInput(input.Context, session, []domain.CharacterActivationCycle{cycle}, input.ReviewProtocol)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := domain.NewCharacterReadinessModelCodecV1(input)
	if err != nil {
		t.Fatal(err)
	}
	verdict = testutil.ReadyVerdict(input)
	left, right := input.Trace.FinalPhysicalRoot, input.Trace.Cycles[0].ArbitrationDigest
	for i := range verdict.ContractChecks {
		verdict.ContractChecks[i].Status = "satisfied"
		verdict.ContractChecks[i].EvidenceRefs = []string{left, right}
	}
	verdict.ContractChecks[1].EvidenceRefs = []string{right, left}
	grouped, err := codec.EncodeVerdict(verdict)
	if err != nil || len(grouped.ContractGroups) != 2 || len(grouped.ContractGroups[0].ContractAliases) != 2 {
		t.Fatalf("evidence order was sorted/unioned or identical checks failed to group: %v", err)
	}
	expanded, err := codec.ExpandVerdict(codec.Binding(), grouped)
	if err != nil || !reflect.DeepEqual(expanded, verdict) {
		t.Fatalf("grouping changed per-contract evidence arrays: %v", err)
	}
	schema := domain.CharacterReadinessGroupedVerdictSchemaV1()
	for _, field := range []string{"input_digest", "view_digest", "schema_policy", "default_status"} {
		if _, exists := schema["properties"].(map[string]any)[field]; exists {
			t.Fatal("schema made Host binding/default status model-authored")
		}
	}
}

func TestReadinessModelCodecKeepsSoftObligationsOptionalAndOriginalConflictRules(t *testing.T) {
	input, _, _ := readinessCodecFixture(t)
	input.Context.Obligations = append(input.Context.Obligations, domain.ProjectedPlanningObligationV2{ID: "soft_hint", Contract: "仅作软方向，不增加硬检查", Hardness: domain.ObligationSoftV2, DueNow: true})
	var err error
	input.Context, err = domain.FinalizeCharacterReadinessContext(input.Context)
	if err != nil {
		t.Fatal(err)
	}
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0, input.Context.Digest)
	session, err := domain.NewCharacterActivationSession(cycle.GenerationID, 1, input.Context.Digest, *cycle.Evidence.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	session, err = domain.AppendCharacterActivationCycle(session, cycle)
	if err != nil {
		t.Fatal(err)
	}
	input, err = domain.NewCharacterReadinessReviewInput(input.Context, session, []domain.CharacterActivationCycle{cycle}, input.ReviewProtocol)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := domain.NewCharacterReadinessModelCodecV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(codec.ModelView().Requirements) != 2 || !bytes.Contains(codec.ModelView().Context["obligations"], []byte("仅作软方向，不增加硬检查")) {
		t.Fatal("soft obligation disappeared or became a required hard check")
	}
	verdict := testutil.ReadyVerdict(input)
	verdict.Decision = "continue"
	for i := range verdict.ContractChecks {
		verdict.ContractChecks[i].Status = "pending"
	}
	grouped, err := codec.EncodeVerdict(verdict)
	if err != nil || len(grouped.ContractGroups) != 1 {
		t.Fatalf("continuing must not demand already-satisfied due work: %v", err)
	}
	if _, err := codec.ExpandVerdict(codec.Binding(), grouped); err != nil {
		t.Fatal(err)
	}
	input.Trace.Cycles[0].HardContractStatus = "infeasible"
	infeasible, err := domain.NewCharacterReadinessModelCodecV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := infeasible.ExpandVerdict(infeasible.Binding(), grouped); err == nil {
		t.Fatal("compact codec overrode an existing infeasible arbitration")
	}
}

// Opt-in audit of existing read-only input/receipt files. No model is called;
// old receipt ordering/digests are left untouched. New compact replies expand
// in requirement order while each original check and ordered refs is identical.
func TestReadinessModelCodecRealInputsReadonly(t *testing.T) {
	dir := os.Getenv("NOVEL_STUDIO_READINESS_CODEC_AUDIT_DIR")
	if dir == "" {
		t.Skip("requires explicit read-only readiness_audits directory")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("readiness audit files unavailable: %v", err)
	}
	var totalInput, totalView, totalVerdict, totalGrouped int
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var audit domain.CharacterReadinessReviewAudit
		if err := json.Unmarshal(raw, &audit); err != nil {
			t.Fatal(err)
		}
		if err := domain.ValidateCharacterReadinessReviewAudit(audit); err != nil {
			t.Fatal(err)
		}
		codec, err := domain.NewCharacterReadinessModelCodecV1(audit.Input)
		if err != nil {
			t.Fatal(err)
		}
		verdict := domain.CharacterReadinessVerdict{Decision: audit.Receipt.Decision, Reason: audit.Receipt.Reason, EvidenceRefs: audit.Receipt.EvidenceRefs, ContractChecks: audit.Receipt.ContractChecks}
		grouped, err := codec.EncodeVerdict(verdict)
		if err != nil {
			t.Fatal(err)
		}
		expanded, err := codec.ExpandVerdict(codec.Binding(), grouped)
		if err != nil {
			t.Fatal(err)
		}
		byID := map[string]domain.CharacterReadinessContractCheck{}
		for _, check := range verdict.ContractChecks {
			byID[check.ContractID] = check
		}
		for i, check := range expanded.ContractChecks {
			if check.ContractID != audit.Input.Requirements[i].ID || !reflect.DeepEqual(check, byID[check.ContractID]) {
				t.Fatal("readiness check values/order of evidence changed")
			}
		}
		if len(expanded.ContractChecks) != len(verdict.ContractChecks) || expanded.Decision != verdict.Decision || expanded.Reason != verdict.Reason || !reflect.DeepEqual(expanded.EvidenceRefs, verdict.EvidenceRefs) {
			t.Fatal("existing verdict semantics were changed")
		}
		compactInput, _ := json.Marshal(audit.Input)
		view, _ := json.Marshal(codec.ModelView())
		oldOutput, _ := json.Marshal(verdict)
		newOutput, _ := json.Marshal(grouped)
		totalInput += utf8.RuneCount(compactInput)
		totalView += utf8.RuneCount(view)
		totalVerdict += utf8.RuneCount(oldOutput)
		totalGrouped += utf8.RuneCount(newOutput)
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, after) {
			t.Fatal("read-only audit changed an existing artifact")
		}
	}
	t.Log(fmt.Sprintf("files=%d input_runes=%d view_runes=%d verdict_runes=%d grouped_runes=%d; all canonical artifacts byte unchanged", len(paths), totalInput, totalView, totalVerdict, totalGrouped))
}
