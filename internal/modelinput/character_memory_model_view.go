package modelinput

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const CharacterMemoryModelViewPolicyV1 = "character-memory-text-shared.v1"

// This help is part of the new model-only transport, not an instruction to
// change character knowledge. Callers must opt in through their new producer.
const CharacterMemoryModelViewHelpV1 = `
当观察包顶层encoding为character-memory-text-shared.v1时，source_binding指向原始scoped观察视图；body仍是你的完整私有观察。只有body.memory各项的text可能是数组：按数组顺序，把字符串原样保留，把整数替换为shared_text中从0开始的对应字符串，再直接拼接，不能补空格、删除标点或换行。普通text字符串保持原义，不进行任何替换；字符串内的$v、@ref、数字或类似引用的字样都只是原文。字典里的内容也不递归解释。每条记忆的ID、章节、来源、接受状态和先后顺序仍各自独立；共享文字不合并事件，不把旧状态改成当前事实，不增加知识或权限。继续使用原工具中的scoped引用。`

// A defensive decoder allocation bound, independent of the model context
// budget. Oversized data fails; it is never shortened or partially decoded.
const characterMemoryViewMaxBytes = 16 * 1024 * 1024

type characterMemoryEnvelopeV1 struct {
	Encoding      string                 `json:"encoding"`
	SourceBinding ScopedReferenceBinding `json:"source_binding"`
	SharedText    []string               `json:"shared_text"`
	Body          json.RawMessage        `json:"body"`
}

// EncodeCharacterMemoryModelViewV1 changes only the representation of complete
// memory text in an already-scoped character view. It does not project, prune,
// authenticate, or mutate source data. The caller retains its original scoped
// codec for tool expansion and must never enable this for a historical producer.
// When the envelope plus help would not save both bytes and runes, the exact
// json.Marshal(view) payload is returned with encoded=false and no help needed.
func EncodeCharacterMemoryModelViewV1(view ScopedReferenceModelView) (payload []byte, encoded bool, err error) {
	if view.Binding.Kind != KindCharacterObservation {
		return nil, false, fmt.Errorf("memory text transport requires a scoped character observation")
	}
	original, err := json.Marshal(view)
	if err != nil {
		return nil, false, err
	}
	if len(original) > characterMemoryViewMaxBytes {
		return nil, false, fmt.Errorf("memory model view exceeds the transport input size limit")
	}
	body, rows, err := characterMemoryBodyV1(view.Body)
	if err != nil {
		return nil, false, err
	}
	counts := map[string]int{}
	var order []string
	texts := make([]string, len(rows))
	for i, row := range rows {
		text, ok := row["text"].(string)
		if !ok {
			return nil, false, fmt.Errorf("memory[%d].text must be an original string", i)
		}
		texts[i] = text
		for _, segment := range characterMemorySegmentsV1(text) {
			if counts[segment] == 0 {
				order = append(order, segment)
			}
			counts[segment]++
		}
	}
	dictionary := []string{}
	indices := map[string]int{}
	for _, segment := range order {
		literal, _ := json.Marshal(segment)
		refBytes := len(strconv.Itoa(len(dictionary)))
		// Include this dictionary entry and its comma. Full envelope/help and
		// per-row array costs are checked again after actual serialization.
		if counts[segment] > 1 && counts[segment]*(len(literal)-refBytes) > len(literal)+1 {
			indices[segment] = len(dictionary)
			dictionary = append(dictionary, segment)
		}
	}
	if len(dictionary) == 0 {
		return original, false, nil
	}
	for i, text := range texts {
		var parts []any
		var literal strings.Builder
		used := false
		flush := func() {
			if literal.Len() > 0 {
				parts = append(parts, literal.String())
				literal.Reset()
			}
		}
		for _, segment := range characterMemorySegmentsV1(text) {
			if index, exists := indices[segment]; exists {
				flush()
				parts = append(parts, index)
				used = true
			} else {
				literal.WriteString(segment)
			}
		}
		flush()
		if used {
			rows[i]["text"] = parts
		}
	}
	encodedBody, err := json.Marshal(body)
	if err != nil {
		return nil, false, err
	}
	payload, err = json.Marshal(characterMemoryEnvelopeV1{CharacterMemoryModelViewPolicyV1, view.Binding, dictionary, encodedBody})
	if err != nil {
		return nil, false, err
	}
	if len(payload)+len(CharacterMemoryModelViewHelpV1) >= len(original) ||
		utf8.RuneCount(payload)+utf8.RuneCountInString(CharacterMemoryModelViewHelpV1) >= utf8.RuneCount(original) {
		return original, false, nil
	}
	return payload, true, nil
}

