package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func loadPipelineDetailWindow(st *store.Store, arc pipelineArcScope, base int, configs ...bootstrap.Config) (*domain.PlanningDetailWindowV1, error) {
	// Absence preserves historical generations and their exact full-arc source
	// digest. Once this project has rehearsals, missing/stale reports are an
	// explicit prerequisite failure, not permission to fall back silently.
	if _, err := os.Stat(filepath.Join(st.Dir(), store.ArcRehearsalRoot)); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	input, err := buildPipelineArcRehearsalInput(st, configs...)
	if err != nil {
		return nil, err
	}
	report, err := st.LoadVerifiedArcRehearsalForInput(input.InputDigest)
	if err != nil {
		return nil, err
	}
	if report == nil || !report.ReadyForDetail {
		return nil, fmt.Errorf("project-all 缺少当前正史/资料对应的整弧预演；先执行 --stages rehearse-arc")
	}
	window := &domain.PlanningDetailWindowV1{
		Version:         domain.PlanningDetailWindowVersionV1,
		ArcID:           domain.DeriveArcCycleID(arc.Volume, arc.Arc, arc.FirstChapter, arc.LastChapter),
		ArcFirstChapter: arc.FirstChapter, ArcLastChapter: arc.LastChapter,
		RehearsalDigest: report.ReportDigest, RehearsalInputDigest: input.InputDigest,
	}
	if base+1 == arc.FirstChapter {
		return window, nil
	}
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, fmt.Errorf("弧内下一窗口缺少已接受的前驱 generation")
	}
	previousID := active.GenerationID
	activeGeneration, err := projected.LoadSealedGeneration(previousID)
	if err != nil {
		return nil, err
	}
	if activeGeneration != nil && activeGeneration.BaseCanonChapter == base {
		previousID = activeGeneration.ParentGenerationID
	}
	boundary, err := projected.LoadAcceptedPlanningWindowBoundaryV1(previousID)
	if err != nil {
		return nil, fmt.Errorf("弧内上一窗口尚未完整验收: %w", err)
	}
	if boundary == nil || boundary.Generation.DetailWindow == nil || boundary.Generation.ScopeID != window.ArcID || boundary.LastBundle.Chapter != base {
		return nil, fmt.Errorf("弧内窗口必须紧接同一逻辑弧已验收的末章 %d", base)
	}
	window.AcceptedPredecessor = &boundary.Predecessor
	window.AcceptedOutcomeDigest = boundary.LastOutcome.ReceiptDigest
	return window, nil
}
