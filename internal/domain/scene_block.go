package domain

import (
	"fmt"
	"regexp"
)

// sceneBlockIDPattern 场景块 ID 格式：chN-bM。
var sceneBlockIDPattern = regexp.MustCompile(`^ch\d+-b\d+$`)

// SceneBlock 章内更细颗粒的最小可控写作单元（1000-3000 字）：
// volume（5-20 万字）→ chapter（3000-8000 字）→ scene_block。
type SceneBlock struct {
	BlockID    string   `json:"block_id"` // "ch23-b1"
	Chapter    int      `json:"chapter"`
	Sequence   int      `json:"sequence"`
	Goal       string   `json:"goal"` // 本块 micro-goal
	Conflict   string   `json:"conflict,omitempty"`
	POV        string   `json:"pov,omitempty"`
	WordTarget int      `json:"word_target,omitempty"`
	HookType   string   `json:"hook_type,omitempty"` // 悬念 / 反转 / 小爽点
	Characters []string `json:"characters,omitempty"`
	Setting    string   `json:"setting,omitempty"`
}

// Validate 校验 ID 格式与必填字段。
func (b SceneBlock) Validate() error {
	if !sceneBlockIDPattern.MatchString(b.BlockID) {
		return fmt.Errorf("scene_block.block_id 格式应为 chN-bM，实际 %q", b.BlockID)
	}
	if b.Goal == "" {
		return fmt.Errorf("scene_block %s 缺少 goal", b.BlockID)
	}
	return nil
}

// SceneBlockList 一章的场景块清单。落盘 drafts/{NN}_blocks.json（可选工件，
// 不强制 Writer 产出，plan_chapter 现有 schema 不变）。
type SceneBlockList struct {
	Chapter    int          `json:"chapter"`
	Blocks     []SceneBlock `json:"blocks"`
	TotalWords int          `json:"total_words,omitempty"`
}

// SumWordTargets 各块字数目标之和（对照章目标用）。
func (l SceneBlockList) SumWordTargets() int {
	sum := 0
	for _, b := range l.Blocks {
		sum += b.WordTarget
	}
	return sum
}
