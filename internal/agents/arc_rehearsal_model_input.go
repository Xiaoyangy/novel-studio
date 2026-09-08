package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const arcRehearsalSharedValueKey = "$v"

// BuildArcRehearsalModelPayload interns repeated JSON subtrees without losing
// a single source value. Only the transport view changes: canonical packets,
// signatures, source files and submitted resource IDs remain untouched.
func BuildArcRehearsalModelPayload(input domain.ArcRehearsalInput, draft *domain.ArcRehearsalDraft) ([]byte, error) {
	raw, err := json.Marshal(map[string]any{"input": input, "architect_draft": draft})
	if err != nil {
		return nil, err
	}
	return compactArcRehearsalJSON(raw)
}

func compactArcRehearsalJSON(raw []byte) ([]byte, error) {
	var root any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	root, fieldNames := compactArcRehearsalFieldNames(root)
	counts := map[string]int{}
	keyOf := func(value any) string {
		encoded, _ := json.Marshal(value)
		if utf8.RuneCount(encoded) < 48 {
			return ""
		}
		return string(encoded)
	}
	var count func(any)
	count = func(value any) {
		if key := keyOf(value); key != "" {
			counts[key]++
		}
		switch value := value.(type) {
		case map[string]any:
			for _, child := range value {
				count(child)
			}
		case []any:
			for _, child := range value {
				count(child)
			}
		}
	}
	count(root)
	values := map[string]any{}
	ids := map[string]string{}
	var replace func(any) any
	replace = func(value any) any {
		key := keyOf(value)
		literalMarker := false
		if object, ok := value.(map[string]any); ok && len(object) == 1 {
			_, literalMarker = object[arcRehearsalSharedValueKey]
		}
		if (key != "" && counts[key] > 1) || literalMarker {
			if key == "" {
				encoded, _ := json.Marshal(value)
				key = string(encoded)
			}
			id := ids[key]
			if id == "" {
				id = fmt.Sprintf("v%04d", len(values)+1)
				ids[key], values[id] = id, value // Dictionary entries are literal, not recursively encoded.
			}
			return map[string]any{arcRehearsalSharedValueKey: id}
		}
		switch value := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			out := make(map[string]any, len(value))
			for _, key := range keys {
				out[key] = replace(value[key])
			}
			return out
		case []any:
			out := make([]any, len(value))
			for i, child := range value {
				out[i] = replace(child)
			}
			return out
		default:
			return value
		}
	}
	encoded := replace(root)
	compact, err := json.Marshal(struct {
		Encoding string            `json:"encoding"`
		Fields   map[string]string `json:"field_names"`
		Values   map[string]any    `json:"shared_values"`
		Payload  any               `json:"payload"`
	}{"arc-rehearsal-lossless-json.v1", fieldNames, values, encoded})
	if err != nil {
		return nil, err
	}
	if len(compact) >= len(raw) || utf8.RuneCount(compact) >= utf8.RuneCount(raw) {
		return append([]byte(nil), raw...), nil
	}
	return compact, nil
}

// Repeated structural labels also consume the provider's input budget. Alias
// only labels with a positive estimated saving, and never collide with an
// original key. This is reversible independently of value interning.
func compactArcRehearsalFieldNames(root any) (any, map[string]string) {
	counts := map[string]int{}
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				counts[key]++
				visit(child)
			}
		case []any:
			for _, child := range value {
				visit(child)
			}
		}
	}
	visit(root)
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	aliases, fieldNames := map[string]string{}, map[string]string{}
	next := 0
	for _, key := range keys {
		if key == arcRehearsalSharedValueKey || counts[key] < 3 || utf8.RuneCountInString(key) < 9 {
			continue
		}
		alias := ""
		for alias == "" || counts[alias] != 0 {
			next++
			alias = fmt.Sprintf("k%d", next)
		}
		if counts[key]*(utf8.RuneCountInString(key)-len(alias)) <= len(key)+len(alias)+8 {
			continue
		}
		aliases[key], fieldNames[alias] = alias, key
	}
	var rename func(any) any
	rename = func(value any) any {
		switch value := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(value))
			for key, child := range value {
				renamed := key
				if alias := aliases[key]; alias != "" {
					renamed = alias
				}
				out[renamed] = rename(child)
			}
			return out
		case []any:
			out := make([]any, len(value))
			for i, child := range value {
				out[i] = rename(child)
			}
			return out
		default:
			return value
		}
	}
	return rename(root), fieldNames
}
