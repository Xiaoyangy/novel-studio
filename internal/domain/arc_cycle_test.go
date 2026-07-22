package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func arcCycleDomainTestDigest(label string) string {
	sum := sha256.Sum256([]byte(label))
	return PlanningV2DigestPrefix + hex.EncodeToString(sum[:])
}

func arcCycleDomainTestManifest(t *testing.T, generationID string) ArcPlanningManifest {
	t.Helper()
	manifest := ArcPlanningManifest{
		Version:           ArcPlanningManifestVersion,
		ArcID:             DeriveArcCycleID(1, 2, 3, 5),
		GenerationID:      generationID,
		Volume:            1,
		Arc:               2,
		FirstChapter:      3,
		LastChapter:       5,
		BookLastChapter:   8,
		FullOutlineDigest: arcCycleDomainTestDigest("outline"),
		ChapterBodyRunes: ArcChapterBodyRuneContract{
			MinRunes:              1,
			MaxRunes:              1000,
			SourceUserRulesDigest: arcCycleDomainTestDigest("user-rules"),
		},
		Chapters: []ArcChapterPlanningBinding{
			{Chapter: 3, BundleDigest: arcCycleDomainTestDigest("bundle-3"), CapacityDigest: arcCycleDomainTestDigest("capacity-3")},
			{Chapter: 4, BundleDigest: arcCycleDomainTestDigest("bundle-4"), CapacityDigest: arcCycleDomainTestDigest("capacity-4")},
			{Chapter: 5, BundleDigest: arcCycleDomainTestDigest("bundle-5"), CapacityDigest: arcCycleDomainTestDigest("capacity-5")},
		},
		CausalLinks: []ArcCausalLink{
			{ID: "cause-3-4", FromChapter: 3, ToChapter: 4, Cause: "第三章的公开选择", Effect: "第四章的对手立即改变策略"},
			{ID: "cause-4-5", FromChapter: 4, ToChapter: 5, Cause: "第四章的关系破裂", Effect: "第五章被迫兑现本弧承诺"},
		},
		Turns:   []ArcNarrativeMarker{{ID: "turn-1", Chapter: 4, Summary: "盟友拒绝原方案并夺走主动权"}},
		Payoffs: []ArcNarrativeMarker{{ID: "payoff-1", Chapter: 5, Summary: "主角承担选择后果并完成阶段目标"}},
		CarriedObligations: []ArcCarriedObligation{{
			ObligationID:     "obl:later-debt",
			OriginChapter:    4,
			DueChapter:       7,
			ObligationDigest: arcCycleDomainTestDigest("later-debt"),
		}},
		CreatedAt: "2026-07-17T10:00:00Z",
	}
	manifest, err := SignArcPlanningManifest(manifest)
	if err != nil {
		t.Fatalf("sign manifest: %v", err)
	}
	return manifest
}

func arcCycleDomainTestAcceptance(t *testing.T, manifest ArcPlanningManifest, chapter int, acceptedAt string) ChapterAcceptanceReceipt {
	t.Helper()
	receipt := ChapterAcceptanceReceipt{
		Version:           ChapterAcceptanceReceiptLegacyVersion,
		ArcID:             manifest.ArcID,
		ArcManifestDigest: manifest.ManifestDigest,
		GenerationID:      manifest.GenerationID,
		Chapter:           chapter,
		ChapterBodySHA256: arcCycleDomainTestDigest("body-" + string(rune('0'+chapter))),
		ChapterBodyRunes:  100,
		ReviewArtifacts: []ChapterReviewArtifactBinding{
			{Path: "reviews/0" + string(rune('0'+chapter)) + ".json", Digest: arcCycleDomainTestDigest("editor-review-" + string(rune('0'+chapter)))},
			{Path: "reviews/0" + string(rune('0'+chapter)) + "_ai_gate.json", Digest: arcCycleDomainTestDigest("ai-gate-" + string(rune('0'+chapter)))},
		},
		OutcomeReceiptDigest: arcCycleDomainTestDigest("outcome-" + string(rune('0'+chapter))),
		AcceptedAt:           acceptedAt,
	}
	receipt, err := SignChapterAcceptanceReceipt(receipt)
	if err != nil {
		t.Fatalf("sign chapter %d acceptance: %v", chapter, err)
	}
	return receipt
}

func TestArcPlanningManifestRejectsMissingAndOutOfOrderChapterBindings(t *testing.T) {
	manifest := arcCycleDomainTestManifest(t, "pg2_arc_domain")

	missing := manifest
	missing.Chapters = append([]ArcChapterPlanningBinding(nil), manifest.Chapters[:2]...)
	missing.ManifestDigest = ""
	if _, err := SignArcPlanningManifest(missing); err == nil || !strings.Contains(err.Error(), "missing chapter") {
		t.Fatalf("missing chapter binding should fail, got %v", err)
	}

	disordered := manifest
	disordered.Chapters = append([]ArcChapterPlanningBinding(nil), manifest.Chapters...)
	disordered.Chapters[0], disordered.Chapters[1] = disordered.Chapters[1], disordered.Chapters[0]
	disordered.ManifestDigest = ""
	if _, err := SignArcPlanningManifest(disordered); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("out-of-order chapter bindings should fail, got %v", err)
	}
}

