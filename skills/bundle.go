package skills

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	slashpath "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	auditassets "github.com/chenhongyang/novel-studio/quality/audit"
)

//go:embed CONTEXT_PROTOCOL.md novel-check novel-cocreate novel-diag novel-douban-write novel-pipeline novel-review novel-rewrite novel-simulate novel-steer novel-write novel-writing-assets review story story-deslop story-douban-long-write story-long-analyze story-long-write story-review story-setup story-short-analyze story-short-write
var skillFS embed.FS

const skillRoot = "."

// Skill describes one embedded skill.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

// ConditionalFiles describes task-gated files from a skill context manifest.
type ConditionalFiles struct {
	When  string   `json:"when"`
	Paths []string `json:"paths"`
}

// ContextManifest is the machine-readable context recovery contract for a skill.
type ContextManifest struct {
	Skill            string             `json:"skill"`
	Entrypoint       string             `json:"entrypoint"`
	AlwaysRead       []string           `json:"always_read"`
	RequiredFiles    []string           `json:"required_files"`
	ConditionalFiles []ConditionalFiles `json:"conditional_files"`
	StateFiles       []string           `json:"state_files"`
	OutputContract   []string           `json:"output_contract"`
	CompactionResume []string           `json:"compaction_resume"`
}

// ContextPlan expands a skill's context manifest into a stable read plan.
type ContextPlan struct {
	Skill        Skill           `json:"skill"`
	ProtocolPath string          `json:"protocol_path"`
	Manifest     ContextManifest `json:"manifest"`
	ReadOrder    []string        `json:"read_order"`
}

// ContextFile is one materialized file from a skill context plan.
type ContextFile struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	Conditional bool   `json:"conditional,omitempty"`
	When        string `json:"when,omitempty"`
	State       bool   `json:"state,omitempty"`
	SourcePath  string `json:"source_path,omitempty"`
}

// ContextBundle materializes the files an executing agent should read for a
// skill, optionally including task-gated conditional files.
type ContextBundle struct {
	Skill             Skill           `json:"skill"`
	ProtocolPath      string          `json:"protocol_path"`
	Manifest          ContextManifest `json:"manifest"`
	Files             []ContextFile   `json:"files"`
	MissingStateFiles []string        `json:"missing_state_files,omitempty"`
}

// List returns the embedded skill catalog.
func List() ([]Skill, error) {
	entries, err := skillFS.ReadDir(skillRoot)
	if err != nil {
		return nil, fmt.Errorf("read skills: %w", err)
	}

	skills := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := entry.Name()
		data, err := skillFS.ReadFile(skillPath + "/SKILL.md")
		if err != nil {
			continue
		}
		skill := parseSkill(entry.Name(), "skills/"+skillPath, string(data))
		skills = append(skills, skill)
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

// ReadPlan returns the context recovery read plan for an embedded skill.
func ReadPlan(name string) (*ContextPlan, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("skill name is required")
	}
	skills, err := List()
	if err != nil {
		return nil, err
	}
	for _, skill := range skills {
		dir := strings.TrimPrefix(skill.Path, "skills/")
		if name != dir && name != skill.Name {
			continue
		}
		return readPlanForSkill(skill)
	}
	return nil, fmt.Errorf("skill %q not found", name)
}

// ReadPlans returns context recovery read plans for all embedded skills.
func ReadPlans() ([]ContextPlan, error) {
	skills, err := List()
	if err != nil {
		return nil, err
	}
	plans := make([]ContextPlan, 0, len(skills))
	for _, skill := range skills {
		plan, err := readPlanForSkill(skill)
		if err != nil {
			return nil, err
		}
		plans = append(plans, *plan)
	}
	return plans, nil
}

// ReadBundleWithState returns a materialized context bundle and, when stateDir
// is provided, includes existing state files declared by the skill manifest.
// ReadBundle 读取指定 skill 的上下文包（无项目状态目录的便捷入口）。
func ReadBundle(name string, includeConditional bool) (*ContextBundle, error) {
	return ReadBundleWithState(name, includeConditional, "")
}

