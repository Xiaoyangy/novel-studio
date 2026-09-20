package domain

import (
	"strings"
	"testing"
)

const narrationPolicyActualSource = "小说写作无论长篇还是短篇，一律使用第三人称视角。"

func TestStoryContractNarrationPolicyFreshAndLegacySourceModes(t *testing.T) {
	for _, source := range []string{narrationPolicyActualSource, "全文采用第三人称叙述", "全书统一使用第三人称视角。"} {
		compass := StoryCompass{NonNegotiables: []string{source}}
		refs, err := BuildStoryContractRegistryForPolicy(compass, StoryContractEvidencePolicyNarrationV1)
		if err != nil || len(refs) != 1 || refs[0].EvidenceMode != StoryContractEvidenceContinuous {
			t.Fatalf("fresh narration mode: %+v %v", refs, err)
		}
		volumes := []VolumeOutline{{Index: 1, Arcs: []ArcOutline{{Index: 1, EstimatedChapters: 3, ContractRefs: refs}}}}
		if issues := StoryContractSkeletonIssuesForPolicy(volumes, compass, true, StoryContractEvidencePolicyNarrationV1); len(issues) > 0 {
			t.Fatal(issues)
		}
		if missing := MissingCompassCoverageForPolicy(volumes, compass, StoryContractEvidencePolicyNarrationV1); len(missing) > 0 {
			t.Fatal(missing)
		}
		volumes[0].Arcs[0].ContractRefs[0].EvidenceMode = ""
		if issues := StoryContractSkeletonIssuesForPolicy(volumes, compass, true, StoryContractEvidencePolicyNarrationV1); len(issues) == 0 {
			t.Fatal("fresh model omitted its Host-derived mode")
		}
	}
	if refs := BuildStoryContractRegistry(StoryCompass{NonNegotiables: []string{narrationPolicyActualSource}}); refs[0].EvidenceMode != StoryContractEvidencePayoff {
		t.Fatal("legacy classifier was silently upgraded")
	}
}

func TestStoryContractNarrationPolicyCannotHidePlotPayoff(t *testing.T) {
	for _, source := range []string{"最后一章主角宣布全书一律采用第三人称视角。", "全书采用第三人称视角；最终主角收回旧港仓库", "终章所有人员一律在码头签字", "主角在终章改变视角后向所有亲人道歉"} {
		compass := StoryCompass{NonNegotiables: []string{source}}
		refs, err := BuildStoryContractRegistryForPolicy(compass, StoryContractEvidencePolicyNarrationV1)
		if err != nil || refs[0].EvidenceMode != StoryContractEvidencePayoff {
			t.Fatalf("plot payoff relaxed: %q %+v %v", source, refs, err)
		}
		refs[0].EvidenceMode = StoryContractEvidenceContinuous
		volumes := []VolumeOutline{{Index: 1, Arcs: []ArcOutline{{Index: 1, EstimatedChapters: 3, ContractRefs: refs}}}}
		if issues := StoryContractSkeletonIssuesForPolicy(volumes, compass, true, StoryContractEvidencePolicyNarrationV1); len(issues) == 0 {
			t.Fatal("model-selected continuous bypass accepted")
		}
	}
	if _, err := BuildStoryContractRegistryForPolicy(StoryCompass{}, "unknown"); err == nil {
		t.Fatal("unknown policy accepted")
	}
}

func TestLegacyEmptyEvidenceModeUsesPayoffInRealCoverageValidators(t *testing.T) {
	for _, source := range []string{narrationPolicyActualSource, "全书始终采用第三人称视角"} {
		compass := StoryCompass{NonNegotiables: []string{source}}
		ref := BuildStoryContractRegistry(compass)[0]
		ref.EvidenceMode = ""
		ref.PlannedPayoffChapter = 1
		ref.PlannedResolution = "林舟与周宁共同核对全部账页并当场签字封存，双方各自确认本次移交范围"
		volumes := []VolumeOutline{{Index: 1, Arcs: []ArcOutline{{Index: 1, ContractRefs: []StoryContractRef{ref}, Chapters: []OutlineEntry{{Chapter: 1, Title: "结案", CoreEvent: ref.PlannedResolution, ContractRefs: []StoryContractRef{ref}}}}}}}
		if issues := StoryContractSkeletonIssues(volumes, compass, true); len(issues) > 0 {
			t.Fatal(issues)
		}
		if missing := MissingCompassCoverage(volumes, compass); len(missing) > 0 {
			t.Fatal(missing)
		}
	}
	compass := StoryCompass{NonNegotiables: []string{"全书始终采用第三人称视角"}}
	ref := BuildStoryContractRegistry(compass)[0]
	ref.EvidenceMode = StoryContractEvidencePayoff
	ref.PlannedPayoffChapter = 1
	ref.PlannedResolution = strings.Repeat("具体落地的真实结果", 4)
	volumes := []VolumeOutline{{Index: 1, Arcs: []ArcOutline{{Index: 1, EstimatedChapters: 1, ContractRefs: []StoryContractRef{ref}}}}}
	if issues := StoryContractSkeletonIssues(volumes, compass, true); len(issues) == 0 {
		t.Fatal("explicit legacy continuous source accepted arbitrary explicit payoff downgrade")
	}
}
