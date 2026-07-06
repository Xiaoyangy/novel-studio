package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	skillbundle "github.com/chenhongyang/novel-studio/skills"
)

func runSkillsCommand(argv []string) int {
	if len(argv) == 0 || argv[0] == "help" || argv[0] == "--help" || argv[0] == "-h" {
		printSkillsUsage(os.Stdout)
		return 0
	}

	switch argv[0] {
	case "list":
		return runSkillsList(os.Stdout)
	case "context":
		return runSkillsContext(argv[1:])
	case "export":
		return runSkillsExport(argv[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown skills command: %s\n\n", argv[0])
		printSkillsUsage(os.Stderr)
		return 2
	}
}

func runSkillsList(w io.Writer) int {
	skills, err := skillbundle.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skills list: %v\n", err)
		return 1
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPATH\tDESCRIPTION")
	for _, skill := range skills {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", skill.Name, skill.Path, oneLine(skill.Description))
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "skills list: %v\n", err)
		return 1
	}
	return 0
}

func runSkillsContext(argv []string) int {
	asJSON := false
	all := false
	withContent := false
	includeConditional := false
	stateDir := ""
	var names []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if strings.HasPrefix(arg, "--state-dir=") {
			stateDir = strings.TrimSpace(strings.TrimPrefix(arg, "--state-dir="))
			continue
		}
		switch arg {
		case "--help", "-h":
			printSkillsContextUsage(os.Stdout)
			return 0
		case "--json":
			asJSON = true
		case "--all":
			all = true
		case "--content":
			withContent = true
		case "--include-conditional":
			includeConditional = true
		case "--state-dir":
			if i+1 >= len(argv) {
				fmt.Fprintln(os.Stderr, "skills context: --state-dir requires a directory")
				return 2
			}
			i++
			stateDir = strings.TrimSpace(argv[i])
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "skills context: unknown option %s\n", arg)
				return 2
			}
			names = append(names, arg)
		}
	}
	if stateDir != "" && !withContent {
		fmt.Fprintln(os.Stderr, "skills context: --state-dir requires --content")
		return 2
	}
	if includeConditional && !withContent {
		fmt.Fprintln(os.Stderr, "skills context: --include-conditional requires --content")
		return 2
	}
	if all {
		if withContent {
			fmt.Fprintln(os.Stderr, "skills context: --content cannot be combined with --all")
			return 2
		}
		if len(names) != 0 {
			fmt.Fprintln(os.Stderr, "skills context: --all cannot be combined with a skill name")
			return 2
		}
		plans, err := skillbundle.ReadPlans()
		if err != nil {
			fmt.Fprintf(os.Stderr, "skills context: %v\n", err)
			return 1
		}
		if asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(plans); err != nil {
				fmt.Fprintf(os.Stderr, "skills context: %v\n", err)
				return 1
			}
			return 0
		}
		for i := range plans {
			if i > 0 {
				fmt.Fprintln(os.Stdout)
			}
			printSkillsContext(os.Stdout, &plans[i])
		}
		return 0
	}
	if len(names) != 1 {
		fmt.Fprintln(os.Stderr, "skills context: expected exactly one skill name")
		printSkillsContextUsage(os.Stderr)
		return 2
	}
	if withContent {
		bundle, err := skillbundle.ReadBundleWithState(names[0], includeConditional, stateDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skills context: %v\n", err)
			return 1
		}
		if asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(bundle); err != nil {
				fmt.Fprintf(os.Stderr, "skills context: %v\n", err)
				return 1
			}
			return 0
		}
		printSkillsContentBundle(os.Stdout, bundle)
		return 0
	}
	plan, err := skillbundle.ReadPlan(names[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "skills context: %v\n", err)
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(plan); err != nil {
			fmt.Fprintf(os.Stderr, "skills context: %v\n", err)
			return 1
		}
		return 0
	}
	printSkillsContext(os.Stdout, plan)
	return 0
}

func runSkillsExport(argv []string) int {
	fs := flag.NewFlagSet("skills export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	to := fs.String("to", "", "destination directory, for example .agents/skills or skills")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	dest := strings.TrimSpace(*to)
	if dest == "" && fs.NArg() > 0 {
		dest = fs.Arg(0)
	}
	if dest == "" {
		fmt.Fprintln(os.Stderr, "skills export: missing --to <dir>")
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "skills export: too many positional arguments")
		return 2
	}
	if err := skillbundle.Export(dest); err != nil {
		fmt.Fprintf(os.Stderr, "skills export: %v\n", err)
		return 1
	}
	skills, err := skillbundle.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skills export: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "exported %d skills to %s\n", len(skills), dest)
	return 0
}

