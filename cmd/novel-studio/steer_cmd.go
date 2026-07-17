package main

// --steer "<指令>"：为当前项目排队一条干预指令。无运行中的 coordinator 时，
// Host.Steer 会把指令持久化到 run.json，下次 --headless 启动 / 恢复时自动注入。
// 替代原 TUI 底部输入框的实时干预——一次性进程无法直接注入另一进程里运行的会话，
// 因此本子命令做的是「排队待下次生效」。

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func parseSteerFlags(argv []string) ([]string, error) {
	fs := flag.NewFlagSet("steer", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --steer \"把感情线提前到第4章\"\n\n")
		fmt.Fprintf(os.Stderr, "为当前项目排队一条干预指令，下次启动 / 恢复时自动注入 Coordinator。\n")
	}
	if err := fs.Parse(argv); err != nil {
		return nil, err
	}
	return fs.Args(), nil
}

// hasSteerFlag 判断 argv 中是否含 --steer 入口。
func hasSteerFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--steer" {
			return true
		}
	}
	return false
}

func steerPipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _ = parseSteerFlags([]string{"--help"})
		return nil
	}
	rest, err := parseSteerFlags(args)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(strings.Join(rest, " "))
	if text == "" {
		return fmt.Errorf("--steer 需要一条干预指令文本，例如：--steer \"加快节奏\"")
	}
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if mode, modeErr := store.NewStore(cfg.OutputDir).LoadWritingPipelineMode(); modeErr != nil {
		return fmt.Errorf("--steer 读取 writing pipeline mode: %w", modeErr)
	} else if mode != nil && mode.Mode == domain.WritingPipelineModeSealedTwoPassV2 {
		return fmt.Errorf(
			"项目已启用 sealed_two_pass_v2，不能把临时 --steer 注入封存计划或正文渲染；请把指令写入稳定创作规则后用 --pipeline --stages preplan,project-all,seal --restart 重推全书",
		)
	}

	eng, cleanup, err := newExistingHost(opts)
	if err != nil {
		return err
	}
	defer cleanup()

	// 一次性进程里 coordinator 不在运行，Steer 会落盘为 pending，下次启动生效。
	eng.Steer(text)
	fmt.Fprintf(os.Stderr, "[steer] 已排队干预指令，下次启动 / 恢复时生效：%s\n", text)
	return nil
}
