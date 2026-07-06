package domain

// StateChange 角色/实体状态变化记录。
type StateChange struct {
	Chapter  int    `json:"chapter"`
	Entity   string `json:"entity"`              // 角色名或实体名
	Field    string `json:"field"`               // 变化属性：goal/pressure/resource/relationship/secret/misbelief/action_tendency/emotion/trust/debt/injury/exposure/status/knowledge/decision_frame/relationship_contract/emotion_appraisal/arc_axis 等
	OldValue string `json:"old_value,omitempty"` // 变化前（首次出现可空）
	NewValue string `json:"new_value"`           // 变化后
	Reason   string `json:"reason,omitempty"`    // 变化原因

	// Task 073（FACTTRACK 语义）：事实有效期区间——区分"合法演化"与"矛盾"。
	// 同 FactKey 的新记录出现时，旧记录被链上作废（SupersededBy 指向新记录章号）；
	// "角色从A地到B地"是有序演化，"同章既在A又在B"才是矛盾。全部可选，老数据零影响。
	FactKey      string `json:"fact_key,omitempty"`      // 同一事实的稳定键，如 character:江烬:location
	ValidFrom    int    `json:"valid_from,omitempty"`    // 生效章号（缺省=Chapter）
	SupersededBy int    `json:"superseded_by,omitempty"` // 作废本条的后续记录章号（0=仍有效）
}

// StateChangeFactKey 返回记录的事实键：显式 FactKey 优先，否则 entity:field 派生。
func StateChangeFactKey(c StateChange) string {
	if c.FactKey != "" {
		return c.FactKey
	}
	if c.Entity == "" || c.Field == "" {
		return ""
	}
	return c.Entity + ":" + c.Field
}

// LinkSupersededChain 对同 fact_key 的记录按章号排序补 superseded_by 链（幂等纯函数）。
func LinkSupersededChain(changes []StateChange) []StateChange {
	latest := map[string]int{} // factKey -> 最新记录下标
	for i := range changes {
		key := StateChangeFactKey(changes[i])
		if key == "" {
			continue
		}
		if prev, ok := latest[key]; ok && changes[i].Chapter >= changes[prev].Chapter {
			changes[prev].SupersededBy = changes[i].Chapter
		}
		latest[key] = i
	}
	return changes
}

// LatestFactValues 返回各 fact_key 的当前有效值（链尾）。
func LatestFactValues(changes []StateChange) map[string]StateChange {
	out := map[string]StateChange{}
	for _, c := range changes {
		key := StateChangeFactKey(c)
		if key == "" {
			continue
		}
		if prev, ok := out[key]; !ok || c.Chapter >= prev.Chapter {
			out[key] = c
		}
	}
	return out
}

// CharacterStageRecord 记录某章时间线中角色所处环境、行动和决策。
// 它覆盖正文内外：正文只记录主视角可见行动，stage record 用来保证非主角
// 也在同一时间线中承受压力、犯错、选择和成长，后续出场不突兀。
type CharacterStageRecord struct {
	Chapter             int      `json:"chapter,omitempty"`
	Character           string   `json:"character"`
	Time                string   `json:"time,omitempty"`
	Location            string   `json:"location"`
	Status              string   `json:"status,omitempty"`
	Environment         string   `json:"environment"`
	CurrentAction       string   `json:"current_action"`
	Pressure            string   `json:"pressure"`
	Decision            string   `json:"decision"`
	MistakeOrMisbelief  string   `json:"mistake_or_misbelief,omitempty"`
	KnowledgeBoundary   string   `json:"knowledge_boundary"`
	VisibleInChapter    bool     `json:"visible_in_chapter,omitempty"`
	Evidence            string   `json:"evidence,omitempty"`
	Transport           string   `json:"transport,omitempty"`
	TravelTime          string   `json:"travel_time,omitempty"`
	MeetingConstraint   string   `json:"meeting_constraint,omitempty"`
	PersonalityDelta    string   `json:"personality_delta,omitempty"`
	DeathState          string   `json:"death_state,omitempty"`
	ProtagonistNotice   string   `json:"protagonist_notice,omitempty"`
	TimelineConsistency string   `json:"timeline_consistency"`
	NextPotential       string   `json:"next_potential,omitempty"`
	Tags                []string `json:"tags,omitempty"`
}

