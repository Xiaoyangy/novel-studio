package eval

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
)

// InspectCommand 对既有 output/novel 目录做离线 harness 检查，不启动模型生成。
func InspectCommand(argv []string) int {
	fs := flag.NewFlagSet("eval inspect", flag.ContinueOnError)
	casesPath := fs.String("cases", "", "inspect case 目录或单个 .json 文件（必填）")
	dirOverride := fs.String("dir", "", "覆盖 case artifact_dir，检查指定 output/novel 目录")
	configPath := fs.String("config", "", "配置文件路径（仅用于 fail-loud 校验配置可解析）")
	outDir := fs.String("out", "", "报告输出目录（缺省 workspace/evals/<run_id>）")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if strings.TrimSpace(*casesPath) == "" {
		fmt.Fprintln(os.Stderr, "eval inspect: 缺少 --cases")
		fs.Usage()
		return 2
	}
	if _, err := bootstrap.LoadConfig(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "eval inspect: 加载配置失败: %v\n", err)
		return 2
	}
	cases, err := LoadCases(*casesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval inspect: 加载 case 失败: %v\n", err)
		return 2
	}

	runID := time.Now().Format("20060102-150405")
	if *outDir == "" {
		*outDir = filepath.Join("workspace", "evals", runID)
	}
	fmt.Fprintf(os.Stderr, "eval inspect %s · %d cases\n", runID, len(cases))

	caseResults := make([]CaseResult, 0, len(cases))
	for _, c := range cases {
		dir := strings.TrimSpace(*dirOverride)
		if dir == "" {
			dir = strings.TrimSpace(c.ArtifactDir)
		}
		if dir == "" {
			fmt.Fprintf(os.Stderr, "eval inspect: case %s 缺少 artifact_dir（或 --dir）\n", c.ID)
			return 2
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval inspect: case %s 解析目录失败: %v\n", c.ID, err)
			return 2
		}
		fmt.Fprintf(os.Stderr, "\n▶ %s (%s)\n  dir=%s\n", c.ID, c.Category, absDir)
		res := Grade(c, Collect(absDir, nil))
		res.Arm, res.Repeat = ArmSingle, 1
		caseResults = append(caseResults, NewSingleRunsCaseResult(c, []RunResult{{
			Arm:    ArmSingle,
			Repeat: 1,
			Result: res,
		}}))
		fmt.Fprintf(os.Stderr, "  → inspect %s\n", res.Outcome)
	}

	suite := Aggregate(runID, "inspect", "", 1, caseResults)
	if err := WriteReport(suite, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "eval inspect: 写报告失败: %v\n", err)
		return 2
	}
	fmt.Fprintf(os.Stderr, "\n%s\n报告: %s\n", Summary(suite), filepath.Join(*outDir, "report.md"))
	if suite.Gate == Fail {
		return 1
	}
	return 0
}
