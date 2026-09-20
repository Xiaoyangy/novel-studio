package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Called only after the real strict decoder rejected an unknown field. This
// locates that existing error; it is not a second validator or a schema source.
func worldCodexUnknownFieldContext(raw []byte, decodeErr error) string {
	field, err := strconv.Unquote(strings.TrimPrefix(decodeErr.Error(), "json: unknown field "))
	if err != nil {
		return ""
	}
	var walk func(json.RawMessage, reflect.Type, string) string
	walk = func(value json.RawMessage, typ reflect.Type, path string) string {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		unmarshaler := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
		if typ.Implements(unmarshaler) || reflect.PointerTo(typ).Implements(unmarshaler) {
			return ""
		}
		switch typ.Kind() {
		case reflect.Slice, reflect.Array:
			var items []json.RawMessage
			if json.Unmarshal(value, &items) != nil {
				return ""
			}
			for i, item := range items {
				if found := walk(item, typ.Elem(), fmt.Sprintf("%s[%d]", path, i)); found != "" {
					return found
				}
			}
		case reflect.Struct:
			fields := map[string]reflect.Type{}
			var names []string
			for i := 0; i < typ.NumField(); i++ {
				member := typ.Field(i)
				// Codex structs use explicit exported fields. Do not guess future
				// embedded-field precedence or custom JSON decoding semantics.
				if member.Anonymous {
					return ""
				}
				if member.PkgPath != "" {
					continue
				}
				name := strings.Split(member.Tag.Get("json"), ",")[0]
				if name == "-" {
					continue
				}
				if name == "" {
					name = member.Name
				}
				fields[name] = member.Type
				names = append(names, name)
			}
			sort.Strings(names)
			decoder := json.NewDecoder(bytes.NewReader(value))
			opening, err := decoder.Token()
			if err != nil || opening != json.Delim('{') {
				return ""
			}
			// Token order follows the strict decoder, including duplicate keys.
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return ""
				}
				key, ok := keyToken.(string)
				if !ok {
					return ""
				}
				var child json.RawMessage
				if decoder.Decode(&child) != nil {
					return ""
				}
				childType, known := fields[key]
				if !known {
					for _, name := range names {
						if strings.EqualFold(name, key) {
							childType, known = fields[name], true
							break
						}
					}
				}
				childPath := path + "." + key
				if strings.ContainsAny(key, ".[]\"\\/ \t\r\n") {
					childPath = path + "[" + strconv.Quote(key) + "]"
				}
				if !known && key == field {
					return fmt.Sprintf("；JSON 解码路径 %s；本层允许字段：%s", childPath, strings.Join(names, ", "))
				}
				if known {
					if found := walk(child, childType, childPath); found != "" {
						return found
					}
				}
			}
		}
		return ""
	}
	return walk(raw, reflect.TypeOf(domain.WorldCodex{}), "$")
}

// Read the persisted file, never the locally merged (but rejected) submission.
// Counts and a content digest make retry instructions precise without echoing
// author facts. Read/decode failure is uncertainty, not an empty draft claim.
func (t *SaveFoundationTool) worldCodexUnchangedDraftHint() string {
	prefix := "本次提交未保存、未合并草稿；此前正式法典（若有）与已暂存草稿均未改写。"
	raw, err := os.ReadFile(filepath.Join(t.store.Dir(), worldCodexDraftRel))
	if os.IsNotExist(err) {
		return prefix + "当前没有已暂存草稿；请修正并重发本次提交。"
	}
	if err != nil {
		return prefix + "当前草稿状态读取失败，不能确认其已存字段；请先核对草稿。"
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	var draft domain.WorldCodex
	if json.Unmarshal(raw, &draft) != nil {
		return prefix + "当前草稿文件无法解析（" + digest + "），不能把本次内容视为已暂存。"
	}
	return fmt.Sprintf("%s保留的 %s（%s）：ability_tiers=%d, skill_domains=%d, races=%d, weapon_categories=%d, equipment_categories=%d, mechanisms=%d, counterfactual_tests=%d, sections=%d。请修正并重发本次新增/替换块，不能只补失败字段而省略本次其它未存块。",
		prefix, worldCodexDraftRel, digest, len(draft.AbilityTiers), len(draft.SkillDomains), len(draft.Races), len(draft.WeaponCategories), len(draft.EquipmentCategories), len(draft.Mechanisms), len(draft.CounterfactualTests), len(draft.Sections))
}
