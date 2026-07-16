package rules

import "testing"

func TestOrchestrationMetadataLeaksCoversMachineOnlyKeys(t *testing.T) {
	keys := []string{
		"simulation_id",
		"world_simulation_id",
		"craft_recall_receipt",
		"render_packet",
		"body_sha256",
		"rewrite_source",
		"plan_details",
		"chapter_world_simulation",
		"source_refs",
		"receipt_id",
		"checkpoint",
		"sha256",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			text := "第一章 风起\n\n【" + key + "：internal-value】"
			leaks := OrchestrationMetadataLeaks(text)
			if len(leaks) != 1 || leaks[0].Rule != OrchestrationMetadataLeakRule ||
				leaks[0].Target != key || leaks[0].Severity != SeverityError {
				t.Fatalf("machine key %q not deterministically rejected: %+v", key, leaks)
			}
		})
	}
}

func TestOrchestrationMetadataLeaksAllowsOrdinaryFictionalSystemPanel(t *testing.T) {
	text := "第一章 余额\n\n【系统提示：余额不足，请换一张卡。】\n\n他把旧卡塞回钱包。"
	if leaks := OrchestrationMetadataLeaks(text); len(leaks) != 0 {
		t.Fatalf("ordinary fictional system panel was mistaken for orchestration metadata: %+v", leaks)
	}
}

func TestOrchestrationMetadataLeaksUsesTokenBoundaries(t *testing.T) {
	text := "第一章 山口\n\n牌子上印着mycheckpointcafe，门还没开。"
	if leaks := OrchestrationMetadataLeaks(text); len(leaks) != 0 {
		t.Fatalf("embedded English letters should not be treated as an exact machine key: %+v", leaks)
	}
}
