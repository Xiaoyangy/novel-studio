#!/bin/sh
# novel-studio 一键安装脚本
#
#   curl -fsSL https://raw.githubusercontent.com/Xiaoyangy/novel-studio/main/scripts/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/Xiaoyangy/novel-studio/main/scripts/install.sh | sh -s -- v1.2.3
#
# 自定义安装目录： curl -fsSL ... | NOVEL_STUDIO_INSTALL_DIR="$HOME/.local/bin" sh
# 指定版本：NOVEL_STUDIO_VERSION=v1.2.3 curl -fsSL ... | sh
set -e

REPO="Xiaoyangy/novel-studio"
BIN="novel-studio"
VERSION="${NOVEL_STUDIO_VERSION:-${1:-latest}}"

if [ -n "${NOVEL_STUDIO_INSTALL_DIR:-}" ]; then
	DEST="$NOVEL_STUDIO_INSTALL_DIR"
elif command -v "$BIN" >/dev/null 2>&1 && [ -w "$(dirname "$(command -v "$BIN")")" ]; then
	DEST=$(dirname "$(command -v "$BIN")")
elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
	DEST=/usr/local/bin
else
	DEST="$HOME/.local/bin"
fi

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

RELEASE=$(curl -fsSL -H "Accept: application/vnd.github+json" -H "User-Agent: novel-studio-installer" "$API")
TAG=$(printf '%s\n' "$RELEASE" | sed -n 's/.*"tag_name":"\([^"]*\)".*/\1/p' | head -1)
URL=$(printf '%s\n' "$RELEASE" \
	| tr '{' '\n' \
	| grep '"browser_download_url"' \
	| grep "_${OS}_${ARCH}.tar.gz\"" \
	| sed -n 's/.*"browser_download_url":"\([^"]*\)".*/\1/p' \
	| head -1)
[ -n "$TAG" ] || { echo "GitHub Release 响应缺少版本号，请稍后重试"; exit 1; }
[ -n "$URL" ] || { echo "未找到 ${OS}_${ARCH} 安装包，请到 https://github.com/$REPO/releases 手动下载"; exit 1; }
CHECKSUM_URL=$(printf '%s\n' "$RELEASE" \
	| tr '{' '\n' \
	| grep '"browser_download_url"' \
	| grep '_checksums.txt"' \
	| sed -n 's/.*"browser_download_url":"\([^"]*\)".*/\1/p' \
	| head -1)

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "下载 $URL"
curl -fsSL -o "$TMP/pkg.tar.gz" "$URL"

if [ -n "$CHECKSUM_URL" ]; then
	curl -fsSL -o "$TMP/checksums.txt" "$CHECKSUM_URL"
	ASSET_NAME=$(basename "$URL")
	EXPECTED=$(awk -v name="$ASSET_NAME" '$2 == name || $2 == "*" name {print $1; exit}' "$TMP/checksums.txt")
	[ -n "$EXPECTED" ] || { echo "校验文件中未找到 $ASSET_NAME"; exit 1; }
	if command -v sha256sum >/dev/null 2>&1; then
		ACTUAL=$(sha256sum "$TMP/pkg.tar.gz" | awk '{print $1}')
	elif command -v shasum >/dev/null 2>&1; then
		ACTUAL=$(shasum -a 256 "$TMP/pkg.tar.gz" | awk '{print $1}')
	else
		echo "需要 sha256sum 或 shasum 校验下载文件"
		exit 1
	fi
	[ "$EXPECTED" = "$ACTUAL" ] || { echo "安装包 SHA-256 校验失败，已停止安装"; exit 1; }
	echo "✓ SHA-256 校验通过"
else
	echo "警告：Release 未提供 checksums 文件，已停止安装" >&2
	exit 1
fi
tar -xzf "$TMP/pkg.tar.gz" -C "$TMP"

echo "安装到 $DEST"
[ -d "$DEST" ] || mkdir -p "$DEST"
if [ -w "$DEST" ]; then
	mv "$TMP/$BIN" "$DEST/$BIN"
else
	echo "安装目录不可写：$DEST" >&2
	echo "请改用用户目录：curl -fsSL https://raw.githubusercontent.com/$REPO/main/scripts/install.sh | NOVEL_STUDIO_INSTALL_DIR=\"$HOME/.local/bin\" sh" >&2
	exit 1
fi
chmod +x "$DEST/$BIN"

# 二进制未签名，macOS 首次运行会被 Gatekeeper 拦，解除隔离
[ "$OS" = "Darwin" ] && xattr -d com.apple.quarantine "$DEST/$BIN" 2>/dev/null || true

echo "✓ 安装完成：$DEST/$BIN"
[ -n "$TAG" ] && echo "版本：$TAG"
"$DEST/$BIN" --version
if command -v "$BIN" >/dev/null 2>&1; then
	if "$DEST/$BIN" --help 2>/dev/null | grep -q "novel-studio doctor"; then
		echo "下一步：$BIN doctor"
	fi
	echo "然后运行：$BIN"
else
	echo "提示：$DEST 不在 PATH 中。当前终端先运行："
	echo "  export PATH=\"$DEST:\$PATH\""
	if "$DEST/$BIN" --help 2>/dev/null | grep -q "novel-studio doctor"; then
		echo "然后运行：$BIN doctor"
	else
		echo "然后运行：$BIN"
	fi
fi
