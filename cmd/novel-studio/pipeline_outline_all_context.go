package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelineOutlineAllVisibleContextVersion  = "outline-all-visible-context.v1"
	pipelineOutlineAllVisibleContextMaxBytes = 192 * 1024
	pipelineOutlineAllBoundaryChapterCount   = 2
)

type pipelineOutlineAllBoundedText struct {
	Digest        string `json:"digest"`
	OriginalBytes int    `json:"original_bytes"`
	Text          string `json:"text"`
	Truncated     bool   `json:"truncated,omitempty"`
}

type pipelineOutlineAllFoundationView struct {
	Root        string                                   `json:"root"`
	Premise     pipelineOutlineAllBoundedText            `json:"premise"`
	Characters  pipelineOutlineAllBoundedText            `json:"characters"`
	WorldRules  pipelineOutlineAllBoundedText            `json:"world_rules"`
	BookWorld   pipelineOutlineAllBoundedText            `json:"book_world"`
	Compass     pipelineOutlineAllBoundedText            `json:"compass"`
	Authorities map[string]pipelineOutlineAllBoundedText `json:"authorities,omitempty"`
}

type pipelineOutlineAllReferenceView struct {
	FullPackDigest string                                   `json:"full_pack_digest"`
	Selected       map[string]pipelineOutlineAllBoundedText `json:"selected"`
}

type pipelineOutlineAllChapterView struct {
	Chapter      int                       `json:"chapter"`
	Title        string                    `json:"title"`
	CoreEvent    string                    `json:"core_event"`
	Hook         string                    `json:"hook"`
	Scenes       []string                  `json:"scenes"`
	ContractRefs []domain.StoryContractRef `json:"contract_refs,omitempty"`
}

type pipelineOutlineAllTargetArcView struct {
	Volume       int                             `json:"volume"`
	VolumeTitle  string                          `json:"volume_title"`
	VolumeTheme  string                          `json:"volume_theme"`
	Arc          int                             `json:"arc"`
	Title        string                          `json:"title"`
	Goal         string                          `json:"goal"`
	Start        int                             `json:"start_chapter"`
	End          int                             `json:"end_chapter"`
	Expanded     bool                            `json:"expanded"`
	ContractRefs []domain.StoryContractRef       `json:"contract_refs,omitempty"`
	Chapters     []pipelineOutlineAllChapterView `json:"chapters,omitempty"`
}

type pipelineOutlineAllBoundaryView struct {
	Volume       int                             `json:"volume"`
	Arc          int                             `json:"arc"`
	Title        string                          `json:"title"`
	Goal         string                          `json:"goal"`
	Start        int                             `json:"start_chapter"`
	End          int                             `json:"end_chapter"`
	ContractRefs []domain.StoryContractRef       `json:"contract_refs,omitempty"`
	Chapters     []pipelineOutlineAllChapterView `json:"chapters,omitempty"`
}

type pipelineOutlineAllModelVisibleContext struct {
	Version                  string                             `json:"version"`
	Operation                int                                `json:"operation"`
	Action                   domain.OutlineAllPendingAction     `json:"action"`
	FoundationContextRoot    string                             `json:"foundation_context_root"`
	FullLayeredOutlineDigest string                             `json:"full_layered_outline_digest"`
	TargetVolumes            int                                `json:"target_volumes"`
	TargetChapters           int                                `json:"target_chapters"`
	Foundation               pipelineOutlineAllFoundationView   `json:"foundation"`
	References               pipelineOutlineAllReferenceView    `json:"references"`
	ContractRegistry         []pipelineOutlineAllContractSource `json:"contract_registry"`
	CompleteLayeredArcMap    []pipelineOutlineAllArcRange       `json:"complete_layered_arc_map"`
	TargetArc                *pipelineOutlineAllTargetArcView   `json:"target_arc,omitempty"`
	PreviousBoundary         *pipelineOutlineAllBoundaryView    `json:"previous_boundary,omitempty"`
	NextBoundary             *pipelineOutlineAllBoundaryView    `json:"next_boundary,omitempty"`
}

