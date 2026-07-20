package domain

// 世界法典（world_codex）：初始化世界时一次性敲定的全局硬设定。
// 目标是让世界像真实世界一样自洽——能力有分级、结构有层级、机制可运转、
// 资源有稀缺、历史有来处；不是空中楼阁，也不随写作漂移。
//
// 两级结构：
//   - 全局法典（WorldCodex）：零章初始化时敲定，覆盖清单强制齐全；
//     修改必须带 change_reason + evidence 走版本升级，禁止随意更改。
//   - 卷级上限（VolumeCodex）：分卷初始化（卷详细大纲落地）时生成，
//     声明该卷会触碰的能力/武器/装备/技能/种族上限，写作不得越级。

// CodexAbilityTier 能力分级的一级：从量级、晋升、边界、代价四面锁死。
type CodexAbilityTier struct {
	Order     int      `json:"order"`              // 层级序号，1 起
	Name      string   `json:"name"`               // 分级名：如 入门/一阶/筑基/微弱神力
	Aliases   []string `json:"aliases,omitempty"`  // 民间叫法/别称
	Magnitude string   `json:"magnitude"`          // 量级：这一级能做到什么（可对比参照物）
	Limits    string   `json:"limits"`             // 边界：这一级做不到什么、被什么克制
	Promotion string   `json:"promotion"`          // 晋升条件：可见证据/代价/仪式/审核
	Cost      string   `json:"cost,omitempty"`     // 维持或使用代价
	Rarity    string   `json:"rarity,omitempty"`   // 稀有度/人口占比
	Samples   []string `json:"samples,omitempty"`  // 代表人物/势力样本
	Evidence  string   `json:"evidence,omitempty"` // 设定依据（premise/outline/craft 素材）
}

// CodexDomainEntry 技能范畴的一个门类（功法/法术/手艺/科技树分支……）。
type CodexDomainEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TierBinding string   `json:"tier_binding,omitempty"` // 与能力分级的对应关系
	Constraints []string `json:"constraints,omitempty"`  // 习得条件/使用禁忌/失败模式
}

// CodexRace 种族/族群设定。
type CodexRace struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Traits      []string `json:"traits,omitempty"`      // 天赋/体质/寿命
	Habitat     string   `json:"habitat,omitempty"`     // 栖息地/分布
	Relations   string   `json:"relations,omitempty"`   // 与其他种族/人类的关系
	Constraints []string `json:"constraints,omitempty"` // 弱点/禁忌/规则约束
}

// CodexGradedCategory 武器/装备范畴：门类 + 分级阶梯。
type CodexGradedCategory struct {
	Name        string   `json:"name"` // 门类：冷兵器/符器/契约资产/义体……
	Description string   `json:"description"`
	Grades      []string `json:"grades,omitempty"`       // 低到高的品级阶梯
	TierBinding string   `json:"tier_binding,omitempty"` // 使用者能力分级要求
	Constraints []string `json:"constraints,omitempty"`
}

// CodexSection 覆盖清单驱动的世界维度。一个真实世界必须回答这些维度；
// 题材确实不涉及时显式声明 not_applicable + 理由，不允许留空糊弄。
type CodexSection struct {
	Key           string   `json:"key"`
	Title         string   `json:"title,omitempty"`
	Content       string   `json:"content,omitempty"`        // 设定正文
	Rules         []string `json:"rules,omitempty"`          // 可执行约束（writer/editor 检查用）
	NotApplicable bool     `json:"not_applicable,omitempty"` // 本题材不适用
	Reason        string   `json:"reason,omitempty"`         // not_applicable 的理由
}

// CodexChange 法典修订记录：没有证据不准改。
type CodexChange struct {
	At       string   `json:"at"`
	Version  int      `json:"version"`
	Reason   string   `json:"reason"`   // 为什么必须改（正文矛盾/新卷需要/用户指令）
	Evidence string   `json:"evidence"` // 依据：章节事实/审阅结论/用户原话
	Fields   []string `json:"fields"`   // 改了哪些部分
}

