package domain

// ThematicQuestion 全书核心命题：零章确定，并让它在每一卷以不同形态变奏出现，
// 否则写到 100 章就开始散。挂在零章推演（prewrite storycraft plan）里。
type ThematicQuestion struct {
	Question              string            `json:"question"`                          // "复仇完成之后主角如何重新生活?"
	VariationsPerVolume   map[string]string `json:"variations_per_volume,omitempty"`   // 卷号 → 该卷的变奏形态
	AuthorStance          string            `json:"author_stance,omitempty"`           // "不站队"
	PrimaryReaderQuestion string            `json:"primary_reader_question,omitempty"` // 读者最想追问的问题
}

// IsEmpty 未填写命题时为真，渲染方据此跳过。
func (t ThematicQuestion) IsEmpty() bool {
	return t.Question == "" && len(t.VariationsPerVolume) == 0 &&
		t.AuthorStance == "" && t.PrimaryReaderQuestion == ""
}