func buildPipelineOutlineAllModelVisibleContext(
	volumes []domain.VolumeOutline,
	compass domain.StoryCompass,
	target domain.BookScaleTarget,
	action domain.OutlineAllPendingAction,
	foundation pipelineOutlineAllFrozenFoundation,
	references tools.References,
) (pipelineOutlineAllModelVisibleContext, []byte, string, error) {
	fullDigest, err := domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		return pipelineOutlineAllModelVisibleContext{}, nil, "", err
	}
	if fullDigest != action.BeforeLayeredDigest {
		return pipelineOutlineAllModelVisibleContext{}, nil, "", fmt.Errorf(
			"outline-all visible context layered digest=%s want intent before=%s",
			fullDigest, action.BeforeLayeredDigest,
		)
	}
	if strings.TrimSpace(foundation.Root) == "" {
		return pipelineOutlineAllModelVisibleContext{}, nil, "", fmt.Errorf("outline-all visible context requires frozen foundation root")
	}

	view := pipelineOutlineAllModelVisibleContext{
		Version:                  pipelineOutlineAllVisibleContextVersion,
		Operation:                action.Operation,
		Action:                   action,
		FoundationContextRoot:    foundation.Root,
		FullLayeredOutlineDigest: fullDigest,
		TargetVolumes:            target.TargetVolumes,
		TargetChapters:           target.TargetChapters,
		Foundation: pipelineOutlineAllFoundationView{
			Root:       foundation.Root,
			Premise:    pipelineOutlineAllBoundText(foundation.Premise, 16*1024),
			Characters: pipelineOutlineAllBoundJSON(foundation.Characters, 28*1024),
			WorldRules: pipelineOutlineAllBoundJSON(foundation.WorldRules, 20*1024),
			BookWorld:  pipelineOutlineAllBoundJSON(foundation.BookWorld, 24*1024),
			Compass:    pipelineOutlineAllBoundJSON(foundation.Compass, 16*1024),
		},
		References: pipelineOutlineAllReferenceView{
			FullPackDigest: pipelineProjectAllDigest(references),
			Selected: map[string]pipelineOutlineAllBoundedText{
				"longform_planning":         pipelineOutlineAllBoundText(references.LongformPlanning, 8*1024),
				"differentiation":           pipelineOutlineAllBoundText(references.Differentiation, 6*1024),
				"arc_templates":             pipelineOutlineAllBoundText(references.ArcTemplates, 8*1024),
				"character_building":        pipelineOutlineAllBoundText(references.CharacterBuilding, 6*1024),
				"emotional_narrative_craft": pipelineOutlineAllBoundText(references.EmotionalNarrativeCraft, 6*1024),
				"production_playbook":       pipelineOutlineAllBoundText(references.ProductionPlaybook, 6*1024),
			},
		},
		ContractRegistry:      pipelineOutlineAllContractRegistry(compass),
		CompleteLayeredArcMap: pipelineOutlineAllArcMap(volumes),
	}
	if len(foundation.Authorities) > 0 {
		view.Foundation.Authorities = make(map[string]pipelineOutlineAllBoundedText, len(foundation.Authorities))
		keys := make([]string, 0, len(foundation.Authorities))
		for key := range foundation.Authorities {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			// These include the frozen web/RAG brief, user rules, brainstorm,
			// and prewrite plan. Each remains digest-bound even when excerpted.
			view.Foundation.Authorities[key] = pipelineOutlineAllBoundText(foundation.Authorities[key], 8*1024)
		}
	}
	if action.Type == domain.OutlineAllActionExpandArc || action.Type == domain.OutlineAllActionReviseArc {
		targetArc, previous, next, err := pipelineOutlineAllTargetAndBoundaries(volumes, action.Volume, action.Arc)
		if err != nil {
			return pipelineOutlineAllModelVisibleContext{}, nil, "", err
		}
		view.TargetArc = targetArc
		view.PreviousBoundary = previous
		view.NextBoundary = next
	} else if action.Type == domain.OutlineAllActionAppendVolume {
		// The new volume does not exist yet. Preserve the last realized edge so
		// the appended skeleton has an explicit causal handoff from prior work.
		_, previous, _, err := pipelineOutlineAllTargetAndBoundaries(volumes, 0, 0)
		if err == nil {
			view.PreviousBoundary = previous
		}
	}
	raw, err := json.Marshal(view)
	if err != nil {
		return pipelineOutlineAllModelVisibleContext{}, nil, "", err
	}
	if len(raw) > pipelineOutlineAllVisibleContextMaxBytes {
		return pipelineOutlineAllModelVisibleContext{}, nil, "", fmt.Errorf(
			"outline-all model-visible context is %d bytes, limit=%d; reduce pathological outline field sizes without dropping contracts",
			len(raw), pipelineOutlineAllVisibleContextMaxBytes,
		)
	}
	return view, raw, pipelineBytesSHA(raw), nil
}

func pipelineOutlineAllBoundJSON(raw json.RawMessage, maxBytes int) pipelineOutlineAllBoundedText {
	var compact bytes.Buffer
	if len(raw) > 0 && json.Compact(&compact, raw) == nil {
		return pipelineOutlineAllBoundText(compact.String(), maxBytes)
	}
	return pipelineOutlineAllBoundText(string(raw), maxBytes)
}

