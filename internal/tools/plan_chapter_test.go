package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func planArgs(chapter int) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"chapter":           chapter,
		"title":             "测试章",
		"goal":              "推进剧情",
		"conflict":          "外部阻力",
		"hook":              "留下悬念",
		"emotion_arc":       "紧张到期待",
		"causal_simulation": testCausalSimulation(false),
	})
	return b
}

func testCausalSimulation(rewrite bool) map[string]any {
	contextSources := []string{
		"current_chapter_outline",
		"future_outline_window",
		"world_foundation",
		"character_dossiers",
		"progression_snapshot.next_plan",
		"chapter_contract",
		"characters",
		"world_rules/book_world",
		"character_continuity",
		"character_stage_records",
		"side_character_journeys",
		"resource_audit",
		"foreshadow_ledger",
		"relationship_state",
		"user_rules/writing_engine",
		"prewrite_storycraft_plan",
		"world_background_plan",
		"dialogue_writing",
		"web_reference_brief",
	}
	if rewrite {
		contextSources = append(contextSources, "rewrite_brief.review_summary", "review.issues")
	}
	sim := map[string]any{
		"project_promise":  "测试承诺",
		"chapter_function": "测试章节功能",
		"context_sources":  contextSources,
		"writing_norms_applied": []map[string]any{{
			"source":              "user_rules/writing_engine",
			"rule_focus":          []string{"动作承载信息"},
			"chapter_application": "用具体动作推进冲突",
			"proof_targets":       []string{"scene_anchors"},
			"failure_risk":        "解释腔",
		}},
		"anti_ai_execution_plan": map[string]any{
			"risk_signals":           []string{"整齐清单"},
			"counter_moves":          []string{"用误判和动作打断"},
			"sentence_rhythm_policy": "长短句混用",
			"object_response_budget": "物件回应不超过三次",
			"dialogue_function_plan": "对白承担试探和拒绝",
			"review_checks":          []string{"没有金句问号收尾"},
		},
		"external_reference_plan": []map[string]any{{
			"query_or_need":         "写前收集当代平台/小区群/生活噪声，用于判断本章是否需要热梗或只保留普通生活纹理",
			"source_type":           "project_web_reference_brief",
			"source_refs":           []string{"meta/web_reference_brief.md"},
			"retrieved_at":          "2026-07-05T00:00:00+08:00",
			"freshness_requirement": "热梗和平台语境需近90天仍在流通；稳定生活动作可作为常识",
			"usable_details":        []string{"小区群半句反应", "生活动作"},
			"transformation_rule":   "只转成角色半句、群聊噪声或可见动作，不搬运网页摘要",
			"do_not_use":            []string{"过时梗", "真实敏感热点"},
		}},
		"trend_language_plan": []map[string]any{{
			"item":              "none",
			"source_context":    "本章不用热梗",
			"character_carrier": "none",
			"scene_function":    "避免突兀",
			"usage_budget":      "0次",
			"forbidden_usage":   "旁白和章尾不用梗",
		}},
		"grounding_details": []map[string]any{{
			"detail":         "门槛泥水",
			"source_ref":     "stable common sense",
			"transformed_as": "现场痕迹",
			"scene_anchor":   "门槛",
		}},
		"offscreen_character_stage": []map[string]any{{
			"character":            "林砚",
			"time":                 "入门夜",
			"location":             "山门外",
			"status":               "存活",
			"environment":          "钟声已响，登记口即将关闭",
			"current_action":       "核对名册",
			"pressure":             "错过登记",
			"decision":             "先确认规则边界",
			"mistake_or_misbelief": "以为名册不可改",
			"knowledge_boundary":   "不知道执事已调换名册",
			"visible_in_chapter":   true,
			"evidence":             "计划事件",
			"transport":            "原地",
			"travel_time":          "0分钟",
			"meeting_constraint":   "林砚只能在山门口处理登记，不能得知登记弟子的后续支线",
			"personality_delta":    "更警惕没读懂的规矩",
			"death_state":          "存活",
			"protagonist_notice":   "主角已在现场直接获知",
			"timeline_consistency": "与第一章同一夜",
			"next_potential":       "引出分组争议",
		}, {
			"character":            "登记弟子",
			"time":                 "入门夜同一刻",
			"location":             "登记口内侧",
			"status":               "存活",
			"environment":          "钟声已响，上级催促收口",
			"current_action":       "压住名册，决定是否把异常递给执事",
			"pressure":             "放错人或拦错人都会被追责",
			"decision":             "先按旧规矩挡住入口",
			"mistake_or_misbelief": "误以为异常只是临时试探",
			"knowledge_boundary":   "不知道内门执事已调换名册",
			"visible_in_chapter":   false,
			"evidence":             "推演支线",
			"transport":            "原地值守",
			"travel_time":          "0分钟；值守期间不能离岗",
			"meeting_constraint":   "只能在登记口与林砚接触，不能随叫随到",
			"personality_delta":    "从机械执行转为担心背锅",
			"death_state":          "存活",
			"protagonist_notice":   "后续通过名册备注或登记口争执传回主角",
			"timeline_consistency": "与第一章同一夜并行",
			"next_potential":       "引出分组争议",
		}},
		"longform_opening": map[string]any{
			"target_reader":      "长篇读者",
			"opening_hook":       "门外异常",
			"serial_engine":      "规则、地图、人物状态逐章推进",
			"reader_reward_loop": []string{"规则验证", "资源入账"},
			"long_range_promises": []map[string]any{{
				"promise":            "长期谜题",
				"first_chapter_seed": "第一章物件",
				"payoff_horizon":     "第一卷",
			}},
			"reveal_budget":       []string{"不解释终局"},
			"first_chapter_proof": []string{"主角有方法但会错判"},
			"retention_risks":     []string{"主角全知"},
		},
		"character_arc_tests": []map[string]any{{
			"character":         "林砚",
			"want":              "确认登记规则",
			"core_lie":          "以为名册封存不可改",
			"need":              "接受规则有灰区",
			"truth":             "规则会被人利用，需要用证据修正判断",
			"pressure_test":     "登记口关闭测试他是否盲签",
			"first_mistake":     "迟疑导致错过窗口",
			"correction_signal": "名册出现新字",
			"chapter_evidence":  "名册和钟声",
		}},
		"reader_reward_plan": map[string]any{
			"chapter_window":            "1-5",
			"first_chapter_small_win":   "林砚拿到名册异常证据",
			"new_debt_or_cost":          "登记弟子盯上他",
			"payoff_visibility":         "名册新增笔迹可见",
			"traffic_risk":              "只有危机无小胜会压低追读",
			"forbidden_reward_patterns": []string{"免费过关", "系统菜单"},
			"reward_ladder": []map[string]any{{
				"chapter": 1,
				"reward":  "确认名册异常",
				"cost":    "被登记弟子记住",
				"hook":    "异常笔迹指向内门",
			}},
		},
		"reader_retention_plan": map[string]any{
			"surface_beats": []map[string]any{{
				"plan_source":    "required_beats[0]/reader_reward_plan",
				"must_show":      "林砚在登记口亲眼看见名册新增笔迹，并因此改变选择",
				"reader_payoff":  "读者确认规则不是空设定，主角得到一条可追的证据",
				"scene_vehicle":  "山门登记口的名册、钟声和守门弟子的阻拦",
				"proof_on_page":  "名册红字、停笔、守门弟子改口",
				"function_shift": "从流程阻拦转为物证异常，再转为追问代价",
			}},
			"latent_context":      []string{"内门执事调换名册的完整原因只约束离屏行动，不在本章摊开"},
			"reveal_budget":       []string{"只露名册异常和登记弟子反应，不解释内门执事动机"},
			"cut_or_compress":     []string{"登记制度长篇说明", "所有离屏角色同时间线行动清单"},
			"page_turn_questions": []string{"名册红字为什么刚好在林砚追问后出现？"},
		},
		"evidence_return_chains": []map[string]any{{
			"offscreen_character":   "登记弟子",
			"event":                 "压住名册并向内门递消息",
			"evidence":              "名册备注和登记口争执",
			"protagonist_access":    "主角通过现场名册和后续消息得知",
			"return_timing":         "第2章",
			"distortion_or_misread": "登记弟子会把责任说成主角闯门",
			"chapter_to_resolve":    2,
		}},
		"ending_consequence_contract": map[string]any{
			"ending_mode":       "具体物件后果",
			"concrete_anchor":   "名册新增红字",
			"consequence":       "林砚被记入异常登记",
			"next_chapter_pull": "追查红字来源",
			"why_not_ui":        "名册笔迹是现场证据，不是给读者看的按钮",
			"forbidden_endings": []string{"UI选项", "突然一声响", "金句问号"},
		},
		"dormant_character_policy": []map[string]any{{
			"character":          "内门执事",
			"status":             "后台观察登记结果",
			"location":           "山门内",
			"no_change_reason":   "第一章没有通信或证据让主角直接见到他",
			"trigger_condition":  "名册异常传入内门",
			"knowledge_boundary": "主角不知道执事安排",
			"next_check":         "第2章",
		}},
		"reality_support_plan": []map[string]any{{
			"domain":               "登记/排队",
			"source_ref":           "stable common sense",
			"usable_detail":        "有人催促、有人误读、名册需要核验",
			"transformed_as":       "山门登记口冲突",
			"chapter_use":          "山门现场",
			"forbidden_direct_use": []string{"真实学校名称", "网页摘要"},
		}},
		"emotional_logic": []map[string]any{{
			"character":                 "林砚",
			"physiological_state":       "夜雨里发冷且体力下降",
			"immediate_state":           "刚听见钟声，注意力锁在名册",
			"baseline_mood":             "紧绷",
			"primary_emotion":           "恐惧",
			"composite_emotion":         "羞耻和不甘",
			"emotional_trigger":         "登记口即将关闭",
			"goal_appraisal":            "错过登记会毁掉入门机会",
			"boundary_threat":           "选择权和尊严受威胁",
			"regulation_strategy":       "压住恐惧，转成追问",
			"defense_mechanism":         "合理化",
			"cognitive_bias":            "损失厌恶",
			"approach_avoidance":        "想靠近入门资格，回避盲签",
			"short_long_term_tension":   "现在抢门 vs 长期不被规则坑",
			"self_relationship_tension": "自我安全 vs 被旁人看成胆怯",
			"conscious_reason":          "我只是要核验证据",
			"hidden_reason":             "害怕再次被不透明规矩淘汰",
			"meaning_need":              "证明自己不是任人挑选的废料",
			"metacognition":             "意识到自己快冲动，强行停一拍",
			"emotion_led_action":        "先追问名册而不是抢门",
			"event_completion_role":     "恐惧让他错过窗口，也逼出名册异常",
			"evidence_in_scene":         []string{"手指停在名册边", "短句追问"},
		}},
		"relationship_emotion_arcs": []map[string]any{{
			"pair":                           []string{"林砚", "登记弟子"},
			"relationship_type":              "制度敌对",
			"current_bond":                   "低信任",
			"emotional_want":                 "林砚想要解释权，登记弟子想免责",
			"fear":                           "被对方拖下水",
			"power_balance":                  "登记弟子掌握入口",
			"intimacy_stage":                 "陌生/对抗",
			"trust_debt":                     "无信任，只有规则债",
			"conflict_trigger":               "钟声和名册",
			"attachment_or_love_language":    "none；制度关系不以亲密表达",
			"boundary":                       "不能突然互信",
			"romance_potential":              "none；本关系无恋爱牵引",
			"next_emotional_beat":            "由互相防备转为互相留证",
			"protagonist_knowledge_boundary": "林砚只知道登记弟子现场表现",
		}},
		"visual_design": []map[string]any{{
			"character":        "林砚",
			"silhouette":       "被雨压窄的肩线",
			"face_and_hair":    "额发湿贴，脸色发白",
			"clothing_style":   "洗旧青衫和泥水鞋",
			"color_palette":    "青灰和泥褐",
			"body_language":    "手常停在名册边",
			"signature_object": "缺角令牌",
			"first_impression": "谨慎但被逼急",
			"status_wear":      "袖口湿透",
			"change_rule":      "入门后衣物逐渐出现门规痕迹",
			"scene_use":        "湿袖和令牌证明他在门外等了很久",
			"do_not_use":       []string{"空泛俊美", "真实品牌"},
			"material_source":  "no_material",
		}},
		"character_kit": []map[string]any{{
			"character":        "林砚",
			"first_appearance": true,
			"appearance_ref":   "visual_design:林砚",
			"weapons": []map[string]any{{
				"name":            "缺角令牌",
				"category":        "信物",
				"material_source": "book_facts",
			}},
			"abilities": []map[string]any{{
				"name":            "名册记忆",
				"codex_tier":      "uncodexed",
				"current_level":   "熟练",
				"usage_scope":     "只用于比对登记条目",
				"material_source": "no_material",
			}},
			"codex_compliance": "本书尚未建立 world_codex；能力约束以 characters.md 与 world_rules 为准，未越界。",
		}},
		"initial_state": []map[string]any{{
			"character":            "林砚",
			"current_goal":         "确认登记规则",
			"pressure":             "登记口关闭",
			"resources":            []string{"令牌"},
			"relationship_forces":  []string{"登记弟子掌握入口"},
			"secrets":              []string{"不暴露底牌"},
			"misbeliefs":           []string{"以为名册封存不可改"},
			"private_boundary":     "不乱签",
			"action_tendency":      "先核验证据",
			"likely_action":        "追问规则",
			"state_delta_to_track": []string{"knowledge", "decision"},
			"competence_stage":     "开局阶段",
			"skill_limits":         []string{"不知道后台调换名册"},
			"plausible_mistakes":   []string{"迟疑导致错过窗口"},
			"correction_triggers":  []string{"名册出现新字"},
			"knowledge_ledger": map[string]any{
				"known_facts":         []string{"登记口即将关闭"},
				"unknown_facts":       []string{"名册是否被换"},
				"evidence_seen":       []string{"钟声"},
				"confidence":          "medium",
				"forbidden_knowledge": []string{"执事真实安排"},
			},
			"decision_frame": map[string]any{
				"available_options":         []string{"抢门", "核对名册"},
				"rejected_options":          []string{"盲签"},
				"decision_rule":             "先核验证据",
				"tradeoff":                  "时间和风险",
				"risk_accepted":             "可能错过登记",
				"expected_gain":             "确认规则",
				"minimum_evidence_required": "看到名册",
			},
			"relationship_contract": []map[string]any{},
			"emotion_appraisal": map[string]any{
				"trigger_event":      "钟声响起",
				"goal_impact":        "压缩判断时间",
				"threat_to_value":    "试炼资格",
				"visible_expression": "先停住追问",
			},
			"arc_axis": map[string]any{
				"want":       "入门",
				"need":       "接受规则有灰区",
				"value_axis": "谨慎/冒险",
			},
		}},
		"voice_logic": []map[string]any{{
			"character":              "林砚",
			"personality_source":     "谨慎",
			"speech_principle":       "先问证据",
			"scene_objective":        "确认规则",
			"hidden_subtext":         "怕盲签后被坑",
			"knowledge_boundary":     "不知道执事调换名册",
			"diction_and_rhythm":     "短句追问，压力上来会停顿",
			"sentence_length":        "中短句为主",
			"punctuation_style":      "少反问，真实疑问才用问号",
			"line_break_style":       "看到名册异样时单独断行",
			"subtext_strategy":       "把害怕藏在费用和规则追问里",
			"silence_or_action_beat": "看名册时用手指停住代替解释",
			"voice_contrast":         "比登记弟子更谨慎、少命令句",
			"dialogue_functions":     []string{"核验证据"},
			"forbidden_moves":        []string{"替作者解释规则"},
		}},
		"dialogue_scene_blueprints": []map[string]any{{
			"scene_id":              "opening-dialogue-entry",
			"dialogue_mode":         "interrogation",
			"mode_reason":           "山门登记需要通过追问名册暴露制度灰区，而不是直接寒暄或告白",
			"scene_pressure":        "登记口即将关闭，名册被改动，令牌可能失效",
			"emotional_temperature": "林砚压住慌张，登记弟子越来越不耐烦",
			"relationship_frame":    "陌生申请者对掌握入口的登记弟子，权力不对等",
			"medium":                "face_to_face",
			"audience_presence": map[string]any{
				"present":         "none",
				"performance_for": "none",
				"audience_effect": "none",
			},
			"information_asymmetry": map[string]any{
				"pov_knows":       "林砚知道自己的令牌来历和等了一夜的事实",
				"pov_lacks":       "林砚不知道名册为何多出一行、登记潜规则是什么",
				"other_holds":     "登记弟子知道名册可被动过，也知道谁动的",
				"reader_position": "reader_level",
				"asymmetry_play":  "名册异常先被看到，追问逼出半句解释，信息差收窄的同时暴露改名册者的存在",
			},
			"value_shift": map[string]any{
				"value":          "入门资格的确定性",
				"opening_charge": "正：林砚以为只要报号就能入门",
				"turn_trigger":   "他看见名册多出一行，吞回报号那句话",
				"closing_charge": "负：报号可能把令牌绑定到错误名字，入门变成风险",
			},
			"power_trajectory": map[string]any{
				"opening_holder": "登记弟子，握着朱印、名册和关门时间",
				"flip_beat":      "第二轮，林砚指出名册异常，弟子被迫解释",
				"closing_holder": "林砚暂时抓住制度漏洞，但仍进不了门",
			},
			"opening_strategy":              "dialogue_first",
			"first_spoken_moment":           "登记弟子先催报号，迫使林砚在未核验名册前回应",
			"entry_line":                    "登记弟子先催他报出令牌号，把他从雨里推到名册前。",
			"entry_speaker":                 "登记弟子",
			"location_anchor":               "山门夜雨，登记桌只剩半盏灯。",
			"pov_state":                     "林砚先误以为只是普通催促，手在令牌边停了一拍。",
			"inner_question":                "名册怎么会多出一行？",
			"memory_bridge":                 "只补林砚等了一夜和令牌来历，不解释宗门全套规矩。",
			"identity_grounding":            "登记弟子握着名册和朱印，是此刻唯一能放人入门的人。",
			"dialogue_objective":            "用对白逼林砚核验名册，暴露登记潜规则。",
			"interlocutor_agenda":           "登记弟子想在关门前把责任推给林砚自己确认。",
			"protagonist_response_strategy": "林砚先追问名册，不直接报令牌号。",
			"objective_tactics": []map[string]any{{
				"character":           "登记弟子",
				"immediate_objective": "让林砚立刻报号并自己承担确认后果",
				"tactic":              "催促、压时间、把朱印悬在名册上",
				"counter_tactic":      "林砚不接催促，反问名册多出的那行",
				"emotional_leak":      "语速变快，称呼从客气改成不耐烦",
				"turn_result":         "登记弟子暴露自己知道名册可被动过",
			}, {
				"character":           "林砚",
				"immediate_objective": "确认名册是否可信，避免令牌被绑定错误名字",
				"tactic":              "短句追问，手指停在证据旁边",
				"counter_tactic":      "登记弟子继续用关门钟声施压",
				"emotional_leak":      "手指停住，先吞掉报号那句话",
				"turn_result":         "对话从催促转成制度漏洞核验",
			}},
			"turn_progression": []map[string]any{{
				"speaker":               "登记弟子",
				"surface_line_function": "催促报号",
				"hidden_subtext":        "把登记责任压给林砚",
				"new_information":       "登记窗口即将关闭",
				"power_move":            "弟子掌握入口和时间压力",
				"action_beat":           "朱印悬在名册上方没有落下",
				"next_pressure":         "林砚必须决定是否报号",
			}, {
				"speaker":               "林砚",
				"surface_line_function": "短句核验",
				"hidden_subtext":        "怕自己一报号就被绑定错误名册",
				"new_information":       "林砚不知道名册为何变化",
				"power_move":            "把催促缩小成可验证的问题",
				"action_beat":           "手指停在多出的那一行旁边",
				"next_pressure":         "登记弟子必须解释或继续催促",
			}},
			"directness_policy":       "关门时间可以直说，名册被动过只能通过反问和动作露出。",
			"subtext_source":          "潜台词来自权力差、令牌风险和登记弟子推责。",
			"escalation_pattern":      "no-and；林砚不报号，登记弟子继续加时间压力。",
			"beat_density":            "雨夜高压场用短动作拍，但不每句都加动作。",
			"silence_policy":          "林砚吞掉报号那一句，让沉默显示他开始怀疑。",
			"info_release_policy":     "名册异常先被看到，再由对白逼出半句解释。",
			"exposition_budget":       "只补令牌和等候，不补宗门历史。",
			"subtext_and_power_shift": "从弟子掌握登记节奏，转为林砚抓住名册漏洞。",
			"exit_beat":               "朱印没有落下，名册上那行墨迹被雨气晕开。",
			"do_not_use":              []string{"照抄样本题材", "先整段解释宗门", "让林砚立刻全懂"},
		}},
		"environment_state": []map[string]any{{
			"place":               "山门",
			"visible_state":       "钟声和泥水",
			"information_carried": "登记将关闭",
			"pressure_applied":    "压缩选择时间",
			"expected_change":     "名册被确认",
		}},
		"world_rules_in_force": []string{"登记需确认"},
		"information_gaps":     []string{"谁调换名册"},
		"causal_beats": []map[string]any{{
			"cause":            "钟声响起",
			"character_choice": "先问名册",
			"world_response":   "登记弟子催促",
			"story_result":     "规则边界暴露",
		}},
		"decision_points":   []string{"先核验再入内"},
		"outcome_shift":     []string{"从门外等待转入登记争议"},
		"scene_constraints": []string{"不提前解释执事安排"},
	}
	for k, v := range testWorldBackgroundFields() {
		sim[k] = v
	}
	if rewrite {
		sim["review_refinement"] = map[string]any{
			"trigger_sources":   []string{"rewrite_brief.review_summary"},
			"failure_modes":     []string{"声口偏移"},
			"acceptance_checks": []string{"对白不解释设定"},
		}
	}
	return sim
}

