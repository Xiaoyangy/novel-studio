package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Source is an explicit repair authority, separate from human-readable error
// text. Only recognized source/code/subject combinations grant a write target.
type architectSourceFinding struct {
	Source string `json:"source"`
	domain.WorldCoherenceFinding
}

func (finding architectSourceFinding) blockingMessage() string {
	return finding.Subject + "：" + finding.Message
}

var architectCharacterStateSubjectRE = regexp.MustCompile(`^characters\[[0-9]+\]\.initial_state(?:\.location)?$`)

func architectCharacterInitialStateFindings(characters []domain.Character, codex *domain.WorldCodex, world *domain.BookWorld) []architectSourceFinding {
	if codex == nil || codex.CharacterViewVersion != domain.CurrentWorldCharacterViewVersion {
		return nil
	}
	knownPlaces := make(map[string]bool)
	knownMechanisms := make(map[string]bool)
	for _, mechanism := range codex.Mechanisms {
		knownMechanisms[mechanism.ID] = true
	}
	if world != nil {
		for _, place := range world.Places {
			for _, name := range []string{place.ID, place.Name} {
				if key := strings.ToLower(strings.TrimSpace(name)); key != "" {
					knownPlaces[key] = true
				}
			}
		}
	}
	var findings []architectSourceFinding
	add := func(index int, code, field, message string) {
		findings = append(findings, architectSourceFinding{
			Source: "characters.json",
			WorldCoherenceFinding: domain.WorldCoherenceFinding{
				Code: code, Severity: domain.WorldCoherenceSeverityError,
				Subject: fmt.Sprintf("characters[%d].initial_state%s", index, field), Message: message,
			},
		})
	}
	for index, character := range characters {
		if strings.TrimSpace(character.Name) == "" {
			continue
		}
		if character.InitialState == nil {
			tier := strings.ToLower(strings.TrimSpace(character.Tier))
			required := tier == "" || tier == "core" || tier == "important" || zeroIsProtagonist(character)
			if required {
				add(index, "characters.initial_state.missing", "", fmt.Sprintf("角色 %q 缺少作者明确的开局位置、个人目标、压力与已知事实；不得从未来 arc/core_event 推断", character.Name))
			}
			continue
		}
		if err := domain.ValidateCharacterInitialState(character); err != nil {
			add(index, "characters.initial_state.invalid", "", err.Error())
			continue
		}
		for _, resource := range character.InitialState.ResourceBalances {
			for _, ref := range resource.AccessRequiresAny {
				if !knownMechanisms[ref] {
					add(index, "characters.initial_state.invalid", "", fmt.Sprintf("资源 %s 的 access_requires_any 引用不存在的世界机制 %q", resource.ResourceID, ref))
				}
			}
		}
		if !knownPlaces[strings.ToLower(strings.TrimSpace(character.InitialState.Location))] {
			add(index, "characters.initial_state.location.unknown", ".location",
				fmt.Sprintf("角色 %q 的开局地点 %q 不对应 book_world.places 的现有 id/name；不能自动移到主角第一场景", character.Name, character.InitialState.Location))
		}
	}
	// A shared resource has one global truth even when several characters
	// reference it. Per-character validation cannot detect divergent replicas.
	registry := domain.CharacterAgentRegistry{Version: domain.CharacterAgentRegistryVersion}
	firstResource := -1
	for index, character := range characters {
		if character.InitialState == nil {
			continue
		}
		if len(character.InitialState.ResourceBalances) > 0 && firstResource < 0 {
			firstResource = index
		}
		updated, _, err := registry.UpsertCharacter(character.Name, character.Aliases, character.Tier, 0, "")
		if err != nil {
			continue
		}
		registry = updated
	}
	if firstResource >= 0 {
		if _, err := domain.BuildWorldPhysicalStateFromInitialV2(characters, registry); err != nil {
			add(firstResource, "characters.initial_state.invalid", "", fmt.Sprintf("角色初始共享资源全局校验失败：%v", err))
		}
	}
	return findings
}

func architectSourceFindingRepairRoot(finding architectSourceFinding) string {
	if finding.Source != "characters.json" || finding.Severity != domain.WorldCoherenceSeverityError ||
		!architectCharacterStateSubjectRE.MatchString(finding.Subject) {
		return ""
	}
	switch finding.Code {
	case "characters.initial_state.missing", "characters.initial_state.invalid", "characters.initial_state.location.unknown":
		return "characters"
	default:
		return ""
	}
}
