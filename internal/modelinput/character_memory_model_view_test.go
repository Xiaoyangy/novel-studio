package modelinput

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

func characterMemoryViewJSON(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("unexpected trailing JSON: %v", err)
	}
	return result
}

func characterMemoryViewMarshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func characterMemoryViewFixture(t *testing.T) ScopedReferenceModelView {
	t.Helper()
	shared := strings.Repeat("本人仅记住当时亲眼看到的燃油读数，尚不知道别人的未公开决定", 10)
	special := "emoji😀𠮷 e\u0301 控制\x00\x01\t 引号\" 反斜杠\\ literal $v @ref1 [0]"
	memory := make([]map[string]any, 48)
	for i := range memory {
		// The first two equal texts belong to distinct identities and sources.
		prefix := fmt.Sprintf("记忆%d；", i)
		if i < 2 {
			prefix = "相同文字；"
		}
		memory[i] = map[string]any{
			"id": fmt.Sprintf("memory_%03d", i), "chapter": i + 1,
			"source": fmt.Sprintf("chapter_%03d_source", i+1), "sources": []string{fmt.Sprintf("source_%03d", i), "duplicate", "duplicate"},
			"text":       prefix + shared + "；" + special + ";" + shared + "\r\n" + special + "\n" + shared + "\r" + "结束",
			"confidence": json.Number("0.006423611111111112"), "timing": json.Number("1.2300e+04"),
		}
	}
	input := map[string]any{
		"memory": memory, "large": json.Number("9007199254740993"), "negative_zero": json.Number("-0"),
		"tiny": json.Number("1.000e-09"), "description": shared,
		"known_facts": []map[string]any{{"id": "fact-current", "text": shared}},
		"nested":      map[string]any{"memory": []map[string]any{{"text": shared}}},
	}
	codec, err := NewScopedReferenceCodec(KindCharacterObservation, input)
	if err != nil {
		t.Fatal(err)
	}
	return codec.ModelView()
}

func TestCharacterMemoryModelViewRoundTripPreservesTextNumbersSourcesAndOrder(t *testing.T) {
	view := characterMemoryViewFixture(t)
	before := characterMemoryViewMarshal(t, view)
	bodyBefore := append([]byte(nil), view.Body...)
	wire, encoded, err := EncodeCharacterMemoryModelViewV1(view)
	if err != nil || !encoded {
		t.Fatalf("long repeated private memory was not encoded: encoded=%t err=%v", encoded, err)
	}
	if len(wire)+len(CharacterMemoryModelViewHelpV1) >= len(before) ||
		utf8.RuneCount(wire)+utf8.RuneCountInString(CharacterMemoryModelViewHelpV1) >= utf8.RuneCount(before) {
		t.Fatal("encoding has no net byte and rune benefit after including its model help")
	}
	if !bytes.Equal(bodyBefore, view.Body) || !bytes.Equal(before, characterMemoryViewMarshal(t, view)) {
		t.Fatal("encoding mutated caller-owned scoped input")
	}
	decoded, err := DecodeCharacterMemoryModelViewV1(wire)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Binding != view.Binding || !reflect.DeepEqual(characterMemoryViewJSON(t, decoded.Body), characterMemoryViewJSON(t, view.Body)) {
		t.Fatal("round trip changed complete JSON values, number lexemes, identity, sources or order")
	}
	want := characterMemoryViewJSON(t, view.Body).(map[string]any)
	got := characterMemoryViewJSON(t, decoded.Body).(map[string]any)
	wantMemory, gotMemory := want["memory"].([]any), got["memory"].([]any)
	for i := range wantMemory {
		a, b := wantMemory[i].(map[string]any), gotMemory[i].(map[string]any)
		if !bytes.Equal([]byte(a["text"].(string)), []byte(b["text"].(string))) {
			t.Fatalf("memory[%d].text changed its exact original bytes", i)
		}
		for _, key := range []string{"id", "chapter", "source", "sources"} {
			if !reflect.DeepEqual(a[key], b[key]) {
				t.Fatalf("memory[%d].%s changed", i, key)
			}
		}
	}
	for key, lexeme := range map[string]string{"large": "9007199254740993", "negative_zero": "-0", "tiny": "1.000e-09"} {
		if got[key].(json.Number).String() != lexeme {
			t.Fatalf("%s lost its numerical lexeme", key)
		}
	}
	if gotMemory[0].(map[string]any)["text"] != gotMemory[1].(map[string]any)["text"] ||
		gotMemory[0].(map[string]any)["source"] == gotMemory[1].(map[string]any)["source"] {
		t.Fatal("equal memory text merged distinct source records")
	}
}

