package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func renderProsePermitFixture(t *testing.T) (*Store, string, []string) {
	t.Helper()
	base := t.TempDir()
	live := filepath.Join(base, "output")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	candidateID := "render-ch0001-permit"
	candidate := filepath.Join(base, ".render-candidates", candidateID, "output")
	st := NewStore(candidate)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	planDigest := "sha256:" + strings.Repeat("a", 64)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionRender, TargetChapter: 1,
		PlanDigest: planDigest, Owner: "render-permit-owner",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	manifest := renderPermitCandidateManifest{
		Version:                "pipeline-render-candidate.v2",
		CandidateID:            candidateID,
		GenerationID:           "pg2_permit",
		Chapter:                1,
		PlanDigest:             planDigest,
		PlanCheckpointSeq:      1,
		ProjectedBundleDigest:  "sha256:" + strings.Repeat("e", 64),
		PromotionReceiptDigest: "sha256:" + strings.Repeat("f", 64),
		SourceOutputDir:        live,
	}
	if err := st.Runtime.io.WriteJSON("meta/planning/render_candidate.json", manifest); err != nil {
		t.Fatal(err)
	}
	authorizations := []string{
		"sha256:" + strings.Repeat("b", 64),
		"sha256:" + strings.Repeat("c", 64),
	}
	ledger := renderPermitDispatchLedger{
		Version: renderDispatchLedgerVersion, CandidateID: candidateID,
		GenerationID: "pg2_permit", Chapter: 1, PlanDigest: planDigest,
		PlanCheckpointSeq:      1,
		ProjectedBundleDigest:  "sha256:" + strings.Repeat("e", 64),
		PromotionReceiptDigest: "sha256:" + strings.Repeat("f", 64),
		Limit:                  domain.PipelineRenderWholeBodyDispatchLimit,
		UpdatedAt:              time.Now().UTC().Format(time.RFC3339Nano),
		Reservations: []renderPermitDispatchReservation{
			{AuthorizationDigest: authorizations[0], Attempt: 1, Status: "reserved", ReservedAt: time.Now().UTC().Format(time.RFC3339Nano)},
			{AuthorizationDigest: authorizations[1], Attempt: 2, Status: "reserved", ReservedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		},
	}
	ledgerPath := filepath.Join(base, ".render-candidates", "convergence", candidateID, "dispatch_budget.json")
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return st, ledgerPath, authorizations
}

type renderPermitIdentityTamperCase struct {
	name     string
	permit   func(*PipelineRenderProsePermit)
	manifest func(*renderPermitCandidateManifest)
	ledger   func(*renderPermitDispatchLedger)
}

func renderPermitIdentityTamperCases() []renderPermitIdentityTamperCase {
	return []renderPermitIdentityTamperCase{
		{
			name:     "candidate-id",
			permit:   func(value *PipelineRenderProsePermit) { value.CandidateID = "render-ch0001-tampered" },
			manifest: func(value *renderPermitCandidateManifest) { value.CandidateID = "render-ch0001-tampered" },
			ledger:   func(value *renderPermitDispatchLedger) { value.CandidateID = "render-ch0001-tampered" },
		},
		{
			name:     "generation-id",
			permit:   func(value *PipelineRenderProsePermit) { value.GenerationID = "pg2_tampered" },
			manifest: func(value *renderPermitCandidateManifest) { value.GenerationID = "pg2_tampered" },
			ledger:   func(value *renderPermitDispatchLedger) { value.GenerationID = "pg2_tampered" },
		},
		{
			name:     "chapter",
			permit:   func(value *PipelineRenderProsePermit) { value.Chapter = 2 },
			manifest: func(value *renderPermitCandidateManifest) { value.Chapter = 2 },
			ledger:   func(value *renderPermitDispatchLedger) { value.Chapter = 2 },
		},
		{
			name:     "plan-digest",
			permit:   func(value *PipelineRenderProsePermit) { value.PlanDigest = "sha256:" + strings.Repeat("1", 64) },
			manifest: func(value *renderPermitCandidateManifest) { value.PlanDigest = "sha256:" + strings.Repeat("1", 64) },
			ledger:   func(value *renderPermitDispatchLedger) { value.PlanDigest = "sha256:" + strings.Repeat("1", 64) },
		},
		{
			name:     "plan-checkpoint-seq",
			permit:   func(value *PipelineRenderProsePermit) { value.PlanCheckpointSeq = 2 },
			manifest: func(value *renderPermitCandidateManifest) { value.PlanCheckpointSeq = 2 },
			ledger:   func(value *renderPermitDispatchLedger) { value.PlanCheckpointSeq = 2 },
		},
		{
			name: "projected-bundle-digest",
			permit: func(value *PipelineRenderProsePermit) {
				value.ProjectedBundleDigest = "sha256:" + strings.Repeat("2", 64)
			},
			manifest: func(value *renderPermitCandidateManifest) {
				value.ProjectedBundleDigest = "sha256:" + strings.Repeat("2", 64)
			},
			ledger: func(value *renderPermitDispatchLedger) {
				value.ProjectedBundleDigest = "sha256:" + strings.Repeat("2", 64)
			},
		},
		{
			name: "promotion-receipt-digest",
			permit: func(value *PipelineRenderProsePermit) {
				value.PromotionReceiptDigest = "sha256:" + strings.Repeat("3", 64)
			},
			manifest: func(value *renderPermitCandidateManifest) {
				value.PromotionReceiptDigest = "sha256:" + strings.Repeat("3", 64)
			},
			ledger: func(value *renderPermitDispatchLedger) {
				value.PromotionReceiptDigest = "sha256:" + strings.Repeat("3", 64)
			},
		},
	}
}

func readRenderPermitTestLedger(t *testing.T, path string) renderPermitDispatchLedger {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ledger renderPermitDispatchLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		t.Fatal(err)
	}
	return ledger
}

