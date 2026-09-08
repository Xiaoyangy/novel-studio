package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

const (
	CurrentBookWorldSchemaVersion = 2
	WorldCoherenceReportVersion   = 1
	WorldCoherenceProtocol        = "world-coherence.v1"
	WorldCoherenceSeverityError   = "error"
	WorldCoherenceSeverityWarning = "warning"
)

// WorldCoherenceFinding 是可稳定排序、可被 CLI/Dashboard 消费的世界问题。
// Subject 使用 JSON 风格路径，避免把整份设定重复塞进错误文本。
type WorldCoherenceFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Subject  string `json:"subject,omitempty"`
	Message  string `json:"message"`
}

type WorldCoherenceStats struct {
	WorldRules          int `json:"world_rules"`
	CodexSections       int `json:"codex_sections"`
	Mechanisms          int `json:"mechanisms"`
	CounterfactualTests int `json:"counterfactual_tests"`
	Places              int `json:"places"`
	Routes              int `json:"routes"`
	Factions            int `json:"factions"`
	ConnectedComponents int `json:"connected_components"`
}

// WorldCoherenceReport 是 Architect 对三份世界源文件做出的确定性证明。
// SourceDigest 绑定 world_rules + world_codex + book_world 的作者设定；
// 势力钟 progress 是 world tick 的运行态，不会伪装成设定漂移。ReportDigest
// 再绑定检查结果本身。报告不含时间戳，因此相同输入始终产生完全相同的字节和摘要。
type WorldCoherenceReport struct {
	Version      int                     `json:"version"`
	Protocol     string                  `json:"protocol"`
	Ready        bool                    `json:"ready"`
	SourceDigest string                  `json:"source_digest"`
	ReportDigest string                  `json:"report_digest"`
	Stats        WorldCoherenceStats     `json:"stats"`
	Findings     []WorldCoherenceFinding `json:"findings,omitempty"`
}

// BlockingIssues 返回用于门禁的人类可读错误，不包含 warning。
func (r WorldCoherenceReport) BlockingIssues() []string {
	var out []string
	for _, finding := range r.Findings {
		if finding.Severity == WorldCoherenceSeverityError {
			out = append(out, formatWorldCoherenceFinding(finding))
		}
	}
	return out
}

// Warnings 返回兼容性或建模质量提示，不阻断旧 v1 项目。
func (r WorldCoherenceReport) Warnings() []string {
	var out []string
	for _, finding := range r.Findings {
		if finding.Severity == WorldCoherenceSeverityWarning {
			out = append(out, formatWorldCoherenceFinding(finding))
		}
	}
	return out
}

func formatWorldCoherenceFinding(finding WorldCoherenceFinding) string {
	if strings.TrimSpace(finding.Subject) == "" {
		return finding.Message
	}
	return finding.Subject + "：" + finding.Message
}

