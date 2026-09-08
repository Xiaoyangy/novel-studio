package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestWorldCharacterViewHistoricalDigests(t *testing.T) {
	// Recorded against the implementation before character_view fields existed.
	// These pin both old schema-1 warnings and schema-2 success, not only JSON
	// round trips through the new types.
	for _, golden := range []struct {
		schema         int
		source, report string
	}{
		{0, "sha256:95e71d1990a1dde0ded16d33094b3f92be6afecf2ddbef4e37bd091e75bfeb63", "sha256:0fb76a5a7775ce0d8ba6150606c04ca490e8abe1a97b21b6d6842c309997c6aa"},
		{2, "sha256:e7ad6dc8636834b0a229019ea2c44fdb4362c98e7e210a28dfa464ebefea3f84", "sha256:adaa1576460b1aa07a68f1f6951c6dc9599285c505d2b4a7a660b08a8b629c69"},
	} {
		rules, codex, world := coherentWorldFixture()
		if golden.schema == 0 {
			codex.SchemaVersion = 0
			codex.Mechanisms = nil
			codex.CounterfactualTests = nil
			codex.SkillDomains[0].TierBinding = "T0-T6"
			world.Version = 1
			world.Routes[0].TravelDays = 0
		}
		report := AuditWorldCoherence(rules, &codex, &world)
		if report.SourceDigest != golden.source || report.ReportDigest != golden.report {
			t.Fatalf("schema=%d historical proof changed: source=%s report=%s", golden.schema, report.SourceDigest, report.ReportDigest)
		}
		if err := VerifyWorldCoherenceReport(report, rules, &codex, &world); err != nil {
			t.Fatalf("historical proof no longer verifies: %v", err)
		}
		payload, err := json.Marshal(struct {
			Rules []WorldRule `json:"rules"`
			Codex WorldCodex  `json:"codex"`
		}{rules, codex})
		if err != nil || strings.Contains(string(payload), "character_view") {
			t.Fatalf("zero-valued new fields changed historical JSON: %s, err=%v", payload, err)
		}
	}
}

func TestWorldCharacterViewProtocolRequiresExplicitPublicViews(t *testing.T) {
	rules, codex, world := coherentWorldFixture()
	codex.CharacterViewVersion = CurrentWorldCharacterViewVersion
	report := AuditWorldCoherence(rules, &codex, &world)
	if report.Ready {
		t.Fatal("character view protocol accepted omniscient author objects without a view")
	}
	for _, code := range []string{"world_rules.character_view.empty", "codex.mechanism.character_view.missing"} {
		if !hasCharacterViewFinding(report, code) {
			t.Fatalf("missing finding %s: %+v", code, report.Findings)
		}
	}
	if err := ValidateWorldCodexV2(codex); err == nil || !strings.Contains(err.Error(), "character_view") {
		t.Fatalf("save-time codex validation ignored missing view: %v", err)
	}
	rules[0].CharacterView = "当事人须出示原件完成核验，口头承诺不能代替凭证。"
	codex.Mechanisms[0].CharacterView = completeCharacterMechanismView()
	report = AuditWorldCoherence(rules, &codex, &world)
	if !report.Ready {
		t.Fatalf("complete explicit views rejected: %+v", report.Findings)
	}
	if err := ValidateWorldCodexV2(codex); err != nil {
		t.Fatalf("complete view cannot be saved: %v", err)
	}
	if err := VerifyWorldCoherenceReport(report, rules, &codex, &world); err != nil {
		t.Fatal(err)
	}
	rules[0].CharacterView += "新增公开规则。"
	if err := VerifyWorldCoherenceReport(report, rules, &codex, &world); err == nil || !strings.Contains(err.Error(), "source digest") {
		t.Fatalf("tampered public rule did not invalidate foundation proof: %v", err)
	}
}

func TestWorldCharacterViewRejectsIncompleteMechanismFields(t *testing.T) {
	for _, field := range []string{"name", "trigger", "timing", "actor_scope", "preconditions", "inputs", "costs", "effects", "failure_modes", "observability"} {
		t.Run(field, func(t *testing.T) {
			rules, codex, world := coherentWorldFixture()
			codex.CharacterViewVersion = CurrentWorldCharacterViewVersion
			rules[0].CharacterView = "公开核验规则。"
			view := completeCharacterMechanismView()
			raw, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if field == "name" || field == "trigger" || field == "timing" {
				fields[field] = json.RawMessage(`"  "`)
			} else {
				fields[field] = json.RawMessage(`["", " "]`)
			}
			raw, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, view); err != nil {
				t.Fatal(err)
			}
			codex.Mechanisms[0].CharacterView = view
			report := AuditWorldCoherence(rules, &codex, &world)
			if report.Ready || !hasCharacterViewFinding(report, "codex.mechanism.character_view."+field+".empty") {
				t.Fatalf("blank %s bypassed character-view validation: %+v", field, report)
			}
		})
	}
}

