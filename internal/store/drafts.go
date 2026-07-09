package store

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// DraftStore 管理章节构思、草稿和终稿。
type DraftStore struct{ io *IO }

func NewDraftStore(io *IO) *DraftStore { return &DraftStore{io: io} }

// SaveChapterPlan 保存章节构思到 drafts/{ch}.plan.json。
func (s *DraftStore) SaveChapterPlan(plan domain.ChapterPlan) error {
	return s.io.WriteJSON(fmt.Sprintf("drafts/%02d.plan.json", plan.Chapter), plan)
}

// LoadChapterPlan 读取章节构思。
func (s *DraftStore) LoadChapterPlan(chapter int) (*domain.ChapterPlan, error) {
	var plan domain.ChapterPlan
	if err := s.io.ReadJSON(fmt.Sprintf("drafts/%02d.plan.json", chapter), &plan); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &plan, nil
}

// SaveChapterPlanConsistencyWarnings 落盘 plan 一致性检查的软疑点，供 drafter
// 在正文阶段（check_consistency）核对，确保正文不越出计划范围。
func (s *DraftStore) SaveChapterPlanConsistencyWarnings(chapter int, warnings []string) error {
	return s.io.WriteJSON(fmt.Sprintf("drafts/%02d.plan_consistency.json", chapter), warnings)
}

// LoadChapterPlanConsistencyWarnings 读取 plan 一致性软疑点；不存在时返回 nil。
func (s *DraftStore) LoadChapterPlanConsistencyWarnings(chapter int) ([]string, error) {
	var warnings []string
	if err := s.io.ReadJSON(fmt.Sprintf("drafts/%02d.plan_consistency.json", chapter), &warnings); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return warnings, nil
}

// SaveChapterPlanPartial 保存两阶段规划的中间态到 drafts/{ch}.plan.partial.json。
// plan_details finalize 通过后由 DeleteChapterPlanPartial 清理。
func (s *DraftStore) SaveChapterPlanPartial(chapter int, partial map[string]any) error {
	return s.io.WriteJSON(fmt.Sprintf("drafts/%02d.plan.partial.json", chapter), partial)
}

// LoadChapterPlanPartial 读取两阶段规划中间态；不存在时返回 nil。
func (s *DraftStore) LoadChapterPlanPartial(chapter int) (map[string]any, error) {
	var partial map[string]any
	if err := s.io.ReadJSON(fmt.Sprintf("drafts/%02d.plan.partial.json", chapter), &partial); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return partial, nil
}

// DeleteChapterPlanPartial 删除两阶段规划中间态；文件不存在视为成功（RemoveFile 已吞 not-exist）。
func (s *DraftStore) DeleteChapterPlanPartial(chapter int) error {
	return s.io.RemoveFile(fmt.Sprintf("drafts/%02d.plan.partial.json", chapter))
}

// SaveDraft 保存整章草稿到 drafts/{ch}.draft.md。
func (s *DraftStore) SaveDraft(chapter int, content string) error {
	return s.io.WriteMarkdown(fmt.Sprintf("drafts/%02d.draft.md", chapter), content)
}

// AppendDraft 追加内容到现有草稿（续写模式）。
func (s *DraftStore) AppendDraft(chapter int, content string) error {
	rel := fmt.Sprintf("drafts/%02d.draft.md", chapter)
	return s.io.WithWriteLock(func() error {
		existing, err := s.io.ReadFileUnlocked(rel)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		var merged string
		if len(existing) > 0 {
			merged = string(existing) + "\n\n" + content
		} else {
			merged = content
		}
		return s.io.WriteFileUnlocked(rel, []byte(merged))
	})
}

