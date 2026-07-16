package domain

import "encoding/json"

// CommitStage 表示章节提交 Saga 的当前阶段。
type CommitStage string

const (
	CommitStageStarted        CommitStage = "started"
	CommitStageStateApplied   CommitStage = "state_applied"
	CommitStageProgressMarked CommitStage = "progress_marked"
	CommitStageQualityChecked CommitStage = "quality_checked"
	CommitStageCheckpointed   CommitStage = "checkpointed"
	CommitStageRAGIndexed     CommitStage = "rag_indexed"
	CommitStageSignalSaved    CommitStage = "signal_saved"
)

// CommitMode distinguishes a first-time chapter commit from an overwrite of an
// already completed chapter.  Recovery must not infer this from Progress: a
// rewrite may already have drained PendingRewrites when the process stops.
type CommitMode string

const (
	CommitModeInitial CommitMode = "initial"
	CommitModeRewrite CommitMode = "rewrite"
)

// PendingCommit 记录章节提交中断时的恢复信息。
type PendingCommit struct {
	Chapter        int           `json:"chapter"`
	Mode           CommitMode    `json:"mode,omitempty"`
	Stage          CommitStage   `json:"stage"`
	Summary        string        `json:"summary,omitempty"`
	HookType       string        `json:"hook_type,omitempty"`
	DominantStrand string        `json:"dominant_strand,omitempty"`
	Result         *CommitResult `json:"result,omitempty"`

	// Payload is the canonical, parsed commit input captured before the first
	// irreversible write. Recovery consumes this snapshot instead of whichever
	// arguments happen to be supplied by a later tool call.
	Payload       json.RawMessage `json:"payload,omitempty"`
	PayloadSHA256 string          `json:"payload_sha256,omitempty"`

	// The following identity fields bind recovery to the exact prose and causal
	// evidence that passed the pre-write gates. A changed draft, plan epoch, body
	// epoch, or consistency proof must fail closed rather than silently commit a
	// different candidate.
	BodySHA256                  string `json:"body_sha256,omitempty"`
	WordCount                   int    `json:"word_count,omitempty"`
	PlanCheckpointSeq           int64  `json:"plan_checkpoint_seq,omitempty"`
	PlanCheckpointDigest        string `json:"plan_checkpoint_digest,omitempty"`
	BodyCheckpointSeq           int64  `json:"body_checkpoint_seq,omitempty"`
	BodyCheckpointDigest        string `json:"body_checkpoint_digest,omitempty"`
	ConsistencyCheckpointSeq    int64  `json:"consistency_checkpoint_seq,omitempty"`
	ConsistencyCheckpointDigest string `json:"consistency_checkpoint_digest,omitempty"`
	ExternalBodySHA256          string `json:"external_body_sha256,omitempty"`
	StrictAIGC                  bool   `json:"strict_aigc,omitempty"`

	// A rewrite overwrites the only final artifact. Retaining the previous bytes
	// until the saga finishes keeps revision metrics reproducible after restart.
	PreviousFinalBody   string `json:"previous_final_body,omitempty"`
	PreviousFinalSHA256 string `json:"previous_final_sha256,omitempty"`
	RewriteFlow         string `json:"rewrite_flow,omitempty"`
	RAGIndexed          bool   `json:"rag_indexed,omitempty"`
	RAGError            string `json:"rag_error,omitempty"`

	StartedAt string `json:"started_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}
