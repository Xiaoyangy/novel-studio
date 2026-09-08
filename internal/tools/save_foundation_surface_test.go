package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func surfaceFoundationCharacters(t *testing.T, surfaces any) []domain.Character {
	t.Helper()
	resource := map[string]any{
		"resource_id": "res_7d683751779f4393", "name": "F07封袋及袋外清单",
		"perceived_label": "F07封袋及袋外清单", "unit": "", "actual_amount": nil,
		"access": "exclusive", "perception": map[string]any{"kind": "unknown", "as_of_chapter": 0},
		"readable_facts":       []map[string]string{{"id": "manifest", "text": "袋外列R02一件；袋内内容另受访问限制。"}},
		"inspectable_surfaces": surfaces,
	}
	raw, err := json.Marshal(resource)
	if err != nil {
		t.Fatal(err)
	}
	var initial domain.InitialCharacterResourceV2
	if err := json.Unmarshal(raw, &initial); err != nil {
		t.Fatal(err)
	}
	return []domain.Character{{Name: "值班员", Role: "主角", InitialState: &domain.CharacterInitialState{
		Location: "值班室", CurrentGoal: "核对袋外清单", Pressure: "交接期限临近",
		KnownFacts:       []string{"T−30交接时观察过旧封条；尚未进行本次检查。"},
		ResourceBalances: []domain.InitialCharacterResourceV2{initial},
	}}}
}

func TestSaveFoundationSurfaceDeclarationPreservesSameResourceAndHistoricalKnowledge(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	characters := surfaceFoundationCharacters(t, []string{"container_exterior", "seal_exterior"})
	args, _ := json.Marshal(map[string]any{"type": "characters", "content": characters})
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	got, err := st.Characters.Load()
	if err != nil || !reflect.DeepEqual(got, characters) {
		t.Fatalf("surface declaration changed character facts: %v", err)
	}
	raw, _ := json.Marshal(got)
	var saved []map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	resources := saved[0]["initial_state"].(map[string]any)["resource_balances"].([]any)
	resource := resources[0].(map[string]any)
	if len(resources) != 1 || resource["resource_id"] != "res_7d683751779f4393" ||
		!reflect.DeepEqual(resource["inspectable_surfaces"], []any{"container_exterior", "seal_exterior"}) ||
		len(resource["readable_facts"].([]any)) != 1 {
		t.Fatalf("surface capability lost or resource/manifest cloned: %s", raw)
	}
	for _, forbidden := range []string{"no_visible_damage", "visible_damage", "operational_observations"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("source declaration manufactured current observation: %s", forbidden)
		}
	}
}

func TestSaveFoundationRejectsInvalidSurfaceBeforeReplacingCharacters(t *testing.T) {
	for _, surfaces := range [][]string{{"intact"}, {"seal_exterior", "seal_exterior"}, {"hidden_contents"}} {
		t.Run(strings.Join(surfaces, "/"), func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			if err := st.Characters.Save([]domain.Character{{Name: "原角色"}}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(st.Dir(), "characters.json")
			before, _ := os.ReadFile(path)
			args, _ := json.Marshal(map[string]any{"type": "characters", "content": surfaceFoundationCharacters(t, surfaces)})
			if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err == nil {
				t.Fatal("invalid surface declaration was saved")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) || len(st.Checkpoints.All()) != 0 {
				t.Fatalf("invalid declaration changed saved source or checkpoints: %v", err)
			}
		})
	}
}

func TestSaveFoundationSurfaceHintDoesNotPromiseAnObservation(t *testing.T) {
	raw, _ := json.Marshal(NewSaveFoundationTool(store.NewStore(t.TempDir())).Schema())
	for _, value := range []string{"inspectable_surfaces", "container_exterior", "seal_exterior", "不填当前", "不删清单", "不能证明从未开启"} {
		if !strings.Contains(string(raw), value) || !strings.Contains(foundationShapeHint("characters"), value) {
			t.Fatalf("surface authoring schema/repair hint omitted %q", value)
		}
	}
}

func TestSaveFoundationRejectsSurfaceStateObjectWithoutOverwritingSource(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "原角色"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "characters.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	args := []byte(`{"type":"characters","content":[{"name":"值班员","initial_state":{"location":"值班室","current_goal":"检查","pressure":"临近交接","known_facts":["上次交接已完成"],"resource_balances":[{"resource_id":"res_7d683751779f4393","name":"F07封袋","unit":"","actual_amount":null,"access":"exclusive","perception":{"kind":"unknown","as_of_chapter":0},"inspectable_surfaces":[{"surface":"seal_exterior","current_state":"intact"}]}]}}]}`)
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err == nil {
		t.Fatal("surface state object was silently dropped or accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) || len(st.Checkpoints.All()) != 0 {
		t.Fatalf("invalid surface state changed source or checkpoints: %v", err)
	}
}
