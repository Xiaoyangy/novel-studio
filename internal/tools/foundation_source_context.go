package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

const FoundationSourceContextVersion = "foundation-source-context.v1"
const foundationSourceMaxRunes = 45000

var foundationSourcePaths = []string{
	"characters.json", "world_codex.json", "world_rules.json", "book_world.json",
	"premise.md", "layered_outline.json", "outline.json", "meta/compass.json",
}

// FoundationSourceContextTool replaces only an exact-target Architect sidecar's
// context capability. It never builds planning context, RAG or writing memory.
type FoundationSourceContextTool struct {
	store         *store.Store
	defaultSource string
}

func NewFoundationSourceContextTool(st *store.Store, target string) *FoundationSourceContextTool {
	return &FoundationSourceContextTool{store: st, defaultSource: foundationSourcePath(target)}
}

func (*FoundationSourceContextTool) Name() string  { return "novel_context" }
func (*FoundationSourceContextTool) Label() string { return "读取完整基础源文件" }
func (*FoundationSourceContextTool) Description() string {
	return "定向Architect修订专用：每次完整读取一份白名单基础源文件，默认当前修订目标。content是逐字原文件，source_sha256绑定原字节，truncated=false；不得把其他来源或写作记忆混入全量回存。需要另一基础源时另按source读取；过大或不完整会明确失败。"
}
func (*FoundationSourceContextTool) ReadOnly(json.RawMessage) bool        { return true }
func (*FoundationSourceContextTool) ConcurrencySafe(json.RawMessage) bool { return true }
func (*FoundationSourceContextTool) Schema() map[string]any {
	chapter := schema.Int("仅兼容历史定向调用的chapter=1，不读取章内容，不改变source范围")
	chapter["minimum"], chapter["maximum"] = 1, 1
	return schema.Object(
		schema.Property("source", schema.Enum("一次选择一份完整源；省略时读取当前定向修订目标", foundationSourcePaths...)),
		schema.Property("chapter", chapter),
		schema.Property("profile", schema.Enum("仅兼容历史定向调用的planning拼写，不构建规划上下文", "planning")),
	)
}

func foundationSourcePath(source string) string {
	source = strings.TrimSpace(source)
	for _, allowed := range foundationSourcePaths {
		if source == allowed {
			return allowed
		}
	}
	if source == "compass" {
		return "meta/compass.json"
	}
	switch source {
	case "characters", "world_codex", "world_rules", "book_world", "premise", "layered_outline", "outline", "update_compass":
		return foundationArtifact(source)
	}
	return ""
}

