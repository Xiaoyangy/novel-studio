package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

var pipelineArcRehearsalRunner = agents.RunArcRehearsal

// A rehearsal is an author-side conditional forecast for the whole logical
// arc, never a character decision, formal chapter plan, or canonical event.
func buildPipelineArcRehearsalInput(st *store.Store, configs ...bootstrap.Config) (domain.ArcRehearsalInput, error) {
	var input domain.ArcRehearsalInput
	if st == nil {
		return input, fmt.Errorf("rehearse-arc requires a store")
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return input, err
	}
	if progress == nil || len(progress.PendingRewrites) != 0 {
		return input, fmt.Errorf("rehearse-arc requires a stable accepted canon prefix")
	}
	base := progress.LatestCompleted()
	arc, err := locatePipelineArcScope(st, base+1)
	if err != nil {
		return input, err
	}
	canonRoot, err := pipelineProjectAllLiveCanonRoot(st.Dir(), progress)
	if err != nil {
		return input, err
	}
	foundationRoot, err := pipelineProjectAllFoundationSnapshotRoot(st.Dir())
	if err != nil {
		return input, err
	}
	ragRoot, err := pipelineProjectAllRAGSnapshotRoot(st.Dir())
	if err != nil {
		return input, err
	}
	input.ArcID = domain.DeriveArcCycleID(arc.Volume, arc.Arc, arc.FirstChapter, arc.LastChapter)
	input.ArcFirstChapter, input.ArcLastChapter = arc.FirstChapter, arc.LastChapter
	input.BaseCanonChapter, input.BaseCanonRoot = base, canonRoot
	input.SourceRoot, err = domain.ComputeArcRehearsalSourceRootV1(foundationRoot, ragRoot)
	if err != nil {
		return input, err
	}
	return agents.BuildArcRehearsalInput(st, input, configs...)
}

func pipelineRehearseArc(opts cliOptions, flags pipelineFlags) (returnErr error) {
	_, releaseControl, err := acquirePublishedOutlineAllStageForInvocation(opts)
	if err != nil {
		return fmt.Errorf("rehearse-arc requires published outline-all: %w", err)
	}
	defer releasePublishedOutlineAllStage(releaseControl, "rehearse-arc", &returnErr)
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if err := pipelineRequirePrewritingReady(cfg.OutputDir); err != nil {
		return err
	}
	st := store.NewStore(cfg.OutputDir)
	if err := requireNoPendingSealedSteer(st, "rehearse-arc"); err != nil {
		return err
	}
	if err := requirePipelineProjectAllRAGSnapshot(st); err != nil {
		return err
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return err
	}
	if progress == nil {
		return fmt.Errorf("rehearse-arc 缺少正史进度")
	}
	next := progress.LatestCompleted() + 1
	owner := pipelineExecutionOwner("rehearse-arc", next)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionProjectAll, TargetChapter: next, Owner: owner,
		ExpiresAt: time.Now().UTC().Add(pipelineProjectAllLease),
	}); err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, st.Runtime.ReleasePipelineExecution(owner)) }()
	input, err := buildPipelineArcRehearsalInput(st, cfg)
	if err != nil {
		return err
	}
	if input.BaseCanonChapter+1 != next {
		return fmt.Errorf("rehearse-arc 获取执行锁期间正史进度发生漂移")
	}
	if (flags.Start > 0 && flags.Start != input.ArcFirstChapter) || (flags.End > 0 && flags.End != input.ArcLastChapter) {
		return fmt.Errorf("rehearse-arc 必须覆盖整弧 %d..%d，不能用章节范围缩短预演", input.ArcFirstChapter, input.ArcLastChapter)
	}
	if report, err := st.LoadVerifiedArcRehearsalForInput(input.InputDigest); err != nil {
		return err
	} else if report != nil {
		return requirePipelineArcRehearsalReady(report)
	}
	// No Coordinator round or tools with canonical write authority are needed.
	// Keep the configured Architect/Arbiter routes and the durable usage meter.
	cfg.DisableLiveRAG = true
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return err
	}
	accounting, err := newPipelineProjectAllAccounting(context.Background(), cfg, st, st, "arc_rehearsal_"+strings.TrimPrefix(input.InputDigest, "sha256:"))
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, accounting.close()) }()
	fmt.Fprintf(os.Stderr, "[pipeline:rehearse-arc] 整弧 %d..%d：Architect 条件预演 → World Arbiter 复核；不写正文或正史\n", input.ArcFirstChapter, input.ArcLastChapter)
	report, err := pipelineArcRehearsalRunner(accounting.ctx, cfg, models, st, input, accounting.hooks())
	if err != nil {
		return err
	}
	current, err := buildPipelineArcRehearsalInput(st, cfg)
	if err != nil {
		return err
	}
	if current.InputDigest != input.InputDigest {
		return fmt.Errorf("rehearse-arc 输入在模型执行期间发生漂移；保留历史报告，但不授权细推")
	}
	return requirePipelineArcRehearsalReady(report)
}

