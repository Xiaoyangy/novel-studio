#!/usr/bin/env bash
# Reuse the same inventory/coverage/failure contracts with Store's two shards.
set -euo pipefail
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
RACE_SHARD_TEST_SCOPE=store exec bash "$script_dir/test-agents-race-shard-test.sh" "$@"
