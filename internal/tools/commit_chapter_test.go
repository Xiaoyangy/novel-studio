package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func testCharacterStageRecords(protagonist string, sideCharacters ...string) []domain.CharacterStageRecord {
	if protagonist == "" {
		protagonist = "主角"
	}
	records := []domain.CharacterStageRecord{{
		Character:           protagonist,
		Time:                "本章时间",
		Location:            "主场景",
		Status:              "存活",
		Environment:         "主线压力正在逼近",
		CurrentAction:       "处理本章核心事件",
		Pressure:            "必须在有限时间内做出选择",
		Decision:            "先保住当前目标，再确认下一步",
		MistakeOrMisbelief:  "误以为信息已经足够",
		KnowledgeBoundary:   "不知道配角线的完整遭遇",
		VisibleInChapter:    true,
		Evidence:            "测试正文",
		Transport:           "原地",
		TravelTime:          "0分钟",
		MeetingConstraint:   "主角已在主场景；不能凭空获知配角线",
		PersonalityDelta:    "压力下更谨慎",
		DeathState:          "存活",
		ProtagonistNotice:   "主角已亲历主线事件",
		TimelineConsistency: "与本章正文同步",
		NextPotential:       "下一章继续核验代价",
		Tags:                []string{"主角", "测试"},
	}}
	if len(sideCharacters) == 0 {
		sideCharacters = []string{"配角"}
	}
	for _, name := range sideCharacters {
		if strings.TrimSpace(name) == "" {
			continue
		}
		records = append(records, domain.CharacterStageRecord{
			Character:           name,
			Time:                "本章同时段",
			Location:            "支线场景",
			Status:              "存活",
			Environment:         "支线规则压力正在形成",
			CurrentAction:       "处理自己的风险，不围着主角待命",
			Pressure:            "信息不足且无法立刻脱身",
			Decision:            "按自身利益先稳住现场",
			MistakeOrMisbelief:  "误以为主角能立刻提供帮助",
			KnowledgeBoundary:   "不知道主角手里的完整信息",
			VisibleInChapter:    false,
			Evidence:            "测试台账",
			Transport:           "步行/公共交通或原地被困",
			TravelTime:          "同城至少15分钟；被困时为0但不能见面",
			MeetingConstraint:   "本章不能随叫随到，需要交通时间或通行凭证",
			PersonalityDelta:    "恐惧后更倾向保守决策",
			DeathState:          "存活；死亡未触发",
			ProtagonistNotice:   "后续通过电话、账单或目击者传回主角",
			TimelineConsistency: "与主角线同一时间段并行",
			NextPotential:       "携带支线风险回归",
			Tags:                []string{"配角", "支线"},
		})
	}
	return records
}

func writeCleanMechanicalGate(t *testing.T, s *store.Store, chapter int) {
	t.Helper()
	body, err := s.Drafts.LoadChapterText(chapter)
	if err != nil {
		t.Fatal(err)
	}
	payload := reviewreport.MechanicalGatePayload{
		Chapter:    chapter,
		BodySHA256: reviewreport.BodySHA256(body),
		AIGCReport: aigc.Report{
			AIGCPercent: 1, AIRatioPercent: 1, BlendedAIGCPercent: 1,
			Stats: aigc.Stats{Hanzi: 3000},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), "reviews", fmt.Sprintf("%02d_ai_gate.json", chapter))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCommitChapterRejectsWordContractBeforeFinalWrite(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRules.Save(&rules.Snapshot{Structured: rules.Structured{
		ChapterWords: &rules.WordRange{Min: 10, Max: 20},
	}}); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("长", 25)
	if err := s.Drafts.SaveDraft(1, content); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "超字数候选不能覆盖终稿。",
		"characters":              []string{"主角", "配角"},
		"key_events":              []string{"候选生成"},
		"character_stage_records": testCharacterStageRecords("主角", "配角"),
	})
	if _, err := NewCommitChapterTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "字数硬门禁未通过") {
		t.Fatalf("expected pre-commit word gate, got %v", err)
	}
	if final, _ := s.Drafts.LoadChapterText(1); final != "" {
		t.Fatalf("invalid draft must not replace final: %q", final)
	}
	progress, _ := s.Progress.Load()
	if len(progress.CompletedChapters) != 0 {
		t.Fatalf("invalid draft must not advance progress: %+v", progress.CompletedChapters)
	}
}

func TestMergeRewriteCharacterStageInheritsUnchangedCast(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	existing := testCharacterStageRecords("林澈", "沈知遥", "贺骁")
	if err := s.SaveCharacterStageRecords(1, existing); err != nil {
		t.Fatal(err)
	}
	submitted := []domain.CharacterStageRecord{existing[0]}
	submitted[0].Decision = "压缩正文后仍按原计划承担付款责任"
	merged := mergeRewriteCharacterStage(s, 1, submitted)
	if len(merged) != 3 {
		t.Fatalf("partial rewrite metadata should inherit omitted cast: %+v", merged)
	}
	if merged[0].Decision != submitted[0].Decision || merged[1].Character != "沈知遥" || merged[2].Character != "贺骁" {
		t.Fatalf("rewrite stage merge lost or failed to update records: %+v", merged)
	}
}

func TestCommitChapterSchemaDescribesFeedbackAsObject(t *testing.T) {
	tool := NewCommitChapterTool(store.NewStore(t.TempDir()))
	schema := tool.Schema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing: %#v", schema["properties"])
	}
	feedback, ok := props["feedback"].(map[string]any)
	if !ok {
		t.Fatalf("feedback schema missing: %#v", props["feedback"])
	}
	desc, _ := feedback["description"].(string)
	if !strings.Contains(desc, "JSON object") || !strings.Contains(desc, "字符串化 JSON") {
		t.Fatalf("feedback description should warn against stringified JSON, got %q", desc)
	}
	if got := feedback["type"]; got != "object" {
		t.Fatalf("feedback type = %v, want object", got)
	}
}