// WorldFoundation 记录正文开始前必须成立的世界铁律、开局时间和过去时间线。
// 这些内容是写前推演的底座：角色未获得改变规则的明确能力/凭证前，正文和台账
// 都只能在这些边界内推进，不能临场发明更方便的解释。
type WorldFoundation struct {
	Version              int                   `json:"version"`
	Project              string                `json:"project,omitempty"`
	StoryStart           StoryStart            `json:"story_start"`
	IronLaws             []WorldIronLaw        `json:"iron_laws"`
	RuleChangeConditions []RuleChangeCondition `json:"rule_change_conditions,omitempty"`
	PastTimeline         []PastTimelineEvent   `json:"past_timeline,omitempty"`
	CityBaseline         []LocationBaseline    `json:"city_baseline,omitempty"`
	KnowledgePolicy      string                `json:"knowledge_policy,omitempty"`
	GeneratedAt          string                `json:"generated_at,omitempty"`
	Sources              []string              `json:"sources,omitempty"`
}

// SimulationRestartPolicy defines the boundary between legacy material and the
// new simulated canon when a book is regenerated from chapter 1. Old chapters
// and ledgers may be mined for seeds, but they must not become active facts
// unless the new simulation re-derives and records them.
type SimulationRestartPolicy struct {
	Version              int      `json:"version"`
	Project              string   `json:"project,omitempty"`
	Active               bool     `json:"active"`
	Mode                 string   `json:"mode"`
	GenerationID         string   `json:"generation_id"`
	GeneratedAt          string   `json:"generated_at,omitempty"`
	CanonicalStart       string   `json:"canonical_start,omitempty"`
	LegacyUse            string   `json:"legacy_use,omitempty"`
	StoryStatePolicy     string   `json:"story_state_policy,omitempty"`
	CharacterStatePolicy string   `json:"character_state_policy,omitempty"`
	ResourcePolicy       string   `json:"resource_policy,omitempty"`
	KnowledgePolicy      string   `json:"knowledge_policy,omitempty"`
	AllowedSeedSources   []string `json:"allowed_seed_sources,omitempty"`
	ForbiddenFactSources []string `json:"forbidden_fact_sources,omitempty"`
	CanonicalStateRoots  []string `json:"canonical_state_roots,omitempty"`
	ResetTargets         []string `json:"reset_targets,omitempty"`
	RAGPolicy            string   `json:"rag_policy,omitempty"`
	Sources              []string `json:"sources,omitempty"`
}

type StoryStart struct {
	AbsoluteTime string `json:"absolute_time,omitempty"`
	StoryClock   string `json:"story_clock,omitempty"`
	Location     string `json:"location,omitempty"`
	Description  string `json:"description,omitempty"`
}

type WorldIronLaw struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Rule      string   `json:"rule"`
	Boundary  string   `json:"boundary"`
	Evidence  string   `json:"evidence,omitempty"`
	AppliesTo []string `json:"applies_to,omitempty"`
}

type RuleChangeCondition struct {
	RuleID        string   `json:"rule_id"`
	AllowedBy     []string `json:"allowed_by,omitempty"`
	ProofNeeded   string   `json:"proof_needed,omitempty"`
	UpdateTargets []string `json:"update_targets,omitempty"`
}

type PastTimelineEvent struct {
	Time             string   `json:"time"`
	Event            string   `json:"event"`
	Locations        []string `json:"locations,omitempty"`
	Participants     []string `json:"participants,omitempty"`
	Consequences     []string `json:"consequences,omitempty"`
	ProtagonistKnows bool     `json:"protagonist_knows,omitempty"`
	Source           string   `json:"source,omitempty"`
}

type LocationBaseline struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	StatusAtStart string   `json:"status_at_start,omitempty"`
	OpenQuestions []string `json:"open_questions,omitempty"`
	UpdatePolicy  string   `json:"update_policy,omitempty"`
}

// CharacterDossier 是每个角色的独立可检索档案。
// 它保存主角视角之外的生活、工作、过去经历、资源、通信边界和关系引入记录，
// 支撑 RAG 在任意章节直接推演该角色自己经历了什么。
type CharacterDossier struct {
	Version               int                     `json:"version"`
	Character             string                  `json:"character"`
	Role                  string                  `json:"role,omitempty"`
	Tier                  string                  `json:"tier,omitempty"`
	Aliases               []string                `json:"aliases,omitempty"`
	Profile               CharacterDossierProfile `json:"profile,omitempty"`
	LifeAnchors           []LifeAnchor            `json:"life_anchors,omitempty"`
	PreStoryTimeline      []CharacterPastEvent    `json:"pre_story_timeline,omitempty"`
	Resources             []CharacterResource     `json:"resources,omitempty"`
	Relationships         []CharacterRelationNote `json:"relationships,omitempty"`
	CommunicationBoundary CommunicationBoundary   `json:"communication_boundary,omitempty"`
	KnowledgeBoundary     string                  `json:"knowledge_boundary,omitempty"`
	DecisionModel         string                  `json:"decision_model,omitempty"`
	CurrentAtStoryStart   CharacterStartState     `json:"current_at_story_start,omitempty"`
	RAGHints              []string                `json:"rag_hints,omitempty"`
	GeneratedAt           string                  `json:"generated_at,omitempty"`
	Sources               []string                `json:"sources,omitempty"`
}

