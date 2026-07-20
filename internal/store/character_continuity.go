package store

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	characterContinuityJSON = "meta/character_continuity.json"
	characterContinuityMD   = "meta/character_continuity.md"
	characterReviewPolicy   = "角色动力学与人物回归规划只作为写作连续性、行为合理性与后续大纲参考；是否让某个角色在本章回归，不作为章级审阅通过/失败条件。"
)

// LoadCharacterContinuityLedger reads the durable character-continuity ledger.
func (s *Store) LoadCharacterContinuityLedger() (*domain.CharacterContinuityLedger, error) {
	var ledger domain.CharacterContinuityLedger
	if err := s.Progress.io.ReadJSON(characterContinuityJSON, &ledger); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &ledger, nil
}

func (s *Store) refreshCharacterContinuityLedger(
	progress *domain.Progress,
	outlineByChapter map[int]domain.OutlineEntry,
	positionByChapter map[int]domain.ChapterPosition,
	completed []int,
	now string,
) (*domain.CharacterContinuityLedger, error) {
	if progress == nil {
		return nil, fmt.Errorf("missing progress")
	}
	chars, err := s.Characters.Load()
	if err != nil {
		return nil, fmt.Errorf("load characters: %w", err)
	}
	cast, err := s.Cast.Load()
	if err != nil {
		return nil, fmt.Errorf("load cast ledger: %w", err)
	}
	stateChanges, err := s.World.LoadStateChanges()
	if err != nil {
		return nil, fmt.Errorf("load state changes: %w", err)
	}
	relationships, err := s.World.LoadRelationships()
	if err != nil {
		return nil, fmt.Errorf("load relationships: %w", err)
	}
	var resources []domain.ResourceClaim
	if resourceLedger, err := s.ResourceLedger.Load(); err == nil && resourceLedger != nil {
		resources = resourceLedger.Claims
	} else if err != nil {
		return nil, fmt.Errorf("load resource ledger: %w", err)
	}
	layered, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		return nil, fmt.Errorf("load layered outline: %w", err)
	}

	appearances, err := s.characterAppearancesBySummary(completed)
	if err != nil {
		return nil, err
	}
	latest := maxCompletedChapter(completed)
	next := progress.NextChapter()
	ledger := &domain.CharacterContinuityLedger{
		Version:           1,
		NovelName:         progress.NovelName,
		GeneratedAt:       now,
		CurrentChapter:    progress.CurrentChapter,
		CompletedChapters: append([]int(nil), completed...),
		ReviewPolicy:      characterReviewPolicy,
	}

	seen := map[string]bool{}
	for _, c := range chars {
		if c.Name == "" {
			continue
		}
		seen[c.Name] = true
		entry := domain.CharacterContinuityEntry{
			Name:               c.Name,
			Source:             "characters",
			Role:               c.Role,
			Tier:               firstNonEmpty(c.Tier, "important"),
			Aliases:            append([]string(nil), c.Aliases...),
			AppearanceChapters: characterAppearanceChapters(c.Name, c.Aliases, appearances),
			CurrentFacts:       characterCurrentFacts(c.Name, stateChanges, relationships, resources, 5),
			ArcDirection:       compactProgressText(c.Arc, 220),
		}
		entry.AppearanceCount = len(entry.AppearanceChapters)
		entry.FirstSeenChapter, entry.LastSeenChapter = firstLastChapter(entry.AppearanceChapters)
		entry.FutureUses = s.characterFutureUses(c.Name, c.Aliases, outlineByChapter, positionByChapter, layered, latest, 6)
		entry.ReturnMode, entry.PlanningNote = characterReturnMode(entry, true)
		entry.Dynamics = characterDynamicsProfile(entry, c, stateChanges, relationships, resources, next, latest)
		entry.ReturnPlan = characterReturnPlan(entry, next, latest)
		entry.ConsistencyChecks = characterConsistencyChecks(entry)
		ledger.Entries = append(ledger.Entries, entry)
	}

	for _, c := range cast {
		if c.Name == "" || c.Promoted || seen[c.Name] {
			continue
		}
		appearanceChapters := append([]int(nil), c.AppearanceChapters...)
		entry := domain.CharacterContinuityEntry{
			Name:               c.Name,
			Source:             "cast_ledger",
			BriefRole:          firstNonEmpty(c.BriefRole, s.inferCastBriefRole(c.Name, c.Aliases, appearanceChapters)),
			Aliases:            append([]string(nil), c.Aliases...),
			FirstSeenChapter:   c.FirstSeenChapter,
			LastSeenChapter:    c.LastSeenChapter,
			AppearanceCount:    c.AppearanceCount,
			AppearanceChapters: appearanceChapters,
			CurrentFacts:       characterCurrentFacts(c.Name, stateChanges, relationships, resources, 4),
		}
		entry.FutureUses = s.characterFutureUses(c.Name, c.Aliases, outlineByChapter, positionByChapter, layered, latest, 4)
		entry.ReturnMode, entry.PlanningNote = characterReturnMode(entry, false)
		entry.Dynamics = characterDynamicsProfile(entry, domain.Character{Name: entry.Name, Role: entry.BriefRole}, stateChanges, relationships, resources, next, latest)
		entry.ReturnPlan = characterReturnPlan(entry, next, latest)
		entry.ConsistencyChecks = characterConsistencyChecks(entry)
		ledger.Entries = append(ledger.Entries, entry)
	}

	sort.SliceStable(ledger.Entries, func(i, j int) bool {
		ri, rj := characterModeRank(ledger.Entries[i]), characterModeRank(ledger.Entries[j])
		if ri != rj {
			return ri < rj
		}
		if ledger.Entries[i].LastSeenChapter != ledger.Entries[j].LastSeenChapter {
			return ledger.Entries[i].LastSeenChapter > ledger.Entries[j].LastSeenChapter
		}
		return ledger.Entries[i].Name < ledger.Entries[j].Name
	})
	ledger.NextChapterFocus = nextCharacterHints(ledger.Entries, next, latest, 10)
	if err := s.writeCharacterContinuityLedger(ledger); err != nil {
		return nil, err
	}
	return ledger, nil
}

func (s *Store) writeCharacterContinuityLedger(ledger *domain.CharacterContinuityLedger) error {
	return s.Progress.io.WithWriteLock(func() error {
		if err := s.Progress.io.WriteJSONUnlocked(characterContinuityJSON, ledger); err != nil {
			return err
		}
		return s.Progress.io.WriteMarkdownUnlocked(characterContinuityMD, renderCharacterContinuityLedger(ledger))
	})
}

func (s *Store) characterAppearancesBySummary(completed []int) (map[string][]int, error) {
	out := map[string][]int{}
	for _, ch := range completed {
		summary, err := s.Summaries.LoadSummary(ch)
		if err != nil {
			return nil, fmt.Errorf("load summary ch%d: %w", ch, err)
		}
		if summary == nil {
			continue
		}
		seen := map[string]bool{}
		for _, name := range summary.Characters {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out[name] = append(out[name], ch)
		}
	}
	return out, nil
}

func characterAppearanceChapters(name string, aliases []string, appearances map[string][]int) []int {
	set := map[int]bool{}
	for _, ch := range appearances[name] {
		set[ch] = true
	}
	for _, alias := range aliases {
		for _, ch := range appearances[alias] {
			set[ch] = true
		}
	}
	out := make([]int, 0, len(set))
	for ch := range set {
		out = append(out, ch)
	}
	sort.Ints(out)
	return out
}

func firstLastChapter(chapters []int) (int, int) {
	if len(chapters) == 0 {
		return 0, 0
	}
	return chapters[0], chapters[len(chapters)-1]
}

