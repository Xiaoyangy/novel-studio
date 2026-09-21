package tools

import (
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Keep the staged diagnostic identical to the existing finalize coverage
// requirement: the inferred protagonist only, never every legacy-cast actor
// or every optional psychological field. With no known protagonist, coverage
// remains optional. Existing records retain their usual field validation.
func planEmotionalLogicMissingCharacters(s *store.Store, records []domain.CharacterEmotionalLogic) []string {
	if s == nil {
		return nil
	}
	return missingEmotionalLogicCoverage(compactStrings([]string{inferCommitProtagonist(s)}), records)
}

// Use the same project/chapter predicate and completeness check as finalize.
// An optional opening on a short book or a later chapter stays optional.
func planLongformOpeningMissing(s *store.Store, plan domain.ChapterPlan) bool {
	return s != nil && attractionRequirementsForChapter(s, plan.Chapter).Longform &&
		!domain.CompleteLongformOpeningDesign(plan.CausalSimulation.LongformOpening)
}

func planDetailsRecommendedBatchesForState(s *store.Store, chapter int, partial, merged map[string]any) []string {
	batches := planDetailsRecommendedBatches()
	plan, err := chapterPlanFromPartial(chapter, partial, merged)
	if err != nil {
		return batches
	}
	missing := planEmotionalLogicMissingCharacters(s, plan.CausalSimulation.EmotionalLogic)
	missingOpening := planLongformOpeningMissing(s, plan)
	if len(missing) == 0 && !missingOpening {
		return batches
	}
	for i, batch := range batches {
		if strings.HasPrefix(batch, "batch4_project_contracts_if_required:") {
			if len(missing) > 0 {
				batches[i] += "；本章当前必补：" + formatMissingCharacterCoverage("emotional_logic", missing) + "。只补缺失角色的既有必需项，保留已保存字段；不扩展为全角色心理矩阵"
			}
			if missingOpening {
				batches[i] += "；本章当前必补：longform_opening（当前长篇首章的既有验收要求，缺失或不完整）；补齐后再 finalize，保留其他已保存字段"
			}
			break
		}
	}
	return batches
}
