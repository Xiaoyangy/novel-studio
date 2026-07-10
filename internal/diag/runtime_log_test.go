package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestCaptureRuntimeContextRewriteLogs(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := strings.Join([]string{
		`time=2026-07-02T12:00:00 level=WARN msg=上下文重写 module=context agent=writer reason=threshold strategy=store_summary committed=true tokens_before=120000 tokens_after=54000 msgs_before=80 msgs_after=24 compacted=56 kept=24 duration_ms=2`,
		`time=2026-07-02T12:01:00 level=WARN msg=上下文重写 module=context agent=writer reason=threshold strategy=store_summary committed=true tokens_before=118000 tokens_after=53000 msgs_before=78 msgs_after=23 compacted=55 kept=23 duration_ms=2`,
		`time=2026-07-02T12:02:00 level=WARN msg=上下文重写 module=context agent=coordinator reason=circuit_breaker strategy=full_summary committed=false tokens_before=99000 tokens_after=99000 msgs_before=60 msgs_after=60 compacted=0 kept=60 duration_ms=0`,
		`time=2026-07-02T12:03:00 level=WARN msg="正文 sentinel should not be parsed as context" module=story`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(logDir, "headless.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := CaptureRuntime(store.NewStore(dir))
	if len(rc.ContextRewrites) != 2 {
		t.Fatalf("context rewrites = %+v, want 2 aggregated rows", rc.ContextRewrites)
	}
	var writer ContextRewriteStat
	for _, stat := range rc.ContextRewrites {
		if stat.Agent == "writer" {
			writer = stat
		}
	}
	if writer.Count != 2 || writer.LastTokensBefore != 118000 || writer.LastTokensAfter != 53000 || writer.LastCompacted != 55 || writer.LastKept != 23 {
		t.Fatalf("unexpected writer context rewrite stat: %+v", writer)
	}

	out := string(RenderExport(Report{}, rc))
	for _, want := range []string{
		"上下文重写",
		"writer/store_summary reason=threshold committed=true ×2 tokens 118000→53000 compacted=55 kept=23",
		"coordinator/full_summary reason=circuit_breaker committed=false ×1 tokens 99000→99000 compacted=0 kept=60",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("export missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sentinel") {
		t.Fatalf("export leaked unrelated log text:\n%s", out)
	}
}

func TestCaptureRuntimeLogCountsOnlyCurrentRunWindow(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := strings.Join([]string{
		`time=2026-07-02T11:00:00 level=INFO msg=启动 module=boot provider=codex model=gpt-5.6-sol`,
		`time=2026-07-02T11:01:00 level=ERROR msg=stale_failure module=event category=ERROR agent=writer`,
		`time=2026-07-02T11:02:00 level=WARN msg=上下文重写 module=context agent=writer reason=threshold strategy=store_summary committed=true tokens_before=120000 tokens_after=54000 compacted=56 kept=24`,
		`time=2026-07-02T12:00:00 level=INFO msg=启动 module=boot provider=codex model=gpt-5.6-sol`,
		`time=2026-07-02T12:01:00 level=WARN msg=current_warning module=usage agent=coordinator`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(logDir, "headless.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := CaptureRuntime(store.NewStore(dir))
	if rc.LogErrors != 0 || rc.LogWarns != 1 {
		t.Fatalf("log counts = error %d warn %d, want error 0 warn 1", rc.LogErrors, rc.LogWarns)
	}
	if len(rc.ContextRewrites) != 0 {
		t.Fatalf("stale context rewrites should be ignored, got %+v", rc.ContextRewrites)
	}
	out := string(RenderExport(Report{}, rc))
	if strings.Contains(out, "error ×1") || strings.Contains(out, "上下文重写") {
		t.Fatalf("export should not include stale runtime signals:\n%s", out)
	}
	if !strings.Contains(out, "logs/headless.log (当前启动窗口尾部)") {
		t.Fatalf("export should label current run log source:\n%s", out)
	}
}

func TestRuntimeFindingContextCompactionCircuitBreaker(t *testing.T) {
	rc := RuntimeCapture{ContextRewrites: []ContextRewriteStat{
		{Agent: "coordinator", Strategy: "full_summary", Reason: "circuit_breaker", Count: 2},
		{Agent: "writer", Strategy: "store_summary", Reason: "threshold", Count: 3},
	}}
	findings := contextCompactionCircuitBreaker(&rc)
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want 1", findings)
	}
	if findings[0].Rule != "ContextCompactionCircuitBreaker" || !strings.Contains(findings[0].Evidence, "coordinator/full_summary×2") {
		t.Fatalf("unexpected finding: %+v", findings[0])
	}
}
