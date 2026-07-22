package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

func effectiveRenderStyleTestFixture(
	t *testing.T,
) (*store.Store, EffectiveRenderStyleContractIdentity, effectiveRenderStyleCandidateManifest) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	const chapter = 6
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: chapter, Title: "风格回执"}); err != nil {
		t.Fatal(err)
	}
	plan, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(chapter),
		"plan",
		"drafts/06.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	base := json.RawMessage(`{
		"_context_profile":"draft",
		"working_memory":{"render_packet":{
			"version":11,
			"chapter":6,
			"title":"风格回执",
			"required_beats":["冻结事件 A","冻结事件 B"],
			"style_contract":{"version":3,"configured_style":{"id":"old","rules":["旧规则"]},"serial_style_memory":{"based_on_chapters":99}}
		}}
	}`)
	frozen, err := PublishFrozenDraftRenderContext(st, chapter, plan.Digest, base)
	if err != nil {
		t.Fatal(err)
	}
	for previous := 1; previous < chapter; previous++ {
		body := "第" + string(rune('0'+previous)) + "章 旧章\n\n夜里，他没有回头。此生未能远行，望你替我看看远方的山海。\n他只是看了看。"
		if err := st.Drafts.SaveFinalChapter(previous, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Progress.Save(&domain.Progress{
		NovelName:         "style receipt fixture",
		TotalChapters:     chapter,
		CompletedChapters: []int{1, 2, 3, 4, 5},
	}); err != nil {
		t.Fatal(err)
	}
	digest := func(seed byte) string { return "sha256:" + strings.Repeat(string(seed), 64) }
	identity := EffectiveRenderStyleContractIdentity{
		GenerationID:              "pg2_effective-style-fixture",
		Chapter:                   chapter,
		PlanDigest:                plan.Digest,
		PlanCheckpointSeq:         plan.Seq,
		BaseRenderContextSHA256:   frozen.PayloadSHA256,
		PipelineRenderInputDigest: digest('4'),
		ProjectedBundleDigest:     digest('5'),
		PromotionReceiptDigest:    digest('6'),
		CandidateID:               "render-ch0006-effective-style-fixture",
	}
	frozenPlan := map[string]any{
		"chapter":                   chapter,
		"plan_digest":               plan.Digest,
		"plan_checkpoint_seq":       plan.Seq,
		"planning_generation_id":    identity.GenerationID,
		"render_context_sha256":     identity.BaseRenderContextSHA256,
		"pipeline_run_input_digest": identity.PipelineRenderInputDigest,
		"projected_bundle_digest":   identity.ProjectedBundleDigest,
		"promotion_receipt_digest":  identity.PromotionReceiptDigest,
	}
	effectiveRenderStyleWriteJSON(t, filepath.Join(st.Dir(), "meta", "planning", "current_frozen_plan.json"), frozenPlan)
	manifest := effectiveRenderStyleCandidateManifest{
		Version:                   effectiveStyleCandidatePreStyle,
		CandidateID:               identity.CandidateID,
		GenerationID:              identity.GenerationID,
		Chapter:                   chapter,
		PlanDigest:                plan.Digest,
		PlanCheckpointSeq:         plan.Seq,
		ProjectedBundleDigest:     identity.ProjectedBundleDigest,
		PromotionReceiptDigest:    identity.PromotionReceiptDigest,
		PipelineRenderInputDigest: identity.PipelineRenderInputDigest,
		RenderContextSHA256:       identity.BaseRenderContextSHA256,
	}
	effectiveRenderStyleWriteJSON(t, filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"), manifest)
	return st, identity, manifest
}

func effectiveRenderStyleWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func effectiveRenderStyleWriteReceipt(
	t *testing.T,
	st *store.Store,
	receipt EffectiveRenderStyleContractReceipt,
) {
	t.Helper()
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), filepath.FromSlash(EffectiveRenderStyleContractPath))
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveRenderStyleReceiptBindsCanonicalSurfaceContractAndSerialSources(t *testing.T) {
	st, identity, manifest := effectiveRenderStyleTestFixture(t)
	styleBody := `# 细腻现实