func characterCurrentFacts(
	name string,
	stateChanges []domain.StateChange,
	relationships []domain.RelationshipEntry,
	resources []domain.ResourceClaim,
	limit int,
) []string {
	if limit <= 0 {
		return nil
	}
	var facts []string
	for i := len(stateChanges) - 1; i >= 0 && len(facts) < limit; i-- {
		c := stateChanges[i]
		if c.Entity == name {
			facts = append(facts, fmt.Sprintf("第%d章 %s：%s", c.Chapter, c.Field, compactProgressText(c.NewValue, 90)))
		}
	}
	for i := len(relationships) - 1; i >= 0 && len(facts) < limit; i-- {
		r := relationships[i]
		if r.CharacterA == name || r.CharacterB == name {
			other := r.CharacterB
			if r.CharacterB == name {
				other = r.CharacterA
			}
			facts = append(facts, fmt.Sprintf("第%d章 与%s：%s", r.Chapter, other, compactProgressText(r.Relation, 90)))
		}
	}
	for i := len(resources) - 1; i >= 0 && len(facts) < limit; i-- {
		claim := resources[i]
		if claim.Owner == name || containsString(claim.Participants, name) {
			facts = append(facts, fmt.Sprintf("第%d章 资源%s：%s(%s)", claim.Chapter, claim.Kind, claim.Name, claim.Status))
		}
	}
	return facts
}

func characterDynamicsProfile(
	entry domain.CharacterContinuityEntry,
	card domain.Character,
	stateChanges []domain.StateChange,
	relationships []domain.RelationshipEntry,
	resources []domain.ResourceClaim,
	next, latest int,
) domain.CharacterDynamicsProfile {
	relationshipForces := characterRelationshipForces(entry.Name, relationships, 4)
	resourceForces := characterResourceForces(entry.Name, resources, 4)
	secretFacts := characterStateFactsByKeywords(entry.Name, stateChanges, []string{"秘密", "隐瞒", "身份", "旧债", "失联", "担保", "姓名", "预挂号", "空白", "医院", "背叛", "旧账"}, 3)
	misbeliefFacts := characterStateFactsByKeywords(entry.Name, stateChanges, []string{"误判", "误会", "以为", "怀疑", "不信", "嫌疑", "洗清", "试探"}, 3)
	emotionalFacts := characterStateFactsByKeywords(entry.Name, stateChanges, []string{"情绪", "恐惧", "信任", "警惕", "态度", "责任", "人味"}, 2)
	physicalFacts := characterStateFactsByKeywords(entry.Name, stateChanges, []string{"伤", "血", "身体", "寿命", "器官", "影子", "名字"}, 2)
	riskFacts := characterRiskFacts(entry.Name, stateChanges, resources, 3)

	goal := ""
	nextAction := ""
	if len(entry.FutureUses) > 0 {
		use := entry.FutureUses[0]
		if use.Chapter == next {
			goal = fmt.Sprintf("下一章目标倾向：%s", compactProgressText(use.Action, 140))
		} else {
			goal = fmt.Sprintf("后续目标倾向：%s", compactProgressText(use.Action, 140))
		}
		nextAction = compactProgressText(use.Action, 160)
	}
	if goal == "" {
		goal = compactProgressText(firstNonEmpty(card.Arc, entry.ArcDirection, entry.PlanningNote), 160)
	}
	if nextAction == "" {
		nextAction = compactProgressText(entry.PlanningNote, 160)
	}

	pressure := firstNonEmpty(firstText(riskFacts), firstText(relationshipForces), firstText(entry.CurrentFacts))
	if pressure == "" && len(entry.FutureUses) > 0 {
		pressure = compactProgressText(entry.FutureUses[0].Action, 120)
	}

	profile := domain.CharacterDynamicsProfile{
		CurrentGoal:          goal,
		PrimaryPressure:      pressure,
		Resources:            resourceForces,
		RelationshipForces:   relationshipForces,
		Secrets:              secretFacts,
		Misbeliefs:           misbeliefFacts,
		ActionBias:           characterActionBias(card, entry),
		RiskPressure:         compactProgressText(strings.Join(riskFacts, "；"), 180),
		EmotionalState:       compactProgressText(strings.Join(emotionalFacts, "；"), 160),
		PhysicalState:        compactProgressText(strings.Join(physicalFacts, "；"), 160),
		ExposureLevel:        characterExposureLevel(entry, next, latest),
		NextLikelyAction:     nextAction,
		ConflictVector:       characterConflictVector(entry, relationshipForces, riskFacts),
		KnowledgeLedger:      characterKnowledgeLedger(entry, secretFacts, misbeliefFacts, latest),
		DecisionFrame:        characterDecisionFrame(card, entry, goal, pressure, nextAction, riskFacts, resourceForces),
		RelationshipContract: characterRelationshipContracts(entry.Name, relationships, 4),
		EmotionAppraisal:     characterEmotionAppraisal(card, pressure, firstText(emotionalFacts), firstText(riskFacts), firstText(relationshipForces)),
		ArcAxis:              characterArcAxis(card, entry, goal, pressure, firstText(misbeliefFacts), latest),
		Psych:                card.Psych,
	}
	if len(profile.Misbeliefs) == 0 {
		profile.Misbeliefs = []string{"未见明确误判台账；写作时只能让此人知道已公开或亲身经历的信息。"}
	}
	return profile
}

func characterRelationshipForces(name string, relationships []domain.RelationshipEntry, limit int) []string {
	var out []string
	for i := len(relationships) - 1; i >= 0 && len(out) < limit; i-- {
		r := relationships[i]
		if r.CharacterA != name && r.CharacterB != name {
			continue
		}
		other := r.CharacterB
		if r.CharacterB == name {
			other = r.CharacterA
		}
		out = append(out, fmt.Sprintf("第%d章 与%s：%s", r.Chapter, other, compactProgressText(r.Relation, 90)))
	}
	return out
}

func characterResourceForces(name string, resources []domain.ResourceClaim, limit int) []string {
	var out []string
	for i := len(resources) - 1; i >= 0 && len(out) < limit; i-- {
		claim := resources[i]
		if claim.Owner != name && !containsString(claim.Participants, name) {
			continue
		}
		status := firstNonEmpty(claim.Status, "unknown")
		out = append(out, fmt.Sprintf("第%d章 %s/%s：%s(%s)", claim.Chapter, firstNonEmpty(claim.Kind, "resource"), status, claim.Name, compactProgressText(claim.Risk, 60)))
	}
	return out
}

func characterStateFactsByKeywords(name string, changes []domain.StateChange, keywords []string, limit int) []string {
	var out []string
	for i := len(changes) - 1; i >= 0 && len(out) < limit; i-- {
		c := changes[i]
		if c.Entity != name {
			continue
		}
		text := strings.Join([]string{c.Field, c.OldValue, c.NewValue, c.Reason}, " ")
		if !containsAnyKeyword(text, keywords) {
			continue
		}
		out = append(out, fmt.Sprintf("第%d章 %s：%s", c.Chapter, c.Field, compactProgressText(c.NewValue, 90)))
	}
	return out
}

func characterRiskFacts(name string, changes []domain.StateChange, resources []domain.ResourceClaim, limit int) []string {
	var out []string
	for i := len(changes) - 1; i >= 0 && len(out) < limit; i-- {
		c := changes[i]
		if c.Entity != name {
			continue
		}
		text := strings.Join([]string{c.Field, c.NewValue, c.Reason}, " ")
		if containsAnyKeyword(text, []string{"压力", "风险", "债", "审计", "担保", "伤", "失联", "背叛", "旧账", "锁定"}) {
			out = append(out, fmt.Sprintf("第%d章 %s：%s", c.Chapter, c.Field, compactProgressText(c.NewValue, 90)))
		}
	}
	for i := len(resources) - 1; i >= 0 && len(out) < limit; i-- {
		claim := resources[i]
		if claim.Owner != name && !containsString(claim.Participants, name) {
			continue
		}
		if claim.Risk == "" && claim.Status != "pending" {
			continue
		}
		out = append(out, fmt.Sprintf("第%d章 资源风险：%s %s", claim.Chapter, claim.Name, compactProgressText(firstNonEmpty(claim.Risk, claim.Status), 80)))
	}
	return out
}

