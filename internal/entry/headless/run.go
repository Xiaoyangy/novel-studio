package headless

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	StopAfterPlanChapter      int
	StopAfterRewriteCommit    int
	StopOnRenderReplanChapter int
	StopAfterFoundation       bool
	StopAfterFoundationChange bool
	StopAfterInitialWorldTick bool
	PreserveUserRules         bool
	SkipQueueReplay           bool
	DisableLiveRAG            bool
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

	if opts.DisableLiveRAG {
		cfg.DisableLiveRAG = true
	}
	eng, err := host.New(cfg, bundle)
	if err != nil {
		return err
	}
	if opts.StopOnRenderReplanChapter > 0 {
		if stopErr := inspectRenderOnlyReplanStop(eng.Dir(), opts.StopOnRenderReplanChapter); stopErr != nil {
			eng.Close()
			return stopErr
		}
	}
	eng.AskUser().SetHandler(newTerminalAskUser(stdin, stderr).handle)
	foundationDigest := ""
	if opts.StopAfterFoundationChange {
		foundationDigest = foundationRevisionDigest(eng.Dir())
	}
	rewriteCommitSeq := latestChapterCommitSeq(eng.Dir(), opts.StopAfterRewriteCommit)
	planSeq := latestChapterPlanSeq(eng.Dir(), opts.StopAfterPlanChapter)
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
		// 新书启动时生成用户规则快照；pipeline 内部的 Architect 刷新提示只是阶段指令，
		// 不能覆盖已经沉淀的长期用户规则。
		if !opts.PreserveUserRules {
			if err := eng.PrepareUserRules(plan.RawPrompt); err != nil {
				return err
			}
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
		return consume(eng, stdout, stderr, roundHasContent, opts.StopAfterChapter, opts.StopAfterPlanChapter, planSeq, opts.StopAfterRewriteCommit, rewriteCommitSeq, opts.StopOnRenderReplanChapter, opts.StopAfterFoundation, opts.StopAfterFoundationChange, foundationDigest, opts.StopAfterInitialWorldTick)
	}

	return consume(eng, stdout, stderr, false, opts.StopAfterChapter, opts.StopAfterPlanChapter, planSeq, opts.StopAfterRewriteCommit, rewriteCommitSeq, opts.StopOnRenderReplanChapter, opts.StopAfterFoundation, opts.StopAfterFoundationChange, foundationDigest, opts.StopAfterInitialWorldTick)
}

func consume(eng *host.Host, stdout, stderr io.Writer, roundHasContent bool, stopAfterChapter, stopAfterPlanChapter int, initialPlanSeq int64, stopAfterRewriteCommit int, initialRewriteCommitSeq int64, stopOnRenderReplanChapter int, stopAfterFoundation, stopAfterFoundationChange bool, initialFoundationDigest string, stopAfterInitialWorldTick bool) error {
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
			if !stopRequested && shouldStopAfterChapterPlan(eng.Dir(), stopAfterPlanChapter, initialPlanSeq) {
				stopRequested = true
				fmt.Fprintf(stderr, "[headless] 第 %d 章正式 plan 已新增，按 pipeline 推演阶段暂停\n", stopAfterPlanChapter)
				eng.Abort()
			}
			if !stopRequested && shouldStopAfterChapterCommit(eng.Dir(), stopAfterRewriteCommit, initialRewriteCommitSeq) {
				stopRequested = true
				fmt.Fprintf(stderr, "[headless] 第 %d 章返工正文已 commit，交还 pipeline 复审\n", stopAfterRewriteCommit)
				eng.Abort()
			}
			if !stopRequested && stopOnRenderReplanChapter > 0 {
				if stopErr := inspectRenderOnlyReplanStop(eng.Dir(), stopOnRenderReplanChapter); stopErr != nil {
					eng.Abort()
					return stopErr
				}
			}
			if !stopRequested && stopAfterFoundation && shouldStopAfterFoundationReady(eng.Dir()) {
				stopRequested = true
				fmt.Fprintln(stderr, "[headless] Architect foundation 已齐，按 pipeline 阶段暂停")
				eng.Abort()
			}
			if !stopRequested && stopAfterFoundationChange && shouldStopAfterFoundationChanged(eng.Dir(), initialFoundationDigest) {
				stopRequested = true
				fmt.Fprintln(stderr, "[headless] Architect foundation 已更新，按 pipeline 刷新阶段暂停")
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

func inspectRenderOnlyReplanStop(dir string, chapter int) error {
	escalation := tools.InspectRenderOnlyReplanEscalation(store.NewStore(dir), chapter)
	if !escalation.Required {
		return nil
	}
	return renderOnlyReplanStopError(chapter, escalation)
}

func renderOnlyReplanStopError(chapter int, escalation tools.RenderOnlyReplanEscalation) error {
	return fmt.Errorf(
		"第 %d 章 render-only 已有 %d 个不同整章哈希触发结构阻断（上限 %d）；冻结计划保持不变，render 阶段立即停止，禁止自动回到 World Simulator/Planner：%s",
		chapter,
		escalation.Attempts,
		escalation.Limit,
		escalation.Reason,
	)
}

func shouldStopAfterFoundationReady(dir string) bool {
	return tools.FoundationCoreComplete(dir)
}

func latestChapterCommitSeq(dir string, chapter int) int64 {
	if chapter <= 0 {
		return 0
	}
	cp := store.NewStore(dir).Checkpoints.LatestByStep(domain.ChapterScope(chapter), "commit")
	if cp == nil {
		return 0
	}
	return cp.Seq
}

func shouldStopAfterChapterCommit(dir string, chapter int, initialSeq int64) bool {
	return chapter > 0 && latestChapterCommitSeq(dir, chapter) > initialSeq
}

func latestChapterPlanSeq(dir string, chapter int) int64 {
	if chapter <= 0 {
		return 0
	}
	cp := store.NewStore(dir).Checkpoints.LatestByStep(domain.ChapterScope(chapter), "plan")
	if cp == nil {
		return 0
	}
	return cp.Seq
}

func shouldStopAfterChapterPlan(dir string, chapter int, initialSeq int64) bool {
	return chapter > 0 && latestChapterPlanSeq(dir, chapter) > initialSeq
}

func shouldStopAfterFoundationChanged(dir, initialDigest string) bool {
	if initialDigest == "" || !tools.FoundationCoreComplete(dir) {
		return false
	}
	current := foundationRevisionDigest(dir)
	return current != "" && current != initialDigest
}

func foundationRevisionDigest(dir string) string {
	h := sha256.New()
	for _, rel := range []string{"outline.json", "layered_outline.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return ""
		}
		_, _ = h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))
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