// LoadDraft 读取整章草稿。
func (s *DraftStore) LoadDraft(chapter int) (string, error) {
	data, err := s.io.ReadFile(fmt.Sprintf("drafts/%02d.draft.md", chapter))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// LoadChapterContent 加载章节草稿正文及字数。
func (s *DraftStore) LoadChapterContent(chapter int) (string, int, error) {
	draft, err := s.LoadDraft(chapter)
	if err != nil {
		return "", 0, err
	}
	if draft != "" {
		return draft, utf8.RuneCountInString(draft), nil
	}
	return "", 0, nil
}

func draftPartIndexPath(chapter int) string {
	return fmt.Sprintf("drafts/%02d.parts/index.json", chapter)
}

func draftPartPath(chapter, part int) string {
	return fmt.Sprintf("drafts/%02d.parts/part-%02d.md", chapter, part)
}

// SaveDraftPart 保存章节正文分片，并更新 drafts/NN.parts/index.json。
// 分片草稿用于长章/高上下文压力场景；最终仍需 MergeDraftParts 后写整章草稿。
func (s *DraftStore) SaveDraftPart(chapter, part, totalParts int, title, focus, content string) (*domain.ChapterDraftPartIndex, domain.ChapterDraftPart, error) {
	if totalParts < part {
		totalParts = part
	}
	now := time.Now().Format(time.RFC3339)
	var idx *domain.ChapterDraftPartIndex
	var item domain.ChapterDraftPart
	err := s.io.WithWriteLock(func() error {
		loaded, err := s.loadDraftPartIndexUnlocked(chapter)
		if err != nil {
			return err
		}
		if loaded == nil {
			loaded = &domain.ChapterDraftPartIndex{Version: 1, Chapter: chapter}
		}
		loaded.Version = 1
		loaded.Chapter = chapter
		loaded.UpdatedAt = now
		path := draftPartPath(chapter, part)
		item = domain.ChapterDraftPart{
			Part:        part,
			TotalParts:  totalParts,
			Title:       title,
			Focus:       focus,
			ContentPath: path,
			RuneCount:   utf8.RuneCountInString(content),
			UpdatedAt:   now,
		}
		replaced := false
		for i := range loaded.Parts {
			if loaded.Parts[i].Part == part {
				loaded.Parts[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			loaded.Parts = append(loaded.Parts, item)
		}
		sort.Slice(loaded.Parts, func(i, j int) bool { return loaded.Parts[i].Part < loaded.Parts[j].Part })
		if err := s.io.WriteFileUnlocked(path, []byte(content)); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(draftPartIndexPath(chapter), loaded); err != nil {
			return err
		}
		cp := *loaded
		cp.Parts = append([]domain.ChapterDraftPart(nil), loaded.Parts...)
		idx = &cp
		return nil
	})
	return idx, item, err
}

// LoadDraftPartIndex 读取分片草稿索引；不存在时返回 nil。
func (s *DraftStore) LoadDraftPartIndex(chapter int) (*domain.ChapterDraftPartIndex, error) {
	var idx domain.ChapterDraftPartIndex
	if err := s.io.ReadJSON(draftPartIndexPath(chapter), &idx); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(idx.Parts, func(i, j int) bool { return idx.Parts[i].Part < idx.Parts[j].Part })
	return &idx, nil
}

func (s *DraftStore) loadDraftPartIndexUnlocked(chapter int) (*domain.ChapterDraftPartIndex, error) {
	var idx domain.ChapterDraftPartIndex
	if err := s.io.ReadJSONUnlocked(draftPartIndexPath(chapter), &idx); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(idx.Parts, func(i, j int) bool { return idx.Parts[i].Part < idx.Parts[j].Part })
	return &idx, nil
}

// LoadDraftPartContent 读取指定正文分片。
func (s *DraftStore) LoadDraftPartContent(chapter, part int) (string, error) {
	data, err := s.io.ReadFile(draftPartPath(chapter, part))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// MergeDraftParts 按 index 顺序合并正文分片。调用方负责写入整章草稿。
func (s *DraftStore) MergeDraftParts(chapter, expectedParts int) (string, *domain.ChapterDraftPartIndex, []int, error) {
	idx, err := s.LoadDraftPartIndex(chapter)
	if err != nil || idx == nil {
		return "", idx, nil, err
	}
	if expectedParts <= 0 {
		for _, part := range idx.Parts {
			if part.TotalParts > expectedParts {
				expectedParts = part.TotalParts
			}
			if part.Part > expectedParts {
				expectedParts = part.Part
			}
		}
	}
	byPart := make(map[int]domain.ChapterDraftPart, len(idx.Parts))
	for _, part := range idx.Parts {
		byPart[part.Part] = part
	}
	var missing []int
	var chunks []string
	for part := 1; part <= expectedParts; part++ {
		if _, ok := byPart[part]; !ok {
			missing = append(missing, part)
			continue
		}
		content, err := s.LoadDraftPartContent(chapter, part)
		if err != nil {
			return "", idx, missing, err
		}
		if strings.TrimSpace(content) == "" {
			missing = append(missing, part)
			continue
		}
		chunks = append(chunks, strings.TrimSpace(content))
	}
	if len(missing) > 0 {
		return "", idx, missing, nil
	}
	return strings.Join(chunks, "\n\n"), idx, nil, nil
}

// SaveFinalChapter 保存最终章节正文到 chapters/{ch}.md。
func (s *DraftStore) SaveFinalChapter(chapter int, content string) error {
	return s.io.WriteMarkdown(fmt.Sprintf("chapters/%02d.md", chapter), content)
}

// SaveMergedManuscript 保存短篇/单卷项目的合并正文。
func (s *DraftStore) SaveMergedManuscript(content string) error {
	return s.io.WriteMarkdown("正文.md", content)
}

// LoadChapterText 读取已提交的终稿原文。
func (s *DraftStore) LoadChapterText(chapter int) (string, error) {
	data, err := s.io.ReadFile(fmt.Sprintf("chapters/%02d.md", chapter))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// LoadChapterRange 读取指定范围的终稿原文片段。
func (s *DraftStore) LoadChapterRange(from, to, maxRunes int) (map[int]string, error) {
	result := make(map[int]string)
	for ch := from; ch <= to; ch++ {
		text, err := s.LoadChapterText(ch)
		if err != nil {
			return nil, err
		}
		if text == "" {
			continue
		}
		if maxRunes > 0 {
			runes := []rune(text)
			if len(runes) > maxRunes {
				text = string(runes[:maxRunes]) + "..."
			}
		}
		result[ch] = text
	}
	return result, nil
}

var dialogueRe = regexp.MustCompile(`"[^"]*"`)

// ExtractDialogue 从已提交章节中提取指定角色的对话片段。
// maxCompletedChapter 由调用方传入，避免跨域依赖。
func (s *DraftStore) ExtractDialogue(characterName string, aliases []string, maxSamples, maxCompletedChapter int) []string {
	if maxSamples <= 0 {
		maxSamples = 5
	}
	names := append([]string{characterName}, aliases...)

	var samples []string
	for ch := maxCompletedChapter; ch >= 1 && len(samples) < maxSamples; ch-- {
		text, err := s.LoadChapterText(ch)
		if err != nil || text == "" {
			continue
		}
		paragraphs := strings.Split(text, "\n")
		for _, para := range paragraphs {
			if len(samples) >= maxSamples {
				break
			}
			found := false
			for _, name := range names {
				if strings.Contains(para, name) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
			matches := dialogueRe.FindAllString(para, -1)
			for _, m := range matches {
				if len(samples) >= maxSamples {
					break
				}
				if utf8.RuneCountInString(m) > 5 {
					samples = append(samples, characterName+": "+m)
				}
			}
		}
	}
	return samples
}

// ExtractStyleAnchors 从已提交章节中提取代表性段落作为风格锚点。
// maxCompletedChapter 由调用方传入，避免跨域依赖。
func (s *DraftStore) ExtractStyleAnchors(maxAnchors, maxCompletedChapter int) []string {
	if maxAnchors <= 0 {
		maxAnchors = 5
	}

	var anchors []string
	for ch := 1; ch <= maxCompletedChapter && len(anchors) < maxAnchors; ch++ {
		text, err := s.LoadChapterText(ch)
		if err != nil || text == "" {
			continue
		}
		paragraphs := strings.Split(text, "\n\n")
		for _, para := range paragraphs {
			if len(anchors) >= maxAnchors {
				break
			}
			para = strings.TrimSpace(para)
			runeCount := utf8.RuneCountInString(para)
			if runeCount < 50 || runeCount > 300 {
				continue
			}
			if strings.Count(para, "\u201c") > 2 {
				continue
			}
			anchors = append(anchors, para)
		}
	}
	return anchors
}