func characterKnowledgeLedger(
	entry domain.CharacterContinuityEntry,
	secretFacts, misbeliefFacts []string,
	latest int,
) domain.CharacterKnowledgeLedger {
	known := limitStrings(entry.CurrentFacts, 4)
	if len(known) == 0 && entry.PlanningNote != "" {
		known = []string{compactProgressText(entry.PlanningNote, 120)}
	}
	unknown := []string{"未在当前人物台账、章节摘要或状态变化中确认的信息，不能当作此角色已知。"}
	if len(secretFacts) > 0 {
		unknown = append(unknown, secretFacts...)
	}
	forbidden := []string{"不得提前知道未公开秘密、其他角色内心、弱 RAG 召回或未由正文证据触发的规则答案。"}
	if len(secretFacts) > 0 {
		forbidden = append(forbidden, secretFacts...)
	}
	confidence := "medium：来自当前项目台账；缺少正文证据时按保守边界处理。"
	if len(known) == 0 {
		confidence = "low：缺少近期沉淀事实；写作时必须依赖本章可见证据。"
	}
	return domain.CharacterKnowledgeLedger{
		KnownFacts:         known,
		UnknownFacts:       limitStrings(unknown, 4),
		Suspicions:         limitStrings(misbeliefFacts, 3),
		FalseBeliefs:       limitStrings(misbeliefFacts, 3),
		EvidenceSeen:       known,
		Confidence:         confidence,
		SourceChapter:      max(entry.LastSeenChapter, latest),
		ForbiddenKnowledge: limitStrings(forbidden, 4),
	}
}

func characterDecisionFrame(
	card domain.Character,
	entry domain.CharacterContinuityEntry,
	goal, pressure, nextAction string,
	riskFacts, resources []string,
) domain.CharacterDecisionFrame {
	actionRule := characterActionBias(card, entry)
	available := []string{}
	if nextAction != "" {
		available = append(available, nextAction)
	}
	if len(resources) > 0 {
		available = append(available, "使用或核验当前资源："+compactProgressText(resources[0], 100))
	}
	if len(available) == 0 {
		available = append(available, "按当前目标和关系压力选择最小越界行动。")
	}
	rejected := []string{
		"证据不足时直接替他人确认、承诺或承担风险。",
		"为了情节推进突然知道未公开信息或忽略既有伤势/债务/资源限制。",
	}
	if entry.ReturnPlan.ReturnPriority == "optional" || entry.ReturnPlan.ReturnPriority == "dormant" {
		rejected = append(rejected, "没有新信息、新压力或关系变化时强行回归。")
	}
	tradeoff := firstNonEmpty(pressure, firstText(riskFacts), firstText(resources), goal)
	risk := firstNonEmpty(firstText(riskFacts), "承担信息不足、关系反噬或资源误用风险。")
	return domain.CharacterDecisionFrame{
		AvailableOptions:        limitStrings(available, 4),
		RejectedOptions:         rejected,
		DecisionRule:            actionRule,
		Tradeoff:                compactProgressText(tradeoff, 160),
		CostPaid:                "只有正文已经展示代价、资源扣减、关系变化或承诺绑定时才能写成已支付。",
		RiskAccepted:            compactProgressText(risk, 160),
		ExpectedGain:            compactProgressText(goal, 160),
		MinimumEvidenceRequired: characterMinimumEvidenceRequired(card),
	}
}

func characterMinimumEvidenceRequired(card domain.Character) string {
	text := strings.Join(append([]string{card.Role, card.Description, card.Arc}, card.Traits...), " ")
	switch {
	case containsAnyKeyword(text, []string{"风控", "契约", "交易", "审计", "账"}):
		return "必须先看到可核验凭证、条款、确认动作、账目或权利边界。"
	case containsAnyKeyword(text, []string{"官方", "队长", "秩序", "职责"}):
		return "必须先看到现场证据、职责授权或公共风险扩大迹象。"
	case containsAnyKeyword(text, []string{"怕", "普通人", "后勤"}):
		return "必须先确认自身安全、可执行动作和是否有人承担后果。"
	default:
		return "至少需要前文事实、现场证据或关系压力支持，不能只因大纲需要行动。"
	}
}

func characterRelationshipContracts(name string, relationships []domain.RelationshipEntry, limit int) []domain.CharacterRelationshipContract {
	out := []domain.CharacterRelationshipContract{}
	for i := len(relationships) - 1; i >= 0 && len(out) < limit; i-- {
		r := relationships[i]
		if r.CharacterA != name && r.CharacterB != name {
			continue
		}
		other := r.CharacterB
		if r.CharacterB == name {
			other = r.CharacterA
		}
		relation := compactProgressText(r.Relation, 120)
		contract := domain.CharacterRelationshipContract{
			Counterpart:       other,
			AllianceStatus:    relation,
			HelpCondition:     "只有能带来新信息、新压力、资源清账或关系位移时，才让此关系推动本章。",
			BetrayalThreshold: "若信任、债务、筹码或秘密被破坏，此关系必须转入拒绝、试探或反咬。",
			SourceChapter:     r.Chapter,
		}
		if containsAnyKeyword(r.Relation, []string{"信任", "托付", "合作", "盟友", "救"}) {
			contract.Trust = relation
		}
		if containsAnyKeyword(r.Relation, []string{"债", "欠", "还", "救命", "人情", "账"}) {
			contract.Debt = relation
		}
		if containsAnyKeyword(r.Relation, []string{"筹码", "证据", "把柄", "合同", "契约", "凭证", "权利"}) {
			contract.Leverage = relation
		}
		if containsAnyKeyword(r.Relation, []string{"承诺", "约定", "答应", "保证"}) {
			contract.Promise = relation
		}
		if containsAnyKeyword(r.Relation, []string{"秘密", "隐瞒", "身份", "密钥", "暗号", "旧账"}) {
			contract.SharedSecret = relation
		}
		if containsAnyKeyword(r.Relation, []string{"欺骗", "骗", "背叛", "出卖", "试探", "隐瞒"}) {
			contract.BetrayalRecord = relation
		}
		if containsAnyKeyword(r.Relation, []string{"依赖", "需要", "绑定", "照顾", "保护"}) {
			contract.Dependency = relation
		}
		if containsAnyKeyword(r.Relation, []string{"怕", "恐惧", "威胁", "惧", "压迫"}) {
			contract.FearSource = relation
		}
		out = append(out, contract)
	}
	return out
}

func characterEmotionAppraisal(card domain.Character, pressure, emotionalFact, riskFact, relationshipFact string) domain.CharacterEmotionAppraisal {
	trigger := firstNonEmpty(emotionalFact, riskFact, pressure, relationshipFact)
	actionPressure := firstNonEmpty(riskFact, pressure, relationshipFact)
	return domain.CharacterEmotionAppraisal{
		TriggerEvent:         compactProgressText(trigger, 120),
		GoalImpact:           compactProgressText(firstNonEmpty(pressure, trigger), 120),
		ThreatToValue:        characterThreatToValue(card),
		VisibleExpression:    characterVisibleExpression(card),
		SuppressedExpression: "不得直接用作者式总结替代情绪；优先通过停顿、动作、回避、追问或交易判断外化。",
		CopingStrategy:       characterCopingStrategy(card),
		ActionPressure:       compactProgressText(actionPressure, 140),
		RelationshipEffect:   compactProgressText(relationshipFact, 120),
	}
}