// AuditWorldCoherence 对世界源文件做纯函数审计。旧 v1 工件仍按原口径读取，
// 但缺少 v2 操作合同的部分会成为 warning；新 v2 工件对应缺口为 error。
func AuditWorldCoherence(rules []WorldRule, codex *WorldCodex, world *BookWorld) WorldCoherenceReport {
	report := WorldCoherenceReport{
		Version:  WorldCoherenceReportVersion,
		Protocol: WorldCoherenceProtocol,
		Stats: WorldCoherenceStats{
			WorldRules: len(rules),
		},
	}
	report.SourceDigest = worldCoherenceDigest(struct {
		WorldRules []WorldRule `json:"world_rules"`
		WorldCodex *WorldCodex `json:"world_codex"`
		BookWorld  *BookWorld  `json:"book_world"`
	}{rules, codex, worldCoherenceFoundationWorld(world)})

	findings := make([]WorldCoherenceFinding, 0)
	add := func(code, severity, subject, message string) {
		findings = append(findings, WorldCoherenceFinding{
			Code: code, Severity: severity, Subject: subject, Message: message,
		})
	}

	strictCodex := codex != nil && codex.SchemaVersion >= CurrentWorldCodexSchemaVersion
	strictCharacterViews := codex != nil && codex.CharacterViewVersion == CurrentWorldCharacterViewVersion
	strictWorld := world != nil && world.Version >= CurrentBookWorldSchemaVersion
	strictOperational := strictCodex || strictWorld
	operationalSeverity := func(strict bool) string {
		if strict {
			return WorldCoherenceSeverityError
		}
		return WorldCoherenceSeverityWarning
	}

	if len(rules) == 0 {
		add("world_rules.missing", WorldCoherenceSeverityError, "world_rules", "至少需要一条世界规则")
	}
	seenRules := map[string]struct{}{}
	for i, rule := range rules {
		subject := fmt.Sprintf("world_rules[%d]", i)
		if strings.TrimSpace(rule.Rule) == "" {
			add("world_rules.rule.empty", WorldCoherenceSeverityError, subject+".rule", "规则正文不能为空")
		}
		if strings.TrimSpace(rule.Category) == "" {
			add("world_rules.category.empty", operationalSeverity(strictOperational), subject+".category", "v2 规则需要稳定类别")
		}
		if strings.TrimSpace(rule.Boundary) == "" {
			add("world_rules.boundary.empty", operationalSeverity(strictOperational), subject+".boundary", "规则必须写明不可突破的边界")
		}
		if strictCharacterViews && WorldRuleVisibility(rule) != "secret" && strings.TrimSpace(rule.CharacterView) == "" {
			add("world_rules.character_view.empty", WorldCoherenceSeverityError, subject+".character_view",
				"角色视图 v1 的非 secret 规则必须提供独立公开文本；不能回退到含作者事实的 rule/boundary")
		}
		key := strings.TrimSpace(rule.Category) + "\x00" + strings.TrimSpace(rule.Rule)
		if key != "\x00" {
			if _, exists := seenRules[key]; exists {
				add("world_rules.duplicate", WorldCoherenceSeverityError, subject, "与前一条规则重复")
			}
			seenRules[key] = struct{}{}
		}
	}

	activeSections := map[string]struct{}{}
	mechanismIDs := map[string]struct{}{}
	if codex == nil {
		add("world_codex.missing", WorldCoherenceSeverityError, "world_codex", "世界法典不存在或不可读")
	} else {
		report.Stats.CodexSections = len(codex.Sections)
		report.Stats.Mechanisms = len(codex.Mechanisms)
		report.Stats.CounterfactualTests = len(codex.CounterfactualTests)
		if codex.SchemaVersion < CurrentWorldCodexSchemaVersion {
			add("world_codex.legacy_schema", WorldCoherenceSeverityWarning, "world_codex.schema_version",
				fmt.Sprintf("旧法典按 v1 兼容读取；下次有依据修订时升级到 v%d 操作合同", CurrentWorldCodexSchemaVersion))
		}
		auditWorldCodex(codex, strictCodex, activeSections, mechanismIDs, add)
	}

	if world == nil {
		add("book_world.missing", WorldCoherenceSeverityError, "book_world", "本书世界不存在或不可读")
	} else {
		report.Stats.Places = len(world.Places)
		report.Stats.Routes = len(world.Routes)
		report.Stats.Factions = len(world.Factions)
		if world.Version < CurrentBookWorldSchemaVersion {
			add("book_world.legacy_schema", WorldCoherenceSeverityWarning, "book_world.version",
				fmt.Sprintf("旧地图按 v1 兼容读取；下次重建时升级到 v%d 并补齐路线耗时与引用", CurrentBookWorldSchemaVersion))
		}
		report.Stats.ConnectedComponents = auditBookWorld(world, strictWorld, add)
	}

	sort.Slice(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.Severity != right.Severity {
			return left.Severity == WorldCoherenceSeverityError
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.Subject != right.Subject {
			return left.Subject < right.Subject
		}
		return left.Message < right.Message
	})
	findings = compactWorldCoherenceFindings(findings)
	// Keep the canonical zero-finding representation nil. JSON omitempty drops
	// both nil and empty slices, so choosing one representation ensures a report
	// remains byte-for-byte/verifier equivalent after a store round trip.
	if len(findings) == 0 {
		findings = nil
	}
	report.Findings = findings
	report.Ready = true
	for _, finding := range findings {
		if finding.Severity == WorldCoherenceSeverityError {
			report.Ready = false
			break
		}
	}
	report.ReportDigest = worldCoherenceReportDigest(report)
	return report
}

// worldCoherenceFoundationWorld removes the two BookWorld fields owned by
// runtime simulation rather than Architect: the synchronization timestamp and
// each faction clock's current progress. Clock shape, pace and consequence stay
// bound, and the live progress is still range-checked on every verification.
// This lets a legitimate save_world_tick advance the world without invalidating
// the immutable foundation proof.
func worldCoherenceFoundationWorld(world *BookWorld) *BookWorld {
	if world == nil {
		return nil
	}
	normalized := *world
	normalized.LastSyncedAt = ""
	normalized.Factions = append([]WorldFaction(nil), world.Factions...)
	for i := range normalized.Factions {
		if normalized.Factions[i].Clock == nil {
			continue
		}
		clock := *normalized.Factions[i].Clock
		clock.Progress = 0
		normalized.Factions[i].Clock = &clock
	}
	return &normalized
}