type CharacterDossierProfile struct {
	Description string   `json:"description,omitempty"`
	Backstory   string   `json:"backstory,omitempty"`
	Traits      []string `json:"traits,omitempty"`
	Arc         string   `json:"arc,omitempty"`
	Fears       []string `json:"fears,omitempty"`
	Desires     []string `json:"desires,omitempty"`
	Boundaries  []string `json:"boundaries,omitempty"`
}

type LifeAnchor struct {
	Kind        string `json:"kind"`
	Place       string `json:"place"`
	Schedule    string `json:"schedule,omitempty"`
	Obligation  string `json:"obligation,omitempty"`
	TravelNotes string `json:"travel_notes,omitempty"`
}

type CharacterPastEvent struct {
	Time               string   `json:"time"`
	Event              string   `json:"event"`
	Location           string   `json:"location,omitempty"`
	PeopleMet          []string `json:"people_met,omitempty"`
	Relationship       string   `json:"relationship,omitempty"`
	Consequence        string   `json:"consequence,omitempty"`
	KnownToProtagonist bool     `json:"known_to_protagonist,omitempty"`
}

type CharacterResource struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
	Status   string `json:"status,omitempty"`
	Risk     string `json:"risk,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type CharacterRelationNote struct {
	Other              string `json:"other"`
	HowMet             string `json:"how_met,omitempty"`
	CurrentTie         string `json:"current_tie,omitempty"`
	DebtOrTrust        string `json:"debt_or_trust,omitempty"`
	KnownToProtagonist bool   `json:"known_to_protagonist,omitempty"`
}

type CommunicationBoundary struct {
	CanContactProtagonist bool     `json:"can_contact_protagonist"`
	Channels              []string `json:"channels,omitempty"`
	Delay                 string   `json:"delay,omitempty"`
	FailureModes          []string `json:"failure_modes,omitempty"`
	InfoAllowed           string   `json:"info_allowed,omitempty"`
}

type CharacterStartState struct {
	Time                string `json:"time,omitempty"`
	Location            string `json:"location,omitempty"`
	Status              string `json:"status,omitempty"`
	CurrentAction       string `json:"current_action,omitempty"`
	Pressure            string `json:"pressure,omitempty"`
	NextIndependentMove string `json:"next_independent_move,omitempty"`
}

// ChapterWorldDelta is the per-chapter all-character/world progression packet.
// It is regenerated on both first commit and rewrite commit, so compressed
// context can recover the active simulated world without relying on chat memory.
type ChapterWorldDelta struct {
	Version         int                     `json:"version"`
	Chapter         int                     `json:"chapter"`
	GenerationID    string                  `json:"generation_id,omitempty"`
	Rewrite         bool                    `json:"rewrite,omitempty"`
	Summary         string                  `json:"summary,omitempty"`
	CharacterDeltas []CharacterChapterDelta `json:"character_deltas,omitempty"`
	WorldDeltas     []WorldChapterDelta     `json:"world_deltas,omitempty"`
	GeneratedAt     string                  `json:"generated_at,omitempty"`
	Sources         []string                `json:"sources,omitempty"`
}

type CharacterChapterDelta struct {
	Character           string `json:"character"`
	Location            string `json:"location,omitempty"`
	Status              string `json:"status,omitempty"`
	VisibleInChapter    bool   `json:"visible_in_chapter,omitempty"`
	CurrentAction       string `json:"current_action,omitempty"`
	Decision            string `json:"decision,omitempty"`
	MistakeOrMisbelief  string `json:"mistake_or_misbelief,omitempty"`
	KnowledgeBoundary   string `json:"knowledge_boundary,omitempty"`
	PersonalityDelta    string `json:"personality_delta,omitempty"`
	DeathState          string `json:"death_state,omitempty"`
	ProtagonistNotice   string `json:"protagonist_notice,omitempty"`
	WorldImpact         string `json:"world_impact,omitempty"`
	NextPotential       string `json:"next_potential,omitempty"`
	TimelineConsistency string `json:"timeline_consistency,omitempty"`
}

type WorldChapterDelta struct {
	Kind                 string `json:"kind"`
	Entity               string `json:"entity,omitempty"`
	Change               string `json:"change"`
	Evidence             string `json:"evidence,omitempty"`
	VisibleToProtagonist bool   `json:"visible_to_protagonist,omitempty"`
}
