package rules

import (
	"strings"
	"testing"
)

func TestLint_CleanText(t *testing.T) {
	if vs := Lint("# 第一章 风起\n他迈步向前。\n夜色渐深。"); len(vs) != 0 {
		t.Errorf("clean text should pass: %+v", vs)
	}
}

func TestLint_MarkdownResidue(t *testing.T) {
	text := "# 第一章\n这是**重点**内容。\n## 小标题\n正文。"
	vs := Lint(text)
	bold := findViolation(vs, "markdown_residue", "**")
	if bold == nil || bold.Actual != 2 {
		t.Errorf("expected ** residue x2: %+v", vs)
	}
	heading := findViolation(vs, "markdown_residue", "#")
	if heading == nil || heading.Actual != 1 {
		t.Errorf("expected 1 heading beyond first line: %+v", vs)
	}
}

func TestLint_NonCJKFragments(t *testing.T) {
	text := "# 第一章\n他发现了一个pattern，这个pattern像DNA一样规律。"
	vs := Lint(text)
	var v *Violation
	for i := range vs {
		if vs[i].Rule == "non_cjk_fragments" {
			v = &vs[i]
			break
		}
	}
	if v == nil {
		t.Fatalf("expected non_cjk violation: %+v", vs)
	}
	if v.Actual != 3 {
		t.Errorf("total count: got %v want 3", v.Actual)
	}
	if !strings.Contains(v.Target, "pattern") || !strings.Contains(v.Target, "DNA") {
		t.Errorf("examples should be distinct: %q", v.Target)
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity: %v", v.Severity)
	}
}

func TestLint_ContentCountMismatch(t *testing.T) {
	text := "# 第一章\n备注栏里留着下午没删的四个字：抵押物待核。"
	vs := Lint(text)
	v := findViolation(vs, "content_count_mismatch", "四个字：抵押物待核")
	if v == nil {
		t.Fatalf("expected content_count_mismatch violation: %+v", vs)
	}
	if v.Limit != 4 {
		t.Errorf("limit=%v, want 4", v.Limit)
	}
	if v.Actual != 5 {
		t.Errorf("actual=%v, want 5", v.Actual)
	}
	if v.Severity != SeverityError {
		t.Errorf("severity=%v, want error", v.Severity)
	}
}

func TestLint_TwoItemsAsTwoChars(t *testing.T) {
	text := "# 第一章\n小票被酸奶洇过，薄荷糖和创可贴两个字发虚。"
	vs := Lint(text)
	v := findViolation(vs, "content_count_mismatch", "薄荷糖和创可贴两个字")
	if v == nil {
		t.Fatalf("expected content_count_mismatch violation: %+v", vs)
	}
	if v.Limit != 2 {
		t.Errorf("limit=%v, want 2", v.Limit)
	}
	if v.Actual != 6 {
		t.Errorf("actual=%v, want 6", v.Actual)
	}
	if v.Severity != SeverityError {
		t.Errorf("severity=%v, want error", v.Severity)
	}
}

func TestLint_ContentCountMatch(t *testing.T) {
	text := "# 第一章\n红票子边角多了四个蓝字：儿童银行。"
	if vs := Lint(text); len(vs) != 0 {
		t.Fatalf("matching count should pass: %+v", vs)
	}
}

func TestLint_AwkwardSimile(t *testing.T) {
	text := "# 第一章\n江烬家的挂钟先停，秒针卡在十二上，像一根没拔干净的刺。"
	v := findViolation(Lint(text), "awkward_simile", "像一根没拔干净的刺")
	if v == nil {
		t.Fatal("expected awkward_simile violation")
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity=%v, want warning", v.Severity)
	}
}

func TestLint_DanglingOrderWord(t *testing.T) {
	text := "# 第一章\n江烬家的挂钟先停了。秒针卡在十二上。"
	v := findViolation(Lint(text), "dangling_order_word", "江烬家的挂钟先停了")
	if v == nil {
		t.Fatal("expected dangling_order_word violation")
	}
}

