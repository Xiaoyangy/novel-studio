package assets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/tools"
)

//go:embed prompts/*.md
var promptsFS embed.FS

//go:embed references
var referencesFS embed.FS

//go:embed styles/*.md
var stylesFS embed.FS

// Prompts 表示嵌入的提示词集合。
type Prompts struct {
	Coordinator      string
	ArchitectShort   string
	ArchitectLong    string
	Brainstorm       string
	Planner          string
	Writer           string
	Drafter          string
	Editor           string
	SimulationSource string
	SimulationMerge  string
}

// Bundle 表示运行所需的静态资源集合。
type Bundle struct {
	References tools.References
	Prompts    Prompts
	Styles     map[string]string
}

// ResolvedStyle is the effective configured asset identity and body after
// defaulting/fallback. Callers that bind provenance must use both fields from
// the same resolution instead of pairing a missing requested ID with the
// default asset body.
type ResolvedStyle struct {
	ID   string
	Body string
}

// ResolveStyle resolves the configured prose-style asset atomically. Empty or
// unknown/non-content IDs both resolve to the default ID and default body.
func (b Bundle) ResolveStyle(style string) ResolvedStyle {
	style = strings.TrimSpace(style)
	if style == "" {
		style = "default"
	}
	if raw := b.Styles[style]; strings.TrimSpace(raw) != "" {
		return ResolvedStyle{ID: style, Body: raw}
	}
	return ResolvedStyle{ID: "default", Body: b.Styles["default"]}
}

// SelectedStyle returns the configured prose-style asset with the same
// defaulting rule used by references. Keeping selection in one place prevents
// an empty config style from silently dropping the render-only style contract.
func (b Bundle) SelectedStyle(style string) string {
	return b.ResolveStyle(style).Body
}

// Load 返回指定风格对应的资源集合。
func Load(style string) Bundle {
	styles := loadStyles()
	effectiveStyle := (Bundle{Styles: styles}).ResolveStyle(style)
	return Bundle{
		References: loadReferences(effectiveStyle.ID),
		Prompts:    loadPrompts(),
		Styles:     styles,
	}
}

// PromptProvenance 一个核心 prompt 的来源与内容指纹，回答"这次 run 用的是哪版 prompt"。
type PromptProvenance struct {
	Name        string `json:"name"`        // "writer.md"
	Source      string `json:"source"`      // builtin / override:<dir>
	Fingerprint string `json:"fingerprint"` // 原始内容 sha256 前 12 位
}

// LoadWithOverrides 在 Load 基础上支持核心 prompt 的运行时覆盖（与 config/rules 的
// 全局→项目分层一致）：overrideDirs 按优先级从低到高排列（后者覆盖前者），目录下
// 与核心 prompt 同名的非空 .md 文件生效；损坏/空文件回退内置并记 warning。
// 返回的 provenance 记录每个核心 prompt 的最终来源与指纹，供落盘 manifest。
func LoadWithOverrides(style string, overrideDirs ...string) (Bundle, []PromptProvenance) {
	bundle := Load(style)
	rawBuiltin := map[string]string{
		"coordinator.md":     mustRead(promptsFS, "prompts/coordinator.md"),
		"architect-short.md": mustRead(promptsFS, "prompts/architect-short.md"),
		"architect-long.md":  mustRead(promptsFS, "prompts/architect-long.md"),
		"planner.md":         mustRead(promptsFS, "prompts/planner.md"),
		"writer.md":          mustRead(promptsFS, "prompts/writer.md"),
		"drafter.md":         mustRead(promptsFS, "prompts/drafter.md"),
		"editor.md":          mustRead(promptsFS, "prompts/editor.md"),
	}
	names := []string{"coordinator.md", "architect-short.md", "architect-long.md", "planner.md", "writer.md", "drafter.md", "editor.md"}

	provenance := make([]PromptProvenance, 0, len(names))
	for _, name := range names {
		source := "builtin"
		raw := rawBuiltin[name]
		for _, dir := range overrideDirs {
			if dir == "" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			if strings.TrimSpace(string(data)) == "" {
				slog.Warn("prompt 覆盖文件为空，回退内置", "module", "assets", "prompt", name, "dir", dir)
				continue
			}
			raw = string(data)
			source = "override:" + dir
		}
		if source != "builtin" {
			if err := bundle.OverridePrompt(name, raw); err != nil {
				slog.Warn("prompt 覆盖失败，回退内置", "module", "assets", "prompt", name, "err", err)
				source = "builtin"
				raw = rawBuiltin[name]
			} else {
				slog.Warn("使用覆盖版 prompt（非出厂内置）", "module", "assets", "prompt", name, "source", source)
			}
		}
		provenance = append(provenance, PromptProvenance{Name: name, Source: source, Fingerprint: promptFingerprint(raw)})
	}
	return bundle, provenance
}

