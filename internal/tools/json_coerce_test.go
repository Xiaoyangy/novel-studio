package tools

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestCoerceJSONShapeChapterPlanFields(t *testing.T) {
	// 复刻 MiniMax 实跑故障现场的四类形状错位。
	type target struct {
		Chapter          int                            `json:"chapter"`
		CausalSimulation domain.ChapterCausalSimulation `json:"causal_simulation"`
	}
	raw := []byte(`{
		"chapter": 1,
		"causal_simulation": {
			"anti_ai_execution_plan": {"risk_signals": {"a":"模板腔","b":"解释感"}},
			"causal_beats": {"b1":{"cause":"欠费","effect":"上门"}},
			"dialogue_scene_blueprints": [{"participants":"江烬"}]
		}
	}`)
	var direct target
	if json.Unmarshal(raw, &direct) == nil {
		t.Fatal("前置假设失败：这些形状本应严格解析失败")
	}
	coerced, changed := coerceJSONShape(raw, reflect.TypeFor[target]())
	if !changed {
		t.Fatal("应发生形状纠正")
	}
	var out target
	if err := json.Unmarshal(coerced, &out); err != nil {
		t.Fatalf("纠正后应可解析: %v\n%s", err, coerced)
	}
	sim := out.CausalSimulation
	if len(sim.AntiAIPlan.RiskSignals) != 2 {
		t.Fatalf("risk_signals 对象→数组失败: %+v", sim.AntiAIPlan.RiskSignals)
	}
	if len(sim.CausalBeats) != 1 || sim.CausalBeats[0].Cause == "" {
		t.Fatalf("causal_beats 对象→数组失败: %+v", sim.CausalBeats)
	}
	if len(sim.DialogueBlueprints) != 1 || len(sim.DialogueBlueprints[0].Participants) != 1 || sim.DialogueBlueprints[0].Participants[0] != "江烬" {
		t.Fatalf("participants 标量→数组失败: %+v", sim.DialogueBlueprints)
	}
}

func TestCoerceJSONShapeLeavesValidUntouched(t *testing.T) {
	type inner struct {
		Tags []string `json:"tags"`
	}
	raw := []byte(`{"tags":["a","b"]}`)
	_, changed := coerceJSONShape(raw, reflect.TypeFor[inner]())
	if changed {
		t.Fatal("合法输入不应被改动")
	}
}

func TestCoerceJSONShapeStringKeysAsContent(t *testing.T) {
	// values 非字符串时，[]string 改取 keys。
	type inner struct {
		Beats []string `json:"beats"`
	}
	raw := []byte(`{"beats":{"抓捕":true,"逃亡":true}}`)
	coerced, changed := coerceJSONShape(raw, reflect.TypeFor[inner]())
	if !changed {
		t.Fatal("应纠正")
	}
	var out inner
	if err := json.Unmarshal(coerced, &out); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(out.Beats) != 2 {
		t.Fatalf("keys 应作为内容: %+v", out.Beats)
	}
}

func TestCoerceSingleStructWrittenAsObject(t *testing.T) {
	// MiniMax 把单个 struct 元素裸写成对象（键=字段名）——应包成单元素数组，
	// 而不是把字段值当数组元素。复刻 environment_state 故障现场。
	type sig struct {
		Place        string `json:"place"`
		VisibleState string `json:"visible_state"`
	}
	type target struct {
		EnvironmentState []sig `json:"environment_state"`
	}
	raw := []byte(`{"environment_state":{"place":"门牌","visible_state":"贴着旧欠费单"}}`)
	if json.Unmarshal(raw, &target{}) == nil {
		t.Fatal("前置假设：本应严格解析失败")
	}
	coerced, changed := coerceJSONShape(raw, reflect.TypeFor[target]())
	if !changed {
		t.Fatal("应纠正")
	}
	var out target
	if err := json.Unmarshal(coerced, &out); err != nil {
		t.Fatalf("纠正后应可解析: %v\n%s", err, coerced)
	}
	if len(out.EnvironmentState) != 1 || out.EnvironmentState[0].Place != "门牌" {
		t.Fatalf("单 struct 对象应包成 1 元素数组: %+v", out.EnvironmentState)
	}
}

func TestCoerceObjectKeyedArrayStillWorks(t *testing.T) {
	// 身份键对象（键非字段名）仍应取 values 转数组。
	type entry struct {
		Character string `json:"character"`
		Weapon    string `json:"weapon"`
	}
	type target struct {
		CharacterKit []entry `json:"character_kit"`
	}
	raw := []byte(`{"character_kit":{"江烬":{"character":"江烬","weapon":"无"},"沈三多":{"character":"沈三多","weapon":"账本"}}}`)
	coerced, changed := coerceJSONShape(raw, reflect.TypeFor[target]())
	if !changed {
		t.Fatal("应纠正")
	}
	var out target
	if err := json.Unmarshal(coerced, &out); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(out.CharacterKit) != 2 {
		t.Fatalf("身份键对象应取 values 成 2 元素: %+v", out.CharacterKit)
	}
}
