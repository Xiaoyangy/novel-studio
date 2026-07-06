#!/bin/sh
# novel-studio 一键安装脚本
#
#   curl -fsSL https://raw.githubusercontent.com/chenhongyang/novel-studio/main/scripts/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/chenhongyang/novel-studio/main/scripts/install.sh | sh -s -- v1.2.3
#
# 自定义安装目录： NOVEL_STUDIO_INSTALL_DIR=~/.local/bin curl -fsSL ... | sh
# 指定版本：NOVEL_STUDIO_VERSION=v1.2.3 curl -fsSL ... | sh
set -e

REPO="chenhongyang/novel-studio"
BIN="novel-studio"
DEST="${NOVEL_STUDIO_INSTALL_DIR:-/usr/local/bin}"
VERSION="${NOVEL_STUDIO_VERSION:-${1:-latest}}"

for cmd in curl tar; do
	command -v "$cmd" >/dev/null 2>&1 || { echo "需要 $cmd，请先安装后重试"; exit 1; }
done

case "$(uname -s)" in
	Darwin) OS="Darwin" ;;
	Linux)  OS="Linux" ;;
	*) echo "不支持的系统 $(uname -s)；Windows 请到 https://github.com/$REPO/releases 手动下载"; exit 1 ;;
esac

case "$(uname -m)" in
	x86_64|amd64)  ARCH="x86_64" ;;
	arm64|aarch64) ARCH="arm64" ;;
	*) echo "不支持的架构 $(uname -m)"; exit 1 ;;
esac

if [ "$VERSION" = "latest" ] || [ -z "$VERSION" ]; then
	API="https://api.github.com/repos/$REPO/releases/latest"
	echo "查询最新版本..."
else
	case "$VERSION" in
		v*) TAG="$VERSION" ;;
		*) TAG="v$VERSION" ;;
	esac
	API="https://api.github.com/repos/$REPO/releases/tags/$TAG"
	echo "查询版本 $TAG..."
fi

RELEASE=$(curl -fsSL "$API")
TAG=$(printf '%s\n' "$RELEASE" | grep '"tag_name"' | head -1 | cut -d '"' -f 4)
URL=$(printf '%s\n' "$RELEASE" \
	| grep "browser_download_url" \
	| grep "_${OS}_${ARCH}.tar.gz" \
	| head -1 | cut -d '"' -f 4)
[ -n "$URL" ] || { echo "未找到 ${OS}_${ARCH} 安装包，请到 https://github.com/$REPO/releases 手动下载"; exit 1; }

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "下载 $URL"
curl -fsSL -o "$TMP/pkg.tar.gz" "$URL"
tar -xzf "$TMP/pkg.tar.gz" -C "$TMP"

echo "安装到 $DEST"
[ -d "$DEST" ] || mkdir -p "$DEST" 2>/dev/null || sudo mkdir -p "$DEST"
if [ -w "$DEST" ]; then
	mv "$TMP/$BIN" "$DEST/$BIN"
else
	echo "需要管理员权限写入 $DEST"
	sudo mv "$TMP/$BIN" "$DEST/$BIN"
fi
chmod +x "$DEST/$BIN"

# 二进制未签名，macOS 首次运行会被 Gatekeeper 拦，解除隔离
[ "$OS" = "Darwin" ] && xattr -d com.apple.quarantine "$DEST/$BIN" 2>/dev/null || true

echo "✓ 安装完成：$DEST/$BIN"
[ -n "$TAG" ] && echo "版本：$TAG"
command -v "$BIN" >/dev/null 2>&1 || echo "提示：$DEST 不在 PATH 中，请将其加入 PATH"
echo "运行 $BIN 开始使用"
