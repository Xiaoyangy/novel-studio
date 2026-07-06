package litellm

import (
	"fmt"
	"regexp"
	"sync/atomic"
	"time"
)

type MessageRepairPolicy uint

const (
	RepairNone MessageRepairPolicy = 0

	RepairNormalizeToolUseIDs MessageRepairPolicy = 1 << iota
	RepairSynthesizeMissingToolUseIDs
	RepairInsertMissingToolResults

	RepairToolUseIDs = RepairNormalizeToolUseIDs | RepairSynthesizeMissingToolUseIDs
	RepairAll        = RepairToolUseIDs | RepairInsertMissingToolResults
)

var (
	toolUseIDForbidden = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	toolUseIDSeq       atomic.Uint64
)

const maxToolUseIDLen = 64

func repairRequest(req *Request, policy MessageRepairPolicy) ([]Warning, error) {
	if req == nil || policy == RepairNone {
		return nil, nil
	}
	messages, warnings := repairMessages(req.Messages, policy)
	req.Messages = messages
	return warnings, nil
}

func repairMessages(messages []Message, policy MessageRepairPolicy) ([]Message, []Warning) {
	idMap := make(map[string]string)
	open := make(map[string]ToolUseBlock)
	var openOrder []string
	out := make([]Message, 0, len(messages)+2)
	var warnings []Warning

	flushMissing := func() {
		if len(open) == 0 {
			return
		}
		if policy&RepairInsertMissingToolResults == 0 {
			return
		}
		for _, id := range openOrder {
			if _, ok := open[id]; !ok {
				continue
			}
			out = append(out, ToolResult(id,
				Text("Tool execution was interrupted; no result available."),
			))
			block := out[len(out)-1].Blocks[0].(ToolResultBlock)
			block.IsError = true
			out[len(out)-1].Blocks[0] = block
			warnings = append(warnings, Warning{
				Code:    "message.synthetic_tool_result_inserted",
				Message: fmt.Sprintf("assistant tool use %q had no matching tool result; inserted synthetic error result", id),
			})
			delete(open, id)
		}
		openOrder = openOrder[:0]
	}

	for _, msg := range messages {
		if msg.Role == RoleUser {
			flushMissing()
		}
		msg.Blocks = repairBlocks(msg.Role, msg.Blocks, policy, idMap, open, &openOrder, &warnings)
		out = append(out, msg)
	}
	flushMissing()
	return out, warnings
}

func repairBlocks(role Role, blocks []Block, policy MessageRepairPolicy, idMap map[string]string, open map[string]ToolUseBlock, openOrder *[]string, warnings *[]Warning) []Block {
	out := make([]Block, len(blocks))
	for i, block := range blocks {
		switch b := block.(type) {
		case ToolUseBlock:
			if role == RoleAssistant {
				b = repairToolUseBlock(b, policy, idMap, warnings)
				if b.ID != "" {
					if policy&RepairInsertMissingToolResults != 0 {
						if _, exists := open[b.ID]; !exists {
							*openOrder = append(*openOrder, b.ID)
						}
					}
					open[b.ID] = b
				}
			}
			out[i] = b
		case ToolResultBlock:
			b = repairToolResultBlock(b, policy, idMap, warnings)
			if b.ToolUseID != "" {
				delete(open, b.ToolUseID)
			}
			out[i] = b
		default:
			out[i] = block
		}
	}
	return out
}

func repairToolUseBlock(block ToolUseBlock, policy MessageRepairPolicy, idMap map[string]string, warnings *[]Warning) ToolUseBlock {
	original := block.ID
	if original == "" && policy&RepairSynthesizeMissingToolUseIDs != 0 {
		block.ID = synthesizeToolUseID()
		*warnings = append(*warnings, Warning{
			Code:    "message.tool_use_id_synthesized",
			Message: fmt.Sprintf("assistant tool use %q was missing id; generated %q", block.Name, block.ID),
		})
		return block
	}
	if original == "" || policy&RepairNormalizeToolUseIDs == 0 {
		return block
	}
	normalized := NormalizeToolUseID(original)
	if normalized != original {
		block.ID = normalized
		idMap[original] = normalized
		*warnings = append(*warnings, Warning{
			Code:    "message.tool_use_id_normalized",
			Message: fmt.Sprintf("assistant tool use id %q was normalized to %q", original, normalized),
		})
	}
	return block
}

func repairToolResultBlock(block ToolResultBlock, policy MessageRepairPolicy, idMap map[string]string, warnings *[]Warning) ToolResultBlock {
	original := block.ToolUseID
	if mapped := idMap[original]; mapped != "" {
		block.ToolUseID = mapped
		*warnings = append(*warnings, Warning{
			Code:    "message.tool_use_id_normalized",
			Message: fmt.Sprintf("tool result id %q was normalized to %q", original, mapped),
		})
		return block
	}
	if original == "" || policy&RepairNormalizeToolUseIDs == 0 {
		return block
	}
	normalized := NormalizeToolUseID(original)
	if normalized != original {
		block.ToolUseID = normalized
		*warnings = append(*warnings, Warning{
			Code:    "message.tool_use_id_normalized",
			Message: fmt.Sprintf("tool result id %q was normalized to %q", original, normalized),
		})
	}
	return block
}

func NormalizeToolUseID(id string) string {
	out := toolUseIDForbidden.ReplaceAllString(id, "_")
	if len(out) > maxToolUseIDLen {
		out = out[:maxToolUseIDLen]
	}
	return out
}

func validToolUseID(id string) bool {
	return id != "" && id == NormalizeToolUseID(id)
}

func synthesizeToolUseID() string {
	return fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), toolUseIDSeq.Add(1))
}

func stampWarnings(warnings []Warning, provider string) {
	for i := range warnings {
		if warnings[i].Provider == "" {
			warnings[i].Provider = provider
		}
	}
}
