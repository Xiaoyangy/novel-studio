package domain

// CharacterDNA 按"何时对读者可见"分组角色事实，每条是一句可直接被 Writer 使用的事实描述。
// 引用（不替代）Secrets / Misbeliefs 等既有字段：DNA 是可见时机的排布策略。
type CharacterDNA struct {
	Exposed []string `json:"exposed,omitempty"` // 显性基因：第一章即可展示（外貌/技能/习惯/口音）
	Hidden  []string `json:"hidden,omitempty"`  // 隐性基因：长线展开（创伤/执念/潜能/misbelief/秘密）
	Latent  []string `json:"latent,omitempty"`  // 突变基因：转折点触发（价值翻转/隐藏身世/道德破防点）
}

// IsEmpty 三组均空时为真，消费方据此跳过注入。
func (d CharacterDNA) IsEmpty() bool {
	return len(d.Exposed) == 0 && len(d.Hidden) == 0 && len(d.Latent) == 0
}
