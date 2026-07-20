package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	buildversion "github.com/chenhongyang/novel-studio/internal/version"
)

const architectReadinessSchemaVersion = 1
const architectFreshnessGrace = 2 * time.Second

type architectCheckFlags struct {
	Dir string
}

type architectReadiness struct {
	Ready            bool           `json:"ready"`
	SchemaVersion    int            `json:"schema_version"`
	GeneratorVersion string         `json:"generator_version,omitempty"`
	Missing          []string       `json:"missing,omitempty"`
	Issues           []string       `json:"issues,omitempty"`
	Warnings         []string       `json:"warnings,omitempty"`
	Stats            map[string]int `json:"stats,omitempty"`
	GeneratedAt      string         `json:"generated_at,omitempty"`
	Path             string         `json:"path,omitempty"`
}

var architectFoundationFreshnessFiles = []string{
	// book_world.json 是 authored foundation 的必需文件，但 save_world_tick 会常规更新
	// 其中的势力/资源进度钟；这类模拟状态更新不应让 Architect readiness 过期。
	"premise.md", "characters.json", "world_rules.json",
	"world_codex.json", "layered_outline.json", "outline.json", filepath.Join("meta", "compass.json"),
}

var architectLegacyBannedTerms = []string{
	"数据合规", "算法审计", "上市前夜", "Nora", "人效星图", "审计盒", "匿名样本M17",
	"匿名样本 M17", "封存回执", "字段来源", "补现场口径", "权限链", "变量链路",
	"测试样本口径", "药量遮盖", "现场纪要", "回执尾号", "申请核验原始读取链",
}

func hasArchitectCheckFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--architect-check" {
			return true
		}
	}
	return false
}

func parseArchitectCheckFlags(argv []string) (architectCheckFlags, []string, error) {
	fs := flag.NewFlagSet("architect-check", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --architect-check [--dir <output/novel>]\n\n")
		fmt.Fprintf(os.Stderr, "检查 Architect foundation 是否完整、干净且可供 zero-init 派生；通过后落盘 meta/architect_readiness.json/md。\n\n选项：\n")
		fs.PrintDefaults()
	}
	var f architectCheckFlags
	fs.StringVar(&f.Dir, "dir", "", "小说 output/novel 目录；为空时使用配置 OutputDir")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

func architectCheckPipeline(opts cliOptions, argv []string) error {
	if hasHelpToken(argv) {
		_, _, _ = parseArchitectCheckFlags([]string{"--help"})
		return nil
	}
	flags, extra, err := parseArchitectCheckFlags(argv)
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return fmt.Errorf("--architect-check 不接受额外参数：%v", extra)
	}
	dir, err := resolveArchitectCheckDir(opts, flags.Dir)
	if err != nil {
		return err
	}
	st := store.NewStore(dir)
	if mode, modeErr := st.LoadWritingPipelineMode(); modeErr != nil {
		return modeErr
	} else if mode != nil && mode.Mode == domain.WritingPipelineModeSealedTwoPassV2 {
		if active, loadErr := st.ProjectedV2().LoadActiveGeneration(); loadErr != nil {
			return loadErr
		} else if active != nil {
			return fmt.Errorf(
				"--architect-check 不能改写 active sealed generation %s 的 readiness 依赖",
				active.GenerationID,
			)
		}
		if cursor, loadErr := st.ProjectedV2().LoadProjectionCursor(); loadErr != nil {
			return loadErr
		} else if cursor != nil {
			return fmt.Errorf(
				"--architect-check 不能改写 generation %s 正在构建或已封存的 readiness 依赖",
				cursor.GenerationID,
			)
		}
	}
	readiness := assessArchitectReadiness(dir)
	if err := writeArchitectReadiness(dir, readiness); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, readiness.Path)
	if !readiness.Ready {
		return fmt.Errorf("Architect readiness 未通过：missing=%v issues=%v warnings=%v", readiness.Missing, readiness.Issues, readiness.Warnings)
	}
	return nil
}

func resolveArchitectCheckDir(opts cliOptions, explicit string) (string, error) {
	dir := strings.TrimSpace(explicit)
	if dir == "" {
		cfg, _, err := loadCfgBundle(opts)
		if err != nil {
			return "", err
		}
		dir = strings.TrimSpace(cfg.OutputDir)
	}
	if dir == "" {
		dir = filepath.Join("output", "novel")
	}
	return filepath.Abs(dir)
}

