#!/usr/bin/env bash
# Fake-go contract tests. An optional argument checks an actual go -list output.
set -euo pipefail

if [[ ${RACE_SHARD_FAKE_GO:-0} == 1 ]]; then
    if [[ $# -eq 5 && $1 == test && $2 == -race && $3 == -list && $4 == . && $5 == ./internal/agents ]]; then
        printf '%s\n' "$RACE_SHARD_LIST"
        exit "${RACE_SHARD_LIST_STATUS:-0}"
    fi
    printf '%s\n' "$@" > "$RACE_SHARD_CAPTURE"
    exit "${RACE_SHARD_RUN_STATUS:-0}"
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
ln -s "$script_dir/test-agents-race-shard-test.sh" "$tmp/go"
sharder="$script_dir/test-agents-race-shard.sh"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

invoke() {
    local inventory=$1 shard=$2 capture=$3 list_status=${4:-0} run_status=${5:-0}
    env PATH="$tmp:$PATH" RACE_SHARD_FAKE_GO=1 RACE_SHARD_LIST="$inventory" \
        RACE_SHARD_CAPTURE="$capture" RACE_SHARD_LIST_STATUS="$list_status" \
        RACE_SHARD_RUN_STATUS="$run_status" bash "$sharder" "$shard" \
        > "$tmp/output" 2> "$tmp/error"
}

check_partition() {
    local inventory=$1 ordered shard i pattern matched expected
    local -a names args
    ordered=$(printf '%s\n' "$inventory" | awk '/^(Test|Example|Fuzz)[^[:space:]]*$/ { print }' | LC_ALL=C sort -u)
    [[ -n $ordered ]] || fail 'test fixture has no names'
    mapfile -t names <<< "$ordered"
    for shard in 0 1 2 3; do
        invoke "$inventory" "$shard" "$tmp/capture.$shard" || fail "valid shard $shard failed"
        mapfile -t args < "$tmp/capture.$shard"
        [[ ${#args[@]} -eq 7 && ${args[0]} == test && ${args[1]} == -race && \
            ${args[2]} == -count=1 && ${args[3]} == -timeout=10m && \
            ${args[4]} == -run && ${args[6]} == ./internal/agents ]] || fail 'race flags/package changed'
        pattern=${args[5]}
        [[ $pattern == '^('*')$' && $pattern != */* ]] || fail 'top-level anchors or unrestricted subtests lost'
        matched=0
        for i in "${!names[@]}"; do
            expected=0
            (( i % 4 != shard )) || expected=1
            if [[ ${names[i]} =~ $pattern ]]; then
                (( expected == 1 )) || fail "${names[i]} selected in wrong shard $shard"
                matched=$((matched + 1))
            else
                (( expected == 0 )) || fail "${names[i]} omitted from shard $shard"
            fi
            [[ ! prefix${names[i]} =~ $pattern && ! ${names[i]}suffix =~ $pattern ]] || fail 'unanchored name match'
        done
        (( matched > 0 )) || fail 'valid inventory produced an empty shard'
    done
    printf 'PASS: %s top-level names partitioned exactly once; descendants unrestricted\n' "${#names[@]}"
}

inventory=$'TestZulu\nExampleAlpha\nTestAlpha\nTestAlphabet\nFuzzSeedCorpus\nTestMeta.[x]+($)^?{2}|Back\\slash\nTestBeta\nTest中文\nBenchmarkIgnored\nok\tpackage\t0.1s\nTestAlpha'
check_partition "$inventory"

if invoke "$inventory" 0 "$tmp/unexpected-list-run" 17; then
    fail 'failed listing became success'
fi
[[ ! -e $tmp/unexpected-list-run ]] || fail 'execution continued after failed listing'

if invoke $'ok\tpackage\t0.1s\nBenchmarkOnly' 0 "$tmp/unexpected-empty-run"; then
    fail 'empty inventory became success'
fi
[[ ! -e $tmp/unexpected-empty-run ]] || fail 'empty regex was passed to go test'

if invoke TestOnly 3 "$tmp/unexpected-shard-run"; then
    fail 'empty shard became success'
fi
[[ ! -e $tmp/unexpected-shard-run ]] || fail 'empty shard ran go test'

status=0
invoke "$inventory" 0 "$tmp/failed-test-run" 0 23 || status=$?
[[ $status -eq 23 ]] || fail 'test failure exit code was swallowed'

for invalid in -1 4 invalid; do
    if invoke "$inventory" "$invalid" "$tmp/invalid-run"; then
        fail "invalid shard $invalid was accepted"
    fi
done

if [[ $# -gt 0 ]]; then
    check_partition "$(< "$1")"
fi
printf 'PASS: list/empty/run failures, regex escaping and shard bounds\n'
