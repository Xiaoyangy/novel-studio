#!/usr/bin/env bash
# novel-studio 本地一键运行脚本
# 用法：
#   ./scripts/run-local.sh                                  # 启动 TUI（默认）
#   ./scripts/run-local.sh tui                              # 同上
#   ./scripts/run-local.sh import <novel.md>                # 完整 LLM 反推
#   ./scripts/run-local.sh import-fast <novel.md> [--bible X]
#   ./scripts/run-local.sh review [--from N --to M]
#   ./scripts/run-local.sh rewrite [--from N --to M]
#   ./scripts/run-local.sh help
#
# 设计：保留 cwd 在 novel-studio 项目根，保证 output/、./.ainovel/、go.mod 等相对路径都对。

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
    import)
        # 完整 LLM 反推。--review-budget 8m 给评审阶段 8 分钟硬时间。
        exec "${BIN[@]}" --import "$@" --review-budget 8m
        ;;
    import-fast)
        # 本地确定性导入，不调 LLM 反推 foundation。--no-review 也跳过评审。
        # 如果用户没显式传 --no-review，默认带上以保证纯本地运行（脚本场景）。
        # 但 --help / -h / help 是查 usage 的，加 --no-review 会让 flag 解析把 --help 当路径。
        has_help=0
        has_no_review=0
        for a in "$@"; do
            case "$a" in
                --help|-h|help) has_help=1 ;;
                --no-review)    has_no_review=1 ;;
            esac
        done
        if [[ $has_help -eq 1 ]]; then
            exec "${BIN[@]}" --import-fast "$@"
        elif [[ $has_no_review -eq 0 ]]; then
            exec "${BIN[@]}" --import-fast "$@" --no-review
        else
            exec "${BIN[@]}" --import-fast "$@"
        fi
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
        echo "用法: $0 [tui|import|import-fast|review|rewrite|help] [args...]" >&2
        exit 1
        ;;
esac
