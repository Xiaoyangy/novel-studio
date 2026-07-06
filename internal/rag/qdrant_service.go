package rag

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type QdrantServiceConfig struct {
	URL           string
	APIKey        string
	AutoStart     bool
	BinaryPath    string
	DockerImage   string
	ContainerName string
	StorageDir    string
	Timeout       time.Duration
}

func EnsureLocalQdrant(ctx context.Context, cfg QdrantServiceConfig) error {
	cfg = defaultQdrantServiceConfig(cfg)
	client, err := NewQdrantClient(QdrantClientConfig{
		URL:        cfg.URL,
		APIKey:     cfg.APIKey,
		Collection: "novel_studio_healthcheck",
		Timeout:    2 * time.Second,
	})
	if err != nil {
		return err
	}
	if err := client.Health(ctx); err == nil {
		return nil
	}
	if !cfg.AutoStart {
		return fmt.Errorf("qdrant is not healthy at %s and auto_start=false", cfg.URL)
	}
	if bin := qdrantBinaryPath(cfg.BinaryPath); bin != "" {
		if err := os.MkdirAll(cfg.StorageDir, 0o755); err != nil {
			return fmt.Errorf("create qdrant storage dir: %w", err)
		}
		if err := startLocalQdrantBinary(bin, cfg); err != nil {
			return fmt.Errorf("start qdrant binary %s: %w", bin, err)
		}
		return waitForQdrantHealthy(ctx, client, cfg.URL, cfg.Timeout)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("qdrant is not healthy at %s and neither qdrant binary nor docker is available: %w", cfg.URL, err)
	}
	if err := os.MkdirAll(cfg.StorageDir, 0o755); err != nil {
		return fmt.Errorf("create qdrant storage dir: %w", err)
	}
	if containerExists(ctx, cfg.ContainerName) {
		if err := runDocker(ctx, "start", cfg.ContainerName); err != nil {
			return fmt.Errorf("docker start %s: %w", cfg.ContainerName, err)
		}
	} else {
		args := []string{
			"run", "-d",
			"--name", cfg.ContainerName,
			"-p", qdrantHTTPPort(cfg.URL) + ":6333",
			"-p", qdrantGRPCPort(cfg.URL) + ":6334",
			"-v", cfg.StorageDir + ":/qdrant/storage",
			cfg.DockerImage,
		}
		if err := runDocker(ctx, args...); err != nil {
			return fmt.Errorf("docker run qdrant: %w", err)
		}
	}
	return waitForQdrantHealthy(ctx, client, cfg.URL, cfg.Timeout)
}

func waitForQdrantHealthy(ctx context.Context, client *QdrantClient, rawURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.Health(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("qdrant did not become healthy at %s: %w", rawURL, lastErr)
}

func defaultQdrantServiceConfig(cfg QdrantServiceConfig) QdrantServiceConfig {
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	cfg.BinaryPath = strings.TrimSpace(cfg.BinaryPath)
	if cfg.URL == "" {
		cfg.URL = "http://127.0.0.1:6333"
	}
	if cfg.DockerImage == "" {
		cfg.DockerImage = "qdrant/qdrant:latest"
	}
	if cfg.ContainerName == "" {
		cfg.ContainerName = "novel-studio-qdrant"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.StorageDir == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			cfg.StorageDir = filepath.Join(home, ".novel-studio", "qdrant")
		} else {
			cfg.StorageDir = filepath.Join(".novel-studio", "qdrant")
		}
	}
	cfg.AutoStart = true
	return cfg
}

func qdrantBinaryPath(configured string) string {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured
		}
	}
	if path, err := exec.LookPath("qdrant"); err == nil {
		return path
	}
	return ""
}

func startLocalQdrantBinary(bin string, cfg QdrantServiceConfig) error {
	logPath := filepath.Join(cfg.StorageDir, "qdrant.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	nullFile, err := os.Open(os.DevNull)
	if err != nil {
		_ = logFile.Close()
		return err
	}
	cmd := exec.Command(bin)
	cmd.Stdin = nullFile
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(),
		"QDRANT__SERVICE__HTTP_PORT="+qdrantHTTPPort(cfg.URL),
		"QDRANT__SERVICE__GRPC_PORT="+qdrantGRPCPort(cfg.URL),
		"QDRANT__STORAGE__STORAGE_PATH="+cfg.StorageDir,
		"QDRANT__TELEMETRY_DISABLED=true",
	)
	detachQdrantCommand(cmd)
	if err := cmd.Start(); err != nil {
		_ = nullFile.Close()
		_ = logFile.Close()
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		_ = nullFile.Close()
		_ = logFile.Close()
		return err
	}
	_ = nullFile.Close()
	return logFile.Close()
}

func containerExists(ctx context.Context, name string) bool {
	cmd := exec.CommandContext(ctx, "docker", "inspect", name)
	return cmd.Run() == nil
}

func runDocker(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func qdrantHTTPPort(rawURL string) string {
	if parsed, err := url.Parse(rawURL); err == nil {
		if _, port, err := net.SplitHostPort(parsed.Host); err == nil && port != "" {
			return port
		}
	}
	return "6333"
}

func qdrantGRPCPort(rawURL string) string {
	port := qdrantHTTPPort(rawURL)
	if n, err := strconv.Atoi(port); err == nil && n > 0 {
		return strconv.Itoa(n + 1)
	}
	return "6334"
}
