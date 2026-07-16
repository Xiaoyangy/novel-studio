package rag

import (
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	SummaryOriginRawPrefix             = "raw_prefix"
	SummaryOriginCuratedMethod         = "curated_method"
	SummaryOriginDerivedMethodMetadata = "derived_method_metadata"
)

// controlledMethodCard is deliberately closed-vocabulary. Raw design text may
// select a card, but none of its free-form spans are copied into the summary
// delivered to an automatic rewrite.
type controlledMethodCard struct {
	label      string
	needles    []string
	mechanism  string
	applicable string
	action     string
	avoid      string
	acceptance string
}

// Keep specific techniques before broad ones. Equal-scoring matches retain
// this order, so a concrete dialogue/scene operation wins over generic advice.
var controlledMethodCards = []controlledMethodCard{
	{
		label: "漏答", needles: []string{"漏答", "答非所问", "避而不答", "不正面回答"},
		mechanism: "用回答缺口显出目标冲突", applicable: "对话过直或信息一次说尽",
		action: "让回答只处理次要部分并留下可追问缺口", avoid: "每轮都完整解释背景",
		acceptance: "删去说明后仍能看出双方各自所求",
	},
	{
		label: "打断", needles: []string{"打断", "插话", "抢话", "截断"},
		mechanism: "用话轮中断改变主动权", applicable: "冲突对白缺少即时压力",
		action: "在关键信息前截断并让下一动作承接压力", avoid: "无目的地连续抢话",
		acceptance: "中断前后权力或信息归属发生变化",
	},
	{
		label: "潜台词", needles: []string{"潜台词", "话外音", "言外之意", "弦外之音"},
		mechanism: "让字面回应与真实诉求错位", applicable: "人物不便直说或关系有顾忌",
		action: "用回避对象和伴随动作暴露真实意图", avoid: "随后补一段心理翻译",
		acceptance: "读者能从错位处推断未说出口的诉求",
	},
	{
		label: "声口差异", needles: []string{"声口", "口吻", "语气", "口头禅", "说话方式", "措辞习惯"},
		mechanism: "把身份和处境压进选词节奏", applicable: "人物台词可互换或辨识度低",
		action: "为人物限定句长称谓和回避方式", avoid: "只靠口头禅制造差异",
		acceptance: "遮住说话人后仍可凭措辞辨认人物",
	},
	{
		label: "权力位移", needles: []string{"权力", "地位", "主动权", "压制", "控制权", "支配"},
		mechanism: "用资源和回应权变化推动关系", applicable: "场面有冲突但局势静止",
		action: "让一方失去资源回应权或退路", avoid: "只用强硬语气宣告胜负",
		acceptance: "场末能指出谁获得了哪项实际控制",
	},
	{
		label: "信息延迟", needles: []string{"信息延迟", "延迟", "隐瞒", "暂不揭示", "延后", "秘而不宣"},
		mechanism: "让关键信息晚于需求出现", applicable: "解释前置或悬念过早结清",
		action: "先给可验证征兆再推迟完整答案", avoid: "无因扣住人物理应说出的事实",
		acceptance: "延迟期间仍有新证据和选择推进",
	},
	{
		label: "信息释放", needles: []string{"信息释放", "信息量", "揭示", "透露", "真相", "知情差", "信息差"},
		mechanism: "按选择成本分批交付信息", applicable: "设定倾倒或信息密度失衡",
		action: "每次只给足以改变当前选择的一层信息", avoid: "把背景资料集中讲完",
		acceptance: "每层信息都触发新的判断或行动",
	},
	{
		label: "限知视角", needles: []string{"限知", "视角", "所见", "所知", "认知范围", "第一人称", "第三人称"},
		mechanism: "把叙述限制在人物当下可感可判", applicable: "叙述越过人物认知或全知解释过多",
		action: "把结论改成观察误判和待验证动作", avoid: "替人物宣布未知真相",
		acceptance: "每个判断都能追溯到当场证据",
	},
	{
		label: "主观因果", needles: []string{"主观", "内心", "心理", "动机", "念头", "因为", "所以"},
		mechanism: "让认知偏差驱动选择和后果", applicable: "事件相连但人物意志缺席",
		action: "补足观察判断选择结果四步链", avoid: "用旁白替人物总结原因",
		acceptance: "删去因果连接词仍看得出为何行动",
	},
	{
		label: "场景目标", needles: []string{"场景目标", "目标", "诉求", "欲望", "意图", "任务", "想要"},
		mechanism: "用可成败目标组织场景动作", applicable: "场景有活动却没有方向",
		action: "给人物一个当场可验证的取得或阻止目标", avoid: "用抽象愿望代替眼前任务",
		acceptance: "场末能判断目标成败及其代价",
	},
	{
		label: "阻力对抗", needles: []string{"阻力", "障碍", "阻碍", "对抗", "难题", "困境", "冲突"},
		mechanism: "让阻力直接卡住当前手段", applicable: "目标明确但推进过于顺滑",
		action: "设置会迫使人物换手段的具体阻碍", avoid: "只增加无关麻烦拖延",
		acceptance: "人物因阻力付出成本或改变方案",
	},
	{
		label: "选择取舍", needles: []string{"选择", "决定", "决策", "取舍", "抉择", "犹豫"},
		mechanism: "用不可兼得暴露人物价值排序", applicable: "人物被情节推着走",
		action: "让两个可理解目标在当场发生排他冲突", avoid: "提供没有损失的伪选择",
		acceptance: "选择同时留下收益和明确损失",
	},
	{
		label: "行动反应", needles: []string{"行动", "动作", "反应", "应对", "举动", "身体反应"},
		mechanism: "用可见动作代替抽象态度", applicable: "情绪和立场主要靠说明",
		action: "把态度落成对物件空间或他人的操作", avoid: "堆叠无后果的小动作",
		acceptance: "动作改变了信息关系或下一步",
	},
	{
		label: "场景后果", needles: []string{"场景后果", "后果", "代价", "结果", "导致", "连锁"},
		mechanism: "让场景结果约束下一场选择", applicable: "章节像互不相干的片段",
		action: "把本场损失证据或承诺带入后续任务", avoid: "场景结束即恢复原状",
		acceptance: "下一场至少一项条件由本场改变",
	},
	{
		label: "证据物件", needles: []string{"证据", "物件", "道具", "痕迹", "凭证", "票据", "线索"},
		mechanism: "让物件同时承载信息和行动限制", applicable: "线索只被口头说明或道具无功能",
		action: "安排人物获取核验转移或失去物件", avoid: "物件出现后不再影响选择",
		acceptance: "物件状态变化对应一项判断变化",
	},
	{
		label: "转折改道", needles: []string{"转折", "反转", "突变", "意外", "改道", "翻转"},
		mechanism: "用新事实改写原有行动方案", applicable: "情节按预期直线推进",
		action: "投放可回溯证据迫使人物调整目标或手段", avoid: "靠无铺垫巧合制造惊讶",
		acceptance: "转折前有迹可循且转折后行动改变",
	},
	{
		label: "冲突升级", needles: []string{"升级", "加剧", "递增", "加码", "恶化", "层层", "递进"},
		mechanism: "逐轮提高失败成本和不可逆性", applicable: "冲突重复但强度不变",
		action: "每轮新增资源损失关系暴露或时间压力", avoid: "仅提高音量和情绪词",
		acceptance: "后一轮比前一轮多一项真实风险",
	},
	{
		label: "伏笔回收", needles: []string{"回收", "呼应", "伏笔", "铺垫", "照应", "兑现"},
		mechanism: "让早先细节在新语境中产生作用", applicable: "细节散落或承诺长期悬空",
		action: "用旧细节解决问题同时暴露新代价", avoid: "用旁白提醒读者曾经出现",
		acceptance: "回收既可识别又改变当前局势",
	},
	{
		label: "章末钩子", needles: []string{"钩子", "结尾", "章末", "收尾", "悬念", "尾声"},
		mechanism: "以未完成行动牵引下一章", applicable: "章末停在总结或情绪金句",
		action: "在新证据新威胁或新选择出现时截断", avoid: "用空泛问句假装悬念",
		acceptance: "下一章开场可直接执行未完成事项",
	},
	{
		label: "节奏张弛", needles: []string{"节奏", "快慢", "张弛", "停顿", "加速", "放慢", "节拍"},
		mechanism: "用信息与动作密度控制阅读速度", applicable: "全章速度单一或重点失焦",
		action: "关键选择前减速取证结果后压缩过场", avoid: "用标点和短句机械提速",
		acceptance: "速度变化对应压力或信息变化",
	},
	{
		label: "句段职责", needles: []string{"句段", "句子", "句式", "段落", "长句", "短句", "篇幅"},
		mechanism: "让相邻句段承担不同叙事任务", applicable: "段落同长同构或连续解释",
		action: "按观察动作判断后果轮换句段职责", avoid: "连续使用相同起手和收束",
		acceptance: "抽查相邻三段能标出不同功能",
	},
	{
		label: "空间调度", needles: []string{"调度", "走位", "空间", "位置", "距离", "出入口", "方位"},
		mechanism: "用距离遮挡和出入口制造机会限制", applicable: "人物像悬空对话或场面位置混乱",
		action: "让移动改变可见范围接触对象或退路", avoid: "只罗列环境陈设",
		acceptance: "关键动作依赖已交代的空间条件",
	},
	{
		label: "感官锚点", needles: []string{"环境", "天气", "声音", "光线", "气味", "触感", "温度", "感官"},
		mechanism: "让可感变化承担压力提示", applicable: "环境描写可从任意场景移走",
		action: "选择一项会影响判断或动作的感官信号", avoid: "并列堆叠景物形容词",
		acceptance: "环境细节参与一次判断或行动",
	},
	{
		label: "关系位移", needles: []string{"关系", "亲疏", "信任", "敌意", "盟友", "背叛", "合作"},
		mechanism: "用具体交换改变人物关系状态", applicable: "互动热闹但关系原地不动",
		action: "安排一次求助拒绝让渡或隐瞒并留下后果", avoid: "直接宣布关系变好或变坏",
		acceptance: "场末双方可做与不可做之事发生变化",
	},
	{
		label: "情绪转向", needles: []string{"情绪", "情感", "愤怒", "恐惧", "紧张", "悲伤", "焦虑", "心情"},
		mechanism: "让情绪随证据判断和动作转向", applicable: "情绪词密集却没有变化依据",
		action: "用触发物和身体选择呈现转向节点", avoid: "同义情绪词反复加码",
		acceptance: "能指出情绪改变前后的触发证据",
	},
	{
		label: "过场压缩", needles: []string{"过场", "转场", "省略", "压缩", "跳过", "时间流逝"},
		mechanism: "跳过不改变状态的过程", applicable: "通勤等待和重复操作拖慢章节",
		action: "保留起点结果及一项途中代价", avoid: "逐步记录无选择的流程",
		acceptance: "压缩后因果不断且重点更集中",
	},
	{
		label: "初登场锚点", needles: []string{"外貌", "肖像", "长相", "容貌", "登场", "亮相", "衣着", "神态"},
		mechanism: "用可辨特征绑定人物当下处境", applicable: "人物首次出现但缺少视觉辨识",
		action: "选择一项外形一项状态细节并让其参与动作", avoid: "暂停情节罗列五官服饰",
		acceptance: "人物离场后仍能记住一个有功能的特征",
	},
	{
		label: "具体化", needles: []string{"具体", "抽象", "说明", "展示", "概括", "空泛", "细节"},
		mechanism: "把抽象判断换成可验证事实", applicable: "评价结论多于现场证据",
		action: "将结论改写为一次动作物件或回应变化", avoid: "用更多形容词包装结论",
		acceptance: "读者可凭事实自行得到原判断",
	},
	{
		label: "机械同构", needles: []string{"ai味", "ai 味", "aigc", "ai检测", "ai 检测", "机器味", "模板化", "同构", "套路化", "重复"},
		mechanism: "用因果和句段职责变化打破模板重复", applicable: "AI检测风险或段落节拍高度同构",
		action: "删除总结句并改换相邻段落的观察动作后果顺序", avoid: "机械替换同义词或随机打碎句子",
		acceptance: "连续三段的起手功能和收束方式不重复",
	},
}

