package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllowsFilter(t *testing.T) {
	if New("", nil).allows("repeat") != true {
		t.Error("events 缺省应全放行")
	}
	n := New("", []string{"run_end", "budget"})
	if !n.allows("run_end") || !n.allows("budget") {
		t.Error("列入的 kind 应放行")
	}
	if n.allows("repeat") {
		t.Error("未列入的 kind 应拦截")
	}
	var nilN *Notifier
	if nilN.allows("run_end") {
		t.Error("nil Notifier 应拦截一切")
	}
	nilN.Send(Notification{Kind: "run_end"}) // 不应 panic
}

func TestCommandChannelEnvAndStdin(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	jsonFile := filepath.Join(dir, "stdin.json")

	n := New(`echo "$NOTIFY_KIND|$NOTIFY_LEVEL|$NOTIFY_TITLE|$NOTIFY_BODY" > `+envFile+` && cat > `+jsonFile, nil)
	nt := Notification{Kind: "budget", Level: "warn", Title: "novel-studio: 预算", Body: "已花费 $8.00"}
	n.deliver(nt) // 同步调用以便断言

	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("command 未执行: %v", err)
	}
	if got := strings.TrimSpace(string(env)); got != "budget|warn|novel-studio: 预算|已花费 $8.00" {
		t.Errorf("环境变量传递不符: %q", got)
	}

	raw, err := os.ReadFile(jsonFile)
	if err != nil {
		t.Fatalf("stdin 未传递: %v", err)
	}
	var decoded Notification
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("stdin 非合法 JSON: %v", err)
	}
	if decoded != nt {
		t.Errorf("stdin JSON 不符: %+v", decoded)
	}
}

func TestCommandChannelTimeoutKill(t *testing.T) {
	n := New("sleep 30", nil)
	n.timeout = 200 * time.Millisecond

	start := time.Now()
	n.deliver(Notification{Kind: "run_end"})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("超时未强杀, 阻塞 %v", elapsed)
	}
}