func characterThreatToValue(card domain.Character) string {
	text := strings.Join(append([]string{card.Role, card.Description, card.Arc}, card.Traits...), " ")
	switch {
	case containsAnyKeyword(text, []string{"妹妹", "家人", "亲情", "责任"}):
		return "亲人安全、责任边界和可控救援成本。"
	case containsAnyKeyword(text, []string{"风控", "契约", "交易", "审计", "账"}):
		return "交易边界、确认动作、可追责凭证和风险隔离。"
	case containsAnyKeyword(text, []string{"官方", "队长", "秩序", "职责"}):
		return "职责边界、公共风险和秩序可信度。"
	default:
		return "当前目标、身份稳定性、关系信任或生存安全。"
	}
}

func characterVisibleExpression(card domain.Character) string {
	text := strings.Join(append([]string{card.Role, card.Description, card.Arc}, card.Traits...), " ")
	switch {
	case containsAnyKeyword(text, []string{"风控", "契约", "交易", "审计", "账"}):
		return "先看条款、凭证和确认动作，台词偏短，少解释情绪。"
	case containsAnyKeyword(text, []string{"怕", "普通人", "后勤"}):
		return "先确认安全，抱怨、停顿或求助多于主动冒险。"
	case containsAnyKeyword(text, []string{"官方", "队长", "秩序", "职责"}):
		return "先确认现场和授权，再压住私人情绪。"
	default:
		return "用动作、沉默、避让、追问或微小选择体现，不直接喊情绪标签。"
	}
}

func characterCopingStrategy(card domain.Character) string {
	text := strings.Join(append([]string{card.Role, card.Description, card.Arc}, card.Traits...), " ")
	switch {
	case containsAnyKeyword(text, []string{"风控", "契约", "交易", "审计", "账"}):
		return "把恐惧转成证据核验、交易拆分和责任边界确认。"
	case containsAnyKeyword(text, []string{"怕", "普通人", "后勤"}):
		return "先找安全路径和可执行小动作，必要时用抱怨卸压。"
	case containsAnyKeyword(text, []string{"官方", "队长", "秩序", "职责"}):
		return "把情绪压进职责判断、队伍命令和风险分级。"
	default:
		return "用其长期行动倾向处理压力，避免突然转性或空泛宣言。"
	}
}

func characterArcAxis(card domain.Character, entry domain.CharacterContinuityEntry, goal, pressure, misbelief string, latest int) domain.CharacterArcAxis {
	text := strings.Join(append([]string{card.Role, card.Description, card.Arc}, card.Traits...), " ")
	stage := "推进阶段"
	if latest <= 1 || entry.LastSeenChapter <= 1 {
		stage = "开局阶段"
	}
	return domain.CharacterArcAxis{
		Want:             compactProgressText(goal, 140),
		Need:             characterArcNeed(text),
		WoundOrGhost:     characterWoundOrGhost(text),
		CoreLie:          compactProgressText(firstNonEmpty(misbelief, characterDefaultCoreLie(text)), 140),
		ValueAxis:        characterValueAxis(text),
		ArcStage:         stage,
		PressureTest:     compactProgressText(pressure, 140),
		GrowthSignal:     characterGrowthSignal(text),
		RegressionSignal: "为了推进事件而越过信息边界、忽略旧伤/债务/资源限制，或说出不属于此人的空泛金句。",
	}
}

func characterArcNeed(text string) string {
	switch {
	case containsAnyKeyword(text, []string{"风控", "契约", "交易", "审计", "账"}):
		return "把风险隔离和对人的责任同时纳入判断，不能只靠拒绝确认自保。"
	case containsAnyKeyword(text, []string{"妹妹", "家人", "亲情", "责任"}):
		return "把保护亲人的冲动转成可持续、可验证、能承担后果的选择。"
	case containsAnyKeyword(text, []string{"怕", "普通人", "后勤"}):
		return "从只求脱身，转向在可控范围内承担具体行动。"
	case containsAnyKeyword(text, []string{"官方", "队长", "秩序", "职责"}):
		return "在职责和真相冲突时重新确认自己的秩序边界。"
	default:
		return "在压力下形成稳定选择标准，而不是只完成情节任务。"
	}
}

func characterWoundOrGhost(text string) string {
	switch {
	case containsAnyKeyword(text, []string{"失业", "裁员", "职业创伤", "风控", "背锅"}):
		return "失业/风控经验留下的确认动作、责任归属和签字创伤。"
	case containsAnyKeyword(text, []string{"旧伤", "伤", "病", "寿命", "器官"}):
		return "身体伤病或寿命代价持续限制其选择。"
	case containsAnyKeyword(text, []string{"背叛", "旧债", "欠债", "旧账"}):
		return "旧债、背叛或未清账让其难以轻信。"
	default:
		return ""
	}
}

func characterDefaultCoreLie(text string) string {
	switch {
	case containsAnyKeyword(text, []string{"风控", "契约", "交易", "审计", "账"}):
		return "只要不确认、不签字、不承诺，就能完全保持安全。"
	case containsAnyKeyword(text, []string{"怕", "普通人", "后勤"}):
		return "只要躲开冲突，就不会被卷入更大的代价。"
	case containsAnyKeyword(text, []string{"官方", "队长", "秩序", "职责"}):
		return "只要按职责流程行动，秩序就一定还能托底。"
	default:
		return ""
	}
}

func characterValueAxis(text string) string {
	switch {
	case containsAnyKeyword(text, []string{"风控", "契约", "交易", "审计", "账"}):
		return "自保/责任，交易边界/人情压力，证据/冲动。"
	case containsAnyKeyword(text, []string{"妹妹", "家人", "亲情", "责任"}):
		return "亲情责任/风险隔离，救人冲动/可承受代价。"
	case containsAnyKeyword(text, []string{"官方", "队长", "秩序", "职责"}):
		return "职责秩序/现场真相，公共风险/私人判断。"
	default:
		return "欲望/需要，安全/代价，信任/怀疑。"
	}
}

func characterGrowthSignal(text string) string {
	switch {
	case containsAnyKeyword(text, []string{"风控", "契约", "交易", "审计", "账"}):
		return "在核验证据后承担有限责任，并让交易留下可审计后果。"
	case containsAnyKeyword(text, []string{"怕", "普通人", "后勤"}):
		return "不是突然勇敢，而是在安全路径内完成一个具体动作。"
	case containsAnyKeyword(text, []string{"官方", "队长", "秩序", "职责"}):
		return "承认流程不足，并用现场证据修正行动。"
	default:
		return "选择比上一阶段更具体、更有代价，也更符合已沉淀状态。"
	}
}

func characterActionBias(card domain.Character, entry domain.CharacterContinuityEntry) string {
	text := strings.Join(append([]string{card.Description, card.Arc}, card.Traits...), " ")
	switch {
	case containsAnyKeyword(text, []string{"风控", "契约", "交易", "冷静", "边界"}):
		return "先核验证据和权利边界，再决定是否行动；避免免费承诺和情绪化签字。"
	case containsAnyKeyword(text, []string{"怕", "胆小", "普通人", "后勤"}):
		return "先求安全和可执行动作，嘴上抱怨但会承担现实后勤。"
	case containsAnyKeyword(text, []string{"账", "债", "会计", "账房"}):
		return "优先核对账目、凭证和责任归属，不轻易表态。"
	case containsAnyKeyword(text, []string{"官方", "队长", "秩序", "职责"}):
		return "先按职责和公共风险判断，再在证据不足处保持警惕。"
	case entry.ArcDirection != "":
		return compactProgressText(entry.ArcDirection, 120)
	default:
		return "按已沉淀目标、压力和关系选择行动；不要只按人设标签机械反应。"
	}
}

