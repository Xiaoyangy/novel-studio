package main

import (
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestMatchPipelineSealedRenderActualDeltaAcceptsOnlyVerifiedConvergenceOverlay(t *testing.T) {
	fixture := newPipelineSealedActualTestFixture(t)
	replacement := fixture.Bundle.ChapterPlan
	replacement.Notes = strings.TrimSpace(replacement.Notes) + "\nconvergence successor scene allocation"
	if err := fixture.Store.Drafts.SaveChapterPlan(replacement); err != nil {
		t.Fatal(err)
	}
	sealedDigest, err := domain.ComputeChapterPlanV2Digest(fixture.Bundle.ChapterPlan)
	if err != nil {
		t.Fatal(err)
	}
	replacementDigest, err := domain.ComputeChapterPlanV2Digest(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if replacementDigest == sealedDigest {
		t.Fatal("fixture replacement did not create a new semantic plan")
	}
	digest := func(ch string) string { return "sha256:" + strings.Repeat(ch, 64) }
	receipt := domain.SealedConvergenceReplanReceipt{
		Version:      domain.SealedConvergenceReplanReceiptVersion,
		GenerationID: fixture.Bundle.GenerationID, Chapter: fixture.Bundle.Chapter,
		BundleDigest: fixture.Bundle.BundleDigest, PromotionReceiptDigest: digest("1"),
		SealedPlanSemanticDigest:     sealedDigest,
		PreviousPlanCheckpointDigest: digest("2"), PreviousPlanCheckpointSeq: 10,
		ExhaustedCandidateID: "render-ch0001-exhausted", ExhaustedLedgerSHA256: digest("3"),
		FailedBodySHA256: []string{strings.Repeat("a", 64), strings.Repeat("b", 64)},
		FailureCount:     2, FailureLimit: 2,
		ReplacementPlanSemanticDigest:   replacementDigest,
		ReplacementPlanCheckpointDigest: digest("4"), ReplacementPlanCheckpointSeq: 12,
		ReplacementRenderContextSHA256: digest("5"),
		StateContractDigest: pipelineSealedConvergenceStateContractDigest(
			replacement, fixture.Bundle.ChapterWorldSimulation, fixture.Bundle.ProjectedDelta,
		),
		FeedbackDigest: digest("6"), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	receipt.ReceiptDigest, err = domain.ComputeSealedConvergenceReplanReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	binding := &pipelineSealedRenderBinding{
		Bundle:                   fixture.Bundle,
		Promotion:                domain.PromotionReceiptV2{ReceiptDigest: receipt.PromotionReceiptDigest},
		ConvergenceReplanReceipt: &receipt,
	}
	got, err := matchPipelineSealedRenderActualDelta(
		fixture.Store, &fixture.Bundle, &fixture.Candidate, fixture.Body, binding,
	)
	if err != nil || !got.ProjectionMatch {
		t.Fatalf("verified convergence overlay was rejected: match=%+v err=%v", got, err)
	}

	for _, tc := range []struct {
		name    string
		binding *pipelineSealedRenderBinding
	}{
		{name: "ordinary mismatch", binding: nil},
		{name: "nil receipt", binding: &pipelineSealedRenderBinding{Bundle: fixture.Bundle}},
		{name: "tampered receipt", binding: func() *pipelineSealedRenderBinding {
			copyBinding := *binding
			copyReceipt := receipt
			copyReceipt.StateContractDigest = digest("9")
			copyBinding.ConvergenceReplanReceipt = &copyReceipt
			return &copyBinding
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bindings []*pipelineSealedRenderBinding
			if tc.binding != nil {
				bindings = append(bindings, tc.binding)
			}
			got, err := matchPipelineSealedRenderActualDelta(
				fixture.Store, &fixture.Bundle, &fixture.Candidate, fixture.Body, bindings...,
			)
			if err != nil {
				t.Fatal(err)
			}
			if got.ProjectionMatch || !pipelineSealedActualTestContains(got.MismatchReasons, "no valid convergence successor binding") {
				t.Fatalf("unverified/tampered overlay was accepted: %+v", got)
			}
		})
	}
}

type pipelineSealedActualTestFixture struct {
	Store     *store.Store
	Bundle    domain.ProjectedChapterBundle
	Candidate domain.ChapterWorldDelta
	Body      string
}

func TestLocatePipelineSealedOrderedNameConsequenceUsesThreeIndependentSlots(t *testing.T) {
	const contract = "约正文40%且不晚于中点，贺铎清楚叫出程野姓名并改变策略；叫名之后另呈现一个独立、可见的新后果。"
	filler := func(marker string, count int) []string {
		out := make([]string, 0, count)
		for index := 0; index < count; index++ {
			out = append(out, marker+strings.Repeat("普通叙事", 24))
		}
		return out
	}
	name := []string{
		"直播画面外，贺铎开了口。",
		"程野。",
		"姓名落下后，原声里空了很久。",
	}
	strategy := "画面中的控制方式变了，约束物被收短，通往外侧的可见空隙随之封住。"
	consequence := "许知遥顺着收紧侧过身，手肘撞到餐碗，瓷碗歪斜着滑出去并响了一次。"
	body := func(before, after int, orderedEvents []string) string {
		paragraphs := append([]string(nil), filler("前", before)...)
		paragraphs = append(paragraphs, orderedEvents...)
		paragraphs = append(paragraphs, filler("后", after)...)
		return strings.Join(paragraphs, "\n\n")
	}

	tests := []struct {
		name   string
		body   string
		locate bool
	}{
		{
			name:   "ordered independent slots",
			body:   body(4, 5, append(append(append([]string{}, name...), strategy), consequence)),
			locate: true,
		},
		{
			name:   "name overlap cannot impersonate strategy and consequence",
			body:   body(4, 5, name),
			locate: false,
		},
		{
			name:   "strategy without independent consequence",
			body:   body(4, 5, append(append([]string{}, name...), strategy)),
			locate: false,
		},
		{
			name:   "pre-name changes do not satisfy post-name slots",
			body:   body(3, 5, append([]string{strategy, consequence}, name...)),
			locate: false,
		},
		{
			name:   "name reveal after midpoint violates timing",
			body:   body(8, 2, append(append(append([]string{}, name...), strategy), consequence)),
			locate: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locator := locatePipelineSealedBodyContract(tt.body, contract, nil)
			if (locator != "") != tt.locate {
				t.Fatalf("locator=%q locate=%v", locator, tt.locate)
			}
		})
	}
}

func TestLocatePipelineSealedPublicStigmaRequiresTopologyAndNamedAccusation(t *testing.T) {
	const contract = "公开舆论倒转，程野重新背负泄密者污名。"
	tests := []struct {
		name   string
		body   string
		locate bool
	}{
		{
			name: "comments propagate a concrete accusation against the subject",
			body: `评论区先空了半秒，随后骤然加速。

“果然是她。”

“本人都指认了。”

转发提示接连浮上来，旧视频的播放数字也跟着翻倍。有人把程野的名字和“持有原片”并排做成标题，头像与旧照很快被重新贴回评论区。`,
			locate: true,
		},
		{
			name: "viral traffic without accusation is insufficient",
			body: `评论区骤然加速，转发提示接连浮上来，播放数字很快翻倍。

有人把程野的新节目做成标题，观众都在讨论她的拍摄手法。`,
			locate: false,
		},
		{
			name:   "private accusation without public propagation is insufficient",
			body:   `门关上后，调查员只对同事说程野有泄密嫌疑。记录留在未公开的内部卷宗里。`,
			locate: false,
		},
		{
			name: "another subject cannot satisfy named stigma",
			body: `评论区骤然加速，转发数字很快翻倍。

有人把林岚的名字和“持有原片”并排做成标题。程野只负责保存页面。`,
			locate: false,
		},
		{
			name: "public exoneration is not stigma",
			body: `评论区骤然加速，澄清帖被接连转发并推上更多页面。

平台证明程野清白，撤回指控，确认她没有嫌疑。`,
			locate: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locator := locatePipelineSealedBodyContract(tt.body, contract, nil)
			if (locator != "") != tt.locate {
				t.Fatalf("locator=%q locate=%v", locator, tt.locate)
			}
			if tt.locate && !strings.Contains(locator, "body:public-stigma:") {
				t.Fatalf("positive match used non-topological locator: %q", locator)
			}
		})
	}
}

func TestLocatePipelineSealedOutcomeStatusUsesBoundedParagraphEvidence(t *testing.T) {
	tests := []struct {
		name     string
		contract string
		body     string
		kind     string
		locate   bool
	}{
		{
			name:     "real C10 front door risk decision spans adjacent paragraphs",
			contract: "正门被明确降为高风险方案",
			body: `“先把它降下来。不是排除，是风险最高。”

姜岚看了她一眼，在部署图上改了标记。那一下没有带来轻松。正门仍在那里，太合理，合理得像贺铎提前替她选好的路。`,
			kind:   pipelineSealedOutcomeStatusRisk,
			locate: true,
		},
		{
			name:     "real C10 named routes remain together",
			contract: "但两条路径仍未排除",
			body: `“都留。”程野说，“上层东侧消防通道，下层卸货坡道消防门。骑手入口能解释前半段，坡道能解释后半段，排风也没把它们分开。”

姜岚没有再追问。她把两条线交给不同的人复核。`,
			kind:   pipelineSealedOutcomeStatusCandidates,
			locate: true,
		},
		{
			name:     "real C10 candidate lines stay pending",
			contract: "警方只完成外围双路部署，两处候选入口继续待核。",
			body: `零点三十四分，姜岚把两条候选线都描深。她下达的安排很短，没有给任何一边冠上“主入口”。

程野在比对表最下方写下：门已开，层位未定。`,
			kind:   pipelineSealedOutcomeStatusCandidates,
			locate: true,
		},
		{
			name:     "front door explicitly cleared as low risk",
			contract: "正门被明确降为高风险方案",
			body:     "部署图仍标着正门。复核完成后，姜岚说它不是高风险方案，已经改为低风险。",
			locate:   false,
		},
		{
			name:     "subject and risk predicate farther than adjacent paragraphs",
			contract: "正门被明确降为高风险方案",
			body: `正门仍在那里，门灯照着台阶。

外围人员继续登记经过车辆，图纸翻到了下一页。

下层坡道风险最高，姜岚先把那一项降下来。`,
			locate: false,
		},
		{
			name:     "later unique entry lock defeats earlier retained candidates",
			contract: "但两条路径仍未排除",
			body: `“都留。”程野指着上层东侧消防通道和下层卸货坡道消防门。

外围核对继续，两个小组暂时没有移动。

新的内侧画面传来后，唯一入口已锁定为下层卸货坡道消防门。`,
			locate: false,
		},
		{
			name:     "both candidates rejected",
			contract: "但两条路径仍未排除",
			body:     "图上并排标着上层东侧消防通道和下层卸货坡道消防门，复核后两条都排除，现场不再保留候选。",
			locate:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locator := locatePipelineSealedBodyContract(tt.body, tt.contract, nil)
			if (locator != "") != tt.locate {
				t.Fatalf("locator=%q locate=%v", locator, tt.locate)
			}
			if tt.locate && !strings.Contains(locator, "body:outcome-status:"+tt.kind) {
				t.Fatalf("positive match used a non-status locator: %q", locator)
			}
		})
	}
}