func TestChapterAcceptanceRejectsArcOrDraftReviewArtifacts(t *testing.T) {
	manifest := arcCycleDomainTestManifest(t, "pg2_arc_review_paths")
	for _, reviewPath := range []string{
		"reviews/03-arc.json",
		"reviews/drafts/03.json",
		"reviews/04.json",
		"../reviews/03.json",
	} {
		receipt := arcCycleDomainTestAcceptance(t, manifest, 3, "2026-07-17T10:01:00Z")
		receipt.ReviewArtifacts = []ChapterReviewArtifactBinding{{Path: reviewPath, Digest: arcCycleDomainTestDigest("review")}}
		receipt.ReceiptDigest = ""
		if _, err := SignChapterAcceptanceReceipt(receipt); err == nil {
			t.Fatalf("review path %q should not be accepted", reviewPath)
		}
	}
}

func TestChapterAcceptanceVersionSeparatesLegacyAndEffectiveStyleEvidence(t *testing.T) {
	manifest := arcCycleDomainTestManifest(t, "pg2_arc_acceptance_style_version")
	legacy := arcCycleDomainTestAcceptance(t, manifest, 3, "2026-07-17T10:01:00Z")

	downgraded := legacy
	downgraded.Version = ChapterAcceptanceReceiptVersion
	downgraded.ReceiptDigest = ""
	if _, err := SignChapterAcceptanceReceipt(downgraded); err == nil ||
		!strings.Contains(err.Error(), "v3 requires complete effective style") {
		t.Fatalf("v3 acceptance without style archive evidence was signed: %v", err)
	}

	legacyWithStyle := legacy
	legacyWithStyle.EffectiveStyleReceiptPath = "meta/planning/effective_render_style_contracts/ch0003/render-ch0003-test/sha256-test.json"
	legacyWithStyle.EffectiveStyleReceiptDigest = arcCycleDomainTestDigest("style-receipt")
	legacyWithStyle.EffectiveStyleArtifactSHA256 = arcCycleDomainTestDigest("style-artifact")
	legacyWithStyle.ReceiptDigest = ""
	if _, err := SignChapterAcceptanceReceipt(legacyWithStyle); err == nil ||
		!strings.Contains(err.Error(), "legacy v2 cannot carry") {
		t.Fatalf("legacy acceptance carried v3 style evidence: %v", err)
	}
}

func TestChapterAcceptanceV3BindsCompleteFormalReviewSetAndKeepsV2Compatible(t *testing.T) {
	manifest := arcCycleDomainTestManifest(t, "pg2_arc_acceptance_review_set")
	legacy := arcCycleDomainTestAcceptance(t, manifest, 3, "2026-07-17T10:01:00Z")

	legacySubset := legacy
	legacySubset.ReviewArtifacts = append([]ChapterReviewArtifactBinding(nil), legacy.ReviewArtifacts[:1]...)
	legacySubset.ReceiptDigest = ""
	if _, err := SignChapterAcceptanceReceipt(legacySubset); err != nil {
		t.Fatalf("legacy v2 acceptance should keep its nonempty review-set compatibility: %v", err)
	}

	v3 := legacy
	v3.Version = ChapterAcceptanceReceiptVersion
	v3.ReviewArtifacts = []ChapterReviewArtifactBinding{
		{Path: "reviews/03.json", Digest: arcCycleDomainTestDigest("editor")},
		{Path: "reviews/03.md", Digest: arcCycleDomainTestDigest("report")},
		{Path: "reviews/03_ai_gate.json", Digest: arcCycleDomainTestDigest("ai-gate")},
		{Path: "reviews/03_ai_voice_redflags.json", Digest: arcCycleDomainTestDigest("ai-voice")},
		{Path: "reviews/03_deepseek_ai_judge.json", Digest: arcCycleDomainTestDigest("deepseek")},
		{Path: "reviews/03_model_provenance.json", Digest: arcCycleDomainTestDigest("model-provenance")},
	}
	v3.EffectiveStyleReceiptPath = "meta/planning/effective_render_style_contracts/ch0003/render-ch0003-test/sha256-test.json"
	v3.EffectiveStyleReceiptDigest = arcCycleDomainTestDigest("style-receipt")
	v3.EffectiveStyleArtifactSHA256 = arcCycleDomainTestDigest("style-artifact")
	v3.ReceiptDigest = ""
	v3, err := SignChapterAcceptanceReceipt(v3)
	if err != nil {
		t.Fatalf("complete v3 review set should sign: %v", err)
	}

	extra := append([]ChapterReviewArtifactBinding(nil), v3.ReviewArtifacts...)
	extra = append(extra, ChapterReviewArtifactBinding{
		Path:   "reviews/03_optional.json",
		Digest: arcCycleDomainTestDigest("optional"),
	})
	replaced := append([]ChapterReviewArtifactBinding(nil), v3.ReviewArtifacts...)
	replaced[len(replaced)-1] = ChapterReviewArtifactBinding{
		Path:   "reviews/03_other.json",
		Digest: arcCycleDomainTestDigest("other"),
	}
	for name, artifacts := range map[string][]ChapterReviewArtifactBinding{
		"missing":  append([]ChapterReviewArtifactBinding(nil), v3.ReviewArtifacts[:len(v3.ReviewArtifacts)-1]...),
		"extra":    extra,
		"replaced": replaced,
	} {
		t.Run(name, func(t *testing.T) {
			invalid := v3
			invalid.ReviewArtifacts = artifacts
			invalid.ReceiptDigest = ""
			if _, signErr := SignChapterAcceptanceReceipt(invalid); signErr == nil ||
				!strings.Contains(signErr.Error(), "complete formal review set") {
				t.Fatalf("inexact v3 review set was signed: %v", signErr)
			}
		})
	}
}