func characterExposureLevel(entry domain.CharacterContinuityEntry, next, latest int) string {
	if len(entry.FutureUses) > 0 {
		use := entry.FutureUses[0]
		if use.Chapter == next {
			return "下一章强相关：可出场或至少带来信息/压力。"
		}
		if use.Chapter > next && use.Chapter <= next+5 {
			return "近期将回归：本章保留状态，不要写死。"
		}
	}
	if entry.LastSeenChapter >= latest-2 {
		return "近期活跃：可作为压力、后勤或关系反应来源。"
	}
	if entry.ReturnMode == "核心长线" {
		return "核心长线：可不出场，但状态必须持续有效。"
	}
	return "低曝光：除非大纲或场景需要，不主动加戏。"
}

func characterConflictVector(entry domain.CharacterContinuityEntry, relationships, risks []string) string {
	if len(risks) > 0 && len(relationships) > 0 {
		return compactProgressText(risks[0]+"；"+relationships[0], 180)
	}
	if len(risks) > 0 {
		return compactProgressText(risks[0], 180)
	}
	if len(relationships) > 0 {
		return compactProgressText(relationships[0], 180)
	}
	return compactProgressText(entry.PlanningNote, 180)
}

func characterReturnPlan(entry domain.CharacterContinuityEntry, next, latest int) domain.CharacterReturnPlan {
	plan := domain.CharacterReturnPlan{
		DueReason:        entry.PlanningNote,
		UpgradePotential: characterUpgradePotential(entry),
	}
	if len(entry.FutureUses) > 0 {
		use := entry.FutureUses[0]
		plan.SuggestedChapter = use.Chapter
		plan.WithNewInformation = use.Action
		switch {
		case use.Chapter == next:
			plan.ReturnPriority = "required"
		case use.Chapter > next && use.Chapter <= next+5:
			plan.ReturnPriority = "near_future"
		default:
			plan.ReturnPriority = "planned_later"
		}
		return plan
	}
	switch entry.ReturnMode {
	case "核心长线":
		plan.ReturnPriority = "background_active"
	case "偶发露脸候选":
		plan.ReturnPriority = "optional"
		plan.WithNewInformation = "仅在客户群体、后勤现场、官方行动或压力样本中带具体新信息回归。"
	default:
		plan.ReturnPriority = "dormant"
		plan.RetireReason = "缺少未来大纲续用点；保留既成事实，不主动加戏。"
	}
	return plan
}

func characterUpgradePotential(entry domain.CharacterContinuityEntry) string {
	if entry.Source == "cast_ledger" && entry.AppearanceCount >= 5 {
		return "高频配角：若后续仍参与关键冲突，建议升格进 characters.json 或弧摘要。"
	}
	if entry.Source == "cast_ledger" && entry.AppearanceCount >= 3 {
		return "可从事件工具人升级为长期变量，但必须携带新信息或新压力。"
	}
	if entry.ReturnMode == "大纲明确回归" {
		return "已由大纲安排续用；回归时必须带新信息、关系变化或压力升级。"
	}
	return ""
}

func characterConsistencyChecks(entry domain.CharacterContinuityEntry) []string {
	checks := []string{
		"写此人物前先核对 dynamics.current_goal、primary_pressure、resources、relationship_forces、knowledge_ledger 和 decision_frame。",
		"不得让此人物知道 knowledge_ledger.forbidden_knowledge 中的信息；秘密/误判只能通过正文证据变化。",
		"关键行动必须能解释 available_options、rejected_options、decision_rule、tradeoff 和 minimum_evidence_required。",
		"本章若改变目标、关系、资源、伤势、债务、秘密暴露度、行动倾向、知识边界、情绪评价、关系契约或弧线阶段，commit_chapter.state_changes/relationship_changes/resource_updates 必须回填。",
	}
	if entry.Dynamics.ActionBias != "" {
		checks = append(checks, "行动选择必须能从 action_bias 推出："+compactProgressText(entry.Dynamics.ActionBias, 80))
	}
	if entry.ReturnPlan.ReturnPriority == "optional" || entry.ReturnPlan.ReturnPriority == "dormant" {
		checks = append(checks, "非必要回归时必须带新信息/新压力，否则不要为露脸加戏。")
	}
	return checks
}

func containsAnyKeyword(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if keyword != "" && strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func firstText(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) <= limit {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[:limit]...)
}

func (s *Store) inferCastBriefRole(name string, aliases []string, chapters []int) string {
	names := append([]string{name}, aliases...)
	var listedFallback string
	for i := len(chapters) - 1; i >= 0; i-- {
		summary, err := s.Summaries.LoadSummary(chapters[i])
		if err != nil || summary == nil {
			continue
		}
		if snippet := characterSnippetFromText(summary.Summary, names, 90); snippet != "" {
			return fmt.Sprintf("由第%d章摘要推断：%s", chapters[i], snippet)
		}
		for _, event := range summary.KeyEvents {
			if snippet := characterSnippetFromText(event, names, 90); snippet != "" {
				return fmt.Sprintf("由第%d章关键事件推断：%s", chapters[i], snippet)
			}
		}
		if listedFallback == "" && summaryListsCharacter(summary, names) && strings.TrimSpace(summary.Summary) != "" {
			listedFallback = fmt.Sprintf("由第%d章出场名单推断：%s", chapters[i], compactProgressText(summary.Summary, 90))
		}
	}
	return listedFallback
}

func summaryListsCharacter(summary *domain.ChapterSummary, names []string) bool {
	if summary == nil {
		return false
	}
	for _, character := range summary.Characters {
		if containsString(names, character) {
			return true
		}
	}
	return false
}

func (s *Store) characterFutureUses(
	name string,
	aliases []string,
	outlineByChapter map[int]domain.OutlineEntry,
	positionByChapter map[int]domain.ChapterPosition,
	layered []domain.VolumeOutline,
	afterChapter int,
	limit int,
) []domain.CharacterFutureUse {
	if limit <= 0 {
		return nil
	}
	names := append([]string{name}, aliases...)
	var chapters []int
	for ch := range outlineByChapter {
		if ch > afterChapter {
			chapters = append(chapters, ch)
		}
	}
	sort.Ints(chapters)
	var uses []domain.CharacterFutureUse
	for _, ch := range chapters {
		if len(uses) >= limit {
			return uses
		}
		outline := outlineByChapter[ch]
		text := outlineSearchText(outline)
		if !containsAnyName(text, names) {
			continue
		}
		uses = append(uses, domain.CharacterFutureUse{
			Chapter:   ch,
			Title:     outline.Title,
			Position:  positionByChapter[ch],
			UsageType: "outline_return",
			Action:    outlineMatchedAction(outline, names),
			Evidence:  fmt.Sprintf("outline 第%d章", ch),
		})
	}
	if len(uses) >= limit {
		return uses
	}
	uses = append(uses, arcLevelFutureUses(names, layered, afterChapter, limit-len(uses))...)
	return uses
}

func outlineSearchText(outline domain.OutlineEntry) string {
	return strings.Join([]string{
		outline.Title,
		outline.CoreEvent,
		outline.Hook,
		strings.Join(outline.Scenes, " "),
	}, " ")
}

func outlineMatchedAction(outline domain.OutlineEntry, names []string) string {
	var matches []string
	for _, segment := range outlineActionSegments(outline) {
		if containsAnyName(segment, names) && !containsString(matches, segment) {
			matches = append(matches, segment)
		}
		if len(matches) >= 3 {
			break
		}
	}
	if len(matches) > 0 {
		return compactProgressText(strings.Join(matches, "；"), 220)
	}
	return compactProgressText(firstNonEmpty(outline.CoreEvent, strings.Join(outline.Scenes, "；"), outline.Hook, outline.Title), 220)
}