// DefaultPromptOverrideDirs 返回默认覆盖链（优先级从低到高）：
// ~/.novel-studio/prompts → ./.novel-studio/prompts。
func DefaultPromptOverrideDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".novel-studio", "prompts"))
	}
	dirs = append(dirs, filepath.Join(".novel-studio", "prompts"))
	return dirs
}

// WritePromptManifest 把 provenance 落盘到 <outputDir>/meta/prompt_manifest.json，
// 供 session 日志与 diag 精确回答"这次 run 用的是哪版 prompt"。best-effort：失败只记日志。
func WritePromptManifest(outputDir string, provenance []PromptProvenance) {
	if outputDir == "" || len(provenance) == 0 {
		return
	}
	path := filepath.Join(outputDir, "meta", "prompt_manifest.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		slog.Warn("prompt manifest 目录创建失败", "module", "assets", "err", err)
		return
	}
	data, err := json.MarshalIndent(provenance, "", "  ")
	if err != nil {
		slog.Warn("prompt manifest 序列化失败", "module", "assets", "err", err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		slog.Warn("prompt manifest 写入失败", "module", "assets", "err", err)
	}
}

func promptFingerprint(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:12]
}

func loadReferences(style string) tools.References {
	style = strings.TrimSpace(style)
	if style == "" {
		style = "default"
	}
	refs := tools.References{
		ChapterGuide:            mustRead(referencesFS, "references/chapter-guide.md"),
		HookTechniques:          mustRead(referencesFS, "references/hook-techniques.md"),
		QualityChecklist:        mustRead(referencesFS, "references/quality-checklist.md"),
		OutlineTemplate:         mustRead(referencesFS, "references/outline-template.md"),
		CharacterTemplate:       mustRead(referencesFS, "references/character-template.md"),
		ChapterTemplate:         mustRead(referencesFS, "references/chapter-template.md"),
		Consistency:             mustRead(referencesFS, "references/consistency.md"),
		ContentExpansion:        mustRead(referencesFS, "references/content-expansion.md"),
		DialogueWriting:         mustRead(referencesFS, "references/dialogue-writing.md"),
		LongformPlanning:        mustRead(referencesFS, "references/longform-planning.md"),
		Differentiation:         mustRead(referencesFS, "references/differentiation.md"),
		AntiAITone:              mustRead(referencesFS, "references/anti-ai-tone.md"),
		ProductionPlaybook:      mustRead(referencesFS, "references/assistant-production-playbook.md"),
		HumanFeelCraft:          mustRead(referencesFS, "references/human-feel-craft.md"),
		CharacterBuilding:       mustRead(referencesFS, "references/character-building.md"),
		EmotionalNarrativeCraft: mustRead(referencesFS, "references/emotional-narrative-craft.md"),
		FictionParagraphing:     mustRead(referencesFS, "references/fiction-paragraphing.md"),
		WritingTechniquesDigest: mustRead(referencesFS, "references/refer-writing-techniques-digest.md"),
		RAGWritingGuidelines:    mustRead(referencesFS, "references/rag-writing-guidelines.md"),
		WebReferenceGuidelines:  mustRead(referencesFS, "references/web-reference-guidelines.md"),
		LongformAIDetector:      mustRead(referencesFS, "references/longform-ai-detector.md"),
		LiteraryRendering:       mustRead(referencesFS, "references/literary-rendering.md"),
		LiteraryRenderingCards:  mustRead(referencesFS, "references/literary-rendering-cards.json"),
		GenreStyleCraft:         mustRead(referencesFS, "references/genre-style-craft.md"),
		GenreStyleProfiles:      mustRead(referencesFS, "references/genre-style-profiles.json"),
	}
	if style != "" && style != "default" {
		genreDir := "references/genres/" + style + "/"
		if data, err := referencesFS.ReadFile(genreDir + "style-references.md"); err == nil {
			refs.StyleReference = string(data)
		}
		if data, err := referencesFS.ReadFile(genreDir + "arc-templates.md"); err == nil {
			refs.ArcTemplates = string(data)
		}
	}
	return refs
}

