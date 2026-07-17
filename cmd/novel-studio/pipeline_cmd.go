package main

// --pipeline：把各功能串成一条可恢复的流水线，按阶段顺序执行。
// 阶段：cocreate → architect → outline-all → zero-init → preplan → project-all → seal → promote → render（默认不含 cocreate）。
// project-all/seal 保留历史阶段名，但事务粒度已是当前弧：整弧推演并封存后，
// 才逐章 promote → render → exact-body review；本弧全章通过后进入下一弧。
// 旧的逐章兼容路径仍为 preplan → plan → render。
// 状态持久化到 meta/pipeline.json：已完成的阶段在重跑时自动跳过，从断点继续。
//
// 设计：流水线只做"阶段编排 + 断点续跑"，每个阶段复用已有子命令逻辑（headless.Run /
// reviewExistingPipeline / ...）。阶段内部各自还有更细的恢复（write 走 checkpoint、
// review/rewrite 按章号），两层恢复叠加。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// defaultPipelineStages 不含 cocreate：默认假设已有创作指令（--prompt/brainstorm.md）。
// 新书必须先显式完成 Architect foundation，再执行 zero-init，最后才允许正文写作。
var defaultPipelineStages = []string{
	"architect", "outline-all", "zero-init", "preplan", "project-all", "seal", "promote", "render",
}

var knownPipelineStages = map[string]bool{
	"cocreate": true, "architect": true, "outline-all": true, "zero-init": true,
	"preplan": true, "project-all": true, "seal": true, "promote": true,
	"plan": true, "render": true,
	"write": true, "review": true, "rewrite": true, "deliver": true,
}

type pipelineFlags struct {
	Stages            string
	Prompt            string
	PromptFile        string
	Restart           bool
	Start             int
	End               int
	WriteTo           int
	StopAfterCommit   int
	Budget            time.Duration
	Role              string
	MaxRewriteRounds  int
	PolishWarnings    bool
	RewriteBriefOnly  bool
	ForceRerender     bool
	NewNovel          bool
	RefreshArchitect  bool
	RefreshZeroInit   bool
	RenderOnly        bool
	RebaseAllChapters bool
}

func parsePipelineFlags(argv []string) (pipelineFlags, []string, error) {
	fs := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --pipeline [--prompt <text> | --prompt-file <path>] [--stages a,b,c] [--restart]\n\n")
		fmt.Fprintf(os.Stderr, "按阶段顺序跑完整流程，状态存 meta/pipeline.json，可断点续跑。\n")
		fmt.Fprintf(os.Stderr, "阶段：cocreate / architect / outline-all / zero-init / preplan / project-all / seal / promote / plan / render / write / review / rewrite / deliver（默认 %s）\n", strings.Join(defaultPipelineStages, ","))
		fmt.Fprintln(os.Stderr, "全书结构：outline-all 在第0章隔离工作区先完成全部卷/弧/章合同，再由 zero-init 与后续全书推演消费。")
		fmt.Fprintln(os.Stderr, "弧式写作：preplan 保留全书稳定章位；project-all 每轮只推演当前一弧，seal 只封存本弧；随后 promote/render 逐章写、逐章审，本弧全部通过后才开放下一弧。")
		fmt.Fprintln(os.Stderr, "兼容路径：preplan → plan → render 仍可逐章规划，但不等同于全书先推演。")
		fmt.Fprintln(os.Stderr, "\n选项：")
		fs.PrintDefaults()
	}
	var f pipelineFlags
	fs.StringVar(&f.Stages, "stages", "", "逗号分隔的阶段列表，缺省 "+strings.Join(defaultPipelineStages, ","))
	fs.StringVar(&f.Prompt, "prompt", "", "创作指令（write 阶段用）")
	fs.StringVar(&f.PromptFile, "prompt-file", "", "从文件读创作指令，'-' 表示 stdin")
	fs.BoolVar(&f.Restart, "restart", false, "清空已保存的流水线状态，从头重跑")
	fs.IntVar(&f.Start, "from", 0, "outline-all/preplan/project-all/seal/promote/plan/render/review/rewrite 阶段起始章号（含），0 = 自动")
	fs.IntVar(&f.End, "to", 0, "outline-all/preplan/project-all/seal/promote/plan/render/review/rewrite 阶段结束章号（含），0 = 自动")
	fs.IntVar(&f.WriteTo, "write-to", 0, "write 阶段写到指定章节后暂停；0 = 写到全书完结")
	fs.DurationVar(&f.Budget, "budget", 0, "review/rewrite 阶段每章 LLM 调用硬时间预算，0 = 使用阶段默认")
	fs.StringVar(&f.Role, "role", "", "rewrite 阶段调用的模型角色，空 = writer")
	fs.IntVar(&f.MaxRewriteRounds, "max-rewrite-rounds", 0, "rewrite 阶段红旗重写-复审闭环最多轮数，0 = 使用默认 3")
	fs.BoolVar(&f.PolishWarnings, "polish-warnings", false, "rewrite 阶段无红旗但存在黄旗时，也按质量优先原则择优打磨")
	fs.BoolVar(&f.RewriteBriefOnly, "brief-only", false, "rewrite 阶段只刷新 rewrite brief，不调用 Writer、不改正文")
	fs.BoolVar(&f.ForceRerender, "force-rerender", false, "rewrite 阶段显式让现有 plan 重新渲染整章；保留世界推演，不伪造外判阻断")
	fs.BoolVar(&f.NewNovel, "new-novel", false, "新建小说：先跑头脑风暴（web/RAG 调研 + 落盘 brainstorm.md），再据此初始化并写作")
	fs.BoolVar(&f.RefreshArchitect, "refresh-architect", false, "已有 foundation 仍强制 Architect 按本次 prompt 重规划开篇/大纲；只在 architect 阶段生效")
	fs.BoolVar(&f.RefreshZeroInit, "refresh-zero-init", false, "已有正文时安全刷新 zero-init 开篇计划和 RAG，不覆盖活动资源/关系台账")
	fs.BoolVar(&f.RebaseAllChapters, "rebase-all-chapters", false, "将现有正文和活动台账完整归档后，把正史安全回到第0章，再按逐弧闭环重推")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

