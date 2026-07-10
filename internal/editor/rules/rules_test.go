package rules

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestAnalyzeChapterFlagsAIVoicePatterns(t *testing.T) {
	text := `# 第 1 章

雨像灰线一样挂下来。她说：“我不要怜悯，也不要施舍。”

风仿佛贴着墙走。卡莱尔说：“你手在抖。”

钟声宛如从井底升起。她没有回答。

月光像刀一样落在门缝上。门后又响了一声？`

	analysis := AnalyzeChapter(1, text, nil)
	if analysis.Metrics.ParagraphCount != 4 {
		t.Fatalf("paragraph count = %d, want 4", analysis.Metrics.ParagraphCount)
	}
	if analysis.Metrics.FigurativeDensity <= FigurativeDensityLimit {
		t.Fatalf("figurative density = %.2f, want above %.2f", analysis.Metrics.FigurativeDensity, FigurativeDensityLimit)
	}
	if len(analysis.Metrics.AphorismHits) == 0 {
		t.Fatalf("expected aphorism hit, got %+v", analysis.Metrics)
	}
	if !analysis.Metrics.ProtagonistWaver {
		t.Fatalf("expected protagonist waver")
	}
	if analysis.Label == "✅ 可通过" {
		t.Fatalf("expected warning/error label, got %s", analysis.Label)
	}
}

func TestCandidateRoughnessRewardsDialogueAndPenalizesAphorism(t *testing.T) {
	smooth := CandidateFromText(1, 1, `她说：“我要撕开真相。”

月光像刀，雾像网，钟声像心跳。她没有犹豫。
`)
	rough := CandidateFromText(2, 1, `卡莱尔问：“你确定？”

她抬起手，又放下。“不确定。你先说门后的声音从哪来。”

他笑了一下：“这句才像活人。”
`)
	if rough.RoughnessScore <= smooth.RoughnessScore {
		t.Fatalf("roughness score = %.2f, want above smooth %.2f", rough.RoughnessScore, smooth.RoughnessScore)
	}
}

func TestAnalyzeChapterFlagsNewHardConstraints(t *testing.T) {
	text := `# 第 1 章

命运从来不会怜悯看不见路的人。

马洛把契约摊开：“第一，签下名字；第二，献出鲜血；第三，忘掉来处。”

卡莱尔问：“你为什么来？”伊莲说：“我从一开始就是为了这个来的。”

她低头看着掌心的旧钥匙。这难道才是真正的选择吗？
`
	analysis := AnalyzeChapter(1, text, nil)
	for _, rule := range []string{
		"opening_single_sentence_aphorism",
		"ending_aphorism_question",
		"numbered_ladder_statement",
		"instant_purpose_answer_without_beat",
	} {
		if !hasRedFlag(analysis.RedFlags, rule) {
			t.Fatalf("expected red flag %s, got %+v", rule, analysis.RedFlags)
		}
	}
	if analysis.Label != "❌ 需返工" {
		t.Fatalf("label = %s, want rewrite label", analysis.Label)
	}
}

func TestAnalyzeChapterFlagsCatalogStuffing(t *testing.T) {
	text := `# 第 5 章

江烬没接话。他把短租庇护收据、保护费凭证、1602的物资押金条分成三叠。退烧贴的胶粘在文件袋里，撕不下来；蓝皮欠条被灰水泡软，边上还沾着半粒米。桌脚旁还散着竹柄雨伞、裂口搪瓷杯、旧台历夹、粉笔头、桦皮袖扣、蓼蓝布头、荞麦壳、陶埙裂片、绢纱穗、菖蒲根、贝母钮和紫铜铃舌。江烬只拨了三样入档，其余推到面单外。

其中一张旧票据背面印着一串铺名：璞泉、砚鹤、槐砧、霁蓝、葭灰、棠棣、篆雨、垆烟、蘅芜、柘枝、硖石、郢匣、黛瓦、缃帙、蕲艾、鹧鸪、麸金、皂荚、砭针、蜀锦。字边盖着歪章。
`
	analysis := AnalyzeChapter(5, text, nil)
	if !hasRedFlag(analysis.RedFlags, "catalog_stuffing") {
		t.Fatalf("expected catalog_stuffing, got %+v", analysis.RedFlags)
	}
	if !hasRedFlag(analysis.RedFlags, "catalog_stuffing_run") {
		t.Fatalf("expected catalog_stuffing_run, got %+v", analysis.RedFlags)
	}
	if analysis.Label != "❌ 需返工" {
		t.Fatalf("label = %s, want rewrite label", analysis.Label)
	}

	candidate := CandidateFromText(1, 5, text)
	if candidate.RoughnessScore >= 0.8 {
		t.Fatalf("roughness score = %.2f, want penalized catalog stuffing candidate", candidate.RoughnessScore)
	}
}

