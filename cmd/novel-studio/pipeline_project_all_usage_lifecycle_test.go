package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPipelineProjectAllUsageRecoveryAfterOwnerExit(t *testing.T) {
	if dir := os.Getenv("NOVEL_TEST_USAGE_EXIT_DIR"); dir != "" {
		st := store.NewStore(dir)
		meter, err := host.NewDurableUsageMeter(st)
		if err != nil {
			t.Fatal(err)
		}
		birth, _, err := pipelineUsageProcessIdentity(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		if err := meter.StartCall("interrupted-provider-call", "world_arbiter", "pg2_interrupted", os.Getpid(), birth); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // Process exits after durable start, without a response/closure.
	}
	live, shadow := projectAccountingStores(t)
	meter, err := host.NewDurableUsageMeter(live)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		role := "character"
		if i >= 3 {
			role = "world_arbiter"
		}
		id := fmt.Sprintf("fully-reported-%d", i)
		if err := meter.Record(id, role, projectUsageMessage(id, .125)); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPipelineProjectAllUsageRecoveryAfterOwnerExit$", "-test.count=1")
	cmd.Env = append(os.Environ(), "NOVEL_TEST_USAGE_EXIT_DIR="+live.Dir())
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start/crash child: %v %s", err, raw)
	}
	for i := 0; i < 2; i++ {
		a, err := newPipelineProjectAllAccounting(context.Background(), bootstrap.Config{}, store.NewStore(live.Dir()), shadow, "pg2_recovered")
		if err != nil {
			t.Fatal(err)
		}
		cost, input, output, _, _ := a.meter.Tracker().Totals()
		if cost != .625 || input != 500 || output != 50 || a.meter.Tracker().MissingAssistantUsage() != 1 || len(a.meter.PendingCalls()) != 0 {
			t.Fatalf("recovery changed known consumption or duplicated unknown: %v %d/%d missing=%d pending=%v", cost, input, output, a.meter.Tracker().MissingAssistantUsage(), a.meter.PendingCalls())
		}
		if err := a.close(); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(live.Dir(), store.UsageAuditPath))
	if err != nil {
		t.Fatal(err)
	}
	unknown := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row struct {
			ID, Kind string
			Metadata map[string]any
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row.ID == "interrupted-provider-call" && row.Kind == "record" {
			unknown++
			if row.Metadata["codex_usage_source"] != "unknown" {
				t.Fatal("orphan call was presented as measured zero")
			}
		}
	}
	if unknown != 1 || strings.Contains(string(raw), "PRIVATE-REASONING-MUST-NOT-BE-AUDITED") {
		t.Fatal("recovery duplicated unknown or stored message content")
	}
}

func TestPipelineProjectAllUsageProcessReuseAndUnverifiableIdentity(t *testing.T) {
	for _, mode := range []string{"alive", "reused", "unverifiable"} {
		t.Run(mode, func(t *testing.T) {
			live, shadow := projectAccountingStores(t)
			a, err := newPipelineProjectAllAccounting(context.Background(), bootstrap.Config{}, live, shadow, "pg2_pid")
			if err != nil {
				t.Fatal(err)
			}
			defer a.close()
			start := "original-birth"
			if mode == "unverifiable" {
				start = ""
			}
			if err := a.meter.StartCall("owner-call", "writer", "pg2_pid", os.Getpid(), start); err != nil {
				t.Fatal(err)
			}
			observe := func(int) (string, bool, error) {
				if mode == "alive" {
					return "original-birth", true, nil
				}
				return "different-birth", true, nil
			}
			err = a.recoverInterruptedCalls(observe)
			switch mode {
			case "alive":
				if err != nil || len(a.meter.PendingCalls()) != 1 || a.meter.Tracker().MissingAssistantUsage() != 0 {
					t.Fatal("live owner falsely declared lost")
				}
			case "reused":
				if err != nil || len(a.meter.PendingCalls()) != 0 || a.meter.Tracker().MissingAssistantUsage() != 1 {
					t.Fatal("reused PID hid an interrupted call")
				}
			case "unverifiable":
				if err == nil || len(a.meter.PendingCalls()) != 1 {
					t.Fatal("unknown owner identity was guessed")
				}
			}
		})
	}
}