// hasPipelineFlag 判断 argv 中是否含 --pipeline 入口。
func hasPipelineFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--pipeline" {
			return true
		}
	}
	return false
}

func pipelinePipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _, _ = parsePipelineFlags([]string{"--help"})
		return nil
	}
	flags, extra, err := parsePipelineFlags(args)
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return fmt.Errorf("--pipeline 不接受额外参数：%v", extra)
	}

	stages, err := resolveStages(flags.Stages)
	if err != nil {
		return err
	}

	prompt, err := resolvePipelinePrompt(flags, opts)
	if err != nil {
		return err
	}

	// 新建小说：先跑头脑风暴，落盘 data/runs/<书名>/brainstorm.md，再把项目目录设为它，
	// 让后续 Architect 基于 brainstorm.md 初始化世界 → zero-init → 写作。
	if flags.NewNovel {
		projectDir, berr := runPipelineBrainstorm(opts, prompt)
		if berr != nil {
			return fmt.Errorf("头脑风暴阶段失败: %w", berr)
		}
		opts.Dir = projectDir  // 后续 OutputDir = projectDir/output/novel
		prompt = ""            // 创作指令改由 brainstorm.md 承载，不再透传原始 idea
		flags.NewNovel = false // 已完成
	}
	if flags.RebaseAllChapters {
		if err := pipelineRebaseAllChapters(opts); err != nil {
			return err
		}
		flags.Restart = true
	}

	return runPipelineWithStages(opts, flags, stages, prompt, nil)
}

// runPipelineBrainstorm 跑头脑风暴子代理，产出 brainstorm.md，返回项目根目录。
func runPipelineBrainstorm(opts cliOptions, idea string) (string, error) {
	if strings.TrimSpace(idea) == "" {
		return "", fmt.Errorf("新建小说需要一个想法：用 --prompt \"<你的小说想法>\"")
	}
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return "", err
	}
	runsRoot := filepath.Join("data", "runs")
	fmt.Fprintln(os.Stderr, "[pipeline:brainstorm] 头脑风暴中：调研题材、推敲逻辑、落盘 brainstorm.md…")
	projectDir, err := agents.RunBrainstorm(cfg, bundle, runsRoot, idea)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "[pipeline:brainstorm] 已落盘：%s\n", filepath.Join(projectDir, "brainstorm.md"))
	return projectDir, nil
}

func runPipelineAlias(opts cliOptions, stages []string, prompt string, stageArgs map[string][]string) error {
	flags := pipelineFlags{Stages: strings.Join(stages, ",")}
	if reviewArgs, ok := stageArgs["review"]; ok {
		reviewFlags, extra, err := parseReviewFlags(reviewArgs)
		if err == nil && len(extra) == 0 {
			flags.Start = reviewFlags.Start
			flags.End = reviewFlags.End
			flags.Budget = reviewFlags.Budget
		}
	}
	if rewriteArgs, ok := stageArgs["rewrite"]; ok {
		rewriteFlags, extra, err := parseRewriteFlags(rewriteArgs)
		if err == nil && len(extra) == 0 {
			flags.Start = rewriteFlags.Start
			flags.End = rewriteFlags.End
			flags.Budget = rewriteFlags.Budget
			flags.Role = rewriteFlags.Role
			flags.PolishWarnings = rewriteFlags.PolishWarnings
			flags.RewriteBriefOnly = rewriteFlags.BriefOnly
		}
	}
	return runPipelineWithStages(opts, flags, stages, prompt, stageArgs)
}

