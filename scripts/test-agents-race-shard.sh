#!/usr/bin/env bash
# Compatibility wrapper: the original 0..3 CLI and 20m budget stay unchanged.
set -euo pipefail
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
exec bash "$script_dir/test-race-shard.sh" agents "$@"
