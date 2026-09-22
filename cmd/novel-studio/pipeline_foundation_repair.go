package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const pipelineFoundationRepairVersion = "chapter-zero-foundation-repair.v1"

// This is host mutation authority, never an author source or pipeline Prompt.
// It deliberately names exact leaves instead of accepting a general JSON patch.
type pipelineFoundationRepairManifest struct {
	Version              string                              `json:"version"`
	Mode                 string                              `json:"mode,omitempty"`
	Target               string                              `json:"target"`
	Instruction          string                              `json:"instruction"`
	ReportDigest         string                              `json:"report_digest"`
	ExpectedSourceDigest string                              `json:"expected_source_digest"`
	ResourceID           string                              `json:"resource_id,omitempty"`
	AllowedJSONPointers  []string                            `json:"allowed_json_pointers"`
	NewResources         []pipelineFoundationRepairResource  `json:"new_resources,omitempty"`
	RequiredAuthorRefs   []domain.AuthorSourceParagraphRefV1 `json:"required_author_refs,omitempty"`
	LeafChanges          []pipelineFoundationLeafChange      `json:"leaf_changes,omitempty"`
	LocationNameUnknown  *pipelineFoundationLocationNameCAS  `json:"location_name_unknown,omitempty"`
}

type pipelineFoundationRepairResource struct {
	CharacterName string `json:"character_name"`
	ResourceID    string `json:"resource_id"`
	Purpose       string `json:"purpose"`
	ExpectedName  string `json:"expected_name"`
	ExpectedUnit  string `json:"expected_unit"`
}

var pipelineRepairReadablePointer = regexp.MustCompile(`^/(0|[1-9][0-9]*)/initial_state/resource_balances/(0|[1-9][0-9]*)/readable_facts$`)

