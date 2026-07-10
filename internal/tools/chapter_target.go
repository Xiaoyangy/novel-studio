package tools

import "github.com/chenhongyang/novel-studio/internal/store"

// pendingRewriteTarget returns the only chapter that writing tools may advance
// while a rewrite queue is active. The queue is ordered and must be drained
// before planning a new chapter.
func pendingRewriteTarget(s *store.Store) (int, bool) {
	if s == nil {
		return 0, false
	}
	progress, err := s.Progress.Load()
	if err != nil || progress == nil || len(progress.PendingRewrites) == 0 {
		return 0, false
	}
	chapter := progress.PendingRewrites[0]
	return chapter, chapter > 0
}