func TestMatchPipelineSealedActualFactsLocatesRealC10OutcomeStatuses(t *testing.T) {
	after := []string{
		"正门被明确降为高风险方案",
		"但两条路径仍未排除",
	}
	body := `“先把它降下来。不是排除，是风险最高。”

姜岚看了她一眼，在部署图上改了标记。正门仍在那里，太合理，合理得像贺铎提前替她选好的路。

“都留。”程野说，“上层东侧消防通道，下层卸货坡道消防门。骑手入口能解释前半段，坡道能解释后半段。”

零点三十四分，姜岚把两条候选线都描深，没有给任何一边冠上“主入口”。层位未定。`
	for index, outcome := range after {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			mutation := domain.StateMutationV2{
				StableID:  "sealed-test:c10-outcome-" + string(rune('a'+index)),
				Subject:   "chapter",
				Field:     "outcome",
				Operation: "advance",
				After:     outcome,
				Cause:     "C10外围部署",
			}
			projected := domain.ProjectedDelta{
				Version:  domain.ProjectedDeltaV2Version,
				Timeline: []domain.StateMutationV2{mutation},
			}
			facts := []pipelineSealedActualFact{{
				Category: "timeline",
				Subject:  "chapter",
				Field:    "outcome",
				After:    outcome,
				Locator:  "timeline.json#chapter=10,index=" + string(rune('0'+index)),
				Hard:     true,
			}}
			requirements := pipelineSealedActualRequirements{VisibleMutation: map[string]bool{
				mutation.StableID: true,
			}}
			got := matchPipelineSealedActualFacts(projected, facts, body, requirements)
			if !got.ProjectionMatch || len(got.MismatchReasons) != 0 {
				t.Fatalf("real C10 outcome status lacked bounded body locator: %+v", got)
			}
		})
	}
}

