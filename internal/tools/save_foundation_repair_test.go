package tools

import (
	"encoding/json"
	"strings"
	"testing"
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
