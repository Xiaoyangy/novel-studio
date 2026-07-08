package tools

import "testing"

func TestDecodeDDGLink(t *testing.T) {
	raw := "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpath%3Fa%3D1&rut=abc"
	if got := decodeDDGLink(raw); got != "https://example.com/path?a=1" {
		t.Fatalf("decodeDDGLink = %q", got)
	}
	if got := decodeDDGLink("https://direct.example.com/x"); got != "https://direct.example.com/x" {
		t.Fatalf("直链应原样返回, got %q", got)
	}
}

func TestCleanHTMLText(t *testing.T) {
	src := "<p>活人\n\n\n<b>夜租</b> &amp; 账本</p><script>x</script>"
	got := cleanHTMLText(src)
	if got != "活人\n夜租 & 账本 x" && got == "" {
		t.Fatalf("cleanHTMLText 意外为空")
	}
	if want := "夜租"; !contains(got, want) {
		t.Fatalf("应包含 %q, got %q", want, got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestGuardWebTarget(t *testing.T) {
	for _, bad := range []string{"http://127.0.0.1:8080/x", "http://localhost/x", "ftp://example.com", "http://169.254.169.254/meta"} {
		if err := guardWebTarget(bad); err == nil {
			t.Fatalf("应拒绝 %s", bad)
		}
	}
}
