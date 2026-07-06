package stylestat

import "testing"

func mk(ch int, text string) ChapterText { return ChapterText{Chapter: ch, Text: text} }

func TestBookReportPetPhrasesAndHomogeneity(t *testing.T) {
	same := "他没有说话。命运的齿轮开始转动。\n“走。”\n命运的齿轮开始转动，又一次。……"
	stats := BookReport([]ChapterText{mk(1, same), mk(2, same), mk(3, same)}, 3)
	if len(stats.PetPhrases) == 0 {
		t.Fatal("跨章重复 n-gram 应被识别为口头禅")
	}
	if stats.OpeningHomogeneity < 0.9 {
		t.Fatalf("相同开头结构应高同质：%.2f", stats.OpeningHomogeneity)
	}
	if BookReport([]ChapterText{mk(1, same)}, 3).Chapters != 1 {
		t.Fatal("单章应安全返回")
	}
}

func TestDriftReport(t *testing.T) {
	short := "他抬手。灯灭了。她后退半步。门开了。\n“谁？”\n没人应。"
	long := "在那个被冥雾整整笼罩了三十七个夜晚的城市里，江烬缓慢地、几乎带着某种仪式感地翻开了那本厚重的账簿，仿佛每一页都记载着无法偿还的债务。"
	base := []ChapterText{mk(1, short), mk(2, short), mk(3, short)}
	stable := []ChapterText{mk(4, short), mk(5, short)}
	drifted := []ChapterText{mk(4, long), mk(5, long)}
	if d := DriftReport(base, stable); d > 0.1 {
		t.Fatalf("平稳样本不应报漂移：%.3f", d)
	}
	if d := DriftReport(base, drifted); d <= 0.25 {
		t.Fatalf("构造漂移样本应超阈值：%.3f", d)
	}
	if DriftReport(nil, stable) != 0 {
		t.Fatal("空基线应返回 0")
	}
}
