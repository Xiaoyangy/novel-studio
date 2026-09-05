package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	buildversion "github.com/chenhongyang/novel-studio/internal/version"
	dashboardassets "github.com/chenhongyang/novel-studio/services/dashboard"
)

type doctorOptions struct {
	ConfigPath string
	Dir        string
	JSON       bool
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type doctorReport struct {
	OK       bool          `json:"ok"`
	Version  string        `json:"version"`
	Platform string        `json:"platform"`
	Checks   []doctorCheck `json:"checks"`
}

func runDoctorCommand(argv []string, info buildversion.Info) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts doctorOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "config file override")
	fs.StringVar(&opts.Dir, "dir", "", "workspace or project directory")
	fs.BoolVar(&opts.JSON, "json", false, "print machine-readable JSON")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: novel-studio doctor [--config path] [--dir path] [--json]")
		fmt.Fprintln(fs.Output(), "Checks the local runtime without calling a model or changing project data.")
	}
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "doctor: too many arguments: %v\n", fs.Args())
		return 2
	}

	report := buildDoctorReport(opts, info)
	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "doctor: encode report: %v\n", err)
			return 1
		}
	} else {
		printDoctorReport(report)
	}
	if !report.OK {
		return 1
	}
	return 0
}

func buildDoctorReport(opts doctorOptions, info buildversion.Info) doctorReport {
	info = buildversion.Resolve(info)
	report := doctorReport{
		OK:       true,
		Version:  info.Version,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	add := func(name, status, detail, fix string) {
		report.Checks = append(report.Checks, doctorCheck{Name: name, Status: status, Detail: detail, Fix: fix})
		if status == "fail" {
			report.OK = false
		}
	}

	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		add("platform", "pass", report.Platform, "")
	} else {
		add("platform", "fail", report.Platform+" is not supported for production locking", "Use WSL2 on Windows or run on macOS/Linux.")
	}
	add("version", "pass", info.Version+" (commit "+shortCommit(info.Commit)+")", "")

	workspace := strings.TrimSpace(opts.Dir)
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	if err := checkWritableDirectory(workspace); err != nil {
		add("workspace", "fail", fmt.Sprintf("%s: %v", workspace, err), "Choose an existing writable directory and pass it with --dir.")
	} else {
		add("workspace", "pass", workspace, "")
	}

	var cfg bootstrap.Config
	configured := !bootstrap.NeedsSetup(opts.ConfigPath)
	if !configured {
		path := opts.ConfigPath
		if path == "" {
			path = bootstrap.DefaultConfigPath()
		}
		add("config", "fail", "no configuration found at "+path, "Run novel-studio once for guided setup, then run novel-studio --check.")
	} else {
		loaded, err := bootstrap.LoadConfig(opts.ConfigPath)
		if err != nil {
			add("config", "fail", err.Error(), "Fix the reported JSON file or pass a valid file with --config.")
		} else if err := loaded.ValidateBase(); err != nil {
			add("config", "fail", err.Error(), "Edit the effective config, then rerun novel-studio doctor.")
		} else {
			cfg = loaded
			add("config", "pass", fmt.Sprintf("provider=%s model=%s", cfg.Provider, cfg.ModelName), "")
		}
	}

	if python, err := findDashboardPython(); err != nil {
		add("dashboard", "warn", err.Error(), "Install Python 3.9+; the writing CLI remains usable without the dashboard.")
	} else if major, minor, err := pythonVersion(python); err != nil {
		add("dashboard", "warn", fmt.Sprintf("cannot read %s version: %v", python, err), "Install Python 3.9+.")
	} else if major < 3 || (major == 3 && minor < 9) {
		add("dashboard", "warn", fmt.Sprintf("%s is Python %d.%d; 3.9+ required", python, major, minor), "Upgrade Python before using service open.")
	} else if dashboardassets.Version() == "unavailable" {
		add("dashboard", "fail", "embedded dashboard assets are unavailable", "Reinstall novel-studio from an official release archive.")
	} else {
		add("dashboard", "pass", fmt.Sprintf("Python %d.%d; embedded assets %s", major, minor, dashboardassets.Version()), "")
	}

	if cfg.RAG.Qdrant.Enabled {
		if cfg.RAG.Qdrant.BinaryPath != "" {
			if _, err := os.Stat(cfg.RAG.Qdrant.BinaryPath); err != nil {
				add("qdrant", "warn", "configured binary is unavailable: "+err.Error(), "Fix rag.qdrant.binary_path or install Docker.")
			} else {
				add("qdrant", "pass", "configured binary "+cfg.RAG.Qdrant.BinaryPath, "")
			}
		} else if path, err := exec.LookPath("qdrant"); err == nil {
			add("qdrant", "pass", "local binary "+path, "")
		} else if path, err := exec.LookPath("docker"); err == nil {
			add("qdrant", "pass", "Docker fallback "+path, "")
		} else {
			add("qdrant", "warn", "enabled but neither qdrant nor docker is on PATH", "Install Docker or set rag.qdrant.binary_path.")
		}
	} else {
		add("qdrant", "skip", "optional vector store is disabled", "")
	}

	if _, err := os.Stat(filepath.Join(workspace, "go.mod")); err == nil {
		if path, err := exec.LookPath("go"); err != nil {
			add("source-build", "fail", "go.mod found but Go is not on PATH", "Install a supported Go toolchain, then rerun doctor.")
		} else if major, minor, patch, err := goVersion(path); err != nil {
			add("source-build", "warn", fmt.Sprintf("cannot read Go version from %s: %v", path, err), "Install Go 1.25.5 or newer.")
		} else if versionLess(major, minor, patch, 1, 25, 5) {
			add("source-build", "fail", fmt.Sprintf("Go %d.%d.%d is too old (%s)", major, minor, patch, path), "Install Go 1.25.5 or newer; current stable is Go 1.27.1.")
		} else {
			add("source-build", "pass", fmt.Sprintf("Go %d.%d.%d (%s)", major, minor, patch, path), "")
		}
	} else {
		add("source-build", "skip", "release installation does not require Go", "")
	}

	return report
}