func outlineActionSegments(outline domain.OutlineEntry) []string {
	var segments []string
	addTextSegments := func(text string) {
		for _, segment := range splitPlanningText(text) {
			segment = strings.TrimSpace(segment)
			if segment != "" {
				segments = append(segments, segment)
			}
		}
	}
	addTextSegments(outline.CoreEvent)
	for _, scene := range outline.Scenes {
		addTextSegments(scene)
	}
	addTextSegments(outline.Hook)
	return segments
}

func splitPlanningText(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '。', '；', ';', '！', '!', '？', '?', '\n', '\r':
			return true
		default:
			return false
		}
	})
}

func characterSnippetFromText(text string, names []string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" || !containsAnyName(text, names) {
		return ""
	}
	for _, segment := range splitPlanningText(text) {
		segment = strings.TrimSpace(segment)
		if containsAnyName(segment, names) {
			return compactProgressText(segment, limit)
		}
	}
	return compactProgressText(text, limit)
}

func arcLevelFutureUses(names []string, layered []domain.VolumeOutline, afterChapter, limit int) []domain.CharacterFutureUse {
	var out []domain.CharacterFutureUse
	if limit <= 0 {
		return nil
	}
	globalCh := 1
	for _, v := range layered {
		for _, a := range v.Arcs {
			arcStart := globalCh
			arcCount := len(a.Chapters)
			if arcCount == 0 {
				arcCount = a.EstimatedChapters
			}
			if arcStart+max(arcCount, 1)-1 <= afterChapter {
				globalCh += arcCount
				continue
			}
			text := a.Title + " " + a.Goal
			if containsAnyName(text, names) {
				useChapter := arcStart
				if useChapter <= afterChapter {
					useChapter = afterChapter + 1
				}
				out = append(out, domain.CharacterFutureUse{
					Chapter: useChapter,
					Title:   a.Title,
					Position: domain.ChapterPosition{
						Volume:      v.Index,
						VolumeTitle: v.Title,
						Arc:         a.Index,
						ArcTitle:    a.Title,
						ArcGoal:     a.Goal,
					},
					UsageType: "arc_plan",
					Action:    compactProgressText(a.Goal, 180),
					Evidence:  fmt.Sprintf("layered_outline V%dA%d", v.Index, a.Index),
				})
				if len(out) >= limit {
					return out
				}
			}
			globalCh += arcCount
		}
	}
	return out
}

func characterReturnMode(entry domain.CharacterContinuityEntry, core bool) (string, string) {
	if len(entry.FutureUses) > 0 {
		use := entry.FutureUses[0]
		return "大纲明确回归", fmt.Sprintf("按%s在%s续用：%s", use.Evidence, characterUseTarget(use), use.Action)
	}
	if core {
		if entry.ArcDirection != "" {
			return "核心长线", "角色弧线仍未完成；后续按章节冲突自然推进，不要为了露脸硬塞。"
		}
		return "核心长线", "核心角色默认参与主线判断；若当前章不适合出场，保留为背景状态。"
	}
	if entry.AppearanceCount >= 3 || entry.LastSeenChapter > 0 {
		return "偶发露脸候选", "可在客户群体、后勤现场、官方行动或压力样本中短暂露面；无合适场景时不强求。"
	}
	return "暂不强推", "目前只保留为已出现事实，除非后续大纲或场景需要，否则不要主动加戏。"
}

func characterUseTarget(use domain.CharacterFutureUse) string {
	if use.Chapter > 0 {
		if use.Title != "" {
			return fmt.Sprintf("第%d章《%s》", use.Chapter, use.Title)
		}
		return fmt.Sprintf("第%d章", use.Chapter)
	}
	if use.Position.ArcTitle != "" {
		return use.Position.ArcTitle
	}
	return "后续弧线"
}

func characterModeRank(entry domain.CharacterContinuityEntry) int {
	switch entry.ReturnMode {
	case "大纲明确回归":
		return 0
	case "核心长线":
		return 1
	case "偶发露脸候选":
		return 2
	default:
		return 3
	}
}

func nextCharacterHints(entries []domain.CharacterContinuityEntry, next, latest, limit int) []domain.CharacterHint {
	if limit <= 0 {
		return nil
	}
	var hints []domain.CharacterHint
	add := func(entry domain.CharacterContinuityEntry, usageType, suggestion, evidence string) {
		if len(hints) >= limit || suggestion == "" {
			return
		}
		for _, h := range hints {
			if h.Name == entry.Name {
				return
			}
		}
		hints = append(hints, domain.CharacterHint{
			Name:       entry.Name,
			UsageType:  usageType,
			Suggestion: suggestion,
			Evidence:   evidence,
			ReviewNote: characterReviewPolicy,
		})
	}
	for _, entry := range entries {
		for _, use := range entry.FutureUses {
			if use.Chapter == next && use.UsageType != "arc_plan" {
				add(entry, use.UsageType, use.Action, use.Evidence)
			}
		}
	}
	for _, entry := range entries {
		if len(hints) >= limit {
			return hints
		}
		if entry.ReturnMode == "大纲明确回归" && len(entry.FutureUses) > 0 && entry.FutureUses[0].Chapter > next && entry.FutureUses[0].Chapter <= next+5 {
			use := entry.FutureUses[0]
			add(entry, "near_future", fmt.Sprintf("近期将在%s回归；本章只需避免写死其状态。%s", characterUseTarget(use), use.Action), use.Evidence)
		}
	}
	for _, entry := range entries {
		if len(hints) >= limit {
			return hints
		}
		for _, use := range entry.FutureUses {
			if use.Chapter == next && use.UsageType == "arc_plan" {
				add(entry, use.UsageType, fmt.Sprintf("当前弧级大纲仍涉及此人物；本章不强制出场，只需保留状态不被写死。%s", use.Action), use.Evidence)
				break
			}
		}
	}
	for _, entry := range entries {
		if len(hints) >= limit {
			return hints
		}
		if entry.ReturnMode == "偶发露脸候选" && entry.LastSeenChapter >= max(1, latest-6) {
			add(entry, "optional_cameo", entry.PlanningNote, "cast_ledger 近期出现")
		}
	}
	return hints
}