func testWorldBackgroundFields() map[string]any {
	return map[string]any{
		"world_background_layers": map[string]any{
			"physical_space":       "山门夜雨、入口狭窄、队列和门槛共同压缩选择空间",
			"time_layer":           "入门钟声即将结束，夜间窗口让判断时间不足",
			"social_institution":   "山门登记制度控制谁能入内",
			"cultural_norm":        "错过登记会被视为无资格，求情有羞耻成本",
			"relationship_network": "林砚、登记弟子和内门执事通过名册形成临时权力网",
			"economic_resource":    "名册、令牌和入门资格是稀缺资源",
			"conflict_tension":     "登记口关闭前，名册被调换打破稳定秩序",
			"social_mood":          "门外弟子焦躁，门内弟子怕背锅",
			"cosmology_meta_rule":  "门规一旦登记即生效，未登记者不能享有保护",
			"narrative_meta":       "读者知道名册异常，但不知道谁调换",
			"event_activation":     "时间窗口、名册资源和信息差共同逼出本章冲突",
		},
		"information_asymmetry": []map[string]any{{
			"subject":            "名册是否被调换",
			"reader_knows":       []string{"名册将出现异常"},
			"protagonist_knows":  []string{"登记口即将关闭"},
			"character_knows":    []string{"登记弟子知道旧名册位置"},
			"character_mistakes": []string{"林砚误以为名册封存不可改"},
			"character_pretends": []string{"登记弟子假装只是按流程催促"},
			"hidden_from_reader": []string{"内门执事的真实安排"},
			"reveal_condition":   "名册红字和后续内门消息回收",
			"tension_function":   "让角色在不知道幕后时做有限选择",
		}},
		"hidden_rule_pressure": []map[string]any{{
			"domain":         "山门登记",
			"visible_rule":   "钟声前登记即可入门",
			"hidden_rule":    "谁控制名册，谁控制资格解释权",
			"cultural_norm":  "迟到者被默认无能，求情会丢面子",
			"who_benefits":   "内门执事",
			"who_pays":       "门外弟子",
			"violation_cost": "被记入异常或失去资格",
			"scene_evidence": "名册、门槛、钟声和登记弟子的沉默",
		}},
		"social_mood_rumors": []map[string]any{{
			"group":              "门外候选弟子",
			"mood":               "焦躁和互相怀疑",
			"rumor":              "有人说晚到半刻也能走后门",
			"source":             "队列中的低声传话",
			"spread_path":        "山门队列",
			"reliability":        "半真半假",
			"behavior_effect":    "有人挤门，有人拉关系",
			"protagonist_access": "林砚只能听见片段",
		}},
		"ritual_calendar": []map[string]any{{
			"time":                 "入门夜钟声结束前",
			"calendar_type":        "仪式/deadline",
			"ritual_or_deadline":   "山门登记截止",
			"social_meaning":       "决定候选者是否被承认为门内人",
			"practical_constraint": "钟声后登记口关闭",
			"emotional_charge":     "错过等于被公开淘汰",
			"missed_cost":          "失去入门资格或被记异常",
			"scene_use":            "山门现场",
		}},
		"structural_resources": []map[string]any{{
			"resource":                      "名册和入门令牌",
			"controller":                    "登记弟子/内门执事",
			"scarcity_reason":               "名额有限且窗口关闭",
			"access_rule":                   "钟声前凭令牌登记",
			"black_market_or_informal_path": "走后门传闻",
			"price_or_cost":                 "被记名、欠人情或失去资格",
			"power_effect":                  "控制名册即可控制解释权",
			"chapter_pressure":              "林砚必须决定抢门还是核验名册",
		}},
		"cosmology_checks": []map[string]any{{
			"layer":               "门规/宗门规则",
			"rule":                "名册登记后门规生效",
			"cost":                "误登会背负异常记录",
			"boundary":            "未登记者不能享有门内保护",
			"exception_condition": "none",
			"evidence":            "名册和钟声",
			"failure_mode":        "若无边界，登记冲突失去意义",
		}},
		"conflict_web": []map[string]any{{
			"parties":         []string{"林砚", "登记弟子"},
			"conflict_type":   "资格/信息差",
			"open_goal":       "林砚要确认登记资格",
			"hidden_agenda":   "登记弟子想免责并按旧规挡人",
			"resource_stake":  "名册和入门资格",
			"information_gap": "林砚不知道名册被调换",
			"time_pressure":   "钟声即将结束",
			"current_balance": "候选者排队等待登记",
			"destabilizer":    "名册红字",
			"next_escalation": "第2章内门执事介入",
		}},
		"narrative_tension_matrix": map[string]any{
			"stability_turbulence":      "山门登记秩序被名册异常打破",
			"explicit_hidden_rules":     "表面按钟声登记，背后按名册控制权说话",
			"information_gap":           "读者和林砚都不知道内门安排",
			"time_pressure_preparation": "钟声倒计时发生在林砚未准备好时",
			"why_event_now":             "登记窗口关闭前才能制造资格压力",
			"reader_question":           "谁调换了名册",
			"pov_boundary":              "不越过林砚能看见的名册和钟声",
		},
	}
}

