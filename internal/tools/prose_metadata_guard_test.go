package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	qualityrules "github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestDraftChapterPrewriteRejectsSimulationIDMetadata(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"chapter": 1,
		"mode":    "write",
		"content": "第一章 山风\n\n【simulation_id：ch001-aeae823879cc】\n\n风从门缝里钻进来。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), args); err == nil ||
		!strings.Contains(err.Error(), "simulation_id") || !strings.Contains(err.Error(), "内部 orchestration") {
		t.Fatalf("simulation_id crossed the draft prewrite boundary: %v", err)
	}
	if draft, err := st.Drafts.LoadDraft(1); err != nil || strings.TrimSpace(draft) != "" {
		t.Fatalf("rejected metadata leak wrote a draft: draft=%q err=%v", draft, err)
	}
}

func TestOrchestrationMetadataLeakIsDeterministicMechanicalFailure(t *testing.T) {
	fields := []string{
		"world_simulation_id:sim-1",
		"craft_recall_receipt:receipt-1",
		"render_packet=v7",
		"rewrite_source:chapters/01.md",
		"plan_details=true",
		"chapter_world_simulation:ready",
		"source_refs:[craft-1]",
		"checkpoint:469",
		"body_sha256:abcdef",
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			violations := qualityrules.Lint("第一章 山风\n\n【" + field + "】")
			var got *qualityrules.Violation
			for i := range violations {
				if violations[i].Rule == qualityrules.OrchestrationMetadataLeakRule {
					got = &violations[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("metadata field did not reach mechanical lint: %q", field)
			}
			if !reviewreport.IsBlockingMechanicalViolation(*got) ||
				!reviewreport.IsDeterministicMechanicalViolation(*got) {
				t.Fatalf("metadata leak was demotable: %+v", *got)
			}
		})
	}
}

func TestCommitChapterRejectsMetadataBeforeFinalWrite(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	body := "第一章 山风\n\n【body_sha256：0123456789abcdef】\n\n他把窗关上。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "主角关窗。",
		"characters":              []string{"主角", "配角"},
		"key_events":              []string{"关窗"},
		"character_stage_records": testCharacterStageRecords("主角", "配角"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCommitChapterTool(st).Execute(context.Background(), args); err == nil ||
		!strings.Contains(err.Error(), "终稿完整性门禁") || !strings.Contains(err.Error(), "body_sha256") {
		t.Fatalf("commit accepted leaked metadata: %v", err)
	}
	if final, _ := st.Drafts.LoadChapterText(1); strings.TrimSpace(final) != "" {
		t.Fatalf("metadata leak reached final chapter: %q", final)
	}
	progress, _ := st.Progress.Load()
	if len(progress.CompletedChapters) != 0 {
		t.Fatalf("metadata leak advanced progress: %+v", progress.CompletedChapters)
	}
}

func TestProseMetadataGuardAllowsOrdinarySystemPanel(t *testing.T) {
	body := "第一章 余额\n\n【系统提示：余额不足，请换一张卡。】\n\n他把旧卡塞回钱包。"
	if err := validateFictionProseMetadataFree(body); err != nil {
		t.Fatalf("ordinary fictional system panel was rejected: %v", err)
	}
	if err := validateDraftProsePayload(body); err != nil {
		t.Fatalf("ordinary fictional system panel failed draft prose validation: %v", err)
	}
}
