package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Host-only publication after the complete chapter loop has closed. It never
// invents another arbitration, calls a model, or advances planning on a partial
// cycle. A retry repairs only a missing checkpoint for the exact saved result.
func PublishCharacterActivationSimulation(ctx context.Context, st *store.Store, generation string, chapter int, sources []string) (*domain.ChapterWorldSimulation, *domain.Checkpoint, error) {
	if err := guardActivationPublicationExecution(ctx, st, chapter); err != nil {
		return nil, nil, err
	}
	var simulation *domain.ChapterWorldSimulation
	var checkpoint *domain.Checkpoint
	err := st.WithCharacterActivationExecution(ctx, generation, chapter, func() error {
		var err error
		simulation, checkpoint, err = publishCharacterActivationSimulation(ctx, st, generation, chapter, sources)
		return err
	})
	return simulation, checkpoint, err
}

func publishCharacterActivationSimulation(ctx context.Context, st *store.Store, generation string, chapter int, sources []string) (*domain.ChapterWorldSimulation, *domain.Checkpoint, error) {
	if err := guardActivationPublicationExecution(ctx, st, chapter); err != nil {
		return nil, nil, err
	}
	evidence, err := st.LoadCharacterActivationChapterEvidence(generation, chapter)
	if err != nil {
		return nil, nil, err
	}
	if evidence == nil {
		return nil, nil, fmt.Errorf("activation publication requires complete ready chapter evidence")
	}
	state, token, err := loadProjectAllStateForExecution(st, chapter)
	if err != nil {
		return nil, nil, err
	}
	if token != "" {
		if state == nil || state.GenerationID != generation || state.ContextDigest != evidence.Context.ProjectionContextDigest {
			return nil, nil, fmt.Errorf("activation publication differs from its exact projected chapter context")
		}
	} else if evidence.Context.ProjectionContextDigest != "" {
		return nil, nil, fmt.Errorf("activation publication lost its original project-all execution context")
	}
	authorization, err := st.LoadCharacterActivationPublication(generation, chapter)
	if err != nil {
		return nil, nil, err
	}
	current, err := st.LoadChapterWorldSimulation(chapter)
	if err != nil {
		return nil, nil, err
	}
	if current != nil {
		if token != "" && (authorization == nil || authorization.SimulationID != current.SimulationID) {
			return nil, nil, fmt.Errorf("saved activation simulation lacks durable publication authorization")
		}
		if err := domain.ValidateCharacterActivationSimulation(*current, *evidence); err != nil {
			return nil, nil, err
		}
		if token != "" && !projectAllStateSourcesContain(current.Sources, token) {
			return nil, nil, fmt.Errorf("published activation lacks its exact projected context token")
		}
		// A different Store may already have published and planned this chapter.
		// Never infer missing work from this instance's checkpoint cache, or erase
		// a partial whose ownership/post-publication age cannot be established.
		checkpoint, err := activationPublicationCheckpoint(st, chapter)
		return current, checkpoint, err
	}
	tickID := ""
	if authorization != nil {
		tickID, sources = authorization.BaseTickID, authorization.Sources
	} else if tick, err := st.WorldSim.LoadTick(); err != nil {
		return nil, nil, err
	} else if tick != nil {
		tickID = tick.TickID
	}
	simulation, err := domain.BuildCharacterActivationSimulation(*evidence, tickID, sources)
	if err != nil {
		return nil, nil, err
	}
	// Check the exact bytes that Store.SaveChapterWorldSimulation writes before
	// consuming an access receipt or touching simulation/partial files. A missing
	// file does not mean its causal checkpoint or later Planner work never existed.
	expectedBytes, err := json.MarshalIndent(simulation, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	existingCP, err := activationPublicationCheckpointPreflight(st, chapter, expectedBytes)
	if err != nil {
		return nil, nil, err
	}
	recovery := authorization != nil || existingCP != nil
	if token != "" && existingCP != nil && authorization == nil {
		return nil, nil, fmt.Errorf("checkpointed activation simulation lacks durable publication authorization")
	}
	if err := validateStoredCharacterAgentProtocol(st, simulation); err != nil {
		return nil, nil, err
	}
	if token != "" {
		if !projectAllStateSourcesContain(simulation.Sources, token) {
			return nil, nil, fmt.Errorf("activation publication differs from its exact projected chapter context")
		}
		if authorization == nil {
			if err := consumePlanningContextAccessReceipt(st, chapter, domain.PlanningContextAccessSimulate, simulation.Sources); err != nil {
				return nil, nil, err
			}
			access, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessSimulate)
			if err != nil {
				return nil, nil, err
			}
			if access == nil {
				return nil, nil, fmt.Errorf("activation publication consumed access receipt disappeared")
			}
			if err := st.SaveCharacterActivationPublication(store.CharacterActivationPublication{
				Version: "character-activation-publication.v1", GenerationID: generation, Chapter: chapter,
				EvidenceDigest: evidence.Digest, SimulationID: simulation.SimulationID,
				BaseTickID: tickID, Sources: simulation.Sources, Access: *access,
			}); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := guardActivationPublicationExecution(ctx, st, chapter); err != nil {
		return nil, nil, err
	}
	if err := st.SaveChapterWorldSimulation(simulation); err != nil {
		return nil, nil, err
	}
	if !recovery {
		if err := st.Drafts.DeleteChapterPlanPartial(chapter); err != nil {
			return nil, nil, err
		}
		if err := st.DeleteChapterWorldSimulationPartial(chapter); err != nil {
			return nil, nil, err
		}
	}
	checkpoint, err := activationPublicationCheckpoint(st, chapter)
	return &simulation, checkpoint, err
}

func activationPublicationCheckpoint(st *store.Store, chapter int) (*domain.Checkpoint, error) {
	artifact := fmt.Sprintf("meta/chapter_simulations/%03d.json", chapter)
	raw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(artifact)))
	if err != nil {
		return nil, err
	}
	current, err := activationPublicationCheckpointPreflight(st, chapter, raw)
	if err != nil {
		return nil, err
	}
	if current != nil {
		// Refresh the caller's cache through the append store's locked disk
		// reconciliation, using only the digest already verified above. Plain
		// Append is history-idempotent even after later plan/review entries.
		return st.Checkpoints.Append(domain.ChapterScope(chapter), "chapter_world_simulation", current.Artifact, current.Digest)
	}
	// This immutable publication is a single causal event. A retry must never
	// append another simulation epoch after later plan/review checkpoints.
	// Append refreshes this Store's cache from disk under checkpointProcessMu
	// before dedup/sequence allocation. Keep that refreshed cache on the caller
	// so the immediately following Planner can verify the published checkpoint.
	return st.Checkpoints.AppendArtifact(domain.ChapterScope(chapter), "chapter_world_simulation", artifact)
}

