package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chenhongyang/novel-studio/internal/store"
)

// 本文件是"第 1 章前必须完成零章初始化"卡点的单一事实源，
// 三个消费方共用同一判定：
//   - writer 派发守卫（internal/agents）：拒绝在未就绪时派 writer 写第 1 章；
//   - Coordinator StopGuard（internal/host/reminder）：该场景放行收工，交还宿主编排；
//   - pipeline write 阶段（cmd/novel-studio）：自动执行 --zero-init 后续跑。
// 判定 = readiness 工件存在 && ready=true && 未因 foundation 更新而过期。

// zeroInitFreshnessGrace：zero-init 运行过程中自身会补齐 book_world 等缺失
// foundation 文件（时间早于 readiness 落盘），留一点余量避免同一次运行内的
// 写盘顺序被误判为"foundation 晚于 readiness 更新"。
const zeroInitFreshnessGrace = 2 * time.Second

// foundationFreshnessFiles 参与 readiness 过期判定的 foundation 工件。
// 任何一个在 readiness 生成之后被重写，零章推演资产就可能引用了旧设定。
//
// 刻意不含 book_world.json：save_world_tick 会常规更新其中的势力进度钟（模拟状态，
// 非 authored 设定变更），尤其是"第 1 章前的初始 world_tick"必然晚于 readiness 落盘，
// 若纳入会把这条正常路径误判为"foundation 变更→零章过期"造成死循环。真正的 authored
// 变更（人物/世界规则/法典/大纲）仍会触发过期。
var foundationFreshnessFiles = []string{
	"premise.md", "characters.json", "world_rules.json",
	"layered_outline.json", "outline.json", "world_codex.json",
}

// EnsureWorldCodexForChapterOne 第 1 章前的全局世界法典门禁：
// 能力分级/技能范畴/种族/武器/装备/16 维世界 sections 是本工作室的
// 第 1 章硬性交付，缺失时引导 Coordinator 派 architect 补齐（会话内可完成）。
func EnsureWorldCodexForChapterOne(st *store.Store) error {
	if !ChapterOnePendingFirstWrite(st) {
		return nil
	}
	if nonEmptyRegularFile(filepath.Join(st.Dir(), "world_codex.json")) {
		return nil
	}
	return fmt.Errorf("第 1 章前必须先落盘全局世界法典（world_codex.json 缺失）：" +
		"请派 architect_long 调用 save_foundation(type=world_codex) 保存能力分级、技能范畴、种族、武器/装备范畴、" +
		"16 维世界 sections 与 immutability_policy，之后再派 writer")
}

// EnsureZeroInitReadyForChapterOne 第 1 章开写前的硬卡点。
// 第 1 章已有完成记录（重写/返工路径）时不拦。
func EnsureZeroInitReadyForChapterOne(st *store.Store) error {
	if !ChapterOnePendingFirstWrite(st) {
		return nil
	}
	ok, reason := ZeroInitReadinessState(st.Dir())
	if ok {
		return nil
	}
	return fmt.Errorf("第 1 章前必须先完成零章初始化（--zero-init）：%s", reason)
}

// ChapterOnePendingFirstWrite 报告当前是否处于"第 1 章还从未写完"的阶段。
func ChapterOnePendingFirstWrite(st *store.Store) bool {
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return true // 读不到进度按最严格口径处理：视为第 1 章未写
	}
	return len(progress.CompletedChapters) == 0
}

// ZeroInitReadinessState 读取 readiness 工件并做过期判定。
// 返回 (是否就绪, 未就绪原因)。
func ZeroInitReadinessState(dir string) (bool, string) {
	path := filepath.Join(dir, "meta", "first_chapter_generation_readiness.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "meta/first_chapter_generation_readiness.json 不存在（尚未执行 zero-init）"
	}
	var r struct {
		Ready       bool     `json:"ready"`
		GeneratedAt string   `json:"generated_at"`
		Missing     []string `json:"missing"`
		Issues      []string `json:"issues"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return false, "readiness 工件无法解析，需重跑 zero-init"
	}
	if !r.Ready {
		return false, fmt.Sprintf("readiness ready=false（missing=%d issues=%d，详见 meta/first_chapter_generation_readiness.md）", len(r.Missing), len(r.Issues))
	}
	generatedAt, err := time.Parse(time.RFC3339, r.GeneratedAt)
	if err != nil {
		return false, "readiness generated_at 无法解析，需重跑 zero-init"
	}
	for _, rel := range foundationFreshnessFiles {
		info, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		if info.ModTime().After(generatedAt.Add(zeroInitFreshnessGrace)) {
			return false, fmt.Sprintf("%s 在零章初始化之后被更新（foundation 变更），零章资产已过期", rel)
		}
	}
	return true, ""
}

// FoundationCoreComplete 报告 Architect 的核心 foundation 是否已齐。
// 用于区分"该派 architect 补设定"与"该做零章初始化"两种未就绪。
// world_codex 也算核心：StopGuard 在它缺失时不放行收工，逼 Coordinator 派 architect 补齐。
func FoundationCoreComplete(dir string) bool {
	for _, rel := range []string{"premise.md", "characters.json", "world_rules.json", "book_world.json", "world_codex.json", filepath.Join("meta", "compass.json")} {
		if !nonEmptyRegularFile(filepath.Join(dir, rel)) {
			return false
		}
	}
	return nonEmptyRegularFile(filepath.Join(dir, "layered_outline.json")) ||
		nonEmptyRegularFile(filepath.Join(dir, "outline.json"))
}

func nonEmptyRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}