// ValidateWorldCodexV2 用于 save_foundation 在落盘前验证新法典。
// 跨文件引用仍由 AuditWorldCoherence 在 book_world 齐备后统一检查。
func ValidateWorldCodexV2(codex WorldCodex) error {
	if codex.SchemaVersion < CurrentWorldCodexSchemaVersion {
		return fmt.Errorf("world_codex.schema_version=%d，新增或修订法典必须使用 v%d", codex.SchemaVersion, CurrentWorldCodexSchemaVersion)
	}
	var findings []WorldCoherenceFinding
	auditWorldCodex(&codex, true, map[string]struct{}{}, map[string]struct{}{}, func(code, severity, subject, message string) {
		if severity == WorldCoherenceSeverityError {
			findings = append(findings, WorldCoherenceFinding{Code: code, Severity: severity, Subject: subject, Message: message})
		}
	})
	return joinWorldCoherenceErrors(findings)
}

// ValidateBookWorldV2 用于 save_foundation 在落盘前验证新地图与势力网。
func ValidateBookWorldV2(world BookWorld) error {
	if world.Version < CurrentBookWorldSchemaVersion {
		return fmt.Errorf("book_world.version=%d，新增世界必须使用 v%d", world.Version, CurrentBookWorldSchemaVersion)
	}
	var findings []WorldCoherenceFinding
	auditBookWorld(&world, true, func(code, severity, subject, message string) {
		if severity == WorldCoherenceSeverityError {
			findings = append(findings, WorldCoherenceFinding{Code: code, Severity: severity, Subject: subject, Message: message})
		}
	})
	return joinWorldCoherenceErrors(findings)
}

func joinWorldCoherenceErrors(findings []WorldCoherenceFinding) error {
	if len(findings) == 0 {
		return nil
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Subject != findings[j].Subject {
			return findings[i].Subject < findings[j].Subject
		}
		return findings[i].Code < findings[j].Code
	})
	messages := make([]string, 0, len(findings))
	for _, finding := range findings {
		messages = append(messages, formatWorldCoherenceFinding(finding))
	}
	return fmt.Errorf("world coherence validation failed: %s", strings.Join(messages, "；"))
}

