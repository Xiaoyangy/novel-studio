package domain

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseBookScaleRangeChineseAndEnglish(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  BookScaleRange
	}{
		{
			value: "\u9884\u8ba1100-130\u4e07\u5b57\uff0c\u7ea68-10\u5377\uff0c360-480\u7ae0\uff1b\u4e3b\u7ebf\u65f6\u95f4\u8de8\u5ea64\u5e74",
			want:  BookScaleRange{MinVolumes: 8, MaxVolumes: 10, MinChapters: 360, MaxChapters: 480},
		},
		{
			value: "about 6\u20138 volumes and 180\u2013240 chapters",
			want:  BookScaleRange{MinVolumes: 6, MaxVolumes: 8, MinChapters: 180, MaxChapters: 240},
		},
	} {
		got, err := ParseBookScaleRange(tc.value)
		if err != nil {
			t.Fatalf("ParseBookScaleRange(%q): %v", tc.value, err)
		}
		if got != tc.want {
			t.Fatalf("ParseBookScaleRange(%q) = %+v, want %+v", tc.value, got, tc.want)
		}
	}
}

func TestResolveBookScaleTargetUsesFrozenMidpointAndWordBudget(t *testing.T) {
	target, err := ResolveBookScaleTarget(
		"\u9884\u8ba1100-130\u4e07\u5b57\uff0c\u7ea68-10\u5377\uff0c360-480\u7ae0\uff1b\u4e3b\u7ebf\u65f6\u95f4\u8de8\u5ea6\u7ea63.5-4\u5e74",
		2,
		128,
	)
	if err != nil {
		t.Fatal(err)
	}
	if target.TargetVolumes != 9 || target.TargetChapters != 420 ||
		target.TargetWords != 1150000 || target.TargetWordsPerChapter != 2738 ||
		target.StoryTimeHint != "3.5-4\u5e74" {
		t.Fatalf("target=%+v", target)
	}
}

func TestResolveBookScaleTargetParsesChineseWanOnBothRangeBounds(t *testing.T) {
	for _, scale := range []string{
		"1-1卷，12-12章；正文2.8万—3万字",
		"1-1卷，12-12章；正文2.8—3万字",
	} {
		target, err := ResolveBookScaleTarget(scale, 1, 12)
		if err != nil {
			t.Fatalf("ResolveBookScaleTarget(%q): %v", scale, err)
		}
		if target.MinWords != 28000 || target.MaxWords != 30000 ||
			target.TargetWords != 29000 || target.TargetWordsPerChapter != 2417 {
			t.Fatalf("ResolveBookScaleTarget(%q) = %+v", scale, target)
		}
	}
}

func TestOutlineChapterContractRejectsThinAndJSONShell(t *testing.T) {
	volumes := []VolumeOutline{{Index: 1, Title: "\u5377", Theme: "\u4e3b\u9898", Arcs: []ArcOutline{{
		Index: 1, Title: "\u5f27", Goal: "\u76ee\u6807", Chapters: []OutlineEntry{
			{Title: "\u91cd\u590d", CoreEvent: "\u7ee7\u7eed\u63a8\u8fdb", Hook: "\u5236\u9020\u60ac\u5ff5", Scenes: []string{`["\u573a\u666f\u4e00","\u573a\u666f\u4e8c"]`}},
			{Title: "\u91cd\u590d", CoreEvent: "\u7ee7\u7eed\u63a8\u8fdb", Hook: "\u5236\u9020\u60ac\u5ff5", Scenes: nil},
		},
	}}}}
	issues := OutlineChapterContractIssues(volumes)
	joined := make([]string, 0, len(issues))
	for _, issue := range issues {
		joined = append(joined, issue.Code)
	}
	all := strings.Join(joined, ",")
	for _, code := range []string{"core_event_not_concrete", "hook_not_actionable", "scene_json_shell", "scenes_too_thin", "duplicate_title", "duplicate_core_event", "duplicate_hook"} {
		if !strings.Contains(all, code) {
			t.Fatalf("issues=%s, want %s", all, code)
		}
	}
}

