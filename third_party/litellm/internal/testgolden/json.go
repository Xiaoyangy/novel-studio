package testgolden

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"
)

func AssertJSON(t testing.TB, path string, actual any) {
	t.Helper()
	data, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		t.Fatalf("marshal actual JSON: %v", err)
	}
	AssertJSONBytes(t, path, data)
}

func AssertJSONBytes(t testing.TB, path string, actual []byte) {
	t.Helper()
	expected, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			actualValue := decodeJSON(t, "actual JSON", actual)
			t.Fatalf("read golden %s: %v\nactual:\n%s", path, err, prettyJSON(t, actualValue))
		}
		t.Fatalf("read golden %s: %v", path, err)
	}

	expectedValue := decodeJSON(t, "golden "+path, expected)
	actualValue := decodeJSON(t, "actual JSON", actual)
	if reflect.DeepEqual(expectedValue, actualValue) {
		return
	}

	expectedPretty := prettyJSON(t, expectedValue)
	actualPretty := prettyJSON(t, actualValue)
	t.Fatalf("JSON golden mismatch for %s\nexpected:\n%s\nactual:\n%s", path, expectedPretty, actualPretty)
}

func decodeJSON(t testing.TB, name string, data []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("decode %s: trailing JSON data", name)
	}
	return value
}

func prettyJSON(t testing.TB, value any) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("format JSON: %v", err)
	}
	return string(data)
}