func loadPipelineFoundationRepairManifest(path string) (pipelineFoundationRepairManifest, string, error) {
	var m pipelineFoundationRepairManifest
	f, err := os.Open(path)
	if err != nil {
		return m, "", fmt.Errorf("read Architect repair manifest: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return m, "", err
	}
	if len(raw) > 65536 {
		return m, "", fmt.Errorf("Architect repair manifest exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, "", fmt.Errorf("decode Architect repair manifest: %w", err)
	}
	if err := ensurePipelineOutlineRepairJSONEOF(d); err != nil {
		return m, "", err
	}
	if err := validatePipelineFoundationRepairManifest(m); err != nil {
		return m, "", err
	}
	digest, err := pipelineProjectAllDigestE(m)
	return m, digest, err
}

func validatePipelineFoundationRepairManifest(m pipelineFoundationRepairManifest) error {
	if m.Version != pipelineFoundationRepairVersion || strings.TrimSpace(m.Instruction) == "" || len(m.Instruction) > 32768 {
		return fmt.Errorf("Architect repair requires its explicit version and a bounded instruction")
	}
	if m.Mode != "" {
		return validatePipelineFoundationLeafManifest(m)
	}
	if len(m.LeafChanges) != 0 || m.LocationNameUnknown != nil {
		return fmt.Errorf("leaf_changes requires its explicit host CAS mode")
	}
	for name, digest := range map[string]string{"report_digest": m.ReportDigest, "expected_source_digest": m.ExpectedSourceDigest} {
		if err := validatePipelineOutlineRepairDigest(name, digest); err != nil {
			return err
		}
	}
	if len(m.AllowedJSONPointers)+len(m.NewResources) == 0 || len(m.AllowedJSONPointers) > 16 || len(m.NewResources) > 8 {
		return fmt.Errorf("Architect repair requires 1..16 exact allowed fields")
	}
	seen := map[string]bool{}
	if len(m.RequiredAuthorRefs) > 4096 || (len(m.RequiredAuthorRefs) > 0 && m.Target != "update_compass") {
		return fmt.Errorf("required_author_refs is only valid for an explicit compass repair")
	}
	refs := map[domain.AuthorSourceParagraphRefV1]bool{}
	for _, ref := range m.RequiredAuthorRefs {
		if ref.SourceID == "" || ref.Paragraph < 0 || refs[ref] {
			return fmt.Errorf("required_author_refs must name unique nonnegative original paragraphs")
		}
		refs[ref] = true
	}
	if m.Target != "characters" && m.Target != "update_compass" {
		return fmt.Errorf("Architect repair only supports characters or update_compass")
	}
	for _, r := range m.NewResources {
		if m.Target != "characters" || strings.TrimSpace(r.CharacterName) == "" || len(r.CharacterName) > 160 || !regexp.MustCompile(`^res_[a-f0-9]{16}$`).MatchString(r.ResourceID) || seen[r.ResourceID] || (r.Purpose != "artifact_material" && r.Purpose != "writing_tool") || strings.TrimSpace(r.ExpectedName) == "" || len(r.ExpectedName) > 160 || strings.TrimSpace(r.ExpectedUnit) == "" || len(r.ExpectedUnit) > 64 {
			return fmt.Errorf("Architect repair new resource requires one bound original owner, unique resource ID, material/tool purpose and exact expected_name/expected_unit")
		}
		seen[r.ResourceID] = true
	}
	for _, p := range m.AllowedJSONPointers {
		if seen[p] {
			return fmt.Errorf("Architect repair contains duplicate allowed field %q", p)
		}
		seen[p] = true
		switch m.Target {
		case "characters":
			if strings.TrimSpace(m.ResourceID) == "" || !pipelineRepairReadablePointer.MatchString(p) {
				return fmt.Errorf("characters repair only permits the bound resource's readable_facts leaf")
			}
		case "update_compass":
			if m.ResourceID != "" || (p != "/non_negotiables" && p != "/author_contracts") {
				return fmt.Errorf("compass repair only permits non_negotiables and their author_contracts binding")
			}
		default:
			return fmt.Errorf("Architect repair only supports characters or update_compass")
		}
	}
	return nil
}

// Compare independent JSON values with exact number spellings. Removing only
// the authorized leaves preserves every ancestor, array slot, identity and
// unrelated field. The original source bytes are never modified.
func validatePipelineFoundationRepairChange(m pipelineFoundationRepairManifest, before, after []byte) error {
	if err := validatePipelineFoundationRepairManifest(m); err != nil {
		return err
	}
	if m.Mode == pipelineFoundationLeafCASMode {
		return validatePipelineFoundationLeafChange(m, before, after)
	}
	decode := func(raw []byte) (any, error) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			return nil, err
		}
		if err := ensurePipelineOutlineRepairJSONEOF(d); err != nil {
			return nil, err
		}
		return v, nil
	}
	a, err := decode(before)
	if err != nil {
		return err
	}
	b, err := decode(after)
	if err != nil {
		return err
	}
	changed := false
	if len(m.NewResources) > 0 {
		if err := removeAuthorizedRepairNewResources(m, a, b); err != nil {
			return err
		}
		changed = true
	}
	for _, p := range m.AllowedJSONPointers {
		pa, key, err := pipelineRepairPointerParent(a, p)
		if err != nil {
			return err
		}
		pb, _, err := pipelineRepairPointerParent(b, p)
		if err != nil {
			return err
		}
		if m.Target == "characters" && (pa["resource_id"] != m.ResourceID || pb["resource_id"] != m.ResourceID) {
			return fmt.Errorf("Architect repair field %s does not identify its original resource", p)
		}
		av, aok := pa[key]
		bv, bok := pb[key]
		if !bok && !(m.Target == "update_compass" && key == "non_negotiables") {
			return fmt.Errorf("Architect repair may not remove authorized field %s", p)
		}
		if aok != bok || (aok && !reflect.DeepEqual(av, bv)) {
			changed = true
		}
		delete(pa, key)
		delete(pb, key)
	}
	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("Architect repair changed a field outside its exact authorization")
	}
	if !changed {
		return fmt.Errorf("Architect repair did not change an authorized field")
	}
	return nil
}