func runPipelineWithStages(opts cliOptions, flags pipelineFlags, stages []string, prompt string, stageArgs map[string][]string) (returnErr error) {
	hasOutlineAll := slices.Contains(stages, "outline-all")
	hasDownstream := pipelineStagesConsumePublishedOutlineAll(stages)
	var invocationExclusiveRelease func() error
	defer func() {
		if invocationExclusiveRelease != nil {
			if err := invocationExclusiveRelease(); err != nil && returnErr == nil {
				returnErr = err
			}
		}
	}()
	outputDir, err := pipelineOutlineAllOutputDirBeforeLoad(opts)
	if err != nil {
		return err
	}
	if outputDir == "" {
		if hasOutlineAll {
			return fmt.Errorf("outline-all requires configured output directory before pipeline setup")
		}
	} else {
		releaseExclusive, err := acquirePipelineOutlineAllControl(outputDir, true)
		if err != nil {
			return err
		}
		// Recovery must be first: in the live_archived crash phase, writing a
		// marker below outputDir would recreate a fake live tree and make the
		// directory transaction unrecoverable.
		if err := recoverAllDirectoryPublishesWithControlHeld(outputDir); err != nil {
			_ = releaseExclusive()
			return err
		}
		if hasOutlineAll {
			if err := ensurePipelineOutlineAllRequirement(outputDir); err != nil {
				_ = releaseExclusive()
				return err
			}
		}
		if hasDownstream && !hasOutlineAll {
			if err := requirePublishedOutlineAllWithControlHeld(outputDir); err != nil {
				_ = releaseExclusive()
				return fmt.Errorf("pipeline requires published outline-all: %w", err)
			}
			if receipt, loadErr := store.NewStore(outputDir).LoadOutlineAllExecutionReceipt(); loadErr != nil {
				_ = releaseExclusive()
				return loadErr
			} else if receipt != nil {
				if _, err := verifyPipelineOutlineAllReceiptAndArtifactsWithControlHeld(outputDir); err != nil {
					_ = releaseExclusive()
					return fmt.Errorf("pipeline requires fully verified outline-all: %w", err)
				}
			}
		}
		// Safety-first serialization: the stable run-root EX covers config writes,
		// every stage, verification, state save, and any whole-live publish.
		invocationExclusiveRelease = releaseExclusive
	}
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if slices.Contains(stages, "outline-all") || slices.Contains(stages, "project-all") {
		if err := activatePipelineSealedTwoPassModeAtOutput(cfg.OutputDir); err != nil {
			return err
		}
	}
	if mode, modeErr := store.NewStore(cfg.OutputDir).LoadWritingPipelineMode(); modeErr != nil {
		return fmt.Errorf("读取项目 writing pipeline mode: %w", modeErr)
	} else if mode != nil && mode.Mode == domain.WritingPipelineModeSealedTwoPassV2 {
		for _, forbidden := range []string{"plan", "write", "rewrite"} {
			if slices.Contains(stages, forbidden) {
				return fmt.Errorf(
					"项目已启用 sealed planning，阶段 %s 会绕过当前弧封存或改写已验收正文；只允许 project-all(当前弧) → seal(当前弧) → promote → render(逐章审核)",
					forbidden,
				)
			}
		}
	}
	if pipelineStagesNeedQdrant(stages) {
		if enabled, err := bootstrap.EnsureRAGQdrant(context.Background(), cfg); err != nil {
			return fmt.Errorf("pipeline 启动 Qdrant 失败: %w", err)
		} else if enabled {
			fmt.Fprintf(os.Stderr, "[pipeline] Qdrant 已就绪\n")
		}
	}
	ensureDashboardServiceForRun(cfg.OutputDir)
	statePath := filepath.Join(cfg.OutputDir, "meta", "pipeline.json")
	if stageArgs == nil {
		stageArgs = pipelineStageArgs(flags)
	}

	inputDigest := pipelineRunInputDigest(cfg, bundle)
	runIdentity := pipelineRunIdentityDigest(flags)
	state, err := loadOrInitPipelineState(statePath, stages, prompt, inputDigest, runIdentity, flags.Restart)
	if err != nil {
		return err
	}
	renderRecoveryPending, err := splitPipelineRenderRecoveryPending(cfg.OutputDir, state)
	if err != nil {
		return err
	}
	if reset, err := resetCompletedSplitPipelineCycle(cfg.OutputDir, state); err != nil {
		return err
	} else if reset {
		if pipelineStateHasStage(state, "project-all") && pipelineStateHasStage(state, "seal") &&
			!state.Done("project-all") && !state.Done("seal") {
			fmt.Fprintln(os.Stderr, "[pipeline] 当前弧全部章节已逐章渲染并审核通过；已封闭本弧，下一轮从下一弧 preplan/project-all 开始")
		} else if pipelineStateHasStage(state, "project-all") && pipelineStateHasStage(state, "seal") {
			fmt.Fprintln(os.Stderr, "[pipeline] 上一章 sealed render 已验收，只清空 promote/render 游标并机械提升下一章")
		} else {
			fmt.Fprintln(os.Stderr, "[pipeline] 上一轮 render 已改变正史，清空拆分写作阶段游标并从 preplan 开始下一轮")
		}
	}
	// Persist restart/input invalidation before the first expensive stage. A
	// crash at stage startup must not resurrect the previous completed graph.
	if err := savePipelineState(statePath, state); err != nil {
		return fmt.Errorf("保存流水线启动状态失败: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[pipeline] 阶段：%s\n", strings.Join(state.Stages, " → "))
	if len(state.Completed) > 0 {
		fmt.Fprintf(os.Stderr, "[pipeline] 已完成（将跳过）：%s\n", strings.Join(state.Completed, ", "))
	}

	for _, stage := range state.Stages {
		if renderRecoveryPending && state.Done(stage) &&
			(stage == "zero-init" || stage == "preplan" || stage == "project-all" || stage == "seal" || stage == "promote" || stage == "plan") {
			fmt.Fprintf(os.Stderr, "[pipeline] 保留已完成阶段 %s：先让 render 收口已提交正文的 recovery receipt\n", stage)
			continue
		}
		if state.Done(stage) {
			storedEvidence := state.Evidence[stage]
			evidence := storedEvidence
			err := verifyStoredPipelineArtifactDigests(cfg.OutputDir, storedEvidence)
			if err == nil {
				evidence, err = verifyPipelineStage(stage, cfg.OutputDir, flags, state)
			}
			if err == nil {
				evidence = stampPipelineArtifactDigests(cfg.OutputDir, evidence)
				state.MarkDone(stage, evidence)
				if stage == "outline-all" && state.Done("architect") {
					architectEvidence, verifyErr := verifyPipelineStage("architect", cfg.OutputDir, flags, state)
					if verifyErr != nil {
						return fmt.Errorf("outline-all refreshed architect evidence invalid: %w", verifyErr)
					}
					state.MarkDone("architect", stampPipelineArtifactDigests(cfg.OutputDir, architectEvidence))
				}
				if err := savePipelineState(statePath, state); err != nil {
					return fmt.Errorf("保存流水线状态失败: %w", err)
				}
				fmt.Fprintf(os.Stderr, "[pipeline] 跳过已完成阶段：%s\n", stage)
				continue
			}
			evidence.Status = "stale"
			evidence.Message = err.Error()
			state.ClearDone(stage, evidence)
			fmt.Fprintf(os.Stderr, "[pipeline] 已完成阶段证据失效，重新执行 %s：%v\n", stage, err)
		}
		fmt.Fprintf(os.Stderr, "\n========== 阶段：%s ==========\n", stage)
		if stage == "write" {
			warnStaleZeroInitReadiness(cfg.OutputDir)
		}
		if err := runPipelineStage(stage, opts, flags, state, stageArgs); err != nil {
			// 不标记完成 → 下次重跑从这里继续。先落盘（可能 cocreate 已写入 prompt）。
			_ = savePipelineState(statePath, state)
			return fmt.Errorf("阶段 %s 失败（修复后重跑 --pipeline 即从此阶段继续）: %w", stage, err)
		}
		evidence, err := verifyPipelineStage(stage, cfg.OutputDir, flags, state)
		if err != nil {
			evidence.Status = "invalid"
			evidence.Message = err.Error()
			state.ClearDone(stage, evidence)
			_ = savePipelineState(statePath, state)
			return fmt.Errorf("阶段 %s 完成证据不足（未标记完成）: %w", stage, err)
		}
		evidence = stampPipelineArtifactDigests(cfg.OutputDir, evidence)
		state.MarkDone(stage, evidence)
		if stage == "outline-all" && state.Done("architect") {
			architectEvidence, verifyErr := verifyPipelineStage("architect", cfg.OutputDir, flags, state)
			if verifyErr != nil {
				return fmt.Errorf("outline-all refreshed architect evidence invalid: %w", verifyErr)
			}
			state.MarkDone("architect", stampPipelineArtifactDigests(cfg.OutputDir, architectEvidence))
		}
		if stage == "render" {
			renderRecoveryPending = false
		}
		if err := savePipelineState(statePath, state); err != nil {
			return fmt.Errorf("保存流水线状态失败: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[pipeline] 阶段完成：%s\n", stage)
	}
	// render intentionally advances canon and marks the speculative all-book
	// projection as requiring a rebase. Stage completion is therefore scoped to
	// this chapter cycle, not permanent for the process lifetime. Clear the
	// coarse stage-name cursor before final reconciliation so the next
	// invocation starts at preplan instead of replaying chapter N's receipts.
	splitCycleReset := false
	if reset, err := resetCompletedSplitPipelineCycle(cfg.OutputDir, state); err != nil {
		return err
	} else if reset {
		if err := savePipelineState(statePath, state); err != nil {
			return fmt.Errorf("保存拆分写作下一轮状态失败: %w", err)
		}
		splitCycleReset = true
	}
	// Later stages intentionally mutate earlier-stage artifacts (rewrite updates
	// chapters and re-review replaces reports). Reconcile every completed stage
	// against the final state so persisted digests describe the delivered graph,
	// not a transient midpoint that is stale by design.
	for _, stage := range state.Stages {
		if !state.Done(stage) {
			continue
		}
		evidence, verifyErr := verifyPipelineStage(stage, cfg.OutputDir, flags, state)
		if verifyErr != nil {
			evidence.Status = "stale"
			evidence.Message = verifyErr.Error()
			state.ClearDone(stage, evidence)
			_ = savePipelineState(statePath, state)
			return fmt.Errorf("pipeline 最终证据对账失败（阶段 %s）: %w", stage, verifyErr)
		}
		state.MarkDone(stage, stampPipelineArtifactDigests(cfg.OutputDir, evidence))
	}
	if err := savePipelineState(statePath, state); err != nil {
		return fmt.Errorf("保存流水线最终证据失败: %w", err)
	}

	if splitCycleReset {
		if pipelineStateHasStage(state, "project-all") && state.Done("project-all") && state.Done("seal") {
			fmt.Fprintln(os.Stderr, "\n[pipeline] 本章已渲染并逐章审核通过；下一轮机械提升当前弧下一章 ✓")
		} else if pipelineStateHasStage(state, "project-all") {
			fmt.Fprintln(os.Stderr, "\n[pipeline] 当前弧全部章节已渲染并逐章审核通过；下一轮开始推演下一弧 ✓")
		} else {
			fmt.Fprintln(os.Stderr, "\n[pipeline] 本轮拆分写作阶段完成；下一轮将先重跑 preplan 再推进可行动章 ✓")
		}
	} else {
		fmt.Fprintln(os.Stderr, "\n[pipeline] 全部阶段完成 ✓")
	}
	return nil
}

func pipelineRunIdentityDigest(flags pipelineFlags) string {
	payload, _ := json.Marshal(struct {
		Schema            string `json:"schema"`
		From              int    `json:"from"`
		To                int    `json:"to"`
		WriteTo           int    `json:"write_to"`
		BudgetNanoseconds int64  `json:"budget_nanoseconds"`
	}{
		Schema:            "pipeline-run-identity.v1",
		From:              flags.Start,
		To:                flags.End,
		WriteTo:           flags.WriteTo,
		BudgetNanoseconds: int64(flags.Budget),
	})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func resetCompletedSplitPipelineCycle(outputDir string, state *domain.PipelineState) (bool, error) {
	if reset, err := resetCompletedSealedPipelineCycle(outputDir, state); err != nil || reset {
		return reset, err
	}
	if state == nil || !pipelineStateHasStage(state, "preplan") || !pipelineStateHasStage(state, "render") {
		return false, nil
	}
	var receipt pipelinePreplanReceipt
	path := filepath.Join(outputDir, pipelinePlanningReceiptPath)
	if err := readPipelinePlanningJSON(path, &receipt); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("读取拆分写作周期回执失败: %w", err)
	}
	rebasePending, err := pipelinePreplanRebasePending(outputDir, receipt)
	if err != nil {
		return false, err
	}
	if !rebasePending {
		return false, nil
	}
	if recoveryPending, err := splitPipelineRenderRecoveryPending(outputDir, state); err != nil {
		return false, err
	} else if recoveryPending {
		return false, nil
	}
	reset := false
	for _, stage := range []string{"preplan", "plan", "render"} {
		if !state.Done(stage) {
			continue
		}
		state.ClearDone(stage, domain.PipelineStageEvidence{
			Stage:     stage,
			Status:    "next_cycle",
			CheckedAt: time.Now(),
			Message:   "render advanced canonical state; the next chapter cycle must rebase preplan before planning or rendering",
		})
		reset = true
	}
	return reset, nil
}

func resetCompletedSealedPipelineCycle(outputDir string, state *domain.PipelineState) (bool, error) {
	if state == nil ||
		!pipelineStateHasStage(state, "promote") ||
		!pipelineStateHasStage(state, "render") ||
		!state.Done("promote") ||
		!state.Done("render") {
		return false, nil
	}
	st := store.NewStore(outputDir)
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil {
		return false, err
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil {
		return false, err
	}
	generation, err := projected.LoadSealedGeneration(active.GenerationID)
	if err != nil || generation == nil {
		return false, err
	}
	var receipt pipelineRenderReceipt
	if err := readPipelinePlanningJSON(filepath.Join(outputDir, pipelineRenderReceiptPath), &receipt); err != nil {
		return false, err
	}
	if receipt.PlanningGenerationID != active.GenerationID ||
		receipt.OutcomeReceiptDigest == "" ||
		cursor.ActivePromotedChapter != 0 ||
		cursor.LastAcceptedChapter != receipt.Chapter ||
		cursor.LastOutcomeReceiptDigest != receipt.OutcomeReceiptDigest {
		return false, nil
	}
	if err := requirePipelineChapterAcceptance(
		st,
		generation,
		receipt.Chapter,
		receipt.OutcomeReceiptDigest,
	); err != nil {
		return false, fmt.Errorf("当前章逐章审核回执尚未封存: %w", err)
	}
	if cursor.NextPromoteChapter > generation.LastProjectedChapter {
		if err := requirePipelineArcChapterAcceptances(outputDir, st, generation); err != nil {
			return false, fmt.Errorf("当前弧尚不能完成封印: %w", err)
		}
		completion, err := completePipelineArcCycle(st, generation, cursor)
		if err != nil {
			return false, fmt.Errorf("当前弧完成回执不可发布: %w", err)
		}
		if generation.BookHorizonChapter > 0 &&
			generation.LastProjectedChapter == generation.BookHorizonChapter {
			// The terminal arc has no successor. Keep every completed stage in
			// its durable terminal state so a second invocation verifies the same
			// receipts and exits idempotently instead of inventing chapter N+1.
			return false, nil
		}
		reset := false
		for _, stage := range []string{"preplan", "project-all", "seal", "promote", "render"} {
			if !state.Done(stage) {
				continue
			}
			state.ClearDone(stage, domain.PipelineStageEvidence{
				Stage:     stage,
				Status:    "next_arc",
				CheckedAt: time.Now(),
				Message:   "every chapter in the current arc passed its own exact-body review; arc completion " + completion.ReceiptDigest + " unlocks exactly the next arc",
			})
			reset = true
		}
		return reset, nil
	}
	for _, stage := range []string{"promote", "render"} {
		state.ClearDone(stage, domain.PipelineStageEvidence{
			Stage:     stage,
			Status:    "next_cycle",
			CheckedAt: time.Now(),
			Message:   "previous sealed chapter accepted; advance to the next immutable bundle",
		})
	}
	return true, nil
}

func splitPipelineRenderRecoveryPending(outputDir string, state *domain.PipelineState) (bool, error) {
	if state == nil || state.Done("render") ||
		(!pipelineStateHasStage(state, "preplan") && !pipelineStateHasStage(state, "promote")) ||
		!pipelineStateHasStage(state, "render") {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(outputDir, pipelineFrozenPlanPath)); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("检查 render recovery 冻结计划失败: %w", err)
	}
	frozen, _, err := loadAndVerifyPipelineFrozenPlan(outputDir)
	if err != nil {
		// A fresh split cycle has no frozen render baseline until plan
		// completes. Missing is the normal preplan state, while an existing
		// but invalid frozen artifact must still fail closed.
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("render recovery 的冻结计划不可验证: %w", err)
	}
	if _, committed := pipelineCommittedAfterFrozenBaseline(store.NewStore(outputDir), frozen); !committed {
		return false, nil
	}
	// A durable commit may precede both the preplan rebase flag and the render
	// receipt. Conversely, the receipt may be durable while pipeline.json has
	// not yet marked render complete. In all three crash windows, preserve the
	// old preplan/plan cursor and replay render idempotently until MarkDone.
	return true, nil
}

func pipelineStateHasStage(state *domain.PipelineState, stage string) bool {
	if state == nil {
		return false
	}
	for _, item := range state.Stages {
		if item == stage {
			return true
		}
	}
	return false
}

func pipelineStagesConsumePublishedOutlineAll(stages []string) bool {
	for _, stage := range stages {
		switch normalizePipelineStageName(stage) {
		case "cocreate", "architect", "outline-all":
			continue
		default:
			return true
		}
	}
	return false
}

// pipelineStagesNeedQdrant keeps the phase boundary honest. outline-all sees
// only its bounded frozen-foundation/reference packet; preplan derives
// prose-free causal projections from versioned local artifacts; project-all
// consumes exactly one current arc; render consumes
// only the exact receipt-backed transformations frozen by plan. None of these
// phases may perform live retrieval. Formal planning and the remaining
// RAG-aware stages preserve Qdrant startup behavior.
func pipelineStagesNeedQdrant(stages []string) bool {
	for _, stage := range stages {
		switch normalizePipelineStageName(stage) {
		case "outline-all", "preplan", "project-all", "seal", "promote", "render":
			continue
		default:
			return true
		}
	}
	return false
}

func verifyStoredPipelineArtifactDigests(outputDir string, evidence domain.PipelineStageEvidence) error {
	for artifact, expected := range evidence.ArtifactDigests {
		if pipelineStageArtifactDigestIsMutable(evidence.Stage, artifact) {
			continue
		}
		if strings.TrimSpace(expected) == "" {
			continue
		}
		path := artifact
		if !filepath.IsAbs(path) {
			path = filepath.Join(outputDir, filepath.FromSlash(artifact))
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("阶段产物 %s 无法读取: %w", artifact, err)
		}
		sum := sha256.Sum256(raw)
		actual := "sha256:" + hex.EncodeToString(sum[:])
		if actual != expected {
			return fmt.Errorf("阶段产物 %s 指纹漂移（expected=%s actual=%s）", artifact, expected, actual)
		}
	}
	return nil
}

func stampPipelineArtifactDigests(outputDir string, evidence domain.PipelineStageEvidence) domain.PipelineStageEvidence {
	if len(evidence.Artifacts) == 0 {
		return evidence
	}
	evidence.ArtifactDigests = make(map[string]string, len(evidence.Artifacts))
	for _, artifact := range evidence.Artifacts {
		if pipelineStageArtifactDigestIsMutable(evidence.Stage, artifact) {
			continue
		}
		path := artifact
		if !filepath.IsAbs(path) {
			path = filepath.Join(outputDir, filepath.FromSlash(artifact))
		}
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			continue
		}
		sum := sha256.Sum256(raw)
		evidence.ArtifactDigests[artifact] = "sha256:" + hex.EncodeToString(sum[:])
	}
	if len(evidence.ArtifactDigests) == 0 {
		evidence.ArtifactDigests = nil
	}
	return evidence
}

// Historical pipeline states may have hashed live ledgers/control cursors as
// if they were immutable stage products. Ignore those known mutable paths both
// when verifying old evidence and when stamping new evidence; their semantics
// are checked by the owning stage against current control state instead.
func pipelineStageArtifactDigestIsMutable(stage, artifact string) bool {
	artifact = filepath.ToSlash(strings.TrimSpace(artifact))
	switch strings.TrimSpace(stage) {
	case "zero-init":
		return artifact == "meta/world_tick.json" || artifact == "meta/world_events.jsonl"
	case "seal":
		return artifact == "meta/planning/v2/realization_cursor.json"
	default:
		return false
	}
}

func pipelineStageArgs(flags pipelineFlags) map[string][]string {
	args := make(map[string][]string)
	var review []string
	var rewrite []string
	if flags.Start > 0 {
		value := strconv.Itoa(flags.Start)
		review = append(review, "--from", value)
		rewrite = append(rewrite, "--from", value)
	}
	if flags.End > 0 {
		value := strconv.Itoa(flags.End)
		review = append(review, "--to", value)
		rewrite = append(rewrite, "--to", value)
	}
	if flags.Budget > 0 {
		value := flags.Budget.String()
		review = append(review, "--budget", value)
		rewrite = append(rewrite, "--budget", value)
	}
	if strings.TrimSpace(flags.Role) != "" {
		rewrite = append(rewrite, "--role", flags.Role)
	}
	if flags.MaxRewriteRounds > 0 {
		rewrite = append(rewrite, "--max-rounds", strconv.Itoa(flags.MaxRewriteRounds))
	}
	if flags.PolishWarnings {
		rewrite = append(rewrite, "--polish-warnings")
	}
	if flags.RewriteBriefOnly {
		rewrite = append(rewrite, "--brief-only")
	}
	if len(review) > 0 {
		args["review"] = review
	}
	if len(rewrite) > 0 {
		args["rewrite"] = rewrite
	}
	return args
}

func runPipelineStage(stage string, opts cliOptions, flags pipelineFlags, state *domain.PipelineState, stageArgs map[string][]string) error {
	switch stage {
	case "cocreate":
		draft, err := runCoCreate(opts, state.Prompt)
		if err != nil {
			return err
		}
		state.Prompt = draft
		return nil
	case "architect":
		return runPipelineFoundationStage(opts, "architect", func() error {
			return pipelineArchitect(opts, flags, state)
		})
	case "outline-all":
		return pipelineOutlineAll(opts, flags)
	case "zero-init":
		return runPipelineFoundationStage(opts, "zero-init", func() error {
			return pipelineZeroInit(opts, flags, state)
		})
	case "preplan":
		return pipelinePreplan(opts, flags)
	case "project-all":
		return pipelineProjectAll(opts, flags)
	case "seal":
		return pipelineSeal(opts, flags)
	case "promote":
		return pipelinePromote(opts, flags)
	case "plan":
		return pipelinePlan(opts, flags)
	case "render":
		return pipelineRender(opts, flags, state)
	case "write":
		return pipelineWrite(opts, flags, state)
	case "review":
		return reviewExistingPipeline(opts, stageArgs["review"])
	case "rewrite":
		return pipelineCausalRewrite(opts, flags, state, stageArgs["review"], stageArgs["rewrite"])
	case "deliver":
		cfg, _, err := loadCfgBundle(opts)
		if err != nil {
			return err
		}
		if err := ensurePipelineRAGReady(cfg); err != nil {
			return fmt.Errorf("deliver 阶段 RAG 就绪检查失败: %w", err)
		}
		return settlePipelineDelivery(cfg.OutputDir, flags)
	default:
		return fmt.Errorf("未知阶段：%s", stage)
	}
}

func runPipelineFoundationStage(
	opts cliOptions,
	stage string,
	run func() error,
) (returnErr error) {
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	st := store.NewStore(cfg.OutputDir)
	if err := st.Init(); err != nil {
		return err
	}
	if mode, err := st.LoadWritingPipelineMode(); err != nil {
		return err
	} else if mode != nil && mode.Mode == domain.WritingPipelineModeSealedTwoPassV2 {
		if active, loadErr := st.ProjectedV2().LoadActiveGeneration(); loadErr != nil {
			return loadErr
		} else if active != nil {
			return fmt.Errorf(
				"%s 不能改写 active sealed generation %s 的 foundation；需要改变基础设定时必须显式 rebase/restart 全书",
				stage,
				active.GenerationID,
			)
		}
	}
	owner := pipelineExecutionOwner(stage, 1)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionFoundation,
		TargetChapter: 1,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(pipelineExecutionLease),
	}); err != nil {
		return fmt.Errorf("%s 获取 foundation execution lock: %w", stage, err)
	}
	defer func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("%s 释放 foundation execution lock: %w", stage, err)
		}
	}()
	return run()
}

