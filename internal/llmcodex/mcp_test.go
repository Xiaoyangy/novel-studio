package llmcodex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/voocel/agentcore"
)

func TestMCPInventoryDisablesNamedServersWithQuotedTOMLKeys(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("CODEX_USAGE_ARGS_LOG", logPath)
	model := usageFakeCLIWithMCP(t, `
printf '%s' '[{"name":"audit.server","command":"PRIVATE_COMMAND","env":{"PRIVATE_KEY":"SECRET_CONFIG_VALUE"}},{"name":"quote\"and\\slash","url":"https://private.invalid/SECRET_CONFIG_VALUE"}]'
exit 0
`, `printf '%s' '隔离后输出' > "$out"`)
	response, err := model.Generate(context.Background(), usageMessages("只输出结果"), nil)
	if err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := `mcp_servers={"audit.server"={enabled=false},"quote\"and\\slash"={enabled=false}}`
	if !strings.Contains(string(args), want) {
		t.Fatalf("MCP override did not quote whole names: %s", args)
	}
	if strings.Contains(string(args), "SECRET_CONFIG_VALUE") || strings.Contains(string(args), "PRIVATE_COMMAND") {
		t.Fatal("server config was copied into exec overrides")
	}
	if strings.Contains(string(args), "model_provider=") || strings.Contains(string(args), "--ignore-user-config") {
		t.Fatal("MCP isolation changed provider/auth routing")
	}
	metadata, err := json.Marshal(response.Message.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metadata), "audit.server") || strings.Contains(string(metadata), "SECRET_CONFIG_VALUE") {
		t.Fatal("private inventory entered usage metadata")
	}
	if response.Message.Metadata["codex_exec_calls"] != 1 {
		t.Fatal("read-only inventory was charged as a model call")
	}
}

func TestMCPInventoryIsRefreshedBeforeEveryExec(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "servers.json")
	logPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("CODEX_MCP_FIXTURE", fixture)
	t.Setenv("CODEX_USAGE_ARGS_LOG", logPath)
	model := usageFakeCLIWithMCP(t, "cat \"$CODEX_MCP_FIXTURE\"\nexit 0\n", `printf '%s' '结果' > "$out"`)
	for _, servers := range []string{`[{"name":"first"}]`, `[{"name":"first"},{"name":"new.server"}]`} {
		if err := os.WriteFile(fixture, []byte(servers), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := model.Generate(context.Background(), usageMessages("重复调用"), nil); err != nil {
			t.Fatal(err)
		}
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), `"new.server"={enabled=false}`) {
		t.Fatalf("new MCP was missed by cached inventory: %s", args)
	}
}

func TestMCPInventoryFailuresStopBeforeModelWithoutLeakingConfig(t *testing.T) {
	for name, body := range map[string]string{
		"CLI failure":    "printf 'SECRET_CONFIG_VALUE' >&2\nexit 9\n",
		"malformed JSON": "printf 'SECRET_CONFIG_VALUE'\nexit 0\n",
		"wrong shape":    "printf 'null'\nexit 0\n",
		"missing name":   "printf '[{\"command\":\"SECRET_CONFIG_VALUE\"}]'\nexit 0\n",
		"oversized":      "head -c 1100000 /dev/zero\nexit 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "model-args")
			t.Setenv("CODEX_USAGE_ARGS_LOG", logPath)
			model := usageFakeCLIWithMCP(t, body, "exit 88\n")
			_, err := model.Generate(context.Background(), usageMessages("不能带MCP继续"), nil)
			if err == nil || !strings.Contains(err.Error(), "Codex MCP isolation") || strings.Contains(err.Error(), "SECRET_CONFIG_VALUE") {
				t.Fatalf("unclear or sensitive isolation failure: %v", err)
			}
			if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
				t.Fatalf("model ran after inventory failure: %v", statErr)
			}
			var usage interface {
				LLMUsage() (*agentcore.Usage, string)
			}
			if errors.As(err, &usage) {
				t.Fatal("inventory failure was treated as a model call")
			}
		})
	}
}

func TestMCPInventoryUsesSameIsolatedCwdAsExec(t *testing.T) {
	model := usageFakeCLIWithMCP(t, `
name=$(basename "$PWD")
printf '[{"name":"%s","command":"unused"}]' "$name"
exit 0
`, `
name=$(basename "$PWD")
expected='mcp_servers={"'"$name"'"={enabled=false}}'
case "$all_args" in
  *"$expected"*) printf '%s' '同一隔离目录' > "$out" ;;
  *) exit 19 ;;
esac
`)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			text, err := model.runCodexIsolated(context.Background(), "并行目录隔离", nil, "high")
			if err != nil || text != "同一隔离目录" {
				t.Errorf("inventory cwd crossed execs: %q %v", text, err)
			}
		})
	}
	wg.Wait()
}

func TestMCPInventoryPreservesCancellation(t *testing.T) {
	model := usageFakeCLI(t, "exit 88\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := model.disabledMCPConfig(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was lost: %v", err)
	}
}