func assessArchitectReadiness(dir string) architectReadiness {
	var missing, issues, warnings []string
	stats := map[string]int{}
	missing = append(missing, tools.FoundationCoreMissing(dir)...)
	if !architectNonEmpty(filepath.Join(dir, "brainstorm.md")) && !architectNonEmpty(filepath.Join(dir, "..", "..", "brainstorm.md")) {
		warnings = append(warnings, "brainstorm.md 缺失或为空：Architect 应以头脑风暴交接为依据初始化 foundation")
	}

	st := store.NewStore(dir)
	premise, _ := st.Outline.LoadPremise()
	outline, _ := st.Outline.LoadOutline()
	layered, _ := st.Outline.LoadLayeredOutline()
	chars, _ := st.Characters.Load()
	rules, _ := st.World.LoadWorldRules()
	world, _ := st.World.LoadBookWorld()
	compass, _ := st.Outline.LoadCompass()

	stats["outline_chapters"] = len(outline)
	stats["characters"] = len(chars)
	stats["world_rules"] = len(rules)
	if world != nil {
		stats["book_world_places"] = len(world.Places)
		stats["book_world_factions"] = len(world.Factions)
		stats["book_world_routes"] = len(world.Routes)
		clocked := 0
		for _, faction := range world.Factions {
			if faction.Clock != nil {
				clocked++
			}
		}
		stats["book_world_faction_clocks"] = clocked
	}
	layeredTotal := 0
	if len(layered) > 0 {
		layeredTotal = domain.TotalChapters(layered)
		stats["layered_total_chapters"] = layeredTotal
		stats["volumes"] = len(layered)
	}

	if strings.TrimSpace(premise) == "" {
		issues = append(issues, "premise.md 为空或不可读")
	}
	if len(outline) == 0 {
		issues = append(issues, "outline.json 为空或不可读")
	}
	if len(layered) == 0 && len(outline) > 30 {
		warnings = append(warnings, "长篇项目建议使用 layered_outline.json 分卷分弧，避免百万字节奏被压成短篇")
	}
	if len(chars) < 2 {
		issues = append(issues, "characters.json 至少需要主角与核心关系角色")
	} else if len(chars) < 5 {
		warnings = append(warnings, "characters.json 少于 5 人：长篇项目建议补足主角朋友、女主闺蜜/朋友和阶段性对手")
	}
	if len(rules) == 0 {
		issues = append(issues, "world_rules.json 规则为空：zero-init 无法派生稳定世界边界")
	} else if len(rules) < 3 {
		warnings = append(warnings, "world_rules.json 少于 3 条：建议补时间尺度、资源边界、感情线边界")
	}
	if world == nil {
		issues = append(issues, "book_world.json 不存在或不可读")
	} else {
		if len(world.Places) == 0 || len(world.Factions) == 0 {
			warnings = append(warnings, "book_world.json places/factions 偏少：zero-init 可用空间与势力压力会偏弱")
		}
		if clockIssues := architectFactionClockIssues(world); len(clockIssues) > 0 {
			issues = append(issues, clockIssues...)
		}
		if relationIssues := world.ValidateFactionRelations(); len(relationIssues) > 0 {
			issues = append(issues, relationIssues...)
		}
	}
	if compass == nil || strings.TrimSpace(compass.EndingDirection) == "" || len(compass.OpenThreads) == 0 {
		issues = append(issues, "meta/compass.json 缺少终局方向或开放长线")
	}
	if layeredTotal > 0 {
		if progress, _ := st.Progress.Load(); progress != nil && progress.TotalChapters > 0 && progress.TotalChapters != layeredTotal {
			issues = append(issues, fmt.Sprintf("layered_outline 规划总章数=%d 与 progress.total_chapters=%d 不一致", layeredTotal, progress.TotalChapters))
		}
		if min, max, ok := architectChapterRangeFromPremise(premise); ok && (layeredTotal < min || layeredTotal > max) {
			issues = append(issues, fmt.Sprintf("layered_outline 规划总章数=%d 超出 premise 声明范围 %d-%d 章", layeredTotal, min, max))
		}
	}
	if len(outline) > 0 {
		first := outline[0]
		firstText := first.Title + " " + first.CoreEvent + " " + first.Hook + " " + strings.Join(first.Scenes, " ")
		if strings.TrimSpace(first.Title) == "" || strings.TrimSpace(first.CoreEvent) == "" {
			issues = append(issues, "第一章大纲缺少 title/core_event")
		}
		if len([]rune(firstText)) < 20 {
			warnings = append(warnings, "第一章大纲信息量偏少：建议补开场场景、冲突触发、钩子和关系位移")
		}
	}
	coreImportant := 0
	for _, c := range chars {
		tier := strings.TrimSpace(c.Tier)
		if tier == "core" || tier == "important" || tier == "" {
			coreImportant++
		}
	}
	if coreImportant < 2 {
		issues = append(issues, "core/important 角色不足，至少需要主角与核心关系角色")
	} else if coreImportant < 5 {
		warnings = append(warnings, "core/important 角色少于 5 人：长篇互动和配角助攻空间会偏窄")
	}

	if codexIssues := architectWorldCodexIssues(filepath.Join(dir, "world_codex.json")); len(codexIssues) > 0 {
		issues = append(issues, codexIssues...)
	}

	readiness := architectReadiness{
		Ready:            len(missing) == 0 && len(issues) == 0,
		SchemaVersion:    architectReadinessSchemaVersion,
		GeneratorVersion: buildversion.Resolve(buildversion.Info{Version: version}).Version,
		Missing:          missing,
		Issues:           issues,
		Warnings:         warnings,
		Stats:            stats,
		GeneratedAt:      time.Now().Format(time.RFC3339),
		Path:             filepath.Join(dir, "meta", "architect_readiness.md"),
	}
	return readiness
}