func TestOutlineChapterContractAcceptsConcreteDistinctChapters(t *testing.T) {
	chapters := []OutlineEntry{
		{
			Title:     "\u96e8\u591c\u7684\u9519\u8d26\u5355",
			CoreEvent: "\u6797\u7b19\u5728\u4f9b\u8d27\u5546\u4e34\u65f6\u6da8\u4ef7\u540e\u91cd\u6392\u644a\u4f4d\uff0c\u62ff\u51fa\u7b2c\u4e09\u5957\u6838\u9500\u89c4\u5219\u4fdd\u4f4f\u4e86\u5f00\u5e02\u65f6\u95f4",
			Hook:      "\u65b0\u89c4\u5219\u521a\u5f20\u8d34\uff0c\u5e02\u76d1\u6240\u5374\u9001\u6765\u4e00\u5f20\u76f8\u53cd\u7684\u6574\u6539\u5355",
			Scenes:    []string{"\u5e93\u623f\u91cc\u4e09\u65b9\u5bf9\u7740\u9519\u8d26\u5355\u8ffd\u67e5\u8d27\u6b3e\u53bb\u5411", "\u96e8\u68da\u4e0b\u6797\u7b19\u73b0\u573a\u6362\u8868\u5e76\u8bf4\u670d\u644a\u4e3b", "\u5f00\u5e02\u540e\u6c88\u77e5\u9065\u6838\u5bf9\u6d41\u6c34\u53d1\u73b0\u65b0\u77db\u76fe"},
		},
		{
			Title:     "\u8c01\u52a8\u4e86\u5907\u7528\u7535\u6e90",
			CoreEvent: "\u6c88\u77e5\u9065\u8ffd\u5230\u914d\u7535\u623f\u627e\u51fa\u65ad\u7535\u4eba\uff0c\u4f46\u4e3a\u4e86\u4fdd\u4f4f\u4e3e\u62a5\u8005\u53ea\u516c\u5e03\u4e86\u53ef\u590d\u6838\u7684\u65f6\u95f4\u7ebf",
			Hook:      "\u65ad\u7535\u4eba\u7559\u4e0b\u7684\u95e8\u7981\u5361\u5c5e\u4e8e\u4e00\u4f4d\u4ece\u672a\u6765\u8fc7\u591c\u5e02\u7684\u7406\u4e8b",
			Scenes:    []string{"\u914d\u7535\u623f\u5185\u62c6\u5f00\u5c01\u6761\u590d\u539f\u65ad\u7535\u987a\u5e8f", "\u76d1\u63a7\u5ba4\u91cc\u9010\u5e27\u6838\u5bf9\u95e8\u7981\u548c\u811a\u5370", "\u529e\u516c\u5ba4\u4e2d\u7528\u533f\u540d\u5907\u5fd8\u5f55\u4fdd\u62a4\u4e3e\u62a5\u8005"},
		},
	}
	volumes := []VolumeOutline{{Index: 1, Arcs: []ArcOutline{{Index: 1, Chapters: chapters}}}}
	if issues := OutlineChapterContractIssues(volumes); len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
}

