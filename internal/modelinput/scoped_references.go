package modelinput

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const ScopedReferenceViewPolicy = "scoped-reference-model-view.v1"

// The binding is held by the Host tool, not selected or echoed by the model.
type ScopedReferenceBinding struct {
	Policy       string               `json:"policy"`
	Kind         ExactAgentPacketKind `json:"kind"`
	SourceDigest string               `json:"source_digest"`
	ViewDigest   string               `json:"view_digest"`
}

type ScopedReferenceModelView struct {
	Binding ScopedReferenceBinding `json:"binding"`
	Body    json.RawMessage        `json:"body"`
}

// This codec changes only typed opaque reference values. It is not a privacy
// filter or an authorization source: callers must first validate/project the
// actual character/arbiter input, and validate expanded tool args normally.
type ScopedReferenceCodec struct {
	binding        ScopedReferenceBinding
	body           json.RawMessage
	forward        map[string]string
	reverse        map[string]string
	literals       map[string]bool
	literalHandles map[string]bool
	artifacts      bool
}

var opaqueModelReference = regexp.MustCompile(`^(sha256:[a-f0-9]{64}|(?:src_|self_|oper_|recv_)[a-f0-9]{64}|(?:res_|ca_|fact_|mem_|received_)[a-f0-9]{16,64})$`)

// Do not let an ASCII suffix hide a generated handle inside a new identifier
// or prose (e.g. msg_@ref1_backup). Original literals are reserved beforehand.
var scopedModelHandle = regexp.MustCompile(`@ref[0-9]+`)

func walkReferenceStrings(value any, key string, visit func(string, string) error) error {
	switch value := value.(type) {
	case map[string]any:
		for name, child := range value {
			if err := walkReferenceStrings(child, name, visit); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := walkReferenceStrings(child, key, visit); err != nil {
				return err
			}
		}
	case string:
		return visit(key, value)
	}
	return nil
}

func typedModelReferenceField(key string) bool {
	switch key {
	case "id", "agent_id", "agent_ids", "affected_agent_ids", "from_agent_id", "to_agent_id", "target_agent_id", "resource_id", "resource_ids", "source_id", "communication_id", "conflict_id", "conflict_ids", "mechanism_ref", "mechanism_refs",
		"digest", "source", "sources", "source_digest", "evidence_refs", "knowledge_refs",
		"proposal_digest", "proposal_digests", "source_proposal_digest", "observation_digest", "observation_digests", "stimulus_digest", "activation_digest",
		"source_stimulus_digest", "source_experience_id", "registry_root", "memory_root", "memory_roots",
		"previous_cycle_digest", "context_digest", "source_context_digest", "cycle_digest", "arbitration_digest", "final_physical_root":
		return true
	}
	return false
}

func referenceJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber() // Do not round large integers or reformat physical decimals.
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("reference JSON has trailing data")
	}
	return value, nil
}

func visitTypedReferences(value any, key string, visit func(string) (string, error)) (any, error) {
	switch value := value.(type) {
	case map[string]any:
		for name, child := range value {
			updated, err := visitTypedReferences(child, name, visit)
			if err != nil {
				return nil, err
			}
			value[name] = updated // Object keys and non-reference strings are untouched.
		}
	case []any:
		for i, child := range value {
			updated, err := visitTypedReferences(child, key, visit)
			if err != nil {
				return nil, err
			}
			value[i] = updated
		}
	case string:
		if typedModelReferenceField(key) {
			return visit(value)
		}
	}
	return value, nil
}

func scopedReferenceDigest(raw []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) }

func NewScopedReferenceCodec(kind ExactAgentPacketKind, input any) (*ScopedReferenceCodec, error) {
	return newScopedReferenceCodec(kind, input, false)
}

func newScopedReferenceCodec(kind ExactAgentPacketKind, input any, artifacts bool) (*ScopedReferenceCodec, error) {
	return newScopedReferenceCodecWithOptions(kind, input, scopedReferenceOptions{artifacts: artifacts})
}

type scopedReferenceOptions struct {
	artifacts bool
}

