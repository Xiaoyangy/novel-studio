package domain

import (
	"fmt"
	"strings"
)

const maxReceivedFactTransitionIssuesV2 = 8
const maxReceivedFactTransitionErrorBytesV2 = 2048

// Diagnostic-only collection inside the original transition validator. It
// cannot authorize a fact, repair a receipt, or become an alternative precheck.
type receivedFactTransitionErrorsV2 struct {
	prefix    string
	issues    []string
	bytes     int
	truncated bool
}

func (e *receivedFactTransitionErrorsV2) Error() string {
	text := e.prefix + "; received_facts checks (materialized post_state): " + strings.Join(e.issues, "; ")
	if e.truncated {
		text += "; further received_facts issues omitted"
	}
	return text
}

func (e *receivedFactTransitionErrorsV2) add(prefix, path, code, sourceID, relatedPath string) {
	if e.prefix == "" {
		e.prefix = prefix
		e.bytes = len(prefix)
	}
	if e.truncated {
		return
	}
	// Only bounded identifiers and host-built field paths are exposed. Never
	// echo a communication/document body, receipt arguments or world secrets.
	message := fmt.Sprintf("path=%s code=%s source_id=%s", path, code, arbitrationReferenceLabel(sourceID))
	if relatedPath != "" {
		message += " related_path=" + relatedPath
	}
	// Reserve fixed prefix, separators and the explicit omission marker.
	if len(e.issues) >= maxReceivedFactTransitionIssuesV2 || e.bytes+len(message)+2 > maxReceivedFactTransitionErrorBytesV2-128 {
		e.truncated = true
		return
	}
	e.issues = append(e.issues, message)
	e.bytes += len(message) + 2
}

// Preserve the established error prefix for normal IDs, without turning an
// arbitrary model-provided source_id into an unbounded/private-text echo.
func receivedFactDiagnosticIDV2(id string) string {
	label := arbitrationReferenceLabel(id)
	if strings.HasPrefix(label, `"`) {
		return id
	}
	return "<non-identifier>"
}

func receivedFactPathV2(resolutionIndex map[string]int, agentID string, actorIndex, factIndex int) string {
	// The kernel sees normalized complete post_state, not the tool's sparse
	// additions array. Error text labels that scope; source_id locates the
	// corresponding submitted claim without pretending these are raw-arg indices.
	base := fmt.Sprintf("/physical_state/actors/%d", actorIndex)
	if index, exists := resolutionIndex[agentID]; exists {
		base = fmt.Sprintf("/resolutions/%d/post_state", index)
	}
	path := base + "/received_facts"
	if factIndex >= 0 {
		path += fmt.Sprintf("/%d", factIndex)
	}
	return path
}

func receivedFactResolutionPathV2(resolutionIndex map[string]int, agentID, field string) string {
	if index, exists := resolutionIndex[agentID]; exists {
		return fmt.Sprintf("/resolutions/%d/%s", index, field)
	}
	return ""
}
