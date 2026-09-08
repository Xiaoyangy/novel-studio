package modelinput

import (
	"fmt"
)

const ScopedArtifactReferenceViewPolicyV1 = "scoped-reference-model-view.artifacts.v1"

// Explicitly selected only by the new artifact execution protocol. The old
// constructor and its bytes/binding retain their original field semantics.
func NewScopedArtifactReferenceCodecV1(kind ExactAgentPacketKind, input any) (*ScopedReferenceCodec, error) {
	return newScopedReferenceCodec(kind, input, true)
}

func typedArtifactReferenceField(path []string, key string) bool {
	in := func(name string) bool {
		for _, p := range path {
			if p == name {
				return true
			}
		}
		return false
	}
	// These are locally declared names, including selections of those names
	// within this same execution. Never turn them into global reference aliases.
	if key == "output_key" || (key == "claim_id" && in("output_requests")) || (key == "claim_ids" && in("output_results")) {
		return false
	}
	if typedModelReferenceField(key) {
		return true
	}
	switch key {
	case "artifact_version_digest":
		// A delivery references the existing version authorized by the
		// custodian's artifact_access; it does not declare a new version.
		return len(path) > 0 && path[len(path)-1] == "resource_deliveries"
	case "source_refs", "version_digest", "expected_version_digest", "previous_version_digest", "origin_proposal_digest", "creator_agent_id", "custodian_agent_id", "lineage_roots", "claim_ids":
		return true
	case "claim_id":
		return in("artifact_views") || in("artifact") || in("artifact_knowledge")
	}
	return false
}

func visitArtifactReferenceStrings(value any, path []string, key string, visit func(string, bool, string) (string, error)) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		for name, child := range v {
			updated, err := visitArtifactReferenceStrings(child, append(append([]string(nil), path...), key), name, visit)
			if err != nil {
				return nil, err
			}
			v[name] = updated
		}
	case []any:
		for i, child := range v {
			updated, err := visitArtifactReferenceStrings(child, path, key, visit)
			if err != nil {
				return nil, err
			}
			v[i] = updated
		}
	case string:
		return visit(v, typedArtifactReferenceField(path, key), key)
	}
	return value, nil
}

func visitArtifactTypedReferences(value any, key string, visit func(string) (string, error)) (any, error) {
	return visitArtifactReferenceStrings(value, nil, key, func(text string, typed bool, _ string) (string, error) {
		if typed {
			return visit(text)
		}
		return text, nil
	})
}

func (c *ScopedReferenceCodec) validateArtifactModelArguments(value any) error {
	_, err := visitArtifactReferenceStrings(value, nil, "", func(text string, typed bool, key string) (string, error) {
		if typed {
			_, err := c.expandTypedReference(text)
			return text, err
		}
		for _, handle := range scopedModelHandle.FindAllString(text, -1) {
			if !c.literalHandles[handle] {
				return "", fmt.Errorf("transport handle in %s literal/declaration; put references only in typed reference fields", key)
			}
		}
		return text, nil
	})
	return err
}