// DerivedSafeRewriteMethodSummary produces an actionable, controlled-vocabulary
// method card. It may inspect raw text to select labels, but never copies a
// sentence, title, name, path, or free-form span into the returned value.
func DerivedSafeRewriteMethodSummary(chunk domain.RAGChunk) string {
	facet := controlledMethodFacet(chunkCraftFacet(chunk))
	tags := controlledMethodTags(facet, strings.ToLower(chunk.Text))
	primary := controlledMethodCardByLabel(tags[0])
	return strings.Join([]string{
		"安全方法卡",
		"内容面=" + string(facet),
		"技法标签=" + strings.Join(tags, "、"),
		"机制=" + primary.mechanism,
		"适用=" + primary.applicable,
		"动作=" + primary.action,
		"避免=" + primary.avoid,
		"验收=" + primary.acceptance,
	}, "；")
}

func controlledMethodTags(facet CraftFacet, text string) []string {
	type scoredTag struct {
		index int
		score int
	}
	matched := make([]scoredTag, 0, 4)
	for index, card := range controlledMethodCards {
		score := 0
		for _, needle := range card.needles {
			if strings.Contains(text, needle) {
				score++
			}
		}
		if score > 0 {
			matched = append(matched, scoredTag{index: index, score: score})
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].score > matched[j].score
	})
	if len(matched) == 0 {
		return []string{fallbackMethodTag(facet)}
	}
	if len(matched) > 3 {
		matched = matched[:3]
	}
	tags := make([]string, 0, len(matched))
	for _, item := range matched {
		tags = append(tags, controlledMethodCards[item.index].label)
	}
	return tags
}

