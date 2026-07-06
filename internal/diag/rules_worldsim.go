package diag

import (
	"fmt"
	"strings"
)

// ThresholdWorldTickLag 世界推演游标允许落后最新完成章的章数（约 1.5 个弧）。
const ThresholdWorldTickLag = 15

// ThresholdAgendaStale 离屏日程允许的最大停滞章数。
const ThresholdAgendaStale = 15

// OffscreenWorldStale 检测世界模拟停摆：启用了世界推演（有 tick 工件）的项目，
// 镜头外世界不应长期落后于正文进度，离屏角色日程不应集体停滞。
// 未启用世界推演的项目（无工件）零噪音。
func OffscreenWorldStale(snap *Snapshot) []Finding {
	if snap.WorldTick == nil || snap.Progress == nil {
		return nil
	}
	var findings []Finding

	latest := snap.LatestCompleted()
	if gap := latest - snap.WorldTick.ThroughChapter; gap > ThresholdWorldTickLag {
		findings = append(findings, Finding{
			Rule:       "OffscreenWorldStale",
			Category:   CatPlanning,
			Severity:   SevWarning,
			Confidence: ConfMedium,
			AutoLevel:  AutoNone,
			Target:     "prompt.architect",
			Title:      fmt.Sprintf("世界推演已落后正文 %d 章", gap),
			Evidence:   fmt.Sprintf("world_tick.through_chapter=%d, latest=ch%d, tick_id=%s", snap.WorldTick.ThroughChapter, latest, snap.WorldTick.TickID),
			Suggestion: "镜头外世界停摆会让离屏事件与伏笔素材断供。Architect 应在下一次弧/卷边界先调 save_world_tick 把世界推进到弧末，再展开规划。",
		})
	}

	if stale := snap.OffscreenAgenda.Stale(latest, ThresholdAgendaStale); len(stale) > 0 {
		var b strings.Builder
		for i, a := range stale {
			if i > 0 {
				b.WriteString("、")
			}
			b.WriteString(a.Name)
			if i >= 3 {
				b.WriteString(" 等")
				break
			}
		}
		names := b.String()
		findings = append(findings, Finding{
			Rule:       "OffscreenWorldStale",
			Category:   CatPlanning,
			Severity:   SevInfo,
			Confidence: ConfLow,
			AutoLevel:  AutoNone,
			Target:     "prompt.architect",
			Title:      fmt.Sprintf("%d 个离屏角色日程停滞超 %d 章", len(stale), ThresholdAgendaStale),
			Evidence:   fmt.Sprintf("stale_agendas=%s, latest=ch%d", names, latest),
			Suggestion: "active 状态的离屏日程长期未推进：下次世界推演时逐个推进 1-2 步，或把已无戏剧价值的角色转 dormant。",
		})
	}
	return findings
}

// BookStyleHomogeneity Task 082：书级 AI 味统计消费——章首/章尾结构同质度过高或
// 跨章口头禅堆积时给 warning 级提醒。无工件（未启用/章数不足）零噪音。
func BookStyleHomogeneity(snap *Snapshot) []Finding {
	if snap.BookStylestat == nil {
		return nil
	}
	var findings []Finding
	getF := func(k string) float64 {
		if v, ok := snap.BookStylestat[k].(float64); ok {
			return v
		}
		return 0
	}
	if oh, eh := getF("opening_homogeneity"), getF("ending_homogeneity"); oh > 0.92 || eh > 0.92 {
		findings = append(findings, Finding{
			Rule: "BookStyleHomogeneity", Category: CatQuality, Severity: SevWarning,
			Confidence: ConfMedium, AutoLevel: AutoNone, Target: "prompt.writer",
			Title:      fmt.Sprintf("章首/章尾结构同质度过高（开=%.2f 尾=%.2f）", oh, eh),
			Evidence:   "meta/book_stylestat.json",
			Suggestion: "AI 长篇的书级暴露面：连续章开头/结尾结构雷同。让 Writer 参照 hook_history 换开场装置与收束方式。",
		})
	}
	if drift, ok := snap.BookStylestat["drift_distance"].(float64); ok && drift > 0.25 {
		findings = append(findings, Finding{
			Rule: "BookStyleHomogeneity", Category: CatQuality, Severity: SevWarning,
			Confidence: ConfMedium, AutoLevel: AutoNone, Target: "runtime.flow",
			Title:      fmt.Sprintf("风格漂移：近 5 章与基线特征距离 %.2f（>0.25）", drift),
			Evidence:   "meta/book_stylestat.json drift_distance",
			Suggestion: "疑似模型/配置切换或状态污染（Task 079）：核对 prompt_manifest 与 roles 配置是否中途变更。",
		})
	}
	if pets, ok := snap.BookStylestat["pet_phrases"].([]any); ok && len(pets) >= 8 {
		findings = append(findings, Finding{
			Rule: "BookStyleHomogeneity", Category: CatQuality, Severity: SevInfo,
			Confidence: ConfLow, AutoLevel: AutoNone, Target: "prompt.writer",
			Title:      fmt.Sprintf("跨章叙述层口头禅累积 %d 条", len(pets)),
			Evidence:   "meta/book_stylestat.json pet_phrases",
			Suggestion: "对照 voice_logic 白名单区分角色口头禅与叙述层复读；后者应替换表达。",
		})
	}
	return findings
}