func auditWorldCodex(
	codex *WorldCodex,
	strict bool,
	activeSections map[string]struct{},
	mechanismIDs map[string]struct{},
	add func(string, string, string, string),
) {
	if codex.CharacterViewVersion != 0 && codex.CharacterViewVersion != CurrentWorldCharacterViewVersion {
		add("codex.character_view_version.unsupported", WorldCoherenceSeverityError, "world_codex.character_view_version",
			fmt.Sprintf("不支持角色视图版本 %d；仅支持历史版本 0 和显式公开视图版本 %d", codex.CharacterViewVersion, CurrentWorldCharacterViewVersion))
	}
	severity := WorldCoherenceSeverityWarning
	if strict {
		severity = WorldCoherenceSeverityError
	}
	if len(codex.AbilityTiers) == 0 {
		add("codex.ability_tiers.missing", WorldCoherenceSeverityError, "world_codex.ability_tiers", "能力分级不能为空")
	}
	if len(codex.SkillDomains) == 0 {
		add("codex.skill_domains.missing", WorldCoherenceSeverityError, "world_codex.skill_domains", "技能范畴不能为空")
	}
	if len(codex.Races) == 0 {
		add("codex.races.missing", WorldCoherenceSeverityError, "world_codex.races", "族群设定不能为空")
	}
	tierNames := map[string]struct{}{}
	tierOrders := map[int]struct{}{}
	for i, tier := range codex.AbilityTiers {
		subject := fmt.Sprintf("world_codex.ability_tiers[%d]", i)
		name := strings.TrimSpace(tier.Name)
		if name == "" {
			add("codex.ability_tier.name.empty", WorldCoherenceSeverityError, subject+".name", "分级名不能为空")
		} else if _, exists := tierNames[name]; exists {
			add("codex.ability_tier.name.duplicate", WorldCoherenceSeverityError, subject+".name", "分级名重复")
		} else {
			tierNames[name] = struct{}{}
			for _, alias := range nonEmptyWorldStrings(tier.Aliases) {
				tierNames[alias] = struct{}{}
			}
		}
		if tier.Order <= 0 {
			add("codex.ability_tier.order.invalid", severity, subject+".order", "层级序号必须大于 0")
		} else if _, exists := tierOrders[tier.Order]; exists {
			add("codex.ability_tier.order.duplicate", severity, subject+".order", "层级序号重复")
		} else {
			tierOrders[tier.Order] = struct{}{}
		}
		for field, value := range map[string]string{
			"magnitude": tier.Magnitude,
			"limits":    tier.Limits,
			"promotion": tier.Promotion,
		} {
			if strings.TrimSpace(value) == "" {
				add("codex.ability_tier."+field+".empty", WorldCoherenceSeverityError, subject+"."+field, "能力分级操作字段不能为空")
			}
		}
		if strings.TrimSpace(tier.Cost) == "" {
			add("codex.ability_tier.cost.empty", severity, subject+".cost", "v2 分级必须显式说明使用/维持代价")
		}
	}

	for i, skill := range codex.SkillDomains {
		subject := fmt.Sprintf("world_codex.skill_domains[%d]", i)
		if strings.TrimSpace(skill.Name) == "" || strings.TrimSpace(skill.Description) == "" {
			add("codex.skill.incomplete", WorldCoherenceSeverityError, subject, "技能门类缺少 name/description")
		}
		if strict && len(nonEmptyWorldStrings(skill.Constraints)) == 0 {
			add("codex.skill.constraints.empty", severity, subject+".constraints", "v2 技能门类必须声明使用禁忌或失败边界")
		}
		if binding := strings.TrimSpace(skill.TierBinding); strict && binding != "" && !codexTierBindingResolves(binding, tierNames) {
			add("codex.skill.tier_binding.unknown", severity, subject+".tier_binding", "v2 绑定必须提及已登记的能力分级/别名，或显式声明不绑定："+binding)
		}
	}
	for i, race := range codex.Races {
		subject := fmt.Sprintf("world_codex.races[%d]", i)
		if strings.TrimSpace(race.Name) == "" || strings.TrimSpace(race.Description) == "" {
			add("codex.race.incomplete", WorldCoherenceSeverityError, subject, "族群缺少 name/description")
		}
		if strict && len(nonEmptyWorldStrings(race.Constraints)) == 0 {
			add("codex.race.constraints.empty", severity, subject+".constraints", "v2 族群必须声明现实约束、弱点或禁忌")
		}
	}
	auditGradedCodexCategories("weapon_categories", codex.WeaponCategories, strict, tierNames, add)
	auditGradedCodexCategories("equipment_categories", codex.EquipmentCategories, strict, tierNames, add)

	sectionByKey := map[string]CodexSection{}
	for i, section := range codex.Sections {
		subject := fmt.Sprintf("world_codex.sections[%d]", i)
		key := strings.TrimSpace(section.Key)
		if key == "" {
			add("codex.section.key.empty", WorldCoherenceSeverityError, subject+".key", "世界维度 key 不能为空")
			continue
		}
		if _, duplicate := sectionByKey[key]; duplicate {
			add("codex.section.key.duplicate", WorldCoherenceSeverityError, subject+".key", "世界维度 key 重复: "+key)
			continue
		}
		sectionByKey[key] = section
		if section.NotApplicable {
			if strings.TrimSpace(section.Reason) == "" {
				add("codex.section.reason.empty", WorldCoherenceSeverityError, subject+".reason", "not_applicable 必须给出理由")
			}
			continue
		}
		activeSections[key] = struct{}{}
		if strings.TrimSpace(section.Content) == "" && len(nonEmptyWorldStrings(section.Rules)) == 0 {
			add("codex.section.content.empty", WorldCoherenceSeverityError, subject, "必须提供 content/rules")
		}
		if strict && len(nonEmptyWorldStrings(section.Rules)) == 0 {
			add("codex.section.rules.empty", severity, subject+".rules", "v2 的适用维度必须至少有一条可执行规则")
		}
	}
	for _, required := range RequiredCodexSections {
		if _, ok := sectionByKey[required.Key]; !ok {
			add("codex.section.required_missing", WorldCoherenceSeverityError, "world_codex.sections."+required.Key, "缺少必需世界维度")
		}
	}
	if strings.TrimSpace(codex.ImmutabilityPolicy) == "" {
		add("codex.immutability_policy.empty", WorldCoherenceSeverityError, "world_codex.immutability_policy", "修订政策不能为空")
	}

	if len(codex.Mechanisms) == 0 {
		add("codex.mechanisms.missing", severity, "world_codex.mechanisms", "v2 至少需要一条操作机制")
	}
	for i, mechanism := range codex.Mechanisms {
		subject := fmt.Sprintf("world_codex.mechanisms[%d]", i)
		if codex.CharacterViewVersion == CurrentWorldCharacterViewVersion && CodexMechanismVisibility(mechanism) != "secret" {
			auditCharacterMechanismView(mechanism.CharacterView, subject+".character_view", add)
		}
		id := strings.TrimSpace(mechanism.ID)
		if id == "" {
			add("codex.mechanism.id.empty", severity, subject+".id", "机制 ID 不能为空")
		} else if _, exists := mechanismIDs[id]; exists {
			add("codex.mechanism.id.duplicate", severity, subject+".id", "机制 ID 重复: "+id)
		} else {
			mechanismIDs[id] = struct{}{}
		}
		if strings.TrimSpace(mechanism.Name) == "" {
			add("codex.mechanism.name.empty", severity, subject+".name", "机制名不能为空")
		}
		switch strings.ToLower(strings.TrimSpace(mechanism.Visibility)) {
		case "formal", "informal", "secret":
		case "":
			add("codex.mechanism.visibility.empty", severity, subject+".visibility", "v2 机制必须声明 formal/informal/secret 可见性")
		default:
			add("codex.mechanism.visibility.invalid", severity, subject+".visibility", "机制可见性只能是 formal/informal/secret")
		}
		if strings.TrimSpace(mechanism.Trigger) == "" {
			add("codex.mechanism.trigger.empty", severity, subject+".trigger", "必须声明进入判定的触发条件")
		}
		if strings.TrimSpace(mechanism.Timing) == "" {
			add("codex.mechanism.timing.empty", severity, subject+".timing", "必须声明生效或传播耗时；即时也要写明")
		}
		for field, values := range map[string][]string{
			"section_refs":  mechanism.SectionRefs,
			"actor_scope":   mechanism.ActorScope,
			"preconditions": mechanism.Preconditions,
			"inputs":        mechanism.Inputs,
			"costs":         mechanism.Costs,
			"effects":       mechanism.Effects,
			"failure_modes": mechanism.FailureModes,
			"observability": mechanism.Observability,
		} {
			if len(nonEmptyWorldStrings(values)) == 0 {
				add("codex.mechanism."+field+".empty", severity, subject+"."+field, "v2 操作机制字段不能为空")
			}
		}
		for _, ref := range nonEmptyWorldStrings(mechanism.SectionRefs) {
			if _, ok := activeSections[ref]; !ok {
				add("codex.mechanism.section_ref.invalid", severity, subject+".section_refs", "引用的世界维度不存在或已标 not_applicable: "+ref)
			}
		}
	}

	if len(codex.CounterfactualTests) == 0 {
		add("codex.counterfactual_tests.missing", severity, "world_codex.counterfactual_tests", "v2 至少需要一条反事实探针")
	}
	probeIDs := map[string]struct{}{}
	coveredMechanisms := map[string]struct{}{}
	for i, probe := range codex.CounterfactualTests {
		subject := fmt.Sprintf("world_codex.counterfactual_tests[%d]", i)
		id := strings.TrimSpace(probe.ID)
		if id == "" {
			add("codex.counterfactual.id.empty", severity, subject+".id", "反事实探针 ID 不能为空")
		} else if _, exists := probeIDs[id]; exists {
			add("codex.counterfactual.id.duplicate", severity, subject+".id", "反事实探针 ID 重复: "+id)
		} else {
			probeIDs[id] = struct{}{}
		}
		if len(nonEmptyWorldStrings(probe.Given)) == 0 {
			add("codex.counterfactual.given.empty", severity, subject+".given", "必须给出不利初态")
		}
		for field, value := range map[string]string{
			"action":            probe.Action,
			"expected_outcome":  probe.ExpectedOutcome,
			"forbidden_outcome": probe.ForbiddenOutcome,
		} {
			if strings.TrimSpace(value) == "" {
				add("codex.counterfactual."+field+".empty", severity, subject+"."+field, "反事实探针字段不能为空")
			}
		}
		if len(nonEmptyWorldStrings(probe.MechanismRefs)) == 0 {
			add("codex.counterfactual.mechanism_refs.empty", severity, subject+".mechanism_refs", "必须引用至少一条操作机制")
		}
		for _, ref := range nonEmptyWorldStrings(probe.MechanismRefs) {
			if _, ok := mechanismIDs[ref]; !ok {
				add("codex.counterfactual.mechanism_ref.unknown", severity, subject+".mechanism_refs", "引用了不存在的机制: "+ref)
				continue
			}
			coveredMechanisms[ref] = struct{}{}
		}
	}
	for id := range mechanismIDs {
		if _, covered := coveredMechanisms[id]; !covered {
			add("codex.mechanism.untested", severity, "world_codex.mechanisms."+id, "没有反事实探针覆盖该机制")
		}
	}
}

