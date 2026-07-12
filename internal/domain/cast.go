package domain

import "strings"

// CastEntry 是配角名册中一条配角记录。
//
// 与 Character（characters.json，Architect 维护的核心档案）解耦：
//   - CastEntry 由 commit_chapter 工具自动累加，记录"出现过的有名字的次要角色"
//   - Character 由 Architect 显式设计，记录主角和关键配角的人格弧线/特质/tier
//
// 同名时以 Character 为准（核心角色不进 cast_ledger），避免重复。
type CastEntry struct {
	Name string `json:"name"`
	// Aliases 当前没有写入通道；预留给将来的"用户 steer 合并别名"工具
	// （如把'李掌柜'与'老李'声明为同一人）。MergeAppearances 已支持别名查找。
	Aliases          []string `json:"aliases,omitempty"`
	BriefRole        string   `json:"brief_role,omitempty"` // 一句话定位（首次出场由 Writer 填，可后续补全；不被覆盖）
	FirstSeenChapter int      `json:"first_seen_chapter"`
	LastSeenChapter  int      `json:"last_seen_chapter"`
	// AppearanceCount 派生自 len(AppearanceChapters)，merge 时保持同步。
	// 保留显式字段方便 UI/JSON 直接读，无需每次重算。
	AppearanceCount    int   `json:"appearance_count"`
	AppearanceChapters []int `json:"appearance_chapters"`
	// Promoted 标记此条目已升格到 characters.json。RecentActive 会跳过这些条目，
	// 避免与核心档案重复召回。当前升格通道未实现，字段为预留 hook。
	Promoted bool `json:"promoted,omitempty"`
}

// CastIntro 是 Writer 在 commit_chapter 时对新出场角色的简介声明。
// 仅在该名字首次出现或 ledger 中 BriefRole 仍为空时才被采用。
type CastIntro struct {
	Name      string `json:"name"`
	BriefRole string `json:"brief_role"`
}

// IsCrowdRoleLabel reports whether a display label describes an interchangeable
// crowd function instead of one durable person. Those labels belong in the
// crowd simulation, not cast_ledger: promoting "烧烤摊主" into a permanent actor
// makes later chapters invent a dossier and a decision record for a role that
// never had an individual identity.
func IsCrowdRoleLabel(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, exact := range []string{
		"摊主", "老板", "老板娘", "店员", "收银员", "服务员", "司机", "工人", "工作人员",
		"顾客", "客人", "游客", "路人", "围观者", "申请者", "商户", "住户", "居民",
		"男人", "女人", "老人", "孩子", "年轻人", "小伙子", "姑娘", "大姐", "大叔",
	} {
		if name == exact {
			return true
		}
	}
	if strings.Contains(name, "的") && hasCrowdRoleSuffix(name) {
		return true
	}
	for _, marker := range []string{
		"冷饮摊", "烧烤摊", "粉摊", "水果摊", "小货摊", "夜市摊", "申请加入", "围观",
		"路过", "取餐", "送货", "排队", "带孩子", "穿工服", "年轻", "年长",
	} {
		if strings.Contains(name, marker) && hasCrowdRoleSuffix(name) {
			return true
		}
	}
	return false
}

func hasCrowdRoleSuffix(name string) bool {
	for _, suffix := range []string{
		"摊主", "老板", "老板娘", "店员", "收银员", "服务员", "司机", "工人", "工作人员",
		"顾客", "客人", "游客", "路人", "申请者", "商户", "住户", "居民", "男人", "女人",
		"老人", "孩子", "年轻人", "小伙子", "姑娘", "大姐", "大叔",
	} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
