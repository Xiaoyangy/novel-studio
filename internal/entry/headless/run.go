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
	Prompt                 string
	StopAfterChapter       int
	StopAfterPlanChapter   int
	StopAfterRewriteCommit int
	// StopAfterGlobalReviewChapter returns control to the pipeline as soon as
	// save_review has durably recorded a new scope=global review at this chapter.
	// It is intentionally separate from StopAfterChapter: a rejected whole-book
	// review may enqueue rewrites, but the sealed finalization stage must stop and
	// report those affected chapters instead of silently starting a legacy rewrite.
	StopAfterGlobalReviewChapter int
	StopOnRenderReplanChapter    int
	// StopOnSealedConvergencePreconditionChapter is used only by the narrow
	// sealed convergence Planner. Deterministic context/authority failures are
	// not model-repairable; abort on the first observed failure instead of
	// paying for the same tool call three times.
	StopOnSealedConvergencePreconditionChapter int
	StopAfterFoundation                        bool
	StopAfterFoundationChange                  bool
	StopAfterInitialWorldTick                  bool
	PreserveUserRules                          bool
	SkipQueueReplay                            bool
	DisableLiveRAG                             bool
	// WriterSessionIdentity is an internal pipeline-only audit identity for a
	// fresh Planner run. Empty preserves the normal writer-chNN routing.
	WriterSessionIdentity string
	Stdin                 io.Reader
	Stdout                io.Writer
	Stderr                io.Writer
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
	eng, err := host.NewWithOptions(cfg, bundle, host.NewOptions{
		WriterSessionIdentity: opts.WriterSessionIdentity,
	})
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
	globalReviewSeq := latestGlobalReviewSeq(eng.Dir(), opts.StopAfterGlobalReviewChapter)
	renderDraftSeq := latestChapterDraftSeq(eng.Dir(), opts.StopOnRenderReplanChapter)
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
		return consume(eng, stdout, stderr, roundHasContent, opts.StopAfterChapter, opts.StopAfterPlanChapter, planSeq, opts.StopAfterRewriteCommit, rewriteCommitSeq, opts.StopAfterGlobalReviewChapter, globalReviewSeq, opts.StopOnRenderReplanChapter, opts.StopOnSealedConvergencePreconditionChapter, opts.StopAfterFoundation, opts.StopAfterFoundationChange, foundationDigest, opts.StopAfterInitialWorldTick, renderDraftSeq)
	}

	return consume(eng, stdout, stderr, false, opts.StopAfterChapter, opts.StopAfterPlanChapter, planSeq, opts.StopAfterRewriteCommit, rewriteCommitSeq, opts.StopAfterGlobalReviewChapter, globalReviewSeq, opts.StopOnRenderReplanChapter, opts.StopOnSealedConvergencePreconditionChapter, opts.StopAfterFoundation, opts.StopAfterFoundationChange, foundationDigest, opts.StopAfterInitialWorldTick, renderDraftSeq)
}

