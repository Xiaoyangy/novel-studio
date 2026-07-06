package imp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

// validHookTypes / validStrands 与 commit_chapter schema 保持一致。
var (
	validHookTypes = map[string]bool{"crisis": true, "mystery": true, "desire": true, "emotion": true, "choice": true}
	validStrands   = map[string]bool{"quest": true, "fire": true, "constellation": true}
)

// ChapterAnalysis 是单章反推的结构化产物，字段直接对齐 commit_chapter 入参。
type ChapterAnalysis struct {
	Summary             string
	Characters          []string
	KeyEvents           []string
	TimelineEvents      []domain.TimelineEvent
	ForeshadowUpdates   []domain.ForeshadowUpdate
	RelationshipChanges []domain.RelationshipEntry
	StateChanges        []domain.StateChange
	HookType            string
	DominantStrand      string
}

// AnalyzeChapter 用一次 LLM 调用，从单章正文反推 commit_chapter 所需事实。
// hooksContext 是已知伏笔池的快照（可空），用于让 LLM 复用既有 ID。
func AnalyzeChapter(
	ctx context.Context,
	llm LLMChat,
	systemPrompt string,
	chapter int,
	chapterTitle, chapterContent string,
	premise, charactersBlock string,
	activeHooks []domain.ForeshadowEntry,
) (*ChapterAnalysis, error) {
	if llm == nil {
		return nil, fmt.Errorf("llm is nil")
	}
	if strings.TrimSpace(chapterContent) == "" {
		return nil, fmt.Errorf("chapter %d: empty content", chapter)
	}

	user := buildAnalyzerUserPrompt(chapter, chapterTitle, chapterContent, premise, charactersBlock, activeHooks)
	resp, err := llm.Generate(ctx, []agentcore.Message{
		agentcore.SystemMsg(systemPrompt),
		agentcore.UserMsg(user),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("llm generate ch%d: %w", chapter, err)
	}
	if resp == nil {
		return nil, fmt.Errorf("ch%d: nil response", chapter)
	}
	return parseAnalyzerOutput(resp.Message.TextContent())
}

func buildAnalyzerUserPrompt(
	chapter int,
	title, content, premise, charactersBlock string,
	hooks []domain.ForeshadowEntry,
) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "请分析第 %d 章正文，输出 9 个 === TAG === 段。\n\n", chapter)
	if title != "" {
		fmt.Fprintf(&sb, "章节标题：%s\n\n", title)
	}

	if strings.TrimSpace(premise) != "" {
		sb.WriteString("## 故事前提（参考）\n\n")
		sb.WriteString(premise)
		sb.WriteString("\n\n")
	}
	if strings.TrimSpace(charactersBlock) != "" {
		sb.WriteString("## 已知角色（参考）\n\n")
		sb.WriteString(charactersBlock)
		sb.WriteString("\n\n")
	}

	if len(hooks) > 0 {
		sb.WriteString("## 已知伏笔池（请复用 ID，不要新造）\n\n")
		for _, h := range hooks {
			fmt.Fprintf(&sb, "- `%s` [%s]：%s（埋设于第 %d 章）\n",
				h.ID, h.Status, h.Description, h.PlantedAt)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 本章正文\n\n")
	sb.WriteString(content)
	sb.WriteString("\n")
	return sb.String()
}

func parseAnalyzerOutput(text string) (*ChapterAnalysis, error) {
	env := parseTaggedEnvelope(text)
	if env == nil {
		return nil, fmt.Errorf("no === TAG === envelope in analyzer output")
	}
	if err := requireTags(env, "SUMMARY", "CHARACTERS", "KEY_EVENTS", "HOOK_TYPE", "DOMINANT_STRAND"); err != nil {
		return nil, err
	}

	a := &ChapterAnalysis{
		Summary:        strings.TrimSpace(env["SUMMARY"]),
		HookType:       strings.ToLower(strings.TrimSpace(env["HOOK_TYPE"])),
		DominantStrand: strings.ToLower(strings.TrimSpace(env["DOMINANT_STRAND"])),
	}
	if a.Summary == "" {
		return nil, fmt.Errorf("summary is empty")
	}
	if !validHookTypes[a.HookType] {
		return nil, fmt.Errorf("invalid hook_type %q (want crisis/mystery/desire/emotion/choice)", a.HookType)
	}
	if !validStrands[a.DominantStrand] {
		return nil, fmt.Errorf("invalid dominant_strand %q (want quest/fire/constellation)", a.DominantStrand)
	}

	if err := decodeJSON("characters", env["CHARACTERS"], &a.Characters); err != nil {
		return nil, err
	}
	if len(a.Characters) == 0 {
		return nil, fmt.Errorf("characters array is empty")
	}
	if err := decodeJSON("key_events", env["KEY_EVENTS"], &a.KeyEvents); err != nil {
		return nil, err
	}
	if len(a.KeyEvents) == 0 {
		return nil, fmt.Errorf("key_events array is empty")
	}

	if err := decodeOptionalArray("timeline", env["TIMELINE"], &a.TimelineEvents); err != nil {
		return nil, err
	}
	if err := decodeOptionalArray("foreshadow", env["FORESHADOW"], &a.ForeshadowUpdates); err != nil {
		return nil, err
	}
	if err := decodeOptionalArray("relationships", env["RELATIONSHIPS"], &a.RelationshipChanges); err != nil {
		return nil, err
	}
	if err := decodeOptionalArray("state_changes", env["STATE_CHANGES"], &a.StateChanges); err != nil {
		return nil, err
	}
	for i, fu := range a.ForeshadowUpdates {
		if fu.Action == "plant" && strings.TrimSpace(fu.Description) == "" {
			return nil, fmt.Errorf("foreshadow[%d] action=plant requires description (id=%s)", i, fu.ID)
		}
	}
	return a, nil
}

// decodeOptionalArray 允许标签缺失或为空字符串；只在非空时解析。
func decodeOptionalArray(label, body string, out any) error {
	body = stripFences(body)
	if body == "" || body == "[]" {
		return nil
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		return fmt.Errorf("parse %s JSON: %w", label, err)
	}
	return nil
}

// PersistChapter 把分析结果落盘：先写章节草稿，再调 commit_chapter 执行原子三件套。
// 已完成章节会被 commit_chapter 自身的幂等检查跳过，仍返回 nil 让循环继续。
func PersistChapter(
	ctx context.Context,
	st *store.Store,
	commitTool *tools.CommitChapterTool,
	chapter int,
	title, content string,
	a *ChapterAnalysis,
) error {
	if a == nil {
		return fmt.Errorf("nil analysis")
	}
	if commitTool == nil {
		return fmt.Errorf("nil commit tool")
	}

	// 1. 落盘草稿（commit_chapter 从 drafts/{ch}.draft.md 读正文）
	if err := st.Drafts.SaveDraft(chapter, content); err != nil {
		return fmt.Errorf("save draft ch%d: %w", chapter, err)
	}

	// 2. 标记进入写作中（ValidateChapterWork 在 FlowWriting 下不阻塞，但 progress 需要这一步保持一致）
	if err := st.Progress.StartChapter(chapter); err != nil {
		return fmt.Errorf("start chapter ch%d: %w", chapter, err)
	}

	// 3. 构造 commit_chapter 入参（注入 chapter title 仅记录用，commit_chapter 不读 title）
	args := map[string]any{
		"chapter":                 chapter,
		"summary":                 a.Summary,
		"characters":              a.Characters,
		"key_events":              a.KeyEvents,
		"character_stage_records": buildImportCharacterStageRecords(chapter, title, a),
		"hook_type":               a.HookType,
		"dominant_strand":         a.DominantStrand,
	}
	if len(a.TimelineEvents) > 0 {
		args["timeline_events"] = a.TimelineEvents
	}
	if len(a.ForeshadowUpdates) > 0 {
		args["foreshadow_updates"] = a.ForeshadowUpdates
	}
	if len(a.RelationshipChanges) > 0 {
		args["relationship_changes"] = a.RelationshipChanges
	}
	if len(a.StateChanges) > 0 {
		args["state_changes"] = a.StateChanges
	}
	_ = title

	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("marshal commit args ch%d: %w", chapter, err)
	}
	if _, err := commitTool.Execute(ctx, raw); err != nil {
		return fmt.Errorf("commit ch%d: %w", chapter, err)
	}
	return nil
}