func TestAnalyzeChapterAllowsFunctionalShortObjectList(t *testing.T) {
	text := `# 第 5 章

桌脚旁散着几样抵押物：裂口搪瓷杯、旧台历夹、紫铜铃舌，还有一枚来历不明的桦皮袖扣。江烬只把能核价的拨进档案，其余全部推到白瓷碟里。

周行舟问：“这几样够不够？”
`
	analysis := AnalyzeChapter(5, text, nil)
	if hasRedFlag(analysis.RedFlags, "catalog_stuffing") || hasRedFlag(analysis.RedFlags, "catalog_stuffing_run") {
		t.Fatalf("unexpected catalog stuffing flags: %+v", analysis.RedFlags)
	}
}

func TestEndingHookUsedAllowsActionAftermath(t *testing.T) {
	aftermath := []string{
		"江烬把圆珠笔扣回去，听见门缝外的纸张慢慢退回灰水里。",
		"几秒后，锁孔里落下一点黑灰，欠费单底部多出一行小字：1704进入权属核验，催缴暂停六十分钟。",
	}
	if endingHookUsed(aftermath) {
		t.Fatalf("action aftermath should not be treated as uniform hook")
	}

	mystery := []string{
		"江烬把圆珠笔扣回去。",
		"下一秒，门后露出一把带血的钥匙。",
	}
	if !endingHookUsed(mystery) {
		t.Fatalf("new mystery ending should be treated as hook")
	}
}

func TestDialogueRatioLimitIsLengthAware(t *testing.T) {
	longChapter := domain.ChapterAIVoiceMetrics{
		DialogueRatio:  0.253,
		SentenceCount:  190,
		ParagraphCount: 60,
	}
	if flags := redFlags(longChapter, nil); hasRedFlag(flags, "supporting_dialogue_ratio") {
		t.Fatalf("unexpected long-chapter dialogue flag: %+v", flags)
	}

	longChapterSoftBand := domain.ChapterAIVoiceMetrics{
		DialogueRatio:  0.217,
		SentenceCount:  190,
		ParagraphCount: 60,
	}
	if flags := redFlags(longChapterSoftBand, nil); hasRedFlag(flags, "supporting_dialogue_ratio") {
		t.Fatalf("unexpected long-chapter soft-band dialogue flag: %+v", flags)
	}

	longChapterTooLow := domain.ChapterAIVoiceMetrics{
		DialogueRatio:  0.19,
		SentenceCount:  190,
		ParagraphCount: 60,
	}
	if flags := redFlags(longChapterTooLow, nil); !hasRedFlag(flags, "supporting_dialogue_ratio") {
		t.Fatalf("expected long-chapter dialogue flag below soft band")
	}

	shortChapter := domain.ChapterAIVoiceMetrics{
		DialogueRatio:  0.253,
		SentenceCount:  80,
		ParagraphCount: 24,
	}
	if flags := redFlags(shortChapter, nil); !hasRedFlag(flags, "supporting_dialogue_ratio") {
		t.Fatalf("expected short-chapter dialogue flag")
	}

	shortChapterNearLimit := domain.ChapterAIVoiceMetrics{
		DialogueRatio:  0.297,
		SentenceCount:  129,
		ParagraphCount: 34,
	}
	if flags := redFlags(shortChapterNearLimit, nil); hasRedFlag(flags, "supporting_dialogue_ratio") {
		t.Fatalf("near-limit short chapter should not be flagged: %+v", flags)
	}
}

func hasRedFlag(flags []domain.AIVoiceRedFlag, rule string) bool {
	for _, flag := range flags {
		if flag.Rule == rule {
			return true
		}
	}
	return false
}