func TestLocatePipelineSealedResolvedCandidateRequiresEntityAndIndependentVerification(t *testing.T) {
	const contract = "下层卸货坡道消防门从候选路径变为经连续记录、门禁状态和公共监控共同确认的唯一安全入口"
	positiveFiveParagraphs := `上层东侧与下层坡道此前都在图上，谁也没有先拿到答案。

服务端连续音轨先收进了坡道风声与门轴承重声，前后没有断点。

门禁顺序随即回传，编号落在下层那扇门上。

公共监控里的警示反光与坡道出口同时对上，第三份记录没有指向上层。

姜岚完成复核后，把下层卸货坡道消防门确认为唯一安全入口。`
	tests := []struct {
		name   string
		body   string
		locate bool
	}{
		{
			name:   "five natural paragraphs bind three independent sources",
			body:   positiveFiveParagraphs,
			locate: true,
		},
		{
			name: "three paragraphs bind two independent sources",
			body: `连续音轨里的坡道风声与开门顺序保持完整。

门禁编号和公共监控中的下层反光各自完成复核。

姜岚据此锁定下层卸货坡道消防门为唯一入口。`,
			locate: true,
		},
		{
			name: "operational handoff binds split door id and excludes other candidate",
			body: `“入口纠正。”程野说，“下层卸货坡道，B17消防门。不是上层东侧。”

她指出门禁顺序已经落到B17。

姜岚让所有人停在原地等待独立复核。

公共监控里下层消防门已经打开，上层东侧门依旧闭合。

姜岚按下通话键：“下层B17，按既定处置进入。”

第一名嫌疑人受控后，程野没有追问，因为另一路尚未确认。`,
			locate: true,
		},
		{
			name: "unique label without multiple verification sources",
			body: `有人指着图说答案已经有了。

姜岚把下层卸货坡道消防门叫作唯一安全入口。`,
			locate: false,
		},
		{
			name: "one verification source is insufficient",
			body: `门禁状态落在下层编号上。

姜岚把下层卸货坡道消防门确认为唯一安全入口。`,
			locate: false,
		},
		{
			name: "later return to two pending routes defeats resolution",
			body: positiveFiveParagraphs + `

新的回传打乱了层位判断。

两条候选仍保留，入口重新未定。`,
			locate: false,
		},
		{
			name: "wrong concrete entry cannot satisfy contract",
			body: `连续音轨记录了门轴和坡道风声。

门禁状态与公共监控先后完成复核。

姜岚把上层东侧消防通道确认为唯一安全入口。`,
			locate: false,
		},
		{
			name: "operational order without excluding competing candidate is insufficient",
			body: `程野说：“下层卸货坡道，B17消防门。”

门禁顺序已经落到B17。

公共监控拍到了下层反光。

姜岚按下通话键：“下层B17，按既定处置进入。”`,
			locate: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locator := locatePipelineSealedBodyContract(tt.body, contract, nil)
			if (locator != "") != tt.locate {
				t.Fatalf("locator=%q locate=%v", locator, tt.locate)
			}
			if tt.locate && !strings.Contains(
				locator,
				"body:outcome-status:"+pipelineSealedOutcomeStatusResolvedCandidate,
			) {
				t.Fatalf("positive match used a non-resolved locator: %q", locator)
			}
		})
	}
}

func TestMatchPipelineSealedActualFactsLocatesResolvedC11Candidate(t *testing.T) {
	const after = "下层卸货坡道消防门从候选路径变为经连续记录、门禁状态和公共监控共同确认的唯一安全入口"
	mutation := domain.StateMutationV2{
		StableID:  "sealed-test:c11-resolved-entry",
		Subject:   "chapter",
		Field:     "outcome",
		Operation: "advance",
		After:     after,
		Cause:     "C11内侧开门与外侧独立复核",
	}
	projected := domain.ProjectedDelta{
		Version:  domain.ProjectedDeltaV2Version,
		Timeline: []domain.StateMutationV2{mutation},
	}
	facts := []pipelineSealedActualFact{{
		Category: "timeline",
		Subject:  "chapter",
		Field:    "outcome",
		After:    after,
		Locator:  "timeline.json#chapter=11,index=0",
		Hard:     true,
	}}
	requirements := pipelineSealedActualRequirements{VisibleMutation: map[string]bool{
		mutation.StableID: true,
	}}
	body := `服务端连续记录没有断，坡道风声紧跟在真正开门声后。

门禁顺序落到下层编号，程野把它与骑手路线重新对齐。

公共监控里的下层警示反光随门打开，姜岚完成独立复核。

下层卸货坡道消防门由此锁定为唯一安全入口。`
	got := matchPipelineSealedActualFacts(projected, facts, body, requirements)
	if !got.ProjectionMatch || len(got.MismatchReasons) != 0 {
		t.Fatalf("resolved C11 entry lacked bounded multi-source evidence: %+v", got)
	}
}

func TestMatchPipelineSealedActualFactsLocatesPublicStigmaTimelineAcrossParagraphs(t *testing.T) {
	const after = "公开舆论倒转，程野重新背负泄密者污名。"
	mutation := domain.StateMutationV2{
		StableID:  "sealed-test:public-stigma",
		Subject:   "chapter",
		Field:     "outcome",
		Operation: "advance",
		After:     after,
		Cause:     "公开指认进入直播",
	}
	projected := domain.ProjectedDelta{
		Version:  domain.ProjectedDeltaV2Version,
		Timeline: []domain.StateMutationV2{mutation},
	}
	facts := []pipelineSealedActualFact{{
		Category: "timeline",
		Subject:  "chapter",
		Field:    "outcome",
		After:    after,
		Locator:  "timeline.json#chapter=7,index=1",
		Hard:     true,
	}}
	requirements := pipelineSealedActualRequirements{VisibleMutation: map[string]bool{
		mutation.StableID: true,
	}}
	body := `评论区先空了半秒，随后骤然加速。

“本人都指认了。”

转发提示接连浮上来，旧视频的播放数字也跟着翻倍。有人把程野的名字和“持有原片”并排做成标题。`
	got := matchPipelineSealedActualFacts(projected, facts, body, requirements)
	if !got.ProjectionMatch || len(got.MismatchReasons) != 0 {
		t.Fatalf("public stigma timeline lacked a visible body locator: %+v", got)
	}

	privateOnly := matchPipelineSealedActualFacts(
		projected,
		facts,
		"调查员在关门会议里说程野有泄密嫌疑，记录没有公开。",
		requirements,
	)
	if privateOnly.ProjectionMatch ||
		!pipelineSealedActualTestContains(privateOnly.MismatchReasons, "no locatable semantic body evidence") {
		t.Fatalf("private suspicion satisfied a public stigma timeline: %+v", privateOnly)
	}
}