func TestOutlineChapterContractRepeatedCoreEventFailureReportsExecutableParserFeedback(t *testing.T) {
	base := OutlineEntry{
		Title: "仓门前的涨价单",
		Hook:  "仓门刚开，第二家供货商又带着停供通知堵住入口",
		Scenes: []string{
			"林澈在仓库门口逐张核对临时涨价单和原始报价",
			"供货商当场拒绝按旧价卸货并要求先改付款记录",
			"沈知遥调出备用名单让双方重新确认开市时限",
		},
	}
	tests := []struct {
		name        string
		coreEvent   string
		placeholder string
	}{
		{
			name:        "reported placeholder even when text is long",
			coreEvent:   "林澈在仓库门口召集供货商继续推进冷链整改并登记新的交货结果",
			placeholder: "继续推进",
		},
		{
			name:      "reported effective rune count when text is short",
			coreEvent: "林澈改了仓位表",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chapter := base
			chapter.CoreEvent = tc.coreEvent
			volumes := []VolumeOutline{{Index: 3, Arcs: []ArcOutline{{Index: 3, Chapters: []OutlineEntry{chapter}}}}}

			// A tool retry validates a fresh copy of the same proposed arc. Repeating
			// the failure must preserve exact, field-level repair instructions rather
			// than collapsing back to the old generic semantic label.
			var messages []string
			for attempt := 0; attempt < 2; attempt++ {
				issues := OutlineChapterContractIssues(volumes)
				var coreIssue *OutlineContractIssue
				for i := range issues {
					if issues[i].Code == "core_event_not_concrete" {
						coreIssue = &issues[i]
						break
					}
				}
				if coreIssue == nil {
					t.Fatalf("attempt %d unexpectedly passed core_event gate: %+v", attempt+1, issues)
				}
				messages = append(messages, coreIssue.Message)
			}

			if messages[0] != messages[1] {
				t.Fatalf("repeated parser feedback drifted:\nfirst: %s\nsecond: %s", messages[0], messages[1])
			}
			wantSignals := []string{
				"field=core_event",
				fmt.Sprintf("meaningful_runes:%d", meaningfulRuneCount(tc.coreEvent)),
				fmt.Sprintf("minimum:%d", outlineCoreEventMinMeaningfulRunes),
				"replace only this chapter's core_event",
				"actor + specific obstacle + chosen visible action + observable state change",
				"do not submit the bracket labels",
				"passing example:",
			}
			if tc.placeholder == "" {
				wantSignals = append(wantSignals, "placeholder_fragment:none")
			} else {
				wantSignals = append(wantSignals, fmt.Sprintf("placeholder_fragment:%q", tc.placeholder))
			}
			for _, signal := range wantSignals {
				if !strings.Contains(messages[1], signal) {
					t.Fatalf("actionable feedback missing %q: %s", signal, messages[1])
				}
			}
		})
	}
}

func TestOutlinePlaceholderTokenDistinguishesBusinessOccupationFromMetaShell(t *testing.T) {
	// This reproduces the production shape that triggered four retries: “占位”
	// describes split orders occupying warehouse capacity inside an otherwise
	// concrete core event. Pad to exactly 248 meaningful runes to keep the
	// regression tied to the long-field case rather than merely crossing 18.
	longConcreteCore := "许牧发现四家申报商户共用同一联系人和车辆，贺骁确认它们都由赵启明统一调车，但赵启明坚持营业执照不同就应分别占位。沈知遥让宋砚核对合同与收款关系，林澈当众新增关联申报和合并上限，四单合并重排，一家真正独立经营的商户不受牵连，旧利益方借拆单占位抢仓的路径被堵住。"
	padding := []rune(strings.Repeat("现场订单车辆收款仓位变化均留下可复核记录", 20))
	need := 248 - meaningfulRuneCount(longConcreteCore)
	if need <= 0 || need > len(padding) {
		t.Fatalf("invalid long core fixture: count=%d padding=%d", meaningfulRuneCount(longConcreteCore), len(padding))
	}
	longConcreteCore += string(padding[:need])
	if got := meaningfulRuneCount(longConcreteCore); got != 248 {
		t.Fatalf("long core fixture meaningful runes=%d, want 248", got)
	}
	if fragment := outlinePlaceholderFragment(longConcreteCore); fragment != "" {
		t.Fatalf("concrete split-order occupation was mistaken for placeholder %q", fragment)
	}

	chapter := OutlineEntry{
		Title:     "四张订单原来是一家人",
		CoreEvent: longConcreteCore,
		Hook:      "失去提前仓位后，赵启明转而拿七天低价包圆争夺货源",
		Scenes: []string{
			"四张订单平码在桌上，许牧圈出相同联系电话和车牌",
			"宋砚核对合同后确认四单最终回款进入同一账户",
			"四块时段牌合并重排，后排两家农户自动前移",
		},
	}
	volumes := []VolumeOutline{{Index: 3, Arcs: []ArcOutline{{Index: 3, Chapters: []OutlineEntry{chapter}}}}}
	for _, issue := range OutlineChapterContractIssues(volumes) {
		if issue.Code == "core_event_not_concrete" {
			t.Fatalf("248-rune concrete core event was rejected: %+v", issue)
		}
	}

	for _, shell := range []string{"占位", "占位内容", "此处占位"} {
		if fragment := outlinePlaceholderFragment(shell); fragment != "占位" {
			t.Fatalf("meta shell %q matched %q, want 占位", shell, fragment)
		}
		feedback := outlineCoreEventRepairFeedback(shell)
		if !strings.Contains(feedback, `placeholder_fragment:"占位"`) {
			t.Fatalf("meta shell %q lost exact placeholder feedback: %s", shell, feedback)
		}
	}
	if fragment := outlinePlaceholderFragment("林澈让团队继续推进"); fragment != "继续推进" {
		t.Fatalf("unambiguous meta fragment matched %q, want 继续推进", fragment)
	}
}

