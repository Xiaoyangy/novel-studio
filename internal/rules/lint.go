package rules

import (
	"regexp"
	"strings"
)

// Lint 内置产品底线检查：扫描正文中的机制残留，与用户规则无关，commit 时始终执行。
// 与 Check 同契约——仅返事实（铁律一），不阻断流程，由评审/用户裁定。
//
// 当前三类（全部来自真实长跑产物的实证缺陷）：
//   - markdown_residue：正文残留 ** 加粗、首行之外的 # 标题行（导出 txt 会裸露符号）
//   - non_cjk_fragments：连续拉丁字母片段（模型语言混杂，如中文正文裸混 "pattern"）
//   - content_count_mismatch：精确数词和后文事实不一致，如“四个字：抵押物待核”
//   - awkward_simile：库存或硬贴物件明喻，如“像一根没拔干净的刺”
//   - dangling_order_word：顺序词悬空，如“挂钟先停了”但没有后续参照
//   - abrupt_strong_event：无铺垫突发强事件，如“隔壁1703忽然砸门”
//   - unsupported_speech_claim：人物声称“我听见你说话了”但需要上文证据
//   - opaque_memo_shorthand：备忘录缩写省掉关键对象或压缩成提纲口吻，如“别回号”“零钱暂不碰”
//   - unit_name_apposition / clipped_habit_sentence：提纲式省略，如“1703蒋牧”“搬来两个月，电梯里……”
//   - clipped_summary_phrase：摘要腔判断句，如“这通话只给了两个确认”“最便宜的坑”
//   - state_clause_pile：同一句状态说明堆叠，如“还亮着，停在，还在”
//   - explanatory_tone / template_emotion / vague_expression：解释腔、模板情绪和空泛表达
//   - semantic_perplexity_low：连续抽象判断句缺少动作、物件、感官或对话分支
//   - ending_aphorism_question：章末用抽象金句反问假装钩子
//   - micro_action_overuse / dramatic_negation_overuse / paragraph_start_repetition /
//     not_but_overuse / precise_measure_overuse：制度戏、群像戏中常见的齐整节奏信号
//   - patch_phrase_overuse / minor_mistake_overuse / isolated_sentence_overuse /
//     supporting_quip_overuse / vague_quantifier_overuse：修补痕迹和孤句/配角/虚量词限流
//   - object_response_overuse / object_response_rhythm_flat：物件回应过密或节奏等距
//   - dialogue_aphorism_overuse / templated_dialogue_chain：主配角连续警句式应答、
//     点名-停顿-补口径-追问式对白链复用
//   - serial_device_repetition：开头/结尾连载装置连续复用超过两章
//   - semicolon_overuse / form_notice_semicolon_chain / dialogue_semicolon_formality：
//     分号滥用、纸面条款一行分号链、对白书面腔
//   - stiff_trade_dialogue：讲价/互怼对白被写成口号或条款
//   - system_message_overpacked：系统单条消息同时承担过多规则、安慰和任务功能
//   - system_message_inline：系统消息与人物台词/旁白或另一条系统消息挤在同一段
//   - abstract_system_reassurance：系统用无对象、无后果的客服式空话假装陪伴
//   - ui_trial_checklist：把同一规则的点击、失败、改备注、删除等试错逐项渲染
//   - opaque_procedure_jargon：对白把流程缩写/验收黑话直接丢给大众读者
//   - designed_role_quip：普通人物为作者临时说工整角色比喻，声口不像现场口语
//   - procedure_stage_pile：一个场景密集交齐授权、报价、安装、测试、付款和检查
//   - dialogue_action_lead_repetition：连续对白段都以人物动作报幕再开口
//   - trend_language_sound_effect_misuse：把“呱，”吐槽起手式写成叫声/拟声动作
//   - system_procedure_narration：系统用后台核验术语代替可读、有人味的短回应
//   - bureaucratic_register_overuse：制度/纪要/表单词连续驱动场景，人物口语和私人压力不足
//   - structured_note_triplet / card_tos_block / empty_parallel_chant：
//     便签三条、黑卡 ToS 块、空对仗童谣等结构感过强的 AI 签名
//   - de_fa_adjective_repetition / duplicate_dialogue_point：
//     "X得发Y" 句式复读、相邻对白同一骂点残留
//   - impossible_body_geometry / impossible_line_of_sight：
//     断影空间逻辑、猫眼视角读背面字等硬伤
//   - causal_evidence_order / identity_effect_delayed / building_floor_mismatch /
//     anomalous_phone_unverified / form_image_mismatch / card_core_rule_overblurred：
//     规则演示、身份验证、空间称呼、载体比喻等因果链硬伤
func Lint(text string) []Violation {
	var vs []Violation
	vs = appendMarkdownResidue(vs, text)
	vs = appendNonCJKFragments(vs, text)
	vs = appendContentCountMismatch(vs, text)
	vs = appendAwkwardSimile(vs, text)
	vs = appendDanglingOrderWord(vs, text)
	vs = appendAbruptStrongEvent(vs, text)
	vs = appendEvidenceAndMemoClarity(vs, text)
	vs = appendStateClausePile(vs, text)
	vs = appendAntiAIPhraseSignals(vs, text)
	vs = appendSemanticPerplexitySignals(vs, text)
	vs = appendEndingAphorismQuestion(vs, text)
	vs = appendPunctuationCadence(vs, text)
	vs = appendSystemMessageSignals(vs, text)
	vs = appendUITrialChecklist(vs, text)
	vs = appendTrendAndSystemVoiceSignals(vs, text)
	vs = appendDialogueAndProcedureNaturalness(vs, text)
	vs = appendHumanFeelStructureSignals(vs, text)
	vs = appendCausalIntegritySignals(vs, text)
	vs = appendCadenceSignals(vs, text)
	return vs
}

