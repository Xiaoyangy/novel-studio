package domain

import "time"

// PipelineState is persisted to meta/pipeline.json and records the coarse
// resumable pipeline state. It is a checkpoint index, not the source of truth
// for stage completion; PipelineStageEvidence records the durable proof checked
// before a stage is marked complete.
type PipelineState struct {
	Stages      []string                         `json:"stages"`
	Completed   []string                         `json:"completed"`
	Prompt      string                           `json:"prompt,omitempty"`
	InputDigest string                           `json:"input_digest,omitempty"`
	UpdatedAt   time.Time                        `json:"updated_at"`
	Evidence    map[string]PipelineStageEvidence `json:"evidence,omitempty"`
}

// ChapterRerenderRequest durably binds an explicit whole-chapter rerender to
// the exact plan, draft and user instruction that authorized it. Keeping the
// instruction body beside its digest matters after render-only escalation:
// world simulation, POV planning and prose rendering run in separate agent
// sessions and must all receive the same chapter-scoped contract.
type ChapterRerenderRequest struct {
	Version               int    `json:"version"`
	Chapter               int    `json:"chapter"`
	PlanSHA256            string `json:"plan_sha256"`
	SupersededDraftSHA256 string `json:"superseded_draft_sha256"`
	Instruction           string `json:"instruction,omitempty"`
	InstructionSHA256     string `json:"instruction_sha256,omitempty"`
	Reason                string `json:"reason"`
	RequestedAt           string `json:"requested_at"`
}

// PipelineStageEvidence is the durable proof attached to a completed pipeline
// stage. The pipeline CLI writes this after verifying artifacts/checkpoints.
type PipelineStageEvidence struct {
	Stage             string            `json:"stage"`
	Status            string            `json:"status"`
	CheckedAt         time.Time         `json:"checked_at"`
	Message           string            `json:"message,omitempty"`
	ProgressPhase     string            `json:"progress_phase,omitempty"`
	ProgressFlow      string            `json:"progress_flow,omitempty"`
	CompletedChapters int               `json:"completed_chapters,omitempty"`
	Artifacts         []string          `json:"artifacts,omitempty"`
	ArtifactDigests   map[string]string `json:"artifact_digests,omitempty"`
	Checkpoints       []string          `json:"checkpoints,omitempty"`
	Missing           []string          `json:"missing,omitempty"`
}

func (s *PipelineState) Done(stage string) bool {
	if s == nil {
		return false
	}
	for _, c := range s.Completed {
		if c == stage {
			return true
		}
	}
	return false
}

func (s *PipelineState) MarkDone(stage string, evidence PipelineStageEvidence) {
	if s == nil {
		return
	}
	if !s.Done(stage) {
		s.Completed = append(s.Completed, stage)
	}
	if s.Evidence == nil {
		s.Evidence = make(map[string]PipelineStageEvidence)
	}
	s.Evidence[stage] = evidence
}

func (s *PipelineState) ClearDone(stage string, evidence PipelineStageEvidence) {
	if s == nil {
		return
	}
	filtered := s.Completed[:0]
	for _, c := range s.Completed {
		if c != stage {
			filtered = append(filtered, c)
		}
	}
	s.Completed = filtered
	if s.Evidence == nil {
		s.Evidence = make(map[string]PipelineStageEvidence)
	}
	s.Evidence[stage] = evidence
}