func TestRequireCurrentDraftConsistencyRejectsMissingAndStaleCheck(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	const first = "# 第一章 测试\n\n第一版正文。"
	if err := s.Drafts.SaveDraft(1, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if err := requireCurrentDraftConsistency(s, 1, first); err == nil || !strings.Contains(err.Error(), "check_consistency") {
		t.Fatalf("missing consistency check should block commit, got %v", err)
	}
	args, _ := json.Marshal(map[string]any{"chapter": 1})
	if _, err := NewCheckConsistencyTool(s).Execute(context.Background(), args); err != nil {
		t.Fatalf("check_consistency: %v", err)
	}
	if err := requireCurrentDraftConsistency(s, 1, first); err != nil {
		t.Fatalf("current consistency check rejected: %v", err)
	}

	const second = "# 第一章 测试\n\n第二版正文。"
	if err := s.Drafts.SaveDraft(1, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	if err := requireCurrentDraftConsistency(s, 1, second); err == nil || !strings.Contains(err.Error(), "check_consistency") {
		t.Fatalf("stale consistency check should block commit, got %v", err)
	}
	if _, err := NewCheckConsistencyTool(s).Execute(context.Background(), args); err != nil {
		t.Fatalf("check edited draft: %v", err)
	}
	if err := requireCurrentDraftConsistency(s, 1, second); err != nil {
		t.Fatalf("edit followed by current consistency check should pass: %v", err)
	}

	const third = "# 第一章 测试\n\n第三版正文。"
	if err := s.Drafts.SaveDraft(1, third); err != nil {
		t.Fatal(err)
	}
	if err := requireCurrentDraftConsistency(s, 1, third); err == nil {
		t.Fatal("unchecked edit after consistency check must still be blocked")
	}
}

func TestRequireDraftPlanTitleMatchBlocksDrift(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "失业饭桌"}); err != nil {
		t.Fatal(err)
	}
	if err := requireDraftPlanTitleMatch(s, 1, "# 第一章 失业饭桌\n\n正文。"); err != nil {
		t.Fatalf("matching title rejected: %v", err)
	}
	if err := requireDraftPlanTitleMatch(s, 1, "# 第一章 回乡第一天\n\n正文。"); err == nil || !strings.Contains(err.Error(), "标题与计划标题不一致") {
		t.Fatalf("title drift should block commit, got %v", err)
	}
}