func printDoctorReport(report doctorReport) {
	fmt.Printf("novel-studio doctor %s (%s)\n\n", report.Version, report.Platform)
	icons := map[string]string{"pass": "✓", "warn": "!", "fail": "✗", "skip": "-"}
	for _, check := range report.Checks {
		fmt.Printf("%s %-12s %s\n", icons[check.Status], check.Name, check.Detail)
		if check.Fix != "" {
			fmt.Printf("  修复：%s\n", check.Fix)
		}
	}
	if report.OK {
		fmt.Println("\n结果：本地运行前置条件已就绪。下一步运行 novel-studio --check 验证真实模型连接。")
	} else {
		fmt.Println("\n结果：存在必须修复的项目；按上方建议处理后重新运行 doctor。")
	}
}

func shortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 12 {
		return commit[:12]
	}
	if commit == "" {
		return "unknown"
	}
	return commit
}

func checkWritableDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	f, err := os.CreateTemp(path, ".novel-studio-doctor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func pythonVersion(python string) (int, int, error) {
	out, err := exec.Command(python, "-c", "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')").Output()
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Split(strings.TrimSpace(string(out)), ".")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected version %q", strings.TrimSpace(string(out)))
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return major, minor, nil
}

func goVersion(goBinary string) (int, int, int, error) {
	out, err := exec.Command(goBinary, "version").Output()
	if err != nil {
		return 0, 0, 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "go") {
		return 0, 0, 0, fmt.Errorf("unexpected output %q", strings.TrimSpace(string(out)))
	}
	parts := strings.Split(strings.TrimPrefix(fields[2], "go"), ".")
	if len(parts) < 2 {
		return 0, 0, 0, fmt.Errorf("unexpected version %q", fields[2])
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, err
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, err
	}
	patch := 0
	if len(parts) > 2 {
		patchText := parts[2]
		for i, r := range patchText {
			if r < '0' || r > '9' {
				patchText = patchText[:i]
				break
			}
		}
		if patchText != "" {
			patch, err = strconv.Atoi(patchText)
			if err != nil {
				return 0, 0, 0, err
			}
		}
	}
	return major, minor, patch, nil
}

func versionLess(major, minor, patch, wantMajor, wantMinor, wantPatch int) bool {
	if major != wantMajor {
		return major < wantMajor
	}
	if minor != wantMinor {
		return minor < wantMinor
	}
	return patch < wantPatch
}