func auditCharacterMechanismView(view *CharacterMechanismView, subject string, add func(string, string, string, string)) {
	if view == nil {
		add("codex.mechanism.character_view.missing", WorldCoherenceSeverityError, subject,
			"角色视图 v1 的非 secret 机制必须提供独立公开操作视图；不得复制作者机制补缺")
		return
	}
	for field, value := range map[string]string{
		"name": view.Name, "trigger": view.Trigger, "timing": view.Timing,
	} {
		if strings.TrimSpace(value) == "" {
			add("codex.mechanism.character_view."+field+".empty", WorldCoherenceSeverityError, subject+"."+field,
				"角色公开机制字段不能为空")
		}
	}
	for field, values := range map[string][]string{
		"actor_scope": view.ActorScope, "preconditions": view.Preconditions, "inputs": view.Inputs,
		"costs": view.Costs, "effects": view.Effects, "failure_modes": view.FailureModes, "observability": view.Observability,
	} {
		if len(nonEmptyWorldStrings(values)) == 0 {
			add("codex.mechanism.character_view."+field+".empty", WorldCoherenceSeverityError, subject+"."+field,
				"角色公开机制必须显式说明适用条件、输入、代价、结果、失败及可观测性")
		}
	}
}

func auditGradedCodexCategories(
	field string,
	items []CodexGradedCategory,
	strict bool,
	tierNames map[string]struct{},
	add func(string, string, string, string),
) {
	severity := WorldCoherenceSeverityWarning
	if strict {
		severity = WorldCoherenceSeverityError
	}
	if len(items) == 0 {
		add("codex."+field+".missing", WorldCoherenceSeverityError, "world_codex."+field, "品类不能为空")
	}
	seen := map[string]struct{}{}
	for i, item := range items {
		subject := fmt.Sprintf("world_codex.%s[%d]", field, i)
		name := strings.TrimSpace(item.Name)
		if name == "" || strings.TrimSpace(item.Description) == "" {
			add("codex."+field+".incomplete", WorldCoherenceSeverityError, subject, "品类缺少 name/description")
		}
		if name != "" {
			if _, duplicate := seen[name]; duplicate {
				add("codex."+field+".duplicate", WorldCoherenceSeverityError, subject+".name", "品类名称重复")
			}
			seen[name] = struct{}{}
		}
		if strict && len(nonEmptyWorldStrings(item.Grades)) == 0 {
			add("codex."+field+".grades.empty", severity, subject+".grades", "v2 品类必须声明品级或明确的单级边界")
		}
		if strict && len(nonEmptyWorldStrings(item.Constraints)) == 0 {
			add("codex."+field+".constraints.empty", severity, subject+".constraints", "v2 品类必须声明限制或失败边界")
		}
		if binding := strings.TrimSpace(item.TierBinding); strict && binding != "" && !codexTierBindingResolves(binding, tierNames) {
			add("codex."+field+".tier_binding.unknown", severity, subject+".tier_binding", "v2 绑定必须提及已登记的能力分级/别名，或显式声明不绑定："+binding)
		}
	}
}