func (t *FoundationSourceContextTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Source string `json:"source"`
		// Old targeted refresh instructions used the ordinary call spelling.
		// These accepted compatibility fields never broaden this reader's scope.
		Chapter int    `json:"chapter,omitempty"`
		Profile string `json:"profile,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("foundation source context args: %w: %w", err, errs.ErrToolArgs)
	}
	if decoder.Decode(new(any)) != io.EOF || bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
		return nil, fmt.Errorf("foundation source context args must be one JSON object: %w", errs.ErrToolArgs)
	}
	if input.Chapter < 0 || input.Chapter > 1 || (input.Profile != "" && input.Profile != "planning") {
		return nil, fmt.Errorf("foundation source context is not a chapter/profile reader: %w", errs.ErrToolArgs)
	}
	source := t.defaultSource
	if strings.TrimSpace(input.Source) != "" {
		source = foundationSourcePath(input.Source)
	}
	if t.store == nil || t.defaultSource == "" || source == "" {
		return nil, fmt.Errorf("foundation source context requires a bound target and an allowed source: %w", errs.ErrToolPrecondition)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	base, err := os.Lstat(t.store.Dir())
	if err != nil || !base.IsDir() || base.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("foundation source root must be a regular directory: %w", errs.ErrToolPrecondition)
	}
	root, err := os.OpenRoot(t.store.Dir())
	if err != nil {
		return nil, err
	}
	defer root.Close()
	openedRoot, err := root.Stat(".")
	if err != nil || !os.SameFile(base, openedRoot) {
		return nil, fmt.Errorf("foundation source root changed while opening: %w", errs.ErrToolPrecondition)
	}
	leaseBefore, err := foundationSourceLease(root)
	if err != nil {
		return nil, err
	}
	raw, err := readFoundationSourceFile(root, source, foundationSourceMaxRunes*utf8.UTFMax)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || !utf8.Valid(raw) || utf8.RuneCount(raw) > foundationSourceMaxRunes {
		return nil, fmt.Errorf("foundation source %s is invalid UTF-8 or exceeds %d-rune exact packet budget; no partial content returned: %w", source, foundationSourceMaxRunes, errs.ErrToolPrecondition)
	}
	if strings.HasSuffix(source, ".json") && !json.Valid(raw) {
		return nil, fmt.Errorf("foundation source %s is not complete valid JSON; no content returned: %w", source, errs.ErrToolPrecondition)
	}
	leaseAfter, err := foundationSourceLease(root)
	if err != nil || string(leaseBefore) != string(leaseAfter) {
		return nil, fmt.Errorf("foundation source lease changed during read; retry under the current foundation lease: %w", errs.ErrToolPrecondition)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	currentRoot, err := os.Lstat(t.store.Dir())
	if err != nil || !os.SameFile(base, currentRoot) || currentRoot.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("foundation source root changed during read: %w", errs.ErrToolPrecondition)
	}
	digest := sha256.Sum256(raw)
	return json.Marshal(struct {
		Version      string `json:"version"`
		Source       string `json:"source"`
		SourceSHA256 string `json:"source_sha256"`
		Content      string `json:"content"`
		Truncated    bool   `json:"truncated"`
	}{FoundationSourceContextVersion, source, "sha256:" + hex.EncodeToString(digest[:]), string(raw), false})
}

func foundationSourceLease(root *os.Root) ([]byte, error) {
	raw, err := readFoundationSourceFile(root, "meta/runtime/pipeline_execution.json", 16384)
	if err != nil {
		return nil, fmt.Errorf("foundation source context requires an active foundation lease: %w", errs.ErrToolPrecondition)
	}
	var lease domain.PipelineExecutionLock
	if err := json.Unmarshal(raw, &lease); err != nil || lease.Version != 1 || lease.Mode != domain.PipelineExecutionFoundation || lease.TargetChapter != 1 || strings.TrimSpace(lease.Owner) == "" || lease.ProcessID != os.Getpid() || !lease.ActiveAt(time.Now().UTC()) || lease.AcquiredAt.IsZero() {
		return nil, fmt.Errorf("foundation source context requires the current process's active foundation lease: %w", errs.ErrToolPrecondition)
	}
	return raw, nil
}

// OpenRoot prevents directory escape; explicit component checks also reject
// in-root symlinks. Identity and metadata checks fail closed on a source swap.
func readFoundationSourceFile(root *os.Root, source string, limit int) ([]byte, error) {
	check := func() (os.FileInfo, error) {
		parts := strings.Split(source, "/")
		var info os.FileInfo
		for i := range parts {
			var err error
			info, err = root.Lstat(path.Join(parts[:i+1]...))
			if err != nil || info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
				return nil, fmt.Errorf("foundation source %s must be a non-symlink regular file: %w", source, errs.ErrToolPrecondition)
			}
		}
		return info, nil
	}
	before, err := check()
	if err != nil {
		return nil, err
	}
	if before.Size() > int64(limit) {
		return nil, fmt.Errorf("foundation source %s exceeds exact packet budget; no partial content returned: %w", source, errs.ErrToolPrecondition)
	}
	file, err := root.Open(source)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("foundation source %s changed while opening: %w", source, errs.ErrToolPrecondition)
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	after, err := check()
	if err != nil || len(raw) > limit || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || int64(len(raw)) != after.Size() {
		return nil, fmt.Errorf("foundation source %s changed or exceeded exact packet budget; no partial content returned: %w", source, errs.ErrToolPrecondition)
	}
	return raw, nil
}
