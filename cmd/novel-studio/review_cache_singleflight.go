package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	reviewCacheLockPollInterval = 25 * time.Millisecond
	reviewCacheDefaultLockWait  = 3 * time.Minute
)

// reviewCacheLockTimeoutError is deliberately typed so callers and diagnostics
// can distinguish provider latency from time spent waiting for an identical
// exact-body request that is already running in another process.
type reviewCacheLockTimeoutError struct {
	Branch string
	Key    string
	Waited time.Duration
}

func (e *reviewCacheLockTimeoutError) Error() string {
	return fmt.Sprintf("等待 %s 精确正文缓存事务超时（key=%s, waited=%s）", e.Branch, shortReviewCacheKey(e.Key), e.Waited.Round(time.Millisecond))
}

// acquireReviewCacheKeyLock serializes one exact request identity across both
// goroutines and OS processes. The kernel releases flock after a crash. Waiting
// is bounded by the branch wall-clock budget, so a dead or slow peer cannot turn
// cache coalescing into an unbounded pipeline hang.
func acquireReviewCacheKeyLock(projectDir, branch, key string, budget time.Duration) (func() error, error) {
	if strings.TrimSpace(projectDir) == "" {
		return nil, fmt.Errorf("review cache project dir 为空")
	}
	if branch == "" || filepath.Base(branch) != branch {
		return nil, fmt.Errorf("非法 review cache branch %q", branch)
	}
	if len(key) != 64 {
		return nil, fmt.Errorf("非法 review cache key %q", key)
	}
	for _, r := range key {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return nil, fmt.Errorf("非法 review cache key %q", key)
		}
	}

	lockDir := filepath.Join(projectDir, "reviews", reviewExistingCacheDirectoryName, ".locks", branch)
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建 review cache lock 目录: %w", err)
	}
	lockPath := filepath.Join(lockDir, key+".lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开 review cache lock: %w", err)
	}

	waitLimit := budget
	if waitLimit <= 0 {
		waitLimit = reviewCacheDefaultLockWait
	}
	started := time.Now()
	deadline := started.Add(waitLimit)
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			var once sync.Once
			var releaseErr error
			return func() error {
				once.Do(func() {
					if unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); unlockErr != nil {
						releaseErr = fmt.Errorf("释放 review cache lock: %w", unlockErr)
					}
					if closeErr := file.Close(); closeErr != nil && releaseErr == nil {
						releaseErr = fmt.Errorf("关闭 review cache lock: %w", closeErr)
					}
				})
				return releaseErr
			}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = file.Close()
			return nil, fmt.Errorf("获取 review cache lock: %w", err)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			_ = file.Close()
			return nil, &reviewCacheLockTimeoutError{Branch: branch, Key: key, Waited: time.Since(started)}
		}
		pause := reviewCacheLockPollInterval
		if remaining < pause {
			pause = remaining
		}
		timer := time.NewTimer(pause)
		<-timer.C
	}
}

func remainingReviewCacheBudget(started time.Time, budget time.Duration) (time.Duration, error) {
	if budget <= 0 {
		return budget, nil
	}
	remaining := budget - time.Since(started)
	if remaining <= 0 {
		return 0, fmt.Errorf("精确正文缓存事务已耗尽 %s 墙钟预算", budget)
	}
	return remaining, nil
}