func TestCharacterMemoryModelViewOnlyReplacesTopLevelMemoryText(t *testing.T) {
	view := characterMemoryViewFixture(t)
	wire, encoded, err := EncodeCharacterMemoryModelViewV1(view)
	if err != nil || !encoded {
		t.Fatalf("encode fixture: %t %v", encoded, err)
	}
	envelope := characterMemoryViewJSON(t, wire).(map[string]any)
	var keys []string
	for key := range envelope {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"body", "encoding", "shared_text", "source_binding"}) || envelope["encoding"] != CharacterMemoryModelViewPolicyV1 {
		t.Fatalf("unexpected wire envelope: keys=%v encoding=%v", keys, envelope["encoding"])
	}
	if !reflect.DeepEqual(envelope["source_binding"], characterMemoryViewJSON(t, characterMemoryViewMarshal(t, view.Binding))) {
		t.Fatal("wire replaced the original scoped binding")
	}
	shared := envelope["shared_text"].([]any)
	if len(shared) == 0 {
		t.Fatal("encoded wire has no shared text")
	}
	for _, value := range shared {
		if _, ok := value.(string); !ok {
			t.Fatal("shared_text contains a non-string")
		}
	}
	want := characterMemoryViewJSON(t, view.Body).(map[string]any)
	body := envelope["body"].(map[string]any)
	wantMemory, bodyMemory := want["memory"].([]any), body["memory"].([]any)
	if len(wantMemory) != len(bodyMemory) {
		t.Fatal("encoding changed memory record count")
	}
	references := 0
	for i := range bodyMemory {
		wantItem, wireItem := wantMemory[i].(map[string]any), bodyMemory[i].(map[string]any)
		switch value := wireItem["text"].(type) {
		case string:
			if value != wantItem["text"] {
				t.Fatal("literal memory text was rewritten")
			}
		case []any:
			for _, piece := range value {
				switch piece := piece.(type) {
				case string:
				case json.Number:
					index, err := piece.Int64()
					if err != nil || index < 0 || index >= int64(len(shared)) {
						t.Fatalf("encoder produced invalid shared index %q", piece)
					}
					references++
				default:
					t.Fatalf("encoder produced non-literal/non-index piece %T", piece)
				}
			}
		default:
			t.Fatalf("encoded memory text has unsupported type %T", value)
		}
		delete(wantItem, "text")
		delete(wireItem, "text")
	}
	if references == 0 || !reflect.DeepEqual(want, body) {
		t.Fatal("codec did not share text or changed a field outside memory[i].text")
	}
}