func TestLint_DanglingOrderWordAllowsFollowup(t *testing.T) {
	text := "# 第一章\n路由器先灭了，随后电脑屏幕也黑下去。"
	if v := findViolation(Lint(text), "dangling_order_word", "路由器先灭了，随后电脑屏幕也黑下去"); v != nil {
		t.Fatalf("follow-up order marker should not violate: %+v", v)
	}
}

func TestLint_DanglingOrderWordCatchesCloseMouth(t *testing.T) {
	text := "# 第一章\n话说完，他自己先闭嘴。"
	v := findViolation(Lint(text), "dangling_order_word", "话说完，他自己先闭嘴")
	if v == nil {
		t.Fatal("expected dangling_order_word violation")
	}
}

func TestLint_AbruptStrongEvent(t *testing.T) {
	text := "# 第一章\n隔壁1703忽然砸门。"
	v := findViolation(Lint(text), "abrupt_strong_event", "隔壁1703忽然砸门")
	if v == nil {
		t.Fatal("expected abrupt_strong_event violation")
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity=%v, want warning", v.Severity)
	}
}

func TestLint_AbruptStrongEventAllowsSetup(t *testing.T) {
	text := "# 第一章\n墙那边传来一声闷响，蒋牧突然砸门。"
	if v := findViolation(Lint(text), "abrupt_strong_event", "蒋牧突然砸门"); v != nil {
		t.Fatalf("setup should not violate: %+v", v)
	}
}

func TestLint_UnsupportedSpeechClaim(t *testing.T) {
	text := "# 第一章\n蒋牧扑过来。“江烬！你在家，对吧？我听见你说话了。”"
	v := findViolation(Lint(text), "unsupported_speech_claim", "我听见你说话了")
	if v == nil {
		t.Fatal("expected unsupported_speech_claim violation")
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity=%v, want warning", v.Severity)
	}
}

func TestLint_OpaqueMemoShorthand(t *testing.T) {
	text := "# 第一章\n他在备忘录里写：普通钱无效。代缴需双方确认。别回号。零钱暂不碰。"
	v := findViolation(Lint(text), "opaque_memo_shorthand", "别回号")
	if v == nil {
		t.Fatal("expected opaque_memo_shorthand violation")
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity=%v, want warning", v.Severity)
	}
	v = findViolation(Lint(text), "opaque_memo_shorthand", "零钱暂不碰")
	if v == nil {
		t.Fatal("expected opaque_memo_shorthand violation for clipped memo wording")
	}
}

func TestLint_AllowsNaturalMemoJudgment(t *testing.T) {
	text := "# 第一章\n他在备忘录里写：普通钱无效。代缴需双方确认。不回复身份证号和名字。零钱暂时不碰。"
	if v := findViolation(Lint(text), "opaque_memo_shorthand", "零钱暂时不碰"); v != nil {
		t.Fatalf("natural memo wording should not violate: %+v", v)
	}
}

func TestLint_UnitNameApposition(t *testing.T) {
	text := "# 第一章\n这个点还会碰他门牌的，多半是1703蒋牧。"
	v := findViolation(Lint(text), "unit_name_apposition", "这个点还会碰他门牌的，多半是1703蒋牧")
	if v == nil {
		t.Fatal("expected unit_name_apposition violation")
	}
}

func TestLint_ClippedHabitSentence(t *testing.T) {
	text := "# 第一章\n搬来两个月，电梯里刷短视频外放，半夜还总把外卖拿错。"
	v := findViolation(Lint(text), "clipped_habit_sentence", "搬来两个月，电梯里刷短视频外放，半夜还总把外卖拿错")
	if v == nil {
		t.Fatal("expected clipped_habit_sentence violation")
	}
}

func TestLint_AllowsNaturalHabitSentence(t *testing.T) {
	text := "# 第一章\n多半是1703的蒋牧。搬来两个月，经常在电梯里刷短视频外放，半夜还总拿错外卖。"
	if v := findViolation(Lint(text), "unit_name_apposition", "1703的蒋牧"); v != nil {
		t.Fatalf("natural unit apposition should not violate: %+v", v)
	}
	if v := findViolation(Lint(text), "clipped_habit_sentence", "搬来两个月，经常在电梯里"); v != nil {
		t.Fatalf("natural habit sentence should not violate: %+v", v)
	}
}

