package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// promptCacheControl is intentionally short-lived: agent loops are dense while
// a chapter is planned or rendered, and a five-minute prefix cache captures the
// expensive repeated system/tools/context prefix without retaining story data
// beyond the active burst.
const promptCacheControl = "ephemeral"

// agentPromptCacheKey returns an opaque, bounded routing key. Providers use the
// key to keep one conversation on the same prefix-cache shard; it is not a cache
// identity or an authorization boundary. Hashing also prevents project paths,
// character names, and prompts from leaking into provider metadata.
func agentPromptCacheKey(role string, identity ...string) string {
	rawRole := strings.TrimSpace(role)
	role = sanitizePromptCacheRole(role)
	hash := sha256.New()
	_, _ = hash.Write([]byte(rawRole))
	for _, part := range identity {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	digest := hash.Sum(nil)
	return "novel-studio:" + role + ":" + hex.EncodeToString(digest[:8])
}

func sanitizePromptCacheRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	var b strings.Builder
	lastSeparator := false
	for _, r := range role {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
			lastSeparator = r == '-'
		} else if b.Len() > 0 && !lastSeparator {
			b.WriteByte('-')
			lastSeparator = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	clean := strings.Trim(b.String(), "-")
	if clean == "" {
		return "agent"
	}
	return clean
}
