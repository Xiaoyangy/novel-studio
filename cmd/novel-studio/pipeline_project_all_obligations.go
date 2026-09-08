package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// v2 character consequences are independently caused events, not a set of
// matching prose strings. Qualifying their contracts keeps the existing stable
// obligation-ID protocol while preserving distinct actors, deliveries and due
// chapters. Historical simulations retain their original contracts and IDs.
func pipelineProjectAllCharacterObligationSource(
	sim domain.ChapterWorldSimulation,
	decision domain.CharacterWorldDecision,
	effect domain.DecisionButterflyEffect,
) (contract, sourceDigest string) {
	contract = strings.TrimSpace(effect.Effect)
	if contract == "" || sim.CharacterAgentProtocol == nil ||
		sim.CharacterAgentProtocol.Version != domain.CharacterAgentDecisionProtocolV2Version {
		return contract, ""
	}
	targets := compactProjectAllStrings(effect.Targets)
	sort.Strings(targets)
	sourceDigest = pipelineProjectAllDigest(struct {
		Version           string   `json:"version"`
		Character         string   `json:"character"`
		Decision          string   `json:"decision"`
		Action            string   `json:"action"`
		Effect            string   `json:"effect"`
		Targets           []string `json:"targets"`
		TransmissionPath  string   `json:"transmission_path"`
		ArrivalChapter    int      `json:"arrival_chapter"`
		Visibility        string   `json:"visibility"`
		ProtagonistImpact string   `json:"protagonist_impact"`
	}{
		Version:   "source-qualified-character-obligations.v1",
		Character: strings.TrimSpace(decision.Character), Decision: strings.TrimSpace(decision.Decision),
		Action: strings.TrimSpace(decision.Action), Effect: contract, Targets: targets,
		TransmissionPath: strings.TrimSpace(effect.TransmissionPath), ArrivalChapter: effect.ArrivalChapter,
		Visibility: strings.TrimSpace(effect.Visibility), ProtagonistImpact: strings.TrimSpace(effect.ProtagonistImpact),
	})
	return fmt.Sprintf("%s〔角色：%s；因果来源：%s〕", contract, strings.TrimSpace(decision.Character), strings.TrimPrefix(sourceDigest, "sha256:")[:20]), sourceDigest
}
