package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const checkpointsFile = "meta/checkpoints.jsonl"

// Stores created by Host, pipeline review and delivery can coexist in one
// process. Their IO mutexes are instance-local, so checkpoint appends need one
// process-wide critical section before refreshing the append-only journal.
var checkpointProcessMu sync.Mutex

// CheckpointStore 管理 step 级 checkpoint 的追加与查询。
// 磁盘格式：meta/checkpoints.jsonl，只追加；查询走内存镜像。
// 不变量：cache 是 checkpoints.jsonl 的镜像，由 Append/Reset 单点维护。
// 并发：cache 受 io.mu 保护，写走 Lock、读走 RLock。
type CheckpointStore struct {
	io     *IO
	seqGen atomic.Int64
	cache  []domain.Checkpoint
}

// NewCheckpointStore 创建 checkpoint 存储，从磁盘一次性加载已有 checkpoint 到 cache。
func NewCheckpointStore(io *IO) *CheckpointStore {
	cs := &CheckpointStore{io: io}
	cs.loadFromDisk()
	return cs
}

// loadFromDisk 一次性把磁盘 jsonl 读进 cache 并恢复 seqGen。
func (cs *CheckpointStore) loadFromDisk() {
	cs.io.mu.Lock()
	defer cs.io.mu.Unlock()

	cs.cache = readCheckpointsFile(cs.io.path(checkpointsFile))
	var maxSeq int64
	for _, cp := range cs.cache {
		if cp.Seq > maxSeq {
			maxSeq = cp.Seq
		}
	}
	cs.seqGen.Store(maxSeq)
}

// Append 追加一条 checkpoint。
// 幂等：相同 Scope + Step + Digest 历史上已存在则跳过写入，直接返回已有记录。
func (cs *CheckpointStore) Append(scope domain.Scope, step, artifact, digest string) (*domain.Checkpoint, error) {
	return cs.append(scope, step, artifact, digest, false, nil)
}

// AppendLatest 只把同 scope+step 的最新记录视为幂等命中。它用于 plan
// 这类定义因果 epoch 的 checkpoint：A→B→A 必须追加新的 A epoch，不能返回
// 历史上的第一个 A；但紧邻重试同一 A 仍应幂等。
func (cs *CheckpointStore) AppendLatest(scope domain.Scope, step, artifact, digest string) (*domain.Checkpoint, error) {
	return cs.append(scope, step, artifact, digest, true, nil)
}

// AppendLatestAcross treats an exact checkpoint as idempotent only when it is
// the latest event in the supplied causal step family. This is required for a
// prose chain such as draft -> edit -> draft: returning an older draft with the
// same digest would leave the current file falsely bound to the intervening
// edit epoch. The emitted checkpoint still keeps its actual step.
func (cs *CheckpointStore) AppendLatestAcross(scope domain.Scope, step, artifact, digest string, causalSteps ...string) (*domain.Checkpoint, error) {
	stepSet := make(map[string]struct{}, len(causalSteps)+1)
	stepSet[step] = struct{}{}
	for _, candidate := range causalSteps {
		if candidate != "" {
			stepSet[candidate] = struct{}{}
		}
	}
	return cs.append(scope, step, artifact, digest, true, stepSet)
}

func (cs *CheckpointStore) append(scope domain.Scope, step, artifact, digest string, latestOnly bool, causalSteps map[string]struct{}) (*domain.Checkpoint, error) {
	checkpointProcessMu.Lock()
	defer checkpointProcessMu.Unlock()
	cs.io.mu.Lock()
	defer cs.io.mu.Unlock()

	// Another Store may have appended since this instance was created. Refresh
	// before idempotence and sequence allocation so stale caches cannot emit
	// duplicate sequence numbers or duplicate artifacts.
	cs.cache = readCheckpointsFile(cs.io.path(checkpointsFile))
	var maxSeq int64
	for _, cp := range cs.cache {
		if cp.Seq > maxSeq {
			maxSeq = cp.Seq
		}
	}
	cs.seqGen.Store(maxSeq)

	if digest != "" {
		for i := len(cs.cache) - 1; i >= 0; i-- {
			cp := cs.cache[i]
			if !cp.Scope.Matches(scope) {
				continue
			}
			if len(causalSteps) > 0 {
				if _, related := causalSteps[cp.Step]; !related {
					continue
				}
				if cp.Step == step && cp.Artifact == artifact && cp.Digest == digest {
					return &cp, nil
				}
				break
			}
			if cp.Step != step {
				continue
			}
			if cp.Digest == digest && (!latestOnly || cp.Artifact == artifact) {
				return &cp, nil
			}
			if latestOnly {
				break
			}
		}
	}

	// seq 写成功后才推进，避免写失败留下永久跳号。
	// 已持 io.mu 写锁，Load+Store 之间不会被并发抢占。
	seq := cs.seqGen.Load() + 1
	cp := domain.Checkpoint{
		Seq:        seq,
		Scope:      scope,
		Step:       step,
		Artifact:   artifact,
		Digest:     digest,
		OccurredAt: time.Now(),
	}

	data, err := json.Marshal(cp)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if err := cs.io.AppendLineUnlocked(checkpointsFile, data); err != nil {
		return nil, err
	}
	cs.seqGen.Store(seq)
	cs.cache = append(cs.cache, cp)
	return &cp, nil
}

