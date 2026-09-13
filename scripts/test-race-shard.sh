#!/usr/bin/env bash
# Partition every top-level Test/Example/Fuzz exactly once; never slice subtests.
set -euo pipefail

if [[ $# -ne 2 ]]; then
    printf 'Usage: %s {agents|store} SHARD_INDEX\n' "$0" >&2
    exit 2
fi
scope=$1
case "$scope" in
    agents) package=./internal/agents; shards=4; timeout=20m ;;
    store) package=./internal/store; shards=2; timeout=10m ;;
    *) printf 'Unsupported race shard scope: %s\n' "$scope" >&2; exit 2 ;;
esac
if [[ ! $2 =~ ^[0-3]$ ]] || (( $2 >= shards )); then
    printf '%s shard must be an integer from 0 to %s.\n' "$scope" "$((shards - 1))" >&2
    exit 2
fi
shard=$2
if ! listing=$(go test -race -list . "$package"); then
    printf 'Failed to list %s race tests; no shard was run.\n' "$scope" >&2
    exit 1
fi
names=$(printf '%s\n' "$listing" | awk '/^(Test|Example|Fuzz)[^[:space:]]*$/ { print }' | LC_ALL=C sort -u)
if [[ -z $names ]]; then
    printf 'The %s test inventory is empty; refusing an empty success.\n' "$scope" >&2
    exit 1
fi
mapfile -t all_tests <<< "$names"
pattern='^('
separator=''
selected=0
for i in "${!all_tests[@]}"; do
    if (( i % shards != shard )); then
        continue
    fi
    escaped=$(printf '%s\n' "${all_tests[i]}" | sed 's/[][\\.^$*+?(){}|]/\\&/g')
    pattern+="$separator$escaped"
    separator='|'
    selected=$((selected + 1))
done
if (( selected == 0 )); then
    printf 'No %s tests assigned to shard %s; check the inventory.\n' "$scope" "$shard" >&2
    exit 1
fi
pattern+=')$'
printf '%s race shard %s/%s: %s of %s top-level tests\n' "$scope" "$shard" "$shards" "$selected" "${#all_tests[@]}"
# The package timeout is cumulative per shard. Keep existing agents 20m and
# Store 10m limits; no slash selector means every descendant/seed still runs.
exec go test -race -count=1 "-timeout=$timeout" -run "$pattern" "$package"