func TestLint_ClippedSummaryPhrase(t *testing.T) {
	text := "# 第一章\n这通话只给了两个确认：外面也在收费，问名字是最便宜的坑。"
	v := findViolation(Lint(text), "clipped_summary_phrase", "这通话只给了两个确认：外面也在收费，问名字是最便宜的坑")
	if v == nil {
		t.Fatal("expected clipped_summary_phrase violation")
	}
}

func TestLint_AllowsNaturalSummaryPhrase(t *testing.T) {
	text := "# 第一章\n这通电话只让他确认两件事：外面也在收费；名字不能随便报。"
	if v := findViolation(Lint(text), "clipped_summary_phrase", "这通电话只让他确认两件事"); v != nil {
		t.Fatalf("natural summary should not violate: %+v", v)
	}
}

func TestLint_StateClausePile(t *testing.T) {
	text := "# 第一章\n电脑屏幕还亮着，客户逾期表停在最后一行，备注栏里下午没删的批注还在：抵押物待核。"
	v := findViolation(Lint(text), "state_clause_pile", "电脑屏幕还亮着，客户逾期表停在最后一行，备注栏里下午没删的批注还在：抵押物待核")
	if v == nil {
		t.Fatal("expected state_clause_pile violation")
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity=%v, want warning", v.Severity)
	}
}

func TestLint_StateClausePileAllowsSplitState(t *testing.T) {
	text := "# 第一章\n电脑屏幕没有熄。客户逾期表停在最后一行，备注栏留着下午那句批注。"
	if v := findViolation(Lint(text), "state_clause_pile", "客户逾期表停在最后一行，备注栏留着下午那句批注"); v != nil {
		t.Fatalf("split state should not violate: %+v", v)
	}
}

func TestLint_AntiAIPhraseSignals(t *testing.T) {
	text := "# 第一章\n这意味着他已经失去选择。空气仿佛凝固，一种说不清的感觉压下来。"
	vs := Lint(text)
	if v := findViolation(vs, "explanatory_tone", "这意味着"); v == nil {
		t.Fatalf("expected explanatory_tone violation: %+v", vs)
	}
	if v := findViolation(vs, "template_emotion", "空气仿佛凝固"); v == nil {
		t.Fatalf("expected template_emotion violation: %+v", vs)
	}
	if v := findViolation(vs, "vague_expression", "一种说不清的感觉"); v == nil {
		t.Fatalf("expected vague_expression violation: %+v", vs)
	}
}

func TestLint_SemanticPerplexityLow(t *testing.T) {
	text := "# 第一章\n这意味着他已经失去选择。某种程度上，这不仅仅是一次失败。真正的答案藏在命运背后。复杂的情绪在内心堆积。"
	v := findViolation(Lint(text), "semantic_perplexity_low", "这意味着他已经失去选择")
	if v == nil {
		t.Fatal("expected semantic_perplexity_low violation")
	}
	if v.Actual != 4 {
		t.Errorf("actual=%v, want 4", v.Actual)
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity=%v, want warning", v.Severity)
	}
}

func TestLint_SemanticPerplexityAllowsSceneAnchors(t *testing.T) {
	text := "# 第一章\n这意味着他不能再拖。江烬把收据按在柜台上，指腹沾到一点冷汗。某种程度上，这个答案已经够清楚。温梨抬眼问他：“你还要继续签吗？”"
	if v := findViolation(Lint(text), "semantic_perplexity_low", "这意味着他不能再拖"); v != nil {
		t.Fatalf("scene anchors should break abstract run: %+v", v)
	}
}

func TestLint_EndingAphorismQuestion(t *testing.T) {
	text := "# 第一章\n门外的欠费单被风吹到他脚边。\n这难道才是真正的选择吗？"
	v := findViolation(Lint(text), "ending_aphorism_question", "这难道才是真正的选择吗？")
	if v == nil {
		t.Fatal("expected ending_aphorism_question violation")
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity=%v, want warning", v.Severity)
	}
}

