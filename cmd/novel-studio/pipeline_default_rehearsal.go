package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

var legacyDefaultPipelineStages = []string{"architect", "outline-all", "zero-init", "preplan", "project-all", "seal", "promote", "render"}

// Read configuration and prompt overrides without loadCfgBundle's home-rule
// initialization or prompt-manifest publication. Rejected recovery is read-only.
func loadPipelineDefaultInputsReadOnly(opts cliOptions) (bootstrap.Config, assets.Bundle, error) {
	if bootstrap.NeedsSetup(opts.ConfigPath) {
		return bootstrap.Config{}, assets.Bundle{}, fmt.Errorf("尚未配置，请先完成配置引导或提供配置文件")
	}
	cfg, err := bootstrap.LoadConfig(opts.ConfigPath)
	if err != nil {
		return cfg, assets.Bundle{}, err
	}
	if err := normalizeOutputAndRAGForInvocation(&cfg, opts.Dir, hasConfiguredRAGQdrantCollection(opts)); err != nil {
		return cfg, assets.Bundle{}, err
	}
	bundle, _ := assets.LoadWithOverrides(cfg.Style, assets.DefaultPromptOverrideDirs()...)
	return cfg, bundle, nil
}

// An existing attempt is selected before the presence of any newer rehearsal
// directory can change its range or identity. IDs/envelopes remain authority;
// directory presence is not permission to upgrade a legacy generation.
func pipelineExistingAttemptBeforeDetailWindow(st *store.Store, base int, arc pipelineArcScope) (*domain.PlanningGenerationV2, error) {
	attempt, err := loadPipelineProjectAllAttemptNonce(st.Dir())
	if err != nil {
		return nil, err
	}
	if successor, err := st.CharacterAgents.LoadCurrentSuccessorPlan(); err != nil {
		return nil, err
	} else if successor != nil && successor.BaseCanonChapter == base && successor.ArcFirstChapter == base+1 && successor.BookLastChapter == arc.BookLastChapter {
		attempt = strings.TrimSpace(attempt + "|" + successor.Digest)
	}
	_, windowLast, err := domain.PlanningDetailWindowRangeV1(base, arc.FirstChapter, arc.LastChapter)
	if err != nil {
		return nil, err
	}
	var found *domain.PlanningGenerationV2
	for _, last := range slices.Compact([]int{arc.LastChapter, windowLast}) {
		candidate, err := pipelineGenerationForActivationAttempt(st, base, base+1, last, attempt)
		if err != nil {
			return nil, err
		}
		if candidate == nil {
			continue
		}
		if candidate.ScopeID != domain.DeriveArcCycleID(arc.Volume, arc.Arc, arc.FirstChapter, arc.LastChapter) || candidate.BookHorizonChapter != arc.BookLastChapter {
			return nil, fmt.Errorf("existing planning attempt differs from the original logical arc")
		}
		if found != nil && found.GenerationID != candidate.GenerationID {
			return nil, fmt.Errorf("existing planning attempt ambiguously contains legacy and windowed generations")
		}
		found = candidate
	}
	return found, nil
}

type pipelineDefaultResume struct {
	Stages         []string
	Generation     *domain.PlanningGenerationV2
	Preserve       bool
	InputDigest    string
	Previous       *domain.PipelineState
	RenderRecovery bool
}

