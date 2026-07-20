package aigc

import (
	"encoding/json"
	"testing"
)

func TestProseRenderPrimingEnvelopeRoundTripAndTamperDetection(t *testing.T) {
	payload := json.RawMessage(`{"_context_profile":"draft","render_packet":{"version":11}}`)
	text, wantIdentity, err := BuildProseRenderPrimingEnvelope(3, "sha256:plan", payload)
	if err != nil {
		t.Fatal(err)
	}
	gotPayload, gotIdentity, recognized, err := ParseProseRenderPrimingEnvelope(text)
	if err != nil || !recognized || gotIdentity != wantIdentity {
		t.Fatalf("priming round trip recognized=%t identity=%+v err=%v", recognized, gotIdentity, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(gotPayload, &decoded); err != nil || decoded["_context_profile"] != "draft" {
		t.Fatalf("priming payload changed: payload=%s err=%v", gotPayload, err)
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["payload"].(map[string]any)["tampered"] = true
	tampered, _ := json.Marshal(envelope)
	if _, _, recognized, err := ParseProseRenderPrimingEnvelope(string(tampered)); !recognized || err == nil {
		t.Fatalf("tampered priming envelope was accepted: recognized=%t err=%v", recognized, err)
	}
}