func loadPrompts() Prompts {
	return Prompts{
		Coordinator:      WithSimulationGuidance(mustRead(promptsFS, "prompts/coordinator.md"), "coordinator"),
		ArchitectShort:   WithSimulationGuidance(mustRead(promptsFS, "prompts/architect-short.md"), "architect"),
		ArchitectLong:    WithSimulationGuidance(mustRead(promptsFS, "prompts/architect-long.md"), "architect"),
		Brainstorm:       mustRead(promptsFS, "prompts/brainstorm.md"),
		Planner:          WithSimulationGuidance(mustRead(promptsFS, "prompts/planner.md"), "writer"),
		Writer:           WithSimulationGuidance(mustRead(promptsFS, "prompts/writer.md"), "writer"),
		Drafter:          WithSimulationGuidance(mustRead(promptsFS, "prompts/drafter.md"), "writer"),
		Editor:           WithSimulationGuidance(mustRead(promptsFS, "prompts/editor.md"), "editor"),
		SimulationSource: mustRead(promptsFS, "prompts/simulation-source.md"),
		SimulationMerge:  mustRead(promptsFS, "prompts/simulation-merge.md"),
	}
}

// WithSimulationGuidance 给核心 prompt 追加仿写画像指引。导出供 eval 等外部场景做
// variant 覆盖时复用，保证覆盖后的 prompt 与 Load 产出的 baseline 等价（同一包装路径）。
func WithSimulationGuidance(prompt, role string) string {
	return prompt + "\n\n" + strings.ReplaceAll(simulationGuidance, "{{role}}", role)
}

// OverridePrompt 用 raw 覆盖 bundle 中指定 prompt 文件对应的角色提示词，并走与 Load
// 完全相同的 WithSimulationGuidance 包装——eval 做 A/B 时只需调它，不必复制包装逻辑，
// 否则 baseline 带仿写画像后缀、variant 不带，A/B 不等价。file 为 prompt 文件名。
func (b *Bundle) OverridePrompt(file, raw string) error {
	role, ok := promptRole[file]
	if !ok {
		return fmt.Errorf("不支持覆盖的 prompt 文件: %s（仅核心提示词可覆盖）", file)
	}
	wrapped := WithSimulationGuidance(raw, role)
	switch file {
	case "coordinator.md":
		b.Prompts.Coordinator = wrapped
	case "architect-short.md":
		b.Prompts.ArchitectShort = wrapped
	case "architect-long.md":
		b.Prompts.ArchitectLong = wrapped
	case "writer.md":
		b.Prompts.Writer = wrapped
	case "drafter.md":
		b.Prompts.Drafter = wrapped
	case "planner.md":
		b.Prompts.Planner = wrapped
	case "editor.md":
		b.Prompts.Editor = wrapped
	}
	return nil
}

// promptRole 把核心 prompt 文件名映射到 simulation guidance 的角色占位符。
var promptRole = map[string]string{
	"coordinator.md":     "coordinator",
	"architect-short.md": "architect",
	"architect-long.md":  "architect",
	"planner.md":         "writer",
	"writer.md":          "writer",
	"drafter.md":         "writer",
	"editor.md":          "editor",
}

const simulationGuidance = `## 仿写画像

当 novel_context 返回 simulation_profile 时，必须把它视为当前作品的仿写方向约束。{{role}} 应读取其中的 style、lexicon、plot_design、hook_design、pacing_density、reader_engagement 和 role_guidance。

使用原则：借鉴结构、节奏、钩子、信息释放和吸引读者的手法；不要复制原文句子、人物、地名、专有设定或固定桥段。若 simulation_profile 与用户显式要求冲突，优先服从用户要求。`

func loadStyles() map[string]string {
	styles := make(map[string]string)
	entries, err := stylesFS.ReadDir("styles")
	if err != nil {
		return styles
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		data, err := stylesFS.ReadFile("styles/" + e.Name())
		if err != nil {
			continue
		}
		styles[name] = string(data)
	}
	return styles
}

func mustRead(fs embed.FS, path string) string {
	data, err := fs.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("embed read %s: %v", path, err))
	}
	return string(data)
}
