package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/voocel/agentcore"
)

func UsageProcessIdentity(pid int) (string, bool, error) {
	if pid <= 0 {
		return "", false, fmt.Errorf("invalid usage owner process id")
	}
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return "", false, nil
	}
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	raw, err := cmd.Output()
	if err != nil {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return "", false, nil
		}
		return "", true, fmt.Errorf("cannot establish usage owner birth identity")
	}
	stamp := strings.Join(strings.Fields(string(raw)), " ")
	if stamp == "" {
		return "", true, fmt.Errorf("usage owner birth identity is empty")
	}
	sum := sha256.Sum256([]byte(stamp))
	return "sha256:" + hex.EncodeToString(sum[:]), true, nil
}

func (m *DurableUsageMeter) RecoverInterruptedCalls(identity func(int) (string, bool, error)) error {
	if identity == nil {
		identity = UsageProcessIdentity
	}
	if err := m.Flush(); err != nil {
		return err
	}
	type owner struct {
		token string
		alive bool
		err   error
	}
	owners := map[int]owner{}
	for id, call := range m.PendingCalls() {
		current, ok := owners[call.ProcessID]
		if !ok {
			current.token, current.alive, current.err = identity(call.ProcessID)
			owners[call.ProcessID] = current
		}
		if current.err != nil {
			return fmt.Errorf("unfinished usage call %s has unverified process identity; no new provider call will start: %w", id, current.err)
		}
		if current.alive {
			if call.ProcessStart == "" || current.token == "" {
				return fmt.Errorf("unfinished usage call %s cannot verify whether its PID was reused; no new provider call will start", id)
			}
			if current.token == call.ProcessStart {
				continue
			}
		}
		if err := m.Record(id, call.Agent, agentcore.Message{Role: agentcore.RoleAssistant, Usage: &agentcore.Usage{}, Metadata: map[string]any{"usage_audit_id": id, "generation_id": call.GenerationID, "codex_usage_source": "unknown", "usage_accounting_gap": "direct provider attempt lost its owner before a usage receipt was persisted; tokens and price remain unknown", "codex_usage_breakdown": []*agentcore.Usage{nil}, "codex_usage_breakdown_complete": true}}); err != nil {
			return err
		}
	}
	return nil
}