func TestMissingCompassCoverageRequiresTerminalContractAndThreads(t *testing.T) {
	compass := StoryCompass{
		EndingDirection: "\u4e24\u4eba\u5b8c\u6210\u5c0f\u57ce\u6837\u677f\u5e76\u56de\u5f52\u5bb6\u5ead",
		OpenThreads:     []string{"\u65e7\u5382\u5b8c\u6210\u81ea\u8f6c"},
		NonNegotiables:  []string{"\u4e0d\u5f97\u727a\u7272\u666e\u901a\u5546\u6237"},
	}
	registry := BuildStoryContractRegistry(compass)
	refs := make([]StoryContractRef, len(registry))
	copy(refs, registry)
	for i := range refs {
		refs[i].PlannedPayoffChapter = 1
		refs[i].PlannedResolution = []string{
			"林笙组织全体商户表决通过小城样板并与家人共同留下运营",
			"沈知遥公开补偿账本并确保普通商户获得足额返还与长期席位",
			"旧厂工人接管订单系统并完成不依赖外部输血的首月自转",
		}[i]
	}
	payoffEvidence := refs[0].PlannedResolution + "；" + refs[1].PlannedResolution + "；" + refs[2].PlannedResolution
	volumes := []VolumeOutline{{Index: 1, Theme: "\u6536\u675f", Arcs: []ArcOutline{{
		Index: 1, Goal: "\u8ba9\u6240\u6709\u957f\u7ebf\u5728\u4eba\u7269\u9009\u62e9\u4e2d\u5f97\u5230\u56de\u5e94", ContractRefs: refs,
		Chapters: []OutlineEntry{{CoreEvent: payoffEvidence, ContractRefs: refs}},
	}}}}
	if missing := MissingCompassCoverage(volumes, compass); len(missing) != 0 {
		t.Fatalf("missing=%v", missing)
	}
	volumes[0].Arcs[0].Chapters[0].ContractRefs[0].SourceDigest = "sha256:wrong"
	if missing := MissingCompassCoverage(volumes, compass); len(missing) == 0 {
		t.Fatal("source digest drift unexpectedly passed")
	}
}