func activationPublicationCheckpointPreflight(st *store.Store, chapter int, expectedBytes []byte) (*domain.Checkpoint, error) {
	journal, err := st.Checkpoints.AllStrict()
	if err != nil {
		return nil, err
	}
	var current *domain.Checkpoint
	hasPlan := false
	for _, cp := range journal {
		if !cp.Scope.Matches(domain.ChapterScope(chapter)) {
			continue
		}
		if cp.Step == "chapter_world_simulation" {
			copy := cp
			current = &copy
		}
		hasPlan = hasPlan || cp.Step == "plan"
	}
	artifact := fmt.Sprintf("meta/chapter_simulations/%03d.json", chapter)
	if current != nil {
		if current.Artifact != artifact || current.Digest != fmt.Sprintf("sha256:%x", sha256.Sum256(expectedBytes)) {
			return nil, fmt.Errorf("activation publication checkpoint differs from the saved simulation")
		}
		return current, nil
	}
	if hasPlan {
		return nil, fmt.Errorf("activation publication cannot repair a missing simulation checkpoint after a plan")
	}
	return nil, nil
}

// Publication is a planning mutation even when replaying a ready evidence
// bundle. Do not borrow another phase's lease or silently treat a stale lease
// as the unrestricted standalone path.
func guardActivationPublicationExecution(ctx context.Context, st *store.Store, chapter int) error {
	if ctx == nil || st == nil || chapter <= 0 {
		return fmt.Errorf("activation publication requires context, store and positive chapter")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := st.Runtime.ValidatePipelineRenderCandidateEvidenceTree(); err != nil {
		return err
	}
	lock, err := st.Runtime.InspectPipelineExecution()
	if err != nil {
		return err
	}
	if lock == nil {
		if _, err := os.Lstat(filepath.Join(st.Dir(), "meta/runtime/pipeline_execution.json")); err == nil {
			return fmt.Errorf("activation publication found an inactive execution lease; reacquire the same chapter planning lease")
		} else if !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := requireCurrentPipelineExecutionProcess(lock, "activation publication"); err != nil {
		return err
	}
	if lock.Mode != domain.PipelineExecutionProjectAll || lock.TargetChapter != chapter {
		return fmt.Errorf("activation publication requires this process's same-chapter project_all lease, not mode=%s chapter=%d", lock.Mode, lock.TargetChapter)
	}
	return nil
}