func TestLint_AllowsConcreteEndingQuestion(t *testing.T) {
	text := "# 第一章\n门外的欠费单被风吹到他脚边。\n门后第二声敲门，又是谁？"
	if v := findViolation(Lint(text), "ending_aphorism_question", "门后第二声敲门，又是谁？"); v != nil {
		t.Fatalf("concrete ending question should not violate: %+v", v)
	}
}

func TestLint_CadenceSignals(t *testing.T) {
	text := `# 第一章

他没有立刻回答。指腹收紧了一下。

他没急着开口。肩膀绷住。

他没让自己去想。喉咙发紧。

他没马上说话。掌心出汗。

他没有回头，只把账单压住。

不是他怕了，而是门外太静。

不是账单变了，而是名字变了。

水线漫过一指，又涨到半寸，随后贴近两寸。

他停了一拍。

他停了停。

他停住了。

他拿错了纸。

他手滑按错了门牌。

他又手滑发错了消息。

孤句一。

孤句二。

孤句三。

孤句四。

孤句五。

蒋牧骂道：“电梯坏了，我爬十七层。”

蒋牧嘀咕：“你们这队排得像医院。”

蒋牧抱怨：“孩子哭半天了。”

蒋牧嚷道：“谁又把胶带拿走了？”

温梨说得慢了一点，手也低了几分，又往后退半步，停了半拍，漏了半句。

他刚说完，屏幕亮起一行字。

她才说完，欠费单多出一行字。

他刚按下按钮，门牌闪了一下。

话音刚落，手机跳出提示。

声音刚停，灯管暗了一截。

“真正的选择从来不在门后。”

“命运只会把答案交给敢看的人。”

“所谓自由，其实是你愿不愿意付账。”

“人心终究比系统更贵。”

# 第二章

他抬头时，屏幕显示：余额不足。

屏幕显示：下一位。

# 第三章

她进门时，屏幕显示：余额不足。

屏幕显示：下一位。

# 第四章

蒋牧站住，屏幕显示：余额不足。

屏幕显示：下一位。`

	vs := Lint(text)
	for _, rule := range []string{
		"micro_action_overuse",
		"dramatic_negation_overuse",
		"paragraph_start_repetition",
		"not_but_overuse",
		"precise_measure_overuse",
		"patch_phrase_overuse",
		"minor_mistake_overuse",
		"isolated_sentence_overuse",
		"supporting_quip_overuse",
		"vague_quantifier_overuse",
		"object_response_overuse",
		"object_response_rhythm_flat",
		"dialogue_aphorism_overuse",
		"serial_device_repetition",
	} {
		if v := findRule(vs, rule); v == nil {
			t.Fatalf("expected %s violation, got %+v", rule, vs)
		}
	}
}

func TestLint_CadenceSignalsAllowLightUse(t *testing.T) {
	text := `# 第一章

门外有人排队，胶带卷从桌边滚到地上。

“电梯坏了，我爬十七层。”蒋牧骂了一句。

江烬没有立刻接话。他把欠费单压住。

温梨问：“我回去拿东西，出事你负责？”`
	for _, rule := range []string{
		"micro_action_overuse",
		"dramatic_negation_overuse",
		"paragraph_start_repetition",
		"not_but_overuse",
		"precise_measure_overuse",
		"patch_phrase_overuse",
		"minor_mistake_overuse",
		"isolated_sentence_overuse",
		"supporting_quip_overuse",
		"vague_quantifier_overuse",
		"object_response_overuse",
		"object_response_rhythm_flat",
		"dialogue_aphorism_overuse",
		"templated_dialogue_chain",
		"serial_device_repetition",
	} {
		if v := findRule(Lint(text), rule); v != nil {
			t.Fatalf("unexpected %s violation: %+v", rule, v)
		}
	}
}