func newScopedReferenceCodecWithOptions(kind ExactAgentPacketKind, input any, options scopedReferenceOptions) (*ScopedReferenceCodec, error) {
	visitReferences := visitTypedReferences
	opaqueReference := opaqueModelReference.MatchString
	policy := ScopedReferenceViewPolicy
	if options.artifacts {
		visitReferences = visitArtifactTypedReferences
		policy = ScopedArtifactReferenceViewPolicyV1
	}
	if kind != KindCharacterObservation && kind != KindWorldArbitration {
		return nil, fmt.Errorf("unsupported scoped reference model view kind")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	body, err := referenceJSON(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := body.(map[string]any); !ok {
		return nil, fmt.Errorf("scoped reference view requires an object input")
	}
	all, opaque := map[string]bool{}, map[string]bool{}
	reserved := map[string]bool{}
	_ = walkReferenceStrings(body, "", func(_ string, text string) error {
		for _, token := range scopedModelHandle.FindAllString(text, -1) {
			reserved[token] = true
		}
		return nil
	})
	if _, err := visitReferences(body, "", func(ref string) (string, error) {
		all[ref] = true
		if opaqueReference(ref) {
			opaque[ref] = true
		}
		return ref, nil
	}); err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(opaque))
	for ref := range opaque {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	codec := &ScopedReferenceCodec{forward: map[string]string{}, reverse: map[string]string{}, literals: all, literalHandles: reserved, artifacts: options.artifacts}
	for index, next := 0, 1; index < len(refs); next++ {
		alias := fmt.Sprintf("@ref%d", next)
		if all[alias] || reserved[alias] {
			continue // An existing literal identifier is never shadowed by an alias.
		}
		codec.forward[refs[index]], codec.reverse[alias] = alias, refs[index]
		index++
	}
	body, err = visitReferences(body, "", func(ref string) (string, error) {
		if alias, ok := codec.forward[ref]; ok {
			return alias, nil
		}
		return ref, nil
	})
	if err != nil {
		return nil, err
	}
	codec.body, err = json.Marshal(body)
	if err != nil {
		return nil, err
	}
	// Bind the exact mapping as well as the view. The mapping remains private.
	proof, err := json.Marshal(struct {
		Policy, Source string
		Kind           ExactAgentPacketKind
		Body           json.RawMessage
		Mapping        map[string]string
	}{policy, scopedReferenceDigest(raw), kind, codec.body, codec.reverse})
	if err != nil {
		return nil, err
	}
	codec.binding = ScopedReferenceBinding{policy, kind, scopedReferenceDigest(raw), scopedReferenceDigest(proof)}
	return codec, nil
}

func (c *ScopedReferenceCodec) Binding() ScopedReferenceBinding { return c.binding }
func (c *ScopedReferenceCodec) ModelView() ScopedReferenceModelView {
	return ScopedReferenceModelView{c.binding, append(json.RawMessage(nil), c.body...)}
}

// Alias is for Host-generated schema hints, never an external lookup API.
func (c *ScopedReferenceCodec) Alias(reference string) string {
	if alias, ok := c.forward[reference]; ok {
		return alias
	}
	return reference
}

func (c *ScopedReferenceCodec) expandTypedReference(reference string) (string, error) {
	if original, ok := c.reverse[reference]; ok {
		return original, nil // A generated handle is legal only as the entire reference.
	}
	if c.literals[reference] {
		return reference, nil // Preserve an exact pre-existing literal identifier.
	}
	if strings.HasPrefix(reference, "@ref") || scopedModelHandle.MatchString(reference) {
		return "", fmt.Errorf("unknown or embedded scoped reference handle")
	}
	// New ordinary IDs still undergo the original domain validation. They may
	// not capture a temporary transport handle in a lasting identifier.
	return reference, nil
}

// ExpandArguments restores typed references only; it never substitutes prose.
// Model-writing callers must first call ValidateModelArguments, as the bound
// tool wrapper does. Read-only metadata/round-trip callers need no prose rewrite.
func (c *ScopedReferenceCodec) ExpandArguments(raw json.RawMessage, binding ScopedReferenceBinding) (json.RawMessage, error) {
	if c == nil || binding != c.binding {
		return nil, fmt.Errorf("scoped reference binding differs from this model input")
	}
	value, err := referenceJSON(raw)
	if err != nil {
		return nil, err
	}
	visitReferences := visitTypedReferences
	if c.artifacts {
		visitReferences = visitArtifactTypedReferences
	}
	value, err = visitReferences(value, "", c.expandTypedReference)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// Generated or unknown transport handles cannot escape into lasting choices, feedback or
// memory prose, where another packet would give them a different meaning. This
// rejects misuse rather than doing an unsafe textual substitution. Original
// literal handle-looking text was reserved before alias assignment.
func (c *ScopedReferenceCodec) ValidateModelArguments(raw json.RawMessage, binding ScopedReferenceBinding) error {
	if c == nil || binding != c.binding {
		return fmt.Errorf("scoped reference binding differs from this model input")
	}
	value, err := referenceJSON(raw)
	if err != nil {
		return err
	}
	if c.artifacts {
		return c.validateArtifactModelArguments(value)
	}
	return walkReferenceStrings(value, "", func(key, text string) error {
		if typedModelReferenceField(key) {
			_, err := c.expandTypedReference(text)
			return err
		}
		for _, handle := range scopedModelHandle.FindAllString(text, -1) {
			if !c.literalHandles[handle] {
				return fmt.Errorf("transport handle in %s prose; use a known natural name or neutral description and put references only in typed reference fields", key)
			}
		}
		return nil
	})
}
