package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type serviceFlags struct {
	Host string
	Port int
}

func runServiceCommand(argv []string) int {
	if len(argv) == 0 || argv[0] == "help" || argv[0] == "--help" || argv[0] == "-h" {
		printServiceUsage(os.Stdout)
		return 0
	}

	switch argv[0] {
	case "start":
		return runServiceStart(argv[1:])
	case "status":
		return runServiceStatus(argv[1:])
	case "open":
		return runServiceOpen(argv[1:])
	case "url":
		return runServiceURL(argv[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown service command: %s\n\n", argv[0])
		printServiceUsage(os.Stderr)
		return 2
	}
}

func parseServiceFlags(name string, argv []string) (serviceFlags, []string, error) {
	fs := flag.NewFlagSet("service "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var f serviceFlags
	fs.StringVar(&f.Host, "host", "127.0.0.1", "service host")
	fs.IntVar(&f.Port, "port", 8765, "service port")
	if err := fs.Parse(argv); err != nil {
		return f, nil, err
	}
	return f, fs.Args(), nil
}

func runServiceStart(argv []string) int {
	flags, extra, err := parseServiceFlags("start", argv)
	if err != nil {
		return 2
	}
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "service start: too many arguments: %v\n", extra)
		return 2
	}
	if serviceHealthy(flags) {
		fmt.Fprintf(os.Stdout, "service: ok %s\n", serviceNovelURL(flags))
		return 0
	}
	if err := stopIncompatibleDashboard(flags); err != nil {
		fmt.Fprintf(os.Stderr, "service start: %v\n", err)
		return 1
	}
	script, err := findShortStoryServiceScript()
	if err != nil {
		fmt.Fprintf(os.Stderr, "service start: %v\n", err)
		return 1
	}
	args := []string{script, "--host", flags.Host, "--port", fmt.Sprint(flags.Port)}
	cmd := exec.Command("python3", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = findProjectRootFrom(script)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "service start: %v\n", err)
		return 1
	}
	return 0
}

func runServiceStatus(argv []string) int {
	flags, extra, err := parseServiceFlags("status", argv)
	if err != nil {
		return 2
	}
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "service status: too many arguments: %v\n", extra)
		return 2
	}
	if err := checkServiceHealth(flags); err != nil {
		fmt.Fprintf(os.Stderr, "service: down (%v)\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "service: ok %s\n", serviceNovelURL(flags))
	return 0
}

func serviceHealthy(flags serviceFlags) bool {
	return checkServiceHealth(flags) == nil
}

func checkServiceHealth(flags serviceFlags) error {
	client := http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/api/health", "/api/novels"} {
		resp, err := client.Get(serviceURL(flags) + path)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("%s unhealthy HTTP %s", path, resp.Status)
		}
	}
	return nil
}

func runServiceOpen(argv []string) int {
	flags, extra, err := parseServiceFlags("open", argv)
	if err != nil {
		return 2
	}
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "service open: too many arguments: %v\n", extra)
		return 2
	}
	if !serviceHealthy(flags) {
		if err := stopIncompatibleDashboard(flags); err != nil {
			fmt.Fprintf(os.Stderr, "service open: %v\n", err)
			return 1
		}
		if err := startServiceBackground(flags, ""); err != nil {
			fmt.Fprintf(os.Stderr, "service open: start dashboard: %v\n", err)
			return 1
		}
	}
	url := serviceNovelURL(flags)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "service open: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, url)
	return 0
}

func runServiceURL(argv []string) int {
	flags, extra, err := parseServiceFlags("url", argv)
	if err != nil {
		return 2
	}
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "service url: too many arguments: %v\n", extra)
		return 2
	}
	fmt.Fprintln(os.Stdout, serviceURL(flags))
	return 0
}

func serviceURL(flags serviceFlags) string {
	return fmt.Sprintf("http://%s:%d", flags.Host, flags.Port)
}

func serviceNovelURL(flags serviceFlags) string {
	return serviceURL(flags) + "/"
}