func TestCommitChapterRejectsNonPendingRewrite(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := store.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := store.Progress.SetPendingRewrites([]int{2}, "测试重写"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}
	if err := store.Drafts.SaveDraft(3, "这是错误章节的正文。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewCommitChapterTool(store)
	args, err := json.Marshal(map[string]any{
		"chapter":         3,
		"summary":         "错误提交",
		"characters":      []string{"主角"},
		"key_events":      []string{"误提交"},
		"timeline_events": []any{},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected commit to be rejected during rewrite flow")
	}

	if _, err := os.Stat(dir + "/chapters/03.md"); !os.IsNotExist(err) {
		t.Fatalf("chapter should not be persisted, stat err=%v", err)
	}

	progress, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if len(progress.CompletedChapters) != 1 || progress.CompletedChapters[0] != 2 {
		t.Fatalf("completed chapters should only contain original chapter 2, got %v", progress.CompletedChapters)
	}
	if progress.CurrentChapter != 3 {
		t.Fatalf("current chapter should not advance beyond original progress, got %d", progress.CurrentChapter)
	}
}

func TestCommitChapterAllowsPendingRewrite(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := store.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := store.Progress.SetPendingRewrites([]int{2}, "测试重写"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}
	if err := store.Drafts.SaveDraft(2, "这是正确待重写章节的正文。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewCommitChapterTool(store)
	args, err := json.Marshal(map[string]any{
		"chapter":         2,
		"summary":         "正确提交",
		"characters":      []string{"主角"},
		"key_events":      []string{"完成重写"},
		"timeline_events": []any{},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(dir + "/chapters/02.md"); err != nil {
		t.Fatalf("chapter should be persisted: %v", err)
	}

	progress, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if len(progress.CompletedChapters) != 1 || progress.CompletedChapters[0] != 2 {
		t.Fatalf("unexpected completed chapters: %v", progress.CompletedChapters)
	}
	pending, err := store.Signals.LoadPendingCommit()
	if err != nil {
		t.Fatalf("LoadPendingCommit: %v", err)
	}
	if pending != nil {
		t.Fatalf("expected pending commit cleared, got %+v", pending)
	}
}

func TestCommitChapterSedimentsRAGIndexAndRewriteUpserts(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 5); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "许闻溪在数据室里核对药盒订单。她把邱梅的配送记录、模型画像字段和审计盒日志逐项保全，随后决定把证据交给梁渡复核。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "许闻溪保全药盒订单与审计盒日志，确认家庭照护数据进入模型画像。",
		"characters":              []string{"许闻溪", "邱梅", "梁渡"},
		"key_events":              []string{"保全药盒订单", "审计盒日志复核"},
		"character_stage_records": testCharacterStageRecords("许闻溪", "邱梅", "梁渡"),
		"timeline_events": []domain.TimelineEvent{{
			Time:       "当晚",
			Event:      "许闻溪完成药盒订单证据保全",
			Characters: []string{"许闻溪"},
		}},
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload struct {
		RAGIndexed bool   `json:"rag_indexed"`
		RAGError   string `json:"rag_error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal commit output: %v", err)
	}
	if !payload.RAGIndexed || payload.RAGError != "" {
		t.Fatalf("expected rag indexed without error, got indexed=%v err=%q", payload.RAGIndexed, payload.RAGError)
	}
	assertChapterRAGChunk(t, s, 1, "家庭照护数据进入模型画像", 1)

	if err := s.Progress.SetPendingRewrites([]int{1}, "测试返工覆盖 RAG"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "返工后，许闻溪只保留授权链路和蓝线冻结证据，把无关猜测删掉。"); err != nil {
		t.Fatalf("SaveDraft rewrite: %v", err)
	}
	rewriteArgs, _ := json.Marshal(map[string]any{
		"chapter":    1,
		"summary":    "返工后许闻溪聚焦授权链路与蓝线冻结证据。",
		"characters": []string{"许闻溪"},
		"key_events": []string{"蓝线冻结证据收束"},
	})
	if _, err := tool.Execute(context.Background(), rewriteArgs); err != nil {
		t.Fatalf("Execute rewrite: %v", err)
	}
	assertChapterRAGChunk(t, s, 1, "蓝线冻结证据", 1)
}

func TestCommitChapterSavesCharacterStageRecords(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "林砚", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "林砚守在山门外，听见外门钟响后没有立刻冲进去，而是先问登记弟子试炼名册是否已经封存。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    1,
		"summary":    "林砚在入门夜核对试炼名册。",
		"characters": []string{"林砚", "登记弟子"},
		"key_events": []string{"核对试炼名册"},
		"character_stage_records": []domain.CharacterStageRecord{{
			Character:           "林砚",
			Time:                "入门夜",
			Location:            "山门外",
			Status:              "存活",
			Environment:         "钟声已响，登记口即将关闭",
			CurrentAction:       "先核对名册封存状态，再决定是否入内",
			Pressure:            "错过登记会失去试炼资格",
			Decision:            "不抢门，先确认规则边界",
			MistakeOrMisbelief:  "以为名册封存后就不能追加备注",
			KnowledgeBoundary:   "不知道内门执事已提前调换名册",
			VisibleInChapter:    true,
			Evidence:            "正文写到林砚先问登记弟子试炼名册是否已经封存",
			Transport:           "原地",
			TravelTime:          "0分钟",
			MeetingConstraint:   "林砚在山门外，无法同时知道登记弟子后续递交名册的支线",
			PersonalityDelta:    "更确认自己不能抢先签没读懂的名册",
			DeathState:          "存活",
			ProtagonistNotice:   "主角已在现场直接获知",
			TimelineConsistency: "与第一章山门登记事件同时间发生",
			NextPotential:       "第二章可由名册备注引出试炼分组争议",
			Tags:                []string{"主角", "规则核验"},
		}, {
			Character:           "登记弟子",
			Time:                "入门夜同一刻",
			Location:            "山门登记口内侧",
			Status:              "存活",
			Environment:         "钟声催促，名册规则和执事压力同时压下来",
			CurrentAction:       "夹住名册边角，决定是否给林砚补问一句",
			Pressure:            "若放错人会被执事追责，若拦错人也会惹出争议",
			Decision:            "先按旧规矩压住入口，再把异常递给上级",
			MistakeOrMisbelief:  "误以为内门执事只是在试探林砚",
			KnowledgeBoundary:   "不知道名册已被提前调换，不知道林砚旧伤来历",
			VisibleInChapter:    true,
			Evidence:            "正文写到登记弟子被林砚追问名册封存状态",
			Transport:           "原地值守",
			TravelTime:          "0分钟；值守期间不能离开登记口",
			MeetingConstraint:   "本章只能在山门口与林砚接触，无法随叫随到其他场景",
			PersonalityDelta:    "从机械执行规矩转向害怕背锅",
			DeathState:          "存活",
			ProtagonistNotice:   "主角已通过其动作和答复知道他有所隐瞒",
			TimelineConsistency: "与第一章山门登记事件同时间发生",
			NextPotential:       "第二章可把名册异常交给内门执事",
			Tags:                []string{"配角", "登记口"},
		}},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	records, err := s.LoadCharacterStageRecords(1)
	if err != nil {
		t.Fatalf("LoadCharacterStageRecords: %v", err)
	}
	if len(records) != 2 || records[0].Character != "林砚" || records[0].Chapter != 1 {
		t.Fatalf("unexpected stage records: %+v", records)
	}
	md, err := os.ReadFile(dir + "/meta/character_stage/001.md")
	if err != nil {
		t.Fatalf("read stage markdown: %v", err)
	}
	if !strings.Contains(string(md), "第二章可由名册备注引出试炼分组争议") {
		t.Fatalf("stage markdown missing next potential: %s", md)
	}
	journeyMD, err := os.ReadFile(filepath.Join(dir, "meta", "side_character_journeys", "001.md"))
	if err != nil {
		t.Fatalf("read side character journey markdown: %v", err)
	}
	if text := string(journeyMD); strings.Contains(text, "## 林砚") || !strings.Contains(text, "## 登记弟子") {
		t.Fatalf("side character journey should exclude protagonist and include side role: %s", text)
	}
	delta, err := s.LoadChapterWorldDelta(1)
	if err != nil || delta == nil {
		t.Fatalf("LoadChapterWorldDelta: %v", err)
	}
	if len(delta.CharacterDeltas) != 2 || delta.CharacterDeltas[1].Character != "登记弟子" {
		t.Fatalf("unexpected chapter world delta: %+v", delta)
	}
	if err := s.Progress.SetPendingRewrites([]int{1}, "测试 rewrite 同步角色台账"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "返工后，林砚没有追问名册封存，而是先逼登记弟子说出谁递过封条。"); err != nil {
		t.Fatalf("SaveDraft rewrite: %v", err)
	}
	rewriteStage := []domain.CharacterStageRecord{
		testCharacterStageRecords("林砚", "登记弟子")[0],
		testCharacterStageRecords("林砚", "登记弟子")[1],
	}
	rewriteStage[1].CurrentAction = "把封条来源压在舌底，准备先去找递封条的人"
	rewriteStage[1].PersonalityDelta = "从怕背锅变成主动切割责任来源"
	rewriteArgs, _ := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "返工后林砚逼出封条来源。",
		"characters":              []string{"林砚", "登记弟子"},
		"key_events":              []string{"封条来源浮出"},
		"character_stage_records": rewriteStage,
	})
	if _, err := tool.Execute(context.Background(), rewriteArgs); err != nil {
		t.Fatalf("Execute rewrite: %v", err)
	}
	records, err = s.LoadCharacterStageRecords(1)
	if err != nil {
		t.Fatalf("LoadCharacterStageRecords after rewrite: %v", err)
	}
	if got := records[1].PersonalityDelta; !strings.Contains(got, "主动切割责任来源") {
		t.Fatalf("rewrite should update character stage, got %+v", records[1])
	}
	delta, err = s.LoadChapterWorldDelta(1)
	if err != nil || delta == nil {
		t.Fatalf("LoadChapterWorldDelta after rewrite: %v", err)
	}
	if !delta.Rewrite || !strings.Contains(delta.CharacterDeltas[1].PersonalityDelta, "主动切割责任来源") {
		t.Fatalf("rewrite should overwrite chapter world delta, got %+v", delta)
	}
}

func TestCommitChapterQueuesPolishOnHighAIGC(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	text := strings.Repeat("首先，主角感到前所未有的恐惧，这意味着局势已经发生了变化。其次，他终于明白自己必须面对命运的安排。最后，所有人都意识到问题的严重性。\n", 70)
	if err := s.Drafts.SaveDraft(1, text); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "高 AI 风险样章",
		"characters":              []string{"主角", "配角"},
		"key_events":              []string{"模板化推进"},
		"character_stage_records": testCharacterStageRecords("主角", "配角"),
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload struct {
		Flow           string `json:"flow"`
		RuleViolations []struct {
			Rule     string `json:"rule"`
			Severity string `json:"severity"`
		} `json:"rule_violations"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	foundAIGCError := false
	for _, v := range payload.RuleViolations {
		if v.Rule == "aigc_ratio" && v.Severity == "error" {
			foundAIGCError = true
		}
	}
	if !foundAIGCError {
		t.Fatalf("expected aigc_ratio error, got %+v", payload.RuleViolations)
	}
	if payload.Flow != string(domain.FlowPolishing) {
		t.Fatalf("flow = %q, want polishing", payload.Flow)
	}
	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if progress.Flow != domain.FlowPolishing || len(progress.PendingRewrites) != 1 || progress.PendingRewrites[0] != 1 {
		t.Fatalf("expected chapter 1 queued for polishing, progress=%+v", progress)
	}
	if _, err := os.Stat(dir + "/reviews/01_ai_gate.json"); err != nil {
		t.Fatalf("expected unified reviews ai gate json: %v", err)
	}
	if _, err := os.Stat(dir + "/reviews/01.md"); err != nil {
		t.Fatalf("expected unified review markdown: %v", err)
	}
	if _, err := os.Stat(dir + "/reviews/第001章_AI味审核.md"); !os.IsNotExist(err) {
		t.Fatalf("new commits should not create legacy ai markdown, err=%v", err)
	}
	if _, err := os.Stat(dir + "/reviews_ai"); !os.IsNotExist(err) {
		t.Fatalf("new commits should not create reviews_ai, err=%v", err)
	}
}

func TestCommitChapterQueuesPolishOnSecondAlgorithmDeprecatedEngine(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SavePremise("《她的第二算法》女频女性职场成长文，主角许闻溪。"); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	text := "许闻溪站在发布会侧台，听见有人说日志窗口还在，原始包稍后走合规邮件。"
	if err := s.Drafts.SaveDraft(1, text); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "旧版证据链污染样章",
		"characters":              []string{"许闻溪", "梁渡"},
		"key_events":              []string{"旧版术语进入正文"},
		"character_stage_records": testCharacterStageRecords("许闻溪", "梁渡"),
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload struct {
		Flow           string `json:"flow"`
		RuleViolations []struct {
			Rule     string `json:"rule"`
			Target   string `json:"target"`
			Severity string `json:"severity"`
		} `json:"rule_violations"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	found := map[string]bool{}
	for _, v := range payload.RuleViolations {
		if v.Rule == "deprecated_story_engine" && v.Severity == "error" {
			found[v.Target] = true
		}
	}
	for _, want := range []string{"日志窗口", "原始包", "合规邮件"} {
		if !found[want] {
			t.Fatalf("expected deprecated_story_engine %q, got %+v", want, payload.RuleViolations)
		}
	}
	if payload.Flow != string(domain.FlowPolishing) {
		t.Fatalf("flow = %q, want polishing", payload.Flow)
	}
	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if progress.Flow != domain.FlowPolishing || len(progress.PendingRewrites) != 1 || progress.PendingRewrites[0] != 1 {
		t.Fatalf("expected chapter 1 queued for polishing, progress=%+v", progress)
	}
	rawGate, err := os.ReadFile(filepath.Join(dir, "reviews", "01_ai_gate.json"))
	if err != nil {
		t.Fatalf("ReadFile ai gate: %v", err)
	}
	if !strings.Contains(string(rawGate), "deprecated_story_engine") {
		t.Fatalf("expected ai gate to include deprecated_story_engine:\n%s", rawGate)
	}
}

func assertChapterRAGChunk(t *testing.T, s *store.Store, chapter int, want string, wantCount int) {
	t.Helper()
	state, err := s.RAG.LoadIndexState()
	if err != nil {
		t.Fatalf("LoadIndexState: %v", err)
	}
	if state == nil {
		t.Fatal("expected rag index state")
	}
	count := 0
	for _, chunk := range state.Chunks {
		if chunk.SourcePath != fmt.Sprintf("summaries/%02d.json", chapter) {
			continue
		}
		count++
		if chunk.SourceKind != "chapter_summary_facts" {
			t.Fatalf("source_kind = %q, want chapter_summary_facts", chunk.SourceKind)
		}
		if !strings.Contains(chunk.Text, want) {
			t.Fatalf("rag chunk text missing %q: %s", want, chunk.Text)
		}
	}
	if count != wantCount {
		t.Fatalf("chapter rag chunk count = %d, want %d", count, wantCount)
	}
}

func TestMechanicalGateRetryLimitHandsOffToChapterReview(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewCommitChapterTool(s)
	commitArgs, _ := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "高 AI 风险样章",
		"characters":              []string{"主角", "配角"},
		"key_events":              []string{"模板化推进"},
		"character_stage_records": testCharacterStageRecords("主角", "配角"),
	})

	for i := 0; i < maxMechanicalGateCommitAttempts; i++ {
		text := strings.Repeat(fmt.Sprintf("版本%d。首先，主角感到前所未有的恐惧，这意味着局势已经发生了变化。其次，他终于明白自己必须面对命运的安排。最后，所有人都意识到问题的严重性。\n", i), 70)
		if err := s.Drafts.SaveDraft(1, text); err != nil {
			t.Fatalf("SaveDraft(%d): %v", i, err)
		}
		raw, err := tool.Execute(context.Background(), commitArgs)
		if err != nil {
			t.Fatalf("Execute(%d): %v", i, err)
		}
		progress, err := s.Progress.Load()
		if err != nil {
			t.Fatalf("LoadProgress(%d): %v", i, err)
		}
		if i < maxMechanicalGateCommitAttempts-1 {
			if progress.Flow != domain.FlowPolishing || len(progress.PendingRewrites) != 1 || progress.PendingRewrites[0] != 1 {
				t.Fatalf("attempt %d should keep chapter queued, progress=%+v", i, progress)
			}
			continue
		}
		if progress.Flow != domain.FlowWriting || len(progress.PendingRewrites) != 0 {
			t.Fatalf("retry limit should hand off to chapter review, progress=%+v", progress)
		}
		var payload struct {
			RuleViolations []struct {
				Rule string `json:"rule"`
			} `json:"rule_violations"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("Unmarshal final payload: %v", err)
		}
		foundLimit := false
		for _, v := range payload.RuleViolations {
			if v.Rule == "mechanical_gate_retry_limit" {
				foundLimit = true
			}
		}
		if !foundLimit {
			t.Fatalf("expected retry-limit warning, got %+v", payload.RuleViolations)
		}
	}
}

func TestBlockingViolationReasonIncludesTemplatedDialogueChain(t *testing.T) {
	reason := blockingViolationReason([]rules.Violation{{
		Rule:     "templated_dialogue_chain",
		Target:   "点名/停笔/补口径/追问",
		Actual:   1,
		Limit:    0,
		Severity: rules.SeverityWarning,
	}})
	if !strings.Contains(reason, "templated_dialogue_chain") {
		t.Fatalf("reason = %q, want templated_dialogue_chain", reason)
	}
}

func TestImmediateMechanicalGateBlocksRendererReadabilityFailures(t *testing.T) {
	for _, rule := range []string{
		"abstract_system_reassurance",
		"opaque_procedure_jargon",
		"dialogue_action_lead_repetition",
	} {
		if !immediateMechanicalGateFailure(rules.Violation{Rule: rule, Severity: rules.SeverityWarning}) {
			t.Fatalf("%s should block before review", rule)
		}
	}
}

func TestAIGCViolationUsesBlendedGateForSegmentFloorOnlyRisk(t *testing.T) {
	violations := aigcViolation(aigc.Report{
		Engine:                 aigc.Engine,
		AIGCPercent:            80,
		AIRatioPercent:         80,
		BlendedAIGCPercent:     7.52,
		Stats:                  aigc.Stats{Hanzi: 6000}, // 长章多检测片段才允许 blended 降权
		SegmentRiskFloor:       80,
		ContentIntegrityFloor:  0,
		ZhuqueCompositePercent: 29.9,
		LatestDetectorProxy: aigc.DetectorProxy{
			CompositePercent: 5.2,
		},
	})
	if len(violations) != 1 {
		t.Fatalf("expected one warning violation, got %+v", violations)
	}
	if violations[0].Severity != "warning" {
		t.Fatalf("expected warning, got %+v", violations[0])
	}
	if violations[0].Actual != 7.52 {
		t.Fatalf("expected blended gate actual 7.52, got %+v", violations[0].Actual)
	}
}

func TestAIGCViolationUsesBlendedGateForMediumCompositeSegmentRisk(t *testing.T) {
	violations := aigcViolation(aigc.Report{
		Engine:                 aigc.Engine,
		AIGCPercent:            80,
		AIRatioPercent:         80,
		BlendedAIGCPercent:     13.08,
		Stats:                  aigc.Stats{Hanzi: 6000}, // 长章多检测片段才允许 blended 降权
		SegmentRiskFloor:       80,
		ContentIntegrityFloor:  0,
		ZhuqueCompositePercent: 17.4,
		LatestDetectorProxy: aigc.DetectorProxy{
			CompositePercent: 13.2,
		},
	})
	if len(violations) != 1 {
		t.Fatalf("expected one warning violation, got %+v", violations)
	}
	if violations[0].Severity != "warning" {
		t.Fatalf("expected warning, got %+v", violations[0])
	}
	if violations[0].Actual != 13.08 {
		t.Fatalf("expected blended gate actual 13.08, got %+v", violations[0].Actual)
	}
}

func TestAIGCViolationUsesBlendedGateForBorderlineSegmentRisk(t *testing.T) {
	violations := aigcViolation(aigc.Report{
		Engine:                 aigc.Engine,
		AIGCPercent:            80,
		AIRatioPercent:         80,
		BlendedAIGCPercent:     19.03,
		Stats:                  aigc.Stats{Hanzi: 6000}, // 长章多检测片段才允许 blended 降权
		SegmentRiskFloor:       80,
		ContentIntegrityFloor:  0,
		ZhuqueCompositePercent: 17.4,
		LatestDetectorProxy: aigc.DetectorProxy{
			CompositePercent: 20.22,
		},
	})
	if len(violations) != 1 {
		t.Fatalf("expected one warning violation, got %+v", violations)
	}
	if violations[0].Severity != "warning" {
		t.Fatalf("expected warning, got %+v", violations[0])
	}
	if violations[0].Actual != 19.03 {
		t.Fatalf("expected blended gate actual 19.03, got %+v", violations[0].Actual)
	}
}

// TestAIGCViolationShortChapterUsesRawSegmentFloor 验证用户要求：约 3000 字的短章会被
// 读者一次性丢进检测器（单检测片段），此时 segment_risk_floor 就是读者看到的真实风险，
// 不能被多片段 blended 平均稀释成低分放行——必须按 raw floor 判 error 打回，而不是 warning。
func TestAIGCViolationShortChapterUsesRawSegmentFloor(t *testing.T) {
	violations := aigcViolation(aigc.Report{
		Engine:                 aigc.Engine,
		AIGCPercent:            80,
		AIRatioPercent:         80,
		BlendedAIGCPercent:     7.52,
		Stats:                  aigc.Stats{Hanzi: 3000}, // 短章：单检测片段，不许 blended 降权
		SegmentRiskFloor:       80,
		ZhuqueCompositePercent: 29.9,
		LatestDetectorProxy:    aigc.DetectorProxy{CompositePercent: 5.2},
	})
	if len(violations) != 1 {
		t.Fatalf("短章高 segment floor 应产生一条违规, got %+v", violations)
	}
	if violations[0].Severity != "error" {
		t.Fatalf("短章 80%% floor 应判 error 打回, got %+v", violations[0])
	}
	if violations[0].Actual != 80.0 {
		t.Fatalf("短章应按 raw floor 80 判, got %+v", violations[0].Actual)
	}
}

// TestCommitChapterUpdatesCastLedger 验证：commit_chapter 把本章 characters 累加进 cast_ledger，
// cast_intros 提供的 brief_role 被采用，且 characters.json 中的核心角色不进入 ledger。
func TestCommitChapterUpdatesCastLedger(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	// 设定核心角色档案（这些不应进 cast_ledger）
	if err := s.Characters.Save([]domain.Character{
		{Name: "林墨", Role: "主角", Tier: "core"},
		{Name: "李清砚", Role: "导师", Tier: "important"},
	}); err != nil {
		t.Fatalf("Save core characters: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "第一章正文，林墨遇到客栈老板老周与小厮阿云。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "林墨入住客栈",
		"characters":              []string{"林墨", "李清砚", "老周", "阿云"},
		"key_events":              []string{"入住"},
		"character_stage_records": testCharacterStageRecords("林墨", "李清砚", "老周", "阿云"),
		"cast_intros": []any{
			map[string]any{"name": "老周", "brief_role": "客栈老板"},
			map[string]any{"name": "阿云", "brief_role": "客栈小厮"},
		},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	entries, err := s.Cast.Load()
	if err != nil {
		t.Fatalf("Cast.Load: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 ledger entries (老周/阿云), got %d: %+v", len(entries), entries)
	}
	byName := map[string]domain.CastEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if e, ok := byName["老周"]; !ok || e.BriefRole != "客栈老板" || e.FirstSeenChapter != 1 {
		t.Errorf("老周 entry wrong: %+v", e)
	}
	if e, ok := byName["阿云"]; !ok || e.BriefRole != "客栈小厮" || e.AppearanceCount != 1 {
		t.Errorf("阿云 entry wrong: %+v", e)
	}
	if _, ok := byName["林墨"]; ok {
		t.Errorf("核心角色 林墨 不应进 ledger")
	}
	if _, ok := byName["李清砚"]; ok {
		t.Errorf("核心角色 李清砚 不应进 ledger")
	}
}

func TestCommitChapterReplayAfterPartialCommitDoesNotDuplicateWorldState(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "第一章正文，林墨遇到黑影并突破。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	timeline := []domain.TimelineEvent{{
		Chapter:    1,
		Time:       "清晨",
		Event:      "林墨遇到黑影",
		Characters: []string{"林墨"},
	}}
	stateChanges := []domain.StateChange{{
		Chapter:  1,
		Entity:   "林墨",
		Field:    "realm",
		OldValue: "凡人",
		NewValue: "练气期",
	}}
	foreshadow := []domain.ForeshadowUpdate{{
		ID:          "f1",
		Action:      "plant",
		Description: "黑影身份",
	}}

	// 模拟 commit_chapter 已写入世界状态，但尚未 MarkChapterComplete 时进程崩溃。
	if err := s.World.AppendTimelineEvents(timeline); err != nil {
		t.Fatalf("AppendTimelineEvents seed: %v", err)
	}
	if err := s.World.AppendStateChanges(stateChanges); err != nil {
		t.Fatalf("AppendStateChanges seed: %v", err)
	}
	if err := s.World.UpdateForeshadow(1, foreshadow); err != nil {
		t.Fatalf("UpdateForeshadow seed: %v", err)
	}
	if err := s.Signals.SavePendingCommit(domain.PendingCommit{
		Chapter: 1,
		Stage:   domain.CommitStageStateApplied,
		Summary: "半提交摘要",
	}); err != nil {
		t.Fatalf("SavePendingCommit: %v", err)
	}

	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":                 1,
		"summary":                 "林墨遇到黑影并突破",
		"characters":              []string{"林墨", "黑影"},
		"key_events":              []string{"遇到黑影", "突破"},
		"character_stage_records": testCharacterStageRecords("林墨", "黑影"),
		"timeline_events":         timeline,
		"state_changes":           stateChanges,
		"foreshadow_updates":      foreshadow,
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute replay: %v", err)
	}

	events, _ := s.World.LoadTimeline()
	if len(events) != 1 {
		t.Fatalf("timeline duplicated after replay, got %d: %+v", len(events), events)
	}
	changes, _ := s.World.LoadStateChanges()
	if len(changes) != 1 {
		t.Fatalf("state changes duplicated after replay, got %d: %+v", len(changes), changes)
	}
	ledger, _ := s.World.LoadForeshadowLedger()
	if len(ledger) != 1 {
		t.Fatalf("foreshadow duplicated after replay, got %d: %+v", len(ledger), ledger)
	}
	pending, _ := s.Signals.LoadPendingCommit()
	if pending != nil {
		t.Fatalf("pending commit should be cleared, got %+v", pending)
	}
	if cp := s.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp == nil {
		t.Fatal("commit checkpoint should be written")
	}
}

func TestCommitChapterCompletedProgressResumesQualityBeforeClearingPending(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatal(err)
	}
	body := "# 第一章\n\n林墨回到县城，先把门店钥匙接了过来。"
	if err := s.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(1, len([]rune(body)), "desire", "quest"); err != nil {
		t.Fatal(err)
	}
	if err := s.Signals.SavePendingCommit(domain.PendingCommit{
		Chapter: 1,
		Stage:   domain.CommitStageStateApplied,
		Summary: "已写状态但质量门禁尚未落盘",
	}); err != nil {
		t.Fatal(err)
	}

	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":         1,
		"summary":         "林墨回县城接下门店",
		"characters":      []string{"林墨"},
		"key_events":      []string{"接下门店"},
		"hook_type":       "desire",
		"dominant_strand": "quest",
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("resume completed commit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reviews", "01_ai_gate.json")); err != nil {
		t.Fatalf("quality artifact must exist before pending is cleared: %v", err)
	}
	if pending, _ := s.Signals.LoadPendingCommit(); pending != nil {
		t.Fatalf("pending commit should clear only after all recovery stages: %+v", pending)
	}
	if cp := s.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp == nil {
		t.Fatal("commit checkpoint missing after recovery")
	}
}

// TestCommitChapterRejectsPolishWithoutDraftChange 验证：已完成章节进入打磨/重写队列后，
// 若 writer 跳过 draft_chapter 直接 commit（drafts 与 chapters 内容完全相同），
// commit_chapter 必须拒绝，强制 writer 先调 draft_chapter 写入新版本。
// TestCommitChapterNonLayeredRecompletesAfterReworkReview 验证非分层书完本后经 reopen 返工，
// 改完章节 commit 只排空队列并清掉旧审阅；必须重新章审 accept 后才回到 complete。
func TestCommitChapterNonLayeredRecompletesAfterRework(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierMid); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}

	// 两章写完并完结。第 2 章备齐 drafts/chapters，供返工提交。
	if err := s.Drafts.SaveFinalChapter(1, "第一章已提交终稿。"); err != nil {
		t.Fatalf("SaveFinalChapter ch1: %v", err)
	}
	ch2 := "第二章原始正文，用于模拟已提交终稿。"
	if err := s.Drafts.SaveDraft(2, ch2); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, ch2); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 100, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete(1): %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(ch2)), "", ""); err != nil {
		t.Fatalf("MarkChapterComplete(2): %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("SaveReview ch1: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 2, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("SaveReview ch2: %v", err)
	}
	if err := s.Progress.MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}

	// reopen 第 2 章 → phase 回 writing、PendingRewrites=[2]、flow=rewriting
	if err := s.Progress.Reopen([]int{2}, "返工"); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	// 返工提交（草稿需与终稿不同才放行）
	if err := s.Drafts.SaveDraft(2, ch2+"\n\n配角问：“你真决定这样收尾？”主角迟疑了一下：“先改完这一处，我再签字。”"); err != nil {
		t.Fatalf("SaveDraft (reworked): %v", err)
	}
	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":                 2,
		"summary":                 "返工后摘要",
		"characters":              []string{"主角", "配角"},
		"key_events":              []string{"清理"},
		"character_stage_records": testCharacterStageRecords("主角", "配角"),
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute rework commit: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if payload["book_complete"] == true {
		t.Errorf("book_complete = %v, want false before renewed review", payload["book_complete"])
	}

	p, _ := s.Progress.Load()
	if p.Phase == domain.PhaseComplete {
		t.Errorf("phase = %s, want writing before renewed review", p.Phase)
	}
	if len(p.PendingRewrites) != 0 {
		t.Errorf("PendingRewrites = %v, want empty", p.PendingRewrites)
	}

	writeCleanMechanicalGate(t, s, 2)
	reviewArgs, _ := json.Marshal(map[string]any{
		"chapter":         2,
		"scope":           "chapter",
		"dimensions":      acceptDimensions(),
		"issues":          []map[string]any{},
		"contract_status": "met",
		"contract_notes":  "返工后章级审阅通过。",
		"verdict":         "accept",
		"summary":         "返工后可收尾。",
	})
	if _, err := NewSaveReviewTool(s).Execute(context.Background(), reviewArgs); err != nil {
		t.Fatalf("SaveReview after rework: %v", err)
	}
	p, _ = s.Progress.Load()
	if p.Phase != domain.PhaseComplete {
		r1, _ := s.World.LoadReview(1)
		r2, _ := s.World.LoadReview(2)
		voice, _ := s.AIVoice.LoadRedFlags(2)
		t.Errorf("phase = %s, want complete after renewed review; pending=%v r1=%+v r2=%+v voice=%+v", p.Phase, p.PendingRewrites, r1, r2, voice)
	}
}

