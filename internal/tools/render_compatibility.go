package tools

import (
	"encoding/json"
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/aigc"
)

// applyProseRenderCompatibilityOverlay injects the provider-independent v11
// compatibility contract into a dynamic prose-facing copy. The verified frozen
// envelope and its signed payload are never rewritten.
func applyProseRenderCompatibilityOverlay(raw json.RawMessage) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode frozen render context for prose compatibility: %w", err)
	}
	result := aigc.ApplyProseRenderCompatibilityContracts(payload)
	if !result.Applied() {
		return raw, nil
	}
	overlaid, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s prose compatibility overlay: %w", result.ProtocolVersion, err)
	}
	return overlaid, nil
}
