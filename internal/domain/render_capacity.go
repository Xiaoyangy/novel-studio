package domain

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	MinRenderCapacityScenes     = 3
	MaxRenderCapacityScenes     = 6
	MinConcreteActionBeatsScene = 3
	MinRenderSceneTargetRunes   = 300
	MaxRenderSceneTargetRunes   = 1400
)

// ChapterRenderCapacity proves that a projected chapter has enough causal,
// on-page material to sustain its configured prose length without padding.
// It is a renderability contract, not a prescribed paragraph or shot order.
type ChapterRenderCapacity struct {
	TotalTargetRunes  int                      `json:"total_target_runes"`
	SceneUnits        []ChapterRenderSceneUnit `json:"scene_units"`
	AntiPaddingPolicy string                   `json:"anti_padding_policy"`
}

// ChapterRenderSceneUnit is the smallest prose-facing causal spine. Each unit
// has an active objective/opposition/turn and enough observable action evidence
// to become a real scene instead of explanatory summary or repeated dialogue.
type ChapterRenderSceneUnit struct {
	SceneID             string   `json:"scene_id"`
	TargetRunes         int      `json:"target_runes"`
	POVObjective        string   `json:"pov_objective"`
	ActiveOpposition    string   `json:"active_opposition"`
	Turn                string   `json:"turn"`
	ExitConsequence     string   `json:"exit_consequence"`
	ConcreteActionBeats []string `json:"concrete_action_beats"`
}

// Validate checks both the scene-level substance and the exact aggregate word
// budget supplied by user_rules. A scene target is intentionally not treated
// as a fixed quota during rendering; the sum is proof of available capacity.
func (c ChapterRenderCapacity) Validate(minRunes, maxRunes int) error {
	if minRunes <= 0 || maxRunes <= 0 || minRunes > maxRunes {
		return fmt.Errorf("render_capacity requires a valid user_rules.chapter_words range")
	}
	if len(c.SceneUnits) < MinRenderCapacityScenes || len(c.SceneUnits) > MaxRenderCapacityScenes {
		return fmt.Errorf(
			"render_capacity.scene_units=%d, want %d-%d",
			len(c.SceneUnits),
			MinRenderCapacityScenes,
			MaxRenderCapacityScenes,
		)
	}
	if strings.TrimSpace(c.AntiPaddingPolicy) == "" {
		return fmt.Errorf("render_capacity.anti_padding_policy is required")
	}

	seenScenes := make(map[string]struct{}, len(c.SceneUnits))
	total := 0
	for i, scene := range c.SceneUnits {
		prefix := fmt.Sprintf("render_capacity.scene_units[%d]", i)
		id := strings.TrimSpace(scene.SceneID)
		if id == "" {
			return fmt.Errorf("%s.scene_id is required", prefix)
		}
		if _, duplicate := seenScenes[id]; duplicate {
			return fmt.Errorf("%s.scene_id=%q is duplicated", prefix, id)
		}
		seenScenes[id] = struct{}{}
		if scene.TargetRunes < MinRenderSceneTargetRunes || scene.TargetRunes > MaxRenderSceneTargetRunes {
			return fmt.Errorf(
				"%s.target_runes=%d, want %d-%d",
				prefix,
				scene.TargetRunes,
				MinRenderSceneTargetRunes,
				MaxRenderSceneTargetRunes,
			)
		}
		for field, value := range map[string]string{
			"pov_objective":     scene.POVObjective,
			"active_opposition": scene.ActiveOpposition,
			"turn":              scene.Turn,
			"exit_consequence":  scene.ExitConsequence,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s.%s is required", prefix, field)
			}
		}

		uniqueBeats := make(map[string]struct{}, len(scene.ConcreteActionBeats))
		for _, beat := range scene.ConcreteActionBeats {
			beat = strings.TrimSpace(beat)
			if renderCapacityMeaningfulRunes(beat) < 4 {
				continue
			}
			uniqueBeats[beat] = struct{}{}
		}
		if len(uniqueBeats) < MinConcreteActionBeatsScene {
			return fmt.Errorf(
				"%s.concrete_action_beats has %d concrete unique beats, want at least %d",
				prefix,
				len(uniqueBeats),
				MinConcreteActionBeatsScene,
			)
		}
		total += scene.TargetRunes
	}

	if c.TotalTargetRunes != total {
		return fmt.Errorf(
			"render_capacity.total_target_runes=%d does not equal scene target sum=%d",
			c.TotalTargetRunes,
			total,
		)
	}
	if total < minRunes || total > maxRunes {
		return fmt.Errorf(
			"render_capacity total=%d is outside user_rules.chapter_words=%d-%d",
			total,
			minRunes,
			maxRunes,
		)
	}
	return nil
}

func renderCapacityMeaningfulRunes(value string) int {
	count := 0
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			count++
		}
	}
	return count
}
