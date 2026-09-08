package domain

import "fmt"

// The start/end of a multi-cycle chapter belong to the full evidence chain,
// not just its last arbitration. Historical single-round checks stay intact.
func CharacterSimulationStoryTimeSource(simulation ChapterWorldSimulation, evidence *CharacterAgentEvidenceBundle, activation *CharacterActivationChapterEvidence) (*StoryClockContext, string, error) {
	if simulation.StoryTime == nil || simulation.CharacterAgentProtocol == nil {
		return nil, "", fmt.Errorf("story clock requires independent actual time evidence")
	}
	if simulation.CharacterActivation != nil {
		if activation == nil || evidence != nil {
			return nil, "", fmt.Errorf("whole-chapter clock requires its complete activation evidence")
		}
		if err := ValidateCharacterActivationSimulation(simulation, *activation); err != nil {
			return nil, "", err
		}
		return activation.Cycles[0].Evidence.Stimulus.StoryClock, activation.Digest, nil
	}
	if activation != nil || evidence == nil || len(evidence.Arbitrations) == 0 {
		return nil, "", fmt.Errorf("story clock requires the sealed final arbitration")
	}
	last := evidence.Arbitrations[len(evidence.Arbitrations)-1]
	if !last.Finalized || !SameStoryTime(simulation.StoryTime, last.StoryTime) || last.Digest != simulation.CharacterAgentProtocol.ArbitrationDigest {
		return nil, "", fmt.Errorf("story clock differs from its sealed final arbitration")
	}
	return evidence.Stimulus.StoryClock, last.Digest, nil
}
