package llmcodex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	maxCodexMCPInventoryBytes = 1 << 20
	maxCodexMCPServers        = 256
	maxCodexMCPNameBytes      = 512
	codexMCPInventoryTimeout  = 5 * time.Second
)

// disabledMCPConfig reads the effective CLI configuration without connecting to
// MCP servers or calling a model. Empty-table overrides are deep-merged by Codex
// and do not clear existing servers, so every discovered name is disabled.
// Never cache names: a later call may use another cwd or changed configuration.
func (m *CodexModel) disabledMCPConfig(ctx context.Context, cwd string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, codexMCPInventoryTimeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, m.binary, "mcp", "list", "--json", "--disable", "plugins", "--disable", "apps")
	cmd.Dir = cwd
	cmd.WaitDelay = time.Second
	var output codexMCPInventoryBuffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard // Configuration errors can include private commands/env values.
	defer func() { clear(output.data) }()
	if err := cmd.Run(); err != nil {
		if probeCtx.Err() != nil {
			return "", errors.Join(errors.New("Codex MCP isolation: configuration inventory canceled or timed out"), probeCtx.Err())
		}
		return "", errors.New("Codex MCP isolation: configuration inventory failed; no model call was started")
	}
	if output.overflow {
		return "", errors.New("Codex MCP isolation: configuration inventory exceeds the size limit")
	}
	names, err := decodeCodexMCPNames(output.data)
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", nil
	}
	var entries []string
	for _, name := range names {
		// JSON quoting is also valid TOML basic-string quoting and preserves
		// dots, quotes and Unicode as a single table key. Do not concatenate
		// names into dotted override paths or shell command text.
		quoted, _ := json.Marshal(name)
		entries = append(entries, string(quoted)+"={enabled=false}")
	}
	return "mcp_servers={" + strings.Join(entries, ",") + "}", nil
}

func decodeCodexMCPNames(raw []byte) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("Codex MCP isolation: expected a JSON server list")
	}
	var servers []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(trimmed, &servers) != nil || len(servers) > maxCodexMCPServers {
		return nil, errors.New("Codex MCP isolation: invalid or oversized server list")
	}
	seen := map[string]struct{}{}
	names := make([]string, 0, len(servers))
	for _, server := range servers {
		if strings.TrimSpace(server.Name) == "" || len(server.Name) > maxCodexMCPNameBytes {
			return nil, errors.New("Codex MCP isolation: invalid server name")
		}
		if _, exists := seen[server.Name]; exists {
			continue
		}
		seen[server.Name] = struct{}{}
		names = append(names, server.Name)
	}
	sort.Strings(names)
	return names, nil
}

type codexMCPInventoryBuffer struct {
	data     []byte
	overflow bool
}

func (b *codexMCPInventoryBuffer) Write(data []byte) (int, error) {
	n := len(data)
	if b.overflow {
		return n, nil
	}
	remaining := maxCodexMCPInventoryBytes - len(b.data)
	if len(data) > remaining {
		b.overflow = true
		return n, nil
	}
	b.data = append(b.data, data...)
	return n, nil
}
