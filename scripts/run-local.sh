#!/usr/bin/env bash
# novel-studio 本地一键运行脚本
# 用法：
#   ./scripts/run-local.sh                                  # 首次配置或打印帮助
#   ./scripts/run-local.sh doctor                           # 本地前置检查
#   ./scripts/run-local.sh check                            # 真实模型连通性检查
#   ./scripts/run-local.sh pipeline --new-novel --prompt ...
#   ./scripts/run-local.sh service open
#   ./scripts/run-local.sh review [--from N --to M]
#   ./scripts/run-local.sh rewrite [--from N --to M]
#   ./scripts/run-local.sh help
#
# 设计：保留 cwd 在 novel-studio 项目根，保证 output/、./.novel-studio/、go.mod 等相对路径都对。

set -euo pipefail

# 解析脚本所在目录的根项目目录（scripts 的父目录）。
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# 用 go run 跑，永远跟源码同步，不需要预编译二进制。
# PATH 里的 novel-studio 是旧 release 二进制，可能跟当前源码不同步。
BIN=(go run ./cmd/novel-studio)

if [ "$#" -eq 0 ]; then
    exec "${BIN[@]}"
fi

cmd="$1"
shift

case "$cmd" in
    tui|ui|setup)
        exec "${BIN[@]}" "$@"
        ;;
    doctor)
        exec "${BIN[@]}" doctor "$@"
        ;;
    check)
        exec "${BIN[@]}" --check "$@"
        ;;
    pipeline)
        exec "${BIN[@]}" --pipeline "$@"
        ;;
    service)
        exec "${BIN[@]}" service "$@"
        ;;
    review)
        exec "${BIN[@]}" --review-existing "$@"
        ;;
    rewrite)
        exec "${BIN[@]}" --rewrite-existing "$@"
        ;;
    help|-h|--help)
        "${BIN[@]}" --help
        ;;
    *)
		# 其它参数原样透传，确保脚本不会落后于 CLI 新增的命令/flag。
		exec "${BIN[@]}" "$cmd" "$@"
        ;;
esac