func TestPlanChapterRejectsUnexpandedLayeredChapter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 5); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "第一弧",
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "一"},
				{Chapter: 2, Title: "二"},
			},
		}, {
			Index:             2,
			Title:             "第二弧",
			EstimatedChapters: 3,
		}},
	}}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}

	tool := NewPlanChapterTool(st)
	if _, err := tool.Execute(context.Background(), planArgs(3)); err == nil || !strings.Contains(err.Error(), "expand_arc") {
		t.Fatalf("expected unexpanded chapter rejection, got %v", err)
	}
	if p, _ := st.Progress.Load(); p != nil && p.InProgressChapter == 3 {
		t.Fatal("unexpanded chapter should not become in-progress")
	}
}

func TestPlanChapterRejectsMissingPrewriteSimulation(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter":  1,
		"title":    "裸计划",
		"goal":     "直接开写",
		"conflict": "旧方案",
		"hook":     "留下悬念",
	})
	if _, err := NewPlanChapterTool(st).Execute(context.Background(), args); err == nil ||
		!strings.Contains(err.Error(), "缺少写前 causal_simulation") {
		t.Fatalf("expected missing prewrite simulation rejection, got %v", err)
	}
}

func TestPlanChapterRejectsMissingWebReferenceCollection(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}

	sim := testCausalSimulation(false)
	sim["context_sources"] = []string{
		"current_chapter_outline",
		"future_outline_window",
		"world_foundation",
		"character_dossiers",
		"progression_snapshot.next_plan",
		"chapter_contract",
		"characters",
		"world_rules/book_world",
		"character_continuity",
		"character_stage_records",
		"resource_audit",
		"foreshadow_ledger",
		"relationship_state",
		"user_rules/writing_engine",
	}
	sim["external_reference_plan"] = []map[string]any{{
		"query_or_need":         "本章不用网络资料",
		"source_type":           "none",
		"source_refs":           []string{"none"},
		"retrieved_at":          "unknown",
		"freshness_requirement": "none",
		"usable_details":        []string{},
		"transformation_rule":   "none",
		"do_not_use":            []string{},
	}}
	args, _ := json.Marshal(map[string]any{
		"chapter":           1,
		"title":             "无网络简报",
		"goal":              "测试",
		"conflict":          "测试",
		"hook":              "测试",
		"causal_simulation": sim,
	})
	if _, err := NewPlanChapterTool(st).Execute(context.Background(), args); err == nil ||
		!strings.Contains(err.Error(), "external_reference_plan.collected_source") {
		t.Fatalf("expected web reference collection rejection, got %v", err)
	}
}

func TestPlanChapterAllowsExpandedLayeredChapter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "第一弧",
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "一"},
				{Chapter: 2, Title: "二"},
			},
		}},
	}}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}

	tool := NewPlanChapterTool(st)
	if _, err := tool.Execute(context.Background(), planArgs(2)); err != nil {
		t.Fatalf("expected expanded chapter to plan, got %v", err)
	}
}

