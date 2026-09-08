package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestArcRehearsalCapabilityDiagnosticsLocateWithoutChangingResults(t *testing.T) {
	_, input, _ := rehearsalCapabilityFixture(t)
	baseline := rehearsalCapabilityBody(input)
	baselineJSON, err := json.Marshal(baseline)
	selectionMust(t, err)
	validTool := &submitArcRehearsalTool{input: input}
	accepted, err := validTool.Execute(context.Background(), baselineJSON)
	selectionMust(t, err)
	for _, tc := range []struct {
		name, path, key, kind, original string
		change                          func(*domain.ArcRehearsalBody)
	}{
		{"future-access", "material_checks[6].capability_requirements[0]", `key="read_note"`, `kind="artifact_read"`, "future artifact use requires preceding recipient access", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[6].CapabilityRequirements[0].DependsOn = []string{"correct_note"}
		}},
		{"ordinary-document-second-requirement", "material_checks[1].capability_requirements[1]", `key="ordinary_doc_as_artifact"`, `kind="artifact_read"`, "artifact read/sign requires an actual artifact or earlier expected write, not an ordinary document", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].RequiresReadable = true
			b.MaterialChecks[1].CapabilityRequirements = append(b.MaterialChecks[1].CapabilityRequirements, domain.ArcRehearsalCapabilityRequirementV1{Key: "ordinary_doc_as_artifact", Kind: "artifact_read", ActorRef: input.CharacterObservations[0].AgentID, ResourceRefs: []string{"res_1111111111111111"}})
		}},
		{"sign-second-requirement-last-material", "material_checks[7].capability_requirements[1]", `key="sign_note"`, `kind="artifact_sign"`, "signing requires own reading of that expected version", func(b *domain.ArcRehearsalBody) {
			r := b.MaterialChecks[7].CapabilityRequirements[0]
			r.DependsOn = []string{"correct_note", "deliver_note"}
			b.MaterialChecks[7].CapabilityRequirements = []domain.ArcRehearsalCapabilityRequirementV1{{Key: "own_work_before_sign", Kind: "self_work", ActorRef: r.ActorRef}, r}
		}},
		{"material-without-capabilities", "material_checks[1]", "", "", "needs explicit executable capability dependencies", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements = nil
		}},
		{"material-coverage-after-valid-requirement", "material_checks[1]", "", "", "lacks an executable dependency", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].ResourceRefs = []string{rehearsalDeviceID}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := rehearsalCapabilityClone(t, baseline)
			tc.change(&candidate)
			raw, err := json.Marshal(candidate)
			selectionMust(t, err)
			tool := &submitArcRehearsalTool{input: input}
			_, err = tool.Execute(context.Background(), raw)
			if err == nil || tool.body != nil {
				t.Fatal("invalid candidate was accepted")
			}
			message := err.Error()
			for _, want := range []string{tc.path, tc.key, tc.kind, tc.original} {
				if !strings.Contains(message, want) {
					t.Fatalf("diagnostic lacks %q: %s", want, message)
				}
			}
			if !strings.HasPrefix(message, tc.path) {
				t.Fatalf("location was lost behind error prose: %s", message)
			}
			if tc.key == "" && strings.Contains(message, ".capability_requirements[") {
				t.Fatalf("material-level error inherited the last requirement index: %s", message)
			}
			if tc.name == "future-access" && !strings.Contains(message, `artifact_ref="correct_note"`) {
				t.Fatalf("missing expected artifact version reference: %s", message)
			}
			// Correcting the same tool's rejected candidate gives exactly the
			// same accepted body and response as a first-try legal submission.
			result, err := tool.Execute(context.Background(), baselineJSON)
			selectionMust(t, err)
			body, _ := json.Marshal(tool.body)
			if !bytes.Equal(result, accepted) || !bytes.Equal(body, baselineJSON) {
				t.Fatal("diagnostic changed legal receipt/body bytes or submission state")
			}
		})
	}
}

func TestArcRehearsalCapabilityDiagnosticKeysAreBoundedAndQuoted(t *testing.T) {
	_, input, _ := rehearsalCapabilityFixture(t)
	for _, key := range []string{"bad\nkey", strings.Repeat("密钥\n", 1000)} {
		body := rehearsalCapabilityBody(input)
		body.MaterialChecks[1].CapabilityRequirements[0].Key = key
		// Embedded control characters are not newly forbidden by this change:
		// use a pre-existing missing-target rejection to test safe quoting.
		body.MaterialChecks[1].CapabilityRequirements[0].ResourceRefs = nil
		err := domain.ValidateArcRehearsalBody(input, body)
		if err == nil {
			t.Fatal("invalid measurement candidate accepted")
		}
		message := err.Error()
		if !strings.HasPrefix(message, "material_checks[1].capability_requirements[0]") || strings.Contains(message, "\n") || len(message) > 700 {
			t.Fatalf("unbounded/unescaped diagnostic: %q", message)
		}
		if len(key) < 128 && !strings.Contains(message, `key="bad\nkey"`) {
			t.Fatalf("key not JSON quoted: %s", message)
		}
		if len(key) > 128 && (!strings.Contains(message, "<omitted utf8_bytes=7000 sha256=") || strings.Contains(message, "密钥")) {
			t.Fatalf("oversized key was echoed/truncated instead of identified: %s", message)
		}
	}
}
