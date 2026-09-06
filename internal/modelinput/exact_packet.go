// Package modelinput describes host-owned transport requirements. It does not
// give source data instruction authority or expose a model-callable capability.
package modelinput

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/voocel/agentcore"
)

const ExactAgentPacketPolicy = "exact-agent-packet.transport.v1"
const ExactAgentPacketMetadataKey = "novel_exact_agent_packet"

type ExactAgentPacketKind string

const (
	KindCharacterObservation ExactAgentPacketKind = "character_observation"
	KindWorldArbitration     ExactAgentPacketKind = "world_arbitration"
	KindPlannerContext       ExactAgentPacketKind = "planner_context"
	KindChapterReadiness     ExactAgentPacketKind = "chapter_readiness"
	KindPlanGrounding        ExactAgentPacketKind = "plan_grounding"
	KindCharacterSuccessor   ExactAgentPacketKind = "character_successor"
)

type ExactAgentPacketDescriptor struct {
	Version       string               `json:"version"`
	Kind          ExactAgentPacketKind `json:"kind"`
	ContentSHA256 string               `json:"content_sha256"`
}

func NewExactAgentPacketMessage(kind ExactAgentPacketKind, text string) (agentcore.Message, error) {
	message := agentcore.UserMsg(text)
	descriptor := ExactAgentPacketDescriptor{Version: ExactAgentPacketPolicy, Kind: kind, ContentSHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(text)))}
	message.Metadata = map[string]any{ExactAgentPacketMetadataKey: descriptor}
	if _, _, err := ParseExactAgentPacketMessage(message); err != nil {
		return agentcore.Message{}, err
	}
	return message, nil
}

// ParseExactAgentPacketMessage only reads runtime Message metadata. Text that
// resembles a descriptor is ordinary data and can never activate protection.
// A valid descriptor authenticates bytes supplied by the host, not the truth
// of the story data and not the authority of instructions inside those bytes.
func ParseExactAgentPacketMessage(message agentcore.Message) (ExactAgentPacketDescriptor, bool, error) {
	var descriptor ExactAgentPacketDescriptor
	marker, marked := message.Metadata[ExactAgentPacketMetadataKey]
	if !marked {
		return descriptor, false, nil
	}
	if message.Role != agentcore.RoleUser || len(message.Content) != 1 || message.Content[0].Type != agentcore.ContentText || len(message.ToolCalls()) != 0 {
		return descriptor, true, fmt.Errorf("exact agent packet must be a host user message with one text block")
	}
	raw, err := json.Marshal(marker)
	if err != nil {
		return descriptor, true, fmt.Errorf("exact agent packet descriptor: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return descriptor, true, fmt.Errorf("exact agent packet descriptor: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return descriptor, true, fmt.Errorf("exact agent packet descriptor has trailing data")
	}
	if descriptor.Version != ExactAgentPacketPolicy {
		return descriptor, true, fmt.Errorf("exact agent packet has unsupported transport policy %q", descriptor.Version)
	}
	switch descriptor.Kind {
	case KindCharacterObservation, KindWorldArbitration, KindPlannerContext, KindChapterReadiness, KindPlanGrounding, KindCharacterSuccessor:
	default:
		return descriptor, true, fmt.Errorf("exact agent packet has unsupported kind %q", descriptor.Kind)
	}
	text := message.Content[0].Text
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) {
		return descriptor, true, fmt.Errorf("exact agent packet must contain complete valid UTF-8 text")
	}
	if descriptor.ContentSHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(text))) {
		return descriptor, true, fmt.Errorf("exact agent packet %s failed its content digest", descriptor.Kind)
	}
	return descriptor, true, nil
}