// AppendArtifact 计算 artifact 内容指纹后追加 checkpoint。
func (cs *CheckpointStore) AppendArtifact(scope domain.Scope, step, artifact string) (*domain.Checkpoint, error) {
	if artifact == "" {
		return cs.Append(scope, step, "", "")
	}
	data, err := cs.io.ReadFile(artifact)
	if err != nil {
		return nil, fmt.Errorf("digest artifact %s: %w", artifact, err)
	}
	sum := sha256.Sum256(data)
	return cs.Append(scope, step, artifact, "sha256:"+hex.EncodeToString(sum[:]))
}

// AppendArtifactLatest is AppendLatest with a digest calculated from artifact.
// Use it for artifacts whose repeated content still starts a new causal epoch
// after a different version has been checkpointed.
func (cs *CheckpointStore) AppendArtifactLatest(scope domain.Scope, step, artifact string) (*domain.Checkpoint, error) {
	if artifact == "" {
		return cs.AppendLatest(scope, step, "", "")
	}
	data, err := cs.io.ReadFile(artifact)
	if err != nil {
		return nil, fmt.Errorf("digest artifact %s: %w", artifact, err)
	}
	sum := sha256.Sum256(data)
	return cs.AppendLatest(scope, step, artifact, "sha256:"+hex.EncodeToString(sum[:]))
}

// AppendArtifactLatestAcross calculates the current artifact digest and then
// applies AppendLatestAcross causal-family idempotence.
func (cs *CheckpointStore) AppendArtifactLatestAcross(scope domain.Scope, step, artifact string, causalSteps ...string) (*domain.Checkpoint, error) {
	if artifact == "" {
		return cs.AppendLatestAcross(scope, step, "", "", causalSteps...)
	}
	data, err := cs.io.ReadFile(artifact)
	if err != nil {
		return nil, fmt.Errorf("digest artifact %s: %w", artifact, err)
	}
	sum := sha256.Sum256(data)
	return cs.AppendLatestAcross(scope, step, artifact, "sha256:"+hex.EncodeToString(sum[:]), causalSteps...)
}

// Latest 返回指定 scope 的最新 checkpoint。
func (cs *CheckpointStore) Latest(scope domain.Scope) *domain.Checkpoint {
	cs.io.mu.RLock()
	defer cs.io.mu.RUnlock()
	for i := len(cs.cache) - 1; i >= 0; i-- {
		if cs.cache[i].Scope.Matches(scope) {
			cp := cs.cache[i]
			return &cp
		}
	}
	return nil
}

// LatestByStep 返回指定 scope + step 的最新 checkpoint。
func (cs *CheckpointStore) LatestByStep(scope domain.Scope, step string) *domain.Checkpoint {
	cs.io.mu.RLock()
	defer cs.io.mu.RUnlock()
	for i := len(cs.cache) - 1; i >= 0; i-- {
		cp := cs.cache[i]
		if cp.Scope.Matches(scope) && cp.Step == step {
			return &cp
		}
	}
	return nil
}

// LatestGlobal 返回全局最新 checkpoint（不区分 scope）。
func (cs *CheckpointStore) LatestGlobal() *domain.Checkpoint {
	cs.io.mu.RLock()
	defer cs.io.mu.RUnlock()
	if len(cs.cache) == 0 {
		return nil
	}
	cp := cs.cache[len(cs.cache)-1]
	return &cp
}

// All 返回全部 checkpoint 列表副本（按 seq 递增）。
func (cs *CheckpointStore) All() []domain.Checkpoint {
	cs.io.mu.RLock()
	defer cs.io.mu.RUnlock()
	if len(cs.cache) == 0 {
		return nil
	}
	out := make([]domain.Checkpoint, len(cs.cache))
	copy(out, cs.cache)
	return out
}

// Reset 清空 checkpoint 文件与 cache。仅在新建小说时使用。
// 先删文件再清内存：删除失败时保留 cache 与 seqGen，避免内存与磁盘状态错位。
func (cs *CheckpointStore) Reset() error {
	checkpointProcessMu.Lock()
	defer checkpointProcessMu.Unlock()
	cs.io.mu.Lock()
	defer cs.io.mu.Unlock()
	if err := cs.io.RemoveFileUnlocked(checkpointsFile); err != nil {
		return err
	}
	cs.seqGen.Store(0)
	cs.cache = nil
	return nil
}

// readCheckpointsFile 解析 jsonl；跳过格式错误行以容忍尾部截断。
func readCheckpointsFile(path string) []domain.Checkpoint {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var result []domain.Checkpoint
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var cp domain.Checkpoint
		if json.Unmarshal(line, &cp) == nil {
			result = append(result, cp)
		}
	}
	if err := scanner.Err(); err != nil {
		// 行超限/读错误会静默截断恢复链，必须留痕：宁可多恢复也不能漏。
		slog.Warn("checkpoints.jsonl 扫描中断，结果可能不完整", "module", "store", "err", err)
	}
	return result
}
