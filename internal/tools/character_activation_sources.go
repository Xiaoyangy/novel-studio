package tools

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func loadCurrentCharacterActivationEvidence(st *store.Store, simulation domain.ChapterWorldSimulation) (*domain.CharacterActivationChapterEvidence, error) {
	verified, err := loadCurrentVerifiedCharacterActivationEvidence(st, simulation)
	if err != nil {
		return nil, err
	}
	if err := verified.ValidateSimulation(simulation); err != nil {
		return nil, err
	}
	evidence := verified.Evidence()
	return &evidence, nil
}

func loadCurrentVerifiedCharacterActivationEvidence(st *store.Store, simulation domain.ChapterWorldSimulation) (*domain.VerifiedCharacterActivationChapter, error) {
	if simulation.CharacterActivation == nil {
		return nil, fmt.Errorf("simulation is not a whole-chapter activation")
	}
	evidence, err := st.LoadVerifiedCharacterActivationChapter(simulation.GenerationID, simulation.Chapter)
	if err != nil {
		return nil, err
	}
	if evidence != nil {
		return evidence, nil
	}
	matched, err := validatePromotedCharacterSimulation(st, simulation)
	if err != nil {
		return nil, err
	}
	if !matched {
		return nil, fmt.Errorf("activation simulation lacks local evidence or an exact sealed promotion")
	}
	bundles, err := st.ProjectedV2().LoadProjectedChapterBundles(simulation.GenerationID)
	if err != nil {
		return nil, err
	}
	for _, bundle := range bundles {
		if bundle.Chapter == simulation.Chapter && bundle.CharacterActivationEvidence != nil {
			verified, err := domain.VerifyCharacterActivationChapter(*bundle.CharacterActivationEvidence)
			if err != nil {
				return nil, err
			}
			return &verified, nil
		}
	}
	return nil, fmt.Errorf("promoted activation simulation lacks its full chapter evidence")
}

func currentPlanGroundingInput(st *store.Store, plan domain.ChapterPlan, simulation domain.ChapterWorldSimulation, protocol string) (domain.PlanGroundingInput, error) {
	if simulation.CharacterActivation != nil {
		evidence, err := loadCurrentVerifiedCharacterActivationEvidence(st, simulation)
		if err != nil {
			return domain.PlanGroundingInput{}, err
		}
		return evidence.NewPlanGroundingInput(plan, simulation, protocol)
	}
	observation, arbitration, err := loadPlanGroundingSources(st, simulation)
	if err != nil {
		return domain.PlanGroundingInput{}, err
	}
	return domain.NewPlanGroundingInput(plan, simulation, observation, arbitration, protocol)
}
