package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func arcRehearsalTestInput(t *testing.T) (*store.Store, domain.ArcRehearsalInput) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	selectionMust(t, st.Init())
	selectionMust(t, st.Characters.Save([]domain.Character{{Name: "甲", Role: "值班员", Tier: "core", InitialState: &domain.CharacterInitialState{Location: "值班室", CurrentGoal: "保留证据", Pressure: "时间有限", KnownFacts: []string{"知道值班室有一本交接夹"}, ResourceBalances: []domain.InitialCharacterResourceV2{{ResourceID: "res_1111111111111111", Name: "既有账本", PerceivedName: "账本", PerceivedLabel: "账本", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}, ReadableFacts: []domain.ResourceReadableFactV2{{ID: "original", Text: "既有记录原文"}}}}}}}))
	selectionMust(t, st.World.SaveWorldRules([]domain.WorldRule{{Category: "核验", Rule: "证据有来源", Boundary: "不凭空造证据", Visibility: "formal", CharacterView: "核对来源"}}))
	selectionMust(t, st.SaveWorldCodex(domain.WorldCodex{Sections: []domain.CodexSection{{Key: "law_order", Content: "有交接夹可查，但尚未声明该可读实体"}}}))
	selectionMust(t, st.World.SaveBookWorld(domain.BookWorld{Version: 1, Name: "渡口", Places: []domain.WorldPlace{{ID: "duty", Name: "值班室"}}}))
	selectionMust(t, st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "发现", CoreEvent: "冲突出现"}, {Chapter: 2, Title: "核验", CoreEvent: "取证可能受阻"}, {Chapter: 3, Title: "结束", CoreEvent: "FUTURE_ONLY_EVENT"}}))
	selectionMust(t, st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "第三章解除错误归责", NonNegotiables: []string{"真实有限代价"}}))
	selectionMust(t, st.UserRules.Save(&rules.Snapshot{Version: 1, Status: rules.StatusReady, Structured: rules.Structured{ChapterWords: &rules.WordRange{Min: 2200, Max: 2500}}, Preferences: "人物须独立行动"}))
	input, err := BuildArcRehearsalInput(st, domain.ArcRehearsalInput{ArcID: "arc_fixture", ArcFirstChapter: 1, ArcLastChapter: 3, BaseCanonRoot: "sha256:" + strings.Repeat("a", 64), SourceRoot: "sha256:" + strings.Repeat("b", 64)})
	selectionMust(t, err)
	return st, input
}

func arcRehearsalTestBody(input domain.ArcRehearsalInput, missing bool) domain.ArcRehearsalBody {
	body := domain.ArcRehearsalBody{Summary: "如果取得现有依据，可能在弧末闭合；这不是执行结果。", UnresolvedItems: []string{"角色实际选择须由后续独立阶段产生"}}
	for _, entry := range input.Outline {
		body.Chapters = append(body.Chapters, domain.ArcRehearsalChapter{Chapter: entry.Chapter, ConditionalForecast: "若人物获得所需信息，则可能推进本章冲突", Assumptions: []string{"条件尚未实际执行"}, CausalLinks: []string{"实际取得依据后才可能核验"}, TimeResourceChecks: []string{"保留真实路程与操作时间，不预定资源充足"}})
	}
	for _, o := range input.CharacterObservations {
		body.CharacterConflicts = append(body.CharacterConflicts, domain.ArcRehearsalCharacterConflict{Character: o.Character, CurrentGoal: o.CurrentGoal, Conflicts: []string{"取证与时间要求冲突"}, ConditionalChoices: []string{"若材料可查，可能选择核读；不是正式决定"}})
	}
	for _, text := range input.HardContracts {
		body.ContractChecks = append(body.ContractChecks, domain.ArcRehearsalContractCheck{Contract: text, Assessment: "conditional", Conditions: []string{"所需资料实际可用且人物自主选择"}})
	}
	check := domain.ArcRehearsalMaterialCheck{Operation: "核读既有账本", RequiresReadable: true, ResourceRefs: []string{"res_1111111111111111"}, Status: "available", Explanation: "输入有账本实体及原始readable_facts"}
	if missing {
		check = domain.ArcRehearsalMaterialCheck{Operation: "核读交接夹", RequiresReadable: true, Status: "missing", Explanation: "文字提到交接夹，但world_state没有对应实体和条款"}
	}
	body.MaterialChecks = []domain.ArcRehearsalMaterialCheck{check}
	return body
}

