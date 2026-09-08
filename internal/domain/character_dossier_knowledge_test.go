package domain

import (
	"encoding/json"
	"testing"
)

func TestCharacterDossierEmptyOpeningKnowledgePreservesLegacyJSON(t *testing.T) {
	const legacy = `{"version":1,"character":"林澄","profile":{},"pre_story_timeline":[{"time":"T-600","event":"已签领原件"}],"communication_boundary":{"can_contact_protagonist":false},"current_at_story_start":{"time":"T0"}}`
	var dossier CharacterDossier
	if err := json.Unmarshal([]byte(legacy), &dossier); err != nil {
		t.Fatal(err)
	}
	for _, facts := range [][]string{nil, {}} {
		dossier.KnownFactsAtStoryStart = facts
		raw, err := json.Marshal(dossier)
		if err != nil || string(raw) != legacy {
			t.Fatalf("empty optional field changed legacy dossier bytes: %s %v", raw, err)
		}
	}
}
