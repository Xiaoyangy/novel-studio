package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const CharacterToolDiagnosticDirectory = "meta/runtime/character_tool_diagnostics"
const CharacterToolDiagnosticErrorRuneLimit = 4096

// CharacterToolDiagnostic is private Host troubleshooting data, not a character
// memory, protocol receipt, model transcript or monetary usage record. It must
// not be added to public dashboard payloads or model context.
type CharacterToolDiagnostic struct {
	Version      string    `json:"version"`
	LoopID       string    `json:"loop_id"`
	Sequence     int       `json:"sequence"`
	GenerationID string    `json:"generation_id,omitempty"`
	Chapter      int       `json:"chapter,omitempty"`
	Cycle        int       `json:"cycle,omitempty"`
	Round        int       `json:"round,omitempty"`
	AgentID      string    `json:"agent_id,omitempty"`
	Role         string    `json:"role,omitempty"`
	UsageID      string    `json:"usage_id,omitempty"`
	Tool         string    `json:"tool"`
	ToolCallID   string    `json:"tool_call_id,omitempty"`
	ErrorText    string    `json:"error_text"`
	ErrorRunes   int       `json:"error_runes"`
	ErrorSHA256  string    `json:"error_sha256"`
	Truncated    bool      `json:"truncated"`
	ObservedAt   time.Time `json:"observed_at"`
}

// AppendCharacterToolDiagnostic writes only the supplied Host tool rejection
// text. The API has no arguments/message/reasoning field. Failure is for the
// caller's redacted warning only; it must not change a paid tool's result.
func (s *Store) AppendCharacterToolDiagnostic(record CharacterToolDiagnostic, errorText string) error {
	if s == nil || !validCharacterDiagnosticLoopID(record.LoopID) || record.Sequence <= 0 || record.Chapter < 0 || record.Cycle < 0 || record.Round < 0 {
		return fmt.Errorf("invalid private tool diagnostic identity")
	}
	for _, label := range []string{record.GenerationID, record.AgentID, record.Role, record.UsageID, record.Tool, record.ToolCallID} {
		if len(label) > 512 || strings.IndexFunc(label, unicode.IsControl) >= 0 {
			return fmt.Errorf("invalid private tool diagnostic label")
		}
	}
	if record.Tool == "" || !utf8.ValidString(errorText) {
		return fmt.Errorf("invalid private tool diagnostic text")
	}
	record.Version = "character-tool-diagnostic.v1"
	record.ErrorRunes = utf8.RuneCountInString(errorText)
	record.ErrorSHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(errorText)))
	record.Truncated = record.ErrorRunes > CharacterToolDiagnosticErrorRuneLimit
	record.ErrorText = errorText
	if record.Truncated {
		record.ErrorText = string([]rune(errorText)[:CharacterToolDiagnosticErrorRuneLimit])
	}
	record.ObservedAt = time.Now().UTC()
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	root := s.Dir()
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private diagnostic store root must be a real directory")
	}
	for _, part := range strings.Split(CharacterToolDiagnosticDirectory, "/") {
		root = filepath.Join(root, part)
		if err := os.Mkdir(root, 0700); err != nil && !os.IsExist(err) {
			return fmt.Errorf("create private diagnostic directory: %w", err)
		}
		info, err := os.Lstat(root)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("private diagnostic directory is not a real directory")
		}
	}
	file, err := os.OpenFile(filepath.Join(root, record.LoopID+".jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return fmt.Errorf("private diagnostic file must be regular and mode 0600")
	}
	if native, ok := info.Sys().(*syscall.Stat_t); ok && native.Nlink != 1 {
		return fmt.Errorf("private diagnostic file cannot share an inode")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	line := append(raw, '\n')
	if count, err := file.Write(line); err != nil {
		return err
	} else if count != len(line) {
		return io.ErrShortWrite
	}
	return file.Sync()
}

func validCharacterDiagnosticLoopID(id string) bool {
	if !strings.HasPrefix(id, "ctd_") || len(id) < 12 || len(id) > 96 {
		return false
	}
	for _, ch := range strings.TrimPrefix(id, "ctd_") {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '_' && ch != '-' {
			return false
		}
	}
	return true
}
