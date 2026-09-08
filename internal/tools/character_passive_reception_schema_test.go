package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPassiveReceptionSchemaRequiresHostPolicyAndCannotAuthorText(t *testing.T) {
	old := NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	if _, exists := old.Schema()["properties"].(map[string]any)["passive_receptions"]; exists {
		t.Fatal("legacy v2 schema acquired passive writes")
	}
	current := NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: []string{domain.CharacterPassiveReceptionPolicyV2}}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	field, exists := current.Schema()["properties"].(map[string]any)["passive_receptions"]
	if !exists {
		t.Fatal("host policy did not expose the passive-reception tool contract")
	}
	raw, _ := json.Marshal(field)
	for _, forbidden := range []string{`"text":`, `"decision":`, `"post_state":`, `"current_goal":`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("passive tool can author receiver content: %s", forbidden)
		}
	}
}
