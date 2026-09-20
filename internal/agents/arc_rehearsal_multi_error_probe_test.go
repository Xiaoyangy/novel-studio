package agents

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Diagnostic-only probe of the real submission seam. This never calls a
// model, touches a real book, or creates a production-valid proposal.
func TestArcRehearsalReportsTwoIndependentCapabilityErrorsTogether(t *testing.T) {
	_, input, _ := rehearsalCapabilityFixture(t)
	baseline := rehearsalCapabilityBody(input)
	badObservation := func(body *domain.ArcRehearsalBody) {
		body.MaterialChecks[2].CapabilityRequirements[0].Key = "chenyan_observe_phone_response"
		body.MaterialChecks[2].CapabilityRequirements[0].MechanismRefs = nil
	}
	badArtifact := func(body *domain.ArcRehearsalBody) {
		body.MaterialChecks[6].CapabilityRequirements[0].Key = "shen_read_final_note"
		body.MaterialChecks[6].RequiresReadable = false
		for i := range body.MaterialChecks {
			for j := range body.MaterialChecks[i].CapabilityRequirements {
				r := &body.MaterialChecks[i].CapabilityRequirements[j]
				if r.Key == "correct_note" {
					r.Key = "note_final"
				}
				if r.ArtifactRef == "correct_note" {
					r.ArtifactRef = "note_final"
				}
				for k, dep := range r.DependsOn {
					if dep == "correct_note" {
						r.DependsOn[k] = "note_final"
					}
					if dep == "read_note" {
						r.DependsOn[k] = "shen_read_final_note"
					}
				}
			}
		}
	}
	feedback := func(body domain.ArcRehearsalBody) string {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		tool := &submitArcRehearsalTool{input: input}
		_, err = tool.Execute(t.Context(), raw)
		if err == nil || tool.body != nil {
			t.Fatal("invalid diagnostic fixture was accepted")
		}
		return err.Error()
	}
	both := rehearsalCapabilityClone(t, baseline)
	badObservation(&both)
	badArtifact(&both)
	got := feedback(both)
	t.Logf("BOTH=%s", got)
	onlyLater := rehearsalCapabilityClone(t, baseline)
	badArtifact(&onlyLater)
	t.Logf("AFTER_EARLIER_FIXED=%s", feedback(onlyLater))
	onlyEarlier := rehearsalCapabilityClone(t, baseline)
	badObservation(&onlyEarlier)
	t.Logf("EARLIER_ALONE=%s", feedback(onlyEarlier))
	if !strings.Contains(got, "chenyan_observe_phone_response") || !strings.Contains(got, "shen_read_final_note") {
		t.Fatalf("one full rejected submission hid an independent later bad item: %s", got)
	}
	for _, want := range []string{"mechanism_refs is empty", "requires_readable=false"} {
		if !strings.Contains(got, want) {
			t.Fatalf("failed field not identified: missing %q in %s", want, got)
		}
	}
	later := feedback(onlyLater)
	if !strings.Contains(later, "requires_readable=true; actual=false") || strings.Contains(later, "requires an actual artifact or earlier expected write") {
		t.Fatalf("existing earlier expected writer was falsely reported missing: %s", later)
	}
}

func TestArcRehearsalDiagnosticsIndependentPredicateMatrix(t *testing.T) {
	_, input, _ := rehearsalCapabilityFixture(t)
	for _, tc := range []struct {
		name, want string
		change     func(*domain.ArcRehearsalBody)
	}{
		{"missing-target", "resource_refs count=0", func(b *domain.ArcRehearsalBody) { b.MaterialChecks[2].CapabilityRequirements[0].ResourceRefs = nil }},
		{"numeric-target", "actual_amount is defined", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[2].CapabilityRequirements[0].ResourceRefs = []string{rehearsalFuelID}
		}},
		{"document-target", "readable_facts is nonempty", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[2].CapabilityRequirements[0].ResourceRefs = []string{"res_1111111111111111"}
		}},
		{"missing-mechanism", "mechanism_refs is empty", func(b *domain.ArcRehearsalBody) { b.MaterialChecks[2].CapabilityRequirements[0].MechanismRefs = nil }},
		{"ordinary-document-not-artifact", "resource_refs[0].artifact is absent", func(b *domain.ArcRehearsalBody) {
			r := &b.MaterialChecks[6].CapabilityRequirements[0]
			r.ArtifactRef = ""
			r.ResourceRefs = []string{"res_1111111111111111"}
		}},
		{"earlier-dependency-still-invalid", "missing, later or unavailable step", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].DependsOn = []string{"missing-step"}
			b.MaterialChecks[6].RequiresReadable = false
		}},
		{"invalid-identity-still-invalid", "invalid or unbounded identity", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].CapabilityRequirements[0].Key = ""
			b.MaterialChecks[6].RequiresReadable = false
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := rehearsalCapabilityBody(input)
			tc.change(&body)
			raw, err := json.Marshal(body)
			selectionMust(t, err)
			tool := &submitArcRehearsalTool{input: input}
			_, err = tool.Execute(t.Context(), raw)
			if err == nil || tool.body != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("predicate/result changed: body retained=%t error=%v", tool.body != nil, err)
			}
		})
	}
}

func TestArcRehearsalDiagnosticsAcceptedRealDraftRemainsExact(t *testing.T) {
	root := os.Getenv("ARC_REHEARSAL_REAL_ROOT")
	if root == "" {
		t.Skip("optional read-only validation of an already accepted real draft")
	}
	inputRaw, err := os.ReadFile(filepath.Join(root, "inputs", "2ef3ceb9b9f5457a78fc1f6c7e4fde563c16a98b46e0f4d7d4a1561ccb71cf2d.json"))
	selectionMust(t, err)
	draftRaw, err := os.ReadFile(filepath.Join(root, "drafts", "53c31116388f6e69ff025bcc7a0840aa68d598f54d03a70765fe2c5df79d0521.json"))
	selectionMust(t, err)
	var input domain.ArcRehearsalInput
	var draft domain.ArcRehearsalDraft
	selectionMust(t, json.Unmarshal(inputRaw, &input))
	selectionMust(t, json.Unmarshal(draftRaw, &draft))
	verified, err := domain.FinalizeArcRehearsalDraft(input, draft)
	selectionMust(t, err)
	want, err := json.Marshal(draft)
	selectionMust(t, err)
	got, err := json.Marshal(verified)
	selectionMust(t, err)
	if !bytes.Equal(want, got) {
		t.Fatal("accepted real draft bytes/digest changed")
	}
	body, err := json.Marshal(draft.Body)
	selectionMust(t, err)
	tool := &submitArcRehearsalTool{input: input}
	_, err = tool.Execute(t.Context(), body)
	selectionMust(t, err)
	accepted, err := json.Marshal(tool.body)
	selectionMust(t, err)
	if !bytes.Equal(body, accepted) {
		t.Fatal("accepted real body changed in submit seam")
	}
	t.Logf("unchanged_accepted_draft=%s body_bytes=%d", verified.DraftDigest, len(body))
}