func architectFactionClockIssues(world *domain.BookWorld) []string {
	if world == nil {
		return nil
	}
	var issues []string
	for i, faction := range world.Factions {
		label := strings.TrimSpace(faction.Name)
		if label == "" {
			label = strings.TrimSpace(faction.ID)
		}
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		if faction.Clock == nil {
			issues = append(issues, fmt.Sprintf("book_world.factions[%s] 缺少势力进度钟 clock", label))
			continue
		}
		if faction.Clock.Segments <= 0 {
			issues = append(issues, fmt.Sprintf("book_world.factions[%s].clock.segments 必须大于 0", label))
		}
		if faction.Clock.Progress < 0 {
			issues = append(issues, fmt.Sprintf("book_world.factions[%s].clock.progress 不能为负", label))
		}
		if faction.Clock.Segments > 0 && faction.Clock.Progress > faction.Clock.Segments {
			issues = append(issues, fmt.Sprintf("book_world.factions[%s].clock.progress 不能大于 segments", label))
		}
		if strings.TrimSpace(faction.Clock.Consequence) == "" {
			issues = append(issues, fmt.Sprintf("book_world.factions[%s].clock.consequence 不能为空", label))
		}
	}
	return issues
}

func architectWorldCodexIssues(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var codex domain.WorldCodex
	if err := json.Unmarshal(data, &codex); err != nil {
		return []string{"world_codex.json 不是有效 WorldCodex JSON"}
	}
	var issues []string
	if len(codex.Sections) < len(domain.RequiredCodexSections) {
		issues = append(issues, fmt.Sprintf("world_codex.sections=%d，少于必需维度 %d", len(codex.Sections), len(domain.RequiredCodexSections)))
	}
	for _, section := range codex.Sections {
		if strings.TrimSpace(section.Key) == "" {
			issues = append(issues, "world_codex.sections 存在空 key")
			continue
		}
		if !section.NotApplicable && strings.TrimSpace(section.Content) == "" && len(section.Rules) == 0 {
			issues = append(issues, "world_codex.sections["+section.Key+"] 缺少 content/rules")
		}
		if section.NotApplicable && strings.TrimSpace(section.Reason) == "" {
			issues = append(issues, "world_codex.sections["+section.Key+"] 标为 not_applicable 但缺少 reason")
		}
	}
	if strings.TrimSpace(codex.ImmutabilityPolicy) == "" {
		issues = append(issues, "world_codex.immutability_policy 不能为空")
	}
	return issues
}