func removeAuthorizedRepairNewResources(m pipelineFoundationRepairManifest, before, after any) error {
	a, aok := before.([]any)
	b, bok := after.([]any)
	if !aok || !bok || len(a) != len(b) {
		return fmt.Errorf("resource repair cannot add or remove a character")
	}
	originalIDs := map[string]bool{}
	for _, c := range a {
		if char, ok := c.(map[string]any); ok {
			if state, ok := char["initial_state"].(map[string]any); ok {
				if rows, ok := state["resource_balances"].([]any); ok {
					for _, r := range rows {
						if row, ok := r.(map[string]any); ok {
							if id, ok := row["resource_id"].(string); ok {
								originalIDs[id] = true
							}
						}
					}
				}
			}
		}
	}
	for _, allowed := range m.NewResources {
		if originalIDs[allowed.ResourceID] {
			return fmt.Errorf("new resource ID already exists in original foundation")
		}
		foundOwner, foundResource := 0, 0
		for i, c := range b {
			char, ok := c.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid character resource container")
			}
			state, ok := char["initial_state"].(map[string]any)
			if !ok {
				continue
			}
			rows, ok := state["resource_balances"].([]any)
			if !ok {
				continue
			}
			if char["name"] == allowed.CharacterName {
				foundOwner++
			}
			kept := make([]any, 0, len(rows))
			for _, r := range rows {
				row, ok := r.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid new resource object")
				}
				if row["resource_id"] != allowed.ResourceID {
					kept = append(kept, r)
					continue
				}
				foundResource++
				original, ok := a[i].(map[string]any)
				if !ok || original["name"] != allowed.CharacterName || char["name"] != allowed.CharacterName {
					return fmt.Errorf("new resource belongs to a different original character")
				}
				if row["name"] != allowed.ExpectedName || row["unit"] != allowed.ExpectedUnit {
					return fmt.Errorf("new resource name/unit differs from the exact operator authorization for %s", allowed.ResourceID)
				}
				amount, ok := row["actual_amount"].(json.Number)
				if !ok {
					return fmt.Errorf("new material/tool resource must declare finite positive initial stock")
				}
				value, err := amount.Float64()
				if err != nil || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
					return fmt.Errorf("new material/tool resource must declare finite positive initial stock")
				}
				unit, ok := row["unit"].(string)
				if !ok || strings.TrimSpace(unit) == "" {
					return fmt.Errorf("new material/tool resource needs its physical unit")
				}
				if facts, exists := row["readable_facts"]; exists && facts != nil {
					values, ok := facts.([]any)
					if !ok || len(values) != 0 {
						return fmt.Errorf("new material/tool cannot introduce historical readable evidence")
					}
				}
			}
			if len(kept) != len(rows) {
				state["resource_balances"] = kept
			}
		}
		if foundOwner != 1 || foundResource != 1 {
			return fmt.Errorf("new resource must occur once for its unique original owner")
		}
	}
	return nil
}

func pipelineRepairPointerParent(root any, pointer string) (map[string]any, string, error) {
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	current := root
	for _, part := range parts[:len(parts)-1] {
		switch v := current.(type) {
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil, "", fmt.Errorf("Architect repair array path does not exist: %s", pointer)
			}
			current = v[i]
		case map[string]any:
			var ok bool
			current, ok = v[part]
			if !ok {
				return nil, "", fmt.Errorf("Architect repair parent does not exist: %s", pointer)
			}
		default:
			return nil, "", fmt.Errorf("Architect repair path has a non-container parent: %s", pointer)
		}
	}
	parent, ok := current.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("Architect repair leaf parent is not an object: %s", pointer)
	}
	return parent, parts[len(parts)-1], nil
}
