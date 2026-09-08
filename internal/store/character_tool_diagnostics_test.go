package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCharacterToolDiagnosticPrivateBoundedTextAndExactFingerprint(t *testing.T) {
	st := NewStore(t.TempDir())
	record := CharacterToolDiagnostic{LoopID: "ctd_test0123456789", Sequence: 1, GenerationID: "pg2_test", Chapter: 2, Cycle: 3, Round: 1, AgentID: "ca_private", Role: "character", UsageID: "usage-test", Tool: "submit_character_decision", ToolCallID: "tool-1"}
	message := "precise validator rejection:" + strings.Repeat("错", 4200)
	if err := st.AppendCharacterToolDiagnostic(record, message); err != nil {
		t.Fatal(err)
	}
	record.Sequence++
	if err := st.AppendCharacterToolDiagnostic(record, "second exact rejection"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), CharacterToolDiagnosticDirectory, record.LoopID+".jsonl")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private file permissions: %v %v", info, err)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil || dirInfo.Mode().Perm() != 0700 {
		t.Fatalf("private directory permissions: %v %v", dirInfo, err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var rows []CharacterToolDiagnostic
	for scanner.Scan() {
		var row CharacterToolDiagnostic
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil || len(rows) != 2 {
		t.Fatalf("diagnostic rows: %v %d", err, len(rows))
	}
	first := rows[0]
	if utf8.RuneCountInString(first.ErrorText) != CharacterToolDiagnosticErrorRuneLimit || first.ErrorRunes != utf8.RuneCountInString(message) || !first.Truncated || first.ErrorSHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(message))) || first.ObservedAt.IsZero() {
		t.Fatal("truncation was hidden or fingerprint no longer covers the original tool error")
	}
	if first.Sequence != 1 || rows[1].Sequence != 2 || rows[1].Truncated || first.AgentID != record.AgentID || first.Cycle != 3 || first.UsageID != record.UsageID {
		t.Fatal("private scope/sequence changed")
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "meta/usage.json")); !os.IsNotExist(err) {
		t.Fatal("diagnostic created a monetary ledger")
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "meta/character_agents")); !os.IsNotExist(err) {
		t.Fatal("diagnostic entered character evidence or memory")
	}
}

func TestCharacterToolDiagnosticRejectsEscapesLinksAndPublicFiles(t *testing.T) {
	for _, kind := range []string{"loop-path", "directory-link", "file-link", "hardlink", "public-file"} {
		t.Run(kind, func(t *testing.T) {
			st := NewStore(t.TempDir())
			record := CharacterToolDiagnostic{LoopID: "ctd_security0123456789", Sequence: 1, Tool: "submit_character_decision"}
			root := filepath.Join(st.Dir(), CharacterToolDiagnosticDirectory)
			if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "private-existing")
			if err := os.WriteFile(outside, []byte("UNCHANGED"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "directory-link" {
				if err := os.Symlink(filepath.Dir(outside), root); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, record.LoopID+".jsonl")
				switch kind {
				case "loop-path":
					record.LoopID = "ctd_../../escaped"
				case "file-link":
					if err := os.Symlink(outside, path); err != nil {
						t.Fatal(err)
					}
				case "hardlink":
					if err := os.Link(outside, path); err != nil {
						t.Fatal(err)
					}
				case "public-file":
					if err := os.WriteFile(path, []byte("UNCHANGED"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := st.AppendCharacterToolDiagnostic(record, "PRIVATE_VALIDATOR_TEXT"); err == nil {
				t.Fatal("unsafe diagnostic target was written")
			}
			if raw, err := os.ReadFile(outside); err != nil || string(raw) != "UNCHANGED" {
				t.Fatal("outside file changed")
			}
		})
	}
}

func TestCharacterToolDiagnosticBusyLockReturnsWithoutWaiting(t *testing.T) {
	st := NewStore(t.TempDir())
	record := CharacterToolDiagnostic{LoopID: "ctd_busy0123456789", Sequence: 1, Tool: "submit_character_decision"}
	if err := st.AppendCharacterToolDiagnostic(record, "first rejection"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), CharacterToolDiagnosticDirectory, record.LoopID+".jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	record.Sequence = 2
	done := make(chan error, 1)
	go func() { done <- st.AppendCharacterToolDiagnostic(record, "must not wait or append") }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("diagnostic ignored another owner's lock")
		}
	case <-time.After(time.Second):
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		<-done
		t.Fatal("non-authoritative diagnostic waited for its file lock")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("contended diagnostic changed an existing record")
	}
}