func TestMissingCompassCoverageRejectsBareResolutionReceiptWithoutChapterEvidence(t *testing.T) {
	compass := StoryCompass{EndingDirection: "主角完成旧城自治并让居民掌握最终决策权"}
	ref := BuildStoryContractRegistry(compass)[0]
	ref.PlannedPayoffChapter = 1
	ref.PlannedResolution = "林笙召集居民投票通过自治章程并把最终决策权交给居民议会"
	volumes := []VolumeOutline{{Index: 1, Arcs: []ArcOutline{{
		Index: 1, ContractRefs: []StoryContractRef{ref},
		Chapters: []OutlineEntry{{
			CoreEvent:    "林笙在广场宣布事情终于解决，众人鼓掌庆祝",
			Scenes:       []string{"居民到场见证会议结束", "旧城重新开门", "主角离开会场"},
			ContractRefs: []StoryContractRef{ref},
		}},
	}}}}
	missing := strings.Join(MissingCompassCoverage(volumes, compass), ",")
	if !strings.Contains(missing, "planned_resolution_not_realized_in_core_event_or_scenes") {
		t.Fatalf("missing=%s", missing)
	}
	volumes[0].Arcs[0].Chapters[0].Scenes = append(
		volumes[0].Arcs[0].Chapters[0].Scenes,
		ref.PlannedResolution,
	)
	if missing := MissingCompassCoverage(volumes, compass); len(missing) != 0 {
		t.Fatalf("resolution evidence should satisfy binding: %v", missing)
	}
}

func TestOutlineContractResolutionRealizedAcceptsParaphraseAndRejectsNonRealization(t *testing.T) {
	ref := StoryContractRef{
		PlannedResolution: "沈知遥凭连续异常完成推理，林澈主动坦白系统全貌，两人确认关系并把她确立为唯一知情者。",
	}
	paraphrase := OutlineEntry{
		CoreEvent: "林澈不再拿资金方挡在前面，主动交代系统的绑定经过、额度边界和失败惩罚；沈知遥用此前记录的连续异常逐项验证，补上推理最后一环。两人把感情选择与任务分开说清，正式确认关系，并约定她是除林澈外唯一知道系统真相的人。",
		Scenes: []string{
			"卷帘门落下后，沈知遥把异常时间线排在旧工作台上。",
			"林澈从第一次听见系统提示讲到额度被冻结时真正看见了什么。",
			"她握住他的手，明确项目共同承担，秘密只留在两人之间。",
		},
	}
	if !OutlineContractResolutionRealized(paraphrase, ref) {
		t.Fatal("concrete paraphrase was rejected")
	}
	for name, tc := range map[string]struct {
		resolution string
		chapter    OutlineEntry
	}{
		"negative obstacle then completed payoff": {
			resolution: "葛远山带领老厂以稳定品控、冷链履约和外地复购完成联合订单季，旧库存项目由此转成县域品牌样品链的长期节点。",
			chapter: OutlineEntry{
				CoreEvent: "第三批遇到设备卡停和冷链截单双重阻力，葛远山没有混过复核或借外县代工补数，而是带老厂工人改用人工逐箱确认，宁可压缩数量也守住与前两批一致的品控。冷链仓按真实排期接续发车，三批货的封签、到货状态和外地复购均通过核验，旧库存时期留下的最后一张批次卡也被转入长期样品记录；老厂由一次清库存项目正式成为县域品牌样品链的稳定节点。",
				Scenes: []string{
					"包装设备停下后，葛远山先削掉赶不及的数量，再带工人把剩余货一箱箱人工复核。",
					"冷链车辆按修订后的时段接货，三批温度封签和到货反馈都能对应到同一套生产记录。",
					"外地老客收到第三批后再次下单，新增订单来自真实购买者。",
					"老厂从临时救火点变成以后每轮联合订单都能复用的产品节点。",
				},
			},
		},
		"rejected proposal then completed payoff": {
			resolution: "赵启明带领旧商户按公开准入、轮换和损耗规则排队并自行承担结果，旧商会依靠摊位与供应控制资源的路径被彻底拆除。",
			chapter: OutlineEntry{
				CoreEvent: "旧商户内部有人提议扣住供应逼出仓位，赵启明却看清旧办法只会让成员失去公开入口，主动撤回统一占位要求，带领各商户逐户按准入、轮换和损耗机制排队。他接受靠后的仓位，并让破损与延误按各自承诺自行结算，不再要求林澈或商会兜底；高峰货物照常发出，旧商会依靠摊位和供应控制资源的路径由此被彻底拆除。",
				Scenes: []string{
					"一名旧商户提议暂缓供货逼出位置，赵启明第一次当众否决了这套自己过去最熟悉的办法。",
					"他撤回四个统一占位申请，让每家商户分别登记货量、时效和能承担的损耗。",
					"赵启明照样领号，并在一箱破损货前签下本方结算责任。",
					"车辆离仓后，旧商会成员开始直接通过公开入口接单。",
				},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !OutlineContractResolutionRealized(tc.chapter, StoryContractRef{PlannedResolution: tc.resolution}) {
				t.Fatal("completed payoff with an earlier obstacle was rejected")
			}
		})
	}
	for name, evidence := range map[string]string{
		"generic": "林澈宣布事情终于解决，众人鼓掌后离开会场。",
		"negated": "沈知遥并未凭连续异常完成推理，林澈也没有主动坦白系统全貌；两人没有确认关系，更未把她确立为唯一知情者。",
		"quoted":  "方案写着：\"" + ref.PlannedResolution + "\"，但二人当场否决并销毁这份假设。",
		"future":  "林澈计划以后坦白系统全貌；如果沈知遥接受，两人以后再确认关系，并考虑把她确立为唯一知情者。",
	} {
		t.Run(name, func(t *testing.T) {
			if OutlineContractResolutionRealized(OutlineEntry{CoreEvent: evidence}, ref) {
				t.Fatalf("non-realization evidence passed: %s", evidence)
			}
		})
	}
	negativeRef := StoryContractRef{PlannedResolution: "系统完成终局核验后停止用额度催促救火并转为守护共同规则"}
	if OutlineContractResolutionRealized(OutlineEntry{
		CoreEvent: "系统并未做到" + negativeRef.PlannedResolution + "，这条方案最终落空。",
	}, negativeRef) {
		t.Fatal("double-negated negative-intent resolution passed")
	}
}

