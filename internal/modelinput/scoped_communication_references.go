package modelinput

import (
	"encoding/json"
	"fmt"
)

const ScopedCommunicationReferenceViewPolicyV1 = "scoped-reference-model-view.communication-addressing.v1"

// A new-producer decorator, not an authority source. Existing codecs keep
// their bytes and field vocabulary; the original exact observation still
// controls which received facts may be used as a reply address.
func WithScopedCommunicationReferencesV1(base *ScopedReferenceCodec) (*ScopedReferenceCodec, error) {
	if base == nil || !base.artifacts {
		return nil, fmt.Errorf("communication reference view requires the existing actor/arbiter artifact codec")
	}
	c := *base
	value, err := referenceJSON(base.body)
	if err != nil {
		return nil, err
	}
	if err := visitCommunicationReplyReferencesV1(value, func(ref string) (string, error) { return base.Alias(ref), nil }); err != nil {
		return nil, err
	}
	c.body, err = json.Marshal(value)
	if err != nil {
		return nil, err
	}
	proof, err := json.Marshal(struct {
		Policy string
		Base   ScopedReferenceBinding
		Body   json.RawMessage
	}{ScopedCommunicationReferenceViewPolicyV1, base.binding, c.body})
	if err != nil {
		return nil, err
	}
	c.binding.Policy = ScopedCommunicationReferenceViewPolicyV1
	c.binding.ViewDigest = scopedReferenceDigest(proof)
	c.communicationAddressing = true
	return &c, nil
}

// communications is a typed array in proposals/tool arguments, never prose.
// Only its new reply field acquires reference semantics; similarly named keys
// in claims or arbitrary nested objects remain literal and fail normal guards.
func visitCommunicationReplyReferencesV1(value any, visit func(string) (string, error)) error {
	root, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	proposal := func(object map[string]any) error {
		child := object["communications"]
		if child == nil {
			return nil
		}
		items, ok := child.([]any)
		if !ok {
			return fmt.Errorf("communication references require a typed array")
		}
		for _, item := range items {
			message, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("communication reference requires an object")
			}
			ref, exists := message["reply_to_received_fact_id"]
			if !exists {
				continue
			}
			text, ok := ref.(string)
			if !ok {
				return fmt.Errorf("reply reference requires a string")
			}
			expanded, err := visit(text)
			if err != nil {
				return err
			}
			message["reply_to_received_fact_id"] = expanded
		}
		return nil
	}
	// A submission is the root proposal. The arbiter has only the explicit
	// top-level proposals array. Never recurse into free-form claims/prose.
	if err := proposal(root); err != nil {
		return err
	}
	if values, ok := root["proposals"].([]any); ok {
		for _, value := range values {
			if object, ok := value.(map[string]any); ok {
				if err := proposal(object); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