// ReaderMetricsDip Task 077：读者数据登记消费——某章 read_through 显著低于相邻章
// （<相邻均值的 70%，样本 ≥3 章）时输出 proposed 观察；样本不足零噪音。
func ReaderMetricsDip(snap *Snapshot) []Finding {
	if len(snap.ReaderMetrics) < 3 {
		return nil
	}
	byChapter := map[int]float64{}
	for _, r := range snap.ReaderMetrics {
		if r.ReadThrough > 0 {
			byChapter[r.Chapter] = r.ReadThrough
		}
	}
	var findings []Finding
	for ch, v := range byChapter {
		prev, okP := byChapter[ch-1]
		next, okN := byChapter[ch+1]
		if !okP || !okN {
			continue
		}
		neighbor := (prev + next) / 2
		if neighbor > 0 && v < neighbor*0.7 {
			hookScore := -1
			if r, ok := snap.Reviews[ch]; ok {
				for _, d := range r.Dimensions {
					if d.Dimension == "hook" {
						hookScore = d.Score
					}
				}
			}
			findings = append(findings, Finding{
				Rule: "ReaderMetricsDip", Category: CatQuality, Severity: SevInfo,
				Confidence: ConfLow, AutoLevel: AutoNone, Target: "prompt.editor",
				Title:      fmt.Sprintf("第 %d 章疑似弃读点（read_through=%.2f，相邻均值 %.2f）", ch, v, neighbor),
				Evidence:   "meta/reader_metrics.jsonl",
				Suggestion: fmt.Sprintf("proposed：复盘该章钩子与节奏（评审 hook 维度当时 %d 分）；数据只登记不自动动作。", hookScore),
			})
		}
	}
	return findings
}

// CostProfileSkew Task 080：成本剖面观测（不动 BudgetSentinel 总额政策）。
// judge（editor+reviewer）成本占比 > 25% 时 info 提醒（LLM-as-Judge 实践口径：
// judge ≤ 生产 10-15%）。单章 cost 维度因 usage 无章号暂缺，见任务书偏差记录。
func CostProfileSkew(snap *Snapshot) []Finding {
	if snap.Usage == nil || snap.Usage.Overall.Cost <= 0 {
		return nil
	}
	judge := 0.0
	for role, t := range snap.Usage.PerAgent {
		if role == "editor" || role == "reviewer" {
			judge += t.Cost
		}
	}
	ratio := judge / snap.Usage.Overall.Cost
	if ratio <= 0.25 {
		return nil
	}
	return []Finding{{
		Rule: "CostProfileSkew", Category: CatQuality, Severity: SevInfo,
		Confidence: ConfMedium, AutoLevel: AutoNone, Target: "runtime.flow",
		Title:      fmt.Sprintf("judge 成本占比 %.0f%%（>25%%）", ratio*100),
		Evidence:   fmt.Sprintf("meta/usage.json：judge=$%.2f / total=$%.2f", judge, snap.Usage.Overall.Cost),
		Suggestion: "评审开销偏高：考虑 reviewer 用更小模型、降低复评频率，或确认无审改死循环（对照 review_round）。",
	}}
}
