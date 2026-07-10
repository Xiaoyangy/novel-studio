package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
)

type currentChapterReviewEvidence struct {
	Chapter     int
	BodySHA256  string
	Verdict     string
	Disposition string
	Artifacts   []string
	Issues      []string
}

// inspectCurrentChapterReview verifies that every durable review component was
// produced for the current chapter bytes. Existence alone is not completion:
// direct edits and interrupted rewrites can leave a complete-looking stale set.
func inspectCurrentChapterReview(projectDir string, chapter int) currentChapterReviewEvidence {
	result := currentChapterReviewEvidence{Chapter: chapter}
	chapterRel := fmt.Sprintf("chapters/%02d.md", chapter)
	body, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(chapterRel)))
	if err != nil || strings.TrimSpace(string(body)) == "" {
		result.Issues = append(result.Issues, chapterRel+" (missing or empty)")
		return result
	}
	result.BodySHA256 = reviewreport.BodySHA256(string(body))

	readJSON := func(rel string, dst any) bool {
		raw, readErr := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(rel)))
		if readErr != nil || len(strings.TrimSpace(string(raw))) == 0 {
			result.Issues = append(result.Issues, rel+" (missing or empty)")
			return false
		}
		if unmarshalErr := json.Unmarshal(raw, dst); unmarshalErr != nil {
			result.Issues = append(result.Issues, rel+" (invalid JSON)")
			return false
		}
		result.Artifacts = append(result.Artifacts, rel)
		return true
	}
	checkIdentity := func(rel string, artifactChapter int, bodyHash string) {
		if artifactChapter != chapter {
			result.Issues = append(result.Issues, fmt.Sprintf("%s (chapter=%d, want %d)", rel, artifactChapter, chapter))
		}
		if strings.TrimSpace(bodyHash) == "" {
			result.Issues = append(result.Issues, rel+" (body_sha256 missing)")
		} else if bodyHash != result.BodySHA256 {
			result.Issues = append(result.Issues, rel+" (body_sha256 stale)")
		}
	}

	mechanicalRel := fmt.Sprintf("reviews/%02d_ai_gate.json", chapter)
	var mechanical reviewreport.MechanicalGatePayload
	if readJSON(mechanicalRel, &mechanical) {
		checkIdentity(mechanicalRel, mechanical.Chapter, mechanical.BodySHA256)
	}

	voiceRel := fmt.Sprintf("reviews/%02d_ai_voice_redflags.json", chapter)
	var voice domain.AIVoiceAnalysis
	if readJSON(voiceRel, &voice) {
		checkIdentity(voiceRel, voice.Chapter, voice.BodySHA256)
	}

	editorRel := fmt.Sprintf("reviews/%02d.json", chapter)
	var editor domain.ReviewEntry
	if readJSON(editorRel, &editor) {
		checkIdentity(editorRel, editor.Chapter, editor.BodySHA256)
		if editor.Scope != "chapter" {
			result.Issues = append(result.Issues, editorRel+" (scope is not chapter)")
		}
		result.Verdict = editor.Verdict
	}

	judgeRel := fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", chapter)
	var judge deepseekAIJudgeArtifact
	if readJSON(judgeRel, &judge) {
		checkIdentity(judgeRel, judge.Chapter, judge.BodySHA256)
		if !judge.RawBodyOnly || judge.UserPayloadKind != "chapter_body_only" {
			result.Issues = append(result.Issues, judgeRel+" (not a raw-body-only judgment)")
		}
	}

	reportRel := fmt.Sprintf("reviews/%02d.md", chapter)
	reportRaw, reportErr := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(reportRel)))
	if reportErr != nil || len(strings.TrimSpace(string(reportRaw))) == 0 {
		result.Issues = append(result.Issues, reportRel+" (missing or empty)")
	} else {
		result.Artifacts = append(result.Artifacts, reportRel)
		if !strings.Contains(string(reportRaw), "sha256="+result.BodySHA256) {
			result.Issues = append(result.Issues, reportRel+" (current body fingerprint missing)")
		}
	}
	if len(result.Issues) == 0 {
		result.Disposition = reviewreport.RewriteDisposition(
			&mechanical,
			&voice,
			deepSeekExternalAIJudge(&judge),
			&editor,
		)
		if result.Disposition == "是" && editor.Verdict != "rewrite" {
			result.Issues = append(result.Issues, editorRel+" (verdict contradicts blocking unified review)")
		}
	}

	return result
}

func inspectReviewSummaryCurrent(projectDir string, chapters []int, hashes map[int]string) (string, []string) {
	rel := "meta/review-summary.md"
	raw, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(rel)))
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return "", []string{rel + " (missing or empty)"}
	}
	body := string(raw)
	var issues []string
	for _, chapter := range chapters {
		if !strings.Contains(body, fmt.Sprintf("**ch%02d**", chapter)) {
			issues = append(issues, fmt.Sprintf("%s (ch%02d row missing)", rel, chapter))
			continue
		}
		if hash := hashes[chapter]; hash == "" || !strings.Contains(body, "body_sha256="+hash) {
			issues = append(issues, fmt.Sprintf("%s (ch%02d current body fingerprint missing)", rel, chapter))
		}
	}
	return rel, issues
}

func currentChapterReviewError(projectDir string, chapter int) error {
	evidence := inspectCurrentChapterReview(projectDir, chapter)
	if len(evidence.Issues) == 0 {
		return nil
	}
	return fmt.Errorf("第 %d 章审核产物不是当前正文版本：%s", chapter, strings.Join(evidence.Issues, ", "))
}