func consume(eng *host.Host, stdout, stderr io.Writer, roundHasContent bool, stopAfterChapter, stopAfterPlanChapter int, initialPlanSeq int64, stopAfterRewriteCommit int, initialRewriteCommitSeq int64, stopAfterGlobalReviewChapter int, initialGlobalReviewSeq int64, stopOnRenderReplanChapter, stopOnSealedConvergencePreconditionChapter int, stopAfterFoundation, stopAfterFoundationChange bool, initialFoundationDigest string, stopAfterInitialWorldTick bool, initialRenderDraftSeq int64) error {
	stopRequested := false
	for {
		select {
		case ev, ok := <-eng.Events():
			if !ok {
				return nil
			}
			writeEvent(stderr, ev)
			if deterministicErr := sealedConvergenceDeterministicPreconditionError(
				ev,
				stopOnSealedConvergencePreconditionChapter,
			); deterministicErr != nil {
				eng.Abort()
				return deterministicErr
			}
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
			if !stopRequested && shouldStopAfterGlobalReview(eng.Dir(), stopAfterGlobalReviewChapter, initialGlobalReviewSeq) {
				stopRequested = true
				fmt.Fprintf(stderr, "[headless] 第 %d 章锚定的全文终审已落盘，交还 pipeline 做 sealed 完结验收\n", stopAfterGlobalReviewChapter)
				eng.Abort()
			}
			if !stopRequested && stopOnRenderReplanChapter > 0 {
				if stopErr := inspectRenderOnlyReplanStop(eng.Dir(), stopOnRenderReplanChapter); stopErr != nil {
					eng.Abort()
					return stopErr
				}
			}
			if !stopRequested && shouldStopAfterChapterDraft(eng.Dir(), stopOnRenderReplanChapter, initialRenderDraftSeq) {
				stopRequested = true
				fmt.Fprintf(stderr, "[headless] 第 %d 章新整章草稿已落盘，立即交还 pipeline 做 exact-hash/static/provider 门禁\n", stopOnRenderReplanChapter)
				eng.Abort()
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

func sealedConvergenceDeterministicPreconditionError(ev host.Event, chapter int) error {
	if chapter <= 0 || ev.Category != "ERROR" {
		return nil
	}
	text := strings.TrimSpace(ev.Detail)
	if text == "" {
		text = strings.TrimSpace(ev.Summary)
	}
	lower := strings.ToLower(text)
	contextBudget := strings.Contains(lower, "novel_context") &&
		(strings.Contains(text, "关键上下文无法安全收敛") ||
			strings.Contains(text, "返工关键上下文超过硬上限") ||
			strings.Contains(text, "关键上下文超过硬上限") ||
			strings.Contains(text, "最终序列化结果超过预算"))
	authorityBinding := strings.Contains(lower, "stored project-all authority receipt invalid") &&
		strings.Contains(lower, "active project-all authority binding no longer matches receipt")
	overlayBinding := strings.Contains(lower, "sealed convergence authority overlay")
	projectedStateBinding := strings.Contains(lower, "plan_details") &&
		strings.Contains(lower, "project-all-state") &&
		strings.Contains(lower, "source token")
	if !contextBudget && !authorityBinding && !overlayBinding && !projectedStateBinding {
		return nil
	}
	return fmt.Errorf(
		"第 %d 章 sealed convergence replan deterministic planning precondition；已在首次失败后终止 Host，禁止模型重复同一调用: %s",
		chapter,
		text,
	)
}

func inspectRenderOnlyReplanStop(dir string, chapter int) error {
	st := store.NewStore(dir)
	// This check runs after every Host event, including the tool result that
	// durably records a structural rejection. It must observe the combined
	// plan-owned ledger before the router can request another prose projection.
	if err := tools.RequireRenderConvergenceAttemptAvailable(st, chapter); err != nil {
		return err
	}
	escalation := tools.InspectRenderOnlyReplanEscalation(st, chapter)
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

func latestChapterDraftSeq(dir string, chapter int) int64 {
	if chapter <= 0 {
		return 0
	}
	cp := store.NewStore(dir).Checkpoints.LatestByStep(domain.ChapterScope(chapter), "draft")
	if cp == nil {
		return 0
	}
	return cp.Seq
}

func shouldStopAfterChapterDraft(dir string, chapter int, initialSeq int64) bool {
	return chapter > 0 && latestChapterDraftSeq(dir, chapter) > initialSeq
}

func shouldStopAfterChapterCommit(dir string, chapter int, initialSeq int64) bool {
	return chapter > 0 && latestChapterCommitSeq(dir, chapter) > initialSeq
}

func latestGlobalReviewSeq(dir string, chapter int) int64 {
	if chapter <= 0 {
		return 0
	}
	want := fmt.Sprintf("reviews/%02d-global.json", chapter)
	checkpoints := store.NewStore(dir).Checkpoints.All()
	for i := len(checkpoints) - 1; i >= 0; i-- {
		checkpoint := checkpoints[i]
		if checkpoint.Scope.Matches(domain.ChapterScope(chapter)) &&
			checkpoint.Step == "review" && checkpoint.Artifact == want {
			return checkpoint.Seq
		}
	}
	return 0
}

func shouldStopAfterGlobalReview(dir string, chapter int, initialSeq int64) bool {
	return chapter > 0 && latestGlobalReviewSeq(dir, chapter) > initialSeq
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