// TestCommitChapterLayeredReopenRecompletesAfterReviewDespiteOpenThread 验证收口：分层书经 reopen
// 返工后，即便 compass 仍有未收束长线（返工可能扰动），排空后也必须重新章审，章审通过后按"结构完整"重新完结——
// 不卡在 writing，杜绝终卷末越界续写死循环（§6.5 / known_outline_exhaustion 家族）。
// 反证：若 reopen 路径仍把正向长篇长线收束作为返工后重新完结的硬条件，本例 open thread 会卡住。
func TestCommitChapterLayeredReopenRecompletesDespiteOpenThread(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	// 单卷单弧两章，全部展开
	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "卷一", "theme": "主题",
			"arcs": []map[string]any{{
				"index": 1, "title": "弧一", "goal": "目标",
				"chapters": []map[string]any{
					{"title": "首章", "core_event": "起", "hook": "续"},
					{"title": "次章", "core_event": "承", "hook": "终"},
				},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}

	// 两章写完落盘并完结
	ch2 := "第二章原始正文，模拟已提交终稿。"
	for ch, body := range map[int]string{1: "第一章正文。", 2: ch2} {
		if err := s.Drafts.SaveDraft(ch, body); err != nil {
			t.Fatalf("SaveDraft %d: %v", ch, err)
		}
		if err := s.Drafts.SaveFinalChapter(ch, body); err != nil {
			t.Fatalf("SaveFinalChapter %d: %v", ch, err)
		}
		if err := s.Progress.MarkChapterComplete(ch, len([]rune(body)), "", ""); err != nil {
			t.Fatalf("MarkChapterComplete %d: %v", ch, err)
		}
	}
	for ch := 1; ch <= 2; ch++ {
		if err := s.World.SaveReview(domain.ReviewEntry{Chapter: ch, Scope: "chapter", Verdict: "accept"}); err != nil {
			t.Fatalf("SaveReview ch%d: %v", ch, err)
		}
	}
	if err := s.Progress.MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}

	// 模拟"返工扰动了长线"：compass 仍有未收束的 open thread
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "主角归乡", OpenThreads: []string{"宿敌未除"}}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}

	// reopen 第 2 章 → 返工提交（草稿需与终稿不同才放行）
	if err := s.Progress.Reopen([]int{2}, "返工"); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if err := s.Drafts.SaveDraft(2, ch2+"\n\n配角问：“你真决定这样收尾？”主角迟疑了一下：“先改完这一处，我再签字。”"); err != nil {
		t.Fatalf("SaveDraft reworked: %v", err)
	}
	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter": 2, "summary": "返工摘要", "characters": []string{"主角", "配角"}, "key_events": []string{"清理"},
		"character_stage_records": testCharacterStageRecords("主角", "配角"),
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute rework commit: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if bc, _ := out["book_complete"].(bool); bc {
		t.Error("返工提交后尚未重新章审，不应直接完结")
	}
	p, _ := s.Progress.Load()
	if p.Phase == domain.PhaseComplete {
		t.Errorf("phase = %s, want writing before renewed review", p.Phase)
	}
	writeCleanMechanicalGate(t, s, 2)
	reviewArgs, _ := json.Marshal(map[string]any{
		"chapter":         2,
		"scope":           "chapter",
		"dimensions":      acceptDimensions(),
		"issues":          []map[string]any{},
		"contract_status": "met",
		"contract_notes":  "返工后章级审阅通过。",
		"verdict":         "accept",
		"summary":         "返工后可收尾。",
	})
	if _, err := NewSaveReviewTool(s).Execute(context.Background(), reviewArgs); err != nil {
		t.Fatalf("SaveReview after rework: %v", err)
	}
	p, _ = s.Progress.Load()
	if p.Phase != domain.PhaseComplete {
		r1, _ := s.World.LoadReview(1)
		r2, _ := s.World.LoadReview(2)
		voice, _ := s.AIVoice.LoadRedFlags(2)
		t.Errorf("phase = %s, want complete after renewed review; pending=%v r1=%+v r2=%+v voice=%+v", p.Phase, p.PendingRewrites, r1, r2, voice)
	}
	if p.ReopenedFromComplete {
		t.Error("重新完结后 ReopenedFromComplete 应被清除")
	}
}

