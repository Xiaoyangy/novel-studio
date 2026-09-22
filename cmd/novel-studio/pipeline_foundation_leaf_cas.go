package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const pipelineFoundationLeafCASMode = "host-character-leaf-cas.v1"

type pipelineFoundationLeafChange struct {
	Character string `json:"character"`
	Pointer   string `json:"pointer"`
	Old       string `json:"old"`
	New       string `json:"new"`
}

// One separately typed add permission, not a general missing-leaf operation.
type pipelineFoundationLocationNameCAS struct {
	Character       string `json:"character"`
	Pointer         string `json:"pointer"`
	ExpectedMissing bool   `json:"expected_missing"`
	New             *bool  `json:"new"`
}

var pipelineFoundationLeafPointer = regexp.MustCompile(`^/(0|[1-9][0-9]*)/initial_state/(known_facts/(0|[1-9][0-9]*)|relationships/(0|[1-9][0-9]*)|current_action|pressure)$`)
var pipelineFoundationLocationNamePointer = regexp.MustCompile(`^/(0|[1-9][0-9]*)/initial_state/location_name_known$`)

func validatePipelineFoundationLeafManifest(m pipelineFoundationRepairManifest) error {
	if m.Mode != pipelineFoundationLeafCASMode || m.Target != "characters" || m.ReportDigest != "" || m.ResourceID != "" || len(m.AllowedJSONPointers) != 0 || len(m.NewResources) != 0 || len(m.RequiredAuthorRefs) != 0 {
		return fmt.Errorf("host leaf CAS requires characters-only authority, without model/report/resource repair fields")
	}
	if err := validatePipelineOutlineRepairDigest("expected_source_digest", m.ExpectedSourceDigest); err != nil {
		return err
	}
	if (len(m.LeafChanges) == 0 && m.LocationNameUnknown == nil) || len(m.LeafChanges) > 64 {
		return fmt.Errorf("host leaf CAS requires 1..64 exact string replacements")
	}
	if c := m.LocationNameUnknown; c != nil {
		if strings.TrimSpace(c.Character) == "" || len(c.Character) > 160 || !pipelineFoundationLocationNamePointer.MatchString(c.Pointer) || !c.ExpectedMissing || c.New == nil || *c.New {
			return fmt.Errorf("location_name_unknown requires an exact character/pointer, expected_missing=true and explicit new=false")
		}
	}
	seen := map[string]bool{}
	for _, c := range m.LeafChanges {
		if strings.TrimSpace(c.Character) == "" || len(c.Character) > 160 || !pipelineFoundationLeafPointer.MatchString(c.Pointer) || seen[c.Pointer] || c.Old == c.New {
			return fmt.Errorf("host leaf CAS requires unique allowed existing string leaves, exact character names and a changed value")
		}
		seen[c.Pointer] = true
	}
	return nil
}

func decodePipelineFoundationLeafJSON(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	if err := ensurePipelineOutlineRepairJSONEOF(d); err != nil {
		return nil, err
	}
	return value, nil
}

// Compile only explicit replacements of existing string slots. No generic
// JSON patch, array insertion/deletion, unknown field or inferred value exists.
func compilePipelineFoundationLeafCAS(m pipelineFoundationRepairManifest, before []byte) ([]byte, error) {
	if m.Mode != pipelineFoundationLeafCASMode {
		return nil, fmt.Errorf("host leaf CAS requires its explicit manifest mode")
	}
	if err := validatePipelineFoundationRepairManifest(m); err != nil {
		return nil, err
	}
	if pipelineFoundationRepairSHA(before) != m.ExpectedSourceDigest {
		return nil, fmt.Errorf("host leaf CAS source SHA changed")
	}
	root, err := decodePipelineFoundationLeafJSON(before)
	if err != nil {
		return nil, err
	}
	characters, ok := root.([]any)
	if !ok {
		return nil, fmt.Errorf("host leaf CAS source is not a character array")
	}
	for _, c := range m.LeafChanges {
		parts := strings.Split(strings.TrimPrefix(c.Pointer, "/"), "/")
		index, err := strconv.Atoi(parts[0])
		if err != nil || index >= len(characters) {
			return nil, fmt.Errorf("host leaf CAS character index missing at %s", c.Pointer)
		}
		character, ok := characters[index].(map[string]any)
		if !ok || character["name"] != c.Character {
			return nil, fmt.Errorf("host leaf CAS character identity mismatch at %s", c.Pointer)
		}
		state, ok := character["initial_state"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("host leaf CAS initial_state missing at %s", c.Pointer)
		}
		if len(parts) == 3 {
			old, exists := state[parts[2]].(string)
			if !exists || old != c.Old {
				return nil, fmt.Errorf("host leaf CAS expected old string mismatch at %s", c.Pointer)
			}
			state[parts[2]] = c.New
		} else {
			items, ok := state[parts[2]].([]any)
			leaf, err := strconv.Atoi(parts[3])
			if !ok || err != nil || leaf >= len(items) {
				return nil, fmt.Errorf("host leaf CAS array slot missing at %s", c.Pointer)
			}
			old, ok := items[leaf].(string)
			if !ok || old != c.Old {
				return nil, fmt.Errorf("host leaf CAS expected old string mismatch at %s", c.Pointer)
			}
			items[leaf] = c.New
		}
	}
	if c := m.LocationNameUnknown; c != nil {
		parts := strings.Split(strings.TrimPrefix(c.Pointer, "/"), "/")
		index, err := strconv.Atoi(parts[0])
		if err != nil || index >= len(characters) {
			return nil, fmt.Errorf("location_name_unknown character index is missing")
		}
		character, ok := characters[index].(map[string]any)
		if !ok || character["name"] != c.Character {
			return nil, fmt.Errorf("location_name_unknown character identity mismatch")
		}
		state, ok := character["initial_state"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("location_name_unknown initial_state is missing")
		}
		if _, present := state["location_name_known"]; present {
			return nil, fmt.Errorf("location_name_known must be absent; existing null, true and false are not missing")
		}
		state["location_name_known"] = false
	}
	return json.Marshal(root)
}

