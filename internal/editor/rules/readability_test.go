package rules

import "testing"

// 一段有现场、有活对白、有主视角动摇、章末留悬念的正文，读者体验分必须明显高于
// 一段事实同样"正确"、却写成流程验收报告的正文。这正是选稿要保护的差别。
func TestReaderExperienceRewardsSceneOverProcedureReport(t *testing.T) {
	vivid := `第1章 西侧的门

零点十七分，程野把手机压到膝盖下面，指尖发凉。

“西侧哪来的客梯？”她本想开麦问出口，话到嘴边又咽了回去。那句话会让一个没核过的位置直接送到对面眼前。

孟乔的声音从耳机里挤进来，压得很低：“门开过两次。我只回传给你，别扩散。”

程野盯着那张导视图，西区、货梯、连廊，来回看了三遍，还是找不到“客梯”两个字。她把纸推回镜头照得见的地方，抬头看了一眼探头。

门后又响了一声，谁在那边？`

	report := `第1章 西侧的门

经核验，园区西侧竣工图与后续消防变更记录一致，西侧无客梯，仅有货梯一部，外接准备区与运输廊。

据门禁台账登记，零点十分至十七分货梯发生两次受控运行，西侧准备区门禁先行触发，运输廊门禁随后触发，两端记录相互吻合。

音轨样本已提交设备方独立比对，校验值、时间码与见证日期逐项对应，元数据完整，保管记录连续，归档编号未见断档。

处置组依照规定就上述事项予以复核，相关材料另行留痕，副本路径独立，未经程野。`

	vividScore := ReaderExperienceScore(vivid, AnalyzeChapter(1, vivid, nil).Metrics)
	reportScore := ReaderExperienceScore(report, AnalyzeChapter(1, report, nil).Metrics)

	if vividScore <= reportScore {
		t.Fatalf("vivid scene should read better than a procedure report: vivid=%.3f report=%.3f", vividScore, reportScore)
	}
	if vividScore < 0.5 {
		t.Fatalf("a clean reader-facing scene should score >= 0.5, got %.3f", vividScore)
	}
}

// SelectionScore 让读者可读性主导，同时保留反检测真实感：读者分高的候选，即使
// roughness 略低，也不能被判负——否则又退回"选最粗糙但难读"的老问题。
func TestSelectionScoreLetsReadabilityLead(t *testing.T) {
	readableButSmoother := SelectionScore(0.85, 0.70)
	roughButDry := SelectionScore(0.35, 1.30)
	if readableButSmoother <= roughButDry {
		t.Fatalf("high readability should win selection even at lower roughness: readable=%.3f rough=%.3f", readableButSmoother, roughButDry)
	}
}

// 候选评分必须把读者体验分和合成选稿分一并落盘，供三采样排序与看板使用。
func TestCandidateCarriesReadabilityAndSelectionScores(t *testing.T) {
	text := `第1章 门

她推开门，风灌进来。“你来干什么？”对方先开口，语气不善。

她没答，只把湿透的信封放在桌上，退后半步。谁也没先说话。`

	c := CandidateFromText(1, 1, text)
	if c.ReadabilityScore <= 0 {
		t.Fatalf("candidate must carry a readability score, got %.3f", c.ReadabilityScore)
	}
	if c.SelectionScore <= 0 {
		t.Fatalf("candidate must carry a blended selection score, got %.3f", c.SelectionScore)
	}
}
