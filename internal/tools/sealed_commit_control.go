package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// applySealedCommitControlPlane replaces model-authored world/offscreen ledger
// arguments with the exact server-side transition from the promoted bundle.
// The Drafter is intentionally not shown hidden character state, so requiring
// it to reproduce that state would either leak POV secrets or make a legitimate
// render impossible to commit.
func applySealedCommitControlPlane(
	st *store.Store,
	args *commitChapterArgs,
) (bool, error) {
	if st == nil || args == nil || args.Chapter <= 0 {
		return false, nil
	}
	args.SealedControlPlaneDigest = ""
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return false, err
	}
	if lock == nil ||
		lock.Mode != domain.PipelineExecutionRender ||
		lock.TargetChapter != args.Chapter ||
		strings.TrimSpace(lock.PlanDigest) == "" {
		return false, nil
	}
	var frozen struct {
		Chapter               int    `json:"chapter"`
		PlanDigest            string `json:"plan_digest"`
		ProjectionBinding     string `json:"projection_binding"`
		PlanningGenerationID  string `json:"planning_generation_id"`
		ProjectedBundleDigest string `json:"projected_bundle_digest"`
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "planning", "current_frozen_plan.json"))
	if err != nil {
		return false, fmt.Errorf("sealed commit read frozen plan: %w", err)
	}
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return false, fmt.Errorf("sealed commit decode frozen plan: %w", err)
	}
	if frozen.ProjectionBinding != "sealed_v2" {
		return false, nil
	}
	if frozen.Chapter != args.Chapter ||
		strings.TrimSpace(frozen.PlanDigest) != strings.TrimSpace(lock.PlanDigest) ||
		strings.TrimSpace(frozen.PlanningGenerationID) == "" ||
		strings.TrimSpace(frozen.ProjectedBundleDigest) == "" {
		return false, fmt.Errorf("sealed commit frozen plan identity mismatch")
	}
	projected := st.ProjectedV2()
	active, err := projected.LoadActiveGeneration()
	if err != nil || active == nil {
		return false, fmt.Errorf("sealed commit load active generation: %w", err)
	}
	if active.GenerationID != frozen.PlanningGenerationID {
		return false, fmt.Errorf("sealed commit active generation drift")
	}
	bundles, err := projected.LoadProjectedChapterBundles(frozen.PlanningGenerationID)
	if err != nil {
		return false, fmt.Errorf("sealed commit load projected bundles: %w", err)
	}
	var bundle *domain.ProjectedChapterBundle
	for i := range bundles {
		if bundles[i].Chapter == args.Chapter {
			copy := bundles[i]
			bundle = &copy
			break
		}
	}
	if bundle == nil ||
		bundle.BundleDigest != frozen.ProjectedBundleDigest {
		return false, fmt.Errorf("sealed commit exact projected bundle is unavailable")
	}
	if err := domain.ValidateProjectedChapterBundle(*bundle); err != nil {
		return false, fmt.Errorf("sealed commit projected bundle: %w", err)
	}

	args.CharacterStage = sealedCommitCharacterStages(*bundle)
	args.TimelineEvents = sealedCommitTimelineEvents(*bundle)
	args.StateChanges = sealedCommitStateChanges(*bundle)
	args.RelationshipChanges = sealedCommitRelationshipChanges(*bundle)
	args.ResourceUpdates, args.ResourceProposals = sealedCommitResourceChanges(*bundle)
	args.ForeshadowUpdates = sealedCommitForeshadowChanges(*bundle)
	args.SealedControlPlaneDigest = bundle.BundleDigest

	args.Characters = nil
	for _, decision := range bundle.ChapterWorldSimulation.CharacterDecisions {
		args.Characters = appendUniqueCommitString(args.Characters, decision.Character)
	}
	if len(bundle.HardRenderContract.MustOccur) > 0 {
		args.KeyEvents = append([]string(nil), bundle.HardRenderContract.MustOccur...)
	}
	return true, nil
}

func sealedCommitCharacterStages(bundle domain.ProjectedChapterBundle) []domain.CharacterStageRecord {
	byName := make(map[string]domain.CharacterStageRecord)
	for _, record := range bundle.ChapterPlan.CausalSimulation.OffscreenStage {
		record.Chapter = bundle.Chapter
		byName[strings.TrimSpace(record.Character)] = record
	}
	out := make([]domain.CharacterStageRecord, 0, len(bundle.ChapterWorldSimulation.CharacterDecisions))
	for _, decision := range bundle.ChapterWorldSimulation.CharacterDecisions {
		name := strings.TrimSpace(decision.Character)
		if name == "" {
			continue
		}
		record := byName[name]
		record.Chapter = bundle.Chapter
		record.Character = name
		record.Time = fallbackCommitControlText(record.Time, decision.Time, bundle.ChapterWorldSimulation.TimeWindow)
		record.Location = fallbackCommitControlText(decision.Location, record.Location, "原地")
		record.Status = fallbackCommitControlText(decision.StateAfter, record.Status, decision.CompletionState)
		record.Environment = fallbackCommitControlText(record.Environment, decision.Pressure, "本章既定环境压力")
		record.CurrentAction = fallbackCommitControlText(decision.Action, record.CurrentAction, decision.Decision)
		record.Pressure = fallbackCommitControlText(decision.Pressure, record.Pressure, decision.CurrentGoal)
		record.Decision = fallbackCommitControlText(decision.Decision, record.Decision, "维持既定行动")
		record.DecisionReason = fallbackCommitControlText(decision.DecisionReason, record.DecisionReason, decision.Pressure)
		record.ButterflyEffects = sealedCommitButterflyEffects(decision.ButterflyEffects)
		record.KnowledgeBoundary = fallbackCommitControlText(decision.KnowledgeBoundary, record.KnowledgeBoundary, "只知道已取得证据")
		record.VisibleInChapter = decision.VisibleToPOV
		record.Evidence = fallbackCommitControlText(record.Evidence, "sealed world simulation "+bundle.ChapterWorldSimulation.SimulationID)
		record.Transport = fallbackCommitControlText(record.Transport, "按预封存地点与时间窗行动")
		record.TravelTime = fallbackCommitControlText(record.TravelTime, decision.ActionDuration, "未发生跨地点移动")
		record.MeetingConstraint = fallbackCommitControlText(record.MeetingConstraint, "服从预封存时间线与知识边界")
		record.TimelineConsistency = fallbackCommitControlText(record.TimelineConsistency, decision.Time, bundle.ChapterWorldSimulation.TimeWindow)
		record.NextPotential = fallbackCommitControlText(record.NextPotential, decision.ImmediateResult)
		out = append(out, record)
	}
	return out
}

