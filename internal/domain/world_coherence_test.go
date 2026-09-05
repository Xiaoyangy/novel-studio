package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuditWorldCoherenceV2IsDeterministicAndReady(t *testing.T) {
	rules, codex, world := coherentWorldFixture()
	first := AuditWorldCoherence(rules, &codex, &world)
	second := AuditWorldCoherence(rules, &codex, &world)
	if !first.Ready || len(first.BlockingIssues()) != 0 {
		t.Fatalf("coherent v2 world rejected: %+v", first.Findings)
	}
	if first.ReportDigest == "" || first.SourceDigest == "" || first.ReportDigest != second.ReportDigest {
		t.Fatalf("audit is not deterministic: first=%+v second=%+v", first, second)
	}
	if err := VerifyWorldCoherenceReport(first, rules, &codex, &world); err != nil {
		t.Fatalf("verify generated report: %v", err)
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip WorldCoherenceReport
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if err := VerifyWorldCoherenceReport(roundTrip, rules, &codex, &world); err != nil {
		t.Fatalf("verify report after JSON round trip: %v", err)
	}
	if err := ValidateWorldCodexV2(codex); err != nil {
		t.Fatalf("validate codex: %v", err)
	}
	if err := ValidateBookWorldV2(world); err != nil {
		t.Fatalf("validate book world: %v", err)
	}
	world.Factions[0].Clock.Progress++
	world.LastSyncedAt = "2026-09-05T16:00:00Z"
	if err := VerifyWorldCoherenceReport(roundTrip, rules, &codex, &world); err != nil {
		t.Fatalf("runtime clock progress must not invalidate authored-world proof: %v", err)
	}
	world.Factions[0].Clock.Progress = world.Factions[0].Clock.Segments + 1
	if err := VerifyWorldCoherenceReport(roundTrip, rules, &codex, &world); err == nil || !strings.Contains(err.Error(), "deterministic audit") {
		t.Fatalf("invalid runtime clock progress must fail live audit: %v", err)
	}
}

func TestAuditWorldCoherenceRejectsBrokenTopologyAndIdentity(t *testing.T) {
	rules, codex, world := coherentWorldFixture()
	world.Places = append(world.Places, WorldPlace{ID: "vault", Name: "封闭库房", Description: "未说明隔绝的第三地点"})
	world.Routes[0].To = "missing-place"
	world.Factions[1].Aliases = []string{"登记处"}
	world.Places[0].Factions = append(world.Places[0].Factions, "unknown-faction")

	report := AuditWorldCoherence(rules, &codex, &world)
	if report.Ready {
		t.Fatalf("broken v2 world unexpectedly ready: %+v", report.Findings)
	}
	joined := strings.Join(report.BlockingIssues(), "\n")
	for _, want := range []string{"missing-place", "unknown-faction", "标识/别名", "互不连通"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in issues:\n%s", want, joined)
		}
	}
}

func TestAuditWorldCoherenceRejectsUntestedMechanism(t *testing.T) {
	_, codex, _ := coherentWorldFixture()
	codex.Mechanisms = append(codex.Mechanisms, CodexMechanism{
		ID: "uncovered", Name: "无人验证机制", Visibility: "formal", SectionRefs: []string{"mechanism_structure"},
		ActorScope: []string{"所有人"}, Trigger: "提出请求", Preconditions: []string{"具有权限"},
		Inputs: []string{"权限记录"}, Costs: []string{"消耗一次机会"}, Effects: []string{"请求进入处理"},
		FailureModes: []string{"权限不足时拒绝"}, Observability: []string{"申请人收到通知"}, Timing: "即时",
	})
	if err := ValidateWorldCodexV2(codex); err == nil || !strings.Contains(err.Error(), "没有反事实探针") {
		t.Fatalf("expected uncovered mechanism rejection, got %v", err)
	}
}

func TestAuditWorldCoherenceTierBindingSupportsRangesAndExplicitNonBinding(t *testing.T) {
	_, codex, _ := coherentWorldFixture()
	codex.AbilityTiers = append(codex.AbilityTiers, CodexAbilityTier{
		Order: 2, Name: "审计员", Aliases: []string{"复核者"}, Magnitude: "复核一组凭证",
		Limits: "不能修改原始登记", Promotion: "通过公开审计", Cost: "占用两个工作时段",
	})
	codex.SkillDomains[0].TierBinding = "登记员至审计员，按持有人权限分层"
	codex.WeaponCategories[0].TierBinding = "不绑定能力升级；所有人受同一物理伤害"
	codex.EquipmentCategories[0].TierBinding = "复核者可签发，登记员只能查阅"
	if err := ValidateWorldCodexV2(codex); err != nil {
		t.Fatalf("actionable prose tier bindings rejected: %v", err)
	}
	codex.SkillDomains[0].TierBinding = "不存在的万能等级"
	if err := ValidateWorldCodexV2(codex); err == nil || !strings.Contains(err.Error(), "已登记的能力分级") {
		t.Fatalf("unknown v2 tier binding should fail: %v", err)
	}
}