// WorldCodex 全局世界法典。
type WorldCodex struct {
	Version     int    `json:"version"`
	NovelName   string `json:"novel_name,omitempty"`
	GeneratedAt string `json:"generated_at,omitempty"`

	// 用户九类硬设定（强类型）
	AbilityTiers        []CodexAbilityTier    `json:"ability_tiers"`        // 能力分级
	SkillDomains        []CodexDomainEntry    `json:"skill_domains"`        // 技能范畴
	Races               []CodexRace           `json:"races"`                // 种族设定
	WeaponCategories    []CodexGradedCategory `json:"weapon_categories"`    // 武器范畴
	EquipmentCategories []CodexGradedCategory `json:"equipment_categories"` // 装备范畴

	// 结构与现实性维度（覆盖清单，见 RequiredCodexSections）
	Sections []CodexSection `json:"sections"`

	// 修订治理
	ImmutabilityPolicy string        `json:"immutability_policy,omitempty"` // 修改条件声明
	ChangeLog          []CodexChange `json:"change_log,omitempty"`
}

// RequiredCodexSections 全局法典必须覆盖的世界维度清单。
// 参考 worldbuilding bible 通用框架 + 网文世界观工程实践整理；
// 每项要么给出设定与规则，要么显式 not_applicable + 理由。
var RequiredCodexSections = []struct {
	Key   string
	Title string
}{
	{"world_morphology", "世界形态结构：位面/大陆/区域层级、地理与空间边界"},
	{"power_structure", "权力结构：政权、势力、组织的层级与制衡"},
	{"mechanism_structure", "机制结构：世界运转的核心机制（契约/审计/税收/轮回/科举……）"},
	{"ability_scope", "能力范围：跨分级的总体边界——能力干预不了什么、铁律优先级"},
	{"geography_ecology", "地理与生态：气候、物产、动植物、环境危险"},
	{"history_timeline", "历史纪元：大事年表、文明兴衰、当前时代的来处"},
	{"economy_currency", "经济与货币：货币体系、物价基准、贸易与稀缺资源"},
	{"society_culture", "社会与文化：阶层结构、习俗、伦理、教育、家庭"},
	{"religion_mythology", "宗教与神话：信仰体系、仪式、传说及其真实性边界"},
	{"language_symbols", "语言与符号：语言文字、称谓体系、标记/纹章"},
	{"law_order", "法律与秩序：律法、执法力量、刑罚、灰色地带"},
	{"technology_level", "科技/工艺水平：生产力、交通、通信、医疗"},
	{"daily_life", "日常生活：衣食住行、娱乐、普通人的一天"},
	{"calendar_time", "历法与时间：纪年、节庆、昼夜与季节规则"},
	{"life_death_soul", "生死与灵魂：死亡观、身后事、灵魂/轮回机制"},
	{"taboos_constraints", "禁忌与铁律：绝不可违背的世界底线及违背后果"},
}

// VolumeCodex 卷级上限：分卷初始化时生成，锁定该卷的力量天花板。
type VolumeCodex struct {
	Volume              int      `json:"volume"`
	VolumeTitle         string   `json:"volume_title,omitempty"`
	TierCeiling         string   `json:"tier_ceiling"`                    // 本卷世界侧能力上限（ability_tiers 的 Name）
	ProtagonistCeiling  string   `json:"protagonist_ceiling"`             // 主角本卷可达上限
	AllowedSkillDomains []string `json:"allowed_skill_domains,omitempty"` // 本卷可出现的技能门类
	WeaponGradeCeiling  string   `json:"weapon_grade_ceiling,omitempty"`  // 武器品级上限
	EquipGradeCeiling   string   `json:"equipment_grade_ceiling,omitempty"`
	NewRaces            []string `json:"new_races,omitempty"`           // 本卷新登场种族
	NewMechanisms       []string `json:"new_mechanisms,omitempty"`      // 本卷解锁的机制
	ForbiddenInVolume   []string `json:"forbidden_in_volume,omitempty"` // 本卷明确不得出现的力量/道具/信息
	Evidence            string   `json:"evidence,omitempty"`            // 与卷大纲的对应依据
	GeneratedAt         string   `json:"generated_at,omitempty"`
}