func TestMatchPipelineSealedRenderActualDeltaRequiresIndependentEvidenceForEveryCategory(t *testing.T) {
	fixture := newPipelineSealedActualTestFixture(t)

	got, err := matchPipelineSealedRenderActualDelta(
		fixture.Store,
		&fixture.Bundle,
		&fixture.Candidate,
		fixture.Body,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete || !got.ProjectionMatch || len(got.MismatchReasons) != 0 {
		t.Fatalf("independently evidenced render did not match: %+v", got)
	}
	projectedDigest, err := domain.ComputeProjectedDeltaV2Digest(fixture.Bundle.ProjectedDelta)
	if err != nil {
		t.Fatal(err)
	}
	actualDigest, err := domain.ComputeProjectedDeltaV2Digest(got.ActualDelta)
	if err != nil {
		t.Fatal(err)
	}
	if actualDigest != projectedDigest {
		t.Fatalf("canonical actual delta digest = %s want %s", actualDigest, projectedDigest)
	}
	evidenced := make(map[string]bool)
	for _, item := range got.Evidence {
		evidenced[item.Category] = true
	}
	for _, category := range []string{
		"timeline",
		"character_state",
		"relationship",
		"resource",
		"knowledge",
		"location",
		"foreshadow",
		"obligation",
		"required_beat",
	} {
		if !evidenced[category] {
			t.Fatalf("successful match has no independently recorded %s evidence: %+v", category, got.Evidence)
		}
	}
}

func TestMatchPipelineSealedRenderActualDeltaFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *pipelineSealedActualTestFixture)
		want   string
	}{
		{
			name: "missing structured location",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Candidate.CharacterDeltas[0].Location = ""
			},
			want: "location[",
		},
		{
			name: "after contradiction",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Candidate.CharacterDeltas[0].Status = "仍然完全不相信这份票据"
			},
			want: "after mismatch",
		},
		{
			name: "before contradiction",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.ProjectedDelta.CharacterState[0].Before = "完全未知"
				fixture.Candidate.WorldDeltas = append(
					fixture.Candidate.WorldDeltas,
					domain.WorldChapterDelta{
						Kind:   "state",
						Entity: "主角.state",
						Change: "已经确信 -> 从猜测转为有限确认",
					},
				)
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "before contradiction",
		},
		{
			name: "unplanned hard state mutation",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Candidate.WorldDeltas = append(
					fixture.Candidate.WorldDeltas,
					domain.WorldChapterDelta{
						Kind:   "state",
						Entity: "主角.death_state",
						Change: "alive -> 突然死亡",
					},
				)
			},
			want: "unplanned hard actual mutation",
		},
		{
			name: "required beat absent from body",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Body = "他进了旧街，只在路口停了一会儿，随后空手离开。"
			},
			want: "hard required beat has no locatable body evidence",
		},
		{
			name: "preserved negative continuity contradicted",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.HardRenderContract.MustPreserve = []string{"摊主不得离开青山县旧街"}
				fixture.Body += "结账后，摊主离开青山县旧街，去了县城另一头。"
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "hard continuity contract was contradicted",
		},
		{
			name: "positive preserved fact explicitly negated",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.HardRenderContract.MustPreserve = []string{"盖章票据归主角持有"}
				fixture.Body += "但那张盖章票据并非归主角持有，他当场还了回去。"
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "hard preserved fact was explicitly negated",
		},
		{
			name: "reveal budget exceeded",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.HardRenderContract.RevealBudget = []domain.RevealBudgetItemV2{{
					FactID: "reveal:test:hidden-owner",
					Action: "limit",
					Limit:  "不揭示后台老板已经决定放行",
				}}
				fixture.Body += "同一刻，后台老板已经决定放行，只等把批条送来。"
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "hard reveal budget was exceeded",
		},
		{
			name: "positive reveal slogan is not mechanically enforceable",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.HardRenderContract.RevealBudget = []domain.RevealBudgetItemV2{{
					FactID: "reveal:test:positive-slogan",
					Action: "limit",
					Limit:  "只允许主角知道票据存在",
				}}
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "hard reveal budget is not mechanically enforceable",
		},
		{
			name: "empty negative reveal probe is not mechanically enforceable",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Bundle.HardRenderContract.RevealBudget = []domain.RevealBudgetItemV2{{
					FactID: "reveal:test:empty-probe",
					Action: "limit",
					Limit:  "不解释",
				}}
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "hard reveal budget is not mechanically enforceable",
		},
		{
			name: "visible resource result contradicted in body",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Body = "午前，主角走到青山县旧街做完交易，拿到盖章票据后却当场交还摊主。"
			},
			want: "no locatable semantic body evidence for visible result",
		},
		{
			name: "visible resource result later returned",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Body += "出了旧街，主角又把盖章票据交还摊主。"
			},
			want: "terminal body transfer contradicts projected result",
		},
		{
			name: "visible resource result later handed to third party",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Body += "出了旧街，主角又把盖章票据交给周舟保管。"
			},
			want: "terminal body transfer contradicts projected result",
		},
		{
			name: "unrelated negation does not hide later resource transfer",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Body += "主角没有犹豫就把盖章票据交给周舟保管。"
			},
			want: "terminal body transfer contradicts projected result",
		},
		{
			name: "projected shadow metadata",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Candidate.Sources = []string{"project-all sealed projection"}
			},
			want: "projected shadow artifact",
		},
		{
			name: "wrong generation metadata",
			mutate: func(_ *testing.T, fixture *pipelineSealedActualTestFixture) {
				fixture.Candidate.GenerationID = "pg2_wrong_generation"
			},
			want: "commit metadata generation mismatch",
		},
		{
			name: "opaque consumed obligation",
			mutate: func(t *testing.T, fixture *pipelineSealedActualTestFixture) {
				obligation := fixture.Bundle.ProjectedDelta.Obligations[0]
				obligation.Operation = "consume"
				obligation.After = "satisfied"
				fixture.Bundle.ProjectedDelta.Obligations = []domain.StateMutationV2{obligation}
				fixture.Bundle.ObligationsCreated = nil
				fixture.Bundle.ObligationsConsumed = []string{obligation.Subject}
				rebindPipelineSealedActualTestBundle(t, &fixture.Bundle)
			},
			want: "only an opaque id is available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPipelineSealedActualTestFixture(t)
			tt.mutate(t, &fixture)
			if err := fixture.Store.SaveChapterWorldDelta(fixture.Candidate); err != nil {
				t.Fatal(err)
			}

			got, err := matchPipelineSealedRenderActualDelta(
				fixture.Store,
				&fixture.Bundle,
				&fixture.Candidate,
				fixture.Body,
			)
			if err != nil {
				t.Fatalf("fail-closed mismatch returned an operational error: %v", err)
			}
			if got.ProjectionMatch {
				t.Fatalf("counterexample was accepted: %+v", got)
			}
			if !pipelineSealedActualTestContains(got.MismatchReasons, tt.want) {
				t.Fatalf("mismatch reasons %q do not contain %q", got.MismatchReasons, tt.want)
			}
		})
	}
}

func TestPipelineSealedResourceTransferAwayScopesNegationToTransfer(t *testing.T) {
	tests := []struct {
		name    string
		segment string
		want    bool
	}{
		{name: "explicitly not returned", segment: "主角没有把盖章票据交还摊主", want: false},
		{name: "refuses handoff", segment: "主角拒绝把盖章票据交给周舟", want: false},
		{name: "returned to projected owner", segment: "摊主把盖章票据交给主角", want: false},
		{name: "unrelated negation then handoff", segment: "主角没有犹豫就把盖章票据交给周舟", want: true},
		{name: "ordinary return", segment: "主角把盖章票据交还摊主", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pipelineSealedResourceTransferAway(tt.segment, "主角"); got != tt.want {
				t.Fatalf("pipelineSealedResourceTransferAway(%q)=%v want %v", tt.segment, got, tt.want)
			}
		})
	}
}