// codexTierBindingResolves keeps TierBinding expressive enough for ranges and
// prose (for example “入门至筑基，仅限持证者”), while still catching a v2
// binding that names no registered tier. Some realistic categories explicitly
// do not scale with power; that declaration is an actionable boundary too.
func codexTierBindingResolves(binding string, tierNames map[string]struct{}) bool {
	if binding = strings.TrimSpace(binding); binding == "" {
		return true
	}
	lower := strings.ToLower(binding)
	for _, marker := range []string{"不绑定", "无绑定", "不适用", "none", "n/a", "not applicable"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for tier := range tierNames {
		if tier != "" && strings.Contains(binding, tier) {
			return true
		}
	}
	return false
}

func auditBookWorld(
	world *BookWorld,
	strict bool,
	add func(string, string, string, string),
) int {
	severity := WorldCoherenceSeverityWarning
	if strict {
		severity = WorldCoherenceSeverityError
	}
	if strings.TrimSpace(world.Name) == "" {
		add("book_world.name.empty", severity, "book_world.name", "v2 世界必须有稳定名称")
	}
	if strings.TrimSpace(world.Summary) == "" {
		add("book_world.summary.empty", severity, "book_world.summary", "v2 必须概括世界如何驱动人物与主冲突")
	}
	if len(world.Places) == 0 {
		add("book_world.places.missing", severity, "book_world.places", "v2 至少需要一个可行动地点")
	}
	placeRefs := map[string]int{}
	placeIDs := make([]string, len(world.Places))
	for i, place := range world.Places {
		subject := fmt.Sprintf("book_world.places[%d]", i)
		id := strings.TrimSpace(place.ID)
		name := strings.TrimSpace(place.Name)
		placeIDs[i] = id
		if id == "" || name == "" {
			add("book_world.place.identity.empty", WorldCoherenceSeverityError, subject, "地点必须同时有稳定 id 和 name")
		}
		for _, ref := range []string{id, name} {
			if ref == "" {
				continue
			}
			if previous, duplicate := placeRefs[ref]; duplicate && previous != i {
				add("book_world.place.identity.duplicate", WorldCoherenceSeverityError, subject, fmt.Sprintf("地点标识 %q 与 places[%d] 冲突", ref, previous))
			} else {
				placeRefs[ref] = i
			}
		}
		if strict && strings.TrimSpace(place.Description) == "" {
			add("book_world.place.description.empty", severity, subject+".description", "v2 地点必须说明可行动条件或当前状态")
		}
	}

	if len(world.Factions) == 0 {
		add("book_world.factions.missing", severity, "book_world.factions", "v2 至少需要一个有目标的行动势力")
	}
	factionRefs := map[string]int{}
	for i, faction := range world.Factions {
		subject := fmt.Sprintf("book_world.factions[%d]", i)
		id := strings.TrimSpace(faction.ID)
		name := strings.TrimSpace(faction.Name)
		if id == "" || name == "" {
			add("book_world.faction.identity.empty", WorldCoherenceSeverityError, subject, "势力必须同时有稳定 id 和 name")
		}
		refs := append([]string{id, name}, faction.Aliases...)
		for _, ref := range nonEmptyWorldStrings(refs) {
			if previous, duplicate := factionRefs[ref]; duplicate && previous != i {
				add("book_world.faction.identity.duplicate", WorldCoherenceSeverityError, subject, fmt.Sprintf("势力标识/别名 %q 与 factions[%d] 冲突", ref, previous))
			} else {
				factionRefs[ref] = i
			}
		}
		if strings.TrimSpace(faction.Goal) == "" {
			add("book_world.faction.goal.empty", severity, subject+".goal", "v2 势力必须有可推进目标")
		}
		if len(nonEmptyWorldStrings(faction.Resources)) == 0 {
			add("book_world.faction.resources.empty", severity, subject+".resources", "v2 势力必须登记可用且有限的资源")
		}
		if faction.Clock == nil {
			add("book_world.faction.clock.missing", WorldCoherenceSeverityError, subject+".clock", "势力缺少进度钟")
		} else {
			if faction.Clock.Segments <= 0 {
				add("book_world.faction.clock.segments.invalid", WorldCoherenceSeverityError, subject+".clock.segments", "必须大于 0")
			}
			if faction.Clock.Progress < 0 || (faction.Clock.Segments > 0 && faction.Clock.Progress > faction.Clock.Segments) {
				add("book_world.faction.clock.progress.invalid", WorldCoherenceSeverityError, subject+".clock.progress", "必须位于 0..segments")
			}
			if strings.TrimSpace(faction.Clock.Consequence) == "" {
				add("book_world.faction.clock.consequence.empty", WorldCoherenceSeverityError, subject+".clock.consequence", "走满后的状态后果不能为空")
			}
			if strict && strings.TrimSpace(faction.Clock.Pace) == "" {
				add("book_world.faction.clock.pace.empty", severity, subject+".clock.pace", "v2 必须声明推进速率或触发口径")
			}
		}
	}

	for i, place := range world.Places {
		for _, faction := range nonEmptyWorldStrings(place.Factions) {
			if _, ok := factionRefs[faction]; !ok {
				add("book_world.place.faction_ref.unknown", WorldCoherenceSeverityError,
					fmt.Sprintf("book_world.places[%d].factions", i), "引用了不存在的势力: "+faction)
			}
		}
	}
	relationCount := 0
	for i, faction := range world.Factions {
		for j, relation := range faction.Relations {
			relationCount++
			subject := fmt.Sprintf("book_world.factions[%d].relations[%d]", i, j)
			if _, ok := factionRefs[strings.TrimSpace(relation.Target)]; !ok {
				add("book_world.faction.relation_target.unknown", WorldCoherenceSeverityError, subject+".target", "引用了不存在的势力: "+strings.TrimSpace(relation.Target))
			}
			if strings.TrimSpace(relation.Kind) == "" {
				add("book_world.faction.relation_kind.empty", severity, subject+".kind", "v2 势力关系必须声明类型")
			}
		}
	}
	if strict && len(world.Factions) > 1 && relationCount == 0 {
		add("book_world.faction.relations.missing", severity, "book_world.factions", "多个势力之间必须至少登记一条冲突、合作或依赖关系")
	}

	adjacency := make([]map[int]struct{}, len(world.Places))
	for i := range adjacency {
		adjacency[i] = map[int]struct{}{}
	}
	routeKeys := map[string]struct{}{}
	for i, route := range world.Routes {
		subject := fmt.Sprintf("book_world.routes[%d]", i)
		fromRef := strings.TrimSpace(route.From)
		toRef := strings.TrimSpace(route.To)
		from, fromOK := placeRefs[fromRef]
		to, toOK := placeRefs[toRef]
		if !fromOK {
			add("book_world.route.from.unknown", WorldCoherenceSeverityError, subject+".from", "引用了不存在的地点: "+fromRef)
		}
		if !toOK {
			add("book_world.route.to.unknown", WorldCoherenceSeverityError, subject+".to", "引用了不存在的地点: "+toRef)
		}
		if fromOK && toOK {
			if from == to {
				add("book_world.route.self_loop", severity, subject, "路线起点和终点相同")
			} else {
				adjacency[from][to] = struct{}{}
				adjacency[to][from] = struct{}{} // 连通性检查只看弱连通；行动方向仍保留 From→To。
			}
		}
		key := fromRef + "\x00" + toRef
		if _, duplicate := routeKeys[key]; duplicate {
			add("book_world.route.duplicate", severity, subject, "同方向路线重复")
		}
		routeKeys[key] = struct{}{}
		if route.TravelDays <= 0 {
			add("book_world.route.travel_days.invalid", severity, subject+".travel_days", "v2 路线必须给出大于 0 的常规旅行天数")
		}
		if strings.TrimSpace(route.Description) == "" {
			add("book_world.route.description.empty", severity, subject+".description", "v2 路线必须说明常规移动方式或通行条件")
		}
		if strict && strings.TrimSpace(route.Risk) == "" {
			add("book_world.route.risk.empty", severity, subject+".risk", "v2 路线必须写明阻断、延误或资源风险；稳定路线也要显式说明")
		}
	}

	components := worldPlaceComponents(world.Places, adjacency)
	if strict && components > 1 {
		add("book_world.topology.disconnected", severity, "book_world.routes",
			fmt.Sprintf("存在 %d 个互不连通的地点分量；若刻意隔绝，请在地点 tags 标记 isolated", components))
	}
	return components
}

func worldPlaceComponents(places []WorldPlace, adjacency []map[int]struct{}) int {
	seen := make([]bool, len(places))
	components := 0
	for start, place := range places {
		if seen[start] || worldPlaceIsExplicitlyIsolated(place) {
			continue
		}
		components++
		queue := []int{start}
		seen[start] = true
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for next := range adjacency[current] {
				if seen[next] || worldPlaceIsExplicitlyIsolated(places[next]) {
					continue
				}
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return components
}

func worldPlaceIsExplicitlyIsolated(place WorldPlace) bool {
	for _, tag := range place.Tags {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "isolated", "unreachable", "隔绝", "不可达":
			return true
		}
	}
	return false
}

func nonEmptyWorldStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func compactWorldCoherenceFindings(findings []WorldCoherenceFinding) []WorldCoherenceFinding {
	if len(findings) < 2 {
		return findings
	}
	out := findings[:0]
	var previous string
	for _, finding := range findings {
		key := finding.Severity + "\x00" + finding.Code + "\x00" + finding.Subject + "\x00" + finding.Message
		if key == previous {
			continue
		}
		previous = key
		out = append(out, finding)
	}
	return out
}

func worldCoherenceDigest(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func worldCoherenceReportDigest(report WorldCoherenceReport) string {
	report.ReportDigest = ""
	return worldCoherenceDigest(report)
}

// VerifyWorldCoherenceReport 同时验证报告自身摘要、输入摘要和重新计算的结论。
// 任何源文件或 finding 被手改都会失败。
func VerifyWorldCoherenceReport(report WorldCoherenceReport, rules []WorldRule, codex *WorldCodex, world *BookWorld) error {
	if report.Version != WorldCoherenceReportVersion || report.Protocol != WorldCoherenceProtocol {
		return fmt.Errorf("world coherence report protocol mismatch: version=%d protocol=%q", report.Version, report.Protocol)
	}
	if strings.TrimSpace(report.ReportDigest) == "" || report.ReportDigest != worldCoherenceReportDigest(report) {
		return fmt.Errorf("world coherence report digest mismatch")
	}
	expected := AuditWorldCoherence(rules, codex, world)
	if !reflect.DeepEqual(report, expected) {
		if report.SourceDigest != expected.SourceDigest {
			return fmt.Errorf("world coherence source digest mismatch")
		}
		return fmt.Errorf("world coherence report does not match deterministic audit")
	}
	return nil
}
