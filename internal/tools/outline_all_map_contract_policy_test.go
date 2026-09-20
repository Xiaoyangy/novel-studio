package tools

import (
	"fmt"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestOutlineAllMapContractsUsesReceiptPolicyAndPreservesStoredModes(t *testing.T) {
	for _, name := range []string{"fresh-narration-continuous", "fresh-legacy-empty", "stored-legacy-empty", "empty-to-continuous", "empty-to-explicit-payoff", "continuous-to-empty", "continuous-to-payoff", "payoff-to-empty", "reordered-stored-modes"} {
		t.Run(name, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			volumes := []domain.VolumeOutline{{Index: 1, Title: "全书", Arcs: []domain.ArcOutline{{Index: 1, Title: "主弧", EstimatedChapters: 12}}}}
			if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
				t.Fatal(err)
			}
			receipt := authorizeOutlineAllForTest(t, st)
			policy := ""
			source := "小说写作无论长篇还是短篇，一律使用第三人称视角。"
			if name == "fresh-narration-continuous" {
				policy = domain.StoryContractEvidencePolicyNarrationV1
			}
			if name == "continuous-to-empty" || name == "continuous-to-payoff" || name == "empty-to-continuous" {
				source = "全书始终采用第三人称视角。"
			}
			compass := domain.StoryCompass{EndingDirection: "林舟交还仓库，周宁公开核定账本。", NonNegotiables: []string{source}, EstimatedScale: "1-1卷，12-12章"}
			if err := st.Outline.SaveCompass(compass); err != nil {
				t.Fatal(err)
			}
			refs, err := domain.BuildStoryContractRegistryForPolicy(compass, policy)
			if err != nil {
				t.Fatal(err)
			}
			contract := -1
			for i := range refs {
				if refs[i].Kind == domain.StoryContractNonNegotiable {
					contract = i
				}
				if domain.StoryContractEvidenceMode(refs[i]) == domain.StoryContractEvidencePayoff {
					refs[i].PlannedPayoffChapter = 12
					refs[i].PlannedResolution = fmt.Sprintf("终局证据%d：林舟与周宁共同核对全部账页并当场签字封存，双方确认移交范围和追索边界", i)
				}
			}
			if contract < 0 {
				t.Fatal("fixture has no author contract")
			}
			if name == "stored-legacy-empty" || name == "reordered-stored-modes" || name == "empty-to-continuous" || name == "empty-to-explicit-payoff" {
				refs[contract].EvidenceMode = ""
				refs[contract].PlannedPayoffChapter = 12
				refs[contract].PlannedResolution = "林舟与周宁共同核对全部账页并当场签字封存，双方确认移交范围和追索边界"
			}
			if name != "fresh-narration-continuous" && name != "fresh-legacy-empty" {
				volumes[0].Arcs[0].ContractRefs = append([]domain.StoryContractRef(nil), refs...)
				if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "empty-to-continuous":
				refs[contract].EvidenceMode = domain.StoryContractEvidenceContinuous
				refs[contract].PlannedPayoffChapter, refs[contract].PlannedResolution = 0, ""
			case "empty-to-explicit-payoff":
				refs[contract].EvidenceMode = domain.StoryContractEvidencePayoff
			case "fresh-legacy-empty", "payoff-to-empty":
				refs[contract].EvidenceMode = ""
			case "continuous-to-empty", "continuous-to-payoff":
				refs[contract].EvidenceMode = ""
				if name == "continuous-to-payoff" {
					refs[contract].EvidenceMode = domain.StoryContractEvidencePayoff
				}
				refs[contract].PlannedPayoffChapter = 12
				refs[contract].PlannedResolution = "作者在终章向全部读者解释全书叙述方式，林舟与周宁共同核对并签字确认其落实范围"
			case "reordered-stored-modes":
				refs[0], refs[1] = refs[1], refs[0]
			}
			// This is a new fixture attempt, not an ordinary policy-changing
			// update of the helper's original receipt.
			receipt.AttemptID += "-map-policy"
			receipt.ContractEvidencePolicy = policy
			receipt.CompassDigest, err = domain.ComputeStoryCompassDigest(compass)
			if err != nil {
				t.Fatal(err)
			}
			receipt.EstimatedScale, receipt.EndingDirection, receipt.NonNegotiables = compass.EstimatedScale, compass.EndingDirection, compass.NonNegotiables
			receipt.MinVolumes, receipt.MaxVolumes, receipt.TargetVolumes = 1, 1, 1
			receipt.MinChapters, receipt.MaxChapters, receipt.TargetChapters = 12, 12, 12
			beforeDigest, err := domain.ComputeLayeredOutlineDigest(volumes)
			if err != nil {
				t.Fatal(err)
			}
			receipt.PendingAction = &domain.OutlineAllPendingAction{Type: domain.OutlineAllActionMapContracts, Operation: 1, ExpectedChapterSpan: 12, BeforeLayeredDigest: beforeDigest, FinalSkeleton: true}
			receipt, err = domain.SignOutlineAllExecutionReceipt(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SaveOutlineAllExecutionReceipt(receipt); err != nil {
				t.Fatal(err)
			}
			before, err := store.DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			err = validateOutlineAllMapContractsContent(st, []domain.ArcContractAssignment{{Volume: 1, Arc: 1, ContractRefs: refs}})
			wantOK := name == "fresh-narration-continuous" || name == "stored-legacy-empty" || name == "reordered-stored-modes" || name == "empty-to-explicit-payoff"
			if (err == nil) != wantOK {
				t.Fatalf("policy=%q map_contracts accepted=%t want=%t: %v", policy, err == nil, wantOK, err)
			}
			if after, err := store.DirectoryContentRoot(st.Dir()); err != nil || before != after {
				t.Fatalf("map validation changed stored source data: %v", err)
			}
		})
	}
}