func TestPipelineSealedForbiddenContractProbesSplitCompoundNegativeClauses(t *testing.T) {
	const contract = "不得认定许知遥故意留下暗号、不得替她解释指认意图，也不得因受控处境洗白十八个月前的越界。"
	want := []string{
		"认定许知遥故意留下暗号",
		"替她解释指认意图",
		"因受控处境洗白十八个月前的越界",
	}
	got := pipelineSealedForbiddenContractProbes(contract)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("compound forbidden probes=%q want %q", got, want)
	}

	// A positive constraint before the first prohibition is context, not a
	// forbidden probe; comma-separated list continuations remain forbidden.
	got = pipelineSealedForbiddenContractProbes("七份订单本章只能完成审批，不得下单、出餐、形成订单履约记录。")
	if strings.Join(got, "|") != "下单|出餐|形成订单履约记录" {
		t.Fatalf("prefaced forbidden probes=%q", got)
	}

	got = pipelineSealedForbiddenContractProbes("不得用许知遥受控制、承认责任或参与营救抵销旧日越界，不得当场原谅、复合或提前兑现终章关系结果。")
	want = []string{
		"用许知遥受控制、承认责任或参与营救抵销旧日越界",
		"当场原谅",
		"复合",
		"提前兑现终章关系结果",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("shared-tail forbidden probes=%q want %q", got, want)
	}
}

func TestPipelineSealedPOVBoundaryRequiresSharedForbiddenTail(t *testing.T) {
	const contract = "不得用许知遥受控制、承认责任或参与营救抵销旧日越界，不得当场原谅、复合或提前兑现终章关系结果。"
	bundle := &domain.ProjectedChapterBundle{HardRenderContract: domain.HardRenderContractV2{
		MustNotOccur: []string{contract},
	}}
	for _, body := range []string{
		"许知遥受控是真的。",
		"这段承认也是真的。",
		"许知遥受控是真的，这段承认也是真的；屏幕那端的危险没有替十八个月前的越界结账。",
	} {
		if got := pipelineSealedPOVBoundaryViolations(body, bundle); len(got) != 0 {
			t.Errorf("shared-tail subject without the forbidden predicate was rejected: body=%q violations=%v", body, got)
		}
	}

	for _, body := range []string{
		"程野用许知遥受控制抵销旧日越界。",
		"程野用承认责任抵销旧日越界。",
		"程野用参与营救抵销旧日越界。",
		"程野当场原谅许知遥。",
		"她们复合了。",
	} {
		got := pipelineSealedPOVBoundaryViolations(body, bundle)
		if len(got) == 0 || !pipelineSealedActualTestContains(got, "hard forbidden move appears in body") {
			t.Errorf("affirmative shared-tail forbidden body escaped: body=%q violations=%v", body, got)
		}
	}

	sharedObject := pipelineSealedForbiddenContractProbes("不得把00:40页面时间或观看端画面当作直播实时且控制持续的证明。")
	if strings.Join(sharedObject, "|") != "把00:40页面时间或观看端画面当作直播实时且控制持续的证明" {
		t.Fatalf("shared-object alternative was corrupted: %q", sharedObject)
	}
}

func TestPipelineSealedForbiddenContractProbesPreserveOrderAndStopAtPositiveBoundary(t *testing.T) {
	orderContract := "不得颠倒00:35—00:37内侧开门、00:37—00:38唯一外侧纠正、00:38—00:40警方控制的顺序。"
	if got := pipelineSealedForbiddenContractProbes(orderContract); strings.Join(got, "|") != strings.TrimSuffix(strings.TrimPrefix(orderContract, "不得"), "。") {
		t.Fatalf("shared-order prohibition was split: %q", got)
	}

	transitionContract := "00:40后不得继续进入、追捕、搏斗、控制嫌疑人或纠正入口，只能转入医疗。"
	want := "00:40后继续进入|追捕|搏斗|控制嫌疑人|纠正入口"
	if got := pipelineSealedForbiddenContractProbes(transitionContract); strings.Join(got, "|") != want {
		t.Fatalf("positive boundary inherited forbidden polarity: got=%q want=%q", got, want)
	}

	relationshipContract := "两人不得亲吻、告白或当场复合，关系答案留到三个月后。"
	want = "两人亲吻|告白|当场复合"
	if got := pipelineSealedForbiddenContractProbes(relationshipContract); strings.Join(got, "|") != want {
		t.Fatalf("positive relationship result inherited forbidden polarity: got=%q want=%q", got, want)
	}

	if got := pipelineSealedForbiddenContractProbes("本章不得真正打开任何消防门，不得确认唯一入口。"); strings.Join(got, "|") != "真正打开任何消防门|确认唯一入口" {
		t.Fatalf("chapter scope leaked into forbidden probes: %q", got)
	}

	creativeContract := "不得把创作阶段的章节编排、人物职责盘点或篇幅核算写进故事世界。"
	if got := pipelineSealedForbiddenContractProbes(creativeContract); strings.Join(got, "|") != strings.TrimSuffix(strings.TrimPrefix(creativeContract, "不得"), "。") {
		t.Fatalf("shared creative tail was split: %q", got)
	}
}

func TestPipelineSealedPOVBoundaryRequiresSharedOrderPredicate(t *testing.T) {
	const contract = "不得颠倒00:35—00:37内侧开门、00:37—00:38唯一外侧纠正、00:38—00:40警方控制的顺序。"
	bundle := &domain.ProjectedChapterBundle{HardRenderContract: domain.HardRenderContractV2{
		MustNotOccur: []string{contract},
	}}
	compliant := "00:35到00:37完成内侧开门，00:37到00:38完成唯一外侧纠正，00:38到00:40警方控制现场。"
	if got := pipelineSealedPOVBoundaryViolations(compliant, bundle); len(got) != 0 {
		t.Fatalf("correct ordered timeline was rejected: %v", got)
	}
	violation := "现场记录明确颠倒了00:35到00:37内侧开门、00:37到00:38唯一外侧纠正、00:38到00:40警方控制的顺序。"
	if got := pipelineSealedPOVBoundaryViolations(violation, bundle); len(got) == 0 {
		t.Fatal("explicit order inversion escaped")
	}
}