func TestArcCompletionRejectsMissingDisorderedCrossGenerationAndBodyDrift(t *testing.T) {
	manifest := arcCycleDomainTestManifest(t, "pg2_arc_completion")
	acceptances := []ChapterAcceptanceReceipt{
		arcCycleDomainTestAcceptance(t, manifest, 3, "2026-07-17T10:01:00Z"),
		arcCycleDomainTestAcceptance(t, manifest, 4, "2026-07-17T10:02:00Z"),
		arcCycleDomainTestAcceptance(t, manifest, 5, "2026-07-17T10:03:00Z"),
	}
	completion, err := NewArcCompletionReceipt(
		manifest,
		acceptances,
		arcCycleDomainTestDigest("actual-post-state"),
		"2026-07-17T10:04:00Z",
	)
	if err != nil {
		t.Fatalf("new completion: %v", err)
	}

	if err := ValidateArcCompletionReceiptAgainstManifest(completion, manifest, acceptances[:2]); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing acceptance should fail, got %v", err)
	}

	disordered := append([]ChapterAcceptanceReceipt(nil), acceptances...)
	disordered[0], disordered[1] = disordered[1], disordered[0]
	if err := ValidateArcCompletionReceiptAgainstManifest(completion, manifest, disordered); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("disordered acceptance should fail, got %v", err)
	}

	crossGeneration := append([]ChapterAcceptanceReceipt(nil), acceptances...)
	crossGeneration[1].GenerationID = "pg2_other_generation"
	crossGeneration[1].ReceiptDigest = ""
	crossGeneration[1], err = SignChapterAcceptanceReceipt(crossGeneration[1])
	if err != nil {
		t.Fatalf("sign standalone cross-generation receipt: %v", err)
	}
	if err := ValidateArcCompletionReceiptAgainstManifest(completion, manifest, crossGeneration); err == nil || !strings.Contains(err.Error(), "exact arc manifest") {
		t.Fatalf("cross-generation acceptance should fail, got %v", err)
	}

	bodyDrift := append([]ChapterAcceptanceReceipt(nil), acceptances...)
	bodyDrift[0].ChapterBodySHA256 = arcCycleDomainTestDigest("replacement-body")
	if err := ValidateArcCompletionReceiptAgainstManifest(completion, manifest, bodyDrift); err == nil || !strings.Contains(err.Error(), "drifted") {
		t.Fatalf("body hash drift should fail, got %v", err)
	}
}

func TestArcCompletionUsesChapterOrderNotWallClockOrder(t *testing.T) {
	manifest := arcCycleDomainTestManifest(t, "pg2_arc_non_monotonic_time")
	acceptances := []ChapterAcceptanceReceipt{
		arcCycleDomainTestAcceptance(t, manifest, 3, "2026-07-17T10:09:00Z"),
		arcCycleDomainTestAcceptance(t, manifest, 4, "2026-07-17T10:01:00Z"),
		arcCycleDomainTestAcceptance(t, manifest, 5, "2026-07-17T10:08:00Z"),
	}
	completion, err := NewArcCompletionReceipt(
		manifest,
		acceptances,
		arcCycleDomainTestDigest("non-monotonic-actual-post-state"),
		// Content-addressed chapter order is authoritative even when a host clock
		// reports completion before one or more independently signed acceptances.
		"2026-07-17T10:00:00Z",
	)
	if err != nil {
		t.Fatalf("non-monotonic but chapter-ordered evidence was rejected: %v", err)
	}
	if err := ValidateArcCompletionReceiptAgainstManifest(completion, manifest, acceptances); err != nil {
		t.Fatalf("non-monotonic timestamps became ordering authority: %v", err)
	}
}
