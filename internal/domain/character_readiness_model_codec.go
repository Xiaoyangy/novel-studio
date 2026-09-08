package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const CharacterReadinessModelViewPolicyV1 = "readiness-model-view.requirements-once-aliases.v1"
const CharacterReadinessGroupedSchemaPolicyV1 = "readiness-grouped-verdict.status-ordered-evidence.v1"

// Binding is Host-only call/audit metadata. Models do not choose or echo these
// fields. A tool constructor must retain the binding belonging to its immutable
// codec and reject a reply envelope from another input/view.
type CharacterReadinessModelBindingV1 struct {
	ViewPolicy   string `json:"view_policy"`
	SchemaPolicy string `json:"schema_policy"`
	InputDigest  string `json:"input_digest"`
	ViewDigest   string `json:"view_digest"`
}

type CharacterReadinessModelRequirementV1 struct {
	ContractAlias string `json:"contract_alias"`
	Contract      string `json:"contract"`
	DueNow        bool   `json:"due_now"`
}

// Context and Trace retain their original JSON values except for the explicitly
// projected fields: duplicate hard-contract text and citable digest keys. Raw
// maps preserve all other fact, choice, time, resource and obligation fields.
// This is visibly a model view, never a re-signed canonical input.
type CharacterReadinessModelViewV1 struct {
	ViewPolicy      string                                 `json:"view_policy"`
	SchemaPolicy    string                                 `json:"schema_policy"`
	SourcePolicy    string                                 `json:"source_policy"`
	Context         map[string]json.RawMessage             `json:"chapter_context"`
	Trace           map[string]json.RawMessage             `json:"actual_events"`
	Requirements    []CharacterReadinessModelRequirementV1 `json:"required_checks"`
	RemainingCycles int                                    `json:"remaining_cycles"`
}

type CharacterReadinessContractGroupV1 struct {
	Status          string   `json:"status"`
	EvidenceRefs    []string `json:"evidence_refs"`
	ContractAliases []string `json:"contract_aliases"`
}

// Only these fields are model-authored. There is no inherited/default status,
// input selector, protocol selector, or model-authored binding metadata.
type CharacterReadinessGroupedVerdictV1 struct {
	Decision       string                              `json:"decision"`
	Reason         string                              `json:"reason"`
	EvidenceRefs   []string                            `json:"evidence_refs"`
	ContractGroups []CharacterReadinessContractGroupV1 `json:"contract_groups"`
}

type CharacterReadinessModelCodecV1 struct {
	input             CharacterReadinessReviewInput
	view              CharacterReadinessModelViewV1
	binding           CharacterReadinessModelBindingV1
	contractsByAlias  map[string]string
	aliasesByContract map[string]string
	evidenceByAlias   map[string]string
	aliasesByEvidence map[string]string
}