func TestPipelineSealedPOVBoundaryScopesCompoundForbiddenPolarity(t *testing.T) {
	const contract = "不得认定许知遥故意留下暗号、不得替她解释指认意图，也不得因受控处境洗白十八个月前的越界。"
	bundle := &domain.ProjectedChapterBundle{HardRenderContract: domain.HardRenderContractV2{
		MustNotOccur: []string{contract},
	}}
	for _, body := range []string{
		`“十八个月前，唐梨那次预告发出以后，是程野从南栈西侧客梯接走了我。”

那场争执留下的越界和裂痕，并不会因为此刻有人控制直播而消失。`,
		"越界和裂痕并不会因为控制直播而消失。",
		"程野不能替她解释指认意图。",
		"调查员不得认定许知遥故意留下暗号。",
		"没有任何人因受控处境洗白十八个月前的越界。",
		"这段陈述未因受控处境洗白十八个月前的越界。",
	} {
		if got := pipelineSealedPOVBoundaryViolations(body, bundle); len(got) != 0 {
			t.Errorf("negated body %q was rejected: %v", body, got)
		}
	}

	for _, body := range []string{
		"调查员认定许知遥故意留下暗号。",
		"叙述替她解释指认意图。",
		"文章因受控处境洗白十八个月前的越界。",
	} {
		got := pipelineSealedPOVBoundaryViolations(body, bundle)
		if len(got) == 0 || !pipelineSealedActualTestContains(got, "hard forbidden move appears in body") {
			t.Errorf("affirmative forbidden body %q escaped: %v", body, got)
		}
	}

	legacy := &domain.ProjectedChapterBundle{HardRenderContract: domain.HardRenderContractV2{
		MustNotOccur: []string{"提前解释全书谜底"},
	}}
	if got := pipelineSealedPOVBoundaryViolations("这里提前解释全书谜底。", legacy); len(got) == 0 {
		t.Fatal("legacy declarative forbidden contract escaped")
	}
}

func TestPipelineSealedSafetyMatcherRejectsIncidentalContractOverlap(t *testing.T) {
	tests := []struct {
		name     string
		actual   string
		expected string
		want     bool
	}{
		{
			name:     "temporal fragment cannot impersonate a longer forbidden assertion",
			actual:   "十八个月前",
			expected: "因受控处境洗白十八个月前的越界",
			want:     false,
		},
		{
			name:     "complete concise forbidden assertion remains detectable",
			actual:   "因受控处境洗白十八个月前的越界",
			expected: "因受控处境洗白十八个月前的越界",
			want:     true,
		},
		{
			name:     "delay uncertainty is not an affirmative realtime claim",
			actual:   "观看端却可能有延迟",
			expected: "把00:40页面时间或观看端画面当作直播实时且控制持续的证明",
			want:     false,
		},
		{
			name:     "character names do not imply relationship resolution",
			actual:   "许知遥的直播忽然跳上程野的手机",
			expected: "把许知遥十八个月前越过程野同意的行为洗成误会，不得因受控直播而立即原谅或复合",
			want:     false,
		},
		{
			name:     "one receipt phrase does not negate compound preserved evidence",
			actual:   "那不是反折小票",
			expected: "两双筷子和反折小票必须成为可回看异常",
			want:     false,
		},
		{
			name:     "concise forbidden action remains detectable",
			actual:   "摊主离开青山县旧街",
			expected: "摊主离开青山县旧街",
			want:     true,
		},
		{
			name:     "explicit ownership contradiction remains detectable",
			actual:   "但那张盖章票据并非归主角持有",
			expected: "盖章票据归主角持有",
			want:     true,
		},
		{
			name:     "short paraphrase of a hidden reveal remains detectable",
			actual:   "直播与页面完全同步",
			expected: "直播画面与观看端页面完全同步的结论",
			want:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pipelineSealedSafetyTextSupports(tt.actual, tt.expected); got != tt.want {
				t.Fatalf("pipelineSealedSafetyTextSupports(%q, %q)=%v want %v", tt.actual, tt.expected, got, tt.want)
			}
		})
	}
}

func TestPipelineSealedPOVBoundaryAllowsExplicitUncertaintyAndPreservedEvidence(t *testing.T) {
	body := `位置和实时性同样没有答案。页面虽然写着零点四十分结束，观看端却可能有延迟。
程野没有联系许知遥、进入评论区或追查位置。第二双筷子从开播起就摆在那里，始终没人入镜使用；那不是反折小票。
许知遥从不吃陈醋，但她先前已经碰过醋瓶并完成两敲一刮。她二十八岁，是调查型女主播。
直播声画、平台记录和公共监控都只能互相核验，任何一项都不能单独定位，更不能据此认定犯罪。
程野仍不知道控制者身份和现场位置。姜岚说，那是截止压力，不是实时证明。`
	bundle := &domain.ProjectedChapterBundle{
		HardRenderContract: domain.HardRenderContractV2{
			MustNotOccur: []string{
				"不得把00:40页面时间或观看端画面当作直播实时且控制持续的证明。",
				"不得把许知遥十八个月前越过程野同意的行为洗成误会，不得因受控直播而立即原谅或复合。",
			},
			MustPreserve: []string{
				"程野必须先保存原始链接并连续录屏，再停进公共监控覆盖的安全区报警；不得进入评论区、追踪位置或离开警方安全边界。",
				"姜岚须独立判断并启动平台保全与属地核验；直播声画、平台记录和公共监控只能互证，不能单独定位或定罪。",
				"许知遥必须触碰自己绝不吃的陈醋，并完成碗沿两下敲击加一次刮擦；两双筷子和反折小票必须成为可回看异常。",
				"许知遥为28岁调查型女主播。",
			},
			RevealBudget: []domain.RevealBudgetItemV2{
				{FactID: "sync", Action: "limit", Limit: "不提前给出直播画面与观看端页面完全同步的结论。"},
				{FactID: "handoff", Action: "limit", Limit: "不揭示许知遥所保全材料的合法交接路径。"},
				{FactID: "relationship", Action: "limit", Limit: "不提前给出程野与许知遥延迟复合的结果。"},
			},
		},
		POVPlan: domain.POVPlanV2{Unknowns: []string{
			"许知遥仍受画外监视约束，程野只能看见提词纸边、壶面倒影与打断敲击的手势，尚不知道控制者身份和现场位置。",
			"观看端存在延迟；页面的00:40结束时间构成倒计时，却不能单独证明画面实时且控制仍在持续。",
		}},
	}
	if got := pipelineSealedPOVBoundaryViolations(body, bundle); len(got) != 0 {
		t.Fatalf("compliant uncertainty/evidence prose was rejected: %v", got)
	}
}