func sealedCommitButterflyEffects(effects []domain.DecisionButterflyEffect) []string {
	out := make([]string, 0, len(effects))
	for _, effect := range effects {
		out = appendUniqueCommitString(out, effect.Effect)
	}
	return out
}

func sealedCommitTimelineEvents(bundle domain.ProjectedChapterBundle) []domain.TimelineEvent {
	characters := make([]string, 0)
	for _, decision := range bundle.ChapterWorldSimulation.CharacterDecisions {
		if decision.VisibleToPOV {
			characters = appendUniqueCommitString(characters, decision.Character)
		}
	}
	var out []domain.TimelineEvent
	for _, mutation := range bundle.ProjectedDelta.Timeline {
		out = append(out, domain.TimelineEvent{
			Chapter:    bundle.Chapter,
			Time:       bundle.ChapterWorldSimulation.TimeWindow,
			Event:      mutation.After,
			Characters: append([]string(nil), characters...),
		})
	}
	return out
}

func sealedCommitStateChanges(bundle domain.ProjectedChapterBundle) []domain.StateChange {
	var out []domain.StateChange
	appendMutations := func(values []domain.StateMutationV2) {
		for _, mutation := range values {
			out = append(out, domain.StateChange{
				Chapter:   bundle.Chapter,
				Entity:    mutation.Subject,
				Field:     mutation.Field,
				OldValue:  mutation.Before,
				NewValue:  mutation.After,
				Reason:    mutation.Cause,
				FactKey:   mutation.Subject + ":" + mutation.Field,
				ValidFrom: bundle.Chapter,
			})
		}
	}
	appendMutations(bundle.ProjectedDelta.CharacterState)
	appendMutations(bundle.ProjectedDelta.Knowledge)
	appendMutations(bundle.ProjectedDelta.Locations)
	return out
}

func sealedCommitRelationshipChanges(bundle domain.ProjectedChapterBundle) []domain.RelationshipEntry {
	out := make([]domain.RelationshipEntry, 0, len(bundle.ProjectedDelta.Relationships))
	for _, mutation := range bundle.ProjectedDelta.Relationships {
		out = append(out, domain.RelationshipEntry{
			CharacterA: mutation.Subject,
			CharacterB: mutation.Object,
			Relation:   mutation.After,
			Chapter:    bundle.Chapter,
		})
	}
	return out
}

func sealedCommitResourceChanges(
	bundle domain.ProjectedChapterBundle,
) (booked []domain.ResourceClaim, pending []domain.ResourceClaim) {
	for _, mutation := range bundle.ProjectedDelta.Resources {
		claim := domain.ResourceClaim{
			ID:           mutation.StableID,
			Name:         fallbackCommitControlText(mutation.Object, mutation.Field),
			Owner:        mutation.Subject,
			Kind:         "sealed_projection",
			Status:       "booked",
			Risk:         mutation.After,
			Evidence:     mutation.Cause,
			Chapter:      bundle.Chapter,
			Participants: []string{mutation.Subject},
		}
		booked = append(booked, claim)
	}
	return booked, pending
}

func sealedCommitForeshadowChanges(bundle domain.ProjectedChapterBundle) []domain.ForeshadowUpdate {
	out := make([]domain.ForeshadowUpdate, 0, len(bundle.ProjectedDelta.Foreshadows))
	for _, mutation := range bundle.ProjectedDelta.Foreshadows {
		action := "advance"
		switch strings.ToLower(strings.TrimSpace(mutation.Operation)) {
		case "create", "plant":
			action = "plant"
		case "resolve", "consume":
			action = "resolve"
		}
		out = append(out, domain.ForeshadowUpdate{
			ID:          fallbackCommitControlText(mutation.Object, mutation.StableID),
			Action:      action,
			Description: mutation.After,
		})
	}
	return out
}

func appendUniqueCommitString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.TrimSpace(existing) == value {
			return values
		}
	}
	return append(values, value)
}

func fallbackCommitControlText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
