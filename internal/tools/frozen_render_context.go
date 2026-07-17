package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	FrozenDraftRenderContextPath    = "meta/planning/current_render_context.json"
	frozenDraftRenderContextVersion = "frozen-draft-render-context.v1"
)

// FrozenDraftRenderContext seals the exact prose-facing novel_context payload
// produced after formal-plan validation. The payload is intentionally stored
// separately from the live plan and context builders: render must not rebuild
// it from newer RAG, reviews, relationship ledgers, style assets or user rules.
type FrozenDraftRenderContext struct {
	Version       string          `json:"version"`
	Chapter       int             `json:"chapter"`
	PlanDigest    string          `json:"plan_digest"`
	PayloadSHA256 string          `json:"payload_sha256"`
	Payload       json.RawMessage `json:"payload"`
	FrozenAt      string          `json:"frozen_at"`
}

// FreezeDraftRenderContext materializes profile=draft once, while the formal
// plan is still under the planning lease, and atomically publishes a
// content-addressed envelope for the later render lease.
func FreezeDraftRenderContext(
	ctx context.Context,
	tool *ContextTool,
	chapter int,
	planDigest string,
) (*FrozenDraftRenderContext, error) {
	if tool == nil || tool.store == nil || chapter <= 0 || strings.TrimSpace(planDigest) == "" {
		return nil, fmt.Errorf("freeze draft render context requires tool, store, chapter and plan digest")
	}
	args, _ := json.Marshal(map[string]any{"chapter": chapter, "profile": "draft"})
	raw, err := tool.Execute(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("build frozen draft context: %w", err)
	}
	return PublishFrozenDraftRenderContext(tool.store, chapter, planDigest, raw)
}

// BuildDraftRenderContextPayload materializes the canonical profile=draft
// payload without publishing the singleton live envelope. Project-all uses it
// inside an isolated projected workspace and seals these exact bytes in the
// chapter bundle for later mechanical promotion.
func BuildDraftRenderContextPayload(
	ctx context.Context,
	tool *ContextTool,
	chapter int,
) (json.RawMessage, error) {
	if tool == nil || tool.store == nil || chapter <= 0 {
		return nil, fmt.Errorf("build draft render context payload requires tool, store and chapter")
	}
	args, _ := json.Marshal(map[string]any{"chapter": chapter, "profile": "draft"})
	raw, err := tool.Execute(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("build draft render context payload: %w", err)
	}
	return canonicalFrozenDraftRenderPayload(raw)
}

// PublishFrozenDraftRenderContext writes an already-built prose payload. It is
// the promotion counterpart to FreezeDraftRenderContext: no live context is
// rebuilt, so a sealed project-all bundle remains the sole inference source.
func PublishFrozenDraftRenderContext(
	st *store.Store,
	chapter int,
	planDigest string,
	raw json.RawMessage,
) (*FrozenDraftRenderContext, error) {
	if st == nil || chapter <= 0 || strings.TrimSpace(planDigest) == "" {
		return nil, fmt.Errorf("publish frozen draft render context requires store, chapter and plan digest")
	}
	payload, err := canonicalFrozenDraftRenderPayload(raw)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("parse frozen draft context payload: %w", err)
	}
	if profile, _ := decoded["_context_profile"].(string); strings.TrimSpace(profile) != "draft" {
		return nil, fmt.Errorf("frozen draft context payload profile=%q, want draft", profile)
	}
	if cp, err := CurrentChapterPlanCheckpoint(st, chapter); err != nil {
		return nil, fmt.Errorf("publish frozen draft context verify plan: %w", err)
	} else if cp.Digest != strings.TrimSpace(planDigest) {
		return nil, fmt.Errorf("publish frozen draft context plan drift: supplied=%s current=%s", planDigest, cp.Digest)
	}
	sum := sha256.Sum256(payload)
	envelope := &FrozenDraftRenderContext{
		Version:       frozenDraftRenderContextVersion,
		Chapter:       chapter,
		PlanDigest:    strings.TrimSpace(planDigest),
		PayloadSHA256: "sha256:" + hex.EncodeToString(sum[:]),
		Payload:       payload,
		FrozenAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode frozen draft context envelope: %w", err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join(st.Dir(), filepath.FromSlash(FrozenDraftRenderContextPath))
	if err := atomicWriteFrozenRenderContext(path, encoded); err != nil {
		return nil, fmt.Errorf("save frozen draft context: %w", err)
	}
	return envelope, nil
}

func canonicalFrozenDraftRenderPayload(raw json.RawMessage) (json.RawMessage, error) {
	var canonical any
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return nil, fmt.Errorf("canonicalize frozen draft context: %w", err)
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("encode frozen draft context: %w", err)
	}
	return payload, nil
}

// LoadFrozenDraftRenderContext verifies the envelope, exact plan identity and
// payload digest before returning prose context bytes.
func LoadFrozenDraftRenderContext(
	st *store.Store,
	chapter int,
	planDigest string,
) (json.RawMessage, *FrozenDraftRenderContext, error) {
	if st == nil || chapter <= 0 || strings.TrimSpace(planDigest) == "" {
		return nil, nil, fmt.Errorf("load frozen draft render context requires store, chapter and plan digest")
	}
	path := filepath.Join(st.Dir(), filepath.FromSlash(FrozenDraftRenderContextPath))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var envelope FrozenDraftRenderContext
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", FrozenDraftRenderContextPath, err)
	}
	if envelope.Version != frozenDraftRenderContextVersion ||
		envelope.Chapter != chapter ||
		strings.TrimSpace(envelope.PlanDigest) != strings.TrimSpace(planDigest) ||
		len(envelope.Payload) == 0 {
		return nil, nil, fmt.Errorf(
			"frozen draft context identity mismatch: version=%q chapter=%d plan_digest=%q",
			envelope.Version,
			envelope.Chapter,
			envelope.PlanDigest,
		)
	}
	var payload any
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, nil, fmt.Errorf("parse frozen draft payload: %w", err)
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("canonicalize frozen draft payload: %w", err)
	}
	sum := sha256.Sum256(canonical)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if strings.TrimSpace(envelope.PayloadSHA256) != actual {
		return nil, nil, fmt.Errorf(
			"frozen draft context payload drift: expected=%s actual=%s",
			envelope.PayloadSHA256,
			actual,
		)
	}
	cp, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil {
		return nil, nil, fmt.Errorf("verify frozen draft context plan: %w", err)
	}
	if cp.Digest != strings.TrimSpace(planDigest) {
		return nil, nil, fmt.Errorf(
			"frozen draft context plan drift: frozen=%s current=%s",
			planDigest,
			cp.Digest,
		)
	}
	return canonical, &envelope, nil
}

func atomicWriteFrozenRenderContext(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".render-context-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