func TestCommitChapterRejectsPolishWithoutDraftChange(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	// 模拟第 2 章已正常完成：drafts 与 chapters 内容相同。
	original := "第二章原始正文内容，用于模拟已提交终稿。"
	if err := s.Drafts.SaveDraft(2, original); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, original); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(original)), "mystery", "quest"); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	// 进入打磨队列：Flow=Polishing, PendingRewrites=[2]
	if err := s.Progress.SetPendingRewrites([]int{2}, "测试打磨"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}

	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"summary":    "假装打磨了",
		"characters": []string{"主角"},
		"key_events": []string{"无改动"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected commit to be rejected when drafts equals final content")
	}

	// 再写一版不同的草稿 → 应该通过
	polished := original + "\n\n打磨后新增段落。"
	if err := s.Drafts.SaveDraft(2, polished); err != nil {
		t.Fatalf("SaveDraft (polished): %v", err)
	}
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute after real polish: %v", err)
	}
}

// TestCommitChapterLayeredRejectsOutOfRangeChapter 验证分层模式下，
// 章号越出 layered_outline 的 commit 必须硬失败，而不是 slog.Warn 放行。
// 这是阻止"裁定误判后 writer 一路裸跑"的物理刹车（《凡骨》ch204..347 案例）。
func TestCommitChapterLayeredRejectsOutOfRangeChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	// 建一份 layered_outline，只有 1 卷 1 弧 1 章
	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "卷一", "theme": "主题",
			"arcs": []map[string]any{{
				"index": 1, "title": "弧一", "goal": "目标",
				"chapters": []map[string]any{
					{"title": "首章", "core_event": "起", "hook": "续"},
				},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	// 越界章节 2 的 commit 必须硬失败
	if err := s.Drafts.SaveDraft(2, "越界章节正文，必须被拦下。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"summary":    "越界章节",
		"characters": []string{"主角"},
		"key_events": []string{"不该被允许"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected commit to fail when chapter out of layered outline range")
	}

	// 章节文件不应落盘、Progress 不应推进
	if _, statErr := os.Stat(dir + "/chapters/02.md"); !os.IsNotExist(statErr) {
		t.Fatalf("chapter 2 should not be persisted, stat err=%v", statErr)
	}
	progress, _ := s.Progress.Load()
	if len(progress.CompletedChapters) != 0 {
		t.Fatalf("CompletedChapters should stay empty, got %v", progress.CompletedChapters)
	}
}

// TestCommitChapterLayeredRequiresReviewBeforeCompleteBook 验证分层模式：
// 最后一章 commit 不直接推 Phase=Complete；必须先章级审阅通过，再由 complete_book 收尾。
func TestCommitChapterLayeredRequiresReviewBeforeCompleteBook(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	// 单卷单弧两章，全部展开（无骨架弧）
	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "卷一", "theme": "主题",
			"arcs": []map[string]any{{
				"index": 1, "title": "弧一", "goal": "目标",
				"chapters": []map[string]any{
					{"title": "首章", "core_event": "起", "hook": "续"},
					{"title": "次章", "core_event": "承", "hook": "终"},
				},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}
	// 指南针长线已收束（OpenThreads 空）
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "主角归乡"}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	tool := NewCommitChapterTool(s)
	commit := func(ch int) map[string]any {
		if err := s.Drafts.SaveDraft(ch, fmt.Sprintf("第 %d 章正文内容，用于测试确定性完结。", ch)); err != nil {
			t.Fatalf("SaveDraft %d: %v", ch, err)
		}
		args, _ := json.Marshal(map[string]any{
			"chapter": ch, "summary": "摘要", "characters": []string{"主角", "配角"}, "key_events": []string{"事件"},
			"character_stage_records": testCharacterStageRecords("主角", "配角"),
		})
		raw, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("Execute ch%d: %v", ch, err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("Unmarshal ch%d: %v", ch, err)
		}
		return out
	}

	// 第 1 章：未写完，不应完结
	if bc, _ := commit(1)["book_complete"].(bool); bc {
		t.Fatal("写完第 1 章不应触发完结")
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseComplete {
		t.Fatal("写完第 1 章 phase 不应为 complete")
	}

	// 第 2 章（最后一章）：提交后仍需章级审阅，不应自动完结
	if bc, _ := commit(2)["book_complete"].(bool); bc {
		t.Fatal("写完最后一章但未审阅，不应触发完结")
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseComplete {
		t.Fatalf("expected phase to stay writing before review, got %s", p.Phase)
	}
	for ch := 1; ch <= 2; ch++ {
		if err := s.World.SaveReview(domain.ReviewEntry{Chapter: ch, Scope: "chapter", Verdict: "accept"}); err != nil {
			t.Fatalf("SaveReview ch%d: %v", ch, err)
		}
	}
	completeArgs, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
	})
	res, err := foundation.Execute(context.Background(), completeArgs)
	if err != nil {
		t.Fatalf("complete_book after reviews: %v", err)
	}
	var completeOut map[string]any
	_ = json.Unmarshal(res, &completeOut)
	if completeOut["book_complete"] != true {
		t.Fatalf("expected complete_book to complete after reviews, got %+v", completeOut)
	}
	if p, _ := s.Progress.Load(); p.Phase != domain.PhaseComplete {
		t.Fatalf("expected phase=complete after complete_book, got %s", p.Phase)
	}
}

// TestCommitChapterLayeredNoAutoCompleteWithOpenThreads 验证保守性：仍有活跃长线时
// 即使章节写满也不自动完结，把"是否继续"的裁定权留给架构师。
func TestCommitChapterLayeredNoAutoCompleteWithOpenThreads(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "卷一", "theme": "主题",
			"arcs": []map[string]any{{
				"index": 1, "title": "弧一", "goal": "目标",
				"chapters": []map[string]any{{"title": "首章", "core_event": "起", "hook": "续"}},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}
	// 仍有未收束的活跃长线
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "主角归乡", OpenThreads: []string{"宿敌未除"}}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	if err := s.Drafts.SaveDraft(1, "唯一一章的正文，但长线未收束。"); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	tool := NewCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "summary": "摘要", "characters": []string{"主角", "配角"}, "key_events": []string{"事件"},
		"character_stage_records": testCharacterStageRecords("主角", "配角"),
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseComplete {
		t.Fatal("活跃长线未收束时不应自动完结")
	}
}