func TestArcRehearsalPolicyChangePreservesHistoryButRequiresNewExecutionInput(t *testing.T) {
	st, input := arcRehearsalTestInput(t)
	legacy := input
	legacy.ProtocolDigest = ""
	legacy, err := domain.FinalizeArcRehearsalInput(legacy)
	selectionMust(t, err)
	if input.ProtocolDigest == "" || input.InputDigest == legacy.InputDigest {
		t.Fatal("new policy did not select a new immutable input")
	}
	call := func(role, id string) domain.ArcRehearsalCall {
		return domain.ArcRehearsalCall{Role: role, Provider: "fixture", Model: "fixture", UsageIDs: []string{id}, ToolCallID: id, ResponseDigest: "sha256:" + strings.Repeat("a", 64)}
	}
	draft, err := domain.FinalizeArcRehearsalDraft(legacy, domain.ArcRehearsalDraft{Body: arcRehearsalTestBody(legacy, false), Call: call("architect", "legacy-draft")})
	selectionMust(t, err)
	report, err := domain.FinalizeArcRehearsalReport(legacy, draft, domain.ArcRehearsalReport{Body: arcRehearsalTestBody(legacy, false), Call: call("world_arbiter", "legacy-review")})
	selectionMust(t, err)
	selectionMust(t, st.SaveArcRehearsalReport(legacy, draft, report))
	loaded, oldInput, err := st.LoadVerifiedArcRehearsal(report.ReportDigest)
	selectionMust(t, err)
	if loaded == nil || oldInput == nil || loaded.ReportDigest != report.ReportDigest || oldInput.ProtocolDigest != "" {
		t.Fatal("historical report was rewritten or became unreadable")
	}
	model := &arcRehearsalFakeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("fixture", "fixture", model)}
	if _, err := RunArcRehearsal(context.Background(), bootstrap.Config{}, models, st, legacy); err == nil || !strings.Contains(err.Error(), "policy changed") || model.calls != 0 {
		t.Fatalf("old policy was silently re-executed: calls=%d err=%v", model.calls, err)
	}
}

type arcRehearsalFakeModel struct {
	calls      int
	failReview bool
	missing    bool
	roles      []string
}