func appendMarkdownResidue(vs []Violation, text string) []Violation {
	if n := strings.Count(text, "**"); n > 0 {
		vs = append(vs, Violation{
			Rule:     "markdown_residue",
			Target:   "**",
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	headings := 0
	seenContent := false
	for line := range strings.SplitSeq(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		// 第一个非空行的 # 标题是章文件的合法格式（不按行号写死，容忍前导空行）
		first := !seenContent
		seenContent = true
		if !first && strings.HasPrefix(t, "#") {
			headings++
		}
	}
	if headings > 0 {
		vs = append(vs, Violation{
			Rule:     "markdown_residue",
			Target:   "#",
			Actual:   headings,
			Severity: SeverityWarning,
		})
	}
	return vs
}

var latinFragmentRe = regexp.MustCompile(`[A-Za-z]{2,}`)
var countWordRe = regexp.MustCompile(`([零一二两三四五六七八九十]{1,4})个(?:小|蓝|黑|白|旧)?字[：:]\s*[“"「『【]?([^。！？!?；;\n]+)`)
var twoItemsAsTwoCharsRe = regexp.MustCompile(`([一-龥]{2,8})和([一-龥]{2,8})两个字`)
var forcedSimileRe = regexp.MustCompile(`像一[根把柄枚颗块片团道条张][^。！？!?；;\n]{0,12}(?:刺|刀|针|钉子|石头|冰|火)`)
var ambiguousOrderRe = regexp.MustCompile(`[^。！？!?；;\n]{0,24}先(?:(?:停|亮|黑|灭|响|断|冷|热|暗|红|白)了|闭嘴|住口|没声)[^。！？!?；;\n]*`)
var orderFollowupRe = regexp.MustCompile(`(?:再|又|随后|接着|然后|才|第二|另一|另一个|后面)`)
var abruptStrongEventRe = regexp.MustCompile(`[^。！？!?；;\n]{0,28}(?:忽然|突然|猛地|猛然|一下子)[^。！？!?；;\n]{0,18}(?:砸门|撞门|踹门|扑过来|扑倒|冲进|冲出|爆开|炸开|裂开|塌下|断电|熄灭|尖叫|惨叫|倒下|伸进来|抓住|咬住)[^。！？!?；;\n]*`)
var eventSetupRe = regexp.MustCompile(`(?:传来|听见|听到|先是|紧接着|接着|随后|声音|动静|脚步|闷响|嗒|咚|咣|看见|只见|惊动)`)
var unsupportedSpeechClaimRe = regexp.MustCompile(`[^。！？!?；;\n]{0,18}我(?:听见|听到)你说话了?[^。！？!?；;\n]*`)
var opaqueMemoShorthandRe = regexp.MustCompile(`[^。！？!?；;\n]{0,24}(?:别回号|别回名|回号|回名|暂不碰)[^。！？!?；;\n]{0,24}`)
var unitNameAppositionRe = regexp.MustCompile(`[^。！？!?；;\n]{0,18}(?:多半是|应该是|估计是|像是|就是)(\d{3,4})([一-龥]{2,4})[^。！？!?；;\n]*`)
var clippedHabitRe = regexp.MustCompile(`(?:搬来|住进来)[^。！？!?；;\n]{0,12}，(?:电梯里|楼道里|门口)[^。！？!?；;\n]{0,18}(?:外放|拿错|敲门|骂人)[^。！？!?；;\n]*`)
var habitNaturalizerRe = regexp.MustCompile(`(?:经常|常常|老是|总是|总在|常在|在电梯里|在楼道里|那人|他)`)
var clippedSummaryRe = regexp.MustCompile(`[^。！？!?；;\n]{0,18}(?:这通话|这通电话只给了两个确认|两个确认：|最便宜的坑)[^。！？!?；;\n]*`)
var sentenceBoundaryRe = regexp.MustCompile(`[。！？!?；;\n]+`)
var stateMarkers = []string{"还", "在", "停在", "亮着", "没熄", "留着", "还在", "还亮着"}
var explanatoryTonePhrases = []string{
	"这意味着",
	"换句话说",
	"某种程度上",
	"从某种意义上",
	"值得注意的是",
	"不难看出",
	"由此可见",
	"显而易见",
	"本质上",
}
var templateEmotionPhrases = []string{
	"五味杂陈",
	"一时之间不知道该说什么",
	"空气仿佛凝固",
	"气氛变得微妙",
	"心里说不出的复杂",
	"复杂的情绪",
}
var vagueExpressionPhrases = []string{
	"某种东西",
	"一种说不清的感觉",
	"说不清道不明",
	"无形的压力",
	"难以言喻",
	"不可名状",
}
var stockSimilePhrases = []string{
	"像一根刺",
	"像一把刀",
	"像一根针",
	"像一块石头",
	"像潮水",
	"像被谁掐住喉咙",
}
var semanticPerplexityAbstractMarkers = []string{
	"这意味着",
	"这让他意识到",
	"这让她意识到",
	"终于明白",
	"不仅仅是",
	"更是",
	"某种程度上",
	"意义",
	"命运",
	"内心",
	"情绪",
	"感觉",
	"关系",
	"真相",
	"答案",
	"选择",
}
var semanticPerplexitySceneMarkers = []string{
	"手机", "电梯", "合同", "收据", "账单", "门牌", "价签", "椅子", "柜台", "纸条", "欠费单", "账本", "门缝", "门锁",
	"血印", "雨水", "水龙头", "筷子", "照片", "录音", "小票", "发票", "病历", "货架", "影子", "黑卡", "冥钞", "门禁卡",
	"工牌", "便利店", "医院", "公寓", "收银台", "纸箱", "“", "”", "「", "」",
}
var semanticPerplexitySceneActionRe = regexp.MustCompile(`(?:把|将|伸手|抬手|低头|回头|转身|按住|拿起|放下|推开|拉开|敲了|砸了|递给|接过|翻开|撕开|退后|停住|看见|看向|问他|问她|回答|走到|站在|坐在|签字|签下|冷汗|发疼|响了一下|亮着|暗下去)`)
var sentenceWithPunctuationRe = regexp.MustCompile(`[^。！？!?；;\n]+[。！？!?]?`)
var endingQuestionRe = regexp.MustCompile(`[？?]\s*$`)
var endingAphorismQuestionRe = regexp.MustCompile(`(?:命运|人生|世界|人心|救赎|自由|勇敢|孤独|残缺|意义|真正|所谓|原来|终究|从来|难道|谁又能|又有谁|还算|才是|最终的答案|真正的答案|最终的选择|真正的选择)[^。！？!?；;\n]{0,48}[？?]\s*$`)
var microActionRe = regexp.MustCompile(`[^。！？!?；;\n]{0,24}(?:(?:指腹|指尖|手指|肩膀|喉咙|掌心|脸皮|眼皮|嘴角|后背|脊背|手腕|膝盖)[^。！？!?；;\n]{0,12}(?:收紧|绷住|发紧|出汗|一抖|发颤|发麻|僵住|顿住|停住|发冷|发白)|[一-龥]{1,8}了?一下)[^。！？!?；;\n]*`)
var dramaticNegationRe = regexp.MustCompile(`[^。！？!?；;\n]{0,24}(?:(?:没有|没)(?:立刻|马上|急着|答|回答|开口|说话|让自己去想|让自己多想)|没有[^。！？!?；;\n]{1,18}[，,]?只[^。！？!?；;\n]{1,24})[^。！？!?；;\n]*`)

// notButRe Task 064：not-x-but-y 语义变体族——同族计数合并，防止 Writer 学会换皮规避：
// 不是A而是B / 并非…只是… / 与其说…不如说… / …的不是…，是… / 从来不是…，而是…
var notButRe = regexp.MustCompile(`(?:不是[^。！？!?；;\n]{1,30}[，,]?(?:而是|，是|,是)[^。！？!?；;\n]{1,30}|并非[^。！？!?；;\n]{1,24}[，,]?只是[^。！？!?；;\n]{1,30}|与其说[^。！？!?；;\n]{1,24}[，,]?不如说[^。！？!?；;\n]{1,30}|从来不是[^。！？!?；;\n]{1,24}[，,]?而是[^。！？!?；;\n]{1,30})`)
var preciseMeasureRe = regexp.MustCompile(`(?:一指|半指|两指|三指|一寸|半寸|两寸|三寸|数寸|一尺|半尺|两尺)`)
var patchPhraseRe = regexp.MustCompile(`(?:停了一拍|停了停|停了半拍|停住了|顿了一拍|顿了顿|慢了一拍|隔了一拍|卡了一下|卡了卡|僵了一拍)`)
var minorMistakeRe = regexp.MustCompile(`(?:手滑|拿错|写错|看错|听错|报错|认错|记错|漏了|掉了|碰掉|卡住|退错|走错|念错|按错|发错|删错)`)
var supportingQuipRe = regexp.MustCompile(`([一-龥]{2,4})[^。！？!?；;\n“”「」]{0,10}(?:骂|嘀咕|吐槽|抱怨|冷笑|嗤|啧|嚷)[^。！？!?；;\n“”「」]{0,12}[“「][^”」]{2,90}[”」]`)
var vagueQuantifierRe = regexp.MustCompile(`(?:一点|几分|半(?:点|分|步|拍|秒|晌|刻|截|句|声|口|圈))`)
var objectResponseRe = regexp.MustCompile(`[^。！？!?；;\n]{0,24}(?:(?:话音|声音|话|字|手指|按钮)[^。！？!?；;\n]{0,8}(?:刚落|刚出口|刚停|刚按下|刚碰到)|刚说完|才说完|刚写完|刚签下)?[^。！？!?；;\n]{0,24}(?:屏幕|手机|白纸|纸面|纸条|账单|欠费单|合同|条款|门牌|电梯|门锁|灯管|灯|广播|卡面|备忘录|提示框|系统|镜子|墙面|货架|价签)[^。！？!?；;\n]{0,18}(?:亮|暗|闪|响|震|跳|弹|显|浮|冒|多出|变成|改成|裂|动|滚|掉|滑)[^。！？!?；;\n]*`)
var objectResponseDelayRe = regexp.MustCompile(`(?:过了|隔了|等了|迟了|晚了|半晌|片刻|几秒|半分钟|很久)[^。！？!?；;\n]{0,36}(?:屏幕|手机|白纸|纸面|纸条|账单|欠费单|合同|条款|门牌|电梯|门锁|灯管|灯|广播|卡面|备忘录|提示框|系统|镜子|墙面|货架|价签)[^。！？!?；;\n]{0,18}(?:亮|暗|闪|响|震|跳|弹|显|浮|冒|多出|变成|改成|裂|动|滚|掉|滑)`)
var objectResponseAbsenceRe = regexp.MustCompile(`(?:没有回应|没回应|没有动静|没动静|什么都没发生|屏幕没亮|纸面没动|纸面没有动|白纸没动|白纸没有动|卡面没变|卡面没有变|灯没闪|门牌没变|门牌没有变|静默|安静|没人接话|无人接话)`)
var dialogueQuoteRe = regexp.MustCompile(`[“「]([^”」]{2,100})[”」]`)
var aphoristicDialogueRe = regexp.MustCompile(`(?:命运|人生|世界|人心|真正|所谓|终究|从来|答案|选择|自由|救赎|意义|真相|没有人|所有人|总有一天|不是[^，。！？!?]{1,18}而是|你以为|其实)`)
var templatedDialogueNameCallInTextRe = regexp.MustCompile(`[“「][一-龥]{2,4}[。！？!?]?[”」][^。！？!?；;\n]{0,12}叫[他她]`)
var templatedDialogueMicroBeatRe = regexp.MustCompile(`(?:叫[他她]|停住|停下|抬眼|抬头|看了[^。！？!?；;\n]{0,8}一眼|把[^。！？!?；;\n]{0,8}(?:停住|放下|推过来|推过去)|模板[^。！？!?；;\n]{0,12}推|笔[^。！？!?；;\n]{0,12}(?:停|顿))`)
var templatedDialogueProcedureRe = regexp.MustCompile(`(?:口径|字段|来源|管理建议|模板|确认|范围|流程|记录|说明|权限|演示|样本|审计|日志|保全|导出)`)
var chapterHeadingRe = regexp.MustCompile(`(?m)^#{1,3}\s*第[0-9零一二三四五六七八九十百]+章[^\n]*$`)
var formNoticeLeadRe = regexp.MustCompile(`(?:纸面写着|纸上写着|白纸[^。！？!?；;\n]{0,12}(?:显出|写着|显示)|卡面[^。！？!?；;\n]{0,18}(?:显字|逐行|浮出|显示)|账单[^。！？!?；;\n]{0,18}(?:写着|显示|显出)|欠费单[^。！？!?；;\n]{0,18}(?:写着|显示|显出)|条款[^。！？!?；;\n]{0,18}(?:写着|显示|显出)|系统提示|提示框|逐行显字)`)
var stiffTradeDialogueRe = regexp.MustCompile(`[“「][^”」]{0,50}(?:活到明早|活到明天|撑到明早|撑到明天)[^”」]{0,50}(?:按进价|不讲兄弟价|别讲兄弟价)[^”」]{0,50}[”」]`)
var systemMessageRe = regexp.MustCompile(`【([^】]{2,240})】`)
var abstractSystemReassuranceRe = regexp.MustCompile(`(?:钱没跑|陪你(?:换条路|找(?:条)?路)|规矩不撤|先喘(?:半)?口气|(?:这回)?算你[^。！？!?]{0,8}挣来的)`)
var uiTrialActionRe = regexp.MustCompile(`(?:试着?还[^。！？!?；;\n]{0,12}(?:信用卡|欠款|账)|点(?:击|开|了)?[^。！？!?；;\n]{0,10}(?:提现|转账|确认|按钮)|按下[^。！？!?；;\n]{0,10}(?:按钮|确认|提现)|确认键|改(?:了|掉)?[^。！？!?；;\n]{0,10}(?:备注|用途)|删(?:掉|了)[^。！？!?；;\n]{0,8}(?:备注|输入|内容)?|重新输入)`)
var uiTrialContextRe = regexp.MustCompile(`(?:手机|页面|按钮|屏幕|系统|备注|提现|转账|信用卡|确认键)`)
var dialogueActionLeadRe = regexp.MustCompile(`^(?:[^“「\n]{0,46})(?:夹着|端着|拿着|护住|划出|刚说了句|说了句|看见|抬起|抬眼|抬头|低头|推了|递了|敲了|收起|放下|咬了|喝了|指了|停了|看了|站起|坐下|把)(?:[^“「\n]{0,34})[：:，,]?[“「]`)
var opaqueProcedureSingletonRe = regexp.MustCompile(`(?:补上再测|补测|用途不符|真实改善消费|合规核验)`)
var trendLanguageSoundEffectMisuseRe = regexp.MustCompile(`(?:呱了(?:一)?声|(?:怪叫|喊|叫|发出|短促地|轻轻地|低低地)[^。！？!?\n]{0,20}呱[，,])`)
var systemProcedureNarrationRe = regexp.MustCompile(`(?:系统(?:判定|显示|提示)[：:]?[^。！？!?\n]{0,48}(?:本地新增交付|进入核验|核验通过|额度解锁)|阶段核验通过|(?:夜市)?小额改善额度解锁[：:]?\s*\d*)`)
var opaqueProcedureJargonTerms = []string{
	"采购凭证", "用途说明", "测试记录", "临时固定", "补测", "核验", "验收记录", "用途不符", "真实改善消费",
}
var designedRoleQuipMarkers = []string{
	"照牌", "审我", "采访", "开会", "汇报", "上课", "判案", "拍广告", "演戏", "查户口", "写报告", "走流程",
}
var procedureStageGroups = [][]string{
	{"答应", "同意", "做不了主", "只动", "别动", "授权"},
	{"报价", "总价", "多少钱", "五金店", "材料单", "四千二百八"},
	{"安装", "送装", "接线", "线槽", "卡扣", "工具箱", "上梯"},
	{"测试", "试过", "漏保", "保护开关", "测试键"},
	{"开票", "电子票", "票据", "付款码", "收款码", "支付", "付款记录"},
	{"检查", "核验", "放行", "收摊", "明早九点", "文旅中心"},
}
var bureaucraticRegisterCompoundRe = regexp.MustCompile(`(?:申请核验[^。！？!?；;\n]{0,48}(?:原始读取链|封存生成记录)|编号不一致[^。！？!?；;\n]{0,24}原因待核|只问版本[^。！？!?；;\n]{0,16}(?:不写人|不点名))`)
var emptyParallelChantRe = regexp.MustCompile(`不开[，,]不报[；;][^。！？!?“”「」]{0,16}不开[，,]不认[；;][^。！？!?“”「」]{0,16}不开[，,]不替`)
var deFaPhraseRe = regexp.MustCompile(`[一-龥]{1,10}(?:得发[潮虚沉乌黄紧硬白黑冷热麻颤暗亮干湿酸涩烫凉疼][一-龥]?|发(?:潮|虚|沉|乌|黄|紧|硬|白|黑|冷|热|麻|颤|暗|亮|干|湿|酸|涩|烫|凉|疼))`)
var duplicateWindControlRe = regexp.MustCompile(`(?s)当风控.{0,160}破风控|破风控.{0,160}当风控`)
var impossibleShadowGeometryRe = regexp.MustCompile(`肩膀以下[^。！？!?；;\n]{0,28}腰以上空了|腰以上空了[^。！？!?；;\n]{0,28}肩膀以下`)
var catEyeImpossibleReadRe = regexp.MustCompile(`(?:猫眼|门里|1704)[^。！？!?；;\n]{0,70}(?:白纸背面|背面翻起一角)[^。！？!?；;\n]{0,45}(?:代缴|双方确认|需双方确认)`)
var formImageMismatchRe = regexp.MustCompile(`四栏[^。！？!?；;\n]{0,30}像临时盖上去的章`)
var bureaucraticRegisterTerms = []string{
	"事由栏", "光标", "第一格", "演示记录", "封存回执", "封存", "回执", "申请核验", "核验", "原始读取链", "读取链", "生成记录",
	"记录员", "纪要", "编号不一致", "原因待核", "便签", "药量", "新补的数字", "内线窗口", "数据室", "只问版本", "不写人",
	"字段", "来源", "口径", "模板", "确认范围", "管理建议", "审计", "保全", "导出", "权限", "流程",
}
var colloquialRegisterMarkers = []string{
	"离谱", "催她", "编个", "那", "呢", "吧", "啊", "吗", "哎", "嗯", "别写", "别把", "别", "求你", "让他问",
	"对不上", "点你名", "写进去", "可主任", "行", "行吧", "算了", "少来", "别急", "先别", "别替", "你还", "我在",
}

// appendNonCJKFragments 报告拉丁字母片段的总次数与去重示例。
// 现代题材的合法英文（品牌名/缩写）也会命中——warning 级事实，由评审按题材裁定。
func appendNonCJKFragments(vs []Violation, text string) []Violation {
	matches := latinFragmentRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return vs
	}
	seen := make(map[string]struct{})
	var examples []string
	for _, m := range matches {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		if len(examples) < 3 {
			examples = append(examples, m)
		}
	}
	return append(vs, Violation{
		Rule:     "non_cjk_fragments",
		Target:   strings.Join(examples, "、"),
		Actual:   len(matches),
		Severity: SeverityWarning,
	})
}

var chineseDigits = map[rune]int{
	'零': 0,
	'一': 1,
	'二': 2,
	'两': 2,
	'三': 3,
	'四': 4,
	'五': 5,
	'六': 6,
	'七': 7,
	'八': 8,
	'九': 9,
}

func appendContentCountMismatch(vs []Violation, text string) []Violation {
	for _, match := range countWordRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		expected, ok := parseChineseSmallNumber(match[1])
		if !ok {
			continue
		}
		payload := trimQuotedPayload(match[2])
		actual := countCJK(payload)
		if actual == 0 || actual == expected {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "content_count_mismatch",
			Target:   strings.TrimSpace(match[0]),
			Limit:    expected,
			Actual:   actual,
			Severity: SeverityError,
		})
	}
	for _, match := range twoItemsAsTwoCharsRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		actual := countCJK(match[1] + match[2])
		if actual == 2 {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "content_count_mismatch",
			Target:   strings.TrimSpace(match[0]),
			Limit:    2,
			Actual:   actual,
			Severity: SeverityError,
		})
	}
	return vs
}

