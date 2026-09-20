package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func longArchitectRefreshFixture(t *testing.T, rebased bool) string {
	t.Helper()
	live := seedZeroInitProject(t)
	st := store.NewStore(live)
	var entries []domain.OutlineEntry
	for chapter := 1; chapter <= 100; chapter++ {
		entries = append(entries, domain.OutlineEntry{Chapter: chapter, Title: fmt.Sprintf("第%d项选择", chapter), CoreEvent: "江烬核对当前收据并承担自己作出的实际选择。", Hook: "尚未完成的责任需要继续履行。", Scenes: []string{"先确认当前实际收到的条款。", "与在场者核对不同选择的后果。", "执行所选行动并保留实际结果。"}})
	}
	publicationArtifactMust(t, st.Outline.SaveOutline(entries))
	compass, err := st.Outline.LoadCompass()
	publicationArtifactMust(t, err)
	compass.EstimatedScale = "1-1卷，100-100章"
	publicationArtifactMust(t, st.Outline.SaveCompass(*compass))
	publicationArtifactMust(t, st.Progress.Init("百章章零作者修订", 100))
	progress, err := st.Progress.Load()
	publicationArtifactMust(t, err)
	progress.Phase = domain.PhaseWriting
	publicationArtifactMust(t, st.Progress.Save(progress))
	if rebased {
		// A real existing epoch forces the ordinary archive + DirectoryPublish
		// path. No forged rebase marker or copied source hash grants authority.
		rebaseAllTestWriteFile(t, live, "meta/pipeline.json", `{"prompt":"原创作合同保持不变"}`)
		publicationArtifactMust(t, pipelineRebaseAllChapters(rebaseAllTestOptions(t, pipelineRebaseRunRoot(live))))
		st = store.NewStore(live)
		publicationArtifactMust(t, st.ValidateRebasedChapterZeroFoundationRefresh())
		publicationArtifactMust(t, tools.RequireChapterZeroFoundationRefreshState(st))
	}
	return live
}

func TestLongArchitectRefreshAllowsVerifiedRebasedChapterZero(t *testing.T) {
	live := longArchitectRefreshFixture(t, true)
	st := store.NewStore(live)
	p, err := st.Progress.Load()
	publicationArtifactMust(t, err)
	if p.TotalChapters != 100 || p.LatestCompleted() != 0 {
		t.Fatalf("fixture did not preserve the 100-chapter canon-zero contract: %+v", p)
	}
	before, err := store.DirectoryContentRoot(live)
	publicationArtifactMust(t, err)
	// This is the actual CLI preflight, reached before invalidating the
	// existing stage graph or dispatching Architect.
	if err := validateExplicitArchitectRefreshState(live, "world_codex"); err != nil {
		t.Fatalf("verified rebased 100-chapter source revision incorrectly rejected: %v", err)
	}
	after, err := store.DirectoryContentRoot(live)
	publicationArtifactMust(t, err)
	if before != after {
		t.Fatal("refresh preflight changed the book")
	}
}

func TestLongArchitectRefreshStillRejectsUnretiredOrAcceptedState(t *testing.T) {
	for _, kind := range []string{"not-rebased", "accepted-chapter", "active-sealed", "zero-init", "damaged-archive"} {
		t.Run(kind, func(t *testing.T) {
			live := longArchitectRefreshFixture(t, kind != "not-rebased")
			st := store.NewStore(live)
			switch kind {
			case "accepted-chapter":
				publicationArtifactMust(t, st.Drafts.SaveFinalChapter(1, "实际正文"))
				publicationArtifactMust(t, st.Progress.MarkChapterComplete(1, 4, "", ""))
			case "active-sealed":
				rebaseAllTestActivateOneChapterGeneration(t, live)
			case "zero-init":
				rebaseAllTestWriteFile(t, live, "meta/first_chapter_generation_readiness.json", `{"ready":true}`)
			case "damaged-archive":
				var r pipelineAllChapterRebaseReceipt
				publicationArtifactMust(t, readPipelinePlanningJSON(filepath.Join(live, "meta/all_chapter_rebase.json"), &r))
				publicationArtifactMust(t, os.WriteFile(filepath.Join(r.ArchiveOutput, "premise.md"), []byte("被更改的归档"), 0o644))
			}
			before, err := store.DirectoryContentRoot(live)
			publicationArtifactMust(t, err)
			if err := validateExplicitArchitectRefreshState(live, "world_codex"); err == nil {
				t.Fatal("unsafe long-book source revision was allowed")
			}
			after, err := store.DirectoryContentRoot(live)
			publicationArtifactMust(t, err)
			if before != after {
				t.Fatal("failed refresh preflight wrote to live")
			}
		})
	}
}

func TestLongArchitectRefreshUsesExplicitSourceOnlyPrompt(t *testing.T) {
	live := longArchitectRefreshFixture(t, true)
	before, err := store.DirectoryContentRoot(live)
	publicationArtifactMust(t, err)
	flags, extra, err := parsePipelineFlags([]string{"--refresh-architect", "--architect-target", "world_codex", "--stages", "architect"})
	publicationArtifactMust(t, err)
	if len(extra) != 0 {
		t.Fatal("unexpected CLI remainder")
	}
	publicationArtifactMust(t, validateExplicitArchitectRefreshParameters("只补作者态事实，不投放角色秘密", flags.ArchitectTarget))
	publicationArtifactMust(t, validateExplicitArchitectRefreshState(live, flags.ArchitectTarget))
	prompt, err := pipelineArchitectRefreshPrompt(live, "只补作者态事实，不投放角色秘密", flags.ArchitectTarget)
	publicationArtifactMust(t, err)
	for _, required := range []string{"100章", "显式 rebase", "world_codex", "本阶段不得修改 layered_outline/outline", "不能把作者掌握的历史事实", "只补作者态事实，不投放角色秘密"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("long source prompt omitted %q", required)
		}
	}
	for _, forbidden := range []string{"100章短篇", "只重做前三章", "重做完整100章", "一卷一弧覆盖全书"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("long source refresh received unrelated rewrite authority %q", forbidden)
		}
	}
	for _, target := range []string{"", "layered_outline", "not-a-source"} {
		if err := validateExplicitArchitectRefreshState(live, target); err == nil {
			t.Fatalf("long refresh accepted target %q", target)
		}
		if _, err := pipelineArchitectRefreshPrompt(live, "作者修订", target); err == nil {
			t.Fatalf("invalid target %q fell back to opening rewrite prompt", target)
		}
		if err := pipelineRefreshArchitectOpening(cliOptions{}, bootstrap.Config{OutputDir: live}, assets.Bundle{}, "作者修订", target); err == nil {
			t.Fatalf("dispatch preflight accepted target %q", target)
		}
	}
	after, err := store.DirectoryContentRoot(live)
	publicationArtifactMust(t, err)
	if before != after {
		t.Fatal("source routing/prompt checks mutated live")
	}
}