func NewCharacterReadinessModelCodecV1(input CharacterReadinessReviewInput) (*CharacterReadinessModelCodecV1, error) {
	inputDigest, err := CharacterReadinessReviewInputDigest(input)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	c := &CharacterReadinessModelCodecV1{contractsByAlias: map[string]string{}, aliasesByContract: map[string]string{}, evidenceByAlias: map[string]string{}, aliasesByEvidence: map[string]string{}}
	if err := json.Unmarshal(raw, &c.input); err != nil {
		return nil, err
	}
	c.view = CharacterReadinessModelViewV1{ViewPolicy: CharacterReadinessModelViewPolicyV1, SchemaPolicy: CharacterReadinessGroupedSchemaPolicyV1, SourcePolicy: input.Policy, RemainingCycles: input.RemainingCycles, Requirements: []CharacterReadinessModelRequirementV1{}}
	for i, requirement := range c.input.Requirements {
		if requirement.ID == "" || c.aliasesByContract[requirement.ID] != "" {
			return nil, fmt.Errorf("readiness model view requires distinct canonical contract IDs")
		}
		alias := fmt.Sprintf("c%03d", i+1)
		c.contractsByAlias[alias], c.aliasesByContract[requirement.ID] = requirement.ID, alias
		c.view.Requirements = append(c.view.Requirements, CharacterReadinessModelRequirementV1{alias, requirement.Contract, requirement.DueNow})
	}
	contextRaw, _ := json.Marshal(c.input.Context)
	if err := json.Unmarshal(contextRaw, &c.view.Context); err != nil {
		return nil, err
	}
	delete(c.view.Context, "hard_contracts")
	// The original context digest belongs to the canonical input retained by
	// the Host. Do not label this projected context with that old digest.
	delete(c.view.Context, "digest")
	c.view.Context["source_version"], _ = json.Marshal(c.input.Context.Version)
	delete(c.view.Context, "version")
	if raw := c.view.Context["obligations"]; len(raw) > 0 {
		var obligations []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obligations); err != nil {
			return nil, err
		}
		for i, obligation := range c.input.Context.Obligations {
			if obligation.Hardness != ObligationHardV2 {
				continue // Soft obligations are not silently promoted into checks.
			}
			alias := c.aliasesByContract[obligation.ID]
			if alias == "" {
				return nil, fmt.Errorf("hard obligation has no canonical readiness requirement")
			}
			delete(obligations[i], "id")
			delete(obligations[i], "contract")
			obligations[i]["contract_alias"], _ = json.Marshal(alias)
		}
		c.view.Context["obligations"], _ = json.Marshal(obligations)
	}
	traceRaw, _ := json.Marshal(c.input.Trace)
	if err := json.Unmarshal(traceRaw, &c.view.Trace); err != nil {
		return nil, err
	}
	if err := c.projectEvidenceField(c.view.Trace, "final_physical_root", "final_state_ref"); err != nil {
		return nil, err
	}
	var cycles []map[string]json.RawMessage
	if err := json.Unmarshal(c.view.Trace["cycles"], &cycles); err != nil {
		return nil, err
	}
	for _, cycle := range cycles {
		if err := c.projectEvidenceField(cycle, "cycle_digest", "cycle_ref"); err != nil {
			return nil, err
		}
		if err := c.projectEvidenceField(cycle, "arbitration_digest", "arbitration_ref"); err != nil {
			return nil, err
		}
		var actions []map[string]json.RawMessage
		if err := json.Unmarshal(cycle["actions"], &actions); err != nil {
			return nil, err
		}
		for _, action := range actions {
			if err := c.projectEvidenceField(action, "proposal_digest", "proposal_ref"); err != nil {
				return nil, err
			}
		}
		cycle["actions"], _ = json.Marshal(actions)
	}
	c.view.Trace["cycles"], _ = json.Marshal(cycles)
	viewDigest, err := characterAgentDigest(struct {
		InputDigest string
		View        CharacterReadinessModelViewV1
	}{inputDigest, c.view})
	if err != nil {
		return nil, err
	}
	c.binding = CharacterReadinessModelBindingV1{CharacterReadinessModelViewPolicyV1, CharacterReadinessGroupedSchemaPolicyV1, inputDigest, viewDigest}
	return c, nil
}

func (c *CharacterReadinessModelCodecV1) projectEvidenceField(object map[string]json.RawMessage, oldKey, newKey string) error {
	var digest string
	if err := json.Unmarshal(object[oldKey], &digest); err != nil {
		return err
	}
	if err := validatePlanningV2Digest("readiness model evidence", digest); err != nil {
		return err
	}
	alias := c.aliasesByEvidence[digest]
	if alias == "" {
		alias = fmt.Sprintf("e%03d", len(c.aliasesByEvidence)+1)
		c.aliasesByEvidence[digest], c.evidenceByAlias[alias] = alias, digest
	}
	delete(object, oldKey)
	object[newKey], _ = json.Marshal(alias)
	return nil
}

