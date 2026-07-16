package domain

import "time"

// PipelineExecutionMode is the mechanical boundary between chapter inference
// and prose rendering. The default (no persisted lock) preserves the legacy
// mixed execution path.
type PipelineExecutionMode string

const (
	PipelineExecutionPreplan PipelineExecutionMode = "preplan"
	PipelineExecutionRender  PipelineExecutionMode = "render"
)

// PipelineExecutionLock scopes an execution mode to one chapter. It is
// intentionally leased: a crashed pipeline cannot leave planning or rendering
// permanently disabled.
type PipelineExecutionLock struct {
	Version       int                   `json:"version"`
	Mode          PipelineExecutionMode `json:"mode"`
	TargetChapter int                   `json:"target_chapter"`
	PlanDigest    string                `json:"plan_digest,omitempty"`
	Owner         string                `json:"owner"`
	ProcessID     int                   `json:"process_id,omitempty"`
	AcquiredAt    time.Time             `json:"acquired_at"`
	ExpiresAt     time.Time             `json:"expires_at"`
}

// ActiveAt reports whether the lease is still active at the supplied time.
func (l PipelineExecutionLock) ActiveAt(now time.Time) bool {
	return !l.ExpiresAt.IsZero() && now.Before(l.ExpiresAt)
}
