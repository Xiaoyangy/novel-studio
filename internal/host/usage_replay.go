package host

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/voocel/agentcore"
)

// sessionRecord 是 meta/sessions/*.jsonl 单条记录的轻量解析形态——只取
// 累计 usage 需要的字段。Content 等大字段跳过解析，节省启动期 IO。
//
// 模型归属三级降级：
//  1. Usage.Provider/Model — agentcore/litellm 透传的真实响应模型（首选）
//  2. Meta(_meta)          — 上游未透传时，写入侧由 ModelLookup 补的"当时生效"模型
//  3. 都没有                — replay 退回 effectiveModel 用当前 ModelSet 反推（精度受损）
type sessionRecord struct {
	Role     agentcore.Role     `json:"role"`
	Usage    *agentcore.Usage   `json:"usage,omitempty"`
	Meta     *sessionRecordMeta `json:"_meta,omitempty"`
	Metadata map[string]any     `json:"metadata,omitempty"`
}

type sessionRecordMeta struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

var errUsageReplayIdentityConflict = errors.New("session usage identity conflict")

// ReplaySessions 扫 meta/sessions/coordinator.jsonl 与 meta/sessions/agents/*.jsonl，
// 把每条 assistant 消息的 usage 重新累加到 tracker。返回回填条数。
//
// 调用约束：仅在 meta/usage.json 缺失（首次升级或 schema 变更）时调用一次，做
// 历史数据回填。正常 Host 持久化由 DurableUsageMeter 的 WAL 负责；
// WAL 存在时禁止并行回放会话。
//
// 精度依赖见 sessionRecord 注释的三级降级——第 3 级（Usage 和 _meta 都缺）
// 在更老日志或上游异常时才会触发。
func (t *UsageTracker) ReplaySessions(rootDir string) (int, error) {
	if t == nil {
		return 0, nil
	}
	t.replayedUsageIDs = map[string]string{}
	t.replayLegacyUsage = 0
	sessionsDir := filepath.Join(rootDir, "meta", "sessions")
	info, err := os.Stat(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.IsDir() {
		return 0, nil
	}

	total := 0
	if n, err := t.replayFile(filepath.Join(sessionsDir, "coordinator.jsonl"), "coordinator"); err != nil {
		if errors.Is(err, errUsageReplayIdentityConflict) {
			return total, err
		}
		slog.Warn("replay coordinator session failed", "module", "usage", "err", err)
	} else {
		total += n
	}

	agentsDir := filepath.Join(sessionsDir, "agents")
	walkErr := filepath.WalkDir(agentsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		agentName := parseAgentNameFromFile(name)
		if agentName == "" {
			return nil
		}
		n, fileErr := t.replayFile(path, agentName)
		if fileErr != nil {
			if errors.Is(fileErr, errUsageReplayIdentityConflict) {
				return fileErr
			}
			slog.Warn("replay agent session failed", "module", "usage", "file", name, "err", fileErr)
			return nil
		}
		total += n
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		return total, walkErr
	}
	if t.replayLegacyUsage > 0 {
		t.mu.Lock()
		t.missingAssistantUsage++
		t.mu.Unlock()
		slog.Warn("legacy session usage has no stable call IDs; imported once as an unverified baseline, not a complete bill", "module", "usage", "records", t.replayLegacyUsage)
	}
	return total, nil
}

// replayFile 扫单个 jsonl 文件，把所有带 Usage 的 assistant 消息喂给 accumulate。
// agentName 由调用方传入（coordinator 或文件名解析的 sub-agent 名）。
func (t *UsageTracker) replayFile(path, agentName string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	role := agentRoleName(agentName)
	count := 0
	scanner := bufio.NewScanner(f)
	// 单行可能很长（assistant 消息 + tool args 等都打平了），放宽到 4MB。
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec sessionRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Role != agentcore.RoleAssistant || rec.Usage == nil {
			continue
		}
		provider, modelName := usageActualModel(rec.Usage)
		if rec.Meta != nil {
			if provider == "" {
				provider = rec.Meta.Provider
			}
			if modelName == "" {
				modelName = rec.Meta.Model
			}
		}
		origin := role
		id, _ := rec.Metadata["usage_audit_id"].(string)
		if id != "" {
			if !validUsageAuditLabel(id, 512) {
				return count, fmt.Errorf("%w: invalid call ID", errUsageReplayIdentityConflict)
			}
			if agent, _ := rec.Metadata["usage_audit_agent"].(string); agent != "" {
				origin = agentRoleName(agent)
			}
			metadata, err := sanitizeUsageAuditMetadata(rec.Metadata)
			if err != nil {
				return count, err
			}
			if err := validateUsageAuditUsage(rec.Usage); err != nil {
				return count, fmt.Errorf("%w: invalid numeric receipt: %v", errUsageReplayIdentityConflict, err)
			}
			// Use the exact WAL identity format. Importing a session baseline
			// must also import its dedup IDs, otherwise a forwarded OnMessage
			// after restart could charge those same calls a second time.
			digest := usageAuditIdentityDigest(usageAuditRecord{Version: 1, Kind: "record", ID: id, Agent: origin, Usage: rec.Usage, Metadata: metadata})
			if old, seen := t.replayedUsageIDs[id]; seen {
				if old != digest {
					return count, fmt.Errorf("%w: %s", errUsageReplayIdentityConflict, id)
				}
				continue
			}
			if t.replayedUsageIDs == nil {
				t.replayedUsageIDs = map[string]string{}
			}
			t.replayedUsageIDs[id] = digest
			t.mu.Lock()
			if t.accountedUsageIDs == nil {
				t.accountedUsageIDs = map[string]string{}
			}
			t.accountedUsageIDs[id] = digest
			t.mu.Unlock()
		} else {
			t.replayLegacyUsage++
		}
		t.accumulateMessageUsage(origin, provider, modelName, *rec.Usage, rec.Metadata)
		count++
	}
	if err := scanner.Err(); err != nil {
		return count, fmt.Errorf("scan %s: %w", path, err)
	}
	return count, nil
}

// parseAgentNameFromFile 从 "writer-ch01.jsonl" / "architect_short-001.jsonl" 提取
// agent 名（"-" 之前部分）。命名约定见 store/session.go::subAgentPath：
// agentName 不含 dash，suffix 是 ch<n> 或递增序号。
func parseAgentNameFromFile(name string) string {
	base := strings.TrimSuffix(name, ".jsonl")
	if i := strings.Index(base, "-"); i > 0 {
		return base[:i]
	}
	return ""
}