func renderCharacterContinuityLedger(ledger *domain.CharacterContinuityLedger) string {
	var b strings.Builder
	b.WriteString("# 人物回归与续用规划台账\n\n")
	if ledger.NovelName != "" {
		fmt.Fprintf(&b, "- 书名：%s\n", ledger.NovelName)
	}
	fmt.Fprintf(&b, "- 当前工程章节：第 %d 章\n", ledger.CurrentChapter)
	fmt.Fprintf(&b, "- 生成时间：%s\n", ledger.GeneratedAt)
	fmt.Fprintf(&b, "- 使用边界：%s\n\n", ledger.ReviewPolicy)

	if len(ledger.NextChapterFocus) > 0 {
		b.WriteString("## 下一章人物续用参考（非审核项）\n\n")
		for _, h := range ledger.NextChapterFocus {
			fmt.Fprintf(&b, "- **%s** [%s]：%s", h.Name, h.UsageType, h.Suggestion)
			if h.Evidence != "" {
				fmt.Fprintf(&b, "（证据：%s）", h.Evidence)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## 总表\n\n")
	b.WriteString("| 人物 | 来源 | 最近出场 | 模式 | 后续规划 |\n")
	b.WriteString("|---|---|---:|---|---|\n")
	for _, e := range ledger.Entries {
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s |\n",
			escapeTable(e.Name),
			escapeTable(firstNonEmpty(e.Role, e.BriefRole, e.Source)),
			e.LastSeenChapter,
			escapeTable(e.ReturnMode),
			escapeTable(e.PlanningNote),
		)
	}
	b.WriteString("\n")

	for _, e := range ledger.Entries {
		fmt.Fprintf(&b, "## %s\n\n", e.Name)
		if e.Role != "" || e.BriefRole != "" {
			fmt.Fprintf(&b, "- 定位：%s\n", firstNonEmpty(e.Role, e.BriefRole))
		}
		if len(e.AppearanceChapters) > 0 {
			fmt.Fprintf(&b, "- 出场章节：%s\n", intsInline(e.AppearanceChapters))
		}
		fmt.Fprintf(&b, "- 回归模式：%s\n", e.ReturnMode)
		fmt.Fprintf(&b, "- 规划说明：%s\n", e.PlanningNote)
		if e.ArcDirection != "" {
			fmt.Fprintf(&b, "- 角色弧线方向：%s\n", e.ArcDirection)
		}
		if hasCharacterDynamics(e.Dynamics) {
			b.WriteString("- 角色动力学：\n")
			if e.Dynamics.CurrentGoal != "" {
				fmt.Fprintf(&b, "  - 当前目标：%s\n", e.Dynamics.CurrentGoal)
			}
			if e.Dynamics.PrimaryPressure != "" {
				fmt.Fprintf(&b, "  - 主要压力：%s\n", e.Dynamics.PrimaryPressure)
			}
			if len(e.Dynamics.Resources) > 0 {
				fmt.Fprintf(&b, "  - 可用/牵制资源：%s\n", strings.Join(e.Dynamics.Resources, "；"))
			}
			if len(e.Dynamics.RelationshipForces) > 0 {
				fmt.Fprintf(&b, "  - 关系牵引：%s\n", strings.Join(e.Dynamics.RelationshipForces, "；"))
			}
			if len(e.Dynamics.Secrets) > 0 {
				fmt.Fprintf(&b, "  - 秘密/未公开压力：%s\n", strings.Join(e.Dynamics.Secrets, "；"))
			}
			if len(e.Dynamics.Misbeliefs) > 0 {
				fmt.Fprintf(&b, "  - 误判/信息边界：%s\n", strings.Join(e.Dynamics.Misbeliefs, "；"))
			}
			if e.Dynamics.ActionBias != "" {
				fmt.Fprintf(&b, "  - 行动倾向：%s\n", e.Dynamics.ActionBias)
			}
			if e.Dynamics.NextLikelyAction != "" {
				fmt.Fprintf(&b, "  - 下一步合理行动：%s\n", e.Dynamics.NextLikelyAction)
			}
			if e.Dynamics.ConflictVector != "" {
				fmt.Fprintf(&b, "  - 冲突咬合点：%s\n", e.Dynamics.ConflictVector)
			}
			if hasKnowledgeLedger(e.Dynamics.KnowledgeLedger) {
				writeKnowledgeLedger(&b, e.Dynamics.KnowledgeLedger)
			}
			if hasDecisionFrame(e.Dynamics.DecisionFrame) {
				writeDecisionFrame(&b, e.Dynamics.DecisionFrame)
			}
			if len(e.Dynamics.RelationshipContract) > 0 {
				writeRelationshipContracts(&b, e.Dynamics.RelationshipContract)
			}
			if hasEmotionAppraisal(e.Dynamics.EmotionAppraisal) {
				writeEmotionAppraisal(&b, e.Dynamics.EmotionAppraisal)
			}
			if hasArcAxis(e.Dynamics.ArcAxis) {
				writeArcAxis(&b, e.Dynamics.ArcAxis)
			}
		}
		if hasCharacterReturnPlan(e.ReturnPlan) {
			b.WriteString("- 回归/续用计划：")
			fmt.Fprintf(&b, "优先级=%s", e.ReturnPlan.ReturnPriority)
			if e.ReturnPlan.SuggestedChapter > 0 {
				fmt.Fprintf(&b, "，建议章节=%d", e.ReturnPlan.SuggestedChapter)
			}
			if e.ReturnPlan.WithNewInformation != "" {
				fmt.Fprintf(&b, "，携带信息=%s", compactProgressText(e.ReturnPlan.WithNewInformation, 120))
			}
			if e.ReturnPlan.RetireReason != "" {
				fmt.Fprintf(&b, "，退场理由=%s", e.ReturnPlan.RetireReason)
			}
			b.WriteString("\n")
		}
		if len(e.ConsistencyChecks) > 0 {
			fmt.Fprintf(&b, "- 行为一致性检查：%s\n", strings.Join(e.ConsistencyChecks, "；"))
		}
		if len(e.CurrentFacts) > 0 {
			fmt.Fprintf(&b, "- 当前沉淀事实：%s\n", strings.Join(e.CurrentFacts, "；"))
		}
		if len(e.FutureUses) > 0 {
			b.WriteString("- 大纲续用点：\n")
			for _, use := range e.FutureUses {
				fmt.Fprintf(&b, "  - %s [%s]：%s", characterUseTarget(use), use.UsageType, use.Action)
				if use.Evidence != "" {
					fmt.Fprintf(&b, "（%s）", use.Evidence)
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func writeKnowledgeLedger(b *strings.Builder, ledger domain.CharacterKnowledgeLedger) {
	if !hasKnowledgeLedger(ledger) {
		return
	}
	parts := []string{}
	if len(ledger.KnownFacts) > 0 {
		parts = append(parts, "已知="+strings.Join(ledger.KnownFacts, "；"))
	}
	if len(ledger.UnknownFacts) > 0 {
		parts = append(parts, "未知="+strings.Join(ledger.UnknownFacts, "；"))
	}
	if len(ledger.Suspicions) > 0 {
		parts = append(parts, "怀疑="+strings.Join(ledger.Suspicions, "；"))
	}
	if len(ledger.FalseBeliefs) > 0 {
		parts = append(parts, "误信="+strings.Join(ledger.FalseBeliefs, "；"))
	}
	if len(ledger.EvidenceSeen) > 0 {
		parts = append(parts, "证据="+strings.Join(ledger.EvidenceSeen, "；"))
	}
	if ledger.Confidence != "" {
		parts = append(parts, "置信="+ledger.Confidence)
	}
	if ledger.SourceChapter > 0 {
		parts = append(parts, fmt.Sprintf("来源章=%d", ledger.SourceChapter))
	}
	if len(ledger.ForbiddenKnowledge) > 0 {
		parts = append(parts, "禁知="+strings.Join(ledger.ForbiddenKnowledge, "；"))
	}
	if len(parts) > 0 {
		fmt.Fprintf(b, "  - 知识账本：%s\n", strings.Join(parts, "；"))
	}
}

func writeDecisionFrame(b *strings.Builder, frame domain.CharacterDecisionFrame) {
	if !hasDecisionFrame(frame) {
		return
	}
	parts := []string{}
	if len(frame.AvailableOptions) > 0 {
		parts = append(parts, "可选="+strings.Join(frame.AvailableOptions, "；"))
	}
	if len(frame.RejectedOptions) > 0 {
		parts = append(parts, "拒选="+strings.Join(frame.RejectedOptions, "；"))
	}
	if frame.DecisionRule != "" {
		parts = append(parts, "规则="+frame.DecisionRule)
	}
	if frame.Tradeoff != "" {
		parts = append(parts, "权衡="+frame.Tradeoff)
	}
	if frame.CostPaid != "" {
		parts = append(parts, "代价="+frame.CostPaid)
	}
	if frame.RiskAccepted != "" {
		parts = append(parts, "接受风险="+frame.RiskAccepted)
	}
	if frame.ExpectedGain != "" {
		parts = append(parts, "收益="+frame.ExpectedGain)
	}
	if frame.MinimumEvidenceRequired != "" {
		parts = append(parts, "行动前证据="+frame.MinimumEvidenceRequired)
	}
	if len(parts) > 0 {
		fmt.Fprintf(b, "  - 决策框架：%s\n", strings.Join(parts, "；"))
	}
}

func writeRelationshipContracts(b *strings.Builder, contracts []domain.CharacterRelationshipContract) {
	if len(contracts) == 0 {
		return
	}
	var rows []string
	for _, c := range contracts {
		parts := []string{}
		if c.Counterpart != "" {
			parts = append(parts, "对象="+c.Counterpart)
		}
		if c.Trust != "" {
			parts = append(parts, "信任="+c.Trust)
		}
		if c.Debt != "" {
			parts = append(parts, "债务="+c.Debt)
		}
		if c.Leverage != "" {
			parts = append(parts, "筹码="+c.Leverage)
		}
		if c.Promise != "" {
			parts = append(parts, "承诺="+c.Promise)
		}
		if c.SharedSecret != "" {
			parts = append(parts, "共同秘密="+c.SharedSecret)
		}
		if c.BetrayalRecord != "" {
			parts = append(parts, "欺骗记录="+c.BetrayalRecord)
		}
		if c.Dependency != "" {
			parts = append(parts, "依赖="+c.Dependency)
		}
		if c.FearSource != "" {
			parts = append(parts, "恐惧源="+c.FearSource)
		}
		if c.AllianceStatus != "" {
			parts = append(parts, "同盟状态="+c.AllianceStatus)
		}
		if c.BetrayalThreshold != "" {
			parts = append(parts, "背叛阈值="+c.BetrayalThreshold)
		}
		if c.HelpCondition != "" {
			parts = append(parts, "帮助条件="+c.HelpCondition)
		}
		if c.SourceChapter > 0 {
			parts = append(parts, fmt.Sprintf("来源章=%d", c.SourceChapter))
		}
		if len(parts) > 0 {
			rows = append(rows, strings.Join(parts, "，"))
		}
	}
	if len(rows) > 0 {
		fmt.Fprintf(b, "  - 关系契约：%s\n", strings.Join(rows, "；"))
	}
}

func writeEmotionAppraisal(b *strings.Builder, appraisal domain.CharacterEmotionAppraisal) {
	if !hasEmotionAppraisal(appraisal) {
		return
	}
	parts := []string{}
	if appraisal.TriggerEvent != "" {
		parts = append(parts, "触发="+appraisal.TriggerEvent)
	}
	if appraisal.GoalImpact != "" {
		parts = append(parts, "目标影响="+appraisal.GoalImpact)
	}
	if appraisal.ThreatToValue != "" {
		parts = append(parts, "价值威胁="+appraisal.ThreatToValue)
	}
	if appraisal.VisibleExpression != "" {
		parts = append(parts, "外显="+appraisal.VisibleExpression)
	}
	if appraisal.SuppressedExpression != "" {
		parts = append(parts, "压抑="+appraisal.SuppressedExpression)
	}
	if appraisal.CopingStrategy != "" {
		parts = append(parts, "应对="+appraisal.CopingStrategy)
	}
	if appraisal.ActionPressure != "" {
		parts = append(parts, "行动压力="+appraisal.ActionPressure)
	}
	if appraisal.RelationshipEffect != "" {
		parts = append(parts, "关系影响="+appraisal.RelationshipEffect)
	}
	if len(parts) > 0 {
		fmt.Fprintf(b, "  - 情绪评价：%s\n", strings.Join(parts, "；"))
	}
}

func writeArcAxis(b *strings.Builder, axis domain.CharacterArcAxis) {
	if !hasArcAxis(axis) {
		return
	}
	parts := []string{}
	if axis.Want != "" {
		parts = append(parts, "want="+axis.Want)
	}
	if axis.Need != "" {
		parts = append(parts, "need="+axis.Need)
	}
	if axis.WoundOrGhost != "" {
		parts = append(parts, "旧伤="+axis.WoundOrGhost)
	}
	if axis.CoreLie != "" {
		parts = append(parts, "核心误信="+axis.CoreLie)
	}
	if axis.ValueAxis != "" {
		parts = append(parts, "价值轴="+axis.ValueAxis)
	}
	if axis.ArcStage != "" {
		parts = append(parts, "阶段="+axis.ArcStage)
	}
	if axis.PressureTest != "" {
		parts = append(parts, "本章测试="+axis.PressureTest)
	}
	if axis.GrowthSignal != "" {
		parts = append(parts, "成长信号="+axis.GrowthSignal)
	}
	if axis.RegressionSignal != "" {
		parts = append(parts, "倒退信号="+axis.RegressionSignal)
	}
	if len(parts) > 0 {
		fmt.Fprintf(b, "  - 长期弧线轴：%s\n", strings.Join(parts, "；"))
	}
}

func hasCharacterDynamics(profile domain.CharacterDynamicsProfile) bool {
	return profile.CurrentGoal != "" ||
		profile.PrimaryPressure != "" ||
		len(profile.Resources) > 0 ||
		len(profile.RelationshipForces) > 0 ||
		len(profile.Secrets) > 0 ||
		len(profile.Misbeliefs) > 0 ||
		profile.ActionBias != "" ||
		profile.RiskPressure != "" ||
		profile.EmotionalState != "" ||
		profile.PhysicalState != "" ||
		profile.ExposureLevel != "" ||
		profile.NextLikelyAction != "" ||
		profile.ConflictVector != "" ||
		hasKnowledgeLedger(profile.KnowledgeLedger) ||
		hasDecisionFrame(profile.DecisionFrame) ||
		len(profile.RelationshipContract) > 0 ||
		hasEmotionAppraisal(profile.EmotionAppraisal) ||
		hasArcAxis(profile.ArcAxis)
}

func hasKnowledgeLedger(ledger domain.CharacterKnowledgeLedger) bool {
	return len(ledger.KnownFacts) > 0 ||
		len(ledger.UnknownFacts) > 0 ||
		len(ledger.Suspicions) > 0 ||
		len(ledger.FalseBeliefs) > 0 ||
		len(ledger.EvidenceSeen) > 0 ||
		ledger.Confidence != "" ||
		ledger.SourceChapter > 0 ||
		len(ledger.ForbiddenKnowledge) > 0
}

func hasDecisionFrame(frame domain.CharacterDecisionFrame) bool {
	return len(frame.AvailableOptions) > 0 ||
		len(frame.RejectedOptions) > 0 ||
		frame.DecisionRule != "" ||
		frame.Tradeoff != "" ||
		frame.CostPaid != "" ||
		frame.RiskAccepted != "" ||
		frame.ExpectedGain != "" ||
		frame.MinimumEvidenceRequired != ""
}

func hasEmotionAppraisal(appraisal domain.CharacterEmotionAppraisal) bool {
	return appraisal.TriggerEvent != "" ||
		appraisal.GoalImpact != "" ||
		appraisal.ThreatToValue != "" ||
		appraisal.VisibleExpression != "" ||
		appraisal.SuppressedExpression != "" ||
		appraisal.CopingStrategy != "" ||
		appraisal.ActionPressure != "" ||
		appraisal.RelationshipEffect != ""
}

func hasArcAxis(axis domain.CharacterArcAxis) bool {
	return axis.Want != "" ||
		axis.Need != "" ||
		axis.WoundOrGhost != "" ||
		axis.CoreLie != "" ||
		axis.ValueAxis != "" ||
		axis.ArcStage != "" ||
		axis.PressureTest != "" ||
		axis.GrowthSignal != "" ||
		axis.RegressionSignal != ""
}

func hasCharacterReturnPlan(plan domain.CharacterReturnPlan) bool {
	return plan.ReturnPriority != "" ||
		plan.SuggestedChapter > 0 ||
		plan.DueReason != "" ||
		plan.WithNewInformation != "" ||
		plan.UpgradePotential != "" ||
		plan.RetireReason != ""
}

func intsInline(values []int) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%d", v))
	}
	return strings.Join(parts, "、")
}

func containsAnyName(text string, names []string) bool {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && strings.Contains(text, name) {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func maxCompletedChapter(completed []int) int {
	maxCh := 0
	for _, ch := range completed {
		if ch > maxCh {
			maxCh = ch
		}
	}
	return maxCh
}
