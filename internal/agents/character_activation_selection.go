package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func characterActivationExecutionLimit(st *store.Store, cfg bootstrap.Config, generation string, boundary ProjectedArcBoundary) (int, error) {
	if cfg.CharacterAgents.ExecutionPolicy == "v3" {
		if cfg.CharacterAgentsProtocolVersion() != domain.CharacterAgentDecisionProtocolV2Version || cfg.CharacterActivationLimit() < 2 {
			return 0, fmt.Errorf("explicit v3 requires configured v2 actors and a multi-cycle execution bound")
		}
		if (boundary.CharacterProtocolPinned || boundary.CharacterActivationPolicy != "") && boundary.CharacterActivationPolicy != domain.CharacterActivationCyclePolicyV3 {
			return 0, fmt.Errorf("explicit v3 requires a new generation, not reinterpretation of frozen legacy execution")
		}
		if !strings.HasPrefix(generation, domain.PlanningGenerationIDPrefix) {
			return 0, fmt.Errorf("explicit v3 requires a host-owned pg2 execution generation; this entry cannot silently run one-shot")
		}
	}
	if boundary.CharacterProtocolPinned || boundary.CharacterActivationPolicy != "" {
		if boundary.CharacterActivationPolicy == "" {
			return 1, nil
		}
		if (boundary.CharacterActivationPolicy != domain.CharacterActivationCyclePolicy && boundary.CharacterActivationPolicy != domain.CharacterActivationCyclePolicyV2 && boundary.CharacterActivationPolicy != domain.CharacterActivationCyclePolicyV3) || cfg.CharacterAgentsProtocolVersion() != domain.CharacterAgentDecisionProtocolV2Version || boundary.MaxCharacterActivationCycles < 2 || boundary.MaxCharacterActivationCycles > 64 {
			return 0, fmt.Errorf("invalid frozen generation character activation policy/limit")
		}
		return boundary.MaxCharacterActivationCycles, nil
	}
	if !strings.HasPrefix(generation, domain.PlanningGenerationIDPrefix) {
		return 1, nil
	}
	if st == nil {
		return 0, fmt.Errorf("standalone activation selection requires its persisted store")
	}
	frozenLimit, err := standaloneCharacterActivationLimit(st, generation)
	if err != nil {
		return 0, err
	}
	// A standalone recovery without the project-all host boundary must not
	// switch in either direction. An existing cycle session is stronger than
	// a new default (including MaxActivationCycles=1), and mixed namespaces are
	// not a choice of which evidence to ignore.
	legacy := false
	if info, err := os.Lstat(filepath.Join(st.Dir(), "meta/character_agents/projected", generation, "chapters")); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("standalone generation has an invalid legacy chapter namespace")
		}
		legacy = true
	} else if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if frozenLimit > 0 {
		if legacy || cfg.CharacterAgentsProtocolVersion() != domain.CharacterAgentDecisionProtocolV2Version {
			return 0, fmt.Errorf("generation %s already has multi-cycle evidence; cannot mix or downgrade its character protocol; start a new generation", generation)
		}
		return frozenLimit, nil
	}
	if legacy {
		return 1, nil
	}
	return cfg.CharacterActivationLimit(), nil
}

// Read exact sessions from this generation only. Do not recover/write a
// cursor, choose the latest mtime, or infer a new limit from missing evidence.
func standaloneCharacterActivationLimit(st *store.Store, generation string) (int, error) {
	// The production loader validates generation components and symlink
	// boundaries even when chapter 1 does not exist (e.g. a later arc).
	if _, err := st.LoadCharacterActivationSession(generation, 1); err != nil {
		return 0, err
	}
	root := filepath.Join(st.Dir(), "meta/character_agents/activation_sessions", generation)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	limit := 0
	for _, entry := range entries {
		chapter, err := strconv.Atoi(entry.Name())
		if err != nil || chapter <= 0 || entry.Name() != fmt.Sprintf("%06d", chapter) || !entry.IsDir() {
			return 0, fmt.Errorf("generation %s has an invalid activation session path %q", generation, entry.Name())
		}
		session, err := st.LoadCharacterActivationSession(generation, chapter)
		if err != nil {
			return 0, err
		}
		if session == nil {
			files, err := os.ReadDir(filepath.Join(root, entry.Name()))
			if err != nil {
				return 0, err
			}
			for _, file := range files {
				if file.Name() != "execution.lock" {
					return 0, fmt.Errorf("generation %s chapter %d has activation evidence without its session; restore the original session before selecting a protocol", generation, chapter)
				}
			}
			continue // Empty pre-dispatch lock directory contains no frozen limit.
		}
		if session.MaxCycles < 2 || session.MaxCycles > 64 || (limit != 0 && session.MaxCycles != limit) {
			return 0, fmt.Errorf("generation %s has conflicting or unsupported frozen activation limits", generation)
		}
		limit = session.MaxCycles
	}
	return limit, nil
}