func TestLint_IsolatedSentenceIgnoresPlainTitleAndPureDialogue(t *testing.T) {
	text := `第一章 讲稿第一句

“到了吗？”

“还差两分钟。”

“你先别签。”

“我看一眼。”

“行。”

许闻溪把讲稿往怀里收了收，侧台的灯从她手背上擦过去。`

	if v := findRule(Lint(text), "isolated_sentence_overuse"); v != nil {
		t.Fatalf("plain title and pure dialogue should not trigger isolated_sentence_overuse: %+v", v)
	}
}

func TestLint_TemplatedDialogueChain(t *testing.T) {
	text := `# 第一章

“许闻溪。”傅行简叫她。

她把笔停住。“我在看字段来源。”

“先补现场口径。”他把模板推过来一寸，“今天只确认演示效果，不扩范围。”

梁渡抬眼。“管理建议生成了吗？”

会议室另一头的投影闪了一下。

“梁渡。”许闻溪叫他。

他把记号笔停住。“我在核演示样本。”

“先补审计口径。”她把模板推过去，“今天只确认导出效果，不扩流程。”

傅行简抬眼。“权限说明生成了吗？”`

	vs := Lint(text)
	v := findRule(vs, "templated_dialogue_chain")
	if v == nil {
		t.Fatalf("expected templated_dialogue_chain violation, got %+v", vs)
	}
	if v.Actual != 2 || v.Limit != 0 {
		t.Fatalf("actual/limit=%v/%v, want 2/0: %+v", v.Actual, v.Limit, v)
	}
}

func TestLint_TemplatedDialogueChainFlagsSingleProceduralChain(t *testing.T) {
	text := `# 第一章

“许闻溪。”傅行简叫她。

她把笔停住。“我在看字段来源。”

“先补现场口径。”他把模板推过来一寸，“今天只确认演示效果，不扩范围。”

梁渡抬眼。“管理建议生成了吗？”

许闻溪没答，先把错列名圈出来。梁渡凑近看了一会儿，把自己的问题划掉，改成了另一句：“那我问现场负责人，不问你。”`

	if v := findRule(Lint(text), "templated_dialogue_chain"); v == nil || v.Actual != 1 {
		t.Fatalf("single procedural chain should violate once: %+v", v)
	}
}

func TestLint_TemplatedDialogueChainAllowsMessyProceduralExchange(t *testing.T) {
	text := `# 第一章

傅行简把模板推过来，没叫她名字，只点了点记录页。“先补现场口径。”

许闻溪没有停笔。她把字段来源那一栏划掉，改写成“待核”，又问投屏同事：“日志入口是谁开的？”

梁渡看着封条，没有接傅行简的话。“我只记现场未见全量导出。”`

	if v := findRule(Lint(text), "templated_dialogue_chain"); v != nil {
		t.Fatalf("messy procedural exchange should not violate: %+v", v)
	}
}

func TestLint_PunctuationCadenceFlagsFormalSemicolonChains(t *testing.T) {
	text := `# 第一章

纸面写着：夜租欠费单；住户：阴阳公寓3栋1704；承租人：江烬；应缴：三百冥钞；缴费截止：00:17。

周行舟说：“你要活到明早，按进价给我算；不讲兄弟价。”

他把账单翻过来；背面还是湿的；门牌也亮着；手机没有信号；厨房滴了一声；走廊里没人。`

	vs := Lint(text)
	for _, rule := range []string{
		"form_notice_semicolon_chain",
		"dialogue_semicolon_formality",
		"stiff_trade_dialogue",
		"semicolon_overuse",
	} {
		if v := findRule(vs, rule); v == nil {
			t.Fatalf("expected %s violation, got %+v", rule, vs)
		}
	}
}

func TestLintSystemMessageOverpacked(t *testing.T) {
	text := `【用途不符。旧债不算新增消费。先别急，钱没跑。换个能留下东西的花法。】`
	if v := findRule(Lint(text), "system_message_overpacked"); v == nil {
		t.Fatalf("expected system_message_overpacked, got %+v", Lint(text))
	}
	clean := "【这笔不算。旧账是昨天欠的。】\n\n【先别乱刷，我帮你挑第一笔。】"
	if v := findRule(Lint(clean), "system_message_overpacked"); v != nil {
		t.Fatalf("short layered system messages should pass: %+v", v)
	}
}