func TestCharacterMemoryModelViewNoBenefitFallsBackToExactOriginalJSON(t *testing.T) {
	codec, err := NewScopedReferenceCodec(KindCharacterObservation, map[string]any{
		"memory": []map[string]any{{"id": "only", "chapter": 1, "source": "original", "text": "$v @ref1 [0];😀\r\n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	view := codec.ModelView()
	want := characterMemoryViewMarshal(t, view)
	wire, encoded, err := EncodeCharacterMemoryModelViewV1(view)
	if err != nil || encoded || !bytes.Equal(want, wire) {
		t.Fatalf("no-benefit input did not preserve the exact plain view: encoded=%t err=%v", encoded, err)
	}
	decoded, err := DecodeCharacterMemoryModelViewV1(wire)
	if err != nil || decoded.Binding != view.Binding || !reflect.DeepEqual(characterMemoryViewJSON(t, decoded.Body), characterMemoryViewJSON(t, view.Body)) {
		t.Fatalf("plain fallback did not round trip: %v", err)
	}
}

func TestCharacterMemoryModelViewRejectsNonActorEncoding(t *testing.T) {
	view := characterMemoryViewFixture(t)
	for _, kind := range []ExactAgentPacketKind{KindWorldArbitration, "unknown", ""} {
		view.Binding.Kind = kind
		if _, _, err := EncodeCharacterMemoryModelViewV1(view); err == nil {
			t.Errorf("accepted non-actor kind %q", kind)
		}
	}
}

func TestCharacterMemoryModelViewNeverEmitsUndecodableOversizeFallback(t *testing.T) {
	const limit = 16 * 1024 * 1024
	view := characterMemoryViewFixture(t)
	prefix, suffix := `{"memory":[],"note":"`, `"}`
	view.Body = json.RawMessage(prefix + strings.Repeat("a", limit-len(prefix)-len(suffix)) + suffix)
	wire, _, err := EncodeCharacterMemoryModelViewV1(view)
	if err != nil {
		return // A defensive size rejection is allowed; partial output is not.
	}
	if _, err := DecodeCharacterMemoryModelViewV1(wire); err != nil {
		t.Fatalf("successful encoding produced an undecodable payload: body_bytes=%d wire_bytes=%d err=%v", len(view.Body), len(wire), err)
	}
}

func TestCharacterMemoryModelViewDecoderRejectsMalformedEnvelopesAndIndices(t *testing.T) {
	view := characterMemoryViewFixture(t)
	wire, encoded, err := EncodeCharacterMemoryModelViewV1(view)
	if err != nil || !encoded {
		t.Fatalf("encode fixture: %t %v", encoded, err)
	}
	for _, name := range []string{"unknown-envelope", "wrong-policy", "missing-shared", "non-string-shared", "negative-index", "out-of-range", "fraction-index", "exponent-index", "huge-index", "boolean-piece", "null-piece", "object-piece", "nested-array-piece", "trailing-json"} {
		t.Run(name, func(t *testing.T) {
			envelope := characterMemoryViewJSON(t, wire).(map[string]any)
			item := envelope["body"].(map[string]any)["memory"].([]any)[0].(map[string]any)
			switch name {
			case "unknown-envelope":
				envelope["unknown"] = true
			case "wrong-policy":
				envelope["encoding"] = "unknown.v1"
			case "missing-shared":
				delete(envelope, "shared_text")
			case "non-string-shared":
				envelope["shared_text"] = []any{json.Number("7")}
			case "negative-index":
				item["text"] = []any{json.Number("-1")}
			case "out-of-range":
				item["text"] = []any{len(envelope["shared_text"].([]any))}
			case "fraction-index":
				item["text"] = []any{json.Number("0.5")}
			case "exponent-index":
				item["text"] = []any{json.Number("0e0")}
			case "huge-index":
				item["text"] = []any{json.Number("9223372036854775808")}
			case "boolean-piece":
				item["text"] = []any{true}
			case "null-piece":
				item["text"] = []any{nil}
			case "object-piece":
				item["text"] = []any{map[string]any{"$v": 0}}
			case "nested-array-piece":
				item["text"] = []any{[]any{0}}
			}
			malformed := characterMemoryViewMarshal(t, envelope)
			if name == "trailing-json" {
				malformed = append(malformed, []byte(" {}")...)
			}
			if _, err := DecodeCharacterMemoryModelViewV1(malformed); err == nil {
				t.Fatal("decoder accepted malformed wire")
			}
		})
	}
}