func pipelineOutlineAllBoundText(value string, maxBytes int) pipelineOutlineAllBoundedText {
	original := []byte(value)
	result := pipelineOutlineAllBoundedText{
		Digest: pipelineBytesSHA(original), OriginalBytes: len(original), Text: value,
	}
	if maxBytes <= 0 || len(original) <= maxBytes {
		return result
	}
	suffix := fmt.Sprintf("\n[TRUNCATED digest=%s original_bytes=%d]", result.Digest, len(original))
	limit := maxBytes - len(suffix)
	if limit < 0 {
		limit = 0
	}
	prefix := original[:limit]
	for len(prefix) > 0 && !utf8.Valid(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	result.Text = string(prefix) + suffix
	result.Truncated = true
	return result
}

func pipelineOutlineAllTargetAndBoundaries(
	volumes []domain.VolumeOutline,
	targetVolume, targetArc int,
) (*pipelineOutlineAllTargetArcView, *pipelineOutlineAllBoundaryView, *pipelineOutlineAllBoundaryView, error) {
	type locatedArc struct {
		volume domain.VolumeOutline
		arc    domain.ArcOutline
		start  int
	}
	located := make([]locatedArc, 0)
	cursor := 1
	targetIndex := -1
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			if volume.Index == targetVolume && arc.Index == targetArc {
				targetIndex = len(located)
			}
			located = append(located, locatedArc{volume: volume, arc: arc, start: cursor})
			cursor += arc.ChapterSpan()
		}
	}
	if targetVolume == 0 && targetArc == 0 {
		if len(located) == 0 {
			return nil, nil, nil, nil
		}
		last := pipelineOutlineAllBoundaryFromLocated(located[len(located)-1], true)
		return nil, last, nil, nil
	}
	if targetIndex < 0 {
		return nil, nil, nil, fmt.Errorf("outline-all visible target V%dA%d not found", targetVolume, targetArc)
	}
	target := located[targetIndex]
	targetView := &pipelineOutlineAllTargetArcView{
		Volume: target.volume.Index, VolumeTitle: pipelineOutlineAllBoundInline(target.volume.Title, 1024),
		VolumeTheme: pipelineOutlineAllBoundInline(target.volume.Theme, 2048),
		Arc:         target.arc.Index, Title: pipelineOutlineAllBoundInline(target.arc.Title, 1024),
		Goal: pipelineOutlineAllBoundInline(target.arc.Goal, 2048), Start: target.start,
		End: target.start + target.arc.ChapterSpan() - 1, Expanded: target.arc.IsExpanded(),
		ContractRefs: append([]domain.StoryContractRef(nil), target.arc.ContractRefs...),
		Chapters:     pipelineOutlineAllChapterViews(target.arc.Chapters, target.start, 0, len(target.arc.Chapters)),
	}
	var previous, next *pipelineOutlineAllBoundaryView
	if targetIndex > 0 {
		previous = pipelineOutlineAllBoundaryFromLocated(located[targetIndex-1], true)
	}
	if targetIndex+1 < len(located) {
		next = pipelineOutlineAllBoundaryFromLocated(located[targetIndex+1], false)
	}
	return targetView, previous, next, nil
}

func pipelineOutlineAllBoundaryFromLocated(value struct {
	volume domain.VolumeOutline
	arc    domain.ArcOutline
	start  int
}, tail bool) *pipelineOutlineAllBoundaryView {
	from, to := 0, len(value.arc.Chapters)
	if tail && to > pipelineOutlineAllBoundaryChapterCount {
		from = to - pipelineOutlineAllBoundaryChapterCount
	}
	if !tail && to > pipelineOutlineAllBoundaryChapterCount {
		to = pipelineOutlineAllBoundaryChapterCount
	}
	return &pipelineOutlineAllBoundaryView{
		Volume: value.volume.Index, Arc: value.arc.Index,
		Title: pipelineOutlineAllBoundInline(value.arc.Title, 1024), Goal: pipelineOutlineAllBoundInline(value.arc.Goal, 2048),
		Start: value.start, End: value.start + value.arc.ChapterSpan() - 1,
		ContractRefs: append([]domain.StoryContractRef(nil), value.arc.ContractRefs...),
		Chapters:     pipelineOutlineAllChapterViews(value.arc.Chapters, value.start, from, to),
	}
}

func pipelineOutlineAllChapterViews(
	chapters []domain.OutlineEntry,
	start, from, to int,
) []pipelineOutlineAllChapterView {
	if from < 0 {
		from = 0
	}
	if to > len(chapters) {
		to = len(chapters)
	}
	if from >= to {
		return nil
	}
	result := make([]pipelineOutlineAllChapterView, 0, to-from)
	for index := from; index < to; index++ {
		chapter := chapters[index]
		scenes := make([]string, 0, len(chapter.Scenes))
		for _, scene := range chapter.Scenes {
			scenes = append(scenes, pipelineOutlineAllBoundInline(scene, 3072))
		}
		result = append(result, pipelineOutlineAllChapterView{
			Chapter: start + index, Title: pipelineOutlineAllBoundInline(chapter.Title, 1024),
			CoreEvent: pipelineOutlineAllBoundInline(chapter.CoreEvent, 4096),
			Hook:      pipelineOutlineAllBoundInline(chapter.Hook, 2048), Scenes: scenes,
			ContractRefs: append([]domain.StoryContractRef(nil), chapter.ContractRefs...),
		})
	}
	return result
}

func pipelineOutlineAllBoundInline(value string, maxBytes int) string {
	return pipelineOutlineAllBoundText(value, maxBytes).Text
}
