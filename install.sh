#!/usr/bin/env bash
# licode 一键安装脚本（Linux / macOS）
# 用法: curl -sSL https://gitee.com/li63050a/licode/raw/main/install.sh | bash
# 或:   ./install.sh [github|gitee]
set -e

VERSION="${VERSION:-v0.2.0}"
SOURCE="${1:-github}"

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64|Linux-amd64)  FILE="api-gateway-linux-amd64" ;;
  Linux-aarch64|Linux-arm64) FILE="api-gateway-linux-arm64" ;;
  Darwin-x86_64|Darwin-amd64) FILE="api-gateway-darwin-amd64" ;;
  Darwin-arm64|Darwin-aarch64) FILE="api-gateway-darwin-arm64" ;;
  *) echo "不支持当前平台: $(uname -s)-$(uname -m)"; exit 1 ;;
esac

case "$SOURCE" in
  gitee)  BASE="https://gitee.com/li63050a/licode/releases/download" ;;
  *)      BASE="https://github.com/li63050a/licode/releases/download" ;;
esac
URL="$BASE/$VERSION/$FILE"

DEST="/usr/local/bin"
if [ ! -w "$DEST" ]; then
  DEST="$HOME/.local/bin"
  mkdir -p "$DEST"
fi

echo "==> 下载 $URL"
if command -v curl >/dev/null 2>&1; then
  curl -fSL "$URL" -o "$DEST/licode"
elif command -v wget >/dev/null 2>&1; then
  wget -q "$URL" -O "$DEST/licode"
else
  echo "需要 curl 或 wget"; exit 1
fi
chmod +x "$DEST/licode"

echo "==> 已安装到 $DEST/licode"
echo "==> 直接运行 licode 进入 TUI（如提示找不到命令，请把 $DEST 加入 PATH）"
"$DEST/licode" --help 2>&1 | head -2 || true