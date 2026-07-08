package tools

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// 反射驱动的 JSON 形状归一化：LLM（尤其 MiniMax 类弱模型）高频把数组写成对象、
// 把单值写成裸标量、把对象写成字符串。这里按目标 Go 类型递归纠正形状，让本来
// 会解析失败的 payload 自愈，避免 writer/architect 陷入"一轮收敛一个字段"的重试死循环。
//
// 只在严格解析失败后作为回退调用（见 decodeChapterPlanArgs），合法输入零开销直通。

var jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()

// coerceJSONShape 按目标类型 t 递归纠正 raw 的形状，返回 (归一化后 JSON, 是否改动)。
// 规则：
//   - slice 目标：收到 object → 按 key 排序取 values 转数组（[]string 优先取字符串值/键）；
//     收到标量 → 包成单元素数组；数组元素逐个递归。
//   - struct 目标：收到 object → 按 json tag 逐字段递归。
//   - string 目标：收到 object/array → 整体 JSON 串化。
//   - map / interface 目标：保持自由，不介入。
//   - 实现 json.Unmarshaler 的类型（time.Time / CharacterPsychProfile 等）自理形状。
//
// 无法安全纠正时原样返回。
func coerceJSONShape(raw json.RawMessage, t reflect.Type) (json.RawMessage, bool) {
	if t == nil {
		return raw, false
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return raw, false
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if reflect.PointerTo(t).Implements(jsonUnmarshalerType) {
		return raw, false
	}
	switch t.Kind() {
	case reflect.Struct:
		return coerceStructShape(trimmed, t)
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 { // []byte / json.RawMessage
			return raw, false
		}
		return coerceSliceShape(trimmed, t)
	case reflect.String:
		if trimmed[0] == '{' || trimmed[0] == '[' {
			if q, err := json.Marshal(string(trimmed)); err == nil {
				return q, true
			}
		}
		return raw, false
	default:
		return raw, false
	}
}

func coerceStructShape(raw json.RawMessage, t reflect.Type) (json.RawMessage, bool) {
	if raw[0] != '{' {
		return raw, false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return raw, false
	}
	fieldType := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // 未导出字段
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			name = f.Name
		}
		fieldType[name] = f.Type
	}
	changed := false
	for k, v := range obj {
		ft, ok := fieldType[k]
		if !ok {
			continue
		}
		if nv, c := coerceJSONShape(v, ft); c {
			obj[k] = nv
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw, false
	}
	return out, true
}

// structElementKeys 判断 obj 是否是"单个 struct 元素被裸写成对象"：elem 解引用后
// 是 struct，且 obj 的键至少有一个命中 elem 的 json 字段名、且没有明显的非字段键。
// 用于把 {"place":..,"visible_state":..} 识别为一个元素而非 values 数组。
func structElementKeys(elem reflect.Type, obj map[string]json.RawMessage) bool {
	for elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct || len(obj) == 0 {
		return false
	}
	fields := make(map[string]struct{}, elem.NumField())
	for i := 0; i < elem.NumField(); i++ {
		f := elem.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			name = f.Name
		}
		fields[name] = struct{}{}
	}
	// 全部键都必须是结构体字段名，才判为"单元素对象"；只要有一个不是字段名
	// （典型是身份键 name），就按 values 数组处理，避免误包。
	hit := false
	for k := range obj {
		if _, ok := fields[k]; !ok {
			return false
		}
		hit = true
	}
	return hit
}

func coerceSliceShape(raw json.RawMessage, t reflect.Type) (json.RawMessage, bool) {
	elem := t.Elem()
	switch raw[0] {
	case '[':
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) != nil {
			return raw, false
		}
		changed := false
		for i, el := range arr {
			if nv, c := coerceJSONShape(el, elem); c {
				arr[i] = nv
				changed = true
			}
		}
		if !changed {
			return raw, false
		}
		out, err := json.Marshal(arr)
		if err != nil {
			return raw, false
		}
		return out, true
	case '{':
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return raw, false
		}
		// 关键歧义：slice 目标收到 object 有两种含义——
		//   (a) 单个 struct 元素被裸写成对象（键=结构体字段名，如 environment_state 写成
		//       {"place":..,"visible_state":..}）→ 应整体包成单元素数组 [obj]；
		//   (b) 数组被写成 {身份键: 元素} 的对象（键=name/身份，如 character_kit 写成
		//       {"江烬":{..}}）→ 应取 values 转数组。
		// 用"对象的键是否落在元素结构体的字段名集合内"来判别：命中即 (a)。
		if structElementKeys(elem, obj) {
			inner, _ := coerceJSONShape(raw, elem) // 递归修元素内部字段
			out, err := json.Marshal([]json.RawMessage{inner})
			if err != nil {
				return raw, false
			}
			return out, true
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		// []string 特例：values 非全字符串时改取 keys（键即内容的常见写法）。
		useKeys := false
		if elem.Kind() == reflect.String {
			for _, k := range keys {
				if v := bytes.TrimSpace(obj[k]); len(v) == 0 || v[0] != '"' {
					useKeys = true
					break
				}
			}
		}
		arr := make([]json.RawMessage, 0, len(keys))
		for _, k := range keys {
			if useKeys {
				if kq, err := json.Marshal(k); err == nil {
					arr = append(arr, kq)
				}
				continue
			}
			nv, _ := coerceJSONShape(obj[k], elem)
			arr = append(arr, nv)
		}
		out, err := json.Marshal(arr)
		if err != nil {
			return raw, false
		}
		return out, true
	default:
		// 裸标量 → 单元素数组。
		nv, _ := coerceJSONShape(raw, elem)
		out, err := json.Marshal([]json.RawMessage{nv})
		if err != nil {
			return raw, false
		}
		return out, true
	}
}
