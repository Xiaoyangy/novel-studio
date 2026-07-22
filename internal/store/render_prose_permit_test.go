package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
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
		SourceOutputDir: live,
		GenerationID:    "pg2_permit", Chapter: 1, PlanDigest: planDigest,
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

func renderProsePermitV3Fixture(t *testing.T) (*Store, string, []string) {
	t.Helper()
	st, ledgerPath, authorizations := renderProsePermitFixture(t)
	var manifest renderPermitCandidateManifest
	if err := st.Runtime.io.ReadJSON("meta/planning/render_candidate.json", &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = renderPermitCandidateManifestVersionV3EffectiveStyle
	manifest.PipelineRenderInputDigest = "sha256:" + strings.Repeat("4", 64)
	manifest.RenderContextSHA256 = "sha256:" + strings.Repeat("5", 64)
	styleContract := json.RawMessage(`{"version":3,"configured_style_id":"permit-fixture"}`)
	receipt := renderPermitEffectiveStyleContractReceipt{
		Version:                   renderPermitEffectiveStyleContractVersion,
		GenerationID:              manifest.GenerationID,
		Chapter:                   manifest.Chapter,
		PlanDigest:                manifest.PlanDigest,
		PlanCheckpointSeq:         manifest.PlanCheckpointSeq,
		BaseRenderContextSHA256:   manifest.RenderContextSHA256,
		PipelineRenderInputDigest: manifest.PipelineRenderInputDigest,
		ProjectedBundleDigest:     manifest.ProjectedBundleDigest,
		PromotionReceiptDigest:    manifest.PromotionReceiptDigest,
		CandidateID:               manifest.CandidateID,
		StyleID:                   "permit-fixture",
		StyleAssetSHA256:          renderPermitEffectiveStyleSHA256([]byte("permit fixture style")),
		StyleContractProtocol:     renderPermitStyleContractProtocolVersion,
		StyleContract:             styleContract,
		StyleContractSHA256:       renderPermitEffectiveStyleSHA256(styleContract),
		SerialMemoryCompletedSet:  []int{},
		SourceChapterBodies:       []renderPermitEffectiveStyleSourceBody{},
		SerialMemoryStopwords:     []string{},
		SerialMemoryCompiler:      stylestat.SerialMemoryCompilerProtocolVersion,
		CreatedAt:                 time.Now().UTC().Format(time.RFC3339Nano),
	}
	receipt.SerialMemoryCompilerRoot = stylestat.SerialMemoryCompilerRoot(
		receipt.SerialMemoryCompletedSet,
		receipt.SourceChapterBodies,
		receipt.SerialMemoryStopwords,
	)
	receiptDigest, err := renderPermitEffectiveStyleReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ReceiptDigest = receiptDigest
	manifest.EffectiveStyleReceiptDigest = receiptDigest
	if err := st.Runtime.io.WriteJSON("meta/planning/render_candidate.json", manifest); err != nil {
		t.Fatal(err)
	}
	writeRenderPermitTestEffectiveStyleReceipt(t, st, receipt)
	writeRenderPermitTestStyleEpochIntent(t, st, manifest)
	ledger := readRenderPermitTestLedger(t, ledgerPath)
	ledger.PipelineRenderInputDigest = manifest.PipelineRenderInputDigest
	ledger.RenderContextSHA256 = manifest.RenderContextSHA256
	ledger.EffectiveStyleReceiptDigest = manifest.EffectiveStyleReceiptDigest
	writeRenderPermitTestLedger(t, ledgerPath, ledger)
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

func readRenderPermitTestEffectiveStyleReceipt(t *testing.T, st *Store) renderPermitEffectiveStyleContractReceipt {
	t.Helper()
	var receipt renderPermitEffectiveStyleContractReceipt
	if err := st.Runtime.io.ReadJSON(renderPermitEffectiveStyleContractPath, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func writeRenderPermitTestEffectiveStyleReceipt(
	t *testing.T,
	st *Store,
	receipt renderPermitEffectiveStyleContractReceipt,
) {
	t.Helper()
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.io.WithWriteLock(func() error {
		return st.Runtime.io.WriteFileUnlocked(renderPermitEffectiveStyleContractPath, append(raw, '\n'))
	}); err != nil {
		t.Fatal(err)
	}
}

func redigestRenderPermitTestEffectiveStyleReceipt(
	t *testing.T,
	receipt *renderPermitEffectiveStyleContractReceipt,
) {
	t.Helper()
	digest, err := renderPermitEffectiveStyleReceiptDigest(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ReceiptDigest = digest
}

func renderPermitTestStyleEpochIntentPath(st *Store, candidateID string) string {
	namespace := filepath.Dir(filepath.Dir(st.Dir()))
	return filepath.Join(namespace, renderPermitStyleEpochIntentDir, candidateID+".json")
}

func writeRenderPermitTestStyleEpochIntent(
	t *testing.T,
	st *Store,
	manifest renderPermitCandidateManifest,
) {
	t.Helper()
	intent := renderPermitStyleEpochIntent{
		Version:                   renderPermitStyleEpochIntentVersion,
		CandidateProtocol:         renderPermitCandidateManifestVersionV3EffectiveStyle,
		StyleContractProtocol:     renderPermitStyleContractProtocolVersion,
		CandidateID:               manifest.CandidateID,
		GenerationID:              manifest.GenerationID,
		Chapter:                   manifest.Chapter,
		PlanDigest:                manifest.PlanDigest,
		PlanCheckpointSeq:         manifest.PlanCheckpointSeq,
		ProjectedBundleDigest:     manifest.ProjectedBundleDigest,
		PromotionReceiptDigest:    manifest.PromotionReceiptDigest,
		PipelineRenderInputDigest: manifest.PipelineRenderInputDigest,
		RenderContextSHA256:       manifest.RenderContextSHA256,
	}
	digest, err := renderPermitStyleEpochIntentDigest(intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.IntentDigest = digest
	raw, err := renderPermitStyleEpochIntentBytes(intent)
	if err != nil {
		t.Fatal(err)
	}
	path := renderPermitTestStyleEpochIntentPath(st, manifest.CandidateID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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

func TestRenderProsePermitV3EffectiveStyleIsDurableAndSingleUse(t *testing.T) {
	st, ledgerPath, authorizations := renderProsePermitV3Fixture(t)
	if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err != nil {
		t.Fatal(err)
	}
	var manifest renderPermitCandidateManifest
	if err := st.Runtime.io.ReadJSON("meta/planning/render_candidate.json", &manifest); err != nil {
		t.Fatal(err)
	}
	var permit PipelineRenderProsePermit
	if err := st.Runtime.io.ReadJSON(pipelineRenderProsePermitPath, &permit); err != nil {
		t.Fatal(err)
	}
	if permit.PipelineRenderInputDigest != manifest.PipelineRenderInputDigest ||
		permit.RenderContextSHA256 != manifest.RenderContextSHA256 ||
		permit.EffectiveStyleReceiptDigest != manifest.EffectiveStyleReceiptDigest ||
		permit.SourceOutputDir != manifest.SourceOutputDir {
		t.Fatalf("v3 permit did not bind effective-style identity: %+v", permit)
	}
	if err := NewStore(st.Dir()).Runtime.ValidatePipelineRenderProsePermit(1); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(st.Dir()).Runtime.ConsumePipelineRenderProsePermit(1); err != nil {
		t.Fatal(err)
	}
	ledger := readRenderPermitTestLedger(t, ledgerPath)
	if got := ledger.Reservations[0]; got.Status != "provider_dispatched" || got.ProviderDispatchedAt == "" {
		t.Fatalf("v3 dispatch reservation was not durably consumed: %+v", got)
	}
}

func TestRenderProsePermitBindsExactSourceOutputDirectory(t *testing.T) {
	st, ledgerPath, authorizations := renderProsePermitFixture(t)
	if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err != nil {
		t.Fatal(err)
	}
	const manifestPath = "meta/planning/render_candidate.json"
	var manifest renderPermitCandidateManifest
	if err := st.Runtime.io.ReadJSON(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	alternate := filepath.Join(filepath.Dir(manifest.SourceOutputDir), "alternate-output")
	if err := os.Mkdir(alternate, 0o755); err != nil {
		t.Fatal(err)
	}
	// Both source directories share the same .render-candidates namespace, so
	// topology alone cannot distinguish them. The permit and dispatch ledger
	// must bind the exact asserted source path.
	manifest.SourceOutputDir = alternate
	if err := st.Runtime.io.WriteJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.ValidatePipelineRenderProsePermit(1); err == nil {
		t.Fatal("source-output tamper passed provider availability validation")
	}
	if err := st.Runtime.ConsumePipelineRenderProsePermit(1); err == nil {
		t.Fatal("source-output tamper consumed a provider permit")
	}
	ledger := readRenderPermitTestLedger(t, ledgerPath)
	if got := ledger.Reservations[0]; got.Status != "permit_armed" || got.ProviderDispatchedAt != "" {
		t.Fatalf("source-output tamper reached provider-dispatched state: %+v", got)
	}
}

func TestRenderProsePermitRejectsDescendantSymlinksBeforeProviderBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*testing.T, *Store)
	}{
		{
			name: "root-level-evidence-file",
			tamper: func(t *testing.T, st *Store) {
				external := filepath.Join(t.TempDir(), "characters.json")
				if err := os.WriteFile(external, []byte("[]\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(st.Dir(), "characters.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "chapter-directory",
			tamper: func(t *testing.T, st *Store) {
				external := filepath.Join(t.TempDir(), "chapters")
				if err := os.Mkdir(external, 0o755); err != nil {
					t.Fatal(err)
				}
				chapters := filepath.Join(st.Dir(), "chapters")
				if err := os.Remove(chapters); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, chapters); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, ledgerPath, authorizations := renderProsePermitFixture(t)
			tc.tamper(t, st)
			if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err == nil {
				t.Fatal("symlinked candidate evidence armed a provider permit")
			}
			ledger := readRenderPermitTestLedger(t, ledgerPath)
			if got := ledger.Reservations[0]; got.Status != "reserved" || got.PermitArmedAt != "" {
				t.Fatalf("symlink rejection advanced dispatch evidence: %+v", got)
			}
		})
	}
}

func TestRenderProsePermitRejectsDescendantSymlinkAfterArming(t *testing.T) {
	st, ledgerPath, authorizations := renderProsePermitFixture(t)
	if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "characters.json")
	if err := os.WriteFile(external, []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(st.Dir(), "characters.json")); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.ValidatePipelineRenderProsePermit(1); err == nil {
		t.Fatal("post-arm evidence symlink passed provider availability validation")
	}
	if err := st.Runtime.ConsumePipelineRenderProsePermit(1); err == nil {
		t.Fatal("post-arm evidence symlink consumed a provider permit")
	}
	ledger := readRenderPermitTestLedger(t, ledgerPath)
	if got := ledger.Reservations[0]; got.Status != "permit_armed" || got.ProviderDispatchedAt != "" {
		t.Fatalf("post-arm symlink reached provider-dispatched state: %+v", got)
	}
}

func TestRenderProsePermitPreStyleCandidateCannotArmProvider(t *testing.T) {
	st, ledgerPath, authorizations := renderProsePermitFixture(t)
	var manifest renderPermitCandidateManifest
	if err := st.Runtime.io.ReadJSON("meta/planning/render_candidate.json", &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "pipeline-render-candidate.v3-pre-style"
	manifest.PipelineRenderInputDigest = "sha256:" + strings.Repeat("4", 64)
	manifest.RenderContextSHA256 = "sha256:" + strings.Repeat("5", 64)
	if err := st.Runtime.io.WriteJSON("meta/planning/render_candidate.json", manifest); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err == nil {
		t.Fatal("pre-style candidate armed a prose provider permit")
	}
	ledger := readRenderPermitTestLedger(t, ledgerPath)
	if got := ledger.Reservations[0]; got.Status != "reserved" || got.PermitArmedAt != "" {
		t.Fatalf("rejected pre-style arm advanced dispatch evidence: %+v", got)
	}
}

func TestRenderProsePermitRejectsV3ManifestVersionDowngradeRetainingEffectiveStyleIdentity(t *testing.T) {
	st, ledgerPath, authorizations := renderProsePermitV3Fixture(t)
	var manifest renderPermitCandidateManifest
	if err := st.Runtime.io.ReadJSON("meta/planning/render_candidate.json", &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = renderPermitCandidateManifestVersionV2
	if err := st.Runtime.io.WriteJSON("meta/planning/render_candidate.json", manifest); err != nil {
		t.Fatal(err)
	}

	err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1)
	if err == nil || !strings.Contains(err.Error(), "v2 contains v3 effective-style identity") {
		t.Fatalf("v3 manifest downgrade retained effective-style identity: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), pipelineRenderProsePermitPath)); !os.IsNotExist(err) {
		t.Fatalf("rejected manifest downgrade left a provider permit behind: %v", err)
	}
	ledger := readRenderPermitTestLedger(t, ledgerPath)
	if got := ledger.Reservations[0]; got.Status != "reserved" || got.PermitArmedAt != "" || got.ProviderDispatchedAt != "" {
		t.Fatalf("rejected manifest downgrade advanced dispatch evidence: %+v", got)
	}
}

func TestRenderProsePermitRejectsClearedV3ManifestWhenEpochIntentExists(t *testing.T) {
	st, ledgerPath, authorizations := renderProsePermitV3Fixture(t)
	var manifest renderPermitCandidateManifest
	if err := st.Runtime.io.ReadJSON("meta/planning/render_candidate.json", &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = renderPermitCandidateManifestVersionV2
	manifest.PipelineRenderInputDigest = ""
	manifest.RenderContextSHA256 = ""
	manifest.EffectiveStyleReceiptDigest = ""
	if err := st.Runtime.io.WriteJSON("meta/planning/render_candidate.json", manifest); err != nil {
		t.Fatal(err)
	}

	err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1)
	if err == nil || !strings.Contains(err.Error(), "immutable v3 render style epoch intent") {
		t.Fatalf("provider permit ignored v3 epoch intent after all mutable fields were cleared: %v", err)
	}
	ledger := readRenderPermitTestLedger(t, ledgerPath)
	if got := ledger.Reservations[0]; got.Status != "reserved" || got.PermitArmedAt != "" || got.ProviderDispatchedAt != "" {
		t.Fatalf("rejected epoch downgrade advanced dispatch evidence: %+v", got)
	}
}

func TestRenderProsePermitV3RequiresExactCanonicalStyleEpochIntent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*testing.T, *Store, renderPermitCandidateManifest, string)
	}{
		{
			name: "missing",
			tamper: func(t *testing.T, _ *Store, _ renderPermitCandidateManifest, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "noncanonical-bytes",
			tamper: func(t *testing.T, _ *Store, _ renderPermitCandidateManifest, path string) {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "canonical-identity-drift",
			tamper: func(t *testing.T, _ *Store, _ renderPermitCandidateManifest, path string) {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var intent renderPermitStyleEpochIntent
				if err := json.Unmarshal(raw, &intent); err != nil {
					t.Fatal(err)
				}
				intent.GenerationID = "pg2_drifted"
				intent.IntentDigest, err = renderPermitStyleEpochIntentDigest(intent)
				if err != nil {
					t.Fatal(err)
				}
				raw, err = renderPermitStyleEpochIntentBytes(intent)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			tamper: func(t *testing.T, _ *Store, _ renderPermitCandidateManifest, path string) {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "style-epoch.json")
				if err := os.WriteFile(target, raw, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, ledgerPath, authorizations := renderProsePermitV3Fixture(t)
			var manifest renderPermitCandidateManifest
			if err := st.Runtime.io.ReadJSON("meta/planning/render_candidate.json", &manifest); err != nil {
				t.Fatal(err)
			}
			path := renderPermitTestStyleEpochIntentPath(st, manifest.CandidateID)
			tc.tamper(t, st, manifest, path)

			if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err == nil {
				t.Fatal("non-canonical v3 style epoch intent armed a provider permit")
			}
			if _, err := os.Stat(filepath.Join(st.Dir(), pipelineRenderProsePermitPath)); !os.IsNotExist(err) {
				t.Fatalf("rejected v3 style epoch intent left a provider permit behind: %v", err)
			}
			ledger := readRenderPermitTestLedger(t, ledgerPath)
			if got := ledger.Reservations[0]; got.Status != "reserved" || got.PermitArmedAt != "" || got.ProviderDispatchedAt != "" {
				t.Fatalf("rejected v3 style epoch intent advanced dispatch evidence: %+v", got)
			}
		})
	}
}

func TestRenderProsePermitV3RequiresStyleEpochIntentAtProviderGate(t *testing.T) {
	st, ledgerPath, authorizations := renderProsePermitV3Fixture(t)
	if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err != nil {
		t.Fatal(err)
	}
	var manifest renderPermitCandidateManifest
	if err := st.Runtime.io.ReadJSON("meta/planning/render_candidate.json", &manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(renderPermitTestStyleEpochIntentPath(st, manifest.CandidateID)); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.ValidatePipelineRenderProsePermit(1); err == nil {
		t.Fatal("missing v3 style epoch intent passed provider availability validation")
	}
	if err := st.Runtime.ConsumePipelineRenderProsePermit(1); err == nil {
		t.Fatal("missing v3 style epoch intent consumed a provider permit")
	}
	ledger := readRenderPermitTestLedger(t, ledgerPath)
	if got := ledger.Reservations[0]; got.Status != "permit_armed" || got.ProviderDispatchedAt != "" {
		t.Fatalf("missing v3 style epoch intent reached provider-dispatched state: %+v", got)
	}
}

func TestRenderProsePermitV3RequiresCompleteEffectiveStyleEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*testing.T, *Store)
	}{
		{"pipeline-render-input-digest", func(t *testing.T, st *Store) {
			var manifest renderPermitCandidateManifest
			if err := st.Runtime.io.ReadJSON("meta/planning/render_candidate.json", &manifest); err != nil {
				t.Fatal(err)
			}
			manifest.PipelineRenderInputDigest = ""
			if err := st.Runtime.io.WriteJSON("meta/planning/render_candidate.json", manifest); err != nil {
				t.Fatal(err)
			}
		}},
		{"render-context-sha256", func(t *testing.T, st *Store) {
			var manifest renderPermitCandidateManifest
			if err := st.Runtime.io.ReadJSON("meta/planning/render_candidate.json", &manifest); err != nil {
				t.Fatal(err)
			}
			manifest.RenderContextSHA256 = ""
			if err := st.Runtime.io.WriteJSON("meta/planning/render_candidate.json", manifest); err != nil {
				t.Fatal(err)
			}
		}},
		{"effective-style-receipt-digest", func(t *testing.T, st *Store) {
			var manifest renderPermitCandidateManifest
			if err := st.Runtime.io.ReadJSON("meta/planning/render_candidate.json", &manifest); err != nil {
				t.Fatal(err)
			}
			manifest.EffectiveStyleReceiptDigest = ""
			if err := st.Runtime.io.WriteJSON("meta/planning/render_candidate.json", manifest); err != nil {
				t.Fatal(err)
			}
		}},
		{"effective-style-contract", func(t *testing.T, st *Store) {
			if err := os.Remove(filepath.Join(st.Dir(), filepath.FromSlash(renderPermitEffectiveStyleContractPath))); err != nil {
				t.Fatal(err)
			}
		}},
		{"serial-memory-compiler-protocol", func(t *testing.T, st *Store) {
			receipt := readRenderPermitTestEffectiveStyleReceipt(t, st)
			receipt.SerialMemoryCompiler = ""
			redigestRenderPermitTestEffectiveStyleReceipt(t, &receipt)
			writeRenderPermitTestEffectiveStyleReceipt(t, st, receipt)
		}},
		{"serial-memory-compiler-root", func(t *testing.T, st *Store) {
			receipt := readRenderPermitTestEffectiveStyleReceipt(t, st)
			receipt.SerialMemoryCompilerRoot = "sha256:" + strings.Repeat("9", 64)
			redigestRenderPermitTestEffectiveStyleReceipt(t, &receipt)
			writeRenderPermitTestEffectiveStyleReceipt(t, st, receipt)
		}},
		{"serial-memory-completed-set", func(t *testing.T, st *Store) {
			receipt := readRenderPermitTestEffectiveStyleReceipt(t, st)
			receipt.SerialMemoryCompletedSet = nil
			redigestRenderPermitTestEffectiveStyleReceipt(t, &receipt)
			writeRenderPermitTestEffectiveStyleReceipt(t, st, receipt)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, ledgerPath, authorizations := renderProsePermitV3Fixture(t)
			tc.tamper(t, st)
			if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err == nil {
				t.Fatal("incomplete v3 effective-style evidence armed a provider permit")
			}
			ledger := readRenderPermitTestLedger(t, ledgerPath)
			if got := ledger.Reservations[0]; got.Status != "reserved" || got.PermitArmedAt != "" {
				t.Fatalf("failed v3 arm advanced dispatch evidence: %+v", got)
			}
		})
	}
}

func TestRenderProsePermitV3RejectsCurrentSerialMemoryInputDrift(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drift func(*testing.T, *Store)
	}{
		{"completed-chapter-set", func(t *testing.T, st *Store) {
			if err := st.Progress.Save(&domain.Progress{CompletedChapters: []int{1}}); err != nil {
				t.Fatal(err)
			}
		}},
		{"canonical-stopwords", func(t *testing.T, st *Store) {
			if err := st.Characters.Save([]domain.Character{{Name: "新增角色", Aliases: []string{"新别名"}}}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, ledgerPath, authorizations := renderProsePermitV3Fixture(t)
			tc.drift(t, st)
			if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err == nil {
				t.Fatal("serial-memory input drift armed a prose provider permit")
			}
			ledger := readRenderPermitTestLedger(t, ledgerPath)
			if got := ledger.Reservations[0]; got.Status != "reserved" || got.PermitArmedAt != "" {
				t.Fatalf("serial-memory drift advanced dispatch evidence: %+v", got)
			}
		})
	}
}

func TestRenderProsePermitV3RejectsEffectiveStyleContractTamperBeforeValidationAndConsumption(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*testing.T, *renderPermitEffectiveStyleContractReceipt)
	}{
		{"candidate-identity", func(t *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.CandidateID = "render-ch0001-other"
			redigestRenderPermitTestEffectiveStyleReceipt(t, receipt)
		}},
		{"generation-identity", func(t *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.GenerationID = "pg2_other"
			redigestRenderPermitTestEffectiveStyleReceipt(t, receipt)
		}},
		{"chapter-identity", func(t *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.Chapter = 2
			redigestRenderPermitTestEffectiveStyleReceipt(t, receipt)
		}},
		{"plan-identity", func(t *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.PlanDigest = "sha256:" + strings.Repeat("6", 64)
			redigestRenderPermitTestEffectiveStyleReceipt(t, receipt)
		}},
		{"plan-checkpoint-identity", func(t *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.PlanCheckpointSeq = 2
			redigestRenderPermitTestEffectiveStyleReceipt(t, receipt)
		}},
		{"pipeline-render-input-identity", func(t *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.PipelineRenderInputDigest = "sha256:" + strings.Repeat("6", 64)
			redigestRenderPermitTestEffectiveStyleReceipt(t, receipt)
		}},
		{"render-context-identity", func(t *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.BaseRenderContextSHA256 = "sha256:" + strings.Repeat("7", 64)
			redigestRenderPermitTestEffectiveStyleReceipt(t, receipt)
		}},
		{"projected-bundle-identity", func(t *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.ProjectedBundleDigest = "sha256:" + strings.Repeat("6", 64)
			redigestRenderPermitTestEffectiveStyleReceipt(t, receipt)
		}},
		{"promotion-receipt-identity", func(t *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.PromotionReceiptDigest = "sha256:" + strings.Repeat("7", 64)
			redigestRenderPermitTestEffectiveStyleReceipt(t, receipt)
		}},
		{"embedded-style-contract-digest", func(t *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.StyleContract = json.RawMessage(`{"version":3,"tampered":true}`)
			redigestRenderPermitTestEffectiveStyleReceipt(t, receipt)
		}},
		{"receipt-digest", func(_ *testing.T, receipt *renderPermitEffectiveStyleContractReceipt) {
			receipt.ReceiptDigest = "sha256:" + strings.Repeat("8", 64)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, ledgerPath, authorizations := renderProsePermitV3Fixture(t)
			if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err != nil {
				t.Fatal(err)
			}
			receipt := readRenderPermitTestEffectiveStyleReceipt(t, st)
			tc.tamper(t, &receipt)
			writeRenderPermitTestEffectiveStyleReceipt(t, st, receipt)

			if err := st.Runtime.ValidatePipelineRenderProsePermit(1); err == nil {
				t.Fatal("tampered effective-style contract passed provider availability validation")
			}
			if err := st.Runtime.ConsumePipelineRenderProsePermit(1); err == nil {
				t.Fatal("tampered effective-style contract consumed a provider permit")
			}
			ledger := readRenderPermitTestLedger(t, ledgerPath)
			if got := ledger.Reservations[0]; got.Status != "permit_armed" || got.ProviderDispatchedAt != "" {
				t.Fatalf("effective-style contract tamper reached provider-dispatched state: %+v", got)
			}
		})
	}
}

func TestRenderProsePermitV3RejectsPermitAndLedgerEffectiveStyleIdentityTamper(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*PipelineRenderProsePermit)
	}{
		{"pipeline-render-input-digest", func(permit *PipelineRenderProsePermit) {
			permit.PipelineRenderInputDigest = "sha256:" + strings.Repeat("6", 64)
		}},
		{"render-context-sha256", func(permit *PipelineRenderProsePermit) {
			permit.RenderContextSHA256 = "sha256:" + strings.Repeat("7", 64)
		}},
		{"effective-style-receipt-digest", func(permit *PipelineRenderProsePermit) {
			permit.EffectiveStyleReceiptDigest = "sha256:" + strings.Repeat("8", 64)
		}},
	} {
		t.Run("permit/"+tc.name, func(t *testing.T) {
			st, ledgerPath, authorizations := renderProsePermitV3Fixture(t)
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
				t.Fatal("effective-style identity-tampered permit passed validation")
			}
			if err := st.Runtime.ConsumePipelineRenderProsePermit(1); err == nil {
				t.Fatal("effective-style identity-tampered permit was consumed")
			}
			ledger := readRenderPermitTestLedger(t, ledgerPath)
			if got := ledger.Reservations[0]; got.Status != "permit_armed" || got.ProviderDispatchedAt != "" {
				t.Fatalf("permit identity tamper reached provider-dispatched state: %+v", got)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		tamper func(*renderPermitDispatchLedger)
	}{
		{"pipeline-render-input-digest", func(ledger *renderPermitDispatchLedger) {
			ledger.PipelineRenderInputDigest = "sha256:" + strings.Repeat("6", 64)
		}},
		{"render-context-sha256", func(ledger *renderPermitDispatchLedger) {
			ledger.RenderContextSHA256 = "sha256:" + strings.Repeat("7", 64)
		}},
		{"effective-style-receipt-digest", func(ledger *renderPermitDispatchLedger) {
			ledger.EffectiveStyleReceiptDigest = "sha256:" + strings.Repeat("8", 64)
		}},
	} {
		t.Run("ledger/"+tc.name, func(t *testing.T) {
			st, ledgerPath, authorizations := renderProsePermitV3Fixture(t)
			ledger := readRenderPermitTestLedger(t, ledgerPath)
			tc.tamper(&ledger)
			writeRenderPermitTestLedger(t, ledgerPath, ledger)
			if err := st.Runtime.ArmPipelineRenderProsePermit(authorizations[0], 1); err == nil {
				t.Fatal("effective-style identity-tampered ledger armed a provider permit")
			}
			ledger = readRenderPermitTestLedger(t, ledgerPath)
			if got := ledger.Reservations[0]; got.Status != "reserved" || got.PermitArmedAt != "" {
				t.Fatalf("ledger identity tamper advanced dispatch evidence: %+v", got)
			}
		})
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