func (c *CharacterReadinessModelCodecV1) Binding() CharacterReadinessModelBindingV1 { return c.binding }

func (c *CharacterReadinessModelCodecV1) ModelView() CharacterReadinessModelViewV1 {
	view := c.view
	clone := func(source map[string]json.RawMessage) map[string]json.RawMessage {
		result := make(map[string]json.RawMessage, len(source))
		for key, raw := range source {
			result[key] = append(json.RawMessage(nil), raw...)
		}
		return result
	}
	view.Context, view.Trace = clone(view.Context), clone(view.Trace)
	view.Requirements = append([]CharacterReadinessModelRequirementV1{}, view.Requirements...)
	return view
}

func (c *CharacterReadinessModelCodecV1) validateBinding(binding CharacterReadinessModelBindingV1) error {
	if c == nil || binding != c.binding {
		return fmt.Errorf("grouped readiness binding differs from its immutable canonical input/model view")
	}
	return nil
}

func (c *CharacterReadinessModelCodecV1) expandRefs(aliases []string) ([]string, error) {
	refs := make([]string, len(aliases))
	for i, alias := range aliases {
		ref, exists := c.evidenceByAlias[alias]
		if !exists {
			return nil, fmt.Errorf("grouped readiness references an unknown evidence alias")
		}
		refs[i] = ref // Order and duplicates are preserved; original validator decides validity.
	}
	return refs, nil
}

// ExpandVerdict emits checks in canonical requirement order. Group ordering
// never changes which requirement is assessed. This new model-view policy does
// not reorder or rewrite any historical receipt stored by the old functions.
func (c *CharacterReadinessModelCodecV1) ExpandVerdict(binding CharacterReadinessModelBindingV1, grouped CharacterReadinessGroupedVerdictV1) (CharacterReadinessVerdict, error) {
	var verdict CharacterReadinessVerdict
	if err := c.validateBinding(binding); err != nil {
		return verdict, err
	}
	if grouped.ContractGroups == nil {
		return verdict, fmt.Errorf("grouped readiness requires an explicit contract_groups array")
	}
	refs, err := c.expandRefs(grouped.EvidenceRefs)
	if err != nil {
		return verdict, err
	}
	verdict = CharacterReadinessVerdict{Decision: grouped.Decision, Reason: grouped.Reason, EvidenceRefs: refs, ContractChecks: []CharacterReadinessContractCheck{}}
	checks := make(map[string]CharacterReadinessContractCheck, len(c.input.Requirements))
	for _, group := range grouped.ContractGroups {
		if len(group.ContractAliases) == 0 {
			return verdict, fmt.Errorf("grouped readiness cannot contain an empty contract group")
		}
		refs, err := c.expandRefs(group.EvidenceRefs)
		if err != nil {
			return verdict, err
		}
		for _, alias := range group.ContractAliases {
			id, exists := c.contractsByAlias[alias]
			if !exists {
				return verdict, fmt.Errorf("grouped readiness references an unknown contract alias")
			}
			if _, duplicate := checks[id]; duplicate {
				return verdict, fmt.Errorf("grouped readiness repeats a contract check")
			}
			checks[id] = CharacterReadinessContractCheck{id, group.Status, append([]string(nil), refs...)}
		}
	}
	if len(checks) != len(c.input.Requirements) {
		return verdict, fmt.Errorf("grouped readiness must explicitly assess every actual requirement; no inherited/default status")
	}
	for _, requirement := range c.input.Requirements {
		verdict.ContractChecks = append(verdict.ContractChecks, checks[requirement.ID])
	}
	if _, err := FinalizeCharacterReadinessReview(c.input, verdict); err != nil {
		return verdict, err
	}
	return verdict, nil
}

