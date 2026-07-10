package main

import (
	"sort"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func syncCompletedChapterWordCounts(st *store.Store) (int, error) {
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return 0, err
	}
	chapters := append([]int(nil), progress.CompletedChapters...)
	sort.Ints(chapters)

	updated := 0
	for _, chapter := range chapters {
		if chapter <= 0 {
			continue
		}
		text, err := st.Drafts.LoadChapterText(chapter)
		if err != nil {
			return updated, err
		}
		if text == "" {
			continue
		}
		changed, err := syncProgressChapterWordCount(st, chapter, domain.WordCount(text))
		if err != nil {
			return updated, err
		}
		if changed {
			updated++
		}
	}
	return updated, nil
}

func syncRewriteProgressWordCount(st *store.Store, chapter, wordCount int) error {
	_, err := syncProgressChapterWordCount(st, chapter, wordCount)
	return err
}

func syncProgressChapterWordCount(st *store.Store, chapter, wordCount int) (bool, error) {
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return false, err
	}
	if !intSliceContains(progress.CompletedChapters, chapter) {
		return false, nil
	}
	if progress.ChapterWordCounts == nil {
		progress.ChapterWordCounts = make(map[int]int)
	}
	wordCountChanged := progress.ChapterWordCounts[chapter] != wordCount
	staleInProgress := progress.InProgressChapter == chapter && progress.CurrentChapter > chapter
	if !wordCountChanged && !staleInProgress {
		return false, nil
	}
	if wordCountChanged {
		progress.ChapterWordCounts[chapter] = wordCount
		total := 0
		for _, count := range progress.ChapterWordCounts {
			total += count
		}
		progress.TotalWordCount = total
	}
	if staleInProgress {
		progress.InProgressChapter = 0
	}
	if err := st.Progress.Save(progress); err != nil {
		return false, err
	}
	return true, nil
}

func intSliceContains(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