func printSkillsUsage(w io.Writer) {
	fmt.Fprintln(w, "novel-studio skills — bundled skill resources")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  novel-studio skills list")
	fmt.Fprintln(w, "  novel-studio skills context <name> [--json]")
	fmt.Fprintln(w, "  novel-studio skills context <name> --content [--include-conditional] [--state-dir <dir>] [--json]")
	fmt.Fprintln(w, "  novel-studio skills context --all --json")
	fmt.Fprintln(w, "  novel-studio skills export --to .agents/skills")
	fmt.Fprintln(w, "  novel-studio skills export --to skills")
}

func printSkillsContextUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  novel-studio skills context <name> [--json]")
	fmt.Fprintln(w, "  novel-studio skills context --json <name>")
	fmt.Fprintln(w, "  novel-studio skills context <name> --content [--include-conditional] [--state-dir <dir>] [--json]")
	fmt.Fprintln(w, "  novel-studio skills context --all --json")
}

func printSkillsContext(w io.Writer, plan *skillbundle.ContextPlan) {
	fmt.Fprintf(w, "skill: %s\n", plan.Skill.Name)
	fmt.Fprintf(w, "path: %s\n", plan.Skill.Path)
	if plan.Skill.Description != "" {
		fmt.Fprintf(w, "description: %s\n", oneLine(plan.Skill.Description))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "read_order:")
	for i, path := range plan.ReadOrder {
		fmt.Fprintf(w, "  %d. %s\n", i+1, path)
	}
	if len(plan.Manifest.ConditionalFiles) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "conditional_files:")
		for _, entry := range plan.Manifest.ConditionalFiles {
			fmt.Fprintf(w, "  - when: %s\n", entry.When)
			for _, path := range entry.Paths {
				fmt.Fprintf(w, "    - %s/%s\n", plan.Skill.Path, path)
			}
		}
	}
	if len(plan.Manifest.StateFiles) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "state_files:")
		for _, path := range plan.Manifest.StateFiles {
			fmt.Fprintf(w, "  - %s\n", path)
		}
	}
	if len(plan.Manifest.OutputContract) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "output_contract:")
		for _, item := range plan.Manifest.OutputContract {
			fmt.Fprintf(w, "  - %s\n", item)
		}
	}
	if len(plan.Manifest.CompactionResume) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "compaction_resume:")
		for _, item := range plan.Manifest.CompactionResume {
			fmt.Fprintf(w, "  - %s\n", item)
		}
	}
}

func printSkillsContentBundle(w io.Writer, bundle *skillbundle.ContextBundle) {
	fmt.Fprintf(w, "skill: %s\n", bundle.Skill.Name)
	fmt.Fprintf(w, "path: %s\n", bundle.Skill.Path)
	if bundle.Skill.Description != "" {
		fmt.Fprintf(w, "description: %s\n", oneLine(bundle.Skill.Description))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "files:")
	for _, file := range bundle.Files {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "## %s\n", file.Path)
		if file.Conditional {
			fmt.Fprintf(w, "conditional: %s\n", oneLine(file.When))
		}
		if file.State {
			fmt.Fprintln(w, "state: true")
			if file.SourcePath != "" {
				fmt.Fprintf(w, "source: %s\n", file.SourcePath)
			}
		}
		fence := markdownFence(file.Content)
		fmt.Fprintf(w, "%s%s\n", fence, fenceInfo(file.Path))
		fmt.Fprint(w, file.Content)
		if !strings.HasSuffix(file.Content, "\n") {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, fence)
	}
	if len(bundle.MissingStateFiles) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "missing_state_files:")
		for _, path := range bundle.MissingStateFiles {
			fmt.Fprintf(w, "  - %s\n", path)
		}
	}
}

func fenceInfo(path string) string {
	switch {
	case strings.HasSuffix(path, ".md"):
		return "markdown"
	case strings.HasSuffix(path, ".json"):
		return "json"
	case strings.HasSuffix(path, ".py"):
		return "python"
	case strings.HasSuffix(path, ".js"):
		return "javascript"
	case strings.HasSuffix(path, ".ts"):
		return "typescript"
	case strings.HasSuffix(path, ".yaml"), strings.HasSuffix(path, ".yml"):
		return "yaml"
	case strings.HasSuffix(path, ".toml"):
		return "toml"
	case strings.HasSuffix(path, ".sh"):
		return "bash"
	default:
		return ""
	}
}

func markdownFence(content string) string {
	fence := "```"
	for strings.Contains(content, fence) {
		fence += "`"
	}
	return fence
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
