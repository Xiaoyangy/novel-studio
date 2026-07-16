package main

// --check：LLM 连通性自检。加载配置，为默认模型与各角色模型做一次最小真实
// Generate 往返，逐一报告 provider/model 是否真的可用。
// 设计动机：配置看起来对、但代理没起 / key 失效 / base_url 写错时，创作会在第一次
// LLM 调用才崩。自检把这一步前置，给出每个目标的明确成败与错误原因。

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/voocel/agentcore"
)

// checkRoles 是参与自检的角色顺序；"default" 代表顶层默认模型。
var checkRoles = []string{"default", "coordinator", "architect", "writer", "drafter", "editor", "reviewer"}

type checkFlags struct {
	Timeout  time.Duration
	Provider string // 仅测指定 provider（覆盖配置默认），需配 --model
	Model    string
}

func parseCheckFlags(argv []string) (checkFlags, []string, error) {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --check [--timeout 30s] [--provider <name> --model <model>]\n\n")
		fmt.Fprintf(os.Stderr, "对默认模型与各角色模型做一次最小真实调用，逐一报告是否可用。\n")
		fmt.Fprintf(os.Stderr, "指定 --provider/--model 时只测该目标（不改配置），用于验证某个备用 provider。\n\n选项：\n")
		fs.PrintDefaults()
	}
	f := checkFlags{Timeout: 30 * time.Second}
	fs.DurationVar(&f.Timeout, "timeout", f.Timeout, "单次连通性调用的超时")
	fs.StringVar(&f.Provider, "provider", "", "只测指定 provider（配置里 providers 的 key 名），需配 --model")
	fs.StringVar(&f.Model, "model", "", "配合 --provider 指定要测的模型名")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

// hasCheckFlag 判断 argv 中是否含 --check 入口。
func hasCheckFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--check" {
			return true
		}
	}
	return false
}

// checkServe 记录一个目标为哪个角色、以什么身份（主 / 兜底）服务。
type checkServe struct {
	role     string
	fallback bool
}

// checkTarget 是去重后的一个待测目标：一个 provider/model 组合（含已构建的模型实例），
// serves 记录它服务的所有 role + 身份，ok 是 ping 结果。
type checkTarget struct {
	provider string
	model    string
	chat     agentcore.ChatModel
	serves   []checkServe
	ok       bool
}

func (t checkTarget) label() string {
	parts := make([]string, 0, len(t.serves))
	for _, s := range t.serves {
		kind := "主"
		if s.fallback {
			kind = "兜底"
		}
		parts = append(parts, s.role+"("+kind+")")
	}
	return strings.Join(parts, ", ")
}

func checkPipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _, _ = parseCheckFlags([]string{"--help"})
		return nil
	}
	flags, extra, err := parseCheckFlags(args)
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return fmt.Errorf("--check 不接受额外参数：%v", extra)
	}
	if (flags.Provider == "") != (flags.Model == "") {
		return fmt.Errorf("--provider 和 --model 必须同时指定")
	}

	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return fmt.Errorf("尚未配置，请先在交互终端运行一次 novel-studio 完成配置引导，或手写配置文件")
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	// --provider/--model：只测这一个目标。把它设为默认、清空角色覆盖，复用同一条路径。
	if flags.Provider != "" {
		if _, ok := cfg.Providers[flags.Provider]; !ok {
			return fmt.Errorf("配置里没有名为 %q 的 provider", flags.Provider)
		}
		cfg.Provider = flags.Provider
		cfg.ModelName = flags.Model
		cfg.Roles = nil
	}

	ms, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return fmt.Errorf("构建模型失败（配置层就不通，无需联网即失败）: %w", err)
	}

	// 按 provider/model 去重：多个角色常指向同一模型，只需 ping 一次。
	targets := dedupeCheckTargets(ms)

	fmt.Fprintf(os.Stderr, "[check] 共 %d 个待测模型目标（含兜底，超时 %s/个）\n\n", len(targets), flags.Timeout)
	for i := range targets {
		latency, perr := pingModel(targets[i].chat, flags.Timeout)
		targets[i].ok = perr == nil
		if perr != nil {
			fmt.Fprintf(os.Stdout, "✗ %s/%s  [%s]\n    %v\n", targets[i].provider, targets[i].model, targets[i].label(), perr)
			continue
		}
		fmt.Fprintf(os.Stdout, "✓ %s/%s  [%s]  %dms\n", targets[i].provider, targets[i].model, targets[i].label(), latency.Milliseconds())
	}

	// 按角色汇总可用性：只要主或任一兜底可用，该角色就能创作（必要时走兜底）。
	return reportRoleUsability(targets)
}