func TestPlanChapterPersistsSceneAnchors(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter":           1,
		"title":             "试炼令",
		"goal":              "让主角确认邀请不是善意",
		"conflict":          "长老只给令牌不解释代价",
		"hook":              "令牌背面浮出第二个名字",
		"scene_anchors":     []string{"缺角试炼令", "门槛上的泥水", "袖口血痕"},
		"causal_simulation": testCausalSimulation(false),
	})

	tool := NewPlanChapterTool(st)
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil {
		t.Fatalf("LoadChapterPlan: %v", err)
	}
	if plan == nil {
		t.Fatal("expected saved chapter plan")
	}
	want := []string{"缺角试炼令", "门槛上的泥水", "袖口血痕"}
	if len(plan.Contract.SceneAnchors) != len(want) {
		t.Fatalf("unexpected scene anchors: %+v", plan.Contract.SceneAnchors)
	}
	for i, anchor := range want {
		if plan.Contract.SceneAnchors[i] != anchor {
			t.Fatalf("scene anchor %d = %q, want %q", i, plan.Contract.SceneAnchors[i], anchor)
		}
	}
}

func TestPlanChapterPersistsCausalSimulation(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter":  1,
		"title":    "午夜欠费单",
		"goal":     "打开夜租规则和黑卡风险",
		"conflict": "收租规则逼主角确认名字",
		"hook":     "欠费单提出姓名抵扣",
		"causal_simulation": map[string]any{
			"project_promise":  "恐怖规则压迫后的合同反杀",
			"chapter_function": "第一章立住江烬从被收租者到规则买方的起点",
			"context_sources": []string{
				"current_chapter_outline",
				"future_outline_window",
				"world_foundation",
				"character_dossiers",
				"progression_snapshot.next_plan",
				"chapter_contract",
				"characters",
				"world_rules/book_world",
				"character_continuity",
				"character_stage_records",
				"side_character_journeys",
				"resource_audit",
				"foreshadow_ledger",
				"relationship_state",
				"user_rules",
				"writing_engine",
				"prewrite_storycraft_plan",
				"world_background_plan",
				"dialogue_writing",
				"reference_pack.references.web_reference_brief",
			},
			"writing_norms_applied": []map[string]any{{
				"source":              "anti_ai_tone/human_feel_craft",
				"rule_focus":          []string{"物件承担信息", "对白不讲设定"},
				"chapter_application": "用欠费单、门牌和便签承载规则，不让江烬解释世界观",
				"proof_targets":       []string{"scene_anchors", "voice_logic", "environment_state"},
				"failure_risk":        "解释段和规则讲解腔",
			}},
			"anti_ai_execution_plan": map[string]any{
				"risk_signals":           []string{"条款清单过整齐", "物件即时回应过密"},
				"counter_moves":          []string{"条款残缺错位", "至少一次门牌静默"},
				"sentence_rhythm_policy": "抽象判断后切回动作和纸面",
				"object_response_budget": "屏幕/门牌回应最多3次",
				"dialogue_function_plan": "对白只用于试探和拒绝，不解释设定",
				"review_checks":          []string{"删掉说话人后江烬是否仍像风控员", "章尾是否具体"},
			},
			"external_reference_plan": []map[string]any{{
				"query_or_need":         "夜间小区、物业群、支付失败和网络梗的当代生活细节",
				"source_type":           "project_web_reference_brief",
				"source_refs":           []string{"meta/web_reference_brief.md"},
				"retrieved_at":          "2026-07-05T00:00:00+08:00",
				"freshness_requirement": "最新资料优先",
				"usable_details":        []string{"小区群语气", "短视频外放"},
				"transformation_rule":   "转成楼道噪声和半截群聊，不搬运网页摘要",
				"do_not_use":            []string{"梗串", "旁白硬贴热词"},
			}},
			"trend_language_plan": []map[string]any{{
				"item":              "抽象成半句口头反应",
				"source_context":    "项目 web_reference_brief 的流行语条目",
				"character_carrier": "楼道里其余住户或手机外放",
				"scene_function":    "制造当代噪声和误判",
				"usage_budget":      "最多1处半句，不进章尾",
				"forbidden_usage":   "江烬关键判断和欠费单条款不用热梗",
			}},
			"grounding_details": []map[string]any{{
				"detail":         "支付失败/物业通知要像真实界面再异常化",
				"source_ref":     "meta/web_reference_brief.md",
				"transformed_as": "欠费单栏位和手机外放声",
				"scene_anchor":   "1704门缝欠费单",
			}},
			"offscreen_character_stage": []map[string]any{{
				"character":            "蒋牧",
				"time":                 "00:00-00:17",
				"location":             "阴阳公寓3栋1703门口",
				"status":               "存活但濒临被收租规则标记",
				"environment":          "1703欠费单和门牌同时施压，楼道冥雾正在吞没普通现金效力",
				"current_action":       "本来要把铁盒里的现金和购物卡拿去处理自己的旧欠，却被首夜夜租截住",
				"pressure":             "旧欠、首夜租和恐惧同时逼近",
				"decision":             "先用普通现金和金戒指硬缴，再试图诱导江烬代缴确认",
				"mistake_or_misbelief": "误以为只要有人替他确认就能把风险转嫁出去",
				"knowledge_boundary":   "不知道黑卡来源，不知道夜租完整规则，只知道自己的支付失败",
				"visible_in_chapter":   true,
				"evidence":             "1703门口现金退化、门牌吞影、蒋牧喊江烬代缴确认",
				"transport":            "原地被1703门牌和旧欠账单牵制",
				"travel_time":          "0分钟；从1703到1704只隔一户但不能越过账单确认",
				"meeting_constraint":   "只能隔门或楼道短暂接触，不能替江烬进入1704",
				"personality_delta":    "从求稳还旧欠转为更愿意把风险转嫁给别人",
				"death_state":          "存活待确认；若被收租需后续以鞋印/账单/1702线索传回",
				"protagonist_notice":   "江烬通过门外动静和账单变化即时获知一部分，完整旧欠后续再传回",
				"timeline_consistency": "与江烬在1704门内观察同一时间发生，不是等待被收的静态道具",
				"next_potential":       "后续可携带恶意转嫁记录和旧欠暂缓线回归",
				"tags":                 []string{"邻居", "首夜样本", "转嫁风险"},
			}, {
				"character":            "周行舟",
				"time":                 "00:08-00:14",
				"location":             "雾北市旧垣区行舟小超市卷帘门内",
				"status":               "存活，店内被雨衣客和异常收款码围困",
				"environment":          "雨衣客敲门、收款码异常亮起、店内学生躲在冷柜后",
				"current_action":       "护住店内学生和后仓物资，同时给江烬打电话核验身份",
				"pressure":             "店铺被诡异交易规则入侵，库存和活人都可能成为抵押物",
				"decision":             "不收纸钱，不试盐，不让学生喊称呼",
				"mistake_or_misbelief": "差点把盐当成可验证规则去试",
				"knowledge_boundary":   "不知道阴阳公寓夜租细则，只知道自己店门口也开始收费",
				"visible_in_chapter":   true,
				"evidence":             "电话中卷帘门、收款码、雨衣客和冷柜学生片段",
				"transport":            "原地守店；若去阴阳公寓需步行+夜间绕路",
				"travel_time":          "旧垣区夜雨中至少18-25分钟，且卷帘门被敲不能离开",
				"meeting_constraint":   "本章只能电话联系，无法赶到1704",
				"personality_delta":    "从爱用经验试错转为承认规则不能乱试",
				"death_state":          "存活；店内学生状态未确认",
				"protagonist_notice":   "江烬通过电话获知片段，店内完整遭遇后续由周行舟或学生补足",
				"timeline_consistency": "和3栋首夜同一时间在雾北市旧垣区另一处发生，交通上不能立刻赶到1704",
				"next_potential":       "后续可作为物资后勤与普通人视角回归",
				"tags":                 []string{"后勤", "城市场景", "非主角线"},
			}},
			"longform_opening": map[string]any{
				"target_reader":      "喜欢诡异规则和合同反杀的男频读者",
				"opening_hook":       "首夜租金可用名字抵扣",
				"serial_engine":      "每个鬼场景都能被确权、购买、经营，但账单审计同步升级",
				"reader_reward_loop": []string{"3章内买下短租庇护", "10章内进入午夜便利店", "每卷获得新资产和新债务"},
				"long_range_promises": []map[string]any{{
					"promise":            "冥府黑卡来源和账单审计",
					"first_chapter_seed": "黑卡卡面只露出有效交易和账单残字",
					"payoff_horizon":     "第一卷后段到第二卷",
				}},
				"reveal_budget":       []string{"不解释阴司银行", "不提前出现白骨财神"},
				"first_chapter_proof": []string{"主角用风控方式拆交易边界", "夜租规则能扩展成资产经营"},
				"retention_risks":     []string{"规则解释过多，必须用蒋牧失败演示"},
			},
			"character_arc_tests": []map[string]any{{
				"character":         "江烬",
				"want":              "先活过首夜",
				"core_lie":          "只要不确认就能完全安全",
				"need":              "把风险隔离和有限责任同时纳入判断",
				"truth":             "拒绝确认也会产生新的账单压力，行动必须留下证据和代价意识",
				"pressure_test":     "蒋牧求救、1704欠费单和黑卡残字同时逼迫他选择",
				"first_mistake":     "受惊时差点把黑卡贴到门缝试额度",
				"correction_signal": "蒋牧现金失败和门缝灰字诱导代缴确认",
				"chapter_evidence":  "江烬缩手、找取消/费用说明、便签记录和账单背面残字",
			}},
			"reader_reward_plan": map[string]any{
				"chapter_window":            "1-5",
				"first_chapter_small_win":   "江烬没有替蒋牧确认代缴，并拿到现金无效与姓名确认的可见证据",
				"new_debt_or_cost":          "1704欠费单转向江烬，黑卡代付对价未知",
				"payoff_visibility":         "欠费单、门牌和便签记录同时改变",
				"traffic_risk":              "只有恐吓没有小胜会压抑，免费黑卡会破坏交易逻辑",
				"forbidden_reward_patterns": []string{"免费代付", "无限额度明示", "UI选项菜单"},
				"reward_ladder": []map[string]any{{
					"chapter": 1,
					"reward":  "拿到首夜交易边界证据",
					"cost":    "被1704账单点名",
					"hook":    "黑卡背面残字烧出代付疑问",
				}, {
					"chapter": 2,
					"reward":  "确认1702/蒋牧线索",
					"cost":    "收租方开始审计江烬身份",
					"hook":    "旧布鞋停向1702",
				}},
			},
			"reader_retention_plan": map[string]any{
				"surface_beats": []map[string]any{{
					"plan_source":    "reader_reward_plan.first_chapter_small_win/ending_consequence_contract",
					"must_show":      "江烬拒绝替蒋牧确认代缴后，欠费单和黑卡残字把风险转向他",
					"reader_payoff":  "读者看到主角保住一次选择权，同时背上更具体的账单问题",
					"scene_vehicle":  "1704欠费单、门牌、黑卡背面残字和蒋牧的现金失败",
					"proof_on_page":  "拒绝确认、现金失效、欠费单改向、黑卡残字",
					"function_shift": "从求救压力转为交易验证，再转为新账单钩子",
				}},
				"latent_context":      []string{"阴司银行来源、白骨财神、蒋牧旧欠全貌只保留在台账和后续证据链"},
				"reveal_budget":       []string{"只露代付对价未知，不解释黑卡系统和收租方组织"},
				"cut_or_compress":     []string{"黑卡功能清单", "住户/房号/旧欠背景一口气说明"},
				"page_turn_questions": []string{"欠费单为什么会把江烬写成下一位承担者？"},
			},
			"evidence_return_chains": []map[string]any{{
				"offscreen_character":   "蒋牧",
				"event":                 "本来要处理旧欠却被首夜夜租截住，并试图把代缴风险转嫁给江烬",
				"evidence":              "旧布鞋鞋尖、1702门口灰印、蒋牧旧欠账单和楼道目击",
				"protagonist_access":    "江烬只能通过门外动静、账单变化和后续1702证据得知",
				"return_timing":         "第2章",
				"distortion_or_misread": "蒋牧会把转嫁说成求救，账单只显露部分旧欠",
				"chapter_to_resolve":    2,
			}, {
				"offscreen_character":   "周行舟",
				"event":                 "守小超市并通过电话传回另一处交易入侵样本",
				"evidence":              "电话噪声、异常收款码、冷柜学生片段",
				"protagonist_access":    "江烬通过电话只知道片段，不知道店内全貌",
				"return_timing":         "第3章",
				"distortion_or_misread": "电话断续导致江烬误判小超市危险等级",
				"chapter_to_resolve":    3,
			}},
			"ending_consequence_contract": map[string]any{
				"ending_mode":       "物件和方向性后果",
				"concrete_anchor":   "旧布鞋鞋尖与黑卡背面残字",
				"consequence":       "江烬被1704欠费单点名，黑卡代付对价未知，蒋牧线索转向1702",
				"next_chapter_pull": "查1702与代付对价，而不是等待按钮选择",
				"why_not_ui":        "黑卡只以烧痕/残字提示，不展示A/B标准选项给读者",
				"forbidden_endings": []string{"附加选项菜单", "突然一声响", "金句问号"},
			},
			"dormant_character_policy": []map[string]any{{
				"character":          "白骨财神",
				"status":             "后台账目压力源，第一章不直接出场",
				"location":           "阴司账目系统/未知",
				"no_change_reason":   "第一章没有证据让江烬见到其实体",
				"trigger_condition":  "黑卡账单或收租审计升级",
				"knowledge_boundary": "江烬不知道白骨财神身份",
				"next_check":         "第一卷账单审计升级时",
			}},
			"reality_support_plan": []map[string]any{{
				"domain":               "支付/账单",
				"source_ref":           "meta/web_reference_brief.md",
				"usable_detail":        "支付失败、费用说明缺失、确认入口和撤销入口",
				"transformed_as":       "江烬先找取消/费用说明，再被迫判断是否触碰黑卡",
				"chapter_use":          "1704门缝欠费单",
				"forbidden_direct_use": []string{"真实平台名称", "免费代付", "UI菜单章末"},
			}},
			"emotional_logic": []map[string]any{{
				"character":                 "江烬",
				"physiological_state":       "凌晨失业后疲惫、低血糖、手心出汗",
				"immediate_state":           "刚被夜租单点名，注意力卡在费用说明和姓名栏",
				"baseline_mood":             "压抑的警惕",
				"primary_emotion":           "恐惧",
				"composite_emotion":         "羞耻、内疚和责任感",
				"emotional_trigger":         "蒋牧求救和1704欠费单同时出现",
				"goal_appraisal":            "如果代缴确认，自己和妹妹的安全边界都会被拖进账里",
				"boundary_threat":           "姓名、签字习惯和责任边界被威胁",
				"regulation_strategy":       "把害怕压成核验动作",
				"defense_mechanism":         "合理化：告诉自己只是查条款",
				"cognitive_bias":            "损失厌恶和锚定在现金失败样本上",
				"approach_avoidance":        "想救人并拿到证据，回避替人认账",
				"short_long_term_tension":   "现在不开门保命 vs 长期建立可交易边界",
				"self_relationship_tension": "自保 vs 对妹妹和邻居的责任压力",
				"conscious_reason":          "我只是在确认交易内容",
				"hidden_reason":             "害怕再签下一份毁掉生活的责任",
				"meaning_need":              "证明自己不是被规则裁掉后只能逃的人",
				"metacognition":             "能意识到自己想用风控口吻遮住恐惧，但会被惊吓打断",
				"emotion_led_action":        "先找取消和费用说明，差点触碰黑卡后缩手",
				"event_completion_role":     "恐惧推动他拒绝代缴，内疚让他保留蒋牧线索",
				"evidence_in_scene":         []string{"缩手", "便签记录", "短句追问"},
			}, {
				"character":                 "蒋牧",
				"physiological_state":       "惊恐缺氧、出汗、手抖",
				"immediate_state":           "旧欠和首夜租同时逼近",
				"baseline_mood":             "慌张",
				"primary_emotion":           "恐惧",
				"composite_emotion":         "羞耻、嫉妒和怨恨",
				"emotional_trigger":         "现金被门牌吞掉效力",
				"goal_appraisal":            "自己再不转嫁就会被收走",
				"boundary_threat":           "生存和面子被威胁",
				"regulation_strategy":       "把求救伪装成提醒",
				"defense_mechanism":         "投射和合理化",
				"cognitive_bias":            "沉没成本和损失厌恶",
				"approach_avoidance":        "靠近江烬求生，回避承认自己坑别人",
				"short_long_term_tension":   "立刻脱身 vs 后续被江烬记账",
				"self_relationship_tension": "自救 vs 不愿承认自己害人",
				"conscious_reason":          "我只是让他帮个忙",
				"hidden_reason":             "害怕自己不值得被救",
				"meaning_need":              "证明自己不是第一个被抛下的人",
				"metacognition":             "几乎没有自控，靠本能转嫁风险",
				"emotion_led_action":        "诱导江烬代缴确认",
				"event_completion_role":     "他的恐惧让夜租规则以人情压力落地",
				"evidence_in_scene":         []string{"拍门", "话语改口", "藏现金铁盒"},
			}},
			"relationship_emotion_arcs": []map[string]any{{
				"pair":                           []string{"江烬", "蒋牧"},
				"relationship_type":              "邻居/债务敌对",
				"current_bond":                   "求救和转嫁混在一起",
				"emotional_want":                 "江烬想保边界，蒋牧想被替罪",
				"fear":                           "江烬怕被认账，蒋牧怕没人替他",
				"power_balance":                  "蒋牧掌握失败样本，江烬掌握是否回应",
				"intimacy_stage":                 "陌生/互不信任",
				"trust_debt":                     "蒋牧欠江烬一个恶意转嫁记录",
				"conflict_trigger":               "代缴确认",
				"attachment_or_love_language":    "none；本关系靠求生和债务牵引",
				"boundary":                       "江烬不能替蒋牧确认",
				"romance_potential":              "none；当前关系无恋爱牵引",
				"next_emotional_beat":            "江烬从厌恶转为把蒋牧当危险样本",
				"protagonist_knowledge_boundary": "江烬只知道蒋牧现场表现，不知道完整旧欠",
			}, {
				"pair":                           []string{"江烬", "周行舟"},
				"relationship_type":              "合作/友谊",
				"current_bond":                   "老同事式互损但能求证",
				"emotional_want":                 "都想确认自己不是单独疯了",
				"fear":                           "电话那头的人变成负担或诱饵",
				"power_balance":                  "周行舟有物资，江烬有规则样本",
				"intimacy_stage":                 "低信任合作",
				"trust_debt":                     "电话求证形成互助债",
				"conflict_trigger":               "小超市异常收款码",
				"attachment_or_love_language":    "用信息和物资支持表达信任",
				"boundary":                       "本章只能电话联系，不能赶场救援",
				"romance_potential":              "none；当前是友谊/合作线",
				"next_emotional_beat":            "从互相验证到互相欠一次活路",
				"protagonist_knowledge_boundary": "江烬只能知道电话传来的片段",
			}},
			"visual_design": []map[string]any{{
				"character":        "江烬",
				"silhouette":       "肩线收紧、偏瘦，像随时准备后退半步",
				"face_and_hair":    "短发凌乱，眼下有失眠青影，表情常慢半拍",
				"clothing_style":   "旧黑外套和皱衬衫，口袋里有便签本",
				"color_palette":    "灰黑、褪白和黑卡一点冷光",
				"body_language":    "先收手、低头看字、不做承诺性点头",
				"signature_object": "便签本和黑卡",
				"first_impression": "普通失业青年被迫进入规则现场",
				"status_wear":      "袖口湿冷，指尖沾灰",
				"change_rule":      "权力上升后衣物仍保留账本/卡片/磨损痕迹",
				"scene_use":        "缩手和旧外套证明他不是开局全能神豪",
				"do_not_use":       []string{"霸总黑卡形象", "空泛冷峻"},
				"material_source":  "no_material",
			}, {
				"character":        "蒋牧",
				"silhouette":       "塌肩、脖子前探，像随时要贴近求救",
				"face_and_hair":    "头发被汗贴住，嘴角发白",
				"clothing_style":   "廉价夹克和被攥皱的现金袋",
				"color_palette":    "暗黄、灰绿和门牌红光",
				"body_language":    "手心向外又突然抓门框",
				"signature_object": "现金铁盒",
				"first_impression": "本来有别的急事却被夜租截住的人",
				"status_wear":      "鞋边沾灰，指甲里有铁锈色",
				"change_rule":      "若回归，外观应带着被账单标记后的缺损",
				"scene_use":        "现金袋和塌肩让求救带着转嫁感",
				"do_not_use":       []string{"纯受害者模板", "无特征邻居"},
				"material_source":  "no_material",
			}},
			"character_kit": []map[string]any{{
				"character":        "江烬",
				"first_appearance": true,
				"appearance_ref":   "visual_design:江烬",
				"equipment": []map[string]any{{
					"name":            "冥府黑卡（虚拟卡面）",
					"category":        "契约资产",
					"material_source": "book_facts",
				}},
				"abilities": []map[string]any{{
					"name":            "风控直觉",
					"codex_tier":      "uncodexed",
					"current_level":   "职业熟练",
					"usage_scope":     "只用于单据核对与交易内容确认，不能直接对抗诡异",
					"material_source": "book_facts",
				}},
				"codex_compliance": "无 world_codex；以 world_foundation 铁律与 dossier 能力边界为准，未越界。",
			}},
			"world_background_layers": map[string]any{
				"physical_space":       "阴阳公寓17楼楼道狭长封闭，1703和1704相邻但门牌、猫眼、门缝和雾气限制见面",
				"time_layer":           "午夜首夜租窗口，凌晨疲惫和倒计时让江烬无法冷静全知",
				"social_institution":   "阳间物业秩序被夜租账单和门牌制度替代",
				"cultural_norm":        "邻居求救、人情代缴和名字确认形成羞耻与责任压力",
				"relationship_network": "江烬、蒋牧、周行舟和收租规则各自处于不同地点与信息量",
				"economic_resource":    "现金失效后，确认权、姓名、黑卡、门牌和账单凭证成为稀缺资源",
				"conflict_tension":     "普通小区首夜被账单、恐惧、转嫁和黑卡诱导撕开",
				"social_mood":          "楼道住户退回门内，电话那头小超市也在压低声音试探",
				"cosmology_meta_rule":  "冥府交易承认确认、名字和账单，不承认阳间现金",
				"narrative_meta":       "读者跟随江烬只知道现场证据，蒋牧旧欠和黑卡来源暂不揭示",
				"event_activation":     "午夜窗口、现金失效、姓名确认潜规则和蒋牧转嫁共同激活首章事件",
			},
			"information_asymmetry": []map[string]any{{
				"subject":            "夜租代缴和姓名确认",
				"reader_knows":       []string{"现金失败和门牌诱导确认"},
				"protagonist_knows":  []string{"江烬只看见蒋牧现金失败和欠费单残字"},
				"character_knows":    []string{"蒋牧知道自己的旧欠被截住但不知道黑卡来源"},
				"character_mistakes": []string{"江烬误以为不确认就完全安全", "蒋牧误以为能诱导别人替他认账"},
				"character_pretends": []string{"蒋牧把转嫁风险伪装成求救"},
				"hidden_from_reader": []string{"黑卡账单日和白骨财神身份"},
				"reveal_condition":   "欠费单残字、1702线索和后续账单审计逐步回收",
				"tension_function":   "防止主角突然全知，让读者追问代付拿什么换",
			}},
			"hidden_rule_pressure": []map[string]any{{
				"domain":         "阴阳公寓夜租",
				"visible_rule":   "欠费单要求缴纳首夜租",
				"hidden_rule":    "谁确认名字和代缴，谁就进入账单责任链",
				"cultural_norm":  "邻居求救会触发人情压力，但人情不能抵消账单",
				"who_benefits":   "收租方和试图转嫁的人",
				"who_pays":       "确认者和被点名住户",
				"violation_cost": "被门牌标记、债务绑定或失去安全边界",
				"scene_evidence": "1703现金退化、1704欠费单、蒋牧改口和门缝灰字",
			}},
			"social_mood_rumors": []map[string]any{{
				"group":              "阴阳公寓住户",
				"mood":               "恐慌、沉默、隔门偷听",
				"rumor":              "有人说不要报名字，不要替别人付第一笔钱",
				"source":             "楼道门后半句和物业群残留消息",
				"spread_path":        "同楼层门缝、群聊、电话",
				"reliability":        "半真半假",
				"behavior_effect":    "住户关门、压低声音、拒绝开门帮忙",
				"protagonist_access": "江烬只能听见门后片段和周行舟电话",
			}},
			"ritual_calendar": []map[string]any{{
				"time":                 "午夜首夜租窗口",
				"calendar_type":        "账单日/deadline",
				"ritual_or_deadline":   "首夜租确认",
				"social_meaning":       "普通住户被重新定义为承租人",
				"practical_constraint": "七分钟内确认或承担未知后果",
				"emotional_charge":     "失业、妹妹责任和邻居求救叠加",
				"missed_cost":          "失去证据、被点名或让蒋牧线索断掉",
				"scene_use":            "1704门内和1703门口",
			}},
			"structural_resources": []map[string]any{{
				"resource":                      "姓名确认权和黑卡交易权限",
				"controller":                    "收租方/冥府黑卡",
				"scarcity_reason":               "阳间现金失效，只有被承认的凭证和确认动作有效",
				"access_rule":                   "必须看清交易内容、确认对象和对价",
				"black_market_or_informal_path": "诱导代缴、冒名、隔门承诺",
				"price_or_cost":                 "姓名暴露、账单审计、关系债务",
				"power_effect":                  "控制确认动作即可控制谁被计入债务",
				"chapter_pressure":              "江烬必须先找取消和费用说明",
			}},
			"cosmology_checks": []map[string]any{{
				"layer":               "诡异契约/账单规则",
				"rule":                "现金无效，确认和账单凭证有效",
				"cost":                "每次试探都可能暴露姓名或形成债务",
				"boundary":            "没有明确凭证和代价前不能免费代付",
				"exception_condition": "none",
				"evidence":            "1703现金失败、欠费单残字、黑卡未完整显额",
				"failure_mode":        "若黑卡免费刷，交易逻辑会崩成作弊器",
			}},
			"conflict_web": []map[string]any{{
				"parties":         []string{"江烬", "蒋牧"},
				"conflict_type":   "求救/转嫁/债务",
				"open_goal":       "江烬想活过首夜，蒋牧想脱身",
				"hidden_agenda":   "蒋牧想把确认风险转给江烬",
				"resource_stake":  "首夜租、姓名、黑卡和失败样本",
				"information_gap": "江烬不知道蒋牧旧欠，蒋牧不知道黑卡来源",
				"time_pressure":   "首夜租窗口即将关闭",
				"current_balance": "旧邻里关系还残留一点人情压力",
				"destabilizer":    "现金无效和门牌标记",
				"next_escalation": "第2章1702和蒋牧旧欠证据回收",
			}, {
				"parties":         []string{"江烬", "收租方"},
				"conflict_type":   "身份确认/规则压迫",
				"open_goal":       "江烬要找交易边界",
				"hidden_agenda":   "收租方诱导江烬留下可收费动作",
				"resource_stake":  "黑卡权限、账单审计和门牌状态",
				"information_gap": "收租方知道完整规则，江烬只能看见残字",
				"time_pressure":   "午夜首夜窗口",
				"current_balance": "江烬尚未被完全纳入账单链",
				"destabilizer":    "1704欠费单点名",
				"next_escalation": "黑卡代付对价和账单日开始咬人",
			}},
			"narrative_tension_matrix": map[string]any{
				"stability_turbulence":      "普通老小区稳定秩序被首夜租打破，江烬先是被打破的人",
				"explicit_hidden_rules":     "表面是欠费缴租，背后是确认动作和姓名责任链",
				"information_gap":           "读者跟江烬只看见局部，蒋牧旧欠和收租方完整规则隐藏",
				"time_pressure_preparation": "倒计时发生在江烬失业疲惫且没有准备时",
				"why_event_now":             "午夜首夜窗口必须在第一章重新激活新 canon",
				"reader_question":           "黑卡代付到底拿什么换，蒋牧为什么去1702",
				"pov_boundary":              "不越过江烬亲见、电话和账单证据",
			},
			"initial_state": []map[string]any{{
				"character":            "江烬",
				"current_goal":         "先活过首夜并摸清交易边界",
				"pressure":             "夜租欠费单要求确认身份",
				"resources":            []string{"便签本", "未试刷的黑卡残字"},
				"relationship_forces":  []string{"蒋牧正在诱导代缴确认"},
				"secrets":              []string{"不能暴露姓名和黑卡可用性"},
				"misbeliefs":           []string{"尚不知道黑卡账单日和姓名抵扣后果"},
				"private_boundary":     "不能报出名字或替人认账",
				"action_tendency":      "先核验证据和交易内容，再决定是否回应",
				"likely_action":        "观察蒋牧失败案例后保全证据",
				"state_delta_to_track": []string{"姓名暴露度", "黑卡账单风险", "对蒋牧的风险评级"},
				"competence_stage":     "开局普通人风控阶段，只能做纸面核验和错误隔离",
				"skill_limits":         []string{"不知道夜租完整规则", "不能判断黑卡真实额度", "不能稳定识别收租鬼试探"},
				"plausible_mistakes":   []string{"受惊时差点触碰黑卡试额度", "把不签字误认为完全安全"},
				"correction_triggers":  []string{"蒋牧现金失败", "门缝灰字诱导代缴确认", "欠费单背面浮出姓名抵扣"},
				"knowledge_ledger": map[string]any{
					"known_facts":         []string{"现金无效", "门缝会诱导确认"},
					"unknown_facts":       []string{"黑卡账单日", "姓名抵扣后果"},
					"suspicions":          []string{"确认动作可能触发代缴"},
					"evidence_seen":       []string{"1703门口现金退化"},
					"confidence":          "medium",
					"forbidden_knowledge": []string{"阴司银行来源", "白骨财神身份"},
					"source_chapter":      1,
				},
				"decision_frame": map[string]any{
					"available_options":         []string{"不开门记录证据", "询问这笔买的是什么"},
					"rejected_options":          []string{"替蒋牧确认代缴", "直接刷黑卡"},
					"decision_rule":             "先核验证据和权利边界，再决定是否交易",
					"tradeoff":                  "不开门会失去邻居信息，开门可能被转嫁租金",
					"risk_accepted":             "暂时承担信息不足",
					"expected_gain":             "摸清夜租确认规则",
					"minimum_evidence_required": "看到条款、付款对象和确认动作后才行动",
				},
				"relationship_contract": []map[string]any{{
					"counterpart":        "蒋牧",
					"trust":              "互不信任",
					"leverage":           "蒋牧失败案例可作为证据",
					"alliance_status":    "临时信息来源",
					"betrayal_threshold": "蒋牧继续诱导代缴时拒绝帮助",
					"help_condition":     "只在不替他确认、不承担账单时交流",
					"source_chapter":     1,
				}},
				"emotion_appraisal": map[string]any{
					"trigger_event":      "门缝诱导确认姓名",
					"goal_impact":        "逼江烬判断是否开门",
					"threat_to_value":    "签字确认和责任转嫁",
					"visible_expression": "短句追问、不开门、记条款",
					"coping_strategy":    "把恐惧转成交易边界核验",
					"action_pressure":    "必须在七分钟内判断是否回应",
				},
				"arc_axis": map[string]any{
					"want":           "先活过首夜",
					"need":           "把风险隔离和对人的责任同时纳入判断",
					"wound_or_ghost": "失业后的风控签字创伤",
					"core_lie":       "只要不确认就能完全安全",
					"value_axis":     "自保/责任，交易边界/人情压力",
					"arc_stage":      "开局阶段",
					"pressure_test":  "蒋牧求救和夜租单同时逼近",
					"growth_signal":  "核验证据后承担有限责任",
				},
			}},
			"voice_logic": []map[string]any{{
				"character":              "江烬",
				"personality_source":     "characters.md:冷静风控、短句、边界意识",
				"speech_principle":       "先确认交易内容和权利边界，再决定是否开口",
				"scene_objective":        "阻止蒋牧把代缴风险转嫁给自己",
				"hidden_subtext":         "不说出姓名，也不让对方拿到确认动作",
				"knowledge_boundary":     "只知道现金无效和门缝诱导确认，不知道黑卡账单来源",
				"relationship_stance":    "邻居求救但互不信任",
				"diction_and_rhythm":     "短句、少术语、先问这笔买的是什么",
				"sentence_length":        "中短句为主，受惊时断句",
				"punctuation_style":      "少反问，费用核验才用问号",
				"line_break_style":       "发现黑卡残字和门牌变化时单独断行",
				"subtext_strategy":       "把恐惧藏在交易内容、确认动作和姓名边界里",
				"silence_or_action_beat": "用便签、缩手、不开门承载潜台词",
				"voice_contrast":         "比蒋牧更少哀求，比收租方更像普通人核验合同",
				"action_beat_policy":     "用便签、沉默和不开门的动作替代解释",
				"dialogue_functions":     []string{"暴露江烬风控思维", "推进代缴确认冲突"},
				"typical_moves":          []string{"少解释术语", "用短句追问这笔买的是什么"},
				"forbidden_moves":        []string{"不能替蒋牧喊确认", "不能用金句解释恐惧"},
				"dialogue_test":          []string{"删掉说话人后，是否仍像江烬在做风控判断"},
			}},
			"dialogue_scene_blueprints": []map[string]any{{
				"scene_id":              "ch01-jiangjin-jiangmu-doorway",
				"dialogue_mode":         "plea_for_help",
				"mode_reason":           "蒋牧在首夜租压迫下表层求助，底层想转嫁确认风险；江烬必须在恐惧和交易边界之间应对。",
				"scene_pressure":        "午夜首夜租倒计时、现金无效、门牌吞影、邻里人情和姓名确认风险同时压迫。",
				"emotional_temperature": "蒋牧慌乱外溢，江烬压住害怕但会误判和迟疑，不是全程冷静。",
				"relationship_frame":    "同楼邻居但互不信任；蒋牧掌握失败案例，江烬掌握是否回应的门内主动权。",
				"medium":                "through_door；隔着1704的门和门缝欠费单对话，动作拍以门内手部动作和纸面痕迹为主。",
				"audience_presence": map[string]any{
					"present":         "无第三方在场，但楼道尽头1702方向有被听见的风险",
					"performance_for": "蒋牧压低声音演给可能偷听的楼层，江烬的沉默演给门外的蒋牧",
					"audience_effect": "被听见的风险让蒋牧不敢喊出旧欠细节，信息只能半句露出",
				},
				"information_asymmetry": map[string]any{
					"pov_knows":       "江烬知道现金失效和门牌异常，握着是否开门的主动权",
					"pov_lacks":       "江烬不知道首夜租完整规则和蒋牧的旧欠",
					"other_holds":     "蒋牧知道首夜租失败案例和自己欠1702的债",
					"reader_position": "reader_level；读者与江烬同步，靠他的核验一点点拼出规则",
					"asymmetry_play":  "蒋牧的求救暴露首夜租存在，但旧欠只从他避开的话题里漏出半角",
				},
				"value_shift": map[string]any{
					"value":          "邻里安全感",
					"opening_charge": "半正：江烬以为这只是物业恶作剧，门还能挡住麻烦",
					"turn_trigger":   "蒋牧的现金被拒、求救词里出现'首夜租'",
					"closing_charge": "负：门挡不住规则，不救邻居的代价开始在江烬身上计息",
				},
				"power_trajectory": map[string]any{
					"opening_holder": "蒋牧，用求救、装熟和时间压力先发制人",
					"flip_beat":      "第二轮，江烬不开门只问'这笔买的是什么'，节奏被短句核验接管",
					"closing_holder": "江烬保住门内主动权，但背上未救邻居的心理债",
				},
				"address_shift":                 "蒋牧的称呼从'江哥'滑向直呼'江烬'再滑向哀求式的'哥'，亲疏伪装随绝望脱落。",
				"opening_strategy":              "object_first",
				"first_spoken_moment":           "先让现金失败或门缝欠费单制造压力，蒋牧的求救在江烬已经起疑后出现。",
				"entry_line":                    "delayed；蒋牧的第一句不是开篇第一句，而是在现金/门牌异常后求江烬搭话。",
				"entry_speaker":                 "蒋牧",
				"location_anchor":               "阴阳公寓17楼楼道，1703和1704之间只隔一户，门缝、欠费单和旧布鞋构成空间压力。",
				"pov_state":                     "江烬先把这当成物业恶作剧或诈骗，受惊时差点去摸黑卡，再把手收回来。",
				"inner_question":                "这笔买的到底是什么？",
				"memory_bridge":                 "只补江烬失业后的风控习惯、蒋牧搬来两个月和电梯外放印象，不补完整黑卡规则。",
				"identity_grounding":            "蒋牧通过1703门牌、旧布鞋、手里现金和求救语气被识别为同楼邻居，不是等着被剧情收走的陌生工具。",
				"dialogue_objective":            "用求助对白暴露首夜租的确认陷阱，让江烬做出有限回应而不是全知判断。",
				"interlocutor_agenda":           "蒋牧本来要处理旧欠和现金，却被首夜租截住；他想活下去，也想让江烬承担一部分确认风险。",
				"protagonist_response_strategy": "江烬只问交易内容和确认对象，允许停顿、错碰、吞回名字，不替蒋牧确认。",
				"objective_tactics": []map[string]any{{
					"character":           "蒋牧",
					"immediate_objective": "让江烬开口或代缴，替自己拖过首夜租窗口",
					"tactic":              "求救、装熟、强调邻居关系、把风险说成小忙",
					"counter_tactic":      "江烬不开门，只追问这笔买的是什么和谁确认",
					"emotional_leak":      "蒋牧重复称呼、声音发飘、避开旧欠，手里现金捏皱",
					"turn_result":         "读者看见蒋牧有自利和恐惧，不只是等待被收",
				}, {
					"character":           "江烬",
					"immediate_objective": "获得最低证据并避免姓名/代缴确认绑定",
					"tactic":              "短句核验、沉默、不开门、把问题缩成交易内容",
					"counter_tactic":      "蒋牧继续用求救和时间压力压他",
					"emotional_leak":      "江烬手指停在黑卡边，差点按错再收回",
					"turn_result":         "江烬保住边界但背上未救邻居的心理压力",
				}},
				"turn_progression": []map[string]any{{
					"speaker":               "蒋牧",
					"surface_line_function": "求救并装熟",
					"hidden_subtext":        "想让江烬替他承担确认风险",
					"new_information":       "普通现金已经无法支付夜租",
					"power_move":            "蒋牧用邻里关系和恐惧抢时间",
					"action_beat":           "旧布鞋停在1704门外，手里的现金边角发灰",
					"next_pressure":         "江烬必须判断是否开门或回应",
				}, {
					"speaker":               "江烬",
					"surface_line_function": "短句核验交易内容",
					"hidden_subtext":        "怕被姓名和代缴动作绑定",
					"new_information":       "江烬不知道完整规则，只抓住买断/代缴边界",
					"power_move":            "把蒋牧的求救缩成具体可验证问题",
					"action_beat":           "他把快碰到黑卡的手收回来，便签只写半句",
					"next_pressure":         "欠费单或门牌给出下一处残缺提示",
				}},
				"directness_policy":       "费用、门牌、是否代缴可以直说；恐惧、自私和姓名陷阱用动作、停顿、半句和纸面残字呈现。",
				"subtext_source":          "潜台词来自失业签字创伤、邻里人情、旧欠、收租规则信息差和生存恐惧。",
				"escalation_pattern":      "yes-but / no-and：江烬可以回应一小步，但每一步都拒绝全局承诺。",
				"beat_density":            "高压楼道用短动作拍，避免每句台词都让门牌即时回应。",
				"silence_policy":          "至少一次无人接话，让江烬的重话落在楼道静默里。",
				"info_release_policy":     "黑卡代付对价只露问题，不用菜单列项；蒋牧旧欠通过1702方向和后续证据回收。",
				"exposition_budget":       "每次只补一个当前对白必须知道的事实：失业风控、蒋牧邻居身份、现金失败。",
				"subtext_and_power_shift": "从蒋牧用求救压江烬，转为江烬发现确认边界；江烬未全赢，只保住一小段距离。",
				"exit_beat":               "旧布鞋在门外停很久，鞋尖转向1702，留下蒋牧下一步而非突然声响。",
				"do_not_use":              []string{"UI选项展示", "突然一声响", "江烬秒懂规则", "蒋牧只等着被收", "菜单式买断条款"},
			}},
			"crowd_roles": []map[string]any{{
				"group_name":        "楼道里其余住户",
				"count":             3,
				"scene_function":    "提供恐惧反应和规模感，证明夜租不是江烬个人幻觉",
				"reaction_policy":   "只用集体退后、压低声音和关门动作反应",
				"voice_budget":      "最多1句群声，不解释规则",
				"naming_policy":     "不命名，不引入正式角色名",
				"continuity_policy": "不进长期人物台账；若有人携带新信息再升级",
				"exit_condition":    "江烬收到欠费单后全部退回门内",
			}},
			"review_refinement": map[string]any{
				"trigger_sources":      []string{"rewrite_brief.review_summary", "rewrite_brief.issues"},
				"failure_modes":        []string{"江烬台词像作者解释规则", "章尾钩子抽象"},
				"localized_targets":    []string{"1703门口对话", "章尾欠费单"},
				"preserve_constraints": []string{"普通现金无效", "黑卡不付款"},
				"replanning_moves":     []string{"把规则说明拆进门牌和便签动作", "把江烬台词改成交易边界问题"},
				"acceptance_checks":    []string{"江烬没有替人确认", "章尾落到具体欠费单新字"},
				"stop_condition":       "连续两轮仍因同一声口问题失败时，停止整章重写，改为定位对话组局部 edit",
				"iteration_limit":      2,
			},
			"environment_state": []map[string]any{{
				"place":               "阴阳公寓17楼楼道",
				"visible_state":       "门牌泛红、灰雾贴地、1703门口现金退化",
				"information_carried": "阳间现金失效，门牌和欠费单才是规则入口",
				"pressure_applied":    "逼江烬判断是否开门代缴",
				"expected_change":     "1704收到自己的欠费单，成为被点名承租人",
			}},
			"world_rules_in_force": []string{"阳间现金失效", "代缴需要双方确认"},
			"information_gaps":     []string{"冥府黑卡额度和账单日未知"},
			"causal_beats": []map[string]any{{
				"cause":            "1703用现金缴租失败",
				"character_choice": "江烬不开门代缴，只拍照和记录",
				"world_response":   "门缝继续诱导双方确认",
				"story_result":     "江烬确认名字和确认动作才是入口",
			}},
			"decision_points":   []string{"江烬拒绝在付款栏写名字"},
			"outcome_shift":     []string{"江烬从普通住户转为有黑卡但受账单审计的人"},
			"scene_constraints": []string{"先展示规则代价，再命名收租鬼"},
		},
	})

	tool := NewPlanChapterTool(st)
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil {
		t.Fatalf("LoadChapterPlan: %v", err)
	}
	if plan == nil {
		t.Fatal("expected saved chapter plan")
	}
	if plan.CausalSimulation.ProjectPromise != "恐怖规则压迫后的合同反杀" {
		t.Fatalf("unexpected causal simulation: %+v", plan.CausalSimulation)
	}
	if len(plan.CausalSimulation.InitialState) != 1 || plan.CausalSimulation.InitialState[0].Character != "江烬" {
		t.Fatalf("unexpected initial state: %+v", plan.CausalSimulation.InitialState)
	}
	if len(plan.CausalSimulation.InitialState[0].Resources) != 2 ||
		plan.CausalSimulation.InitialState[0].ActionTendency == "" ||
		len(plan.CausalSimulation.InitialState[0].StateDeltaToTrack) != 3 {
		t.Fatalf("expected detailed character system state, got %+v", plan.CausalSimulation.InitialState[0])
	}
	initial := plan.CausalSimulation.InitialState[0]
	if len(initial.KnowledgeLedger.KnownFacts) != 2 ||
		initial.DecisionFrame.MinimumEvidenceRequired == "" ||
		len(initial.RelationshipContract) != 1 ||
		initial.EmotionAppraisal.TriggerEvent == "" ||
		initial.ArcAxis.CoreLie == "" {
		t.Fatalf("expected dynamic character P0 fields, got %+v", initial)
	}
	if len(plan.CausalSimulation.VoiceLogic) != 1 ||
		plan.CausalSimulation.VoiceLogic[0].SpeechPrinciple != "先确认交易内容和权利边界，再决定是否开口" {
		t.Fatalf("unexpected voice logic: %+v", plan.CausalSimulation.VoiceLogic)
	}
	if len(plan.CausalSimulation.CrowdRoles) != 1 ||
		plan.CausalSimulation.CrowdRoles[0].NamingPolicy == "" ||
		plan.CausalSimulation.CrowdRoles[0].ContinuityPolicy == "" {
		t.Fatalf("expected crowd role design, got %+v", plan.CausalSimulation.CrowdRoles)
	}
	if plan.CausalSimulation.VoiceLogic[0].SceneObjective == "" ||
		len(plan.CausalSimulation.VoiceLogic[0].DialogueFunctions) != 2 {
		t.Fatalf("expected detailed voice logic design, got %+v", plan.CausalSimulation.VoiceLogic[0])
	}
	if len(plan.CausalSimulation.DialogueBlueprints) != 1 ||
		plan.CausalSimulation.DialogueBlueprints[0].DialogueMode != "plea_for_help" ||
		plan.CausalSimulation.DialogueBlueprints[0].OpeningStrategy != "object_first" ||
		len(plan.CausalSimulation.DialogueBlueprints[0].ObjectiveTactics) != 2 {
		t.Fatalf("expected dialogue scene blueprint with mode and tactics, got %+v", plan.CausalSimulation.DialogueBlueprints)
	}
	if plan.CausalSimulation.ReviewRefinement.IterationLimit != 2 ||
		len(plan.CausalSimulation.ReviewRefinement.AcceptanceChecks) != 2 {
		t.Fatalf("unexpected review refinement loop: %+v", plan.CausalSimulation.ReviewRefinement)
	}
	if len(plan.CausalSimulation.CausalBeats) != 1 || plan.CausalSimulation.CausalBeats[0].WorldResponse == "" {
		t.Fatalf("unexpected causal beats: %+v", plan.CausalSimulation.CausalBeats)
	}
	if len(plan.CausalSimulation.ContextSources) < 10 || plan.CausalSimulation.ContextSources[0] != "current_chapter_outline" {
		t.Fatalf("unexpected context sources: %+v", plan.CausalSimulation.ContextSources)
	}
	if len(plan.CausalSimulation.WritingNorms) != 1 ||
		plan.CausalSimulation.WritingNorms[0].Source == "" ||
		plan.CausalSimulation.AntiAIPlan.ObjectResponseBudget == "" ||
		len(plan.CausalSimulation.ExternalRefs) != 1 ||
		len(plan.CausalSimulation.TrendLanguage) != 1 ||
		len(plan.CausalSimulation.GroundingDetails) != 1 {
		t.Fatalf("expected writing norms, anti-ai, external refs, trend language and grounding details, got %+v", plan.CausalSimulation)
	}
	if len(plan.CausalSimulation.EnvironmentState) != 1 || plan.CausalSimulation.EnvironmentState[0].Place != "阴阳公寓17楼楼道" {
		t.Fatalf("unexpected environment state: %+v", plan.CausalSimulation.EnvironmentState)
	}
	if plan.CausalSimulation.WorldLayers.EventActivation == "" ||
		len(plan.CausalSimulation.InformationLedger) == 0 ||
		len(plan.CausalSimulation.HiddenRules) == 0 ||
		len(plan.CausalSimulation.SocialMoodRumors) == 0 ||
		len(plan.CausalSimulation.RitualCalendar) == 0 ||
		len(plan.CausalSimulation.StructuralResources) == 0 ||
		len(plan.CausalSimulation.CosmologyChecks) == 0 ||
		len(plan.CausalSimulation.ConflictWeb) == 0 ||
		plan.CausalSimulation.TensionMatrix.ReaderQuestion == "" {
		t.Fatalf("expected complete world background simulation fields, got %+v", plan.CausalSimulation)
	}
	if len(plan.CausalSimulation.OffscreenStage) != 2 ||
		plan.CausalSimulation.OffscreenStage[0].Character != "蒋牧" ||
		plan.CausalSimulation.OffscreenStage[1].Location == "" {
		t.Fatalf("expected offscreen character stage records, got %+v", plan.CausalSimulation.OffscreenStage)
	}
	if plan.CausalSimulation.LongformOpening.SerialEngine == "" {
		t.Fatalf("expected longform opening serial engine, got %+v", plan.CausalSimulation.LongformOpening)
	}
	if len(plan.CausalSimulation.LongformOpening.LongRangePromises) != 1 ||
		plan.CausalSimulation.LongformOpening.LongRangePromises[0].Promise != "冥府黑卡来源和账单审计" {
		t.Fatalf("unexpected long range promises: %+v", plan.CausalSimulation.LongformOpening.LongRangePromises)
	}
}

