package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestDraftChapterRejectsUnfinishedPendingRewrite(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 80); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	for ch := 1; ch <= 58; ch++ {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}

	p, _ := s.Progress.Load()
	p.Flow = domain.FlowPolishing
	p.PendingRewrites = []int{65}
	if err := s.Progress.Save(p); err != nil {
		t.Fatalf("Save corrupt progress: %v", err)
	}

	tool := NewDraftChapterTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 65,
		"content": "错误写入未来章节。",
		"mode":    "write",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "pending_rewrites 只能包含已完成章节") {
		t.Fatalf("expected invalid pending_rewrites rejection, got %v", err)
	}
	progress, _ := s.Progress.Load()
	if progress.InProgressChapter == 65 {
		t.Fatalf("future chapter should not become in progress")
	}
}

func TestDraftChapterRejectsUnexpandedLayeredChapter(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 5); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "第一弧",
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "一"},
				{Chapter: 2, Title: "二"},
			},
		}, {
			Index:             2,
			Title:             "第二弧",
			EstimatedChapters: 3,
		}},
	}}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}

	tool := NewDraftChapterTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"content": "越界正文。",
		"mode":    "write",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "expand_arc") {
		t.Fatalf("expected unexpanded chapter rejection, got %v", err)
	}
	progress, _ := s.Progress.Load()
	if progress.InProgressChapter == 3 {
		t.Fatalf("unexpanded chapter should not become in progress")
	}
}

func TestDraftChapterRejectsConsecutiveDraftWithoutConsistencyCheck(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}

	tool := NewDraftChapterTool(s)
	first, err := json.Marshal(map[string]any{
		"chapter": 1,
		"content": "第一版草稿。",
		"mode":    "write",
	})
	if err != nil {
		t.Fatalf("Marshal first: %v", err)
	}
	if _, err := tool.Execute(context.Background(), first); err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	second, err := json.Marshal(map[string]any{
		"chapter": 1,
		"content": "第二版草稿。",
		"mode":    "write",
	})
	if err != nil {
		t.Fatalf("Marshal second: %v", err)
	}
	if _, err := tool.Execute(context.Background(), second); err == nil || !strings.Contains(err.Error(), "禁止连续 draft_chapter") {
		t.Fatalf("expected consecutive draft rejection, got %v", err)
	}
}

func TestDraftChapterPersistsButFlagsWordContractFailure(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{Structured: rules.Structured{
		ChapterWords: &rules.WordRange{Min: 10, Max: 20},
	}}); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("长", 25)
	args, _ := json.Marshal(map[string]any{"chapter": 1, "content": content, "mode": "write"})
	raw, err := NewDraftChapterTool(s).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result["hard_gate_passed"] != false || !strings.Contains(result["next_step"].(string), "edit_chapter") {
		t.Fatalf("expected hard gate guidance, got %s", raw)
	}
	if draft, _ := s.Drafts.LoadDraft(1); draft != content {
		t.Fatalf("out-of-range draft should remain editable, got %q", draft)
	}
}

func TestDraftChapterRejectsSecondAlgorithmCrossProjectContamination(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("她的第二算法", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "许闻溪", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}

	tool := NewDraftChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"content": "第一章\n\n江烬收到欠费单，鬼城的门牌亮了一下。",
		"mode":    "write",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "跨项目污染") {
		t.Fatalf("expected cross-project contamination rejection, got %v", err)
	}
	if draft, _ := s.Drafts.LoadDraft(1); draft != "" {
		t.Fatalf("contaminated draft must not persist: %s", draft)
	}
}