func TestWorldCharacterViewUnknownVersionFailsClosed(t *testing.T) {
	for _, version := range []int{-1, 2, 99} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			rules, codex, world := coherentWorldFixture()
			codex.CharacterViewVersion = version
			report := AuditWorldCoherence(rules, &codex, &world)
			if report.Ready || !hasCharacterViewFinding(report, "codex.character_view_version.unsupported") {
				t.Fatalf("unknown version did not fail closed: %+v", report)
			}
			if err := ValidateWorldCodexV2(codex); err == nil {
				t.Fatal("unknown character-view version passed save-time validation")
			}
		})
	}
}

func TestWorldCharacterViewSecretAuthorFactsStayIndependent(t *testing.T) {
	rules, codex, world := coherentWorldFixture()
	codex.CharacterViewVersion = CurrentWorldCharacterViewVersion
	rules[0].CharacterView = "仅在双方出示原件后核对当前记录。"
	codex.Mechanisms[0].CharacterView = completeCharacterMechanismView()
	secret := codex.Mechanisms[0]
	secret.ID, secret.Name, secret.Visibility = "hidden-change", "隐秘改账", "secret"
	secret.CharacterView = nil
	codex.Mechanisms = append(codex.Mechanisms, secret)
	codex.CounterfactualTests[0].MechanismRefs = append(codex.CounterfactualTests[0].MechanismRefs, secret.ID)
	rules = append(rules, WorldRule{Category: "past_truth", Rule: "幕后改写者持有秘密记录。", Boundary: "不能自动传播给角色。", Visibility: "secret"})
	before := AuditWorldCoherence(rules, &codex, &world)
	if !before.Ready {
		t.Fatalf("secret-only author data wrongly requires a public view: %+v", before.Findings)
	}
	publicBefore, err := json.Marshal(codex.Mechanisms[0].CharacterView)
	if err != nil {
		t.Fatal(err)
	}
	// Author and character fields are intentionally distinct contracts. Even
	// public mechanisms may contain author-only detail outside their view.
	codex.Mechanisms[0].Name = "作者知道的真正机关"
	codex.Mechanisms[0].Effects = []string{"幕后人员通过改账隐藏燃油去向"}
	codex.Mechanisms[0].Observability = []string{"作者全知；结局才回收"}
	codex.Mechanisms[1].Effects = []string{"秘密作者事实发生修订"}
	rules[0].Rule = "作者终局固定全部真相。"
	rules[1].Rule = "幕后改写者的具体秘密已经改变。"
	publicAfter, err := json.Marshal(codex.Mechanisms[0].CharacterView)
	if err != nil || string(publicAfter) != string(publicBefore) || strings.Contains(string(publicAfter), "幕后") {
		t.Fatalf("author changes contaminated the explicit character view: %s err=%v", publicAfter, err)
	}
	after := AuditWorldCoherence(rules, &codex, &world)
	if !after.Ready || after.SourceDigest == before.SourceDigest {
		t.Fatalf("author revisions must remain independently bound to the source proof: %+v", after)
	}
	proof := after
	codex.Mechanisms[0].CharacterView.Costs[0] = "修改了角色可见代价"
	if err := VerifyWorldCoherenceReport(proof, rules, &codex, &world); err == nil {
		t.Fatal("public mechanism view change escaped the source proof")
	}
}

func TestWorldCharacterViewWhitespaceRuleIsNotPublicKnowledge(t *testing.T) {
	rules, codex, world := coherentWorldFixture()
	codex.CharacterViewVersion = CurrentWorldCharacterViewVersion
	codex.Mechanisms[0].CharacterView = completeCharacterMechanismView()
	for _, visibility := range []string{"", "formal", "informal"} {
		rules[0].Visibility, rules[0].CharacterView = visibility, " \t\n"
		report := AuditWorldCoherence(rules, &codex, &world)
		if report.Ready || !hasCharacterViewFinding(report, "world_rules.character_view.empty") {
			t.Fatalf("visibility=%q blank public view was accepted: %+v", visibility, report)
		}
	}
}

func hasCharacterViewFinding(report WorldCoherenceReport, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func completeCharacterMechanismView() *CharacterMechanismView {
	return &CharacterMechanismView{
		Name: "凭证核验", ActorScope: []string{"持有人及值班员"}, Trigger: "持有人提出当面核验请求",
		Preconditions: []string{"原件与实际保管人均在场"}, Inputs: []string{"实际出示的凭证"},
		Costs: []string{"需要一个核验时段"}, Effects: []string{"确认当前原件与登记的一致性"},
		FailureModes:  []string{"缺少原件则等待补充，不推断材料内容"},
		Observability: []string{"现场人员能看见已出示材料及明确告知的核验结果"}, Timing: "完成核验后生效",
	}
}
