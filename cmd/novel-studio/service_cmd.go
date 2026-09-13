package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	dashboardassets "github.com/chenhongyang/novel-studio/services/dashboard"
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
	// A healthy dashboard is not enough: replace it if it is serving stale code
	// from a different checkout or an older version, so the user is never pinned
	// to an outdated board that still answers requests.
	replaceStaleDashboardIfNeeded(flags)
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
	python, err := findDashboardPython()
	if err != nil {
		fmt.Fprintf(os.Stderr, "service start: %v\n", err)
		return 1
	}
	args := []string{script, "--host", flags.Host, "--port", fmt.Sprint(flags.Port)}
	cmd := exec.Command(python, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = findProjectRootFrom(script)
	cmd.Env = dashboardEnvironment(cmd.Dir, "")
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
	replaceStaleDashboardIfNeeded(flags)
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
	replaceStaleDashboardIfNeeded(flags, outputDir)
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
	python, err := findDashboardPython()
	if err != nil {
		return err
	}
	root := findProjectRootFrom(script)
	logDir := dashboardLogDir(script, root)
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
	cmd := exec.Command(python, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.Dir = root
	cmd.Env = dashboardEnvironment(root, novelDir)
	cmd.SysProcAttr = detachSysProcAttr()
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

// replaceStaleDashboardIfNeeded stops a healthy-but-stale dashboard so the next
// health check fails and the normal start path launches the current checkout. A
// no-op when nothing is running or the running board already matches this code.
func replaceStaleDashboardIfNeeded(flags serviceFlags, novelDirs ...string) {
	if !serviceHealthy(flags) {
		return
	}
	script, err := findShortStoryServiceScript()
	if err != nil {
		return
	}
	novelDir := ""
	if len(novelDirs) > 0 {
		novelDir = novelDirs[0]
	}
	expectedRunsDir := expectedDashboardRunsDir(findProjectRootFrom(script), novelDir)
	if matches, reason := runningDashboardMatchesCheckout(flags, script, expectedRunsDir); matches {
		return
	} else {
		fmt.Fprintf(os.Stderr, "[dashboard] replacing stale board on port %d (%s)\n", flags.Port, reason)
		_ = stopDashboardProcesses(flags)
	}
}

func stopIncompatibleDashboard(flags serviceFlags) error {
	healthErr := checkServiceHealth(flags)
	if healthErr == nil {
		return nil
	}
	if !strings.Contains(healthErr.Error(), "/api/novels") {
		return nil
	}
	return stopDashboardProcesses(flags)
}

// stopDashboardProcesses interrupts the dashboard process listening on the port,
// used both for an old board that lacks /api/novels and for a stale board that
// is healthy but serving a different checkout/version.
func stopDashboardProcesses(flags serviceFlags) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("port %d has a running dashboard that must be stopped manually on Windows; stop it or choose another --port", flags.Port)
	}
	pids, err := listeningPIDs(flags.Port)
	if err != nil || len(pids) == 0 {
		return fmt.Errorf("port %d is occupied but its process could not be located; stop it or choose another --port", flags.Port)
	}
	stopped := 0
	for _, pid := range pids {
		cmdline, _ := processCommandLine(pid)
		if !isNovelStudioDashboardCommand(cmdline) {
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
		return fmt.Errorf("port %d is occupied by a non-dashboard process; stop it or choose another --port", flags.Port)
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
	return fmt.Errorf("dashboard on port %d did not stop in time", flags.Port)
}

// runningDashboardMatchesCheckout reports whether the healthy dashboard on the
// port is serving the current checkout's code. It compares the content version
// stamped by /api/health against a hash of the local server and bundled pages, so
// a stale instance from another checkout (or older code) is detected and
// replaced instead of silently pinning the user to an outdated board.
func runningDashboardMatchesCheckout(flags serviceFlags, scriptPath string, expectedRunsDirs ...string) (bool, string) {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(serviceURL(flags) + "/api/health")
	if err != nil {
		return false, "health unreachable"
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return false, "health unreadable"
	}
	var health struct {
		Version string `json:"version"`
		Script  string `json:"script"`
		RunsDir string `json:"runs_dir"`
	}
	if err := json.Unmarshal(body, &health); err != nil {
		return false, "health not JSON"
	}
	if strings.TrimSpace(health.Version) == "" {
		return false, "no version stamp (old dashboard)"
	}
	want := currentDashboardVersion(scriptPath)
	if want == "" {
		// Cannot compute a local version (missing files); avoid needless churn.
		return true, ""
	}
	if health.Version != want {
		return false, "version mismatch"
	}
	if len(expectedRunsDirs) > 0 {
		if health.RunsDir == "" {
			return false, "missing data source stamp"
		}
		got := health.RunsDir
		if !filepath.IsAbs(got) {
			got = filepath.Join(findProjectRootFrom(health.Script), got)
		}
		got, _ = filepath.Abs(got)
		expected, _ := filepath.Abs(expectedRunsDirs[0])
		if filepath.Clean(got) != filepath.Clean(expected) {
			return false, "data source mismatch"
		}
	}
	return true, ""
}

func expectedDashboardRunsDir(projectRoot, novelDir string) string {
	if value, explicit := os.LookupEnv("NOVEL_STUDIO_RUNS_DIR"); explicit {
		if !filepath.IsAbs(value) {
			value = filepath.Join(projectRoot, value)
		}
		return filepath.Clean(value)
	}
	if value := runsDirForNovelOutput(novelDir); value != "" {
		return value
	}
	return filepath.Join(projectRoot, "data", "runs")
}

// currentDashboardVersion hashes the local dashboard code identically to the
// Python server's /api/health version stamp: ordered server and static bytes.
// truncated to 16 hex chars.
func currentDashboardVersion(scriptPath string) string {
	h := sha256.New()
	staticDir := filepath.Join(filepath.Dir(scriptPath), "static")
	wrote := false
	// Match the Python health stamp and the embedded release asset order.
	for _, p := range []string{scriptPath, filepath.Join(staticDir, "index.html"), filepath.Join(staticDir, "broadcast.html"), filepath.Join(staticDir, "broadcast.css"), filepath.Join(staticDir, "broadcast.js")} {
		data, err := os.ReadFile(p)
		if err != nil {
			h.Write([]byte{0})
			continue
		}
		h.Write(data)
		wrote = true
	}
	if !wrote {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
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
	configDir := bootstrap.DefaultConfigDir()
	if configDir == "" {
		return "", fmt.Errorf("services/dashboard/server.py not found and user runtime directory is unavailable")
	}
	script, err := dashboardassets.Materialize(filepath.Join(configDir, "runtime", "dashboard"))
	if err != nil {
		return "", err
	}
	return script, nil
}

func findProjectRootFrom(path string) string {
	dir := filepath.Dir(path)
	for {
		if st, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			if cwd, err := os.Getwd(); err == nil && cwd != "" {
				return cwd
			}
			return filepath.Dir(path)
		}
		dir = parent
	}
}

func findDashboardPython() (string, error) {
	python, err := exec.LookPath("python3")
	if err != nil {
		return "", fmt.Errorf("需要 Python 3.9+ 才能启动看板；请安装 Python 3 后重试，或运行 novel-studio doctor 查看修复建议")
	}
	major, minor, err := pythonVersion(python)
	if err != nil {
		return "", fmt.Errorf("无法读取 Python 版本（%s）：%w", python, err)
	}
	if major < 3 || (major == 3 && minor < 9) {
		return "", fmt.Errorf("看板需要 Python 3.9+，当前为 %d.%d（%s）", major, minor, python)
	}
	return python, nil
}

func dashboardLogDir(script, projectRoot string) string {
	normalized := filepath.ToSlash(script)
	if strings.Contains(normalized, "/services/dashboard/server.py") {
		return filepath.Join(projectRoot, "output", "logs")
	}
	if dir := bootstrap.DefaultConfigDir(); dir != "" {
		return filepath.Join(dir, "logs")
	}
	return filepath.Join(projectRoot, "output", "logs")
}

func dashboardEnvironment(projectRoot, novelDir string) []string {
	env := os.Environ()
	if _, explicitlySet := os.LookupEnv("NOVEL_STUDIO_RUNS_DIR"); explicitlySet {
		return env
	}
	runsDir := ""
	if novelDir != "" {
		runsDir = runsDirForNovelOutput(novelDir)
	}
	if runsDir == "" && projectRoot != "" {
		runsDir = filepath.Join(projectRoot, "data", "runs")
	}
	if abs, err := filepath.Abs(runsDir); err == nil && runsDir != "" {
		env = append(env, "NOVEL_STUDIO_RUNS_DIR="+abs)
	}
	return env
}

func runsDirForNovelOutput(outputDir string) string {
	abs, err := filepath.Abs(outputDir)
	if err != nil {
		return ""
	}
	if filepath.Base(abs) == "novel" && filepath.Base(filepath.Dir(abs)) == "output" {
		return filepath.Dir(filepath.Dir(filepath.Dir(abs)))
	}
	return ""
}

func isNovelStudioDashboardCommand(commandLine string) bool {
	normalized := filepath.ToSlash(commandLine)
	if strings.Contains(normalized, "services/dashboard/server.py") {
		return true
	}
	return strings.Contains(normalized, ".novel-studio/runtime/dashboard/") &&
		strings.Contains(normalized, "/server.py")
}

func printServiceUsage(w *os.File) {
	fmt.Fprintln(w, "novel-studio service — embedded browser progress board")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  novel-studio service start [--host 127.0.0.1] [--port 8765]")
	fmt.Fprintln(w, "  novel-studio service status [--host 127.0.0.1] [--port 8765]")
	fmt.Fprintln(w, "  novel-studio service open [--host 127.0.0.1] [--port 8765]")
	fmt.Fprintln(w, "  novel-studio service url [--host 127.0.0.1] [--port 8765]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Data root: $NOVEL_STUDIO_RUNS_DIR or <workspace>/data/runs")
	fmt.Fprintln(w, "Release binaries extract the embedded dashboard into ~/.novel-studio/runtime/dashboard.")
}
