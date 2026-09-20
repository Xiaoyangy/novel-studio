package main

import (
	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"testing"
)

func TestOutlineAllLegacyContractPolicyWireGolden(t *testing.T) {
	cfg := bootstrap.Config{Provider: "test", ModelName: "architect"}
	bundle := assets.Bundle{Prompts: assets.Prompts{ArchitectLong: "frozen architect test prompt"}}
	_, _, protocol, identity, err := pipelineOutlineAllExecutionIdentity(cfg, bundle)
	if err != nil {
		t.Fatal(err)
	}
	compass := domain.StoryCompass{EndingDirection: "居民完成账目交接", NonNegotiables: []string{"小说写作无论长篇还是短篇，一律使用第三人称视角。", "全书始终采用第三人称视角"}}
	volumes := []domain.VolumeOutline{{Index: 1, Title: "第一卷", Theme: "交接", Arcs: []domain.ArcOutline{{Index: 1, Title: "核对", Goal: "居民核对账目并作出交接选择", EstimatedChapters: 3}}}}
	digest, err := domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		t.Fatal(err)
	}
	action := domain.OutlineAllPendingAction{Type: domain.OutlineAllActionMapContracts, Operation: 1, ExpectedChapterSpan: 3, FinalSkeleton: true, BeforeLayeredDigest: digest}
	target := domain.BookScaleTarget{TargetVolumes: 1, TargetChapters: 3}
	foundation := pipelineOutlineAllFrozenFoundation{Root: "sha256:foundation", Premise: "居民核对账目并共同交接。"}
	view, raw, visible, err := buildPipelineOutlineAllModelVisibleContext(volumes, compass, target, action, foundation, tools.References{})
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := pipelineOutlineAllOperationPrompt(volumes, compass, target, action, view, raw)
	if err != nil {
		t.Fatal(err)
	}
	// Recorded by this same fixture against the unmodified 5faf5a9 source.
	// Thus a new default cannot silently change an unfinished legacy call's
	// protocol, model-visible context or full operation prompt.
	for label, pair := range map[string][2]string{
		"protocol": {protocol, "sha256:352b5afb446d07e1ffc8dc16644de8af8678f1c268b9de529b9f57be74d81799"},
		"identity": {pipelineBytesSHA([]byte(identity)), "sha256:9bdbd442ff379786de3f08523c0dba58bdaf15f4857163b646905cd97c754d63"},
		"context":  {visible, "sha256:9ca9c59a2eafec14bbfe29ec214658809b06f53694f9aefb8d050a13ef8335b2"},
		"prompt":   {pipelineBytesSHA([]byte(prompt)), "sha256:44340ab0a6794929cf91e4c621a0b1ba40e93baae16146dcdac55ec3f69b4ec9"},
	} {
		if pair[0] != pair[1] {
			t.Fatalf("legacy %s drifted: %s want %s", label, pair[0], pair[1])
		}
	}
}
