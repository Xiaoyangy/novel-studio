package main

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func zeroExplicitInitialResourceLedger(project zeroInitProject) (domain.ResourceLedger, bool) {
	ledger := domain.ResourceLedger{Version: 1, Claims: []domain.ResourceClaim{}}
	explicit := false
	for _, c := range project.Characters {
		if c.InitialState == nil {
			continue
		}
		explicit = true
		for _, resource := range c.InitialState.Resources {
			resource = strings.TrimSpace(resource)
			if resource == "" {
				continue
			}
			sum := sha256.Sum256([]byte(c.Name + "\x00" + resource))
			ledger.Claims = append(ledger.Claims, domain.ResourceClaim{
				ID: fmt.Sprintf("initial-%x", sum[:8]), Name: resource, Owner: c.Name,
				Kind: "initial_possession", Status: "booked", Evidence: "characters.json:initial_state.resources",
				Participants: []string{c.Name}, UpdatedAt: project.GeneratedAt,
			})
		}
	}
	return ledger, explicit
}

// The soft chapter outline is not a character's already-perceived pressure,
// knowledge, location or chosen next action. Author the baseline before seal.
func zeroValidateCharacterInitialStates(project zeroInitProject) error {
	if project.WorldCodex == nil || project.WorldCodex.CharacterViewVersion == 0 {
		return nil
	}
	if project.WorldCodex.CharacterViewVersion != domain.CurrentWorldCharacterViewVersion {
		return fmt.Errorf("unsupported character opening view protocol %d", project.WorldCodex.CharacterViewVersion)
	}
	findings := architectCharacterInitialStateFindings(project.Characters, project.WorldCodex, project.BookWorld)
	if len(findings) > 0 {
		messages := make([]string, 0, len(findings))
		for _, finding := range findings {
			messages = append(messages, finding.blockingMessage())
		}
		return fmt.Errorf("zero-init requires authored opening states before outline freeze: %s", strings.Join(messages, "; "))
	}
	return nil
}

func zeroExplicitInitialCharacterState(c domain.Character) domain.CharacterSimulationState {
	i := c.InitialState
	return domain.CharacterSimulationState{
		Character: c.Name, CurrentGoal: i.CurrentGoal, Pressure: i.Pressure,
		Resources: append([]string(nil), i.Resources...), RelationshipForces: append([]string(nil), i.Relationships...),
		PrivateBoundary: "只依据自己已经知道、感知或收到的信息行动。",
		ActionTendency:  strings.Join(c.Traits, "；"), LikelyAction: i.CurrentAction,
		StateDeltaToTrack: []string{"goal", "pressure", "resource", "relationship_contract", "knowledge", "emotion", "arc_axis"},
		CompetenceStage:   "作者明确给出的开局状态，尚无正文新增经历。",
		SkillLimits: []string{
			"只能使用自身已具备的能力、已知事实和实际可用资源；不能推定未注明的专长或他人知识。",
		},
		// These are conditional risks and correction criteria, not an authored
		// mistake, false belief or future outcome that the actor must enact.
		PlausibleMistakes: []string{
			"若将未经核实的推测当成事实，判断可能出错；开局未断言已发生此错误，也不要求角色必须犯错。",
		},
		CorrectionTriggers: []string{
			"只有角色实际感知或收到、且与原判断不符的新证据，才可触发修正；尚未发生修正。",
		},
		KnowledgeLedger: domain.CharacterKnowledgeLedger{
			KnownFacts: append([]string(nil), i.KnownFacts...), EvidenceSeen: []string{"characters.json:initial_state.known_facts"},
			Confidence: "authored_initial_state", SourceChapter: 0,
			UnknownFacts:       []string{"未实际感知或收到的信息仍属未知；没有凭空补全的事实。"},
			ForbiddenKnowledge: []string{"未来事件、未披露的他人秘密和未感知的作者态事实不能作为角色知识。"},
		},
		DecisionFrame: domain.CharacterDecisionFrame{
			AvailableOptions:        []string{"在当前地点观察或核验可感知的信息", "维持现状或暂缓承诺，等待实际刺激"},
			DecisionRule:            "根据当前目标、可用资源和实际知情范围自行选择；尚未作出的选择保持开放。",
			MinimumEvidenceRequired: "观察到的事实或由角色实际收到的信息。",
		},
		EmotionAppraisal: domain.CharacterEmotionAppraisal{
			TriggerEvent: i.Pressure, GoalImpact: i.CurrentGoal, ActionPressure: i.Pressure,
		},
		ArcAxis: domain.CharacterArcAxis{
			Want: i.CurrentGoal, PressureTest: i.Pressure, ArcStage: "authored_initial_state",
		},
		RelationshipContract: []domain.CharacterRelationshipContract{},
	}
}

func zeroInitialCharacterLocation(project zeroInitProject, c domain.Character) string {
	if c.InitialState != nil {
		return strings.TrimSpace(c.InitialState.Location)
	}
	// Legacy data can supply an unambiguous protagonist position, but cannot
	// teleport all chapter participants into the first planned scene.
	if zeroIsPrimaryProtagonist(project, c) && project.BookWorld != nil {
		var found []string
		for _, place := range project.BookWorld.Places {
			name := strings.TrimSpace(place.Name)
			if name != "" && strings.Contains(project.BookWorld.ProtagonistPosition, name) {
				found = append(found, name)
			}
		}
		if len(found) == 1 {
			return found[0]
		}
	}
	return "离屏/未定；位置尚未由作者明确，不能据未来章纲推定已到场。"
}

func zeroApplyExplicitInitialDossier(d *domain.CharacterDossier, c domain.Character) {
	i := c.InitialState
	if i == nil {
		return
	}
	d.CurrentAtStoryStart = domain.CharacterStartState{
		Time: i.Time, Location: i.Location, Status: "作者明确的开局状态",
		CurrentAction: i.CurrentAction, Pressure: i.Pressure, NextIndependentMove: i.CurrentGoal,
	}
	d.Profile.Desires = []string{i.CurrentGoal}
	d.Profile.Arc = ""
	d.Profile.Backstory = "只以角色明确已知的开局事实为依据。"
	d.Resources = nil
	for n, resource := range i.Resources {
		d.Resources = append(d.Resources, domain.CharacterResource{
			ID: fmt.Sprintf("initial-resource-%d", n+1), Name: resource, Status: "开局可用", Evidence: "characters.json:initial_state.resources",
		})
	}
	// Retain authored relationship text in dynamics/observations instead of
	// inventing counterpart identities, promises or trust from a chapter cast.
	d.Relationships = nil
	d.PreStoryTimeline = nil
	// Knowing a fact at T+0 does not mean that the event occurred at T+0,
	// or that the actor personally experienced it. Keep the knowledge snapshot
	// distinct from genuinely authored, timestamped historical events.
	d.KnownFactsAtStoryStart = append([]string(nil), i.KnownFacts...)
	d.LifeAnchors = nil
	for _, commitment := range i.Commitments {
		d.LifeAnchors = append(d.LifeAnchors, domain.LifeAnchor{Kind: "已有承诺", Place: i.Location, Obligation: commitment})
	}
	d.Sources = []string{"characters.json:initial_state"}
}
