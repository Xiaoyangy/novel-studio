package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestAIVoiceStorePersistsMetricsRedFlagsAndSampling(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	metrics := domain.ChapterAIVoiceMetrics{
		Chapter:           3,
		FigurativeDensity: 0.2,
		DialogueRatio:     0.35,
		AIVoiceScore:      0.18,
		ChapterFunction:   "互动",
	}
	if err := s.AIVoice.SaveChapterMetrics(metrics, false); err != nil {
		t.Fatalf("SaveChapterMetrics: %v", err)
	}
	loaded, err := s.AIVoice.LoadChapterMetrics(3)
	if err != nil || loaded == nil {
		t.Fatalf("LoadChapterMetrics: %v", err)
	}
	if loaded.ChapterFunction != "互动" {
		t.Fatalf("unexpected metrics: %+v", loaded)
	}
	analysis := domain.AIVoiceAnalysis{Chapter: 3, Label: "✅ 可通过", Metrics: metrics}
	if err := s.AIVoice.SaveRedFlags(analysis); err != nil {
		t.Fatalf("SaveRedFlags: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reviews", "03_ai_voice_redflags.json")); err != nil {
		t.Fatalf("expected redflags file: %v", err)
	}
	if err := s.AIVoice.SaveSamplingRecord(domain.SamplingRecord{Chapter: 3, SelectedIndex: 2}); err != nil {
		t.Fatalf("SaveSamplingRecord: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta", "sampling", "03.json")); err != nil {
		t.Fatalf("expected sampling file: %v", err)
	}
}

func TestAIVoiceStoreLoadsLegacyReviewsAIFallback(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	legacyDir := filepath.Join(dir, "reviews_ai")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy reviews_ai: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "04_ai_voice_redflags.json"), []byte(`{"chapter":4,"label":"legacy"}`), 0o644); err != nil {
		t.Fatalf("write legacy redflags: %v", err)
	}
	analysis, err := s.AIVoice.LoadRedFlags(4)
	if err != nil || analysis == nil {
		t.Fatalf("LoadRedFlags legacy: %v", err)
	}
	if analysis.Label != "legacy" {
		t.Fatalf("unexpected legacy analysis: %+v", analysis)
	}
}
