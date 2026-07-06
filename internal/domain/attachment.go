package domain

import "fmt"

// AttachmentStyle 依恋类型（Bowlby / Ainsworth 四型）。
// 依恋是人格常量，RelationshipContract 是关系变量，两者互补不替代：
// 两个回避型永远走不出追-逃循环——这由依恋类型决定，不由关系状态决定。
type AttachmentStyle string

const (
	AttachmentSecure             AttachmentStyle = "secure"              // 安全型：信任、平衡、可表达需求
	AttachmentAnxiousPreoccupied AttachmentStyle = "anxious-preoccupied" // 焦虑型：怕被抛弃，追-逃循环的"追"方
	AttachmentDismissiveAvoidant AttachmentStyle = "dismissive-avoidant" // 回避型：情感距离，不表达需要
	AttachmentFearfulAvoidant    AttachmentStyle = "fearful-avoidant"    // 混乱型：想要又怕，行为不可预测
)

// Validate 校验依恋类型枚举。
func (a AttachmentStyle) Validate() error {
	switch a {
	case AttachmentSecure, AttachmentAnxiousPreoccupied,
		AttachmentDismissiveAvoidant, AttachmentFearfulAvoidant:
		return nil
	}
	return fmt.Errorf("非法依恋类型: %s", a)
}

// Attachment 角色依恋画像。
type Attachment struct {
	Style           AttachmentStyle `json:"style"`
	History         string          `json:"history,omitempty"`          // 依恋成因（童年/关键关系史）
	Triggers        []string        `json:"triggers,omitempty"`         // 触发依恋反应的情境
	SecureBehaviors []string        `json:"secure_behaviors,omitempty"` // 安全感充足时的表现
}