func appendPunctuationCadence(vs []Violation, text string) []Violation {
	narrative := stripQuotedText(text)
	if count := strings.Count(narrative, "；"); count > 6 {
		vs = append(vs, Violation{
			Rule:     "semicolon_overuse",
			Target:   joinExamples(semicolonExamples(narrative), 3),
			Limit:    6,
			Actual:   count,
			Severity: SeverityWarning,
		})
	}
	for _, sentence := range semicolonNoticeSegments(text) {
		if strings.Count(sentence, "；") >= 3 && formNoticeLeadRe.MatchString(sentence) {
			vs = append(vs, Violation{
				Rule:     "form_notice_semicolon_chain",
				Target:   truncateRunes(strings.TrimSpace(sentence), 90),
				Limit:    "条款/单据优先换行分项",
				Actual:   strings.Count(sentence, "；"),
				Severity: SeverityWarning,
			})
			break
		}
	}
	for _, match := range dialogueQuoteRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		quote := strings.TrimSpace(match[1])
		if strings.Contains(quote, "；") && !chantLikeDialogue(quote) {
			vs = append(vs, Violation{
				Rule:     "dialogue_semicolon_formality",
				Target:   truncateRunes(quote, 90),
				Limit:    "普通对白不用分号",
				Actual:   strings.Count(quote, "；"),
				Severity: SeverityWarning,
			})
			break
		}
	}
	if match := stiffTradeDialogueRe.FindString(text); strings.TrimSpace(match) != "" {
		vs = append(vs, Violation{
			Rule:     "stiff_trade_dialogue",
			Target:   truncateRunes(strings.Trim(match, "“”「」"), 90),
			Limit:    "讲价/互怼对白要像普通人说话",
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendSystemMessageSignals(vs []Violation, text string) []Violation {
	for _, paragraph := range narrativeParagraphs(text) {
		matches := systemMessageRe.FindAllString(paragraph, -1)
		if len(matches) == 0 {
			continue
		}
		if len(matches) == 1 && strings.TrimSpace(paragraph) == strings.TrimSpace(matches[0]) {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "system_message_inline",
			Target:   truncateRunes(strings.TrimSpace(paragraph), 110),
			Limit:    "系统完整内容放在一对【】内并独占一段，禁止【系统消息】标签后续接正文",
			Actual:   len(matches),
			Severity: SeverityError,
		})
		break
	}
	for _, match := range systemMessageRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		message := strings.TrimSpace(match[1])
		punctuation := strings.Count(message, "。") + strings.Count(message, "！") +
			strings.Count(message, "？") + strings.Count(message, "!") + strings.Count(message, "?")
		clauseBreaks := punctuation + strings.Count(message, "，") + strings.Count(message, "；")
		messageLen := len([]rune(message))
		if (punctuation < 4 && clauseBreaks < 5 && messageLen < 70) ||
			!containsAnyPhrase(message, []string{"系统", "额度", "任务", "核验", "用途", "消费", "奖励", "限时"}) {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "system_message_overpacked",
			Target:   truncateRunes(message, 100),
			Limit:    "系统单条消息只承担拒绝、安慰、解释、任务或奖励中的一项",
			Actual:   clauseBreaks,
			Severity: SeverityError,
		})
		break
	}
	for _, match := range systemMessageRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 || !abstractSystemReassuranceRe.MatchString(match[1]) {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "abstract_system_reassurance",
			Target:   truncateRunes(strings.TrimSpace(match[1]), 100),
			Limit:    "系统回应必须指向眼前问题、具体对象或可执行下一步",
			Actual:   1,
			Severity: SeverityWarning,
		})
		break
	}
	vs = appendOpaqueProcedureJargon(vs, text)
	return vs
}