func TestPlanChapterAllowsRewritePlanForPendingCompletedChapter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := st.Progress.MarkChapterComplete(1, 3000, "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := st.Progress.SetPendingRewrites([]int{1}, "审核指出江烬声口和因果链偏移"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter":           1,
		"title":             "午夜欠费单",
		"goal":              "按审核结论重建首章推演",
		"conflict":          "名字抵扣与黑卡试刷诱导",
		"hook":              "欠费单背面浮出姓名抵扣",
		"causal_simulation": testCausalSimulation(true),
	})

	result, err := NewPlanChapterTool(st).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	if payload["rewrite"] != true {
		t.Fatalf("expected rewrite=true result, got %+v", payload)
	}
	plan, err := st.Drafts.LoadChapterPlan(1)
	if err != nil {
		t.Fatalf("LoadChapterPlan: %v", err)
	}
	if plan == nil || len(plan.CausalSimulation.VoiceLogic) != 1 {
		t.Fatalf("expected saved rewrite causal plan with voice logic, got %+v", plan)
	}
	if len(plan.CausalSimulation.ReviewRefinement.TriggerSources) != 1 {
		t.Fatalf("expected saved rewrite review refinement, got %+v", plan.CausalSimulation.ReviewRefinement)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatalf("Progress.Load: %v", err)
	}
	if progress.InProgressChapter == 1 {
		t.Fatalf("rewrite planning should not mark completed chapter in-progress: %+v", progress)
	}
}
