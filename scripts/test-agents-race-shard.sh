#!/usr/bin/env bash
# Run every top-level agents Test/Example/Fuzz in exactly one of four shards.
set -euo pipefail

if [[ $# -ne 1 || ! $1 =~ ^[0-3]$ ]]; then
    printf 'Usage: %s SHARD_INDEX (0..3)\n' "$0" >&2
    exit 2
fi
shard=$1

if ! listing=$(go test -race -list . ./internal/agents); then
    printf 'Failed to list agents race tests; no shard was run.\n' >&2
    exit 1
fi
names=$(printf '%s\n' "$listing" | awk '/^(Test|Example|Fuzz)[^[:space:]]*$/ { print }' | LC_ALL=C sort -u)
if [[ -z $names ]]; then
    printf 'The agents test inventory is empty; refusing an empty success.\n' >&2
    exit 1
fi
mapfile -t all_tests <<< "$names"
pattern='^('
separator=''
selected=0
for i in "${!all_tests[@]}"; do
    if (( i % 4 != shard )); then
        continue
    fi
    # Quote RE2 metacharacters individually; anchor the complete top-level name.
    escaped=$(printf '%s\n' "${all_tests[i]}" | sed 's/[][\\.^$*+?(){}|]/\\&/g')
    pattern+="$separator$escaped"
    separator='|'
    selected=$((selected + 1))
done
if (( selected == 0 )); then
    printf 'No agents tests assigned to shard %s; check the inventory.\n' "$shard" >&2
    exit 1
fi
pattern+=')$'
printf 'agents race shard %s/4: %s of %s top-level tests\n' "$shard" "$selected" "${#all_tests[@]}"
# No slash selector: all subtests and fuzz seed cases below a selected name run.
exec go test -race -count=1 -timeout=10m -run "$pattern" ./internal/agents