func requirePipelineArcRehearsalReady(report *domain.ArcRehearsalReport) error {
	if report == nil {
		return fmt.Errorf("rehearse-arc 未产出已复核报告")
	}
	if !report.ReadyForDetail {
		return fmt.Errorf("整弧预演发现未解决的前提或可达性问题；请修复报告 %s 指出的来源，再进入详细规划", report.ReportDigest)
	}
	fmt.Fprintf(os.Stderr, "[pipeline:rehearse-arc] 整弧条件预演已复核：%s；仅授权后续独立细推，不代表事件已经发生\n", report.ReportDigest)
	return nil
}

func verifyPipelineArcRehearsalStage(outputDir string, evidence domain.PipelineStageEvidence, configs ...bootstrap.Config) (domain.PipelineStageEvidence, error) {
	st := store.NewStore(outputDir)
	var report *domain.ArcRehearsalReport
	if len(configs) == 1 {
		var err error
		report, err = loadPipelineBuildingArcRehearsalForRecovery(st, configs[0])
		if err != nil {
			return evidence, err
		}
	}
	var inputErr error
	if report == nil {
		var input domain.ArcRehearsalInput
		input, inputErr = buildPipelineArcRehearsalInput(st, configs...)
		if inputErr == nil {
			var err error
			report, err = st.LoadVerifiedArcRehearsalForInput(input.InputDigest)
			if err != nil {
				return evidence, err
			}
		}
	}
	if report == nil {
		// Accepted chapters legitimately advance canon inside a sealed window.
		// Verify that window's immutable rehearsal instead of launching a new
		// forecast halfway through its realization (or after the book ends).
		active, err := st.ProjectedV2().LoadActiveGeneration()
		if err != nil {
			return evidence, err
		}
		if active != nil {
			generation, err := st.ProjectedV2().LoadSealedGeneration(active.GenerationID)
			if err != nil {
				return evidence, err
			}
			progress, err := st.Progress.Load()
			if err != nil {
				return evidence, err
			}
			if generation != nil && generation.DetailWindow != nil && progress != nil && len(progress.PendingRewrites) == 0 && progress.LatestCompleted() >= generation.BaseCanonChapter &&
				(progress.LatestCompleted() < generation.LastProjectedChapter || (progress.LatestCompleted() == generation.LastProjectedChapter && generation.LastProjectedChapter == generation.BookHorizonChapter)) {
				report, _, err = st.LoadVerifiedArcRehearsal(generation.DetailWindow.RehearsalDigest)
				if err != nil {
					return evidence, err
				}
			}
		}
		if report == nil && inputErr != nil {
			return evidence, inputErr
		}
	}
	if err := requirePipelineArcRehearsalReady(report); err != nil {
		return evidence, err
	}
	evidence.Message = "verified speculative whole-arc rehearsal " + report.ReportDigest
	for _, artifact := range []struct{ kind, digest string }{
		{"inputs", report.InputDigest}, {"drafts", report.DraftDigest}, {"reports", report.ReportDigest},
	} {
		evidence.Artifacts = append(evidence.Artifacts, filepath.ToSlash(filepath.Join(store.ArcRehearsalRoot, artifact.kind, strings.TrimPrefix(artifact.digest, "sha256:")+".json")))
	}
	return evidence, nil
}

// A policy upgrade changes fresh rehearsal inputs, not a building generation's
// already frozen window. Reuse only its exact verified identity/source/report;
// an arbitrary old report or directory is never recovery authority.
func loadPipelineBuildingArcRehearsalForRecovery(st *store.Store, cfg bootstrap.Config) (*domain.ArcRehearsalReport, error) {
	if filepath.Clean(cfg.OutputDir) != filepath.Clean(st.Dir()) {
		return nil, fmt.Errorf("rehearsal recovery configuration belongs to another store")
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return nil, err
	}
	if progress == nil || progress.LatestCompleted() >= progress.TotalChapters {
		return nil, nil
	}
	arc, err := locatePipelineArcScope(st, progress.LatestCompleted()+1)
	if err != nil {
		return nil, err
	}
	candidate, err := pipelineExistingAttemptBeforeDetailWindow(st, progress.LatestCompleted(), arc)
	if err != nil {
		return nil, err
	}
	if candidate == nil || candidate.Status != domain.PlanningGenerationBuildingV2 || candidate.DetailWindow == nil {
		return nil, nil
	}
	if len(progress.PendingRewrites) != 0 {
		return nil, fmt.Errorf("building rehearsal recovery requires unchanged accepted canon")
	}
	bundle, _ := assets.LoadWithOverrides(cfg.Style, assets.DefaultPromptOverrideDirs()...)
	identity, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress)
	if err != nil {
		return nil, fmt.Errorf("building rehearsal recovery inputs drifted: %w", err)
	}
	if err := validatePipelineProjectAllGenerationIdentity(*candidate, identity.Generation); err != nil {
		return nil, err
	}
	if err := st.ProjectedV2().ValidatePlanningDetailWindowSourcesV1(*candidate, identity.Source); err != nil {
		return nil, err
	}
	report, input, err := st.LoadVerifiedArcRehearsal(candidate.DetailWindow.RehearsalDigest)
	if err != nil {
		return nil, err
	}
	if report == nil || input == nil || input.InputDigest != candidate.DetailWindow.RehearsalInputDigest {
		return nil, fmt.Errorf("building window lost its exact historical rehearsal input")
	}
	return report, nil
}
