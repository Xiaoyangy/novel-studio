package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/voocel/agentcore/schema"
)

// SaveBrainstormTool 头脑风暴阶段的落盘入口：把确认好的小说思路写成 brainstorm.md，
// 存到项目根（data/runs/<小说名>/brainstorm.md）。这是整本小说产出的基础文件，
// Architect 初始化世界前必须以它为依据。
//
// 设计：工具只做"必填字段校验 + 确定性渲染成 md"；头脑风暴的研究/推敲（RAG+联网）
// 由 brainstorm 子代理在调用本工具之前完成，结论浓缩进各字段。
type SaveBrainstormTool struct {
	// runsRoot 是 data/runs；实际项目目录 = runsRoot/<安全化书名>，由 title 派生。
	// 新建小说时书名在头脑风暴中才确定，故目录随 title 生成。
	runsRoot string
	// savedDir 记录本次落盘的项目目录，供调用方（pipeline）读取以设定 OutputDir。
	savedDir string
}

func NewSaveBrainstormTool(runsRoot string) *SaveBrainstormTool {
	return &SaveBrainstormTool{runsRoot: runsRoot}
}

// SavedDir 返回最近一次 save_brainstorm 落盘的项目根目录（data/runs/<书名>）。
func (t *SaveBrainstormTool) SavedDir() string { return t.savedDir }

func (t *SaveBrainstormTool) Name() string  { return "save_brainstorm" }
func (t *SaveBrainstormTool) Label() string { return "保存头脑风暴" }
func (t *SaveBrainstormTool) Description() string {
	return "把头脑风暴确认好的小说思路落盘成 brainstorm.md（整本小说产出的基础文件，Architect 初始化世界的依据）。" +
		"调用前应已用 web_research/craft_recall 做过题材调研与逻辑推敲。所有核心字段必填。"
}
func (t *SaveBrainstormTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveBrainstormTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveBrainstormTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("title", schema.String("书名（也用于项目目录名）")).Required(),
		schema.Property("expected_word_count", schema.String("预期总字数（如 100-130 万字 / 3 万字短篇）")).Required(),
		schema.Property("genre", schema.String("题材（如 男频诡异末日+都市契约商战 / 女频古言宅斗）")).Required(),
		schema.Property("novel_type", schema.String("小说类型/结构（长篇连载多卷多弧 / 单卷短篇 / 单元剧…）")).Required(),
		schema.Property("logline", schema.String("一句话核心卖点")).Required(),
		schema.Property("premise_summary", schema.String("故事逻辑梗概：世界前提、核心冲突、主角目标与终局方向")).Required(),
		schema.Property("protagonist", schema.String("主角设定：身份、性格、初始处境、成长弧方向")).Required(),
		schema.Property("has_cp", schema.Bool("主角是否有 CP/感情线")).Required(),
		schema.Property("cp_setup", schema.String("若有 CP：对象设定与感情线走向；无 CP 填说明为何不设")),
		schema.Property("main_characters", schema.Array("关键角色设定（每条：姓名+定位+目标+与主角关系）", schema.String(""))).Required(),
		schema.Property("world_setting", schema.String("世界观要点：时代/地域/力量或规则体系/势力格局")).Required(),
		schema.Property("core_appeal", schema.Array("核心爽点/情绪兑现（读者为什么追看）", schema.String(""))).Required(),
		schema.Property("tone", schema.String("基调与叙事风格")),
		schema.Property("differentiation", schema.String("差异化：与同题材相比的独特点")),
		schema.Property("taboos", schema.Array("写作禁区（明确不写什么）", schema.String(""))),
		schema.Property("research_notes", schema.Array("题材调研要点（RAG/联网得到、可转化为设定的现实支架），每条注明来源", schema.String(""))),
		schema.Property("architect_handoff", schema.String("给 Architect 的交接说明：初始化世界时最需要先敲定什么")).Required(),
	)
}

type brainstormArgs struct {
	Title             string   `json:"title"`
	ExpectedWordCount string   `json:"expected_word_count"`
	Genre             string   `json:"genre"`
	NovelType         string   `json:"novel_type"`
	Logline           string   `json:"logline"`
	PremiseSummary    string   `json:"premise_summary"`
	Protagonist       string   `json:"protagonist"`
	HasCP             bool     `json:"has_cp"`
	CPSetup           string   `json:"cp_setup"`
	MainCharacters    []string `json:"main_characters"`
	WorldSetting      string   `json:"world_setting"`
	CoreAppeal        []string `json:"core_appeal"`
	Tone              string   `json:"tone"`
	Differentiation   string   `json:"differentiation"`
	Taboos            []string `json:"taboos"`
	ResearchNotes     []string `json:"research_notes"`
	ArchitectHandoff  string   `json:"architect_handoff"`
}