func activatePipelineSealedTwoPassMode(opts cliOptions) error {
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	return activatePipelineSealedTwoPassModeAtOutput(cfg.OutputDir)
}

func activatePipelineSealedTwoPassModeAtOutput(outputDir string) error {
	st := store.NewStore(outputDir)
	if current, err := st.LoadWritingPipelineMode(); err != nil {
		return err
	} else if current != nil {
		if current.Mode != domain.WritingPipelineModeSealedTwoPassV2 {
			return fmt.Errorf("unsupported existing writing pipeline mode %q", current.Mode)
		}
		return nil
	}
	receipt := domain.WritingPipelineModeReceipt{
		Version:     domain.WritingPipelineModeReceiptVersion,
		Mode:        domain.WritingPipelineModeSealedTwoPassV2,
		ActivatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	digest, err := domain.ComputeWritingPipelineModeReceiptDigest(receipt)
	if err != nil {
		return err
	}
	receipt.ReceiptDigest = digest
	if err := st.SaveWritingPipelineMode(receipt); err != nil {
		return fmt.Errorf("启用 sealed_two_pass_v2: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[pipeline] 已锁定弧式 sealed 流程：推演并封存当前弧 → 逐章渲染/逐章审核 → 再进入下一弧")
	return nil
}

func requireNoPendingSealedSteer(st *store.Store, stage string) error {
	if st == nil {
		return fmt.Errorf("%s pending steer validation requires store", stage)
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		return fmt.Errorf("%s 读取 pending steer: %w", stage, err)
	}
	if meta != nil && strings.TrimSpace(meta.PendingSteer) != "" {
		return fmt.Errorf(
			"%s 禁止消费未封存的 pending steer；请先把该指令纳入稳定 foundation/创作规则并用 --restart 重推全书（pending=%q）",
			stage,
			strings.TrimSpace(meta.PendingSteer),
		)
	}
	return nil
}

// warnStaleZeroInitReadiness 写前检查零章就绪指纹（Task 052）：readiness 缺 schema_version
// 或低于当前版本时一律视为 not ready 并告警——防止旧版生成器的 ready:true 被误信。
// 这里只负责提前告警；第 1 章前的实际阻断由共享的
// tools.ZeroInitReadinessState 执行。已完成第 1 章的历史项目不会被倒逼重跑。
func warnStaleZeroInitReadiness(outputDir string) {
	data, err := os.ReadFile(filepath.Join(outputDir, "meta", "first_chapter_generation_readiness.json"))
	if err != nil {
		return // 无 readiness（未走 zero-init 的项目）不告警
	}
	var r zeroInitReadiness
	if err := json.Unmarshal(data, &r); err != nil {
		fmt.Fprintln(os.Stderr, "[pipeline] ⚠ first_chapter_generation_readiness.json 无法解析，视为 not ready，建议重跑 --zero-init")
		return
	}
	if r.SchemaVersion < zeroReadinessSchemaVersion {
		fmt.Fprintf(os.Stderr, "[pipeline] ⚠ readiness 由旧版生成器产出（schema_version=%d < %d），其 ready=%v 不可信，视为 not ready：请先重跑 --zero-init --overwrite\n", r.SchemaVersion, zeroReadinessSchemaVersion, r.Ready)
	}
}