func (c *CharacterReadinessModelCodecV1) DecodeVerdict(binding CharacterReadinessModelBindingV1, raw json.RawMessage) (CharacterReadinessVerdict, error) {
	var grouped CharacterReadinessGroupedVerdictV1
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&grouped); err != nil {
		return CharacterReadinessVerdict{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return CharacterReadinessVerdict{}, fmt.Errorf("grouped readiness has trailing data")
	}
	return c.ExpandVerdict(binding, grouped)
}

func (c *CharacterReadinessModelCodecV1) FinalizeGrouped(binding CharacterReadinessModelBindingV1, raw json.RawMessage) (CharacterChapterReadiness, error) {
	verdict, err := c.DecodeVerdict(binding, raw)
	if err != nil {
		return CharacterChapterReadiness{}, err
	}
	return FinalizeCharacterReadinessReview(c.input, verdict)
}

// EncodeVerdict groups only identical status + completely identical ordered
// reference arrays. It never unions different evidence or invents assessments.
func (c *CharacterReadinessModelCodecV1) EncodeVerdict(verdict CharacterReadinessVerdict) (CharacterReadinessGroupedVerdictV1, error) {
	var grouped CharacterReadinessGroupedVerdictV1
	if c == nil {
		return grouped, fmt.Errorf("readiness codec unavailable")
	}
	if _, err := FinalizeCharacterReadinessReview(c.input, verdict); err != nil {
		return grouped, err
	}
	aliases := func(refs []string) []string {
		result := make([]string, len(refs))
		for i, ref := range refs {
			result[i] = c.aliasesByEvidence[ref]
		}
		return result
	}
	grouped = CharacterReadinessGroupedVerdictV1{Decision: verdict.Decision, Reason: verdict.Reason, EvidenceRefs: aliases(verdict.EvidenceRefs), ContractGroups: []CharacterReadinessContractGroupV1{}}
	groups := map[string]int{}
	for _, check := range verdict.ContractChecks {
		key, _ := json.Marshal(struct {
			Status string
			Refs   []string
		}{check.Status, check.EvidenceRefs})
		index, found := groups[string(key)]
		if !found {
			index = len(grouped.ContractGroups)
			groups[string(key)] = index
			grouped.ContractGroups = append(grouped.ContractGroups, CharacterReadinessContractGroupV1{Status: check.Status, EvidenceRefs: aliases(check.EvidenceRefs), ContractAliases: []string{}})
		}
		grouped.ContractGroups[index].ContractAliases = append(grouped.ContractGroups[index].ContractAliases, c.aliasesByContract[check.ContractID])
	}
	return grouped, nil
}

// Static schema: it contains no per-input IDs, model-selectable bindings, or
// implicit old status. Root may hash this schema with the two policy constants
// when opting a new generation into the codec.
func CharacterReadinessGroupedVerdictSchemaV1() map[string]any {
	alias := func(prefix string) map[string]any {
		return map[string]any{"type": "string", "pattern": "^" + prefix + "[0-9]{3,}$"}
	}
	refs := func() map[string]any {
		return map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "items": alias("e"), "description": "本轮视图显示的短证据引用；至少一个实际cycle/arbitration/final_state引用，提案单独不能证明结果；保持引用顺序"}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"decision", "reason", "evidence_refs", "contract_groups"}, "properties": map[string]any{
		"decision":      map[string]any{"type": "string", "enum": []string{"continue", "ready_for_plan", "hard_conflict"}},
		"reason":        map[string]any{"type": "string", "minLength": 1, "maxLength": 1000},
		"evidence_refs": refs(),
		"contract_groups": map[string]any{"type": "array", "description": "每项required_checks必须明确覆盖且仅一次，不继承前轮状态；仅相同status和完全相同有序evidence_refs的合同可合组", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"status", "evidence_refs", "contract_aliases"}, "properties": map[string]any{
			"status":           map[string]any{"type": "string", "enum": []string{"satisfied", "preserved", "pending", "impossible"}},
			"evidence_refs":    refs(),
			"contract_aliases": map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": alias("c")},
		}}},
	}}
}