func fallbackMethodTag(facet CraftFacet) string {
	switch facet {
	case FacetDialogue:
		return "漏答"
	case FacetScene:
		return "空间调度"
	case FacetAppearance:
		return "初登场锚点"
	case FacetEmotion:
		return "情绪转向"
	case FacetPlot, FacetOutline, FacetTrope:
		return "场景后果"
	case FacetPersona:
		return "选择取舍"
	default:
		return "主观因果"
	}
}

func controlledMethodCardByLabel(label string) controlledMethodCard {
	for _, card := range controlledMethodCards {
		if card.label == label {
			return card
		}
	}
	// fallbackMethodTag and the controlled rule table are a closed set. Keep a
	// deterministic fallback here so malformed persisted metadata still cannot
	// introduce free-form text into a generated summary.
	for _, card := range controlledMethodCards {
		if card.label == "主观因果" {
			return card
		}
	}
	return controlledMethodCard{}
}

func controlledMethodFacet(facet CraftFacet) CraftFacet {
	switch facet {
	case FacetAppearance, FacetDialogue, FacetScene, FacetEmotion, FacetLexicon,
		FacetWeapon, FacetSkillAbility, FacetInstitution, FacetOutline, FacetTrope,
		FacetPersona, FacetPlot, FacetBenchmark, FacetWorldview, FacetMethodology,
		FacetMarket, FacetCalibration, FacetUncategorized:
		return facet
	default:
		return FacetUncategorized
	}
}

func safeRewriteSummaryProvenance(chunk domain.RAGChunk) bool {
	if chunk.Metadata == nil {
		return false
	}
	origin, _ := chunk.Metadata["summary_origin"].(string)
	switch strings.ToLower(strings.TrimSpace(origin)) {
	case SummaryOriginCuratedMethod, SummaryOriginDerivedMethodMetadata:
		return true
	default:
		return false
	}
}