func ReadBundleWithState(name string, includeConditional bool, stateDir string) (*ContextBundle, error) {
	plan, err := ReadPlan(name)
	if err != nil {
		return nil, err
	}

	bundle := &ContextBundle{
		Skill:        plan.Skill,
		ProtocolPath: plan.ProtocolPath,
		Manifest:     plan.Manifest,
	}
	seen := make(map[string]bool)
	addFile := func(file ContextFile) error {
		key := file.Path
		if file.State {
			key = "state:" + key
		}
		if seen[key] {
			return nil
		}
		seen[key] = true
		bundle.Files = append(bundle.Files, file)
		return nil
	}

	for _, path := range plan.ReadOrder {
		data, err := readContextFile(path)
		if err != nil {
			return nil, err
		}
		if err := addFile(ContextFile{Path: path, Content: string(data)}); err != nil {
			return nil, err
		}
	}
	if includeConditional {
		dir := strings.TrimPrefix(plan.Skill.Path, "skills/")
		for _, entry := range plan.Manifest.ConditionalFiles {
			for _, rel := range entry.Paths {
				path := joinSkillPath(dir, rel)
				data, err := readContextFile(path)
				if err != nil {
					return nil, err
				}
				if err := addFile(ContextFile{
					Path:        path,
					Content:     string(data),
					Conditional: true,
					When:        entry.When,
				}); err != nil {
					return nil, err
				}
			}
		}
	}
	if strings.TrimSpace(stateDir) != "" {
		root, err := filepath.Abs(stateDir)
		if err != nil {
			return nil, fmt.Errorf("resolve state dir: %w", err)
		}
		for _, spec := range plan.Manifest.StateFiles {
			display, source, ok := resolveStateFile(root, spec)
			if !ok {
				continue
			}
			data, err := os.ReadFile(source)
			if os.IsNotExist(err) {
				bundle.MissingStateFiles = append(bundle.MissingStateFiles, display)
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read state file %s: %w", source, err)
			}
			if err := addFile(ContextFile{
				Path:       display,
				Content:    string(data),
				State:      true,
				SourcePath: source,
			}); err != nil {
				return nil, err
			}
		}
	}
	return bundle, nil
}

func readPlanForSkill(skill Skill) (*ContextPlan, error) {
	dir := strings.TrimPrefix(skill.Path, "skills/")
	manifestPath := dir + "/context.json"
	data, err := skillFS.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifestPath, err)
	}
	var manifest ContextManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	readOrder := []string{"skills/CONTEXT_PROTOCOL.md"}
	for _, rel := range manifest.AlwaysRead {
		readOrder = append(readOrder, joinSkillPath(dir, rel))
	}
	for _, rel := range manifest.RequiredFiles {
		readOrder = append(readOrder, joinSkillPath(dir, rel))
	}
	return &ContextPlan{
		Skill:        skill,
		ProtocolPath: "skills/CONTEXT_PROTOCOL.md",
		Manifest:     manifest,
		ReadOrder:    readOrder,
	}, nil
}

// Export copies the embedded skills into dest.
func Export(dest string) error {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return fmt.Errorf("export destination is required")
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	if err := fs.WalkDir(skillFS, skillRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == skillRoot {
			return nil
		}
		rel := path
		target := filepath.Join(root, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := skillFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, exportMode(rel)); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := auditassets.ExportReviewSupport(filepath.Join(root, "review")); err != nil {
		return fmt.Errorf("export review audit support: %w", err)
	}
	if err := auditassets.ExportTypoScan(filepath.Join(root, "scripts", "typo_scan.py")); err != nil {
		return fmt.Errorf("export legacy typo_scan support: %w", err)
	}
	return nil
}

func parseSkill(defaultName, path, raw string) Skill {
	skill := Skill{Name: defaultName, Path: path}
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return skill
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = trimFrontmatterValue(value)
		switch strings.TrimSpace(key) {
		case "name":
			if value != "" {
				skill.Name = value
			}
		case "description":
			skill.Description = value
		}
	}
	return skill
}

func joinSkillPath(dir, rel string) string {
	return slashpath.Clean("skills/" + strings.Trim(dir, "/") + "/" + strings.TrimSpace(rel))
}

func resolveStateFile(root, spec string) (string, string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.ContainsAny(spec, "{}*") {
		return "", "", false
	}
	spec = strings.TrimPrefix(spec, "<项目目录>/")
	spec = strings.TrimPrefix(spec, "<项目目录>\\")
	if strings.Contains(spec, "<") || strings.Contains(spec, ">") {
		return "", "", false
	}
	spec = filepath.FromSlash(spec)
	if filepath.IsAbs(spec) {
		return filepath.ToSlash(filepath.Clean(spec)), filepath.Clean(spec), true
	}
	clean := filepath.Clean(spec)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	return filepath.ToSlash(clean), filepath.Join(root, clean), true
}

func readContextFile(path string) ([]byte, error) {
	path = slashpath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return nil, fmt.Errorf("context path is required")
	}
	if strings.HasPrefix(path, "quality/audit/") {
		return auditassets.ReadSupportFile(path)
	}
	if strings.HasPrefix(path, "skills/") {
		data, err := skillFS.ReadFile(strings.TrimPrefix(path, "skills/"))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("unsupported context path %s", path)
}

func trimFrontmatterValue(value string) string {
	value = strings.TrimSpace(value)
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return strings.Trim(value, `"'`)
}

func exportMode(rel string) fs.FileMode {
	base := filepath.Base(rel)
	if strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".py") {
		return 0o755
	}
	return 0o644
}