func TestClassifyPipelineSealedMutationVisibilityIsFieldAware(t *testing.T) {
	bundle := &domain.ProjectedChapterBundle{
		ChapterWorldSimulation: domain.ChapterWorldSimulation{
			SimulationID: "sim-field-aware",
			CharacterDecisions: []domain.CharacterWorldDecision{
				{Character: "程野", VisibleToPOV: true},
				{Character: "许知遥", VisibleToPOV: true},
				{Character: "远端警员", VisibleToPOV: true},
			},
			ProtagonistProjection: domain.ProtagonistDecisionProjection{ObservableEffects: []string{
				"远端警员已从城南联合处置组回传；远端警员只知道报警记录。",
			}},
		},
		POVPlan: domain.POVPlanV2{
			POVCharacterID: "程野",
			KnowledgeBoundary: []string{
				"程野不知道许知遥位于南栈旧景棚通路。",
			},
			Scenes: []domain.POVSceneV2{{
				Location: "公共停车区", PresentActors: []string{"程野", "现场证人"},
				POVKnows: []string{"远端警员位于城南联合处置组；远端警员只知道报警记录。"},
			}},
		},
		ProjectedDelta: domain.ProjectedDelta{
			Version: domain.ProjectedDeltaV2Version,
			CharacterState: []domain.StateMutationV2{
				{StableID: "state-xu", Subject: "许知遥", Field: "state", Operation: "update", After: "镜头内受控", Cause: "画面可见"},
			},
			Locations: []domain.StateMutationV2{
				{StableID: "loc-pov", Subject: "程野", Field: "location", Operation: "set", After: "公共停车区", Cause: "停车"},
				{StableID: "loc-xu", Subject: "许知遥", Field: "location", Operation: "set", After: "南栈旧景棚通路", Cause: "隐藏控制"},
				{StableID: "loc-witness", Subject: "现场证人", Field: "location", Operation: "set", After: "公共停车区", Cause: "同场"},
				{StableID: "loc-remote", Subject: "远端警员", Field: "location", Operation: "set", After: "城南联合处置组", Cause: "通话回传"},
			},
			Knowledge: []domain.StateMutationV2{
				{StableID: "know-pov", Subject: "程野", Field: "knowledge_boundary", Operation: "set", After: "只知道直播画面", Cause: "限知"},
				{StableID: "know-xu", Subject: "许知遥", Field: "knowledge_boundary", Operation: "set", After: "知道控制者身份", Cause: "被控制"},
				{StableID: "know-remote", Subject: "远端警员", Field: "knowledge_boundary", Operation: "set", After: "只知道报警记录", Cause: "接警"},
			},
		},
	}
	requirements := pipelineSealedActualRequirements{
		VisibleMutation:   map[string]bool{},
		OffscreenEvidence: map[string]string{},
	}
	classifyPipelineSealedMutationVisibility(bundle, &requirements)

	for _, id := range []string{"state-xu", "loc-pov", "loc-witness", "loc-remote", "know-pov", "know-remote"} {
		if !requirements.VisibleMutation[id] {
			t.Errorf("%s should be prose-visible", id)
		}
	}
	for _, id := range []string{"loc-xu", "know-xu"} {
		if requirements.VisibleMutation[id] {
			t.Errorf("%s leaked a field merely because the character is visible", id)
		}
		if !strings.Contains(requirements.OffscreenEvidence[id], "sealed-offscreen-simulation:sim-field-aware") {
			t.Errorf("%s lacks sealed offscreen evidence: %q", id, requirements.OffscreenEvidence[id])
		}
	}
}

func TestMatchPipelineSealedActualFactsLocatesConsumedObligationInBody(t *testing.T) {
	const obligationID = "obl:rule:1:consume-proof"
	projected := domain.ProjectedDelta{
		Version: domain.ProjectedDeltaV2Version,
		Timeline: []domain.StateMutationV2{{
			StableID:  "sealed-test:timeline",
			Subject:   "chapter",
			Field:     "outcome",
			Operation: "advance",
			After:     "柜员完成复核",
			Cause:     "票据被提交",
		}},
		Obligations: []domain.StateMutationV2{{
			StableID:  "sealed-test:obligation",
			Subject:   obligationID,
			Field:     "state",
			Operation: "consume",
			After:     "satisfied",
			Cause:     "本章是密封计划指定的兑现章",
		}},
	}
	facts := []pipelineSealedActualFact{{
		Category: "timeline",
		Subject:  "chapter",
		Field:    "outcome",
		After:    "柜员完成复核",
		Locator:  "chapter_world_delta.world_deltas[0]",
		Hard:     true,
	}}
	requirements := pipelineSealedActualRequirements{
		Obligations: map[string]domain.ObligationV2{
			obligationID: {
				ID:       obligationID,
				Contract: "主角把盖章票据交给柜员复核",
				Hardness: domain.ObligationHardV2,
			},
		},
	}

	got := matchPipelineSealedActualFacts(
		projected,
		facts,
		"排到窗口后，主角把盖章票据交给柜员复核。柜员对照存根，点了点头。",
		requirements,
	)
	if !got.ProjectionMatch || len(got.ObligationsSatisfied) != 1 ||
		got.ObligationsSatisfied[0] != obligationID {
		t.Fatalf("body-locatable hard obligation was not matched: %+v", got)
	}

	missing := matchPipelineSealedActualFacts(
		projected,
		facts,
		"主角在窗口外等到天黑，最后仍旧没有递出手里的票据。",
		requirements,
	)
	if missing.ProjectionMatch ||
		!pipelineSealedActualTestContains(missing.MismatchReasons, "consume has no locatable body evidence") {
		t.Fatalf("unrealized hard obligation was accepted: %+v", missing)
	}
}

func TestMatchPipelineSealedActualFactsRejectsInsufficientIdentitySchema(t *testing.T) {
	projected := domain.ProjectedDelta{
		Version: domain.ProjectedDeltaV2Version,
		Timeline: []domain.StateMutationV2{{
			StableID:  "sealed-test:timeline",
			Subject:   "chapter",
			Field:     "outcome",
			Operation: "advance",
			After:     "交易完成",
			Cause:     "现场交割",
		}},
		Resources: []domain.StateMutationV2{{
			StableID:  "sealed-test:resource",
			Subject:   "主角",
			Field:     "resource",
			Operation: "update",
			After:     "booked",
			Cause:     "交易完成",
		}},
	}
	facts := []pipelineSealedActualFact{
		{
			Category: "timeline",
			Subject:  "chapter",
			Field:    "outcome",
			After:    "交易完成",
			Locator:  "chapter_world_delta.world_deltas[0]",
			Hard:     true,
		},
		{
			Category: "resource",
			Subject:  "主角",
			Object:   "盖章票据",
			Field:    "resource",
			After:    "booked",
			Locator:  "resource_ledger.json#chapter=1,index=0",
			Hard:     true,
		},
	}

	got := matchPipelineSealedActualFacts(
		projected,
		facts,
		"主角完成交易并收好盖章票据。",
		pipelineSealedActualRequirements{},
	)
	if got.ProjectionMatch ||
		!pipelineSealedActualTestContains(got.MismatchReasons, "lacks a stable resource object") {
		t.Fatalf("resource without independently matchable identity was accepted: %+v", got)
	}
}

