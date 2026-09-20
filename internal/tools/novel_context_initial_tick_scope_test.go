package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
)

func TestInitialWorldTickScopedContextKeepsWorldSourcesWithHundredChapterOutline(t *testing.T) {
	st := codexTestStore(t)
	if err := st.Progress.Init("百章只读上下文", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		t.Fatal(err)
	}
	var volumes []domain.VolumeOutline
	for volume := 1; volume <= 4; volume++ {
		v := domain.VolumeOutline{Index: volume, Title: fmt.Sprintf("卷%d", volume)}
		for arc := 1; arc <= 5; arc++ {
			a := domain.ArcOutline{Index: arc, Title: fmt.Sprintf("弧%d", arc), Goal: "未来仅为条件安排，不是已发生事实"}
			for slot := 1; slot <= 5; slot++ {
				chapter := (volume-1)*25 + (arc-1)*5 + slot
				a.Chapters = append(a.Chapters, domain.OutlineEntry{Chapter: chapter, Title: fmt.Sprintf("场景%d", chapter), CoreEvent: "角色甲依据实际资料独立作出选择。" + strings.Repeat("未来条件性剧情须待真实执行，不可冒充已经发生的事实。", 24), Hook: "本人尚未决定是否回应", Scenes: []string{"进入实际场景", "根据来源作判断", "保留未决选择"}})
			}
			v.Arcs = append(v.Arcs, a)
		}
		volumes = append(volumes, v)
	}
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "按实际选择收束", NonNegotiables: []string{"不得假造已经发生的事实"}, EstimatedScale: "4-4卷，100-100章"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "角色甲", Tier: "core", Role: "主角", Description: "CONTEXT_OWNER_SOURCE", InitialState: &domain.CharacterInitialState{Location: "接待处", CurrentGoal: "核验本人实际收到的资料", Pressure: "来访者等待", KnownFacts: []string{"本人知道自己的来意"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{{Category: "资料", Rule: "CONTEXT_WORLD_RULE_SOURCE", Boundary: "未发生不等于已完成", Visibility: "formal", CharacterView: "只读本人获准资料"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveBookWorld(domain.BookWorld{Version: 1, Name: "CONTEXT_BOOK_WORLD_SOURCE", Summary: "只有当前既定世界"}); err != nil {
		t.Fatal(err)
	}
	var codex domain.WorldCodex
	raw, _ := json.Marshal(validWorldCodexContent())
	if err := json.Unmarshal(raw, &codex); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorldCodex(codex); err != nil {
		t.Fatal(err)
	}
	tool := NewContextTool(st, References{}, "default")
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"profile":"world_simulation"}`)); !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "planning_memory.layered_outline") {
		t.Fatalf("unscoped full-book budget must still fail explicitly: %v", err)
	}
	result, err := tool.Execute(t.Context(), json.RawMessage(`{"chapter":1,"profile":"world_simulation"}`))
	if err != nil {
		t.Fatal(err)
	}
	if contextBudget(0, "world_simulation") != 61440 || len(result) > contextBudget(1, "world_simulation") {
		t.Fatal("read-scoping changed caps or exceeded budget")
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"world_rules", "characters", "book_world", "world_codex", "user_rules", "current_chapter_outline"} {
		if _, ok := firstContextValue(payload, key); !ok {
			t.Fatalf("focused initial world context lost %s", key)
		}
	}
	if _, ok := payload["_trimmed"]; ok {
		t.Fatalf("world sources were trimmed: %v", payload["_trimmed"])
	}
	if _, ok := firstContextValue(payload, "layered_outline"); ok {
		t.Fatal("focused task regained entire hundred-chapter outline")
	}
}