func TestLint_AbstractSystemReassurance(t *testing.T) {
	bad := "【钱没跑。】\n\n【我陪你换条路。】"
	if v := findRule(Lint(bad), "abstract_system_reassurance"); v == nil {
		t.Fatalf("expected abstract_system_reassurance, got %+v", Lint(bad))
	}
	clean := "【这笔不算。旧账是昨天欠的。】\n\n【夜市缺灯，先去问摊主愿不愿意。】"
	if v := findRule(Lint(clean), "abstract_system_reassurance"); v != nil {
		t.Fatalf("concrete system reply should pass: %+v", v)
	}
}

func TestLint_OpaqueProcedureJargon(t *testing.T) {
	bad := `沈知遥说：“补上再测。”

“九点带上采购凭证、用途说明和测试记录。”`
	if v := findRule(Lint(bad), "opaque_procedure_jargon"); v == nil {
		t.Fatalf("expected opaque_procedure_jargon, got %+v", Lint(bad))
	}
	clean := `沈知遥指了指松开的卡扣：“这里没固定好，今晚一扯就可能断电。先补两个扣，明早我再来看。”`
	if v := findRule(Lint(clean), "opaque_procedure_jargon"); v != nil {
		t.Fatalf("plain-language consequence should pass: %+v", v)
	}
}

func TestLint_UITrialChecklist(t *testing.T) {
	bad := `林澈试着还信用卡，确认键刚亮，页面就退了回去。他又点了提现，按钮按下去没反应，最后把用途备注改成“今日周转”，看了两秒又删掉。`
	if v := findRule(Lint(bad), "ui_trial_checklist"); v == nil {
		t.Fatalf("expected ui_trial_checklist, got %+v", Lint(bad))
	}
	clean := `他先问信用卡能不能还，屏幕回得很快：【旧账不行。那是你绑定前花的钱。】“提现呢？”【也不行。你能拿它买东西、请人干活，不能取成现金。】`
	if v := findRule(Lint(clean), "ui_trial_checklist"); v != nil {
		t.Fatalf("plain result-level explanation should pass: %+v", v)
	}
}

func TestLint_DialogueActionLeadRepetition(t *testing.T) {
	bad := `二姨夫夹着鱼：“回来也别闲着。”

二姨把转盘推过去：“人家小赵跟你同岁。”

父亲刚说了句“先吃饭”，桌边没人听。

朋友看见他停筷，抬起眼：“你们替他上班了？”`
	if v := findRule(Lint(bad), "dialogue_action_lead_repetition"); v == nil {
		t.Fatalf("expected dialogue_action_lead_repetition, got %+v", Lint(bad))
	}
	clean := `“回来也别闲着。”二姨夫说。

桌上静了一瞬。二姨还想接话，赵航先笑了：“人家替他上班了？”

“先吃饭。”

没人听林建国的。`
	if v := findRule(Lint(clean), "dialogue_action_lead_repetition"); v != nil {
		t.Fatalf("mixed dialogue topology should pass: %+v", v)
	}
}

func TestLint_TrendLanguageSoundEffectMisuse(t *testing.T) {
	bad := `赵航怪叫一声：“呱，照这算法，门卫也算世界五百强元老。”`
	if v := findRule(Lint(bad), "trend_language_sound_effect_misuse"); v == nil {
		t.Fatalf("expected trend_language_sound_effect_misuse, got %+v", Lint(bad))
	}
	clean := `赵航先笑了：“呱，照这算法，门卫也算世界五百强元老。”`
	if v := findRule(Lint(clean), "trend_language_sound_effect_misuse"); v != nil {
		t.Fatalf("direct spoken trend phrase should pass: %+v", v)
	}
}

func TestLint_SystemProcedureNarration(t *testing.T) {
	bad := `系统判定：本地新增交付，可进入核验。阶段核验通过。夜市小额改善额度解锁：50000元。`
	if v := findRule(Lint(bad), "system_procedure_narration"); v == nil {
		t.Fatalf("expected system_procedure_narration, got %+v", Lint(bad))
	}
	clean := `屏幕上只多了一句：这单算。第一碗卖出去了，五万元给你。`
	if v := findRule(Lint(clean), "system_procedure_narration"); v != nil {
		t.Fatalf("plain companion reply should pass: %+v", v)
	}
}

