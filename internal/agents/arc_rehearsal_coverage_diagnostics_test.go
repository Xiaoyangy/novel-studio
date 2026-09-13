package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestArcRehearsalCoverageDiagnosticsAggregateWithoutAccepting(t *testing.T) {
	_, input, _ := rehearsalCapabilityFixture(t)
	baseline := rehearsalCapabilityBody(input)
	for _, tc := range []struct {
		name   string
		change func(*domain.ArcRehearsalBody)
		want   []string
	}{
		{"multiple-resources-in-one-material", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].ResourceRefs = []string{rehearsalFuelID, rehearsalDeviceID, rehearsalPaperID}
		}, []string{"material_checks[1]", rehearsalDeviceID, rehearsalPaperID, "lacks an executable dependency"}},
		{"multiple-materials", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].ResourceRefs = []string{rehearsalDeviceID}
			b.MaterialChecks[2].ResourceRefs = []string{rehearsalPaperID}
		}, []string{"material_checks[1]", rehearsalDeviceID, "material_checks[2]", rehearsalPaperID}},
		{"coverage-before-invalid-future-access", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[1].ResourceRefs = []string{rehearsalDeviceID}
			b.MaterialChecks[6].CapabilityRequirements[0].DependsOn = []string{"correct_note"}
		}, []string{"material_checks[1]", rehearsalDeviceID, "material_checks[6].capability_requirements[0]", `key="read_note"`, "future artifact use requires preceding recipient access"}},
		{"cross-material-dependency-is-not-local-coverage", func(b *domain.ArcRehearsalBody) {
			b.MaterialChecks[3].ResourceRefs = []string{rehearsalFuelID, rehearsalPaperID}
			b.MaterialChecks[3].CapabilityRequirements[0].DependsOn = []string{"fuel_measure"}
		}, []string{"material_checks[3]", rehearsalFuelID, "lacks an executable dependency"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := rehearsalCapabilityClone(t, baseline)
			tc.change(&body)
			raw, err := json.Marshal(body)
			selectionMust(t, err)
			tool := &submitArcRehearsalTool{input: input}
			_, err = tool.Execute(context.Background(), raw)
			if err == nil || tool.body != nil {
				t.Fatal("invalid material coverage/dependency was accepted or retained")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("one diagnostic omitted %q: %s", want, err)
				}
			}
		})
	}
}

func TestArcRehearsalCoverageMaterialInputsPreserveLegalBodyAndReceipt(t *testing.T) {
	_, input, _ := rehearsalCapabilityFixture(t)
	legal := rehearsalCapabilityBody(input)
	// The current material's own write consumes paper through MaterialInputs,
	// not CapabilityRequirements.ResourceRefs or a different material's action.
	legal.MaterialChecks[3].ResourceRefs = []string{rehearsalPaperID}
	if len(legal.MaterialChecks[3].CapabilityRequirements[0].ResourceRefs) != 0 {
		t.Fatal("fixture no longer isolates MaterialInputs coverage")
	}
	raw, err := json.Marshal(legal)
	selectionMust(t, err)
	fresh := &submitArcRehearsalTool{input: input}
	wantReceipt, err := fresh.Execute(context.Background(), raw)
	selectionMust(t, err)
	wantBody, err := json.Marshal(fresh.body)
	selectionMust(t, err)
	if !bytes.Equal(raw, wantBody) {
		t.Fatal("legal body changed during submission")
	}
	invalid := rehearsalCapabilityClone(t, legal)
	invalid.MaterialChecks[1].ResourceRefs = []string{rehearsalDeviceID, rehearsalPaperID}
	bad, err := json.Marshal(invalid)
	selectionMust(t, err)
	reused := &submitArcRehearsalTool{input: input}
	if _, err := reused.Execute(context.Background(), bad); err == nil || reused.body != nil {
		t.Fatal("coverage failure retained a body")
	}
	gotReceipt, err := reused.Execute(context.Background(), raw)
	selectionMust(t, err)
	gotBody, err := json.Marshal(reused.body)
	selectionMust(t, err)
	if !bytes.Equal(gotReceipt, wantReceipt) || !bytes.Equal(gotBody, wantBody) {
		t.Fatal("aggregated diagnostics changed a corrected legal receipt/body")
	}
	call := domain.ArcRehearsalCall{Role: "architect", Provider: "fixture", Model: "fixture", UsageIDs: []string{"coverage-call"}, ToolCallID: "coverage-submit", ResponseDigest: "sha256:" + strings.Repeat("a", 64)}
	first, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: *fresh.body, Call: call})
	selectionMust(t, err)
	second, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: *reused.body, Call: call})
	selectionMust(t, err)
	firstJSON, err := json.Marshal(first)
	selectionMust(t, err)
	secondJSON, err := json.Marshal(second)
	selectionMust(t, err)
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("legal finalized draft bytes/digest changed after a rejected attempt")
	}
}
