package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSaveFoundationPreservesDistinctExplicitCharacterOpeningStates(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, CoreEvent: "未来全章事件与尚未揭露的真相"}}); err != nil {
		t.Fatal(err)
	}
	characters := []domain.Character{
		{Name: "值班员", Role: "主角", Arc: "未来恢复信任", InitialState: &domain.CharacterInitialState{
			Location: "仓库", CurrentGoal: "核对眼前账页", Pressure: "交接期限临近", KnownFacts: []string{"眼前账页的数字有覆盖痕迹"},
		}},
		{Name: "机修员", Role: "重要配角", InitialState: &domain.CharacterInitialState{
			Location: "机修棚", CurrentGoal: "完成机务检查", Pressure: "安全工序不可省略", KnownFacts: []string{"当前检查尚未完成"},
		}},
		{Name: "历史配角", Arc: "旧记录没有开局态"},
	}
	args, _ := json.Marshal(map[string]any{"type": "characters", "content": characters})
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	got, err := st.Characters.Load()
	if err != nil || !reflect.DeepEqual(got, characters) {
		t.Fatalf("tool rewrote/inferred character state: %+v err=%v", got, err)
	}
	if got[2].InitialState != nil || got[0].InitialState.Location == got[1].InitialState.Location {
		t.Fatal("legacy or offscreen character was assigned the protagonist's opening location")
	}
}

func TestSaveFoundationRejectsInvalidCharacterStateBeforeReplacingAnyCharacters(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "保留原角色"}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(st.Dir(), "characters.json"))
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"type": "characters", "content": []domain.Character{
		{Name: "合法角色", InitialState: &domain.CharacterInitialState{
			Location: "值班室", CurrentGoal: "核对凭据", Pressure: "期限临近", KnownFacts: []string{"持有柜钥匙"},
		}},
		{Name: "重复事实角色", InitialState: &domain.CharacterInitialState{
			Location: "机修棚", CurrentGoal: "检查船舶", Pressure: "不能省略检查", KnownFacts: []string{"检查未完成", "检查未完成"},
		}},
	}})
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); !errors.Is(err, errs.ErrToolArgs) || !strings.Contains(err.Error(), "characters[1]") {
		t.Fatalf("invalid batch lacked field-specific rejection: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(st.Dir(), "characters.json"))
	if err != nil || string(after) != string(before) || len(st.Checkpoints.All()) != 0 {
		t.Fatalf("invalid batch partially overwrote characters or checkpointed: %s err=%v", after, err)
	}
}

func TestSaveFoundationSchemaExplainsOpeningStateWithoutFutureFallback(t *testing.T) {
	raw, _ := json.Marshal(NewSaveFoundationTool(store.NewStore(t.TempDir())).Schema())
	for _, word := range []string{"initial_state", "current_goal", "current_action", "known_facts", "commitments", "未来", "core_event"} {
		if !strings.Contains(string(raw), word) || !strings.Contains(foundationShapeHint("characters"), word) {
			t.Fatalf("character schema/repair hint omitted %q", word)
		}
	}
}