func TestAuditWorldCoherenceLegacyRemainsReadable(t *testing.T) {
	rules, codex, world := coherentWorldFixture()
	codex.SchemaVersion = 0
	codex.Mechanisms = nil
	codex.CounterfactualTests = nil
	codex.SkillDomains[0].TierBinding = "T0-T6" // v1 historically allowed free-form labels.
	world.Version = 1
	world.Routes[0].TravelDays = 0
	report := AuditWorldCoherence(rules, &codex, &world)
	if !report.Ready {
		t.Fatalf("legacy world should remain readable: %+v", report.Findings)
	}
	warnings := strings.Join(report.Warnings(), "\n")
	for _, want := range []string{"旧法典", "旧地图", "mechanisms", "travel_days"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("expected legacy warning %q in %s", want, warnings)
		}
	}
}

func TestVerifyWorldCoherenceReportRejectsTamperingAndSourceChange(t *testing.T) {
	rules, codex, world := coherentWorldFixture()
	report := AuditWorldCoherence(rules, &codex, &world)
	tampered := report
	tampered.Ready = false
	if err := VerifyWorldCoherenceReport(tampered, rules, &codex, &world); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered report should fail digest verification: %v", err)
	}
	world.Routes[0].TravelDays = 2
	if err := VerifyWorldCoherenceReport(report, rules, &codex, &world); err == nil || !strings.Contains(err.Error(), "source digest") {
		t.Fatalf("changed source should invalidate report: %v", err)
	}
}

func coherentWorldFixture() ([]WorldRule, WorldCodex, BookWorld) {
	rules := []WorldRule{{Category: "contract", Rule: "凭证确认后才能转移资源", Boundary: "口头承诺不能替代原始凭证"}}
	sections := make([]CodexSection, 0, len(RequiredCodexSections))
	for _, required := range RequiredCodexSections {
		sections = append(sections, CodexSection{
			Key: required.Key, Title: required.Title, Content: "该维度服务契约城市的现实运行。", Rules: []string{"状态变化必须留下可核验记录。"},
		})
	}
	codex := WorldCodex{
		Version: 1, SchemaVersion: CurrentWorldCodexSchemaVersion,
		AbilityTiers:        []CodexAbilityTier{{Order: 1, Name: "登记员", Magnitude: "核验单份凭证", Limits: "不能越权确权", Promotion: "完成三次无误核验", Cost: "占用一个工作时段"}},
		SkillDomains:        []CodexDomainEntry{{Name: "凭证核验", Description: "比对原件与登记状态", TierBinding: "登记员", Constraints: []string{"必须接触原件"}}},
		Races:               []CodexRace{{Name: "人类", Description: "受时间、地点和制度限制的普通人", Constraints: []string{"不能瞬时移动或全知"}}},
		WeaponCategories:    []CodexGradedCategory{{Name: "普通器械", Description: "现实工具", Grades: []string{"民用"}, Constraints: []string{"不能绕过门禁"}}},
		EquipmentCategories: []CodexGradedCategory{{Name: "凭证", Description: "权利与资源的证明", Grades: []string{"待核", "有效"}, Constraints: []string{"伪造会触发审计"}}},
		Sections:            sections, ImmutabilityPolicy: "修订必须提供事实证据。",
		Mechanisms: []CodexMechanism{{
			ID: "proof-transfer", Name: "凭证转移", Visibility: "formal", SectionRefs: []string{"mechanism_structure", "economy_currency"},
			ActorScope: []string{"登记员", "凭证持有人"}, Trigger: "持有人申请转移", Preconditions: []string{"原件有效", "双方在场或完成授权"},
			Inputs: []string{"原始凭证"}, Costs: []string{"占用一次核验时段"}, Effects: []string{"登记持有人改变"},
			FailureModes: []string{"缺少原件则拒绝并记录"}, Observability: []string{"当事人收到回执；旁观者只见公开登记"}, Timing: "核验后一个工作日",
		}},
		CounterfactualTests: []CodexCounterfactualProbe{{
			ID: "transfer-without-proof", Given: []string{"申请人没有原件"}, Action: "请求转移",
			ExpectedOutcome: "拒绝并记录失败", ForbiddenOutcome: "因申请人是主角而口头放行", MechanismRefs: []string{"proof-transfer"},
		}},
	}
	world := BookWorld{
		Version: CurrentBookWorldSchemaVersion, Name: "凭证城", Summary: "权利随可核验凭证流动。",
		Places: []WorldPlace{
			{ID: "hall", Name: "登记大厅", Description: "公开核验地点", Factions: []string{"registry"}},
			{ID: "market", Name: "交易市集", Description: "资源交换地点", Factions: []string{"holders"}},
		},
		Routes: []WorldRoute{{From: "hall", To: "market", Description: "步行通道", Risk: "闭馆时无法通行", TravelDays: 0.05}},
		Factions: []WorldFaction{
			{ID: "registry", Name: "登记处", Aliases: []string{"登记所"}, Goal: "维持凭证可信度", Resources: []string{"登记簿", "核验员"}, Relations: []FactionRelation{{Target: "holders", Kind: "regulates"}}, Clock: &FactionClock{Segments: 6, Progress: 1, Consequence: "启动全城复核", Pace: "每弧一段"}},
			{ID: "holders", Name: "持有人联盟", Aliases: []string{"持有人"}, Goal: "降低交易摩擦", Resources: []string{"有效凭证", "成员信用"}, Relations: []FactionRelation{{Target: "registry", Kind: "depends_on"}}, Clock: &FactionClock{Segments: 6, Progress: 0, Consequence: "建立私下互认", Pace: "每弧一段"}},
		},
	}
	return rules, codex, world
}
