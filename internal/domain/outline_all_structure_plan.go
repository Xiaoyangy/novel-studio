package domain

import "fmt"

// OutlineAllVolumePlan is one volume's model-chosen shape in the frozen
// full-book structure plan: an ordered list of arc chapter spans. Titles,
// themes, arc goals and chapter detail are still authored later at
// append_volume / expand_arc time; only the structural allocation lives here.
type OutlineAllVolumePlan struct {
	ArcSpans []int `json:"arc_spans"`
}

// ChapterSpan is the volume's total reserved chapter count.
func (p OutlineAllVolumePlan) ChapterSpan() int {
	total := 0
	for _, span := range p.ArcSpans {
		total += span
	}
	return total
}

// OutlineAllStructurePlan is the model-allocated, host-frozen breakdown of the
// whole book into volumes and per-arc chapter spans. Once frozen into the
// execution receipt it is replayed deterministically: append_volume reserves a
// volume's arc spans from here, and expand_arc fills each reservation. It
// replaces the previous arithmetic partition (even volume split +
// RecommendedOutlineAllArcSpans), so the number of volumes, the chapters per
// volume, and the chapters per arc are all story decisions made by the model.
type OutlineAllStructurePlan struct {
	Volumes []OutlineAllVolumePlan `json:"volumes"`
}

// TotalVolumes returns the model-chosen volume count.
func (p OutlineAllStructurePlan) TotalVolumes() int { return len(p.Volumes) }

// TotalChapters returns the model-chosen whole-book chapter total.
func (p OutlineAllStructurePlan) TotalChapters() int {
	total := 0
	for _, volume := range p.Volumes {
		total += volume.ChapterSpan()
	}
	return total
}

// ArcSpansForVolume returns the frozen arc spans for the 1-based volume index,
// or nil when the plan has no such volume.
func (p OutlineAllStructurePlan) ArcSpansForVolume(volumeIndex int) []int {
	if volumeIndex <= 0 || volumeIndex > len(p.Volumes) {
		return nil
	}
	spans := p.Volumes[volumeIndex-1].ArcSpans
	out := make([]int, len(spans))
	copy(out, spans)
	return out
}

// DeriveOutlineAllStructurePlan reads the model-authored reservation skeleton
// (real, positive-index volumes with reservation arcs) into the compact plan
// the host freezes into the receipt. Legacy compatibility shells (index <= 0
// or arc-less volumes) are ignored, matching RealVolumeCount.
func DeriveOutlineAllStructurePlan(volumes []VolumeOutline) OutlineAllStructurePlan {
	plan := OutlineAllStructurePlan{}
	for _, volume := range volumes {
		if volume.Index <= 0 || len(volume.Arcs) == 0 {
			continue
		}
		spans := make([]int, 0, len(volume.Arcs))
		for _, arc := range volume.Arcs {
			spans = append(spans, arc.ChapterSpan())
		}
		plan.Volumes = append(plan.Volumes, OutlineAllVolumePlan{ArcSpans: spans})
	}
	return plan
}

// ValidateOutlineAllStructurePlan checks a model-proposed plan against the
// frozen estimated_scale range. Arc spans are model-chosen with no upper
// bound; only positivity (>= OutlineAllMinPlanArcChapters) and the whole-book
// volume/chapter range from estimated_scale are enforced.
func ValidateOutlineAllStructurePlan(plan OutlineAllStructurePlan, scale BookScaleRange) error {
	if err := scale.Validate(); err != nil {
		return err
	}
	if len(plan.Volumes) == 0 {
		return fmt.Errorf("outline-all structure plan requires at least one volume")
	}
	for vi, volume := range plan.Volumes {
		if len(volume.ArcSpans) == 0 {
			return fmt.Errorf("outline-all structure plan volume %d requires at least one arc", vi+1)
		}
		for ai, span := range volume.ArcSpans {
			if span < OutlineAllMinPlanArcChapters {
				return fmt.Errorf(
					"outline-all structure plan V%dA%d span %d is below the %d-chapter floor",
					vi+1, ai+1, span, OutlineAllMinPlanArcChapters,
				)
			}
		}
	}
	volumes := plan.TotalVolumes()
	if volumes < scale.MinVolumes || volumes > scale.MaxVolumes {
		return fmt.Errorf(
			"outline-all structure plan volume count %d is outside estimated_scale %d-%d",
			volumes, scale.MinVolumes, scale.MaxVolumes,
		)
	}
	chapters := plan.TotalChapters()
	if chapters < scale.MinChapters || chapters > scale.MaxChapters {
		return fmt.Errorf(
			"outline-all structure plan chapter total %d is outside estimated_scale %d-%d",
			chapters, scale.MinChapters, scale.MaxChapters,
		)
	}
	return nil
}
