package testgolden

import (
	"os"
	"testing"
)

func ReadFixture(t testing.TB, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return data
}

func ReadFixtureString(t testing.TB, path string) string {
	t.Helper()
	return string(ReadFixture(t, path))
}