func appendUITrialChecklist(vs []Violation, text string) []Violation {
	for _, paragraph := range narrativeParagraphs(text) {
		if !uiTrialContextRe.MatchString(paragraph) {
			continue
		}
		matches := uiTrialActionRe.FindAllString(paragraph, -1)
		if len(matches) < 3 {
			continue
		}
		return append(vs, Violation{
			Rule:     "ui_trial_checklist",
			Target:   truncateRunes(strings.TrimSpace(paragraph), 110),
			Limit:    "同一规则只保留一次会改变人物判断的界面试错",
			Actual:   len(matches),
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendOpaqueProcedureJargon(vs []Violation, text string) []Violation {
	messages := make([]string, 0)
	for _, match := range dialogueQuoteRe.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			messages = append(messages, strings.TrimSpace(match[1]))
		}
	}
	for _, match := range systemMessageRe.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			messages = append(messages, strings.TrimSpace(match[1]))
		}
	}
	for _, message := range messages {
		count := 0
		for _, term := range opaqueProcedureJargonTerms {
			if strings.Contains(message, term) {
				count++
			}
		}
		if !opaqueProcedureSingletonRe.MatchString(message) && count < 2 {
			continue
		}
		return append(vs, Violation{
			Rule:     "opaque_procedure_jargon",
			Target:   truncateRunes(message, 100),
			Limit:    "普通读者当场能看懂风险、后果和下一步；流程黑话不得成串",
			Actual:   count,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendTrendAndSystemVoiceSignals(vs []Violation, text string) []Violation {
	if match := trendLanguageSoundEffectMisuseRe.FindString(text); strings.TrimSpace(match) != "" {
		vs = append(vs, Violation{
			Rule:     "trend_language_sound_effect_misuse",
			Target:   truncateRunes(strings.TrimSpace(match), 100),
			Limit:    "“呱，”只能由角色直接开口并接完整吐槽，前面不得写叫声或拟声动作",
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	if match := systemProcedureNarrationRe.FindString(text); strings.TrimSpace(match) != "" {
		vs = append(vs, Violation{
			Rule:     "system_procedure_narration",
			Target:   truncateRunes(strings.TrimSpace(match), 100),
			Limit:    "系统短答必须让普通读者当场看懂，并体现陪伴声口；不得播报后台核验状态",
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendDialogueAndProcedureNaturalness(vs []Violation, text string) []Violation {
	for _, match := range dialogueQuoteRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		message := strings.TrimSpace(match[1])
		if !(strings.Contains(message, "，还是") || strings.Contains(message, ",还是") || strings.Contains(message, "，不是")) {
			continue
		}
		if !containsAnyPhrase(message, designedRoleQuipMarkers) {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "designed_role_quip",
			Target:   truncateRunes(message, 100),
			Limit:    "普通人物先说眼前麻烦，不替作者临时造工整比喻",
			Actual:   1,
			Severity: SeverityWarning,
		})
		break
	}

	paragraphs := narrativeParagraphs(text)
	const windowSize = 12
	for start := 0; start < len(paragraphs); start++ {
		end := min(len(paragraphs), start+windowSize)
		window := strings.Join(paragraphs[start:end], " ")
		stages := 0
		for _, group := range procedureStageGroups {
			if containsAnyPhrase(window, group) {
				stages++
			}
		}
		if stages < len(procedureStageGroups) {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "procedure_stage_pile",
			Target:   truncateRunes(window, 120),
			Limit:    "单场只保留一次关键交涉、一个结果和真正改变人物的后果",
			Actual:   stages,
			Severity: SeverityWarning,
		})
		break
	}
	return vs
}

func appendHumanFeelStructureSignals(vs []Violation, text string) []Violation {
	vs = appendBureaucraticRegisterOveruse(vs, text)
	vs = appendStructuredNoteTriplet(vs, text)
	vs = appendCardTOSBlock(vs, text)
	if match := emptyParallelChantRe.FindString(text); strings.TrimSpace(match) != "" {
		vs = append(vs, Violation{
			Rule:     "empty_parallel_chant",
			Target:   truncateRunes(match, 90),
			Limit:    "童谣不能连续空对仗",
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	matches := deFaPhraseRe.FindAllString(text, -1)
	if len(matches) > 4 {
		vs = append(vs, Violation{
			Rule:     "de_fa_adjective_repetition",
			Target:   joinExamples(matches, 4),
			Limit:    4,
			Actual:   len(matches),
			Severity: SeverityWarning,
		})
	}
	if match := duplicateWindControlRe.FindString(text); strings.TrimSpace(match) != "" {
		vs = append(vs, Violation{
			Rule:     "duplicate_dialogue_point",
			Target:   truncateRunes(match, 90),
			Limit:    "相邻对白不重复同一骂点",
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	if match := impossibleShadowGeometryRe.FindString(text); strings.TrimSpace(match) != "" {
		vs = append(vs, Violation{
			Rule:     "impossible_body_geometry",
			Target:   truncateRunes(match, 90),
			Limit:    "身体/影子空间关系必须可成像",
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	if match := catEyeImpossibleReadRe.FindString(text); strings.TrimSpace(match) != "" {
		vs = append(vs, Violation{
			Rule:     "impossible_line_of_sight",
			Target:   truncateRunes(match, 90),
			Limit:    "猫眼侧向视角不能读清背面小字",
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendCausalIntegritySignals(vs []Violation, text string) []Violation {
	if evidence := strings.Index(text, "你见过物业把人昵称改成承租物"); evidence >= 0 {
		change := strings.Index(text, "群昵称从")
		if change < 0 || evidence < change {
			vs = append(vs, Violation{
				Rule:     "causal_evidence_order",
				Target:   "你见过物业把人昵称改成承租物",
				Limit:    "角色只能指向已经出现的证据",
				Actual:   1,
				Severity: SeverityWarning,
			})
		}
	}

	if report := strings.Index(text, "报后四位"); report >= 0 {
		afterReport := text[report:]
		if static := strings.Index(afterReport, "电流声"); static >= 0 {
			afterStatic := afterReport[static+len("电流声"):]
			if change := strings.Index(afterStatic, "群昵称从"); change >= 0 {
				gap := afterStatic[:change]
				if strings.Contains(gap, "前夫") || strings.Contains(gap, "买票") || strings.Contains(gap, "售票口") || strings.Contains(gap, "物业以前") {
					vs = append(vs, Violation{
						Rule:     "identity_effect_delayed",
						Target:   truncateRunes(gap, 90),
						Limit:    "报身份后的规则后果要紧贴演示，不能被闲聊冲散",
						Actual:   1,
						Severity: SeverityWarning,
					})
				}
			}
		}
	}

	if strings.Contains(text, "阴阳公寓3栋") && strings.Contains(text, "5栋临时承租物") {
		vs = append(vs, Violation{
			Rule:     "building_floor_mismatch",
			Target:   "5栋临时承租物",
			Limit:    "楼栋和楼层称呼不能混用",
			Actual:   1,
			Severity: SeverityWarning,
		})
	}

	if start := strings.Index(text, "这通电话显然不是从基站过来的"); start >= 0 {
		after := text[start:]
		if firstKnownVoice := strings.Index(after, "你那边是不是也起雾"); firstKnownVoice >= 0 {
			gap := after[:firstKnownVoice]
			if !containsAnyPhrase(gap, []string{"多找", "少找", "旧账", "核验", "验证", "确认身份", "只有本人"}) {
				vs = append(vs, Violation{
					Rule:     "anomalous_phone_unverified",
					Target:   truncateRunes(gap, 90),
					Limit:    "非基站来电先核验身份，再相信对面声口",
					Actual:   1,
					Severity: SeverityWarning,
				})
			}
		}
	}

	if match := formImageMismatchRe.FindString(text); strings.TrimSpace(match) != "" {
		vs = append(vs, Violation{
			Rule:     "form_image_mismatch",
			Target:   truncateRunes(match, 90),
			Limit:    "栏位和章的形状不能错配",
			Actual:   1,
			Severity: SeverityWarning,
		})
	}

	if start := strings.Index(text, "冥府黑卡"); start >= 0 {
		end := start + 320
		if end > len(text) {
			end = len(text)
		}
		span := text[start:end]
		if strings.Contains(span, "须有") && !strings.Contains(span, "可确认") {
			vs = append(vs, Violation{
				Rule:     "card_core_rule_overblurred",
				Target:   truncateRunes(span, 90),
				Limit:    "黑卡核心规则可残缺，但关键可玩信息不能全糊掉",
				Actual:   1,
				Severity: SeverityWarning,
			})
		}
	}

	return vs
}

func appendBureaucraticRegisterOveruse(vs []Violation, text string) []Violation {
	body := stripMarkdownHeadingLines(text)
	if strings.TrimSpace(body) == "" {
		return vs
	}
	totalHits := countPhraseHits(body, bureaucraticRegisterTerms)
	compoundHits := len(bureaucraticRegisterCompoundRe.FindAllString(body, -1))
	if totalHits < 8 && compoundHits < 2 {
		return vs
	}
	colloquialHits := countPhraseHits(body, colloquialRegisterMarkers)
	if colloquialHits >= 5 && compoundHits < 2 {
		return vs
	}
	var dense []string
	for _, sentence := range sentenceWithPunctuationRe.FindAllString(body, -1) {
		s := strings.TrimSpace(sentence)
		if s == "" {
			continue
		}
		hits := countPhraseHits(s, bureaucraticRegisterTerms)
		if bureaucraticRegisterCompoundRe.MatchString(s) {
			hits += 2
		}
		if hits >= 2 {
			dense = append(dense, s)
		}
	}
	if len(dense) < 3 && compoundHits < 2 {
		return vs
	}
	vs = append(vs, Violation{
		Rule:     "bureaucratic_register_overuse",
		Target:   joinExamples(dense, 3),
		Limit:    "制度/纪要信息必须被人物口语、担责压力、动作或私人打断稀释",
		Actual:   totalHits,
		Severity: SeverityWarning,
	})
	return vs
}

func appendStructuredNoteTriplet(vs []Violation, text string) []Violation {
	lines := strings.Split(text, "\n")
	var run []string
	flush := func() bool {
		if len(run) < 3 || !structuredNoteRun(run) {
			run = nil
			return false
		}
		vs = append(vs, Violation{
			Rule:     "structured_note_triplet",
			Target:   strings.Join(run[:min(len(run), 3)], " / "),
			Limit:    "便签/备忘录不能写成三条工整风控手册",
			Actual:   len(run),
			Severity: SeverityWarning,
		})
		run = nil
		return true
	}
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if plainMemoLine(t) {
			run = append(run, t)
			continue
		}
		if flush() {
			return vs
		}
	}
	flush()
	return vs
}

func appendCardTOSBlock(vs []Violation, text string) []Violation {
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if !shortTermsLine(t) {
			continue
		}
		block := []string{t}
		for j := i + 1; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if !shortTermsLine(next) {
				break
			}
			block = append(block, next)
		}
		if len(block) < 4 {
			continue
		}
		contextStart := i - 3
		if contextStart < 0 {
			contextStart = 0
		}
		context := strings.Join(lines[contextStart:i], "\n")
		joined := strings.Join(block, " / ")
		if strings.Contains(context+joined, "黑卡") && strings.Contains(joined, "交易") && strings.Contains(joined, "账单") {
			vs = append(vs, Violation{
				Rule:     "card_tos_block",
				Target:   truncateRunes(joined, 100),
				Limit:    "黑卡/系统提示不能完整 ToS 式列项",
				Actual:   len(block),
				Severity: SeverityWarning,
			})
			return vs
		}
	}
	return vs
}

func plainMemoLine(line string) bool {
	if line == "" || strings.HasPrefix(line, "#") {
		return false
	}
	if strings.ContainsAny(line, "。！？!?；;，,：:、（）()“”「」") {
		return false
	}
	n := countCJK(line)
	return n >= 4 && n <= 18
}

func shortTermsLine(line string) bool {
	if line == "" || strings.HasPrefix(line, "#") {
		return false
	}
	if strings.ContainsAny(line, "。！？!?；;（）()“”「」") {
		return false
	}
	n := countCJK(line)
	return n >= 2 && n <= 24
}

func structuredNoteRun(lines []string) bool {
	joined := strings.Join(lines, "\n")
	hits := 0
	for _, marker := range []string{"代缴", "确认", "身份证", "名字", "零钱", "不回", "不碰"} {
		if strings.Contains(joined, marker) {
			hits++
		}
	}
	if hits >= 3 {
		return true
	}
	prefixCounts := map[rune]int{}
	for _, line := range lines {
		for _, r := range line {
			prefixCounts[r]++
			break
		}
	}
	return prefixCounts['不'] >= 2 && len(lines) >= 3
}

func semicolonNoticeSegments(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		start := 0
		for i, r := range line {
			if strings.ContainsRune("。！？!?", r) {
				segment := strings.TrimSpace(line[start : i+len(string(r))])
				if segment != "" {
					out = append(out, segment)
				}
				start = i + len(string(r))
			}
		}
		if tail := strings.TrimSpace(line[start:]); tail != "" {
			out = append(out, tail)
		}
	}
	return out
}

func stripQuotedText(text string) string {
	var b strings.Builder
	inQuote := false
	for _, r := range text {
		switch r {
		case '“', '「', '『':
			inQuote = true
			continue
		case '”', '」', '』':
			inQuote = false
			continue
		}
		if !inQuote {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func semicolonExamples(text string) []string {
	var examples []string
	for _, sentence := range sentenceWithPunctuationRe.FindAllString(text, -1) {
		if strings.Contains(sentence, "；") {
			examples = append(examples, truncateRunes(strings.TrimSpace(sentence), 50))
			if len(examples) >= 3 {
				break
			}
		}
	}
	return examples
}

func chantLikeDialogue(quote string) bool {
	if strings.Contains(quote, "儿歌") || strings.Contains(quote, "童谣") {
		return true
	}
	markers := 0
	for _, marker := range []string{"门开", "不开", "门认", "名认", "账认", "170"} {
		if strings.Contains(quote, marker) {
			markers++
		}
	}
	return markers >= 2
}

func parseChineseSmallNumber(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	if s == "十" {
		return 10, true
	}
	if strings.Contains(s, "十") {
		parts := strings.SplitN(s, "十", 2)
		tens := 1
		if parts[0] != "" {
			rs := []rune(parts[0])
			if len(rs) != 1 {
				return 0, false
			}
			var ok bool
			tens, ok = chineseDigits[rs[0]]
			if !ok {
				return 0, false
			}
		}
		ones := 0
		if parts[1] != "" {
			rs := []rune(parts[1])
			if len(rs) != 1 {
				return 0, false
			}
			var ok bool
			ones, ok = chineseDigits[rs[0]]
			if !ok {
				return 0, false
			}
		}
		return tens*10 + ones, true
	}
	rs := []rune(s)
	if len(rs) != 1 {
		return 0, false
	}
	n, ok := chineseDigits[rs[0]]
	return n, ok
}

func trimQuotedPayload(s string) string {
	for _, sep := range []string{"”", "\"", "」", "』", "】"} {
		if i := strings.Index(s, sep); i >= 0 {
			s = s[:i]
		}
	}
	return s
}

func countCJK(s string) int {
	n := 0
	for _, r := range s {
		if r >= '\u4e00' && r <= '\u9fff' {
			n++
		}
	}
	return n
}

func appendAwkwardSimile(vs []Violation, text string) []Violation {
	seen := map[string]struct{}{}
	for _, match := range forcedSimileRe.FindAllString(text, -1) {
		if _, ok := seen[match]; ok {
			continue
		}
		seen[match] = struct{}{}
		vs = append(vs, Violation{
			Rule:     "awkward_simile",
			Target:   match,
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	for _, phrase := range stockSimilePhrases {
		n := strings.Count(text, phrase)
		if n == 0 {
			continue
		}
		if _, ok := seen[phrase]; ok {
			continue
		}
		seen[phrase] = struct{}{}
		vs = append(vs, Violation{
			Rule:     "awkward_simile",
			Target:   phrase,
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendDanglingOrderWord(vs []Violation, text string) []Violation {
	for _, match := range ambiguousOrderRe.FindAllString(text, -1) {
		if orderFollowupRe.MatchString(match) {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "dangling_order_word",
			Target:   strings.TrimSpace(match),
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendAbruptStrongEvent(vs []Violation, text string) []Violation {
	for _, match := range abruptStrongEventRe.FindAllString(text, -1) {
		if eventSetupRe.MatchString(match) {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "abrupt_strong_event",
			Target:   strings.TrimSpace(match),
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendEvidenceAndMemoClarity(vs []Violation, text string) []Violation {
	for _, match := range unsupportedSpeechClaimRe.FindAllString(text, -1) {
		vs = append(vs, Violation{
			Rule:     "unsupported_speech_claim",
			Target:   strings.TrimSpace(match),
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	for _, match := range opaqueMemoShorthandRe.FindAllString(text, -1) {
		vs = append(vs, Violation{
			Rule:     "opaque_memo_shorthand",
			Target:   strings.TrimSpace(match),
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	for _, match := range unitNameAppositionRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 || strings.Contains(match[0], match[1]+"的") {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "unit_name_apposition",
			Target:   strings.TrimSpace(match[0]),
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	for _, match := range clippedHabitRe.FindAllString(text, -1) {
		if habitNaturalizerRe.MatchString(match) {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "clipped_habit_sentence",
			Target:   strings.TrimSpace(match),
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	for _, match := range clippedSummaryRe.FindAllString(text, -1) {
		vs = append(vs, Violation{
			Rule:     "clipped_summary_phrase",
			Target:   strings.TrimSpace(match),
			Actual:   1,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendStateClausePile(vs []Violation, text string) []Violation {
	for _, sent := range sentenceBoundaryRe.Split(text, -1) {
		sent = strings.TrimSpace(sent)
		if sent == "" {
			continue
		}
		commaCount := strings.Count(sent, "，") + strings.Count(sent, ",")
		stateHits := 0
		for _, marker := range stateMarkers {
			stateHits += strings.Count(sent, marker)
		}
		repeatedHai := strings.Count(sent, "还") >= 2
		if (commaCount >= 2 && stateHits >= 4) || (repeatedHai && commaCount >= 1) {
			vs = append(vs, Violation{
				Rule:     "state_clause_pile",
				Target:   truncateRunes(sent, 80),
				Actual:   stateHits,
				Severity: SeverityWarning,
			})
		}
	}
	return vs
}

func appendAntiAIPhraseSignals(vs []Violation, text string) []Violation {
	vs = appendPhraseListViolation(vs, text, "explanatory_tone", explanatoryTonePhrases)
	vs = appendPhraseListViolation(vs, text, "template_emotion", templateEmotionPhrases)
	vs = appendPhraseListViolation(vs, text, "vague_expression", vagueExpressionPhrases)
	return vs
}

func appendSemanticPerplexitySignals(vs []Violation, text string) []Violation {
	var run []string
	flush := func() {
		if len(run) < 3 {
			run = nil
			return
		}
		target := run[0]
		vs = append(vs, Violation{
			Rule:     "semantic_perplexity_low",
			Target:   truncateRunes(target, 100),
			Actual:   len(run),
			Limit:    "连续抽象判断句 < 3",
			Severity: SeverityWarning,
		})
		run = nil
	}
	for _, sent := range sentenceBoundaryRe.Split(text, -1) {
		sent = strings.TrimSpace(sent)
		if countCJK(sent) < 8 {
			continue
		}
		abstract := containsAnyPhrase(sent, semanticPerplexityAbstractMarkers) || containsAnyPhrase(sent, explanatoryTonePhrases) || containsAnyPhrase(sent, vagueExpressionPhrases)
		scene := hasSemanticSceneAnchor(sent)
		if abstract && !scene {
			run = append(run, sent)
			continue
		}
		flush()
	}
	flush()
	return vs
}

func appendEndingAphorismQuestion(vs []Violation, text string) []Violation {
	sentence := lastNarrativeSentence(text)
	if sentence == "" || !endingQuestionRe.MatchString(sentence) {
		return vs
	}
	if !endingAphorismQuestionRe.MatchString(sentence) {
		return vs
	}
	return append(vs, Violation{
		Rule:     "ending_aphorism_question",
		Target:   truncateRunes(sentence, 100),
		Actual:   1,
		Severity: SeverityWarning,
	})
}

func lastNarrativeSentence(text string) string {
	matches := sentenceWithPunctuationRe.FindAllString(stripMarkdownHeadingLines(text), -1)
	for i := len(matches) - 1; i >= 0; i-- {
		sentence := strings.TrimSpace(matches[i])
		if countCJK(sentence) == 0 {
			continue
		}
		return sentence
	}
	return ""
}

func appendCadenceSignals(vs []Violation, text string) []Violation {
	hanziCount := countCJK(text)
	vs = appendThresholdViolation(vs, text, microActionRe, "micro_action_overuse", "微动作节拍复读", scaledLimit(hanziCount, 3))
	vs = appendThresholdViolation(vs, text, dramaticNegationRe, "dramatic_negation_overuse", "戏剧性否定句式复读", 2)
	vs = appendThresholdViolation(vs, text, notButRe, "not_but_overuse", "不是A而是B句式复读", 1)
	vs = appendThresholdViolation(vs, text, preciseMeasureRe, "precise_measure_overuse", "精确计量口癖", 2)
	vs = appendThresholdViolation(vs, text, patchPhraseRe, "patch_phrase_overuse", "补丁替代表达复读", 2)
	vs = appendThresholdViolation(vs, text, minorMistakeRe, "minor_mistake_overuse", "刻意小失误过量", 2)
	vs = appendThresholdViolation(vs, text, vagueQuantifierRe, "vague_quantifier_overuse", "高频虚量词", 4)
	vs = appendParagraphStartRepetition(vs, text)
	vs = appendIsolatedSentenceOveruse(vs, text)
	vs = appendSupportingQuipOveruse(vs, text)
	vs = appendObjectResponseIssues(vs, text)
	vs = appendDialogueAphorismOveruse(vs, text)
	vs = appendTemplatedDialogueChain(vs, text)
	vs = appendDialogueActionLeadRepetition(vs, text)
	vs = appendSerialDeviceRepetition(vs, text)
	return vs
}

func appendDialogueActionLeadRepetition(vs []Violation, text string) []Violation {
	paragraphs := narrativeParagraphs(text)
	run := 0
	var examples []string
	for _, paragraph := range paragraphs {
		if !strings.ContainsAny(paragraph, "“「") {
			run = 0
			examples = nil
			continue
		}
		if !dialogueActionLeadRe.MatchString(strings.TrimSpace(paragraph)) {
			run = 0
			examples = nil
			continue
		}
		run++
		if len(examples) < 3 {
			examples = append(examples, truncateRunes(strings.TrimSpace(paragraph), 48))
		}
		if run < 3 {
			continue
		}
		return append(vs, Violation{
			Rule:     "dialogue_action_lead_repetition",
			Target:   strings.Join(examples, " / "),
			Limit:    2,
			Actual:   run,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendThresholdViolation(vs []Violation, text string, re *regexp.Regexp, rule, label string, limit int) []Violation {
	matches := re.FindAllString(text, -1)
	if len(matches) <= limit {
		return vs
	}
	return append(vs, Violation{
		Rule:     rule,
		Target:   joinExamples(matches, 3),
		Limit:    limit,
		Actual:   len(matches),
		Severity: SeverityWarning,
	})
}

func appendParagraphStartRepetition(vs []Violation, text string) []Violation {
	paragraphs := narrativeParagraphs(text)
	if len(paragraphs) < 3 {
		return vs
	}
	var previous string
	run := 0
	for _, paragraph := range paragraphs {
		start := paragraphStartToken(paragraph)
		if start == "" {
			previous = ""
			run = 0
			continue
		}
		if start == previous {
			run++
		} else {
			previous = start
			run = 1
		}
		if run >= 3 {
			return append(vs, Violation{
				Rule:     "paragraph_start_repetition",
				Target:   start,
				Limit:    "连续段首同主语 < 3",
				Actual:   run,
				Severity: SeverityWarning,
			})
		}
	}
	return vs
}

func appendIsolatedSentenceOveruse(vs []Violation, text string) []Violation {
	paragraphs := narrativeParagraphs(text)
	count := 0
	var examples []string
	for i, paragraph := range paragraphs {
		if ignoreIsolatedSentenceParagraph(i, paragraph) {
			continue
		}
		sentences := sentenceBoundaryRe.Split(paragraph, -1)
		sentenceCount := 0
		for _, sentence := range sentences {
			if countCJK(sentence) > 0 {
				sentenceCount++
			}
		}
		if sentenceCount == 1 {
			count++
			if len(examples) < 3 {
				examples = append(examples, truncateRunes(strings.TrimSpace(paragraph), 40))
			}
		}
	}
	if count <= 4 {
		return vs
	}
	return append(vs, Violation{
		Rule:     "isolated_sentence_overuse",
		Target:   strings.Join(examples, " / "),
		Limit:    4,
		Actual:   count,
		Severity: SeverityWarning,
	})
}

func ignoreIsolatedSentenceParagraph(index int, paragraph string) bool {
	p := strings.TrimSpace(paragraph)
	if p == "" {
		return true
	}
	if index == 0 && isPlainChapterTitle(p) {
		return true
	}
	return isDialogueOnlyParagraph(p) || isStandaloneSystemMessageParagraph(p)
}

func isStandaloneSystemMessageParagraph(paragraph string) bool {
	p := strings.TrimSpace(paragraph)
	match := systemMessageRe.FindStringIndex(p)
	return match != nil && match[0] == 0 && match[1] == len(p) && len(systemMessageRe.FindAllStringIndex(p, -1)) == 1
}

func isPlainChapterTitle(p string) bool {
	if countCJK(p) > 24 {
		return false
	}
	p = strings.TrimSpace(p)
	return (strings.HasPrefix(p, "第") && strings.Contains(p, "章")) ||
		(strings.HasPrefix(p, "Chapter ") && len([]rune(p)) <= 24)
}

func isDialogueOnlyParagraph(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || !(strings.HasPrefix(p, "“") || strings.HasPrefix(p, "\"") || strings.HasPrefix(p, "「")) {
		return false
	}
	for _, r := range []string{"”", "\"", "」"} {
		if strings.HasSuffix(p, r) {
			return true
		}
	}
	return false
}

func appendSupportingQuipOveruse(vs []Violation, text string) []Violation {
	counts := map[string]int{}
	examples := map[string][]string{}
	for _, match := range supportingQuipRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		name := match[1]
		counts[name]++
		if len(examples[name]) < 3 {
			examples[name] = append(examples[name], truncateRunes(strings.TrimSpace(match[0]), 40))
		}
	}
	for name, count := range counts {
		if count <= 3 {
			continue
		}
		return append(vs, Violation{
			Rule:     "supporting_quip_overuse",
			Target:   name + ": " + strings.Join(examples[name], " / "),
			Limit:    3,
			Actual:   count,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendObjectResponseIssues(vs []Violation, text string) []Violation {
	matches := objectResponseRe.FindAllString(text, -1)
	if len(matches) > 4 {
		vs = append(vs, Violation{
			Rule:     "object_response_overuse",
			Target:   joinExamples(matches, 3),
			Limit:    4,
			Actual:   len(matches),
			Severity: SeverityWarning,
		})
	}
	if len(matches) >= 3 && (objectResponseDelayRe.FindString(text) == "" || objectResponseAbsenceRe.FindString(text) == "") {
		missing := []string{}
		if objectResponseDelayRe.FindString(text) == "" {
			missing = append(missing, "延迟")
		}
		if objectResponseAbsenceRe.FindString(text) == "" {
			missing = append(missing, "缺席/静默")
		}
		vs = append(vs, Violation{
			Rule:     "object_response_rhythm_flat",
			Target:   strings.Join(missing, "、"),
			Limit:    "物件回应至少一次延迟、一次缺席",
			Actual:   len(matches),
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendDialogueAphorismOveruse(vs []Violation, text string) []Violation {
	run := 0
	total := 0
	var examples []string
	for _, match := range dialogueQuoteRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 || !aphoristicDialogueRe.MatchString(match[1]) {
			run = 0
			continue
		}
		total++
		run++
		if len(examples) < 3 {
			examples = append(examples, truncateRunes(match[1], 40))
		}
		if run > 3 {
			return append(vs, Violation{
				Rule:     "dialogue_aphorism_overuse",
				Target:   strings.Join(examples, " / "),
				Limit:    "连续警句式应答 <= 3",
				Actual:   run,
				Severity: SeverityWarning,
			})
		}
	}
	if total > 4 {
		return append(vs, Violation{
			Rule:     "dialogue_aphorism_overuse",
			Target:   strings.Join(examples, " / "),
			Limit:    4,
			Actual:   total,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func appendTemplatedDialogueChain(vs []Violation, text string) []Violation {
	paragraphs := narrativeParagraphs(text)
	if len(paragraphs) < 3 {
		return vs
	}
	var examples []string
	for i := 0; i < len(paragraphs); i++ {
		end := i + 4
		if end > len(paragraphs) {
			end = len(paragraphs)
		}
		window := strings.Join(paragraphs[i:end], "\n")
		quotes := dialogueQuoteRe.FindAllStringSubmatch(window, -1)
		if len(quotes) < 3 {
			continue
		}
		if !templatedDialogueNameCallInTextRe.MatchString(window) || !templatedDialogueMicroBeatRe.MatchString(window) || !templatedDialogueProcedureRe.MatchString(window) {
			continue
		}
		examples = append(examples, truncateRunes(window, 70))
		i = end - 1
	}
	if len(examples) == 0 {
		return vs
	}
	return append(vs, Violation{
		Rule:     "templated_dialogue_chain",
		Target:   joinExamples(examples, 2),
		Limit:    0,
		Actual:   len(examples),
		Severity: SeverityWarning,
	})
}

func appendSerialDeviceRepetition(vs []Violation, text string) []Violation {
	chapters := splitChaptersForDeviceScan(text)
	if len(chapters) < 3 {
		return vs
	}
	for _, edge := range []string{"opening", "ending"} {
		previous := ""
		run := 0
		for _, chapter := range chapters {
			device := chapterDevice(chapter, edge)
			if device == "" {
				previous = ""
				run = 0
				continue
			}
			if device == previous {
				run++
			} else {
				previous = device
				run = 1
			}
			if run > 2 {
				return append(vs, Violation{
					Rule:     "serial_device_repetition",
					Target:   edge + ": " + device,
					Limit:    "同一开头/结尾装置连用 <= 2 章",
					Actual:   run,
					Severity: SeverityWarning,
				})
			}
		}
	}
	return vs
}

func splitChaptersForDeviceScan(text string) []string {
	matches := chapterHeadingRe.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return []string{text}
	}
	var chapters []string
	for i, match := range matches {
		start := match[1]
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		if chapter := strings.TrimSpace(text[start:end]); chapter != "" {
			chapters = append(chapters, chapter)
		}
	}
	return chapters
}

func chapterDevice(chapter, edge string) string {
	paragraphs := narrativeParagraphs(chapter)
	if len(paragraphs) == 0 {
		return ""
	}
	target := paragraphs[0]
	if edge == "ending" {
		target = paragraphs[len(paragraphs)-1]
	}
	return classifySerialDevice(target)
}

func classifySerialDevice(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	if regexp.MustCompile(`(?:屏幕|手机|提示框|系统|弹窗|监控)[^。！？!?；;\n]{0,30}(?:显示|显字|亮|跳出|弹出|多出|刷新)`).MatchString(t) {
		return "屏幕显字"
	}
	if regexp.MustCompile(`(?:白纸|纸面|纸条|账单|欠费单|合同|条款|便签|卡面)[^。！？!?；;\n]{0,30}(?:显示|显字|多出|写着|浮出|变成|改成)`).MatchString(t) {
		return "纸面显字"
	}
	if regexp.MustCompile(`(?:话没说完|没说完|说到一半|[—…]\s*$|[“「][^”」]{2,60}$)`).MatchString(t) {
		return "对话截断"
	}
	if regexp.MustCompile(`(?:门牌|灯|灯管|门锁|黑卡|账单|欠费单|纸条|价签|货架)[^。！？!?；;\n]{0,30}(?:亮|暗|闪|响|震|跳|显|浮|多出|变成|改成|停|裂|动|滚|掉|滑)`).MatchString(t) {
		return "凶兆物微动"
	}
	return ""
}

func narrativeParagraphs(text string) []string {
	raw := strings.Split(stripMarkdownHeadingLines(text), "\n")
	var paragraphs []string
	var buf []string
	flush := func() {
		p := strings.TrimSpace(strings.Join(buf, "\n"))
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
		buf = nil
	}
	for _, line := range raw {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return paragraphs
}

func paragraphStartToken(paragraph string) string {
	t := strings.TrimLeft(strings.TrimSpace(paragraph), "“\"「『（(")
	for _, pronoun := range []string{"他", "她", "我"} {
		if strings.HasPrefix(t, pronoun) {
			return pronoun
		}
	}
	var rs []rune
	for _, r := range t {
		if r < '\u4e00' || r > '\u9fff' {
			break
		}
		rs = append(rs, r)
		if len(rs) == 3 {
			break
		}
	}
	if len(rs) < 2 {
		return ""
	}
	return string(rs)
}

func scaledLimit(hanziCount, perThreeK int) int {
	if hanziCount <= 3000 {
		return perThreeK
	}
	return perThreeK * ((hanziCount + 2999) / 3000)
}

func joinExamples(matches []string, limit int) string {
	seen := make(map[string]struct{})
	var examples []string
	for _, match := range matches {
		example := truncateRunes(strings.TrimSpace(match), 40)
		if _, ok := seen[example]; ok {
			continue
		}
		seen[example] = struct{}{}
		examples = append(examples, example)
		if len(examples) == limit {
			break
		}
	}
	return strings.Join(examples, " / ")
}

func stripMarkdownHeadingLines(text string) string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func appendPhraseListViolation(vs []Violation, text, rule string, phrases []string) []Violation {
	for _, phrase := range phrases {
		n := strings.Count(text, phrase)
		if n == 0 {
			continue
		}
		vs = append(vs, Violation{
			Rule:     rule,
			Target:   phrase,
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	return vs
}

func containsAnyPhrase(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func countPhraseHits(text string, phrases []string) int {
	total := 0
	for _, phrase := range phrases {
		if phrase == "" {
			continue
		}
		total += strings.Count(text, phrase)
	}
	return total
}

func hasSemanticSceneAnchor(sent string) bool {
	return containsAnyPhrase(sent, semanticPerplexitySceneMarkers) || semanticPerplexitySceneActionRe.MatchString(sent)
}

func truncateRunes(s string, limit int) string {
	rs := []rune(s)
	if len(rs) <= limit {
		return s
	}
	return string(rs[:limit])
}