func ensureDashboardServiceForRun(outputDir string) {
	flags := serviceFlags{Host: "127.0.0.1", Port: 8765}
	if serviceHealthy(flags) {
		fmt.Fprintf(os.Stderr, "[dashboard] %s\n", serviceNovelURL(flags))
		return
	}
	if err := stopIncompatibleDashboard(flags); err != nil {
		fmt.Fprintf(os.Stderr, "[dashboard] 旧服务不可复用（创作继续）：%v\n", err)
		return
	}
	if err := startServiceBackground(flags, outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "[dashboard] 启动失败（创作继续）：%v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "[dashboard] %s\n", serviceNovelURL(flags))
}

func startServiceBackground(flags serviceFlags, novelDir string) error {
	if serviceHealthy(flags) {
		return nil
	}
	if err := stopIncompatibleDashboard(flags); err != nil {
		return err
	}
	script, err := findShortStoryServiceScript()
	if err != nil {
		return err
	}
	root := findProjectRootFrom(script)
	logDir := filepath.Join(root, "output", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, "dashboard.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	args := []string{script, "--host", flags.Host, "--port", fmt.Sprint(flags.Port)}
	cmd := exec.Command("python3", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.Dir = root
	cmd.Env = os.Environ()
	cmd.SysProcAttr = detachSysProcAttr()
	if novelDir != "" {
		if abs, err := filepath.Abs(novelDir); err == nil {
			cmd.Env = append(cmd.Env, "NOVEL_STUDIO_NOVEL_DIR="+abs)
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(logDir, "dashboard.pid"), []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644)
	_ = cmd.Process.Release()
	deadline := time.Now().Add(4 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := checkServiceHealth(flags); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("service did not become healthy: %w", lastErr)
}

func stopIncompatibleDashboard(flags serviceFlags) error {
	healthErr := checkServiceHealth(flags)
	if healthErr == nil {
		return nil
	}
	if !strings.Contains(healthErr.Error(), "/api/novels") {
		return nil
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("port %d is running an old dashboard without /api/novels; stop it or choose another --port", flags.Port)
	}
	pids, err := listeningPIDs(flags.Port)
	if err != nil || len(pids) == 0 {
		return fmt.Errorf("port %d is running an old dashboard without /api/novels; stop it or choose another --port", flags.Port)
	}
	stopped := 0
	for _, pid := range pids {
		cmdline, _ := processCommandLine(pid)
		if !strings.Contains(cmdline, "services/dashboard/server.py") {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Signal(os.Interrupt); err != nil {
			_ = proc.Kill()
		}
		stopped++
	}
	if stopped == 0 {
		return fmt.Errorf("port %d is running an old dashboard without /api/novels; stop it or choose another --port", flags.Port)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(serviceURL(flags) + "/api/health")
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("old dashboard on port %d did not stop in time", flags.Port)
}

func listeningPIDs(port int) ([]int, error) {
	out, err := exec.Command("lsof", "-nP", "-tiTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Fields(string(out))
	pids := make([]int, 0, len(lines))
	for _, line := range lines {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func processCommandLine(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	return strings.TrimSpace(string(out)), err
}

func findShortStoryServiceScript() (string, error) {
	relCandidates := []string{
		filepath.Join("services", "dashboard", "server.py"),
	}
	roots := []string{"."}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		roots = append(roots, dir, filepath.Join(dir, ".."))
	}
	var candidates []string
	for _, root := range roots {
		for _, rel := range relCandidates {
			candidates = append(candidates, filepath.Join(root, rel))
		}
	}
	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err != nil {
			continue
		}
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			return abs, nil
		}
	}
	return "", fmt.Errorf("services/dashboard/server.py not found; run from the novel-studio project root")
}

func findProjectRootFrom(path string) string {
	dir := filepath.Dir(path)
	for {
		if st, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Dir(path)
		}
		dir = parent
	}
}

func printServiceUsage(w *os.File) {
	fmt.Fprintln(w, "novel-studio service — browser progress board for novel and short-story projects")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  novel-studio service start [--host 127.0.0.1] [--port 8765]")
	fmt.Fprintln(w, "  novel-studio service status [--host 127.0.0.1] [--port 8765]")
	fmt.Fprintln(w, "  novel-studio service open [--host 127.0.0.1] [--port 8765]")
	fmt.Fprintln(w, "  novel-studio service url [--host 127.0.0.1] [--port 8765]")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Service: %s\n", filepath.Join("services", "dashboard"))
	fmt.Fprintf(w, "Novel board: %s\n", "/")
	fmt.Fprintf(w, "Short-story data root: %s\n", filepath.Join("data", "generated-output", "short_story_service", "projects"))
	fmt.Fprintf(w, "Audit scripts: %s\n", filepath.Join("quality", "audit", "scripts"))
}
