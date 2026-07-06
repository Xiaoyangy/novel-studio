package tools

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// 本文件承载 Coordinator/Architect 规划路径的上下文组装（从 novel_context_builders.go 拆出）。
func (t *ContextTool) buildArchitectContext(result map[string]any, warn func(string, error)) {
	envelope := newArchitectContextEnvelope()
	result["memory_policy"] = domain.NewArchitectMemoryPolicy()
	t.buildArchitectPlanning(&envelope, warn)
	t.buildArchitectFoundation(&envelope, warn)
	t.buildArchitectReferences(&envelope, warn)
	envelope.apply(result)
}

func (t *ContextTool) buildArchitectPlanning(envelope *architectContextEnvelope, warn func(string, error)) {
	runMeta, err := t.store.RunMeta.Load()
	warn("run_meta", err)
	if runMeta != nil && runMeta.PlanningTier != "" {
		envelope.Planning["planning_tier"] = runMeta.PlanningTier
	}

	t.buildWorldSimulationPlanning(envelope.Planning, warn)

	var layered []domain.VolumeOutline
	if l, err := t.store.Outline.LoadLayeredOutline(); err == nil && len(l) > 0 {
		layered = l
		envelope.Planning["layered_outline"] = layered
		var skeletonArcs []map[string]any
		for _, v := range layered {
			for _, a := range v.Arcs {
				if !a.IsExpanded() {
					skeletonArcs = append(skeletonArcs, map[string]any{
						"volume":             v.Index,
						"arc":                a.Index,
						"title":              a.Title,
						"goal":               a.Goal,
						"estimated_chapters": a.EstimatedChapters,
					})
				}
			}
		}
		if len(skeletonArcs) > 0 {
			envelope.Planning["skeleton_arcs"] = skeletonArcs
		}
	} else {
		warn("layered_outline", err)
	}

	var compass *domain.StoryCompass
	if c, err := t.store.Outline.LoadCompass(); err == nil && c != nil {
		compass = c
		envelope.Planning["compass"] = compass
	} else {
		warn("compass", err)
	}
	if volSummaries, err := t.store.Summaries.LoadAllVolumeSummaries(); err == nil && len(volSummaries) > 0 {
		envelope.Planning["volume_summaries"] = volSummaries
	} else {
		warn("volume_summaries", err)
	}
	if ledger, err := t.store.LoadCharacterContinuityLedger(); err == nil && ledger != nil {
		envelope.Planning["character_continuity"] = compactCharacterContinuitySnapshot(ledger, 0)
	} else {
		warn("character_continuity", err)
	}

	// completion_signals 把"全书是否该结尾"的关键事实集中呈现，
	// 让架构师在裁定 complete_book / append_volume 时一眼看到对照面。
	// 散落在 progress / compass / foreshadow / layered_outline 里靠 LLM 脑算容易漏。
	envelope.Planning["completion_signals"] = t.completionSignals(layered, compass)
	if ragState, err := t.store.RAG.LoadIndexState(); err == nil && ragState != nil {
		envelope.Planning["rag_index_state"] = map[string]any{
			"config":       ragState.Config,
			"chunks":       len(ragState.Chunks),
			"chunk_hashes": len(ragState.ChunkHashes),
			"updated_at":   ragState.UpdatedAt,
		}
	} else {
		warn("rag_index_state", err)
	}
}

func (t *ContextTool) completionSignals(layered []domain.VolumeOutline, compass *domain.StoryCompass) map[string]any {
	signals := map[string]any{}
	if progress, _ := t.store.Progress.Load(); progress != nil {
		signals["completed_chapters"] = len(progress.CompletedChapters)
		signals["total_word_count"] = progress.TotalWordCount
		signals["phase"] = string(progress.Phase)
	}
	if len(layered) > 0 {
		signals["planned_chapters"] = len(domain.FlattenOutline(layered))
		signals["volumes_total"] = len(layered)
	}
	if compass != nil {
		if compass.EstimatedScale != "" {
			signals["compass_estimated_scale"] = compass.EstimatedScale
		}
		signals["open_threads_count"] = len(compass.OpenThreads)
	}
	if active, err := t.store.World.LoadActiveForeshadow(); err == nil {
		signals["active_foreshadow_count"] = len(active)
	}
	return signals
}

