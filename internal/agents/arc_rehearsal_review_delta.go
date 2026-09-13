package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const arcRehearsalReviewDeltaPolicy = "arc-rehearsal-review-delta.v1"
const arcRehearsalReviewDeltaPrompt = `
你是独立复核者，完整 input 和 architect_draft 仍全部可见。按本次 review schema 提交全部评估；material_checks仍是完整、有序的材料检查数组。
每项仍提交operation、requires_readable、resource_refs、status、explanation，只把capability_requirements改为additional_capability_requirements。对于draft已有的同名operation，Host深拷贝并继承该项全部原capability_requirements；你只提交追加的新key，没有新增则[]。不得把任何原key放进additional_capability_requirements，即使原样重复也拒绝；更不能借此改写、删除或重命名原依赖。
可以增加读取要求、资源引用和新的材料操作，并按最终合法依赖顺序排列material_checks。对于新operation，additional_capability_requirements就是其全部依赖。所有原operation仍须出现；原依赖不成立时写missing/unclear及原因，不可通过not_required丢弃所选依赖。
summary、chapters、character_conflicts、contract_checks和unresolved_items仍完整提交。继承不是认可：独立判断可用性。最终完整报告仍运行原能力、覆盖、时序、读取和复核校验，不创造世界事实、角色行动、正文或正史。`

type arcRehearsalMaterialReview struct {
	Operation                        string                                       `json:"operation"`
	RequiresReadable                 bool                                         `json:"requires_readable"`
	ResourceRefs                     []string                                     `json:"resource_refs"`
	Status                           string                                       `json:"status"`
	Explanation                      string                                       `json:"explanation"`
	AdditionalCapabilityRequirements []domain.ArcRehearsalCapabilityRequirementV1 `json:"additional_capability_requirements"`
}

type arcRehearsalReviewDelta struct {
	Summary            string                                 `json:"summary"`
	Chapters           []domain.ArcRehearsalChapter           `json:"chapters"`
	CharacterConflicts []domain.ArcRehearsalCharacterConflict `json:"character_conflicts"`
	ContractChecks     []domain.ArcRehearsalContractCheck     `json:"contract_checks"`
	MaterialChecks     []arcRehearsalMaterialReview           `json:"material_checks"`
	UnresolvedItems    []string                               `json:"unresolved_items"`
}

func arcRehearsalReviewDeltaSchema() map[string]any {
	legacy := (&submitArcRehearsalTool{}).Schema()
	material := legacy["properties"].(map[string]any)["material_checks"].(map[string]any)["items"].(map[string]any)
	properties := material["properties"].(map[string]any)
	additional := properties["capability_requirements"].(map[string]any)
	additional["description"] = "同名原operation只追加全新key；新operation填写全部依赖。Host完整继承原依赖，旧key重复也拒绝；没有新增时[]"
	properties["additional_capability_requirements"] = additional
	delete(properties, "capability_requirements")
	required := material["required"].([]string)
	for i, name := range required {
		if name == "capability_requirements" {
			required[i] = "additional_capability_requirements"
		}
	}
	return legacy
}

func (t *submitArcRehearsalTool) compileReviewDelta(raw json.RawMessage) (domain.ArcRehearsalBody, error) {
	var zero domain.ArcRehearsalBody
	if t.draft == nil {
		return zero, fmt.Errorf("review delta requires its host-bound draft")
	}
	current, err := ArcRehearsalProtocolDigest()
	if err != nil {
		return zero, err
	}
	if t.input.ProtocolDigest != current {
		return zero, fmt.Errorf("review delta does not match its input protocol")
	}
	verified, err := domain.FinalizeArcRehearsalDraft(t.input, *t.draft)
	if err != nil || !sameCharacterCycleValue(verified, *t.draft) {
		return zero, fmt.Errorf("review delta requires its exact verified draft")
	}
	var delta arcRehearsalReviewDelta
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&delta); err != nil {
		return zero, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return zero, fmt.Errorf("review delta must contain one JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return zero, err
	}
	for _, name := range []string{"summary", "chapters", "character_conflicts", "contract_checks", "material_checks", "unresolved_items"} {
		if _, ok := fields[name]; !ok {
			return zero, fmt.Errorf("review delta requires explicit %s", name)
		}
	}
	var reviews []map[string]json.RawMessage
	if err := json.Unmarshal(fields["material_checks"], &reviews); err != nil {
		return zero, err
	}
	for _, review := range reviews {
		for _, name := range []string{"operation", "requires_readable", "resource_refs", "status", "explanation", "additional_capability_requirements"} {
			value, ok := review[name]
			if !ok || (name == "additional_capability_requirements" && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
				return zero, fmt.Errorf("review delta material requires explicit %s", name)
			}
		}
	}
	if len(delta.MaterialChecks) > 64 {
		return zero, fmt.Errorf("review delta exceeds 64 materials")
	}
	// JSON-copy the exact verified host draft before touching any inherited
	// slice. Rejection and caller edits to the result cannot mutate that draft.
	copyRaw, err := json.Marshal(t.draft.Body)
	if err != nil {
		return zero, err
	}
	var inherited domain.ArcRehearsalBody
	if err := json.Unmarshal(copyRaw, &inherited); err != nil {
		return zero, err
	}
	originals := map[string]domain.ArcRehearsalMaterialCheck{}
	keys := map[string]bool{}
	for _, m := range inherited.MaterialChecks {
		originals[m.Operation] = m
		for _, r := range m.CapabilityRequirements {
			keys[r.Key] = true
		}
	}
	body := domain.ArcRehearsalBody{Summary: delta.Summary, Chapters: delta.Chapters, CharacterConflicts: delta.CharacterConflicts, ContractChecks: delta.ContractChecks, UnresolvedItems: delta.UnresolvedItems}
	for _, review := range delta.MaterialChecks {
		requirements := originals[review.Operation].CapabilityRequirements
		if len(requirements)+len(review.AdditionalCapabilityRequirements) > 16 {
			return zero, fmt.Errorf("review delta material %q exceeds 16 capability requirements", review.Operation)
		}
		for _, r := range review.AdditionalCapabilityRequirements {
			if keys[r.Key] {
				return zero, fmt.Errorf("review delta cannot repeat or rewrite existing capability key %q", r.Key)
			}
			keys[r.Key] = true
		}
		requirements = append(requirements, review.AdditionalCapabilityRequirements...)
		body.MaterialChecks = append(body.MaterialChecks, domain.ArcRehearsalMaterialCheck{Operation: review.Operation, RequiresReadable: review.RequiresReadable, ResourceRefs: review.ResourceRefs, Status: review.Status, Explanation: review.Explanation, CapabilityRequirements: requirements})
	}
	merged, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	if len(merged) > 256*1024 {
		return zero, fmt.Errorf("merged rehearsal review exceeds bounded size")
	}
	if err := domain.ValidateArcRehearsalReviewBody(t.input, t.draft.Body, body); err != nil {
		return zero, err
	}
	return body, nil
}
