package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/store"
)

// TestCommitChapterNormalPathWritesAllMetadata 实证 normal commit（非 rewrite 路径）
// 三类元数据全部落盘：meta/chapter_metrics/NN.json、meta/chapter_world_deltas/、
// meta/character_stage/。守住「rewrite 与 normal 两条路径产物一致」的设计意图。
func TestCommitChapterNormalPathWritesAllMetadata(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "许闻溪核对药盒订单，邱梅在门外等结果。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "许闻溪保全药盒订单证据。",
		"characters":              []string{"许闻溪", "邱梅"},
		"key_events":              []string{"保全药盒订单"},
		"character_stage_records": testCharacterStageRecords("许闻溪", "邱梅"),
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if metrics, err := s.AIVoice.LoadChapterMetrics(1); err != nil || metrics == nil {
		t.Errorf("normal commit 未写 chapter_metrics: err=%v metrics=%v", err, metrics)
	}
	if recs, err := s.LoadCharacterStageRecords(1); err != nil || len(recs) == 0 {
		t.Errorf("normal commit 未写 character_stage: err=%v len=%d", err, len(recs))
	}
	if delta, err := s.LoadChapterWorldDelta(1); err != nil || delta == nil {
		t.Errorf("normal commit 未写 chapter_world_deltas: err=%v delta=%v", err, delta)
	}
}
