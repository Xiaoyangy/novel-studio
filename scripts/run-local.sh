#!/usr/bin/env bash
# novel-studio 本地一键运行脚本
# 用法：
#   ./scripts/run-local.sh                                  # 启动 TUI（默认）
#   ./scripts/run-local.sh tui                              # 同上
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

cmd="${1:-tui}"
shift || true

case "$cmd" in
    tui|ui)
        exec "${BIN[@]}" "$@"
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
        echo "未知命令: $cmd" >&2
        echo "用法: $0 [tui|review|rewrite|help] [args...]" >&2
        exit 1
        ;;
esac