func (t *ContextTool) buildArchitectFoundation(envelope *architectContextEnvelope, warn func(string, error)) {
	if premise, err := t.store.Outline.LoadPremise(); err == nil && premise != "" {
		if sections := parsePremiseSections(premise); len(sections) > 0 {
			envelope.Foundation["premise_sections"] = sections
		}
		tier := domain.PlanningTier("")
		if meta, err := t.store.RunMeta.Load(); err == nil && meta != nil {
			tier = meta.PlanningTier
		}
		envelope.Foundation["premise_structure"] = premiseStructure(premise, tier)
	} else {
		warn("premise", err)
	}

	if chars, err := t.store.Characters.Load(); err == nil && chars != nil {
		envelope.Foundation["characters"] = chars
	} else {
		warn("characters", err)
	}

	if snapshots, err := t.store.Characters.LoadLatestSnapshots(); err == nil && len(snapshots) > 0 {
		envelope.Foundation["character_snapshots"] = snapshots
	} else {
		warn("character_snapshots", err)
	}
	if rules, err := t.store.World.LoadWorldRules(); err == nil && len(rules) > 0 {
		envelope.Foundation["world_rules"] = rules
	} else {
		warn("world_rules", err)
	}
	if world, err := t.store.World.LoadBookWorld(); err == nil && world != nil {
		envelope.Foundation["book_world"] = world
	} else {
		warn("book_world", err)
	}
	if foreshadow, err := t.store.World.LoadActiveForeshadow(); err == nil && len(foreshadow) > 0 {
		envelope.Foundation["foreshadow_ledger"] = foreshadow
	} else {
		warn("foreshadow_ledger", err)
	}
	envelope.Foundation["foundation_status"] = t.foundationStatus()
}

func (t *ContextTool) buildArchitectReferences(envelope *architectContextEnvelope, warn func(string, error)) {
	if engine := t.loadWritingEngine(nil, nil, warn); engine != nil {
		envelope.References["writing_engine"] = engine
	} else {
		warn("writing_assets", nil)
	}
	if styleRules, err := t.store.World.LoadStyleRules(); err == nil && styleRules != nil {
		envelope.References["style_rules"] = styleRules
	} else {
		warn("style_rules", err)
	}

	refs := t.architectReferences()
	t.addProjectWebReferenceBrief(refs, warn)
	t.addPrewriteStorycraftPlan(refs, warn)
	t.addWorldBackgroundPlan(refs, warn)
	envelope.References["references"] = refs
}

func (t *ContextTool) addPrewriteStorycraftPlan(refs map[string]string, warn func(string, error)) {
	if refs == nil {
		return
	}
	plan := t.loadPrewriteStorycraftPlan(warn)
	if plan == "" {
		return
	}
	refs["prewrite_storycraft_plan"] = plan
}

func (t *ContextTool) loadPrewriteStorycraftPlan(warn func(string, error)) string {
	for _, rel := range []string{
		filepath.Join("meta", "prewrite_storycraft_plan.md"),
		filepath.Join("meta", "prewrite_storycraft_plan.json"),
		filepath.Join("references", "prewrite_storycraft_plan.md"),
	} {
		data, err := os.ReadFile(filepath.Join(t.store.Dir(), rel))
		if err != nil {
			if !os.IsNotExist(err) {
				warn("prewrite_storycraft_plan:"+rel, err)
			}
			continue
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			return text
		}
	}
	return ""
}

func (t *ContextTool) addWorldBackgroundPlan(refs map[string]string, warn func(string, error)) {
	if refs == nil {
		return
	}
	plan := t.loadWorldBackgroundPlan(warn)
	if plan == "" {
		return
	}
	refs["world_background_plan"] = plan
}

func (t *ContextTool) loadWorldBackgroundPlan(warn func(string, error)) string {
	for _, rel := range []string{
		filepath.Join("meta", "world_background_plan.md"),
		filepath.Join("meta", "world_background_plan.json"),
		filepath.Join("references", "world_background_plan.md"),
	} {
		data, err := os.ReadFile(filepath.Join(t.store.Dir(), rel))
		if err != nil {
			if !os.IsNotExist(err) {
				warn("world_background_plan:"+rel, err)
			}
			continue
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			return text
		}
	}
	return ""
}

func (t *ContextTool) addProjectWebReferenceBrief(refs map[string]string, warn func(string, error)) {
	if refs == nil {
		return
	}
	brief := t.loadProjectWebReferenceBrief(warn)
	if brief == "" {
		return
	}
	refs["web_reference_brief"] = brief
}

func (t *ContextTool) loadProjectWebReferenceBrief(warn func(string, error)) string {
	for _, rel := range []string{
		filepath.Join("meta", "web_reference_brief.md"),
		filepath.Join("meta", "web_reference_brief.json"),
		filepath.Join("references", "web_reference_brief.md"),
	} {
		data, err := os.ReadFile(filepath.Join(t.store.Dir(), rel))
		if err != nil {
			if !os.IsNotExist(err) {
				warn("web_reference_brief:"+rel, err)
			}
			continue
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			return text
		}
	}
	return ""
}
