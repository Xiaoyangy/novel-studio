package modelinput

import (
	"encoding/json"
	"fmt"
	"strings"
)

const ScopedLocationMetadataViewPolicyV1 = "scoped-reference-model-view.location-metadata.v1"

type scopedLocationMetadataV1 struct {
	reverse      map[string]string
	currentAlias string
}

// NewScopedLocationMetadataCodecV1 is selected only by a Host-owned actor
// policy. Canonical locations remain intact; only four actor-view paths are
// hidden. Authored facts, claims and other natural language are never rewritten.
func NewScopedLocationMetadataCodecV1(kind ExactAgentPacketKind, input any) (*ScopedReferenceCodec, error) {
	if kind != KindCharacterObservation {
		return nil, fmt.Errorf("location metadata view requires a character observation")
	}
	codec, err := NewScopedArtifactReferenceCodecV1(kind, input)
	if err != nil {
		return nil, err
	}
	body, err := referenceJSON(codec.body)
	if err != nil {
		return nil, err
	}
	currentLocation, ok := body.(map[string]any)["location"].(string)
	if !ok || strings.TrimSpace(currentLocation) == "" {
		return nil, fmt.Errorf("location metadata view requires its exact current location")
	}
	fields, err := locationMetadataFieldsV1(body.(map[string]any))
	if err != nil {
		return nil, err
	}
	locations := map[string]bool{}
	var names []string
	for _, object := range fields {
		location, ok := object["location"].(string)
		if !ok {
			return nil, fmt.Errorf("location metadata requires string location fields")
		}
		if location != "" && !locations[location] {
			locations[location] = true
			names = append(names, location)
		}
	}
	// Assign from the fixed path traversal, never the hidden names' lexical
	// order. The current origin is always first; repeats reuse its identity.
	forward, reverse := map[string]string{}, map[string]string{}
	for index, name := range names {
		alias := fmt.Sprintf("@loc_%s_%d", strings.TrimPrefix(codec.binding.SourceDigest, "sha256:"), index+1)
		forward[name], reverse[alias] = alias, name
	}
	for _, object := range fields {
		if alias, ok := forward[object["location"].(string)]; ok {
			object["location"] = alias
		}
	}
	codec.body, err = json.Marshal(body)
	if err != nil {
		return nil, err
	}
	proof, err := json.Marshal(struct {
		Policy  string
		Source  ScopedReferenceBinding
		Body    json.RawMessage
		Mapping map[string]string
	}{ScopedLocationMetadataViewPolicyV1, codec.binding, codec.body, reverse})
	if err != nil {
		return nil, err
	}
	codec.binding.Policy = ScopedLocationMetadataViewPolicyV1
	codec.binding.ViewDigest = scopedReferenceDigest(proof)
	codec.locationMetadata = &scopedLocationMetadataV1{reverse: reverse, currentAlias: forward[currentLocation]}
	return codec, nil
}

// Explicit structural traversal prevents a similarly named nested field or
// authored claim from becoming a new location-metadata projection path.
func locationMetadataFieldsV1(body map[string]any) ([]map[string]any, error) {
	var fields []map[string]any
	add := func(object map[string]any) {
		if _, exists := object["location"]; exists {
			fields = append(fields, object)
		}
	}
	add(body)
	for _, path := range []struct{ array, nested string }{
		{"self_experiences", ""}, {"resource_views", "known_placement"}, {"artifact_views", "placement"},
	} {
		value := body[path.array]
		if value == nil {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("location metadata path requires an array")
		}
		for _, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("location metadata path requires an object")
			}
			if path.nested != "" {
				if object[path.nested] == nil {
					continue
				}
				object, ok = object[path.nested].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("location metadata placement requires an object")
				}
			}
			add(object)
		}
	}
	return fields, nil
}

func (c *ScopedReferenceCodec) validateLocationMetadataArgumentsV1(value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("location metadata arguments require an object")
	}
	location, ok := object["location"].(string)
	if !ok || location == "" || location != c.locationMetadata.currentAlias {
		return fmt.Errorf("location metadata argument must use this observation's exact current location handle")
	}
	for key, child := range object {
		if strings.Contains(key, "@loc_") {
			return fmt.Errorf("location metadata handle is not a field name")
		}
		if key == "location" {
			continue
		}
		if err := rejectLocationMetadataHandleV1(child); err != nil {
			return err
		}
	}
	return nil
}

func rejectLocationMetadataHandleV1(value any) error {
	switch value := value.(type) {
	case string:
		if strings.Contains(value, "@loc_") {
			return fmt.Errorf("unknown, embedded or misplaced location metadata handle; use it only as the complete root location value")
		}
	case []any:
		for _, child := range value {
			if err := rejectLocationMetadataHandleV1(child); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, child := range value {
			if err := rejectLocationMetadataHandleV1(key); err != nil {
				return err
			}
			if err := rejectLocationMetadataHandleV1(child); err != nil {
				return err
			}
		}
	}
	return nil
}
