package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const chapterPipelineInstructionTokenPrefix = "chapter_pipeline_instruction:sha256:"

type chapterPipelineInstruction struct {
	Chapter       int
	Text          string
	SHA256        string
	Token         string
	Artifact      string
	ScopeArtifact string
}

// loadChapterPipelineInstruction resolves the instruction through a
// checkpointed chapter scope. New requests carry a fallback copy; the live
// meta/pipeline.json prompt wins when present so a strengthened pipeline input
// immediately gets a new source identity instead of disappearing behind an
// older rerender-request SHA.
func loadChapterPipelineInstruction(st *store.Store, chapter int) (*chapterPipelineInstruction, error) {
	if st == nil || chapter <= 0 {
		return nil, nil
	}
	cp := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "rerender-request")
	if cp == nil {
		return nil, nil
	}
	expectedArtifact := filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.rerender_request.json", chapter)))
	artifact := filepath.ToSlash(strings.TrimSpace(cp.Artifact))
	if artifact != expectedArtifact {
		return nil, fmt.Errorf("第 %d 章 rerender-request checkpoint 指向异常 artifact=%q", chapter, artifact)
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(artifact)))
	if err != nil {
		return nil, fmt.Errorf("读取第 %d 章 rerender request: %w", chapter, err)
	}
	artifactSum := sha256.Sum256(raw)
	artifactDigest := "sha256:" + hex.EncodeToString(artifactSum[:])
	if strings.TrimSpace(cp.Digest) != artifactDigest {
		return nil, fmt.Errorf("第 %d 章 rerender request 已偏离 checkpoint：want=%s got=%s", chapter, cp.Digest, artifactDigest)
	}
	var request domain.ChapterRerenderRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("解析第 %d 章 rerender request: %w", chapter, err)
	}
	if request.Chapter != chapter {
		return nil, fmt.Errorf("第 %d 章 rerender request 内部章号为 %d", chapter, request.Chapter)
	}
	requestDigest := strings.ToLower(strings.TrimSpace(request.InstructionSHA256))
	requestInstruction := strings.TrimSpace(request.Instruction)
	if requestInstruction != "" {
		requestSum := sha256.Sum256([]byte(requestInstruction))
		if actual := hex.EncodeToString(requestSum[:]); requestDigest == "" || actual != requestDigest {
			return nil, fmt.Errorf("第 %d 章 rerender request 内 instruction SHA 不匹配：want=%s got=%s", chapter, requestDigest, actual)
		}
	}
	digest := requestDigest
	instruction := ""
	instructionSource := artifact
	// meta/pipeline.json is the live pipeline input. A user may strengthen the
	// prompt after a render-only request was checkpointed; that newer prompt must
	// replace the stale request identity for every later simulation/plan/draft
	// session in this chapter. The rerender checkpoint still supplies chapter
	// scope, while the prompt-field SHA supplies exact source freshness.
	var state struct {
		Prompt string `json:"prompt"`
	}
	if stateRaw, readErr := os.ReadFile(filepath.Join(st.Dir(), "meta", "pipeline.json")); readErr == nil && json.Unmarshal(stateRaw, &state) == nil {
		if current := strings.TrimSpace(state.Prompt); current != "" {
			instruction = current
			sum := sha256.Sum256([]byte(current))
			digest = hex.EncodeToString(sum[:])
			instructionSource = "meta/pipeline.json#prompt"
		}
	}
	if instruction == "" {
		instruction = requestInstruction
	}
	if instruction == "" || digest == "" {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(instruction))
	actual := hex.EncodeToString(sum[:])
	if actual != digest {
		return nil, fmt.Errorf("第 %d 章 pipeline instruction SHA 不匹配：want=%s got=%s", chapter, digest, actual)
	}
	return &chapterPipelineInstruction{
		Chapter: chapter, Text: instruction, SHA256: digest,
		Token: chapterPipelineInstructionTokenPrefix + digest, Artifact: instructionSource, ScopeArtifact: artifact,
	}, nil
}

func (t *ContextTool) addChapterPipelineInstructionContext(result map[string]any, chapter int) error {
	if t == nil || result == nil || chapter <= 0 {
		return nil
	}
	instruction, err := loadChapterPipelineInstruction(t.store, chapter)
	if err != nil {
		return err
	}
	if instruction == nil {
		return nil
	}
	result["chapter_pipeline_instruction"] = map[string]any{
		"chapter":      instruction.Chapter,
		"instruction":  instruction.Text,
		"sha256":       instruction.SHA256,
		"source":       instruction.Artifact,
		"scope_source": instruction.ScopeArtifact,
		"source_token": instruction.Token,
		"policy":       "这是当前章节的用户硬合同，优先于旧 world simulation、旧 plan、旧正文和示例修法。world simulation 必须把 source_token 写入 sources；新 POV plan 必须继续绑定同一 token；正文不得绕开或弱化其中的先后、资源、知识边界和渲染要求。",
	}
	return nil
}

func chapterPipelineInstructionGap(st *store.Store, sim domain.ChapterWorldSimulation) string {
	instruction, err := loadChapterPipelineInstruction(st, sim.Chapter)
	if err != nil {
		return "chapter_pipeline_instruction invalid: " + err.Error()
	}
	if instruction == nil {
		return ""
	}
	for _, source := range sim.Sources {
		if strings.TrimSpace(source) == instruction.Token {
			return ""
		}
	}
	return "chapter_pipeline_instruction source missing: " + instruction.Token
}
