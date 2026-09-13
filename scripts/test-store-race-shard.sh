#!/usr/bin/env bash
# Store's complete race inventory, split into two unchanged-10m test processes.
set -euo pipefail
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
exec bash "$script_dir/test-race-shard.sh" store "$@"