func decodeArcRehearsalFakePayload(raw []byte, target any) error {
	var view struct {
		Encoding string            `json:"encoding"`
		Fields   map[string]string `json:"field_names"`
		Values   map[string]any    `json:"shared_values"`
		Payload  any               `json:"payload"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&view); err != nil {
		return err
	}
	if view.Encoding == "" {
		return json.Unmarshal(raw, target)
	}
	var expand func(any) (any, error)
	expand = func(value any) (any, error) {
		switch value := value.(type) {
		case map[string]any:
			if len(value) == 1 {
				if id, ok := value[arcRehearsalSharedValueKey].(string); ok {
					literal, exists := view.Values[id]
					if !exists {
						return nil, fmt.Errorf("missing shared input %s", id)
					}
					return literal, nil
				}
			}
			out := map[string]any{}
			for key, child := range value {
				decoded, err := expand(child)
				if err != nil {
					return nil, err
				}
				out[key] = decoded
			}
			return out, nil
		case []any:
			out := make([]any, len(value))
			for i, child := range value {
				decoded, err := expand(child)
				if err != nil {
					return nil, err
				}
				out[i] = decoded
			}
			return out, nil
		default:
			return value, nil
		}
	}
	expanded, err := expand(view.Payload)
	if err != nil {
		return err
	}
	var restore func(any) any
	restore = func(value any) any {
		switch value := value.(type) {
		case map[string]any:
			out := map[string]any{}
			for key, child := range value {
				if original := view.Fields[key]; original != "" {
					key = original
				}
				out[key] = restore(child)
			}
			return out
		case []any:
			out := make([]any, len(value))
			for i, child := range value {
				out[i] = restore(child)
			}
			return out
		default:
			return value
		}
	}
	expanded = restore(expanded)
	encoded, err := json.Marshal(expanded)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func (*arcRehearsalFakeModel) SupportsTools() bool { return true }
func (m *arcRehearsalFakeModel) Generate(_ context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	if len(specs) != 1 || specs[0].Name != "submit_arc_rehearsal" {
		return nil, fmt.Errorf("unexpected model capability")
	}
	var payload struct {
		Input domain.ArcRehearsalInput  `json:"input"`
		Draft *domain.ArcRehearsalDraft `json:"architect_draft"`
	}
	for _, message := range messages {
		if message.Role == agentcore.RoleUser {
			if err := decodeArcRehearsalFakePayload([]byte(message.TextContent()), &payload); err != nil {
				return nil, err
			}
		}
	}
	role := "architect"
	if payload.Draft != nil {
		role = "world_arbiter"
	}
	m.roles = append(m.roles, role)
	if role == "world_arbiter" && m.failReview {
		m.failReview = false
		return nil, context.Canceled
	}
	raw, err := json.Marshal(arcRehearsalTestBody(payload.Input, m.missing))
	if err != nil {
		return nil, err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse, Usage: &agentcore.Usage{Input: 123, Output: 45}, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: fmt.Sprintf("arc-call-%d", m.calls), Name: specs[0].Name, Args: raw})}}}, nil
}
func (m *arcRehearsalFakeModel) GenerateStream(ctx context.Context, msg []agentcore.Message, spec []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, err := m.Generate(ctx, msg, spec, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}

func arcRehearsalSourceFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			if filepath.ToSlash(rel) == store.ArcRehearsalRoot {
				return filepath.SkipDir
			}
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(raw)
		return nil
	})
	selectionMust(t, err)
	return out
}

func TestArcRehearsalRealRunnerIsSpeculativeAccountedAndRecoverable(t *testing.T) {
	st, input := arcRehearsalTestInput(t)
	before := arcRehearsalSourceFiles(t, st.Dir())
	second, err := BuildArcRehearsalInput(st, input)
	selectionMust(t, err)
	if !reflect.DeepEqual(input, second) {
		t.Fatal("Build is not deterministic")
	}
	encoded, _ := json.Marshal(input.CharacterObservations)
	if strings.Contains(string(encoded), "FUTURE_ONLY_EVENT") {
		t.Fatal("future soft outline entered current character observation")
	}
	if !strings.Contains(strings.Join(input.HardContracts, "\n"), "2200—2500") || input.SourceFiles["meta/user_rules.json"] == "" {
		t.Fatal("normalized user budget missing")
	}
	model := &arcRehearsalFakeModel{failReview: true}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "arc-fake", model)}
	var recorded []string
	accounting := ProjectedPlanningAccounting{RecordUsage: func(role string, message agentcore.AgentMessage) {
		if role != "architect" && role != "world_arbiter" {
			t.Errorf("wrong logical usage role: %s", role)
		}
		if msg, ok := message.(agentcore.Message); ok {
			if id, _ := msg.Metadata["usage_audit_id"].(string); id != "" {
				recorded = append(recorded, id)
			}
		}
	}}
	if _, err := RunArcRehearsal(context.Background(), bootstrap.Config{}, models, st, input, accounting); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected review interruption after real draft: %v", err)
	}
	draft, _, err := st.LoadArcRehearsalDraftForInput(input.InputDigest)
	selectionMust(t, err)
	if draft == nil {
		t.Fatal("draft not durably recoverable")
	}
	st = store.NewStore(st.Dir())
	report, err := RunArcRehearsal(context.Background(), bootstrap.Config{}, models, st, input, accounting)
	selectionMust(t, err)
	if !report.ReadyForDetail || report.Authority != "speculative" || model.calls != 3 || fmt.Sprint(model.roles) != "[architect world_arbiter world_arbiter]" {
		t.Fatalf("invalid real two-stage recovery: %+v calls=%d roles=%v", report, model.calls, model.roles)
	}
	if len(recorded) < 2 || len(report.Call.UsageIDs) == 0 || len(draft.Call.UsageIDs) == 0 {
		t.Fatal("actual usage sources missing")
	}
	for _, id := range append(append([]string(nil), draft.Call.UsageIDs...), report.Call.UsageIDs...) {
		found := false
		for _, recordedID := range recorded {
			found = found || recordedID == id
		}
		if !found {
			t.Fatal("report cites a usage id absent from the actual recorder")
		}
	}
	loaded, loadedInput, err := st.LoadVerifiedArcRehearsal(report.ReportDigest)
	selectionMust(t, err)
	if !sameCharacterCycleValue(report, loaded) || loadedInput.InputDigest != input.InputDigest {
		t.Fatal("content addressed report did not reload")
	}
	if _, err := RunArcRehearsal(context.Background(), bootstrap.Config{}, models, st, input, accounting); err != nil || model.calls != 3 {
		t.Fatalf("completed rehearsal reran model: %v", err)
	}
	if !reflect.DeepEqual(before, arcRehearsalSourceFiles(t, st.Dir())) {
		t.Fatal("rehearsal wrote source, canon, official memory or world state")
	}
	rebuilt, err := BuildArcRehearsalInput(st, input)
	selectionMust(t, err)
	if rebuilt.InputDigest != input.InputDigest {
		t.Fatal("rehearsal output entered its own source digest")
	}
	selectionMust(t, st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "改变了的作者来源", NonNegotiables: []string{"新约束"}}))
	if _, err := RunArcRehearsal(context.Background(), bootstrap.Config{}, models, st, input, accounting); err == nil || model.calls != 3 {
		t.Fatal("source drift reused a report or started another model call")
	}
}

func TestArcRehearsalMissingReadableMaterialCannotBecomeReady(t *testing.T) {
	st, input := arcRehearsalTestInput(t)
	model := &arcRehearsalFakeModel{missing: true}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "arc-fake", model)}
	report, err := RunArcRehearsal(context.Background(), bootstrap.Config{}, models, st, input, ProjectedPlanningAccounting{RecordUsage: func(string, agentcore.AgentMessage) {}})
	selectionMust(t, err)
	if report.ReadyForDetail || report.Body.MaterialChecks[0].Status != "missing" {
		t.Fatal("missing document was presented as available")
	}
	bad := arcRehearsalTestBody(input, true)
	bad.MaterialChecks[0].Status = "available"
	if err := domain.ValidateArcRehearsalBody(input, bad); err == nil {
		t.Fatal("undefined readable resource accepted")
	}
	bad = arcRehearsalTestBody(input, false)
	bad.Chapters = bad.Chapters[:2]
	if err := domain.ValidateArcRehearsalBody(input, bad); err == nil {
		t.Fatal("partial arc called completed rehearsal")
	}
	tool := &submitArcRehearsalTool{input: input}
	raw, _ := json.Marshal(map[string]any{"body": arcRehearsalTestBody(input, false), "call": report.Call})
	if _, err := tool.Execute(context.Background(), raw); err == nil {
		t.Fatal("model could author its call proof")
	}
}

func TestArcRehearsalStoredInputAndReviewTamperingAreRejected(t *testing.T) {
	st, input := arcRehearsalTestInput(t)
	model := &arcRehearsalFakeModel{}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("test", "arc-fake", model)}
	report, err := RunArcRehearsal(context.Background(), bootstrap.Config{}, models, st, input, ProjectedPlanningAccounting{RecordUsage: func(string, agentcore.AgentMessage) {}})
	selectionMust(t, err)
	for _, kind := range []string{"reports", "inputs"} {
		t.Run(kind, func(t *testing.T) {
			digest := report.ReportDigest
			if kind == "inputs" {
				digest = input.InputDigest
			}
			path := filepath.Join(st.Dir(), store.ArcRehearsalRoot, kind, strings.TrimPrefix(digest, "sha256:")+".json")
			raw, err := os.ReadFile(path)
			selectionMust(t, err)
			var value map[string]any
			selectionMust(t, json.Unmarshal(raw, &value))
			if kind == "reports" {
				value["ready_for_detail"] = false
			} else {
				value["source_root"] = "sha256:" + strings.Repeat("f", 64)
			}
			changed, _ := json.Marshal(value)
			selectionMust(t, os.WriteFile(path, changed, 0o644))
			if _, _, err := store.NewStore(st.Dir()).LoadVerifiedArcRehearsal(report.ReportDigest); err == nil {
				t.Fatal("modified content-addressed source was accepted")
			}
			selectionMust(t, os.WriteFile(path, raw, 0o644))
		})
	}
	if model.calls != 2 {
		t.Fatal("artifact verification invoked a model")
	}
}
