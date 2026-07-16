package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
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
	mechanicalCurrent := readJSON(mechanicalRel, &mechanical)
	if mechanicalCurrent {
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

	// A user-reported high sample can be registered after an otherwise current
	// review was produced. Bind every readable, still-blocking identity to the
	// mechanical gate, checkpoint journal and unified report so a low result from
	// another identity cannot revive that review. The sampling journal is not a
	// production dependency: if it is unreadable, registration remains fail
	// closed but chapter review freshness continues on automated evidence.
	registered, registeredErr := reviewreport.LatestRegisteredExternalDetections(projectDir, chapter, result.BodySHA256)
	if registeredErr == nil {
		checkpoints := store.NewStore(projectDir).Checkpoints.All()
		for _, detection := range registered {
			if detection.NormalizedScorePercent < aigc.PassExclusivePercent {
				continue
			}
			identity := registeredExternalDetectionIdentity(detection)
			if !mechanicalCurrent || !mechanicalHasRegisteredExternalDetection(&mechanical, detection) {
				result.Issues = append(result.Issues, fmt.Sprintf(
					"%s (current registered external detection %s %.2f%% missing)",
					mechanicalRel, identity, detection.NormalizedScorePercent,
				))
			}
			if !hasRegisteredExternalDetectionCheckpoint(checkpoints, chapter, detection) {
				result.Issues = append(result.Issues, fmt.Sprintf(
					"meta/checkpoints.jsonl (current registered external detection %s not reviewed)", identity,
				))
			}
			reportNeedle := fmt.Sprintf("external_aigc_ratio｜actual=%v｜limit=<4%%｜target=%s",
				detection.NormalizedScorePercent, registeredExternalDetectionTarget(detection))
			if reportErr == nil && !strings.Contains(string(reportRaw), reportNeedle) {
				result.Issues = append(result.Issues, fmt.Sprintf(
					"%s (current registered external detection %s missing)", reportRel, identity,
				))
			}
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

func registeredExternalDetectionIdentity(detection reviewreport.RegisteredExternalDetection) string {
	detector := strings.TrimSpace(detection.Detector)
	mode := strings.TrimSpace(detection.Mode)
	if mode == "" {
		return detector
	}
	return detector + "/" + mode
}

func registeredExternalDetectionTarget(detection reviewreport.RegisteredExternalDetection) string {
	return registeredExternalDetectionIdentity(detection)
}

func mechanicalHasRegisteredExternalDetection(mechanical *reviewreport.MechanicalGatePayload, detection reviewreport.RegisteredExternalDetection) bool {
	if mechanical == nil {
		return false
	}
	wantTarget := registeredExternalDetectionTarget(detection)
	for _, violation := range mechanical.RuleViolations {
		if strings.TrimSpace(violation.Rule) != "external_aigc_ratio" ||
			!strings.EqualFold(strings.TrimSpace(violation.Target), wantTarget) {
			continue
		}
		actual, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(violation.Actual)), 64)
		if err == nil && math.Abs(actual-detection.NormalizedScorePercent) <= 0.0001 {
			return true
		}
	}
	return false
}

func hasRegisteredExternalDetectionCheckpoint(checkpoints []domain.Checkpoint, chapter int, detection reviewreport.RegisteredExternalDetection) bool {
	wantDigest := reviewreport.RegisteredExternalDetectionDigest(detection)
	wantScope := domain.ChapterScope(chapter)
	for i := len(checkpoints) - 1; i >= 0; i-- {
		checkpoint := checkpoints[i]
		if checkpoint.Scope.Matches(wantScope) &&
			checkpoint.Step == "registered-external-detection" &&
			checkpoint.Digest == wantDigest {
			return true
		}
	}
	return false
}

// currentRegisteredExternalDeliveryIssues is deliberately delivery-only. A
// user-reported high result bound to the exact current hash requires a rewrite;
// absence of a spot-check result never blocks. Missing identities can only come
// from an explicitly configured automated external gate.
func currentRegisteredExternalDeliveryIssues(projectDir string, chapter int) []string {
	chapterPath := filepath.Join(projectDir, "chapters", fmt.Sprintf("%02d.md", chapter))
	body, readErr := os.ReadFile(chapterPath)
	if readErr != nil || strings.TrimSpace(string(body)) == "" {
		return []string{fmt.Sprintf("chapters/%02d.md missing or empty", chapter)}
	}
	inspection, err := tools.InspectRegisteredExternalRetestsForBody(
		projectDir, chapter, reviewreport.BodySHA256(string(body)),
	)
	if err != nil {
		return []string{fmt.Sprintf("reviews/drafts/%02d external gate unreadable", chapter)}
	}
	if !inspection.Required || inspection.Approved {
		return nil
	}
	if len(inspection.Blocking) > 0 {
		return []string{fmt.Sprintf(
			"reviews/drafts/%02d current exact-hash external sampling result requires rewrite (%s)",
			chapter, strings.Join(inspection.Blocking, "; "),
		)}
	}
	details := make([]string, 0, len(inspection.Missing))
	if len(inspection.Missing) > 0 {
		details = append(details, "missing="+strings.Join(inspection.Missing, ","))
	}
	return []string{fmt.Sprintf(
		"reviews/drafts/%02d explicit automated external gate unresolved (required exact-payload retest: %s)",
		chapter, strings.Join(details, "; "),
	)}
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