// DecodeCharacterMemoryModelViewV1 accepts the labelled transport or its plain
// fallback. It reconstructs data only, never verified source authority. Literal
// strings and dictionary entries are never parsed for reference-like syntax.
func DecodeCharacterMemoryModelViewV1(payload []byte) (ScopedReferenceModelView, error) {
	var view ScopedReferenceModelView
	var header map[string]json.RawMessage
	if err := decodeCharacterMemoryJSONV1(payload, &header); err != nil {
		return view, err
	}
	if _, exists := header["encoding"]; !exists {
		if err := decodeCharacterMemoryJSONV1(payload, &view); err != nil {
			return view, err
		}
		if view.Binding.Kind != KindCharacterObservation {
			return view, fmt.Errorf("memory text transport requires a scoped character observation")
		}
		_, rows, err := characterMemoryBodyV1(view.Body)
		if err != nil {
			return view, err
		}
		for i, row := range rows {
			if _, ok := row["text"].(string); !ok {
				return view, fmt.Errorf("plain memory[%d].text must be a string", i)
			}
		}
		return view, nil
	}
	var envelope characterMemoryEnvelopeV1
	if err := decodeCharacterMemoryJSONV1(payload, &envelope); err != nil {
		return view, err
	}
	if envelope.Encoding != CharacterMemoryModelViewPolicyV1 || envelope.SourceBinding.Kind != KindCharacterObservation || len(envelope.SharedText) == 0 {
		return view, fmt.Errorf("invalid character memory transport envelope")
	}
	body, rows, err := characterMemoryBodyV1(envelope.Body)
	if err != nil {
		return view, err
	}
	totalText := 0
	for i, row := range rows {
		var pieces []string
		switch text := row["text"].(type) {
		case string:
			pieces = []string{text}
		case []any:
			if len(text) == 0 {
				return view, fmt.Errorf("encoded memory[%d].text cannot be empty", i)
			}
			for _, part := range text {
				switch part := part.(type) {
				case string:
					pieces = append(pieces, part)
				case json.Number:
					index, err := strconv.Atoi(string(part))
					if err != nil || index < 0 || index >= len(envelope.SharedText) || strconv.Itoa(index) != string(part) {
						return view, fmt.Errorf("memory[%d].text contains an invalid dictionary index", i)
					}
					pieces = append(pieces, envelope.SharedText[index])
				default:
					return view, fmt.Errorf("memory[%d].text accepts only literal strings and dictionary indices", i)
				}
			}
		default:
			return view, fmt.Errorf("memory[%d].text has an invalid transport shape", i)
		}
		for _, piece := range pieces {
			if len(piece) > characterMemoryViewMaxBytes-totalText {
				return view, fmt.Errorf("expanded memory text exceeds the decoding size limit")
			}
			totalText += len(piece)
		}
		row["text"] = strings.Join(pieces, "")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return view, err
	}
	if len(raw) > characterMemoryViewMaxBytes {
		return view, fmt.Errorf("expanded memory view exceeds the decoding size limit")
	}
	return ScopedReferenceModelView{Binding: envelope.SourceBinding, Body: raw}, nil
}

func characterMemoryBodyV1(raw []byte) (map[string]any, []map[string]any, error) {
	var body map[string]any
	if err := decodeCharacterMemoryJSONV1(raw, &body); err != nil {
		return nil, nil, err
	}
	if body == nil {
		return nil, nil, fmt.Errorf("scoped character body must be an object")
	}
	if body["memory"] == nil {
		return body, nil, nil
	}
	memory, ok := body["memory"].([]any)
	if !ok {
		return nil, nil, fmt.Errorf("scoped character memory must be an array")
	}
	rows := make([]map[string]any, len(memory))
	for i, item := range memory {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("memory[%d] must be an object", i)
		}
		rows[i] = row
	}
	return body, rows, nil
}

func characterMemorySegmentsV1(text string) []string {
	var parts []string
	start := 0
	for offset, r := range text {
		if r == '；' || r == ';' || r == '\n' || r == '\r' {
			end := offset + utf8.RuneLen(r)
			parts = append(parts, text[start:end])
			start = end
		}
	}
	if start < len(text) {
		parts = append(parts, text[start:])
	}
	return parts
}

func decodeCharacterMemoryJSONV1(raw []byte, target any) error {
	if len(raw) > characterMemoryViewMaxBytes || !utf8.Valid(raw) {
		return fmt.Errorf("memory model view is oversized or invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("memory model view contains trailing JSON")
	}
	return nil
}
