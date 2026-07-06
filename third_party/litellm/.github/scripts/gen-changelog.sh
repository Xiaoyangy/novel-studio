#!/bin/sh
#
# Generate AI-summarized release notes from git commits.
# Usage: .github/scripts/gen-changelog.sh [previous_tag]
#
# Requires GEMINI_API_KEY (preferred), ANTHROPIC_API_KEY, or OPENAI_API_KEY.
# Falls back to raw commit list if no API key is set.
#
set -e

PREV_TAG="${1:-$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo "")}"
CURR_TAG="$(git describe --tags --abbrev=0 HEAD 2>/dev/null || echo "HEAD")"

if [ -n "$PREV_TAG" ]; then
    COMMITS=$(git log "${PREV_TAG}..${CURR_TAG}" --pretty=format:"- %s" --no-merges)
    RANGE="${PREV_TAG}..${CURR_TAG}"
else
    COMMITS=$(git log --pretty=format:"- %s" --no-merges -50)
    RANGE="last 50 commits"
fi

if [ -z "$COMMITS" ]; then
    echo "No commits found in range ${RANGE}"
    exit 0
fi

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

cat > "$TMPDIR/prompt.txt" <<PROMPT_EOF
You are a release note writer for a Go library called 'litellm' (a multi-provider LLM client for Go).
Given the following git commits, generate clean release notes in Markdown.

Rules:
- Group by: Features, Bug Fixes, Performance, Refactor, Other (skip empty groups)
- Each item: one concise line, no commit hashes, no author names
- Remove conventional commit prefixes (feat:, fix:, etc.)
- Merge related commits into one entry
- Use imperative mood (Add, Fix, Update)
- Focus on user-visible changes such as provider support, request/stream behavior, API compatibility, resilience, examples, and documentation
- Output ONLY the markdown, no intro text

Commits (${RANGE}):
${COMMITS}
PROMPT_EOF

build_body() { jq -Rs "$1" < "$TMPDIR/prompt.txt" > "$TMPDIR/body.json"; }
extract() { python3 -c "import json,sys; d=json.load(open('$TMPDIR/result.json')); print($1)"; }

fallback() {
    echo "## What's Changed"
    echo ""
    echo "$COMMITS"
}

if [ -n "$GEMINI_API_KEY" ]; then
    API_URL="${GEMINI_BASE_URL:-https://generativelanguage.googleapis.com}/v1beta/models/gemini-2.5-flash:generateContent?key=${GEMINI_API_KEY}"
    build_body '{contents: [{parts: [{text: .}]}]}'
    if curl -fsSL "$API_URL" -H "content-type: application/json" -d @"$TMPDIR/body.json" -o "$TMPDIR/result.json"; then
        extract "d['candidates'][0]['content']['parts'][0]['text']"
    else
        fallback
    fi
elif [ -n "$ANTHROPIC_API_KEY" ]; then
    API_URL="${ANTHROPIC_BASE_URL:-https://api.anthropic.com}/v1/messages"
    build_body '{model: "claude-sonnet-4-5-20250514", max_tokens: 1024, messages: [{role: "user", content: .}]}'
    if curl -fsSL "$API_URL" -H "x-api-key: ${ANTHROPIC_API_KEY}" -H "anthropic-version: 2023-06-01" -H "content-type: application/json" -d @"$TMPDIR/body.json" -o "$TMPDIR/result.json"; then
        extract "d['content'][0]['text']"
    else
        fallback
    fi
elif [ -n "$OPENAI_API_KEY" ]; then
    API_URL="${OPENAI_BASE_URL:-https://api.openai.com}/v1/chat/completions"
    build_body '{model: "gpt-4o-mini", messages: [{role: "user", content: .}]}'
    if curl -fsSL "$API_URL" -H "Authorization: Bearer ${OPENAI_API_KEY}" -H "content-type: application/json" -d @"$TMPDIR/body.json" -o "$TMPDIR/result.json"; then
        extract "d['choices'][0]['message']['content']"
    else
        fallback
    fi
else
    fallback
fi