- 叙述声音：克制、具体，不替人物总结情绪。
- 句法：长短句随现场压力变化。
- 冲突设计：每章增加一个外部阻碍。
- 线索数量：本章新增三条线索。`
	receipt, err := PublishEffectiveRenderStyleContract(st, identity, "realist", styleBody)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.SourceChapterBodies) != 5 {
		t.Fatalf("serial source bodies=%d want=5", len(receipt.SourceChapterBodies))
	}
	if !slices.Equal(receipt.SerialMemoryCompletedSet, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("authoritative completed set was not bound: %#v", receipt.SerialMemoryCompletedSet)
	}
	if receipt.SerialMemoryCompiler != stylestat.SerialMemoryCompilerProtocolVersion ||
		receipt.SerialMemoryCompilerRoot != stylestat.SerialMemoryCompilerRoot(
			receipt.SerialMemoryCompletedSet,
			receipt.SourceChapterBodies,
			receipt.SerialMemoryStopwords,
		) {
		t.Fatalf("serial-memory compiler protocol/root are incomplete: %+v", receipt)
	}
	if !bytes.Contains(receipt.StyleContract, []byte(`"serial_style_memory"`)) {
		t.Fatalf("accepted-prose serial memory was not compiled: %s", receipt.StyleContract)
	}
	contractText := string(receipt.StyleContract)
	if !strings.Contains(contractText, "叙述声音") || !strings.Contains(contractText, "句法") {
		t.Fatalf("surface rules missing from receipt: %s", contractText)
	}
	if strings.Contains(contractText, "冲突设计") || strings.Contains(contractText, "线索数量") {
		t.Fatalf("semantic planning rules leaked into render-only style receipt: %s", contractText)
	}

	manifest.Version = effectiveStyleCandidateVersion
	manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
	effectiveRenderStyleWriteJSON(t, filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"), manifest)
	_, loaded, err := LoadEffectiveRenderStyleContract(st, identity.Chapter, identity.PlanDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.StyleContract, receipt.StyleContract) {
		t.Fatalf("reloaded canonical style bytes drifted\nwant=%s\n got=%s", receipt.StyleContract, loaded.StyleContract)
	}

	// Once the candidate has a receipt, current asset drift cannot silently
	// change either Drafter or Editor input for the same candidate identity.
	again, err := PublishEffectiveRenderStyleContract(
		st,
		identity,
		"changed-current-style",
		"# changed\n- 叙述声音：完全不同。",
	)
	if err != nil {
		t.Fatal(err)
	}
	if again.ReceiptDigest != receipt.ReceiptDigest || !bytes.Equal(again.StyleContract, receipt.StyleContract) {
		t.Fatal("same candidate rebuilt its effective style from current assets")
	}

	before, _, err := LoadFrozenDraftRenderContext(st, identity.Chapter, identity.PlanDigest)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyEffectiveRenderStyleContract(before, st, identity.Chapter, identity.PlanDigest)
	if err != nil {
		t.Fatal(err)
	}
	style, err := ExtractRenderStyleContract(applied)
	if err != nil {
		t.Fatal(err)
	}
	styleRaw, _ := json.Marshal(style)
	if !bytes.Equal(styleRaw, receipt.StyleContract) {
		t.Fatalf("provider-facing style differs from receipt\nwant=%s\n got=%s", receipt.StyleContract, styleRaw)
	}
}

func TestRecoverEffectiveRenderStyleArchiveFromCurrentRequiresExactCanonicalReceipt(t *testing.T) {
	t.Run("rebuild exact archive", func(t *testing.T) {
		st, identity, _ := effectiveRenderStyleTestFixture(t)
		receipt, err := PublishEffectiveRenderStyleContract(st, identity, "realist", "# 现实主义\n- 叙述声音：克制具体。")
		if err != nil {
			t.Fatal(err)
		}
		archiveRel, err := EffectiveRenderStyleContractArchivePath(
			receipt.Chapter, receipt.CandidateID, receipt.ReceiptDigest,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(st.Dir(), filepath.FromSlash(archiveRel))); err != nil {
			t.Fatal(err)
		}
		recovered, recoveredRel, err := RecoverEffectiveRenderStyleContractArchiveFromCurrent(st, identity)
		if err != nil || recovered == nil || recovered.ReceiptDigest != receipt.ReceiptDigest || recoveredRel != archiveRel {
			t.Fatalf("current-only archive recovery failed: receipt=%+v rel=%q err=%v", recovered, recoveredRel, err)
		}
		if _, err := os.Lstat(filepath.Join(st.Dir(), filepath.FromSlash(archiveRel))); err != nil {
			t.Fatalf("recovered archive is absent: %v", err)
		}
	})

	t.Run("non-canonical current fails closed", func(t *testing.T) {
		st, identity, _ := effectiveRenderStyleTestFixture(t)
		receipt, err := PublishEffectiveRenderStyleContract(st, identity, "realist", "# 现实主义\n- 句法：压力处缩短。")
		if err != nil {
			t.Fatal(err)
		}
		archiveRel, err := EffectiveRenderStyleContractArchivePath(
			receipt.Chapter, receipt.CandidateID, receipt.ReceiptDigest,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(st.Dir(), filepath.FromSlash(archiveRel))); err != nil {
			t.Fatal(err)
		}
		currentPath := filepath.Join(st.Dir(), filepath.FromSlash(EffectiveRenderStyleContractPath))
		raw, err := os.ReadFile(currentPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(currentPath, append(raw, ' '), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := RecoverEffectiveRenderStyleContractArchiveFromCurrent(st, identity); err == nil ||
			!strings.Contains(err.Error(), "non-canonical") {
			t.Fatalf("non-canonical current receipt rebuilt an archive: %v", err)
		}
	})
}

func TestEffectiveRenderStyleReceiptFailsClosedWhenBoundReceiptIsMissingOrSourceDrifts(t *testing.T) {
	t.Run("missing current pointer restores only the immutable archive", func(t *testing.T) {
		st, identity, manifest := effectiveRenderStyleTestFixture(t)
		receipt, err := PublishEffectiveRenderStyleContract(
			st,
			identity,
			"realist",
			"# realistic\n- 叙述距离：贴近视角人物当下感知。",
		)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Version = effectiveStyleCandidateVersion
		manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
		effectiveRenderStyleWriteJSON(t, filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"), manifest)
		if err := os.Remove(filepath.Join(st.Dir(), filepath.FromSlash(EffectiveRenderStyleContractPath))); err != nil {
			t.Fatal(err)
		}
		restored, err := PublishEffectiveRenderStyleContract(
			st,
			identity,
			"changed",
			"# changed\n- 叙述距离：重新编译。",
		)
		if err != nil || restored.ReceiptDigest != receipt.ReceiptDigest ||
			!bytes.Equal(restored.StyleContract, receipt.StyleContract) {
			t.Fatalf("missing current pointer did not restore exact archived receipt: restored=%+v err=%v", restored, err)
		}
		archiveRel, err := EffectiveRenderStyleContractArchivePath(
			identity.Chapter,
			identity.CandidateID,
			receipt.ReceiptDigest,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(st.Dir(), filepath.FromSlash(EffectiveRenderStyleContractPath))); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(st.Dir(), filepath.FromSlash(archiveRel))); err != nil {
			t.Fatal(err)
		}
		if _, err := PublishEffectiveRenderStyleContract(
			st,
			identity,
			"changed-again",
			"# changed again\n- 叙述距离：重新编译。",
		); err == nil || !strings.Contains(err.Error(), "archive is unavailable") {
			t.Fatalf("missing bound receipt and archive was not fail-closed: %v", err)
		}
		if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
			Mode: domain.PipelineExecutionRender, TargetChapter: identity.Chapter,
			PlanDigest: identity.PlanDigest, Owner: "missing-style-receipt-test",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := NewContextTool(st, References{}, "changed").Execute(
			context.Background(),
			json.RawMessage(`{"chapter":6,"profile":"draft"}`),
		); err == nil || !strings.Contains(err.Error(), "style receipt") {
			t.Fatalf("Drafter context fell back after bound receipt deletion: %v", err)
		}
	})

	t.Run("accepted source body hash drift invalidates receipt", func(t *testing.T) {
		st, identity, manifest := effectiveRenderStyleTestFixture(t)
		receipt, err := PublishEffectiveRenderStyleContract(
			st,
			identity,
			"realist",
			"# realistic\n- 节奏：压力处缩短句子。",
		)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Version = effectiveStyleCandidateVersion
		manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
		effectiveRenderStyleWriteJSON(t, filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"), manifest)
		if err := st.Drafts.SaveFinalChapter(3, "第三章 被篡改的已验收正文"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadEffectiveRenderStyleContract(st, identity.Chapter, identity.PlanDigest); err == nil ||
			!strings.Contains(err.Error(), "source chapter 3 drift") {
			t.Fatalf("serial-memory source drift was not rejected: %v", err)
		}
	})

	t.Run("authoritative completed chapter set drift invalidates receipt", func(t *testing.T) {
		st, identity, manifest := effectiveRenderStyleTestFixture(t)
		receipt, err := PublishEffectiveRenderStyleContract(
			st,
			identity,
			"realist",
			"# realistic\n- 节奏：压力处缩短句子。",
		)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Version = effectiveStyleCandidateVersion
		manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
		effectiveRenderStyleWriteJSON(t, filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"), manifest)
		progress, err := st.Progress.Load()
		if err != nil {
			t.Fatal(err)
		}
		progress.CompletedChapters = []int{1, 2, 3, 4}
		if err := st.Progress.Save(progress); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadEffectiveRenderStyleContract(st, identity.Chapter, identity.PlanDigest); err == nil ||
			!strings.Contains(err.Error(), "completed chapter set drift") {
			t.Fatalf("completed-set drift was not rejected: %v", err)
		}
	})

	t.Run("canonical stopword drift invalidates receipt", func(t *testing.T) {
		st, identity, manifest := effectiveRenderStyleTestFixture(t)
		receipt, err := PublishEffectiveRenderStyleContract(
			st,
			identity,
			"realist",
			"# realistic\n- 节奏：压力处缩短句子。",
		)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Version = effectiveStyleCandidateVersion
		manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
		effectiveRenderStyleWriteJSON(t, filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"), manifest)
		if err := st.Characters.Save([]domain.Character{{
			Name: "沈岚", Aliases: []string{"阿岚"}, Role: "主角",
		}}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadEffectiveRenderStyleContract(st, identity.Chapter, identity.PlanDigest); err == nil ||
			!strings.Contains(err.Error(), "stopword drift") {
			t.Fatalf("stopword drift was not rejected: %v", err)
		}
	})

	t.Run("compiler root cannot be self-redigested around its bound inputs", func(t *testing.T) {
		st, identity, manifest := effectiveRenderStyleTestFixture(t)
		receipt, err := PublishEffectiveRenderStyleContract(
			st,
			identity,
			"realist",
			"# realistic\n- 节奏：压力处缩短句子。",
		)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Version = effectiveStyleCandidateVersion
		manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
		effectiveRenderStyleWriteJSON(t, filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"), manifest)
		receipt.SerialMemoryCompilerRoot = "sha256:" + strings.Repeat("9", 64)
		receipt.ReceiptDigest = effectiveRenderStyleReceiptDigest(*receipt)
		effectiveRenderStyleWriteReceipt(t, st, *receipt)
		if _, _, err := LoadEffectiveRenderStyleContract(st, identity.Chapter, identity.PlanDigest); err == nil ||
			!strings.Contains(err.Error(), "compiler root") {
			t.Fatalf("self-redigested compiler-root tamper was not rejected: %v", err)
		}
	})
}

func TestBoundArchivedEffectiveStyleSurvivesExpectedPostCommitMutableState(t *testing.T) {
	st, identity, manifest := effectiveRenderStyleTestFixture(t)
	receipt, err := PublishEffectiveRenderStyleContract(
		st,
		identity,
		"realist",
		"# realistic\n- 叙述声音：克制具体。",
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = effectiveStyleCandidateVersion
	manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
	effectiveRenderStyleWriteJSON(t, filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"), manifest)

	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	progress.CompletedChapters = append(progress.CompletedChapters, identity.Chapter)
	if err := st.Progress.Save(progress); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "新出场者", Role: "配角"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadEffectiveRenderStyleContract(st, identity.Chapter, identity.PlanDigest); err == nil {
		t.Fatal("provider-time loader accepted post-commit mutable serial inputs")
	}
	contract, archived, rel, err := LoadBoundArchivedEffectiveRenderStyleContract(
		st, identity.Chapter, identity.PlanDigest,
	)
	if err != nil {
		t.Fatalf("post-commit bound archive was rejected: %v", err)
	}
	if archived == nil || archived.ReceiptDigest != receipt.ReceiptDigest || rel == "" || len(contract) == 0 {
		t.Fatalf("bound archive identity was incomplete: receipt=%+v path=%q contract=%v", archived, rel, contract)
	}
}

func TestArchivedEffectiveStyleRejectsSymlinkAncestor(t *testing.T) {
	st, identity, manifest := effectiveRenderStyleTestFixture(t)
	receipt, err := PublishEffectiveRenderStyleContract(
		st, identity, "realist", "# realistic\n- 叙述声音：克制。",
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = effectiveStyleCandidateVersion
	manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
	effectiveRenderStyleWriteJSON(t, filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json"), manifest)
	rel, err := EffectiveRenderStyleContractArchivePath(
		identity.Chapter, identity.CandidateID, receipt.ReceiptDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(st.Dir(), filepath.FromSlash(rel))
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	candidateArchiveDir := filepath.Dir(abs)
	externalDir := filepath.Join(t.TempDir(), "external-candidate-archive")
	if err := os.MkdirAll(externalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(externalDir, filepath.Base(abs)), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(candidateArchiveDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalDir, candidateArchiveDir); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadArchivedEffectiveRenderStyleContract(
		st, rel, identity.Chapter, receipt.ReceiptDigest,
	); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked archive ancestor was not rejected: %v", err)
	}
}