// reportRoleUsability 按角色判定可用性：主可用为最佳；主挂但兜底可用记为"降级可用"；
// 主与全部兜底都挂才算该角色不可用。退出码只看"是否每个角色都至少有一条可用路径"。
func reportRoleUsability(targets []checkTarget) error {
	primaryOK := map[string]bool{}
	fallbackOK := map[string]bool{}
	roleSeen := map[string]bool{}
	var order []string
	for _, t := range targets {
		for _, s := range t.serves {
			if !roleSeen[s.role] {
				roleSeen[s.role] = true
				order = append(order, s.role)
			}
			if t.ok {
				if s.fallback {
					fallbackOK[s.role] = true
				} else {
					primaryOK[s.role] = true
				}
			}
		}
	}

	fmt.Fprintln(os.Stderr, "\n按角色汇总：")
	var unusable, degraded []string
	for _, role := range order {
		switch {
		case primaryOK[role]:
			fmt.Fprintf(os.Stderr, "  ✓ %s：主模型可用\n", role)
		case fallbackOK[role]:
			degraded = append(degraded, role)
			fmt.Fprintf(os.Stderr, "  ⚠ %s：主模型不可用，已可走兜底\n", role)
		case role == "default":
			// default 是未显式配置角色的兜底模板；具名角色都配齐时它不参与创作主流程，
			// 故仅提示、不计入硬失败（若具名角色没配，它们会解析到 default 并各自触发失败）。
			fmt.Fprintf(os.Stderr, "  ⚠ %s：默认模型不可用（仅影响未配置兜底的辅助路径，如共创）\n", role)
		default:
			unusable = append(unusable, role)
			fmt.Fprintf(os.Stderr, "  ✗ %s：主与兜底均不可用\n", role)
		}
	}

	fmt.Fprintln(os.Stderr)
	if len(unusable) > 0 {
		return fmt.Errorf("以下角色无任何可用模型：%s（常见原因：代理未启动 / api_key 失效 / base_url 写错）", strings.Join(unusable, ", "))
	}
	if len(degraded) > 0 {
		fmt.Fprintf(os.Stderr, "[check] 可创作（降级）：%s 将走兜底；如需主模型请启动其 provider。\n", strings.Join(degraded, ", "))
		return nil
	}
	fmt.Fprintln(os.Stderr, "[check] 全部角色主模型可用 ✓")
	return nil
}

// dedupeCheckTargets 收集各角色的主模型与兜底模型，按 provider/model 组合去重，
// 记录每个目标服务哪些角色（主 / 兜底），并带上已构建的模型实例供 ping。
func dedupeCheckTargets(ms *bootstrap.ModelSet) []checkTarget {
	var targets []checkTarget
	index := make(map[string]int) // "provider\x00model" → targets 下标
	add := func(provider, model string, serve checkServe, chat agentcore.ChatModel) {
		key := provider + "\x00" + model
		if i, ok := index[key]; ok {
			targets[i].serves = append(targets[i].serves, serve)
			return
		}
		index[key] = len(targets)
		targets = append(targets, checkTarget{provider: provider, model: model, chat: chat, serves: []checkServe{serve}})
	}
	for _, role := range checkRoles {
		provider, model, _ := ms.CurrentSelection(role)
		add(provider, model, checkServe{role: role}, ms.ForRole(role))
		for _, fb := range ms.FallbackTargets(role) {
			add(fb.Provider, fb.Model, checkServe{role: role, fallback: true}, fb.ChatModel)
		}
	}
	return targets
}

// pingModel 发一条最小请求，确认模型真能返回内容。返回往返耗时。
func pingModel(model agentcore.ChatModel, timeout time.Duration) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	resp, err := model.Generate(ctx,
		[]agentcore.Message{
			{Role: "user", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: "ping，请只回复：ok"}}},
		},
		nil,
	)
	if err != nil {
		return 0, err
	}
	if resp == nil || resp.Message.TextContent() == "" {
		return 0, fmt.Errorf("模型返回空响应（连接通但无内容，疑似代理/模型名问题）")
	}
	return time.Since(start), nil
}
