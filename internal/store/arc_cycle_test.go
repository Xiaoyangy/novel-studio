package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

func arcCycleStoreTestDigest(label string) string {
	sum := sha256.Sum256([]byte(label))
	return domain.PlanningV2DigestPrefix + hex.EncodeToString(sum[:])
}

func arcCycleStoreTestManifest(t *testing.T, generationID string) domain.ArcPlanningManifest {
	t.Helper()
	manifest := domain.ArcPlanningManifest{
		Version:           domain.ArcPlanningManifestVersion,
		ArcID:             domain.DeriveArcCycleID(1, 1, 1, 3),
		GenerationID:      generationID,
		Volume:            1,
		Arc:               1,
		FirstChapter:      1,
		LastChapter:       3,
		BookLastChapter:   6,
		FullOutlineDigest: arcCycleStoreTestDigest("outline"),
		ChapterBodyRunes: domain.ArcChapterBodyRuneContract{
			MinRunes:              1,
			MaxRunes:              1000,
			SourceUserRulesDigest: arcCycleStoreTestDigest("user-rules"),
		},
		Chapters: []domain.ArcChapterPlanningBinding{
			{Chapter: 1, BundleDigest: arcCycleStoreTestDigest("bundle-1"), CapacityDigest: arcCycleStoreTestDigest("capacity-1")},
			{Chapter: 2, BundleDigest: arcCycleStoreTestDigest("bundle-2"), CapacityDigest: arcCycleStoreTestDigest("capacity-2")},
			{Chapter: 3, BundleDigest: arcCycleStoreTestDigest("bundle-3"), CapacityDigest: arcCycleStoreTestDigest("capacity-3")},
		},
		CausalLinks: []domain.ArcCausalLink{
			{ID: "cause-1-2", FromChapter: 1, ToChapter: 2, Cause: "第一章的代价", Effect: "第二章失去安全退路"},
			{ID: "cause-2-3", FromChapter: 2, ToChapter: 3, Cause: "第二章的公开拒绝", Effect: "第三章必须正面结算冲突"},
		},
		Turns:   []domain.ArcNarrativeMarker{{ID: "turn-1", Chapter: 2, Summary: "原有合作关系翻转"}},
		Payoffs: []domain.ArcNarrativeMarker{{ID: "payoff-1", Chapter: 3, Summary: "阶段承诺在行动后果中兑现"}},
		CarriedObligations: []domain.ArcCarriedObligation{{
			ObligationID:     "obl:future-cost",
			OriginChapter:    2,
			DueChapter:       5,
			ObligationDigest: arcCycleStoreTestDigest("future-cost"),
		}},
		CreatedAt: "2026-07-17T11:00:00Z",
	}
	manifest, err := domain.SignArcPlanningManifest(manifest)
	if err != nil {
		t.Fatalf("sign manifest: %v", err)
	}
	return manifest
}

type arcCycleStoreEvidence struct {
	body       []byte
	reviewPath string
	review     []byte
	aiGatePath string
	aiGate     []byte
}