func TestLint_PunctuationCadenceAllowsRhymeSemicolons(t *testing.T) {
	text := `# 第一章

孩子像背错了儿歌：“门开门开，名字来；名字来了，账也来。妈妈不开，小宝不开；1701开过，1702不开，1703半开，1704快开。门认名，名认账；账认门，门认人。不开，不报……不开——妈妈，后面是什么？不认不替，1234，名字卖了回不来。这屋不开，那屋不开，叔叔不开，1704不开。”

玻璃杯碎了。许曼哭腔很低：“别念了。”`

	for _, rule := range []string{"dialogue_semicolon_formality", "semicolon_overuse", "form_notice_semicolon_chain"} {
		if v := findRule(Lint(text), rule); v != nil {
			t.Fatalf("unexpected %s violation for protected rhyme: %+v", rule, v)
		}
	}
}

func TestLint_HumanFeelStructureFlagsAIArtifacts(t *testing.T) {
	text := `# 第一章

他又往下补了三行：

代缴要双方确认
不回身份证号和名字
不碰来历不明的零钱

卡面下方排着几行小字：

冥府黑卡
仅限诡异世界有效交易
交易须有可确认内容
当前额度未公开
账单日待生成

孩子像背错了儿歌：“不开，不报；不开，不认；不开，不替。”

玄关照得发潮。两行字淡得发虚。地址黑得发沉。金戒指发乌。便签本发黄。周行舟声音发紧。指节白得发硬。

周行舟骂得很轻：“行，活人让你当风控，鬼来了你还当风控。”
“行，你还是那套破风控。”周行舟骂了一声。

墙上只剩断影，肩膀以下歪着，腰以上空了。

他从猫眼里看见白纸背面翻起一角，像故意让江烬看见：代缴需双方确认。`

	vs := Lint(text)
	for _, rule := range []string{
		"structured_note_triplet",
		"card_tos_block",
		"empty_parallel_chant",
		"de_fa_adjective_repetition",
		"duplicate_dialogue_point",
		"impossible_body_geometry",
		"impossible_line_of_sight",
	} {
		if v := findRule(vs, rule); v == nil {
			t.Fatalf("expected %s violation, got %+v", rule, vs)
		}
	}
}

func TestLint_HumanFeelStructureAllowsMessyNotesAndBrokenCardText(t *testing.T) {
	text := `# 第一章

便签本纸边有一圈油黄。他先写“普通钱无效”，笔尖停了一下，又写“代缴要双方——”。第二个“方”写歪了，他把“双方”圈住，在旁边挤了两个小字：确认。下一行只写了“不回身份证号”，写完又把“名字”塞到行尾。

手机中央多了一张黑色卡面。第一行还能看清“冥府黑卡”。第二行只剩“仅限”和“有效交易”，中间像被水泡开。再往下是“须有”，旁边三个字从水渍里浮出来：可确认。后半截糊在黑底里。最下面一行空得厉害，只剩“账单”两个字。`

	for _, rule := range []string{"structured_note_triplet", "card_tos_block", "empty_parallel_chant"} {
		if v := findRule(Lint(text), rule); v != nil {
			t.Fatalf("unexpected %s violation: %+v", rule, v)
		}
	}
}

func TestLint_BureaucraticRegisterOveruse(t *testing.T) {
	text := `# 第一章

事由栏很窄，光标在第一格里等着。她敲下：演示记录尾号六二九四，封存回执尾号六三九四，申请核验原始读取链与封存生成记录。

记录员挠了一下眉心。“纪要怎么进？我写编号不一致，原因待核？”

“写到这里为止。”许闻溪把便签折了一道，盖住邱梅那行药量，只露出新补的数字，“别替它找原因。”

笔尖终于落下。记录员刚写完“不一致”，内线窗口弹出乔安的消息：你还在数据室吗？

许闻溪回：只问版本，不写人。`

	v := findRule(Lint(text), "bureaucratic_register_overuse")
	if v == nil {
		t.Fatalf("expected bureaucratic_register_overuse violation")
	}
	if !strings.Contains(v.Target, "申请核验原始读取链") {
		t.Fatalf("target should cite formal register evidence, got %q", v.Target)
	}
}

