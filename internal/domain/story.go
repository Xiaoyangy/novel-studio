package domain

import (
	"encoding/json"
	"fmt"
)

// Novel 小说元信息。
type Novel struct {
	Name          string `json:"name"`
	TotalChapters int    `json:"total_chapters"`
}

// OutlineEntry 大纲条目，对应一章。
type OutlineEntry struct {
	Chapter   int      `json:"chapter"`
	Title     string   `json:"title"`
	CoreEvent string   `json:"core_event"`
	Hook      string   `json:"hook"`
	Scenes    []string `json:"scenes"`
}

// Character 角色档案。
type Character struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"` // 别名/称号/绰号（如"废物少年"、"炎哥"）
	Role        string   `json:"role"`
	Description string   `json:"description"`
	Arc         string   `json:"arc"`
	Traits      []string `json:"traits"`
	Tier        string   `json:"tier,omitempty"` // core / important / secondary / decorative（默认 important）

	// Psych 定量心理画像（大五/依恋/价值观/偏差/能力/DNA 等），Architect 落笔的作者源。
	// 可选：缺失时所有消费方跳过。
	Psych *CharacterPsychProfile `json:"psych,omitempty"`
}

// VolumeOutline 卷级大纲（长篇分层模式）。
type VolumeOutline struct {
	Index int          `json:"index"`
	Title string       `json:"title"`
	Theme string       `json:"theme"` // 本卷核心冲突/主题
	Arcs  []ArcOutline `json:"arcs"`
}

// IsExpanded 判断卷是否已展开（有弧级结构）。
func (v *VolumeOutline) IsExpanded() bool { return len(v.Arcs) > 0 }

// StoryCompass 终局方向指南针，替代固定的骨架卷列表。
// Architect 在每次卷边界时可更新，允许故事方向随创作演化。
type StoryCompass struct {
	EndingDirection string   `json:"ending_direction"`          // 终局方向（主题性描述）
	OpenThreads     []string `json:"open_threads,omitempty"`    // 活跃长线（需收束才能结局）
	EstimatedScale  string   `json:"estimated_scale,omitempty"` // 模糊规模（如"预计 4-6 卷"）
	LastUpdated     int      `json:"last_updated,omitempty"`    // 更新时的已完成章节数
}

// UnmarshalJSON 容忍 LLM 把 open_threads 当字符串而不是字符串数组返回。
// 现象：import 反推 foundation 时偶发 "cannot unmarshal string into []string"。
// 这里手动读取字段，再把 string / []any / []string 三种形态都归一为 []string。
func (c *StoryCompass) UnmarshalJSON(data []byte) error {
	var raw struct {
		EndingDirection string `json:"ending_direction"`
		OpenThreadsRaw  any    `json:"open_threads"`
		EstimatedScale  string `json:"estimated_scale"`
		LastUpdated     int    `json:"last_updated"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("compass unmarshal: %w", err)
	}
	c.EndingDirection = raw.EndingDirection
	c.EstimatedScale = raw.EstimatedScale
	c.LastUpdated = raw.LastUpdated
	switch v := raw.OpenThreadsRaw.(type) {
	case nil:
		c.OpenThreads = nil
	case string:
		if v != "" {
			c.OpenThreads = []string{v}
		}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		c.OpenThreads = out
	case []string:
		c.OpenThreads = v
	}
	return nil
}

// ArcOutline 弧级大纲。
type ArcOutline struct {
	Index             int            `json:"index"` // 卷内弧序号
	Title             string         `json:"title"`
	Goal              string         `json:"goal"`                         // 弧目标（起承转合）
	EstimatedChapters int            `json:"estimated_chapters,omitempty"` // 骨架弧的预估章数（展开后清零）
	Chapters          []OutlineEntry `json:"chapters"`
}

// IsExpanded 判断弧是否已展开（有详细章节）。
func (a *ArcOutline) IsExpanded() bool { return len(a.Chapters) > 0 }

// TotalChapters 计算分层大纲的当前规划总章数。
// 已展开弧按真实章节数计，骨架弧按 EstimatedChapters 计。
// Progress.TotalChapters 用它判断长篇上下文策略；真正可写章节仍来自 FlattenOutline。
func TotalChapters(volumes []VolumeOutline) int {
	n := 0
	for _, v := range volumes {
		for _, a := range v.Arcs {
			if a.IsExpanded() {
				n += len(a.Chapters)
			} else {
				n += a.EstimatedChapters
			}
		}
	}
	return n
}

// FlattenOutline 将分层大纲展开为扁平章节列表，保持全局章节号连续。
func FlattenOutline(volumes []VolumeOutline) []OutlineEntry {
	var result []OutlineEntry
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for _, e := range a.Chapters {
				e.Chapter = ch
				result = append(result, e)
				ch++
			}
		}
	}
	return result
}

// WorldRule 世界观规则条目。
type WorldRule struct {
	Category string `json:"category"` // magic / technology / geography / society / other
	Rule     string `json:"rule"`     // 规则描述
	Boundary string `json:"boundary"` // 不可违反的边界

	// Visibility 规则可见性：formal（显规则：制度/法律）/ informal（潜规则：礼俗/默契）/
	// secret（隐秘规则：Writer 可知、正文不得明写）。缺省视为 formal，老数据零迁移。
	Visibility string `json:"visibility,omitempty"`
	// Source 规则出处（朝廷 / 江湖 / 家族 / 门派），潜规则叙事时的归属线索。
	Source string `json:"source,omitempty"`
}

// WorldRuleVisibility 归一化可见性：空值归 formal。
func WorldRuleVisibility(r WorldRule) string {
	switch r.Visibility {
	case "informal", "secret":
		return r.Visibility
	default:
		return "formal"
	}
}

// FilterWorldRules 按可见性过滤规则（visibility 传 formal/informal/secret）。
func FilterWorldRules(rules []WorldRule, visibility string) []WorldRule {
	var out []WorldRule
	for _, r := range rules {
		if WorldRuleVisibility(r) == visibility {
			out = append(out, r)
		}
	}
	return out
}