func TestOutlineContractResolutionRealizedRejectsFramedCopiesAcrossContractShapes(t *testing.T) {
	resolutions := []string{
		"沈知遥凭连续异常完成推理，林澈主动坦白系统全貌，两人确认关系并把她确立为唯一知情者。",
		"林澈与沈知遥用联合订单串联老街、美食街、手艺村、体育场、冷链仓与创业园，使各处项目按阶段形成互相供血的运营链。",
		"葛远山带领老厂以稳定品控、冷链履约和外地复购完成联合订单季，旧库存项目由此转成县域品牌样品链的长期节点。",
		"叶南栀与陈鹿拒绝摆拍路线，把普通经营周期中的错标、退出和自主补救完整呈现，最终建立可核验且有人情味的内容边界。",
		"林澈与沈知遥在婚礼如期完成、项目节点能够自主运转后确认怀孕，使稳定家庭关系与事业扩张后的孕育线按既定时序落定。",
		"赵启明带领旧商户按公开准入、轮换和损耗规则排队并自行承担结果，旧商会依靠摊位与供应控制资源的路径被彻底拆除。",
		"许牧用多入口订单工具解释精准调度并接下县域数字底座，始终接受公开资金渠道解释而未接触系统真相。",
		"陈鹿与宋砚在联合订单复盘中确认伴侣关系和长期协作边界，继续分别守住真实影像与稳健风控且不抢主线位置。",
		"青山县团队用追溯凭据、真实回头客和自主交付击穿唐越的速成叙事，并迫使秦晚晴按非独家和地方收益优先边界合作。",
		"系统以旺季自转结果完成最终核验，停止用额度驱动林澈亲自救火，转而确认并守护参与者共同执行的县城规则。",
		"青山县各节点在林澈未返场救火时完成交付，系统确认自转规则成型；林澈陪沈知遥迎来孩子并开启一家三口的新生活。",
		"林澈与沈知遥把最终核验和系统使命转换始终锁在夫妻共同守密范围内，其他角色直到收束都只看见公开经营规则。",
		"临产与旺季故障并发时，林澈选择陪伴沈知遥并共同守住边界，两人在无冷战无分离的稳定婚姻中完成终局选择。",
		"全书时间推进至约第四年且沈知遥未满三十三岁，婚姻已经稳定、事业完成扩张后走完孕期并平安生产。",
		"主角团各自接管长期节点并让项目脱离个人救火持续运转，孩子出生后青山县与家庭进入可见新日常，系统秘密从未公开。",
		"孩子平安出生后，夜市、老厂、冷链仓与创业园仍按公开规则照常运转，林澈和沈知遥带孩子走入县城烟火中的家庭新日常。",
	}
	wrappers := map[string]func(string) string{
		"negated":              func(value string) string { return "并非" + value },
		"quoted_pending":       func(value string) string { return "会议纪要原文如下：" + value + "（待审）" },
		"future":               func(value string) string { return "会议决定下个月将" + value },
		"future_reordered":     func(value string) string { return "会议决定将在下个月" + value },
		"proposal":             func(value string) string { return "有人提议" + value + "，随后被否决。" },
		"proposal_not_adopted": func(value string) string { return "会上提出" + value + "，最终没有采纳。" },
		"not_achieved":         func(value string) string { return "并未做到" + value },
		"never_happened":       func(value string) string { return "这并未曾真正发生：" + value },
		"plan_quote":           func(value string) string { return "方案写着" + value },
		"plan_future":          func(value string) string { return "计划以后" + value },
		"display_discussion": func(value string) string {
			return "公告栏展示一段文字" + value + "，但尚处讨论阶段。"
		},
	}
	for index, resolution := range resolutions {
		ref := StoryContractRef{PlannedResolution: resolution}
		if !OutlineContractResolutionRealized(OutlineEntry{CoreEvent: resolution}, ref) {
			t.Fatalf("exact realized resolution %d was rejected", index)
		}
		for name, wrap := range wrappers {
			t.Run(fmt.Sprintf("%02d_%s", index, name), func(t *testing.T) {
				if OutlineContractResolutionRealized(OutlineEntry{CoreEvent: wrap(resolution)}, ref) {
					t.Fatalf("%s wrapper passed for resolution %d", name, index)
				}
			})
		}
	}
}

func TestMissingCompassCoverageRejectsChapterRefResolutionDrift(t *testing.T) {
	compass := StoryCompass{OpenThreads: []string{"旧厂工人最终独立接管排产"}}
	parent := BuildStoryContractRegistry(compass)[0]
	parent.PlannedPayoffChapter = 1
	parent.PlannedResolution = "林澈召集旧厂工人表决通过轮班规则并由工人接管排产系统"
	child := parent
	child.PlannedResolution = "林澈独自宣布保留旧排产制度并继续掌握全部班次决定权"
	volumes := []VolumeOutline{{Index: 1, Arcs: []ArcOutline{{
		Index:        1,
		ContractRefs: []StoryContractRef{parent},
		Chapters: []OutlineEntry{{
			CoreEvent:    child.PlannedResolution,
			ContractRefs: []StoryContractRef{child},
		}},
	}}}}
	missing := strings.Join(MissingCompassCoverage(volumes, compass), ",")
	if !strings.Contains(missing, "contract_ref_drift") {
		t.Fatalf("chapter planned_resolution drift escaped final validation: %s", missing)
	}
}