func (t *SaveBrainstormTool) Execute(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var a brainstormArgs
	if err := unmarshalToolArgs(raw, &a); err != nil {
		// 形状容错：与 plan 路径同一套反射归一化。
		if coerced, changed := coerceJSONShape(json.RawMessage(stripJSONWrapping(string(raw))), reflect.TypeFor[brainstormArgs]()); changed {
			_ = json.Unmarshal(coerced, &a)
		}
	}
	var missing []string
	req := func(ok bool, name string) {
		if !ok {
			missing = append(missing, name)
		}
	}
	req(strings.TrimSpace(a.Title) != "", "title")
	req(strings.TrimSpace(a.ExpectedWordCount) != "", "expected_word_count")
	req(strings.TrimSpace(a.Genre) != "", "genre")
	req(strings.TrimSpace(a.NovelType) != "", "novel_type")
	req(strings.TrimSpace(a.Logline) != "", "logline")
	req(strings.TrimSpace(a.PremiseSummary) != "", "premise_summary")
	req(strings.TrimSpace(a.Protagonist) != "", "protagonist")
	req(len(a.MainCharacters) > 0, "main_characters")
	req(strings.TrimSpace(a.WorldSetting) != "", "world_setting")
	req(len(a.CoreAppeal) > 0, "core_appeal")
	req(strings.TrimSpace(a.ArchitectHandoff) != "", "architect_handoff")
	if a.HasCP && strings.TrimSpace(a.CPSetup) == "" {
		missing = append(missing, "cp_setup(has_cp=true 时必填)")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("头脑风暴信息不完整，缺少：%s。请补齐后再落盘（这是整本小说产出的基础，Architect 会据此初始化）: %w",
			strings.Join(missing, ", "), errs.ErrToolArgs)
	}

	runDir := t.runsRoot
	if runDir == "" {
		runDir = filepath.Join("data", "runs")
	}
	// 项目目录按书名派生（新建小说时目录随 title 生成）。
	projectDir := filepath.Join(runDir, brainstormSafeDir(a.Title))
	md := renderBrainstormMarkdown(a)
	path := filepath.Join(projectDir, "brainstorm.md")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return nil, fmt.Errorf("create project dir: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		return nil, fmt.Errorf("write brainstorm.md: %w: %w", errs.ErrStoreWrite, err)
	}
	t.savedDir = projectDir
	return json.Marshal(map[string]any{
		"saved":            true,
		"path":             path,
		"project_dir":      projectDir,
		"title":            a.Title,
		"brainstorm_ready": true,
		"next_step":        "头脑风暴已落盘；后续 pipeline 会让 Architect 基于 brainstorm.md 初始化世界，再 zero-init 与写作。",
	})
}

func renderBrainstormMarkdown(a brainstormArgs) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — 小说思路（头脑风暴）\n\n", a.Title)
	fmt.Fprintf(&b, "> 本文件是整本小说产出的基础。Architect 初始化世界、zero-init 与后续写作都以它为依据。\n> 生成时间：%s\n\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "## 基本参数\n\n- 预期字数：%s\n- 题材：%s\n- 小说类型：%s\n- 基调风格：%s\n- 主角 CP：%s\n\n",
		a.ExpectedWordCount, a.Genre, a.NovelType, orDash(a.Tone), cpLabel(a.HasCP))
	fmt.Fprintf(&b, "## 一句话卖点\n\n%s\n\n", a.Logline)
	fmt.Fprintf(&b, "## 故事逻辑梗概\n\n%s\n\n", a.PremiseSummary)
	fmt.Fprintf(&b, "## 主角设定\n\n%s\n\n", a.Protagonist)
	if a.HasCP {
		fmt.Fprintf(&b, "## 感情线（CP）\n\n%s\n\n", a.CPSetup)
	} else if strings.TrimSpace(a.CPSetup) != "" {
		fmt.Fprintf(&b, "## 感情线\n\n无 CP：%s\n\n", a.CPSetup)
	}
	b.WriteString("## 关键角色\n\n")
	for _, c := range a.MainCharacters {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	fmt.Fprintf(&b, "\n## 世界观要点\n\n%s\n\n", a.WorldSetting)
	b.WriteString("## 核心爽点 / 情绪兑现\n\n")
	for _, c := range a.CoreAppeal {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	if strings.TrimSpace(a.Differentiation) != "" {
		fmt.Fprintf(&b, "\n## 差异化\n\n%s\n", a.Differentiation)
	}
	if len(a.Taboos) > 0 {
		b.WriteString("\n## 写作禁区\n\n")
		for _, c := range a.Taboos {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	if len(a.ResearchNotes) > 0 {
		b.WriteString("\n## 题材调研（RAG/联网）\n\n")
		for _, c := range a.ResearchNotes {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	fmt.Fprintf(&b, "\n## 给 Architect 的交接\n\n%s\n", a.ArchitectHandoff)
	return b.String()
}

// brainstormSafeDir 把书名净化为安全目录名（去路径分隔符与首尾空白）。
func brainstormSafeDir(title string) string {
	title = strings.TrimSpace(title)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", "\n", " ", "\t", " ")
	title = strings.TrimSpace(replacer.Replace(title))
	if title == "" {
		return "未命名小说"
	}
	return title
}

func cpLabel(has bool) string {
	if has {
		return "有"
	}
	return "无"
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