func buildImportCharacterStageRecords(chapter int, title string, a *ChapterAnalysis) []domain.CharacterStageRecord {
	if a == nil {
		return nil
	}
	characters := trimNonEmpty(a.Characters)
	if len(characters) == 0 {
		characters = []string{"主角"}
	}
	protagonist := characters[0]
	sideCharacters := characters[1:]
	if len(sideCharacters) == 0 {
		sideCharacters = []string{"本章场外压力"}
	}
	timeHint := importTimeHint(a.TimelineEvents)
	if timeHint == "" {
		timeHint = fmt.Sprintf("第%d章期间", chapter)
	}
	locationHint := importLocationHint(a.StateChanges, protagonist)
	if locationHint == "" {
		locationHint = importTitleLocation(title)
	}
	if locationHint == "" {
		locationHint = "本章主场景"
	}
	eventHint := strings.Join(a.KeyEvents, "；")
	if eventHint == "" {
		eventHint = a.Summary
	}

	records := []domain.CharacterStageRecord{{
		Character:           protagonist,
		Time:                timeHint,
		Location:            locationHint,
		Status:              "存活；导入章节未提供死亡确认",
		Environment:         importNonEmpty(a.Summary, "导入章节主线压力"),
		CurrentAction:       importNonEmpty(eventHint, "推进本章核心事件"),
		Pressure:            importNonEmpty(firstKeyEvent(a.KeyEvents), "必须处理本章关键冲突"),
		Decision:            "按导入正文中的行为推进主线，具体动机以后续章节台账校正",
		MistakeOrMisbelief:  "导入反推未提供明确误判，后续续写前需从正文补证",
		KnowledgeBoundary:   "只知道导入章节正文已经呈现的信息，不预知后续章节",
		VisibleInChapter:    true,
		Evidence:            importNonEmpty(title, fmt.Sprintf("导入第%d章", chapter)),
		Transport:           "按导入正文场景移动",
		TravelTime:          "导入反推未给出精确距离；后续续写需按世界地图补足",
		MeetingConstraint:   "不能凭空获知场外角色经历，需通过正文证据或后续台账传回",
		PersonalityDelta:    "受本章事件影响，后续需在 character_continuity 中继续细化",
		DeathState:          "存活；导入章节未提供死亡确认",
		ProtagonistNotice:   "主角已亲历或在本章正文中直接获知主线信息",
		TimelineConsistency: "来自导入章节事实反推，与章节正文同步",
		NextPotential:       "后续续写前按本章摘要和关键事件继续校正",
		Tags:                []string{"imported", "protagonist"},
	}}
	for _, name := range sideCharacters {
		records = append(records, domain.CharacterStageRecord{
			Character:           name,
			Time:                timeHint,
			Location:            importSideLocation(a.TimelineEvents, name, locationHint),
			Status:              "存活/状态未确认；导入章节未提供死亡确认",
			Environment:         importNonEmpty(a.Summary, "导入章节支线压力"),
			CurrentAction:       importSideAction(a.TimelineEvents, name, eventHint),
			Pressure:            "被本章事件牵动，但不默认围绕主角待命",
			Decision:            "按自身信息边界参与或回避本章事件",
			MistakeOrMisbelief:  "导入反推未提供明确误判，后续出场前需补证",
			KnowledgeBoundary:   "只知道自己在导入正文中可见/可参与的信息，不知道主角内心和后续真相",
			VisibleInChapter:    name != "本章场外压力",
			Evidence:            importNonEmpty(title, fmt.Sprintf("导入第%d章", chapter)),
			Transport:           "按导入正文场景移动；若未展示移动则视为原地",
			TravelTime:          "导入反推未给出精确距离；后续必须按世界地图或现实耗时补足",
			MeetingConstraint:   "不能随主角随叫随到；再次见面需正文证据、交通时间或场景重合",
			PersonalityDelta:    "受本章事件影响，后续需在 character_continuity 中继续细化",
			DeathState:          "存活/未确认；若后续死亡必须在台账中更新并安排传回主角",
			ProtagonistNotice:   "通过本章正文可见行动、对话、物件证据或后续回忆传回主角",
			TimelineConsistency: "与导入章节同一时间段并行，禁止后续无成本突现",
			NextPotential:       "后续续写前按本章摘要和关键事件继续校正",
			Tags:                []string{"imported", "side_character"},
		})
	}
	return records
}

func trimNonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if v := strings.TrimSpace(s); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func importTimeHint(events []domain.TimelineEvent) string {
	for _, ev := range events {
		if strings.TrimSpace(ev.Time) != "" {
			return strings.TrimSpace(ev.Time)
		}
	}
	return ""
}

func importLocationHint(changes []domain.StateChange, protagonist string) string {
	for _, ch := range changes {
		if strings.TrimSpace(ch.Entity) != protagonist || strings.TrimSpace(ch.Field) != "location" {
			continue
		}
		if strings.TrimSpace(ch.NewValue) != "" {
			return strings.TrimSpace(ch.NewValue)
		}
	}
	return ""
}

func importTitleLocation(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	return title + "相关场景"
}

func firstKeyEvent(events []string) string {
	for _, ev := range events {
		if strings.TrimSpace(ev) != "" {
			return strings.TrimSpace(ev)
		}
	}
	return ""
}

func importNonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func importSideLocation(events []domain.TimelineEvent, character, fallback string) string {
	for _, ev := range events {
		if !containsString(ev.Characters, character) {
			continue
		}
		if strings.TrimSpace(ev.Event) != "" {
			return strings.TrimSpace(ev.Event) + "相关场景"
		}
	}
	return fallback
}

func importSideAction(events []domain.TimelineEvent, character, fallback string) string {
	for _, ev := range events {
		if !containsString(ev.Characters, character) {
			continue
		}
		if strings.TrimSpace(ev.Event) != "" {
			return strings.TrimSpace(ev.Event)
		}
	}
	return fallback
}

func containsString(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, v := range values {
		if strings.TrimSpace(v) == want {
			return true
		}
	}
	return false
}