func TestLint_BureaucraticRegisterAllowsColloquialPressure(t *testing.T) {
	text := `# 第一章

事由栏窄得离谱，光标卡在第一格，闪一下，停一下，像在催她快点编个说法。

许闻溪把手腕往外挪了挪，重新敲字：演示记录尾号6294，封存回执尾号6394。申请核原始读取链，核封存记录是谁生成的。

记录员盯着屏幕看了两秒，挠了下眉心。“那纪要呢？就写编号对不上，原因待核？”

“别写原因。”许闻溪把便签折了一道，盖住邱梅那行药量，只露出刚补上的数字，“先写到对不上。”

“可主任要问起来……”

“让他问。”

乔安的消息隔了半天才跳出来：别把我写进去，求你。`

	if v := findRule(Lint(text), "bureaucratic_register_overuse"); v != nil {
		t.Fatalf("colloquial pressure should not violate: %+v", v)
	}
}

func TestLint_CausalIntegrityFlagsOrderAndVerificationIssues(t *testing.T) {
	text := `# 第一章

信号栏空着。五楼老钱发语音：“是不是报后四位就能登记？我刚发了。”
老钱那边只剩电流声。二十二楼有人问：“那我报我前夫的？”保安小魏说：“值班室外面有人买票。”郝律师反问：“你见过物业把人昵称改成承租物？”
下一秒，老钱的头像灰下去，群昵称从“钱建国”变成了“5栋临时承租物”。阴阳公寓3栋楼道安静下来。

信号栏还是空的。这通电话显然不是从基站过来的。第二声铃响到一半，他按下接听。“你那边是不是也起雾？”

薄页自己摊开，下面四栏隔得不齐，像临时盖上去的章。
手机多出冥府黑卡。卡面写着须有两个字，后面全糊了。`

	vs := Lint(text)
	for _, rule := range []string{
		"causal_evidence_order",
		"identity_effect_delayed",
		"building_floor_mismatch",
		"anomalous_phone_unverified",
		"form_image_mismatch",
		"card_core_rule_overblurred",
	} {
		if v := findRule(vs, rule); v == nil {
			t.Fatalf("expected %s violation, got %+v", rule, vs)
		}
	}
}

func TestLint_CausalIntegrityAllowsVerifiedSequence(t *testing.T) {
	text := `# 第一章

一个蓝天白云头像连发三遍：收到请回复身份证后四位。五楼老钱发语音：“是不是报后四位就能登记？我刚发了。”郝律师立刻打字：撤回！别确认身份！
老钱那边只剩电流声。下一秒，他的头像灰下去，群昵称从“钱建国”变成了“3栋5楼临时承租物”。
有人骂郝律师装懂，郝律师反问：“往上翻，看老钱。你见过物业把人昵称改成承租物？”

这通电话显然不是从基站过来的。江烬问：“上回我在你店里买电池，你多找了还是少找了？”周行舟骂：“少找两块。你那边是不是也起雾？”
薄页自己摊开，下面四栏隔得不齐，像临时拼出来的。
手机多出冥府黑卡。卡面写着须有，旁边三个字从水渍里浮出来：可确认。`

	for _, rule := range []string{
		"causal_evidence_order",
		"identity_effect_delayed",
		"building_floor_mismatch",
		"anomalous_phone_unverified",
		"form_image_mismatch",
		"card_core_rule_overblurred",
	} {
		if v := findRule(Lint(text), rule); v != nil {
			t.Fatalf("unexpected %s violation: %+v", rule, v)
		}
	}
}

func findRule(vs []Violation, rule string) *Violation {
	for i := range vs {
		if vs[i].Rule == rule {
			return &vs[i]
		}
	}
	return nil
}
