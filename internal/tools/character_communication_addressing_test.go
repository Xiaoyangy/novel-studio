package tools

import (
	"encoding/json"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"strings"
	"testing"
)

func TestCommunicationAddressingSchemaIsExplicitAndNewOnly(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var sources []string
		if enabled {
			sources = []string{domain.CharacterCommunicationAddressingPolicyV1}
		}
		submit := NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: sources})
		actor, _ := json.Marshal(submit.Schema())
		for _, field := range []string{"recipient_hint", "reply_to_received_fact_id"} {
			if strings.Contains(string(actor), field) != enabled {
				t.Fatalf("new=%v actor schema missing or leaking %s", enabled, field)
			}
		}
		resolve := NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: sources}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
		world, _ := json.Marshal(resolve.Schema())
		if strings.Contains(string(world), "communication_receptions") != enabled {
			t.Fatal("new-only recipient receipt schema not selected")
		}
	}
}