// Only used to reject a false "new project" classification. Existence never
// authorizes recovery; the generation/source validators below still do that.
func pipelineHasDefaultPlanningFootprint(st *store.Store, previous domain.PipelineState) (bool, error) {
	for _, rel := range []string{"meta/planning/v2/active_generation.json", "meta/planning/v2/projection_cursor.json"} {
		if _, err := os.Lstat(filepath.Join(st.Dir(), rel)); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	for _, rel := range []string{"meta/planning/v2/.building", "meta/planning/v2/generations"} {
		entries, err := os.ReadDir(filepath.Join(st.Dir(), rel))
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		if len(entries) > 0 {
			return true, nil
		}
	}
	for _, stage := range []string{"outline-all", "zero-init", "preplan", "project-all", "seal", "promote"} {
		if previous.Done(stage) {
			return true, nil
		}
	}
	return false, nil
}

func preparePipelineDefaultResume(opts cliOptions, flags pipelineFlags, stages []string, prompt string) (*pipelineDefaultResume, error) {
	if flags.InitializeOnly || strings.TrimSpace(flags.Stages) != "" || !slices.Equal(stages, defaultPipelineStages) || flags.Restart || flags.RefreshArchitect {
		return nil, nil
	}
	cfg, bundle, err := loadPipelineDefaultInputsReadOnly(opts)
	if err != nil {
		return nil, err
	}
	return preparePipelineDefaultResumeWithInputs(cfg, bundle, flags, stages, prompt)
}

func preparePipelineDefaultResumeWithInputs(cfg bootstrap.Config, bundle assets.Bundle, flags pipelineFlags, stages []string, prompt string) (*pipelineDefaultResume, error) {
	plan := &pipelineDefaultResume{Stages: append([]string(nil), stages...), InputDigest: pipelineRunInputDigest(cfg, bundle)}
	var previous domain.PipelineState
	if err := readPipelinePlanningJSON(filepath.Join(cfg.OutputDir, "meta/pipeline.json"), &previous); err == nil {
		plan.Previous = &previous
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	st := store.NewStore(cfg.OutputDir)
	progress, err := st.Progress.Load()
	if err != nil {
		return nil, err
	}
	if progress == nil || progress.TotalChapters <= 0 || progress.LatestCompleted() > progress.TotalChapters {
		if exists, err := pipelineHasDefaultPlanningFootprint(st, previous); err != nil {
			return nil, err
		} else if exists {
			return nil, fmt.Errorf("default recovery has original planning evidence but progress is missing or invalid; state was not changed")
		}
		return plan, nil
	}
	base := progress.LatestCompleted()
	// A building generation at the current base takes precedence over its
	// already accepted parent. Lookup also covers a crash before cursor creation.
	if base < progress.TotalChapters {
		arc, err := locatePipelineArcScope(st, base+1)
		if err != nil {
			// A new project's Architect/outline-all have not run yet.
			if exists, footprintErr := pipelineHasDefaultPlanningFootprint(st, previous); footprintErr != nil {
				return nil, footprintErr
			} else if exists {
				return nil, fmt.Errorf("default recovery has original planning evidence but its arc scope is unavailable: %w", err)
			}
			return plan, nil
		}
		candidate, err := pipelineExistingAttemptBeforeDetailWindow(st, base, arc)
		if err != nil {
			return nil, err
		}
		if candidate != nil {
			identity, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress)
			if err != nil {
				return nil, fmt.Errorf("default recovery refused changed building inputs before state mutation: %w", err)
			}
			if err := validatePipelineProjectAllGenerationIdentity(*candidate, identity.Generation); err != nil {
				return nil, fmt.Errorf("default recovery changed the original planning attempt: %w", err)
			}
			plan.Generation, plan.Preserve = candidate, true
			if candidate.Status == domain.PlanningGenerationSealedV2 {
				active, err := st.ProjectedV2().LoadActiveGeneration()
				if err != nil {
					return nil, err
				}
				if active != nil && active.GenerationID == candidate.GenerationID {
					plan.Generation = nil // Verify the active cursor/render boundary below too.
				}
			}
		}
	}
	if plan.Generation == nil {
		active, err := st.ProjectedV2().LoadActiveGeneration()
		if err != nil {
			return nil, err
		}
		if active != nil {
			generation, err := st.ProjectedV2().LoadSealedGeneration(active.GenerationID)
			if err != nil || generation == nil {
				return nil, fmt.Errorf("default recovery requires the original sealed generation: %w", err)
			}
			cursor, err := st.ProjectedV2().LoadRealizationCursor()
			if err != nil || cursor == nil || cursor.ActiveGenerationID != generation.GenerationID {
				return nil, fmt.Errorf("default recovery requires the exact sealed realization cursor: %w", err)
			}
			plan.Generation = generation
			if previous.Stages != nil {
				plan.RenderRecovery, err = splitPipelineRenderRecoveryPending(cfg.OutputDir, &previous)
				if err != nil {
					return nil, err
				}
			}
			if cursor.LastAcceptedChapter != base && !plan.RenderRecovery {
				return nil, fmt.Errorf("default recovery canon does not match its accepted cursor")
			}
			if !plan.RenderRecovery {
				if err := validatePipelineProjectAllLiveCanonForPromotion(cfg.OutputDir, progress, st.ProjectedV2(), cursor, generation); err != nil {
					return nil, fmt.Errorf("default recovery canonical inputs drifted before state mutation: %w", err)
				}
			}
			if cursor.ActivePromotedChapter != 0 || plan.RenderRecovery {
				frozen, _, err := loadAndVerifyPipelineFrozenPlan(cfg.OutputDir)
				if err != nil {
					return nil, err
				}
				if _, err := validatePipelineSealedRenderBinding(st, frozen, plan.RenderRecovery); err != nil {
					return nil, err
				}
				if err := validatePipelineFrozenRenderDependencies(cfg.OutputDir, frozen); err != nil {
					return nil, err
				}
				input := pipelineRunInputDigest(cfg, bundle)
				if frozen.EffectiveStyleProtocol == pipelineRenderCandidateManifestVersion {
					input = pipelineRenderInputDigest(cfg, bundle)
				}
				if frozen.PipelineRunInputDigest != input {
					return nil, fmt.Errorf("default recovery frozen render model/provider/prompt drift; state was not changed")
				}
			}
			plan.Preserve = base < generation.LastProjectedChapter || plan.RenderRecovery
			if !plan.Preserve {
				if generation.DetailWindow != nil && generation.LastProjectedChapter < generation.DetailWindow.ArcLastChapter {
					_, err = st.ProjectedV2().LoadAcceptedPlanningWindowBoundaryV1(generation.GenerationID)
				} else {
					_, err = requirePipelineArcCompletion(st, generation)
				}
				if err != nil {
					plan.Preserve = true
				} // Finish original render/completion recovery first.
			}
			if base == progress.TotalChapters {
				plan.Preserve = true
			} // No chapter N+1 at a terminal book.
			if plan.Preserve {
				if err := validatePipelineSealedGenerationDependencies(cfg, bundle, *generation); err != nil {
					return nil, fmt.Errorf("default recovery refused changed sealed inputs before state mutation: %w", err)
				}
			}
		}
	}
	if plan.Generation == nil || !plan.Preserve {
		return plan, nil
	}
	if plan.Generation.DetailWindow == nil {
		plan.Stages = append([]string(nil), legacyDefaultPipelineStages...)
	}
	if plan.Previous != nil {
		if prompt != "" && prompt != plan.Previous.Prompt {
			return nil, fmt.Errorf("default recovery author prompt changed; original state was not changed")
		}
		identity := pipelineRunIdentityDigest(flags)
		if plan.Previous.RunIdentity != "" && plan.Previous.RunIdentity != identity {
			return nil, fmt.Errorf("default recovery invocation range changed; original state was not changed")
		}
		if plan.Previous.InputDigest != "" && plan.Previous.InputDigest != plan.InputDigest {
			return nil, fmt.Errorf("新默认 stage 图可兼容旧 generation，但原 pipeline input fingerprint 已改变且缺少可核实的旧组件；未修改状态。请恢复该 generation 原模型/provider/prompt/作者输入后，再用原显式 --stages project-all,seal 或 promote,render 恢复；显式恢复仍执行原输入校验，不能用新预演或 restart 绕过漂移")
		}
		for _, stage := range plan.Previous.Completed {
			if plan.RenderRecovery && (stage == "zero-init" || stage == "preplan" || stage == "project-all" || stage == "seal" || stage == "promote") {
				continue // The exact committed frozen-render recovery was verified above.
			}
			if err := verifyStoredPipelineArtifactDigests(cfg.OutputDir, plan.Previous.Evidence[stage]); err != nil {
				return nil, fmt.Errorf("default recovery original %s evidence drifted before state mutation: %w", stage, err)
			}
		}
	}
	// A prior explicit downstream invocation (or a lost pipeline index) must
	// not cause default resume to initialize or preplan an existing generation.
	// Keep its old evidence records, and dispatch only the unfinished original
	// generation's operational stages; no prerequisite completion is invented.
	if plan.Previous == nil || (!slices.Equal(plan.Previous.Stages, legacyDefaultPipelineStages) && !slices.Equal(plan.Previous.Stages, defaultPipelineStages)) {
		plan.Stages = []string{"project-all", "seal", "promote", "render"}
		if plan.Generation.Status == domain.PlanningGenerationSealedV2 {
			plan.Stages = []string{"seal", "promote", "render"}
			if active, err := st.ProjectedV2().LoadActiveGeneration(); err != nil {
				return nil, err
			} else if active != nil && active.GenerationID == plan.Generation.GenerationID {
				plan.Stages = []string{"promote", "render"}
			}
		}
	}
	return plan, nil
}

// This only migrates the coarse index after authenticating the generation.
// Existing receipt bytes, range, source bindings and completed evidence are
// retained. Ordinary explicit stage-list changes keep their old reset behavior.
func loadPipelineStateWithDefaultResume(path string, stages []string, prompt, inputDigest, runIdentity string, restart bool, plan *pipelineDefaultResume) (*domain.PipelineState, error) {
	if plan == nil || plan.Previous == nil || restart {
		return loadOrInitPipelineState(path, stages, prompt, inputDigest, runIdentity, restart)
	}
	previous := plan.Previous
	pureGraphChange := (slices.Equal(previous.Stages, legacyDefaultPipelineStages) || slices.Equal(previous.Stages, defaultPipelineStages) || slices.Equal(previous.Stages, []string{"architect", "outline-all", "zero-init"})) &&
		(previous.InputDigest == "" || previous.InputDigest == inputDigest) && (previous.RunIdentity == "" || previous.RunIdentity == runIdentity) && (prompt == "" || prompt == previous.Prompt)
	if !plan.Preserve && !pureGraphChange && plan.Generation == nil {
		return loadOrInitPipelineState(path, stages, prompt, inputDigest, runIdentity, restart)
	}
	if inputDigest != plan.InputDigest {
		return nil, fmt.Errorf("default invocation input changed after read-only recovery preflight")
	}
	state := *previous
	state.Stages = append([]string(nil), stages...)
	state.Completed = append([]string(nil), previous.Completed...)
	state.Evidence = make(map[string]domain.PipelineStageEvidence, len(previous.Evidence))
	for stage, evidence := range previous.Evidence {
		state.Evidence[stage] = evidence
	}
	state.InputDigest, state.RunIdentity = inputDigest, runIdentity
	if prompt != "" {
		state.Prompt = prompt
	}
	return &state, nil
}