func arcCycleStoreWriteEvidence(t *testing.T, root string, chapter int) arcCycleStoreEvidence {
	t.Helper()
	evidence := arcCycleStoreEvidence{
		body:       []byte(fmt.Sprintf("第%d章正文：人物作出选择，并承担可见后果。", chapter)),
		reviewPath: fmt.Sprintf("reviews/%02d.json", chapter),
		review:     []byte(fmt.Sprintf(`{"chapter":%d,"scope":"chapter","verdict":"accept"}`, chapter)),
		aiGatePath: fmt.Sprintf("reviews/%02d_ai_gate.json", chapter),
		aiGate:     []byte(fmt.Sprintf(`{"chapter":%d,"verdict":"pass"}`, chapter)),
	}
	for rel, raw := range map[string][]byte{
		fmt.Sprintf("chapters/%02d.md", chapter): evidence.body,
		evidence.reviewPath:                      evidence.review,
		evidence.aiGatePath:                      evidence.aiGate,
	} {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return evidence
}

func arcCycleStoreTestAcceptance(
	t *testing.T,
	manifest domain.ArcPlanningManifest,
	chapter int,
	evidence arcCycleStoreEvidence,
) domain.ChapterAcceptanceReceipt {
	t.Helper()
	receipt := domain.ChapterAcceptanceReceipt{
		Version:           domain.ChapterAcceptanceReceiptLegacyVersion,
		ArcID:             manifest.ArcID,
		ArcManifestDigest: manifest.ManifestDigest,
		GenerationID:      manifest.GenerationID,
		Chapter:           chapter,
		ChapterBodySHA256: domain.ComputeArcChapterBodySHA256(evidence.body),
		ChapterBodyRunes:  utf8.RuneCount(evidence.body),
		ReviewArtifacts: []domain.ChapterReviewArtifactBinding{
			{Path: evidence.reviewPath, Digest: domain.ComputeArcArtifactSHA256(evidence.review)},
			{Path: evidence.aiGatePath, Digest: domain.ComputeArcArtifactSHA256(evidence.aiGate)},
		},
		OutcomeReceiptDigest: arcCycleStoreTestDigest(fmt.Sprintf("outcome-%d", chapter)),
		AcceptedAt:           fmt.Sprintf("2026-07-17T11:0%d:00Z", chapter),
	}
	receipt, err := domain.SignChapterAcceptanceReceipt(receipt)
	if err != nil {
		t.Fatalf("sign chapter %d acceptance: %v", chapter, err)
	}
	return receipt
}

func TestArcCycleStoreRoundTripAndValidateCompletion(t *testing.T) {
	root := t.TempDir()
	arcStore := NewStore(root).ArcCycle()
	manifest := arcCycleStoreTestManifest(t, "pg2_arc_store_roundtrip")

	manifestDigest, err := arcStore.SaveArcPlanningManifest(manifest)
	if err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if second, err := arcStore.SaveArcPlanningManifest(manifest); err != nil || second != manifestDigest {
		t.Fatalf("idempotent manifest save: digest=%s err=%v", second, err)
	}

	acceptances := make([]domain.ChapterAcceptanceReceipt, 0, 3)
	for chapter := 1; chapter <= 3; chapter++ {
		evidence := arcCycleStoreWriteEvidence(t, root, chapter)
		receipt := arcCycleStoreTestAcceptance(t, manifest, chapter, evidence)
		if _, err := arcStore.SaveChapterAcceptanceReceipt(receipt); err != nil {
			t.Fatalf("save chapter %d acceptance: %v", chapter, err)
		}
		acceptances = append(acceptances, receipt)
	}

	completion, err := domain.NewArcCompletionReceipt(
		manifest,
		acceptances,
		arcCycleStoreTestDigest("actual-post-state-3"),
		"2026-07-17T11:04:00Z",
	)
	if err != nil {
		t.Fatalf("new completion: %v", err)
	}
	completionDigest, err := arcStore.SaveArcCompletionReceipt(completion)
	if err != nil {
		t.Fatalf("save completion: %v", err)
	}
	if err := arcStore.ValidateArcCycle(manifest.GenerationID); err != nil {
		t.Fatalf("validate arc cycle: %v", err)
	}
	if err := arcStore.ValidateArcCompletion(manifest.GenerationID, completionDigest); err != nil {
		t.Fatalf("validate arc completion: %v", err)
	}

	loadedManifest, err := arcStore.LoadArcPlanningManifest(manifest.GenerationID, manifestDigest)
	if err != nil || loadedManifest == nil || loadedManifest.ManifestDigest != manifestDigest {
		t.Fatalf("load manifest: %+v err=%v", loadedManifest, err)
	}
	loadedAcceptance, err := arcStore.LoadChapterAcceptanceReceipt(manifest.GenerationID, 2, acceptances[1].ReceiptDigest)
	if err != nil || loadedAcceptance == nil || loadedAcceptance.ChapterBodySHA256 != acceptances[1].ChapterBodySHA256 {
		t.Fatalf("load acceptance: %+v err=%v", loadedAcceptance, err)
	}
	loadedCompletion, err := arcStore.LoadArcCompletionReceipt(manifest.GenerationID, completionDigest)
	if err != nil || loadedCompletion == nil || loadedCompletion.ReceiptDigest != completionDigest {
		t.Fatalf("load completion: %+v err=%v", loadedCompletion, err)
	}
	if listed, err := arcStore.ListChapterAcceptanceReceipts(manifest.GenerationID); err != nil || len(listed) != 3 {
		t.Fatalf("list acceptances: len=%d err=%v", len(listed), err)
	}
}

func TestArcCycleStoreDetectsBodyAndReviewArtifactReplacement(t *testing.T) {
	root := t.TempDir()
	arcStore := NewStore(root).ArcCycle()
	manifest := arcCycleStoreTestManifest(t, "pg2_arc_store_drift")
	if _, err := arcStore.SaveArcPlanningManifest(manifest); err != nil {
		t.Fatal(err)
	}
	evidence := arcCycleStoreWriteEvidence(t, root, 1)
	acceptance := arcCycleStoreTestAcceptance(t, manifest, 1, evidence)
	if _, err := arcStore.SaveChapterAcceptanceReceipt(acceptance); err != nil {
		t.Fatal(err)
	}

	bodyPath := filepath.Join(root, "chapters", "01.md")
	if err := os.WriteFile(bodyPath, []byte("正文已被替换"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := arcStore.ValidateArcCycle(manifest.GenerationID); err == nil || !strings.Contains(err.Error(), "body hash drift") {
		t.Fatalf("body replacement should fail validation, got %v", err)
	}
	if err := os.WriteFile(bodyPath, evidence.body, 0o644); err != nil {
		t.Fatal(err)
	}

	reviewPath := filepath.Join(root, filepath.FromSlash(evidence.reviewPath))
	if err := os.WriteFile(reviewPath, []byte(`{"chapter":1,"scope":"chapter","verdict":"rewrite"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := arcStore.ValidateArcCycle(manifest.GenerationID); err == nil || !strings.Contains(err.Error(), "review artifact hash drift") {
		t.Fatalf("review replacement should fail validation, got %v", err)
	}
	if _, err := arcStore.SaveChapterAcceptanceReceipt(acceptance); err == nil || !strings.Contains(err.Error(), "review artifact hash drift") {
		t.Fatalf("idempotent re-save must still detect review replacement, got %v", err)
	}
}

func TestArcCycleStoreRejectsUnsafeSealedEvidencePaths(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string, evidence arcCycleStoreEvidence)
	}{
		{
			name: "chapter leaf symlink",
			mutate: func(t *testing.T, root string, evidence arcCycleStoreEvidence) {
				t.Helper()
				target := filepath.Join(t.TempDir(), "chapter.md")
				if err := os.WriteFile(target, evidence.body, 0o644); err != nil {
					t.Fatal(err)
				}
				replaceArcCycleTestPathWithSymlink(t, filepath.Join(root, "chapters", "01.md"), target)
			},
		},
		{
			name: "review leaf symlink",
			mutate: func(t *testing.T, root string, evidence arcCycleStoreEvidence) {
				t.Helper()
				target := filepath.Join(t.TempDir(), "review.json")
				if err := os.WriteFile(target, evidence.review, 0o644); err != nil {
					t.Fatal(err)
				}
				replaceArcCycleTestPathWithSymlink(t, filepath.Join(root, filepath.FromSlash(evidence.reviewPath)), target)
			},
		},
		{
			name: "chapter hardlink",
			mutate: func(t *testing.T, root string, evidence arcCycleStoreEvidence) {
				t.Helper()
				external := filepath.Join(root, "external", "chapter.md")
				if err := os.MkdirAll(filepath.Dir(external), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(external, evidence.body, 0o644); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, "chapters", "01.md")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(external, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "review fifo",
			mutate: func(t *testing.T, root string, evidence arcCycleStoreEvidence) {
				t.Helper()
				path := filepath.Join(root, filepath.FromSlash(evidence.reviewPath))
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "review ancestor symlink",
			mutate: func(t *testing.T, root string, _ arcCycleStoreEvidence) {
				t.Helper()
				original := filepath.Join(root, "reviews")
				external := filepath.Join(t.TempDir(), "reviews")
				if err := os.Rename(original, external); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, original); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			arcStore := NewStore(root).ArcCycle()
			generationID := "pg2_arc_store_unsafe_" + strings.NewReplacer(" ", "_", "/", "_").Replace(tc.name)
			manifest := arcCycleStoreTestManifest(t, generationID)
			if _, err := arcStore.SaveArcPlanningManifest(manifest); err != nil {
				t.Fatal(err)
			}
			evidence := arcCycleStoreWriteEvidence(t, root, 1)
			acceptance := arcCycleStoreTestAcceptance(t, manifest, 1, evidence)
			if _, err := arcStore.SaveChapterAcceptanceReceipt(acceptance); err != nil {
				t.Fatal(err)
			}

			tc.mutate(t, root, evidence)
			if _, err := arcStore.SaveChapterAcceptanceReceipt(acceptance); err == nil {
				t.Fatal("idempotent acceptance save accepted unsafe sealed evidence path")
			}
			if err := arcStore.ValidateArcCycle(manifest.GenerationID); err == nil {
				t.Fatal("arc validation accepted unsafe sealed evidence path")
			}
		})
	}
}

func replaceArcCycleTestPathWithSymlink(t *testing.T, path string, target string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func TestArcCycleStoreDetectsEffectiveStyleArchiveMissingAndTamper(t *testing.T) {
	setup := func(t *testing.T) (*ArcCycleStore, string, string, []byte) {
		t.Helper()
		root := t.TempDir()
		arcStore := NewStore(root).ArcCycle()
		manifest := arcCycleStoreTestManifest(t, "pg2_arc_store_style_archive_"+strings.ReplaceAll(t.Name(), "/", "_"))
		if _, err := arcStore.SaveArcPlanningManifest(manifest); err != nil {
			t.Fatal(err)
		}
		evidence := arcCycleStoreWriteEvidence(t, root, 1)
		styleContract := json.RawMessage(`{"version":3,"usage_policy":"surface-only"}`)
		styleReceipt := renderPermitEffectiveStyleContractReceipt{
			Version:                   renderPermitEffectiveStyleContractVersion,
			GenerationID:              manifest.GenerationID,
			Chapter:                   1,
			PlanDigest:                arcCycleStoreTestDigest("style-plan"),
			PlanCheckpointSeq:         1,
			BaseRenderContextSHA256:   arcCycleStoreTestDigest("style-context"),
			PipelineRenderInputDigest: arcCycleStoreTestDigest("style-render-input"),
			ProjectedBundleDigest:     manifest.Chapters[0].BundleDigest,
			PromotionReceiptDigest:    arcCycleStoreTestDigest("style-promotion"),
			CandidateID:               "render-ch0001-archive-evidence",
			StyleID:                   "archive-evidence",
			StyleAssetSHA256:          arcCycleStoreTestDigest("style-asset"),
			StyleContractProtocol:     renderPermitStyleContractProtocolVersion,
			StyleContract:             styleContract,
			StyleContractSHA256:       renderPermitEffectiveStyleSHA256(styleContract),
			SerialMemoryCompletedSet:  []int{},
			SourceChapterBodies:       []renderPermitEffectiveStyleSourceBody{},
			SerialMemoryStopwords:     []string{},
			SerialMemoryCompiler:      stylestat.SerialMemoryCompilerProtocolVersion,
			CreatedAt:                 "2026-07-17T11:01:00Z",
		}
		styleReceipt.SerialMemoryCompilerRoot = stylestat.SerialMemoryCompilerRoot(
			styleReceipt.SerialMemoryCompletedSet,
			styleReceipt.SourceChapterBodies,
			styleReceipt.SerialMemoryStopwords,
		)
		var err error
		styleReceipt.ReceiptDigest, err = renderPermitEffectiveStyleReceiptDigest(styleReceipt)
		if err != nil {
			t.Fatal(err)
		}
		archiveRel := filepath.ToSlash(filepath.Join(
			"meta", "planning", "effective_render_style_contracts", "ch0001",
			styleReceipt.CandidateID,
			strings.TrimPrefix(styleReceipt.ReceiptDigest, "sha256:")+".json",
		))
		archiveRaw, err := json.Marshal(styleReceipt)
		if err != nil {
			t.Fatal(err)
		}
		archiveRaw = append(archiveRaw, '\n')
		archivePath := filepath.Join(root, filepath.FromSlash(archiveRel))
		if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(archivePath, archiveRaw, 0o644); err != nil {
			t.Fatal(err)
		}

		acceptance := arcCycleStoreTestAcceptance(t, manifest, 1, evidence)
		acceptance.Version = domain.ChapterAcceptanceReceiptVersion
		v3ReviewArtifacts := []struct {
			path string
			raw  []byte
		}{
			{path: evidence.reviewPath, raw: evidence.review},
			{path: "reviews/01.md", raw: []byte("# review\n")},
			{path: evidence.aiGatePath, raw: evidence.aiGate},
			{path: "reviews/01_ai_voice_redflags.json", raw: []byte(`{"chapter":1}`)},
			{path: "reviews/01_deepseek_ai_judge.json", raw: []byte(`{"chapter":1}`)},
			{path: "reviews/01_model_provenance.json", raw: []byte(`{"chapter":1}`)},
		}
		acceptance.ReviewArtifacts = make([]domain.ChapterReviewArtifactBinding, 0, len(v3ReviewArtifacts))
		for _, artifact := range v3ReviewArtifacts {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(artifact.path)), artifact.raw, 0o644); err != nil {
				t.Fatal(err)
			}
			acceptance.ReviewArtifacts = append(acceptance.ReviewArtifacts, domain.ChapterReviewArtifactBinding{
				Path:   artifact.path,
				Digest: domain.ComputeArcArtifactSHA256(artifact.raw),
			})
		}
		acceptance.ReviewArtifacts = domain.CanonicalChapterReviewArtifacts(acceptance.ReviewArtifacts)
		acceptance.EffectiveStyleReceiptPath = archiveRel
		acceptance.EffectiveStyleReceiptDigest = styleReceipt.ReceiptDigest
		acceptance.EffectiveStyleArtifactSHA256 = domain.ComputeArcArtifactSHA256(archiveRaw)
		acceptance.ReceiptDigest = ""
		acceptance, err = domain.SignChapterAcceptanceReceipt(acceptance)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := arcStore.SaveChapterAcceptanceReceipt(acceptance); err != nil {
			t.Fatalf("save acceptance with effective-style archive: %v", err)
		}
		return arcStore, manifest.GenerationID, archivePath, archiveRaw
	}

	t.Run("missing", func(t *testing.T) {
		arcStore, generationID, archivePath, _ := setup(t)
		if err := os.Remove(archivePath); err != nil {
			t.Fatal(err)
		}
		if err := arcStore.ValidateArcCycle(generationID); err == nil ||
			!strings.Contains(err.Error(), "effective style receipt archive is missing") {
			t.Fatalf("missing effective-style archive was not rejected: %v", err)
		}
	})

	t.Run("tampered bytes", func(t *testing.T) {
		arcStore, generationID, archivePath, archiveRaw := setup(t)
		tampered := append(append([]byte(nil), archiveRaw...), ' ')
		if err := os.WriteFile(archivePath, tampered, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := arcStore.ValidateArcCycle(generationID); err == nil ||
			!strings.Contains(err.Error(), "effective style receipt archive hash drift") {
			t.Fatalf("tampered effective-style archive was not rejected: %v", err)
		}
	})

	t.Run("symlink file", func(t *testing.T) {
		arcStore, generationID, archivePath, archiveRaw := setup(t)
		external := filepath.Join(t.TempDir(), "external-style-receipt.json")
		if err := os.WriteFile(external, archiveRaw, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(archivePath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, archivePath); err != nil {
			t.Fatal(err)
		}
		if err := arcStore.ValidateArcCycle(generationID); err == nil ||
			!strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlinked effective-style archive was not rejected: %v", err)
		}
	})

	t.Run("hardlink file", func(t *testing.T) {
		arcStore, generationID, archivePath, archiveRaw := setup(t)
		external := filepath.Join(filepath.Dir(filepath.Dir(archivePath)), "hardlinked-style-receipt.json")
		if err := os.WriteFile(external, archiveRaw, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(archivePath); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(external, archivePath); err != nil {
			t.Fatal(err)
		}
		if err := arcStore.ValidateArcCycle(generationID); err == nil ||
			!strings.Contains(err.Error(), "hard link") {
			t.Fatalf("hardlinked effective-style archive was not rejected: %v", err)
		}
	})
}

func TestArcCycleStoreEnforcesSealedUnicodeRuneRangeAndReviewImmutability(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bodyRunes int
		wantError bool
	}{
		{name: "below sealed minimum", bodyRunes: 1999, wantError: true},
		{name: "inside sealed range", bodyRunes: 2500, wantError: false},
		{name: "above sealed maximum", bodyRunes: 3301, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			arcStore := NewStore(root).ArcCycle()
			manifest := arcCycleStoreTestManifest(t, "pg2_arc_store_runes_"+strings.ReplaceAll(tc.name, " ", "_"))
			manifest.ChapterBodyRunes = domain.ArcChapterBodyRuneContract{
				MinRunes:              2000,
				MaxRunes:              3300,
				SourceUserRulesDigest: arcCycleStoreTestDigest("user-rules-2000-3300"),
			}
			manifest.ManifestDigest = ""
			var err error
			manifest, err = domain.SignArcPlanningManifest(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := arcStore.SaveArcPlanningManifest(manifest); err != nil {
				t.Fatal(err)
			}
			if err := arcStore.RequireChapterReviewArtifactsMutable(1); err != nil {
				t.Fatalf("unaccepted chapter review should still be writable: %v", err)
			}

			evidence := arcCycleStoreWriteEvidence(t, root, 1)
			evidence.body = []byte(strings.Repeat("县", tc.bodyRunes))
			if err := os.WriteFile(filepath.Join(root, "chapters", "01.md"), evidence.body, 0o644); err != nil {
				t.Fatal(err)
			}
			acceptance := arcCycleStoreTestAcceptance(t, manifest, 1, evidence)
			_, err = arcStore.SaveChapterAcceptanceReceipt(acceptance)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "outside sealed range=2000-3300") {
					t.Fatalf("bodyRunes=%d should be rejected by sealed range, got %v", tc.bodyRunes, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid 2500-rune body rejected: %v", err)
			}
			if err := arcStore.ValidateArcCycle(manifest.GenerationID); err != nil {
				t.Fatalf("valid exact-body acceptance rejected: %v", err)
			}
			if err := arcStore.RequireChapterReviewArtifactsMutable(1); err == nil ||
				!strings.Contains(err.Error(), "standalone review overwrite is forbidden") {
				t.Fatalf("accepted review artifacts remained overwritable: %v", err)
			}

			// Current user rules are mutable operating state. Changing them cannot
			// retroactively weaken the manifest-bound 2000-3300 proof.
			if err := os.WriteFile(
				filepath.Join(root, "meta", "user_rules.json"),
				[]byte(`{"structured":{"chapter_words":{"min":1,"max":10}}}`),
				0o644,
			); err != nil {
				t.Fatal(err)
			}
			if err := arcStore.ValidateArcCycle(manifest.GenerationID); err != nil {
				t.Fatalf("mutable user_rules drift changed sealed acceptance: %v", err)
			}

			// Editing the sealed range itself is detectable even if the body and
			// review artifacts are untouched.
			manifestPath := filepath.Join(root, arcCycleManifestPath(manifest.GenerationID, manifest.ManifestDigest))
			raw, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			tampered := bytes.Replace(raw, []byte(`"min_runes": 2000`), []byte(`"min_runes": 1`), 1)
			if bytes.Equal(raw, tampered) {
				t.Fatal("test could not locate sealed min_runes field")
			}
			if err := os.WriteFile(manifestPath, tampered, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := arcStore.ValidateArcCycle(manifest.GenerationID); err == nil ||
				!strings.Contains(err.Error(), "manifest_digest mismatch") {
				t.Fatalf("sealed word contract tamper was not detected: %v", err)
			}
		})
	}
}

func TestArcCycleStoreRejectsOutOfOrderAndDifferentContent(t *testing.T) {
	root := t.TempDir()
	arcStore := NewStore(root).ArcCycle()
	manifest := arcCycleStoreTestManifest(t, "pg2_arc_store_conflict")
	if _, err := arcStore.SaveArcPlanningManifest(manifest); err != nil {
		t.Fatal(err)
	}

	changedManifest := manifest
	changedManifest.Payoffs = append([]domain.ArcNarrativeMarker(nil), manifest.Payoffs...)
	changedManifest.Payoffs[0].Summary = "被替换的弧兑现"
	changedManifest.ManifestDigest = ""
	changedManifest, err := domain.SignArcPlanningManifest(changedManifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := arcStore.SaveArcPlanningManifest(changedManifest); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("different manifest content should be rejected, got %v", err)
	}

	evidence2 := arcCycleStoreWriteEvidence(t, root, 2)
	chapter2 := arcCycleStoreTestAcceptance(t, manifest, 2, evidence2)
	if _, err := arcStore.SaveChapterAcceptanceReceipt(chapter2); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("chapter 2 before chapter 1 should fail, got %v", err)
	}

	evidence1 := arcCycleStoreWriteEvidence(t, root, 1)
	chapter1 := arcCycleStoreTestAcceptance(t, manifest, 1, evidence1)
	if _, err := arcStore.SaveChapterAcceptanceReceipt(chapter1); err != nil {
		t.Fatal(err)
	}
	replacementBody := []byte("第一章被重新渲染后的不同正文")
	if err := os.WriteFile(filepath.Join(root, "chapters", "01.md"), replacementBody, 0o644); err != nil {
		t.Fatal(err)
	}
	differentAcceptance := chapter1
	differentAcceptance.ChapterBodySHA256 = domain.ComputeArcChapterBodySHA256(replacementBody)
	differentAcceptance.ChapterBodyRunes = utf8.RuneCount(replacementBody)
	differentAcceptance.ReceiptDigest = ""
	differentAcceptance, err = domain.SignChapterAcceptanceReceipt(differentAcceptance)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := arcStore.SaveChapterAcceptanceReceipt(differentAcceptance); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("second acceptance for same chapter should fail, got %v", err)
	}
}

func TestArcCycleStoreRejectsCrossGenerationAcceptance(t *testing.T) {
	root := t.TempDir()
	arcStore := NewStore(root).ArcCycle()
	manifest := arcCycleStoreTestManifest(t, "pg2_arc_store_generation_a")
	if _, err := arcStore.SaveArcPlanningManifest(manifest); err != nil {
		t.Fatal(err)
	}
	evidence := arcCycleStoreWriteEvidence(t, root, 1)
	receipt := arcCycleStoreTestAcceptance(t, manifest, 1, evidence)
	receipt.GenerationID = "pg2_arc_store_generation_b"
	receipt.ReceiptDigest = ""
	receipt, err := domain.SignChapterAcceptanceReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := arcStore.SaveChapterAcceptanceReceipt(receipt); err == nil || !strings.Contains(err.Error(), "no arc planning manifest") {
		t.Fatalf("cross-generation acceptance should fail, got %v", err)
	}
}
