package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestDirectPipelineRenderRequiresStableExclusiveControlBeforeLoad(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	runRoot := filepath.Dir(filepath.Dir(live))
	configPath := filepath.Join(runRoot, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "provider": "ollama",
  "model": "render-exclusive-test",
  "providers": {"ollama": {"type": "openai", "base_url": "http://127.0.0.1:11434/v1"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	controlRoot := pipelineOutlineAllControlRoot(live)
	if err := os.MkdirAll(controlRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	lockFile, err := os.OpenFile(filepath.Join(controlRoot, "control.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()
	err = pipelineRender(
		cliOptions{ConfigPath: configPath, Dir: runRoot},
		pipelineFlags{},
		&domain.PipelineState{},
	)
	if err == nil || !strings.Contains(err.Error(), "control is busy") {
		t.Fatalf("direct render did not stop at stable EX: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(live, "meta", "prompt_manifest.json")); !os.IsNotExist(statErr) {
		t.Fatalf("render loaded config before stable EX: %v", statErr)
	}
}

func TestPipelineEntryRecoversRenderLiveArchivedBeforePromptManifestWrite(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	candidate, err := preparePipelineRenderCandidate(live, pipelineRenderCandidateTestFrozen())
	if err != nil {
		t.Fatal(err)
	}
	const body = "render candidate survives unified pre-load recovery\n"
	if err := os.WriteFile(filepath.Join(candidate.OutputDir, "chapters", "01.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := publishPipelineRenderCandidate(live, candidate); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(live, candidate.OutputDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(candidate.TransactionRoot, candidate.ID, "receipt.json")); err != nil {
		t.Fatal(err)
	}
	publisher := store.NewDirectoryPublishStore(candidate.TransactionRoot)
	if state, err := publisher.LoadDirectoryPublishState(candidate.ID); err != nil || state == nil || state.Phase != store.DirectoryPublishLiveArchived {
		t.Fatalf("render crash fixture state=%+v err=%v", state, err)
	}
	if _, err := os.Lstat(live); !os.IsNotExist(err) {
		t.Fatalf("render crash fixture unexpectedly retained live: %v", err)
	}

	runRoot := filepath.Dir(filepath.Dir(live))
	configPath := filepath.Join(runRoot, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "provider": "ollama",
  "model": "unified-render-recovery-test",
  "providers": {"ollama": {"type": "openai", "base_url": "http://127.0.0.1:11434/v1"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runPipelineWithStages(
		cliOptions{ConfigPath: configPath, Dir: runRoot},
		pipelineFlags{},
		[]string{},
		"",
		nil,
	); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(live, "chapters", "01.md"))
	if err != nil || string(got) != body {
		t.Fatalf("pipeline entry wrote into fake live: body=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(live, "meta", "prompt_manifest.json")); err != nil {
		t.Fatalf("prompt manifest was not written after recovery: %v", err)
	}
	if state, err := publisher.LoadDirectoryPublishState(candidate.ID); err != nil || state == nil || state.Phase != store.DirectoryPublishFinalized {
		t.Fatalf("render transaction not finalized before load: state=%+v err=%v", state, err)
	}
}

func TestRenderStartupRecoveryFinalizesReceiptWrittenPublishIdempotently(t *testing.T) {
	live := pipelineRenderCandidateTestLive(t)
	candidate, err := preparePipelineRenderCandidate(
		live,
		pipelineRenderCandidateTestFrozen(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(candidate.OutputDir, "chapters", "01.md"),
		[]byte("receipt-written startup recovery body\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	wantLive := pipelineRenderCandidateTestSnapshot(t, candidate.OutputDir)

	receipt, err := publishPipelineRenderCandidate(live, candidate)
	if err != nil {
		t.Fatal(err)
	}
	publisher := store.NewDirectoryPublishStore(candidate.TransactionRoot)
	crashed, err := publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if crashed == nil ||
		crashed.Phase != store.DirectoryPublishReceiptWritten ||
		crashed.Intent == nil ||
		crashed.Receipt == nil ||
		crashed.Receipt.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("crash window state=%+v, want durable receipt_written", crashed)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, wantLive) {
		t.Fatalf("receipt_written live differs from promoted candidate:\ngot=%v\nwant=%v", got, wantLive)
	}

	runRoot := filepath.Dir(filepath.Dir(live))
	configPath := filepath.Join(runRoot, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "provider": "ollama",
  "model": "render-startup-recovery-test",
  "providers": {
    "ollama": {
      "type": "openai",
      "base_url": "http://127.0.0.1:11434/v1"
    }
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := cliOptions{ConfigPath: configPath, Dir: runRoot}
	if err := recoverPipelineRenderPublishesBeforeLoad(opts); err != nil {
		t.Fatalf("startup recovery: %v", err)
	}

	finalized, err := publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalized == nil ||
		finalized.Phase != store.DirectoryPublishFinalized ||
		finalized.Intent != nil ||
		finalized.Receipt == nil ||
		finalized.Receipt.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("startup recovery state=%+v, want finalized with original receipt", finalized)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, wantLive) {
		t.Fatalf("startup recovery changed committed live:\ngot=%v\nwant=%v", got, wantLive)
	}
	if _, err := os.Lstat(receipt.ArchiveDir); !os.IsNotExist(err) {
		t.Fatalf("startup recovery retained rollback archive: %v", err)
	}
	if _, err := os.Lstat(candidate.OutputDir); !os.IsNotExist(err) {
		t.Fatalf("startup recovery retained candidate residue: %v", err)
	}
	firstProtocolState := pipelineRenderCandidateTestSnapshot(t, candidate.TransactionRoot)

	if err := recoverPipelineRenderPublishesBeforeLoad(opts); err != nil {
		t.Fatalf("repeated startup recovery: %v", err)
	}
	repeated, err := publisher.LoadDirectoryPublishState(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repeated == nil ||
		repeated.Phase != store.DirectoryPublishFinalized ||
		repeated.Receipt == nil ||
		repeated.Receipt.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("repeated startup recovery state=%+v, want same finalized receipt", repeated)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, live); !reflect.DeepEqual(got, wantLive) {
		t.Fatalf("repeated startup recovery changed live:\ngot=%v\nwant=%v", got, wantLive)
	}
	if got := pipelineRenderCandidateTestSnapshot(t, candidate.TransactionRoot); !reflect.DeepEqual(got, firstProtocolState) {
		t.Fatalf("repeated startup recovery rewrote protocol evidence:\ngot=%v\nwant=%v", got, firstProtocolState)
	}
}
