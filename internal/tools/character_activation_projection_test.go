package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestActivationArbitrationSchemaAndPersistenceAllowOmittedPOVOnlyForCycle(t *testing.T) {
	st, session, cycle, proof := activationToolFixture(t, true)
	e := cycle.Evidence
	tool, err := NewResolveCharacterActivationTool(st, session, e.Stimulus, e.Activation, e.Proposals, e.ProtocolDigest, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range tool.Schema()["required"].([]string) {
		if field == "protagonist_projection" {
			t.Fatal("cycle still requires a chapter POV proposal")
		}
	}
	property := tool.Schema()["properties"].(map[string]any)["protagonist_projection"].(map[string]any)
	if !strings.Contains(property["description"].(string), "省略") {
		t.Fatal("cycle schema does not tell the arbiter how to proceed")
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(activationArbiterArgs(t, cycle), &args); err != nil {
		t.Fatal(err)
	}
	delete(args, "protagonist_projection")
	raw, _ := json.Marshal(args)
	if _, err := tool.Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	receipt, err := proof.LoadArbitration(e.GenerationID, e.Chapter, 1)
	if err != nil || receipt == nil || receipt.ProtagonistProjection.Protagonist != "" || receipt.Resolutions[0].Decision != e.Proposals[0].Decision {
		t.Fatalf("cycle receipt was not persisted without inventing POV: %v", err)
	}
	// Omission cannot make a one-shot constructor publish cycle evidence.
	if _, err := NewResolveChapterWorldTool(st, e.Stimulus, e.Activation, e.Proposals, e.ProtocolDigest, nil, 1).Execute(context.Background(), raw); err == nil {
		t.Fatal("cycle escaped through one-shot publication")
	}
	for _, version := range []string{domain.WorldStimulusPacketVersion, domain.WorldStimulusPacketV2Version} {
		stimulus := e.Stimulus
		stimulus.Version, stimulus.Sources = version, nil
		legacy := NewResolveChapterWorldTool(nil, stimulus, e.Activation, e.Proposals, e.ProtocolDigest, nil, 1)
		required := false
		for _, field := range legacy.Schema()["required"].([]string) {
			required = required || field == "protagonist_projection"
		}
		if !required {
			t.Fatalf("%s one-shot schema lost required POV", version)
		}
	}
}
