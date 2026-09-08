package modelinput

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unicode/utf8"
)

func TestScopedReferencesRealObservationsReadOnly(t *testing.T) {
	root := os.Getenv("NOVEL_SCOPED_REFS_FIXTURE_WORK")
	if root == "" {
		t.Skip("set NOVEL_SCOPED_REFS_FIXTURE_WORK for read-only actual observation replay")
	}
	paths, err := filepath.Glob(filepath.Join(root, "*", "proof", "observations", "round-01", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no real observation files: %v", err)
	}
	var originalRunes, viewRunes int
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		codec, err := NewScopedReferenceCodec(KindCharacterObservation, json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		view := codec.ModelView()
		if err := codec.ValidateModelArguments(view.Body, view.Binding); err != nil {
			t.Fatalf("valid model view literals/references were rejected: %v", err)
		}
		expanded, err := codec.ExpandArguments(view.Body, view.Binding)
		if err != nil {
			t.Fatal(err)
		}
		original, _ := referenceJSON(raw)
		restored, _ := referenceJSON(expanded)
		if !reflect.DeepEqual(original, restored) {
			t.Fatalf("round trip changed real observation %s", filepath.Base(path))
		}
		compact, _ := json.Marshal(json.RawMessage(raw))
		encoded, _ := json.Marshal(view) // Include policy/source/view envelope overhead.
		originalRunes += utf8.RuneCount(compact)
		viewRunes += utf8.RuneCount(encoded)
		after, err := os.ReadFile(path)
		if err != nil || string(raw) != string(after) {
			t.Fatal("read-only replay changed a source file")
		}
	}
	t.Logf("actual_observations=%d compact_source_runes=%d model_view_runes_with_envelope=%d delta_runes=%d (not provider tokens)", len(paths), originalRunes, viewRunes, originalRunes-viewRunes)
}
