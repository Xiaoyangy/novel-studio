package headless

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/diag"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/entry/startup"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/logger"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

type Options struct {
	Prompt                    string
	StopAfterChapter          int
	StopAfterFoundation       bool
	StopAfterInitialWorldTick bool
	SkipQueueReplay           bool
	Stdin                     io.Reader
	Stdout                    io.Writer
	Stderr                    io.Writer
}

// Run 以无界面模式运行会话内核，直接消费 Engine 事件与流式输出。
// 未来若新增“续写已有小说”等共享启动方式，不应直接堆到这里，
// 而应先落到 internal/entry/startup，再由 headless 入口调用。
func Run(cfg bootstrap.Config, bundle assets.Bundle, opts Options) error {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	eng, err := host.New(cfg, bundle)
	if err != nil {
		return err
	}
	eng.AskUser().SetHandler(newTerminalAskUser(stdin, stderr).handle)
	cleanup := logger.SetupFile(eng.Dir(), "headless.log", false)
	defer cleanup()
	defer eng.Close()
	// 运行结束 / 出错返回时落一份脱敏诊断，方便 headless 用户贴 issue。
	// （外部 kill 的挂死不走 defer，仍需在 TUI 里手动 /diag。）
	defer func() { _, _ = diag.Export(store.NewStore(eng.Dir())) }()

	prompt := strings.TrimSpace(opts.Prompt)
	if prompt != "" {
		plan, err := startup.PrepareQuick(startup.Request{
			Mode:        startup.ModeQuick,
			UserPrompt:  prompt,
			OutputDir:   eng.Dir(),
			Interactive: true,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(stderr, "headless 启动: %s\n", eng.Dir())
		// 启动侧确定性生成本书用户规则快照（用原始 prompt 归一化），须在 StartPrepared 前。
		if err := eng.PrepareUserRules(plan.RawPrompt); err != nil {
			return err
		}
		if err := eng.StartPrepared(plan.StartPrompt); err != nil {
			return err
		}
	} else {
		roundHasContent := false
		if !opts.SkipQueueReplay {
			items, err := eng.ReplayQueue(0)
			if err != nil {
				return err
			}
			roundHasContent, err = replayQueue(items, stdout, stderr)
			if err != nil {
				return err
			}
		}
		label, err := eng.Resume()
		if err != nil {
			return err
		}
		if label == "" {
			return fmt.Errorf("headless 模式需要 --prompt，或输出目录 %q 下已有可恢复会话", eng.Dir())
		}
		fmt.Fprintf(stderr, "headless 恢复: %s (%s)\n", eng.Dir(), label)
		return consume(eng, stdout, stderr, roundHasContent, opts.StopAfterChapter, opts.StopAfterFoundation, opts.StopAfterInitialWorldTick)
	}

	return consume(eng, stdout, stderr, false, opts.StopAfterChapter, opts.StopAfterFoundation, opts.StopAfterInitialWorldTick)
}

func consume(eng *host.Host, stdout, stderr io.Writer, roundHasContent bool, stopAfterChapter int, stopAfterFoundation bool, stopAfterInitialWorldTick bool) error {
	stopRequested := false
	for {
		select {
		case ev, ok := <-eng.Events():
			if !ok {
				return nil
			}
			writeEvent(stderr, ev)
			if !stopRequested && shouldStopAfterChapter(eng.Dir(), stopAfterChapter) {
				stopRequested = true
				fmt.Fprintf(stderr, "[headless] 已完成到第 %d 章，按 --write-to 暂停写作\n", stopAfterChapter)
				eng.Abort()
			}
			if !stopRequested && stopAfterFoundation && shouldStopAfterFoundationReady(eng.Dir()) {
				stopRequested = true
				fmt.Fprintln(stderr, "[headless] Architect foundation 已齐，按 pipeline 阶段暂停")
				eng.Abort()
			}
			if !stopRequested && stopAfterInitialWorldTick && shouldStopAfterInitialWorldTickReady(eng.Dir()) {
				stopRequested = true
				fmt.Fprintln(stderr, "[headless] 初始 world_tick 已齐，按 pipeline 阶段暂停")
				eng.Abort()
			}
		case delta, ok := <-eng.Stream():
			if !ok {
				continue
			}
			if delta == host.StreamClearSentinel {
				if roundHasContent {
					if _, err := io.WriteString(stdout, "\n\n"); err != nil {
						return err
					}
					roundHasContent = false
				}
				continue
			}
			if delta == "" {
				continue
			}
			if _, err := io.WriteString(stdout, delta); err != nil {
				return err
			}
			roundHasContent = true
		case _, ok := <-eng.Done():
			if !ok {
				return nil
			}
			return drainPending(eng, stdout, stderr, roundHasContent)
		}
	}
}

func shouldStopAfterFoundationReady(dir string) bool {
	return tools.FoundationCoreComplete(dir)
}

func shouldStopAfterInitialWorldTickReady(dir string) bool {
	return tools.EnsureInitialWorldTickForChapterOne(store.NewStore(dir)) == nil
}

func shouldStopAfterChapter(dir string, chapter int) bool {
	if chapter <= 0 {
		return false
	}
	progress, err := store.NewStore(dir).Progress.Load()
	if err != nil || progress == nil {
		return false
	}
	for _, pending := range progress.PendingRewrites {
		if pending > 0 && pending <= chapter {
			return false
		}
	}
	for _, ch := range progress.CompletedChapters {
		if ch == chapter {
			return true
		}
	}
	return false
}

func drainPending(eng *host.Host, stdout, stderr io.Writer, roundHasContent bool) error {
	for {
		select {
		case ev, ok := <-eng.Events():
			if ok {
				writeEvent(stderr, ev)
			}
		case delta, ok := <-eng.Stream():
			if !ok {
				continue
			}
			if delta == host.StreamClearSentinel {
				if roundHasContent {
					if _, err := io.WriteString(stdout, "\n\n"); err != nil {
						return err
					}
					roundHasContent = false
				}
				continue
			}
			if delta != "" {
				if _, err := io.WriteString(stdout, delta); err != nil {
					return err
				}
				roundHasContent = true
			}
		default:
			if roundHasContent {
				if _, err := io.WriteString(stdout, "\n"); err != nil {
					return err
				}
			}
			return nil
		}
	}
}

func writeEvent(w io.Writer, ev host.Event) {
	if w == nil || strings.TrimSpace(ev.Summary) == "" {
		return
	}
	ts := ev.Time.Format("15:04:05")
	if ts == "00:00:00" {
		ts = "--:--:--"
	}
	fmt.Fprintf(w, "[%s] [%s] %s\n", ts, ev.Category, ev.Summary)
}

func replayQueue(items []domain.RuntimeQueueItem, stdout, stderr io.Writer) (bool, error) {
	var roundHasContent bool
	for _, item := range items {
		switch item.Kind {
		case domain.RuntimeQueueUIEvent:
			writeEvent(stderr, host.Event{
				Time:     item.Time,
				Category: item.Category,
				Summary:  item.Summary,
			})
		case domain.RuntimeQueueStreamClear:
			if roundHasContent {
				if _, err := io.WriteString(stdout, "\n\n"); err != nil {
					return roundHasContent, err
				}
				roundHasContent = false
			}
		case domain.RuntimeQueueStreamDelta:
			text := host.ReplayDeltaText(item)
			if text == "" {
				continue
			}
			if _, err := io.WriteString(stdout, text); err != nil {
				return roundHasContent, err
			}
			roundHasContent = true
		}
	}
	return roundHasContent, nil
}
