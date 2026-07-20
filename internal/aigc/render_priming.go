package aigc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ProseRenderPrimingProtocolVersion = "server-owned-frozen-render-context.v1"
	proseRenderPrimingMarker          = "_server_owned_frozen_render_context"
)

// ProseRenderPrimingIdentity binds the server-owned message injected before a
// Drafter provider call to the exact frozen plan and dynamic payload copy.
type ProseRenderPrimingIdentity struct {
	ProtocolVersion string `json:"protocol_version"`
	Chapter         int    `json:"chapter"`
	PlanDigest      string `json:"plan_digest"`
	PayloadSHA256   string `json:"payload_sha256"`
	Policy          string `json:"policy"`
}

type proseRenderPrimingEnvelope struct {
	Identity ProseRenderPrimingIdentity `json:"_server_owned_frozen_render_context"`
	Payload  json.RawMessage            `json:"payload"`
}

// BuildProseRenderPrimingEnvelope creates a provider-valid user message. It is
// JSON rather than a synthetic tool role, so OpenAI-compatible providers do
// not require a fabricated tool_call_id. The wrapper appends this message only
// to a per-call message copy; it never enters persisted conversation history.
func BuildProseRenderPrimingEnvelope(
	chapter int,
	planDigest string,
	payload json.RawMessage,
) (string, ProseRenderPrimingIdentity, error) {
	if chapter <= 0 || strings.TrimSpace(planDigest) == "" || strings.TrimSpace(planDigest) != planDigest {
		return "", ProseRenderPrimingIdentity{}, fmt.Errorf("render priming requires canonical chapter and plan digest")
	}
	canonical, err := canonicalProseRenderPrimingPayload(payload)
	if err != nil {
		return "", ProseRenderPrimingIdentity{}, err
	}
	identity := ProseRenderPrimingIdentity{
		ProtocolVersion: ProseRenderPrimingProtocolVersion,
		Chapter:         chapter,
		PlanDigest:      planDigest,
		PayloadSHA256:   proseRenderPrimingSHA(canonical),
		Policy:          "服务端已在首个正文 token 前加载 exact frozen draft context；先完整消费 payload，再继续原始渲染任务。",
	}
	raw, err := json.Marshal(proseRenderPrimingEnvelope{Identity: identity, Payload: canonical})
	if err != nil {
		return "", ProseRenderPrimingIdentity{}, fmt.Errorf("encode render priming envelope: %w", err)
	}
	return string(raw), identity, nil
}

// ParseProseRenderPrimingEnvelope recognizes and authenticates a server-owned
// priming envelope. recognized=true with err!=nil means the message claimed
// this protocol but was malformed and must be replaced/fail closed.
func ParseProseRenderPrimingEnvelope(
	text string,
) (json.RawMessage, ProseRenderPrimingIdentity, bool, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &probe); err != nil {
		return nil, ProseRenderPrimingIdentity{}, false, nil
	}
	if _, present := probe[proseRenderPrimingMarker]; !present {
		return nil, ProseRenderPrimingIdentity{}, false, nil
	}
	var envelope proseRenderPrimingEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &envelope); err != nil {
		return nil, ProseRenderPrimingIdentity{}, true, fmt.Errorf("decode render priming envelope: %w", err)
	}
	identity := envelope.Identity
	if identity.ProtocolVersion != ProseRenderPrimingProtocolVersion ||
		identity.Chapter <= 0 || strings.TrimSpace(identity.PlanDigest) == "" ||
		strings.TrimSpace(identity.PlanDigest) != identity.PlanDigest ||
		strings.TrimSpace(identity.PayloadSHA256) == "" || strings.TrimSpace(identity.Policy) == "" {
		return nil, identity, true, fmt.Errorf("render priming envelope identity is invalid")
	}
	canonical, err := canonicalProseRenderPrimingPayload(envelope.Payload)
	if err != nil {
		return nil, identity, true, err
	}
	if actual := proseRenderPrimingSHA(canonical); actual != identity.PayloadSHA256 {
		return nil, identity, true, fmt.Errorf(
			"render priming payload drift: expected=%s actual=%s",
			identity.PayloadSHA256,
			actual,
		)
	}
	return canonical, identity, true, nil
}

func canonicalProseRenderPrimingPayload(raw json.RawMessage) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode render priming payload: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("render priming payload must be a JSON object")
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode render priming payload: %w", err)
	}
	return canonical, nil
}

func proseRenderPrimingSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