func architectChapterRangeFromPremise(premise string) (int, int, bool) {
	exactArabic := regexp.MustCompile(`(?:全书|正文|共|总计|规划|计划|建议|预计|预期|目标)\s*(\d{1,3})\s*章`)
	if m := exactArabic.FindStringSubmatch(premise); len(m) == 2 {
		if total, err := strconv.Atoi(m[1]); err == nil && total > 0 {
			return total, total, true
		}
	}
	exactChinese := regexp.MustCompile(`(?:全书|正文|共|总计|规划|计划|建议|预计|预期|目标)\s*([零〇一二两三四五六七八九十百]+)\s*章`)
	if m := exactChinese.FindStringSubmatch(premise); len(m) == 2 {
		if total, ok := architectChineseChapterNumber(m[1]); ok {
			return total, total, true
		}
	}
	re := regexp.MustCompile(`(\d{1,3})\s*[-—~到至]\s*(\d{1,3})\s*章`)
	for _, idx := range re.FindAllStringSubmatchIndex(premise, -1) {
		if len(idx) != 6 {
			continue
		}
		// “第4—5章前/第4—5章完成”是章位，不是全书章数声明。
		prefix := strings.TrimSpace(premise[:idx[0]])
		if strings.HasSuffix(prefix, "第") {
			continue
		}
		min, err1 := strconv.Atoi(premise[idx[2]:idx[3]])
		max, err2 := strconv.Atoi(premise[idx[4]:idx[5]])
		if err1 == nil && err2 == nil && min > 0 && max >= min {
			return min, max, true
		}
	}
	return 0, 0, false
}

func architectChineseChapterNumber(raw string) (int, bool) {
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	total, current := 0, 0
	for _, r := range raw {
		if value, ok := digits[r]; ok {
			current = value
			continue
		}
		switch r {
		case '十':
			if current == 0 {
				current = 1
			}
			total += current * 10
			current = 0
		case '百':
			if current == 0 {
				current = 1
			}
			total += current * 100
			current = 0
		default:
			return 0, false
		}
	}
	total += current
	return total, total > 0
}

func writeArchitectReadiness(dir string, readiness architectReadiness) error {
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(readiness, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "architect_readiness.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Architect Readiness\n\n")
	fmt.Fprintf(&b, "- ready: %v\n", readiness.Ready)
	fmt.Fprintf(&b, "- generated_at: %s\n", readiness.GeneratedAt)
	if len(readiness.Missing) > 0 {
		fmt.Fprintf(&b, "\n## Missing\n\n")
		for _, item := range readiness.Missing {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(readiness.Issues) > 0 {
		fmt.Fprintf(&b, "\n## Issues\n\n")
		for _, item := range readiness.Issues {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(readiness.Warnings) > 0 {
		fmt.Fprintf(&b, "\n## Warnings\n\n")
		for _, item := range readiness.Warnings {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(readiness.Stats) > 0 {
		fmt.Fprintf(&b, "\n## Stats\n\n")
		keys := []string{
			"volumes", "layered_total_chapters", "outline_chapters", "characters",
			"world_rules", "book_world_places", "book_world_factions", "book_world_faction_clocks", "book_world_routes",
		}
		for _, key := range keys {
			if v, ok := readiness.Stats[key]; ok {
				fmt.Fprintf(&b, "- %s: %d\n", key, v)
			}
		}
	}
	return os.WriteFile(filepath.Join(dir, "meta", "architect_readiness.md"), []byte(b.String()), 0o644)
}

func architectReadinessState(dir string) (bool, string) {
	path := filepath.Join(dir, "meta", "architect_readiness.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "meta/architect_readiness.json 不存在；请先运行 novel-studio --architect-check --dir <output/novel>"
	}
	var r architectReadiness
	if err := json.Unmarshal(data, &r); err != nil {
		return false, "architect_readiness.json 无法解析，需重跑 --architect-check"
	}
	if r.SchemaVersion < architectReadinessSchemaVersion {
		return false, fmt.Sprintf("architect_readiness schema_version=%d 过旧，需重跑 --architect-check", r.SchemaVersion)
	}
	if !r.Ready {
		return false, fmt.Sprintf("Architect 未通过（missing=%d issues=%d warnings=%d，详见 meta/architect_readiness.md）", len(r.Missing), len(r.Issues), len(r.Warnings))
	}
	generatedAt, err := time.Parse(time.RFC3339, r.GeneratedAt)
	if err != nil {
		return false, "architect_readiness.generated_at 无法解析，需重跑 --architect-check"
	}
	for _, rel := range architectFoundationFreshnessFiles {
		if rel == "outline.json" &&
			nonEmptyFile(filepath.Join(dir, "layered_outline.json")) {
			// In a layered project, preplan deterministically refreshes the flat
			// outline as a compatibility/index view. layered_outline.json is the
			// authored foundation and remains freshness-checked; touching its
			// derived flat projection must not invalidate the Architect receipt
			// between preplan and project-all.
			continue
		}
		info, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		if info.ModTime().After(generatedAt.Add(architectFreshnessGrace)) {
			return false, fmt.Sprintf("%s 在 Architect 检查之后被更新；请重跑 --architect-check 后再 zero-init", rel)
		}
	}
	return true, ""
}

func architectNonEmpty(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