func validatePipelineFoundationLeafChange(m pipelineFoundationRepairManifest, before, after []byte) error {
	want, err := compilePipelineFoundationLeafCAS(m, before)
	if err != nil {
		return err
	}
	expected, err := decodePipelineFoundationLeafJSON(want)
	if err != nil {
		return err
	}
	actual, err := decodePipelineFoundationLeafJSON(after)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, actual) {
		return fmt.Errorf("host leaf CAS changed a value outside its exact replacements, or typed source serialization dropped an existing field")
	}
	return nil
}

func (p *pipelineFoundationRepairPlan) preflightHostLeafCAS(live string) error {
	st := store.NewStore(live)
	if lock, err := st.Runtime.InspectPipelineExecution(); err != nil {
		return err
	} else if lock != nil {
		return fmt.Errorf("host leaf CAS refuses an active execution lock")
	}
	if err := st.ValidateRebasedChapterZeroFoundationRefresh(); err != nil {
		return fmt.Errorf("host leaf CAS requires verified chapter-zero rebase: %w", err)
	}
	for _, rel := range []string{"meta/runtime/chapter_delivery/ledger.json", "meta/planning/v2"} {
		if _, err := os.Lstat(filepath.Join(live, rel)); err == nil {
			return fmt.Errorf("host leaf CAS refuses detailed generation or budget evidence")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if entries, err := os.ReadDir(filepath.Join(pipelineRebaseRunRoot(live), ".project-all")); err != nil && !os.IsNotExist(err) {
		return err
	} else if len(entries) > 0 {
		return fmt.Errorf("host leaf CAS refuses detailed workspaces")
	}
	if err := readPipelinePlanningJSON(filepath.Join(live, "meta/pipeline.json"), &p.State); err != nil {
		return err
	}
	if strings.TrimSpace(p.State.Prompt) == "" || p.ExplicitPrompt != "" && p.ExplicitPrompt != p.State.Prompt {
		return fmt.Errorf("host leaf CAS must preserve the captured original author prompt")
	}
	var err error
	p.Source, err = os.ReadFile(filepath.Join(live, "characters.json"))
	if err != nil {
		return err
	}
	_, err = compilePipelineFoundationLeafCAS(p.Manifest, p.Source)
	return err
}

func (p *pipelineFoundationRepairPlan) saveHostLeafCAS(candidate string) error {
	patched, err := compilePipelineFoundationLeafCAS(p.Manifest, p.Source)
	if err != nil {
		return err
	}
	args, err := json.Marshal(map[string]any{"type": "characters", "content": json.RawMessage(patched)})
	if err != nil {
		return err
	}
	// The host invokes the ordinary restricted source writer, not a fabricated
	// model response. Its typed validation and real refresh checkpoint remain.
	_, err = tools.NewSaveFoundationTool(store.NewStore(candidate)).WithFoundationTypeRestriction("characters").WithFoundationRefreshEpoch(true).WithOneShotFoundationRefresh(true).WithDeferredFoundationFinalization(true).Execute(context.Background(), args)
	return err
}
