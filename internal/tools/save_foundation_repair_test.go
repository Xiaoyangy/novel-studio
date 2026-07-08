package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestRepairLooseJSON(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		want    string
		changed bool
	}{
		{"数组尾逗号", `[{"a":1}, ]`, `[{"a":1} ]`, true},
		{"对象尾逗号", `{"a":1,
}`, "{\"a\":1\n}", true},
		{"字符串内裸换行", "{\"a\":\"x\ny\"}", `{"a":"x\ny"}`, true},
		{"字符串内的逗号括号不受影响", `{"a":", ]", "b":1}`, `{"a":", ]", "b":1}`, false},
		{"合法输入原样返回", `{"a":[1,2],"b":"c"}`, `{"a":[1,2],"b":"c"}`, false},
		{"转义引号后仍在字符串内", `{"a":"he said \"hi\"
"}`, `{"a":"he said \"hi\"\n"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := repairLooseJSON(tc.src)
			if got != tc.want || changed != tc.changed {
				t.Fatalf("repairLooseJSON(%q) = (%q, %v)，want (%q, %v)", tc.src, got, changed, tc.want, tc.changed)
			}
			if changed && !json.Valid([]byte(got)) {
				t.Fatalf("修复后仍非法 JSON: %q", got)
			}
		})
	}
}

func TestDecodeFoundationJSONRepairFallback(t *testing.T) {
	// 尾逗号 + 裸换行的混合失误应被静默修复。
	src := "[{\"name\":\"江烬\",\"description\":\"前风控\n专员\"}, ]"
	var out []struct {
		Name string `json:"name"`
	}
	if err := decodeFoundationJSON("characters", src, &out); err != nil {
		t.Fatalf("应被修复回退接住: %v", err)
	}
	if len(out) != 1 || out[0].Name != "江烬" {
		t.Fatalf("修复后解析结果不对: %+v", out)
	}
	// 无法无歧义修复的（缺冒号）仍要报错，且带行列提示。
	var m map[string]any
	err := decodeFoundationJSON("book_world", `{"a" 1}`, &m)
	if err == nil || !strings.Contains(err.Error(), "line") {
		t.Fatalf("不可修复输入应报行列错误, got: %v", err)
	}
}

func TestDecodeFoundationJSONTypeErrorHint(t *testing.T) {
	var out struct {
		Values map[string]float64 `json:"values"`
	}
	err := decodeFoundationJSON("characters", `{"values":["a","b"]}`, &out)
	if err == nil || !strings.Contains(err.Error(), "期望 JSON 对象") {
		t.Fatalf("类型错误应给字段级形状提示, got: %v", err)
	}
}

func TestNormalizeWorldCodexContent(t *testing.T) {
	// MiniMax 实跑故障现场的三类形状：对象当数组、字符串当数组、对象当字符串。
	src := `{
		"ability_tiers": {"夜租新客": {"order":1,"magnitude":"能签短约","limits":"碰不了债权","promotion":"完成首笔确权","constraints":"不得越级交易"}},
		"sections": {"world_morphology": {"content":"雾北市分七区","rules":["冥雾夜涨"]}, "power_structure": "阴司银行在顶层"},
		"races": [{"name":"人类","description":"活人","constraints":"必须缴夜租"}],
		"immutability_policy": {"rule":"改动须有证据"}
	}`
	out := normalizeWorldCodexContent(src)
	var codex struct {
		AbilityTiers []struct {
			Name        string   `json:"name"`
			Order       int      `json:"order"`
			Constraints []string `json:"constraints"`
		} `json:"ability_tiers"`
		Sections []struct {
			Key     string   `json:"key"`
			Content string   `json:"content"`
			Rules   []string `json:"rules"`
		} `json:"sections"`
		Races []struct {
			Constraints []string `json:"constraints"`
		} `json:"races"`
		ImmutabilityPolicy string `json:"immutability_policy"`
	}
	if err := json.Unmarshal([]byte(out), &codex); err != nil {
		t.Fatalf("归一化后应可严格解析: %v\n%s", err, out)
	}
	if len(codex.AbilityTiers) != 1 || codex.AbilityTiers[0].Name != "夜租新客" || codex.AbilityTiers[0].Order != 1 {
		t.Fatalf("ability_tiers 对象应转数组并注入 name: %+v", codex.AbilityTiers)
	}
	if len(codex.AbilityTiers[0].Constraints) != 1 {
		t.Fatalf("元素内 constraints 字符串应转数组: %+v", codex.AbilityTiers[0])
	}
	if len(codex.Sections) != 2 {
		t.Fatalf("sections 对象应转数组: %+v", codex.Sections)
	}
	byKey := map[string]string{}
	for _, s := range codex.Sections {
		byKey[s.Key] = s.Content
	}
	if byKey["world_morphology"] != "雾北市分七区" || byKey["power_structure"] != "阴司银行在顶层" {
		t.Fatalf("sections 键应注入 key，纯文本值应落 content: %v", byKey)
	}
	if len(codex.Races) != 1 || len(codex.Races[0].Constraints) != 1 {
		t.Fatalf("races[].constraints 字符串应转数组: %+v", codex.Races)
	}
	if codex.ImmutabilityPolicy == "" {
		t.Fatalf("immutability_policy 对象应转字符串")
	}
	// 已是约定形状的输入不应被改动。
	good := `{"ability_tiers":[{"name":"a","order":1}],"sections":[{"key":"k","content":"c"}]}`
	if got := normalizeWorldCodexContent(good); got != good {
		t.Fatalf("合规输入不应改动:\n%s", got)
	}
}

func TestMergeWorldCodexDraft(t *testing.T) {
	base := &domain.WorldCodex{
		Sections: []domain.CodexSection{{Key: "world_morphology", Content: "旧"}, {Key: "law_order", Content: "律"}},
		Races:    []domain.CodexRace{{Name: "人类"}},
	}
	in := &domain.WorldCodex{
		AbilityTiers:       []domain.CodexAbilityTier{{Name: "夜租新客"}},
		Sections:           []domain.CodexSection{{Key: "world_morphology", Content: "新"}, {Key: "economy_currency", Content: "币"}},
		ImmutabilityPolicy: "改动须证据",
	}
	out := mergeWorldCodex(base, in)
	if len(out.AbilityTiers) != 1 || len(out.Races) != 1 {
		t.Fatalf("整块字段应按非空覆盖/保留: %+v", out)
	}
	if len(out.Sections) != 3 {
		t.Fatalf("sections 应按 key 合并成 3 条: %+v", out.Sections)
	}
	byKey := map[string]string{}
	for _, s := range out.Sections {
		byKey[s.Key] = s.Content
	}
	if byKey["world_morphology"] != "新" || byKey["law_order"] != "律" || byKey["economy_currency"] != "币" {
		t.Fatalf("同 key 应被来者覆盖、旧 key 保留、新 key 追加: %v", byKey)
	}
	if out.ImmutabilityPolicy != "改动须证据" {
		t.Fatalf("policy 未合并")
	}
}