func newPipelineSealedActualTestFixture(t *testing.T) pipelineSealedActualTestFixture {
	t.Helper()
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	bundle, _, err := buildPipelineProjectedChapterBundle(
		generation,
		outline,
		genesis,
		generation.BaseStateRoot,
		artifacts,
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}

	const obligationID = "obl:rule:1:sealed-test"
	bundle.ProjectedDelta = domain.NormalizeProjectedDeltaV2(domain.ProjectedDelta{
		Version: domain.ProjectedDeltaV2Version,
		Timeline: []domain.StateMutationV2{{
			StableID:  "sealed-test:timeline",
			Subject:   "chapter",
			Field:     "outcome",
			Operation: "advance",
			After:     "完成小额验证",
			Cause:     "主角选择可撤回交易",
		}},
		CharacterState: []domain.StateMutationV2{{
			StableID:  "sealed-test:character",
			Subject:   "主角",
			Field:     "state",
			Operation: "update",
			After:     "从猜测转为有限确认",
			Cause:     "拿到可复核票据",
		}},
		Relationships: []domain.StateMutationV2{{
			StableID:  "sealed-test:relationship",
			Subject:   "主角",
			Object:    "摊主",
			Field:     "relationship",
			Operation: "update",
			After:     "从试探转为初步信任",
			Cause:     "摊主当面写清票据",
		}},
		Resources: []domain.StateMutationV2{{
			StableID:  "sealed-test:resource",
			Subject:   "主角",
			Object:    "盖章票据",
			Field:     "resource",
			Operation: "update",
			After:     "票据归主角持有",
			Cause:     "交易完成并留下凭证",
		}},
		Knowledge: []domain.StateMutationV2{{
			StableID:  "sealed-test:knowledge",
			Subject:   "主角",
			Field:     "knowledge_boundary",
			Operation: "set",
			After:     "只知道自己亲历和收到的票据",
			Cause:     "主角没有接触后台信息",
		}},
		Locations: []domain.StateMutationV2{{
			StableID:  "sealed-test:location",
			Subject:   "主角",
			Field:     "location",
			Operation: "set",
			After:     "青山县旧街",
			Cause:     "主角到旧街完成交易",
		}},
		Foreshadows: []domain.StateMutationV2{{
			StableID:  "sealed-test:foreshadow",
			Subject:   "摊主",
			Object:    "receipt-origin",
			Field:     "evidence_return",
			Operation: "advance",
			After:     "票据来源留下后续核查入口",
			Cause:     "票据来源留下后续核查入口",
		}},
		Obligations: []domain.StateMutationV2{{
			StableID:  "sealed-test:obligation",
			Subject:   obligationID,
			Field:     "state",
			Operation: "create",
			After:     "planned",
			Cause:     "密封计划创建后续规则义务",
		}},
	})
	bundle.ObligationsConsumed = nil
	bundle.ObligationsCreated = []string{obligationID}
	bundle.ObligationsCarried = nil
	rebindPipelineSealedActualTestBundle(t, &bundle)

	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(bundle.ChapterPlan); err != nil {
		t.Fatal(err)
	}
	candidate := domain.ChapterWorldDelta{
		Version:      1,
		Chapter:      bundle.Chapter,
		GenerationID: bundle.GenerationID,
		Summary:      "主角完成小额验证，拿到票据并留下后续核查入口。",
		CharacterDeltas: []domain.CharacterChapterDelta{{
			Character:         "主角",
			Location:          "青山县旧街",
			Status:            "从猜测转为有限确认",
			KnowledgeBoundary: "只知道自己亲历和收到的票据",
			DeathState:        "alive",
		}, {
			Character:  "摊主",
			Status:     "存活",
			DeathState: "alive",
		}},
		WorldDeltas: []domain.WorldChapterDelta{
			{
				Kind:     "timeline",
				Change:   "完成小额验证",
				Evidence: "主角完成小额验证并拿到票据",
			},
			{
				Kind:     "timeline",
				Change:   "摊主逐项写清票据",
				Evidence: "摊主当面写清金额与时间",
			},
			{
				Kind:     "relationship",
				Entity:   "主角|摊主",
				Change:   "从试探转为初步信任",
				Evidence: "摊主当面写清金额与时间",
			},
			{
				Kind:     "resource_booked",
				Entity:   "盖章票据",
				Change:   "票据归主角持有",
				Evidence: "票据盖章后被主角收进内袋",
			},
			{
				Kind:     "foreshadow",
				Entity:   "receipt-origin",
				Change:   "票据来源留下后续核查入口",
				Evidence: "票据角落印着陌生编号",
			},
		},
		Sources: []string{
			"commit_chapter",
			"character_stage_records",
			"timeline/resource/relationship/state deltas",
		},
	}
	if err := st.SaveChapterWorldDelta(candidate); err != nil {
		t.Fatal(err)
	}
	if err := st.ResourceLedger.Save(domain.ResourceLedger{
		Version: 1,
		Claims: []domain.ResourceClaim{{
			ID:       "receipt-1",
			Name:     "盖章票据",
			Owner:    "主角",
			Status:   "票据归主角持有",
			Evidence: "",
			Chapter:  bundle.Chapter,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return pipelineSealedActualTestFixture{
		Store:     st,
		Bundle:    bundle,
		Candidate: candidate,
		Body:      "午前，主角走到青山县旧街，先做了一笔小额验证。摊主当面写清金额与时间，盖章后把票据递了过来。盖章票据归主角持有，他把它收进内袋。票据角落印着陌生编号，给来源留下后续核查入口。后台怎么处理，他没看见，也只认自己亲手拿到的凭证。摊主见他按规矩办事，语气里的试探淡了些。",
	}
}

func rebindPipelineSealedActualTestBundle(
	t *testing.T,
	bundle *domain.ProjectedChapterBundle,
) {
	t.Helper()
	var err error
	bundle.ProjectedDelta = domain.NormalizeProjectedDeltaV2(bundle.ProjectedDelta)
	bundle.ProjectedPostStateRoot, err = domain.DeriveProjectedPostStateRootV2(
		bundle.ProjectedPreStateRoot,
		bundle.ProjectedDelta,
	)
	if err != nil {
		t.Fatal(err)
	}
	canonicalContract := pipelineHardRenderContractV2(
		bundle.ChapterPlan,
		bundle.ChapterWorldSimulation,
		bundle.ProjectedDelta,
	)
	bundle.HardRenderContract.ForeshadowChanges = canonicalContract.ForeshadowChanges
	bundle.HardRenderContract.ResourceChanges = canonicalContract.ResourceChanges
	bundle.HardRenderContract.RelationshipChanges = canonicalContract.RelationshipChanges
	bundle.HardRenderContract.KnowledgeChanges = canonicalContract.KnowledgeChanges
	bundle.RenderContext, err = augmentPipelineProjectAllRenderContext(bundle.RenderContext, *bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RenderContextSHA256, err = domain.ComputePlanningV2JSONDigest(bundle.RenderContext)
	if err != nil {
		t.Fatal(err)
	}
	bundle.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(*bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedChapterBundle(*bundle); err != nil {
		t.Fatalf("invalid sealed actual matcher fixture bundle: %v", err)
	}
}

func pipelineSealedActualTestContains(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