func writeRenderPermitTestLedger(t *testing.T, path string, ledger renderPermitDispatchLedger) {
	t.Helper()
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func tamperRenderPermitTestEvidence(
	t *testing.T,
	st *Store,
	ledgerPath string,
	target string,
	tamper renderPermitIdentityTamperCase,
) {
	t.Helper()
	switch target {
	case "permit":
		var permit PipelineRenderProsePermit
		if err := st.Runtime.io.ReadJSON(pipelineRenderProsePermitPath, &permit); err != nil {
			t.Fatal(err)
		}
		tamper.permit(&permit)
		if err := st.Runtime.io.WriteJSON(pipelineRenderProsePermitPath, permit); err != nil {
			t.Fatal(err)
		}
	case "manifest":
		const path = "meta/planning/render_candidate.json"
		var manifest renderPermitCandidateManifest
		if err := st.Runtime.io.ReadJSON(path, &manifest); err != nil {
			t.Fatal(err)
		}
		tamper.manifest(&manifest)
		if err := st.Runtime.io.WriteJSON(path, manifest); err != nil {
			t.Fatal(err)
		}
	case "ledger":
		ledger := readRenderPermitTestLedger(t, ledgerPath)
		tamper.ledger(&ledger)
		writeRenderPermitTestLedger(t, ledgerPath, ledger)
	default:
		t.Fatalf("unsupported render permit evidence target %q", target)
	}
}

func TestRenderProsePermitIsDurableAndSingleUse(t *testing.T) {
	st, ledgerPath, authorizations := renderProsePermitFixture(t)
	if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err != nil {
		t.Fatal(err)
	}
	// A fresh Store instance models Host/store reconstruction. The permit must
	// remain available exactly once and be consumed against the same ledger.
	restarted := NewStore(st.Dir())
	if err := restarted.Runtime.ConsumePipelineRenderProsePermit(1); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(st.Dir()).Runtime.ConsumePipelineRenderProsePermit(1); err == nil {
		t.Fatal("consumed render prose permit authorized a second Drafter")
	}
	raw, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	var ledger renderPermitDispatchLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		t.Fatal(err)
	}
	if got := ledger.Reservations[0]; got.Status != "provider_dispatched" || got.ProviderDispatchedAt == "" {
		t.Fatalf("dispatch reservation was not durably consumed: %+v", got)
	}
}

