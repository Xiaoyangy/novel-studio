package store

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// AIVoiceStore 管理反 AI 腔指标、红旗和采样记录。
type AIVoiceStore struct{ io *IO }

func NewAIVoiceStore(io *IO) *AIVoiceStore { return &AIVoiceStore{io: io} }

func (s *AIVoiceStore) SaveChapterMetrics(metrics domain.ChapterAIVoiceMetrics, draft bool) error {
	rel := fmt.Sprintf("meta/chapter_metrics/%02d.json", metrics.Chapter)
	if draft {
		rel = fmt.Sprintf("meta/chapter_metrics/%02d.draft.json", metrics.Chapter)
	}
	return s.io.WriteJSON(rel, metrics)
}

func (s *AIVoiceStore) LoadChapterMetrics(chapter int) (*domain.ChapterAIVoiceMetrics, error) {
	var metrics domain.ChapterAIVoiceMetrics
	if err := s.io.ReadJSON(fmt.Sprintf("meta/chapter_metrics/%02d.json", chapter), &metrics); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &metrics, nil
}

func (s *AIVoiceStore) LoadAllChapterMetrics() ([]domain.ChapterAIVoiceMetrics, error) {
	base := s.io.path("meta/chapter_metrics")
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []domain.ChapterAIVoiceMetrics
	for _, entry := range entries {
		if entry.IsDir() || len(entry.Name()) != len("00.json") {
			continue
		}
		var metrics domain.ChapterAIVoiceMetrics
		if err := s.io.ReadJSON("meta/chapter_metrics/"+entry.Name(), &metrics); err != nil {
			return nil, err
		}
		out = append(out, metrics)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Chapter < out[j].Chapter })
	return out, nil
}

func (s *AIVoiceStore) SaveRedFlags(analysis domain.AIVoiceAnalysis) error {
	return s.io.WriteJSON(fmt.Sprintf("reviews/%02d_ai_voice_redflags.json", analysis.Chapter), analysis)
}

func (s *AIVoiceStore) LoadRedFlags(chapter int) (*domain.AIVoiceAnalysis, error) {
	var analysis domain.AIVoiceAnalysis
	if err := s.io.ReadJSON(fmt.Sprintf("reviews/%02d_ai_voice_redflags.json", chapter), &analysis); err != nil {
		if os.IsNotExist(err) {
			if legacyErr := s.io.ReadJSON(fmt.Sprintf("reviews_ai/%02d_ai_voice_redflags.json", chapter), &analysis); legacyErr != nil {
				if os.IsNotExist(legacyErr) {
					return nil, nil
				}
				return nil, legacyErr
			}
			return &analysis, nil
		}
		return nil, err
	}
	return &analysis, nil
}

func (s *AIVoiceStore) SaveSamplingRecord(record domain.SamplingRecord) error {
	if record.GeneratedAt == "" {
		record.GeneratedAt = time.Now().Format(time.RFC3339)
	}
	return s.io.WriteJSON(fmt.Sprintf("meta/sampling/%02d.json", record.Chapter), record)
}

func (s *AIVoiceStore) AppendScore(chapter int, source string, score float64) error {
	metrics, err := s.LoadChapterMetrics(chapter)
	if err != nil || metrics == nil {
		return err
	}
	metrics.AIVoiceScoreHistory = append(metrics.AIVoiceScoreHistory, domain.AIVoiceScorePoint{
		Round:  metrics.RevisionRound,
		Source: source,
		Score:  score,
		At:     time.Now().Format(time.RFC3339),
	})
	if source == "editor" {
		metrics.AIVoiceScore = score
	}
	return s.SaveChapterMetrics(*metrics, false)
}