func TestRenderProsePermitRejectsReplacedExecutionLease(t *testing.T) {
	st, _, authorizations := renderProsePermitFixture(t)
	if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[1], 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.ReleasePipelineExecution("render-permit-owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionRender, TargetChapter: 1,
		PlanDigest: "sha256:" + strings.Repeat("a", 64), Owner: "replacement-owner",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(st.Dir()).Runtime.ConsumePipelineRenderProsePermit(1); err == nil {
		t.Fatal("permit from replaced execution lease reached Drafter")
	}
}

func TestRenderProsePermitCannotArmWithoutLedgerReservation(t *testing.T) {
	st, _, _ := renderProsePermitFixture(t)
	unknown := "sha256:" + strings.Repeat("d", 64)
	if err := st.Runtime.ArmPipelineRenderProsePermit(unknown, 3); err == nil {
		t.Fatal("unreserved authorization armed render prose permit")
	}
}

func TestRenderProsePermitArmRejectsEveryManifestLedgerIdentityMismatch(t *testing.T) {
	for _, identity := range renderPermitIdentityTamperCases() {
		for _, target := range []string{"manifest", "ledger"} {
			t.Run(target+"/"+identity.name, func(t *testing.T) {
				st, ledgerPath, authorizations := renderProsePermitFixture(t)
				tamperRenderPermitTestEvidence(t, st, ledgerPath, target, identity)

				if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err == nil {
					t.Fatal("identity-mismatched evidence armed a provider permit")
				}
				if _, err := os.Stat(filepath.Join(st.Dir(), pipelineRenderProsePermitPath)); !os.IsNotExist(err) {
					t.Fatalf("failed arm left a provider permit behind: %v", err)
				}
				ledger := readRenderPermitTestLedger(t, ledgerPath)
				if got := ledger.Reservations[0]; got.Status != "reserved" || got.PermitArmedAt != "" || got.ProviderDispatchedAt != "" {
					t.Fatalf("failed arm advanced dispatch evidence: %+v", got)
				}
			})
		}
	}
}

func TestRenderProsePermitValidateAndConsumeRejectEveryIdentityTamper(t *testing.T) {
	for _, identity := range renderPermitIdentityTamperCases() {
		for _, target := range []string{"permit", "manifest", "ledger"} {
			t.Run(target+"/"+identity.name, func(t *testing.T) {
				st, ledgerPath, authorizations := renderProsePermitFixture(t)
				if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err != nil {
					t.Fatal(err)
				}
				tamperRenderPermitTestEvidence(t, st, ledgerPath, target, identity)

				if err := st.Runtime.ValidatePipelineRenderProsePermit(1); err == nil {
					t.Fatal("identity-tampered evidence passed provider availability validation")
				}
				if err := st.Runtime.ConsumePipelineRenderProsePermit(1); err == nil {
					t.Fatal("identity-tampered evidence consumed a provider permit")
				}
				ledger := readRenderPermitTestLedger(t, ledgerPath)
				if got := ledger.Reservations[0]; got.Status != "permit_armed" || got.ProviderDispatchedAt != "" {
					t.Fatalf("identity tamper reached provider-dispatched state: %+v", got)
				}
			})
		}
	}
}

func TestRenderProsePermitValidateAndConsumeRejectEveryExecutionLeaseTamper(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*PipelineRenderProsePermit)
	}{
		{"owner", func(permit *PipelineRenderProsePermit) { permit.ExecutionOwner = "tampered-owner" }},
		{"process-id", func(permit *PipelineRenderProsePermit) { permit.ExecutionProcessID++ }},
		{"acquired-at", func(permit *PipelineRenderProsePermit) {
			permit.ExecutionAcquiredAt = permit.ExecutionAcquiredAt.Add(time.Nanosecond)
		}},
		{"expires-at", func(permit *PipelineRenderProsePermit) {
			permit.ExecutionExpiresAt = permit.ExecutionExpiresAt.Add(-time.Nanosecond)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, ledgerPath, authorizations := renderProsePermitFixture(t)
			if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err != nil {
				t.Fatal(err)
			}
			var permit PipelineRenderProsePermit
			if err := st.Runtime.io.ReadJSON(pipelineRenderProsePermitPath, &permit); err != nil {
				t.Fatal(err)
			}
			tc.tamper(&permit)
			if err := st.Runtime.io.WriteJSON(pipelineRenderProsePermitPath, permit); err != nil {
				t.Fatal(err)
			}

			if err := st.Runtime.ValidatePipelineRenderProsePermit(1); err == nil {
				t.Fatal("lease-tampered permit passed provider availability validation")
			}
			if err := st.Runtime.ConsumePipelineRenderProsePermit(1); err == nil {
				t.Fatal("lease-tampered permit consumed a provider permit")
			}
			ledger := readRenderPermitTestLedger(t, ledgerPath)
			if got := ledger.Reservations[0]; got.Status != "permit_armed" || got.ProviderDispatchedAt != "" {
				t.Fatalf("lease tamper reached provider-dispatched state: %+v", got)
			}
		})
	}
}

func TestRenderProsePermitRejectsTamperedDispatchLedger(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*renderPermitDispatchLedger)
	}{
		{"limit-drift", func(ledger *renderPermitDispatchLedger) { ledger.Limit = 99 }},
		{"duplicate-authorization", func(ledger *renderPermitDispatchLedger) {
			ledger.Reservations[1].AuthorizationDigest = ledger.Reservations[0].AuthorizationDigest
		}},
		{"invalid-state", func(ledger *renderPermitDispatchLedger) {
			ledger.Reservations[0].Status = "provider_dispatched"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, ledgerPath, authorizations := renderProsePermitFixture(t)
			raw, err := os.ReadFile(ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			var ledger renderPermitDispatchLedger
			if err := json.Unmarshal(raw, &ledger); err != nil {
				t.Fatal(err)
			}
			tc.tamper(&ledger)
			raw, err = json.MarshalIndent(ledger, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ledgerPath, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err == nil {
				t.Fatal("tampered dispatch ledger armed a provider permit")
			}
		})
	}
}
