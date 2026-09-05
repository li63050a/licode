#!/usr/bin/env sh
# 前端依赖离线化：把 CDN 托管的第三方库下载到 internal/web/static/，
# 再随二进制 go:embed 打包，保证 licode 前端完全离线可用（无任何外网引用）。
#
# 用法： sh scripts/vendor-frontend-deps.sh
# 校验： 下载后按 sha256 强校验，防止被篡改 / 版本漂移。

set -e

DIR="$(cd "$(dirname "$0")/.." && pwd)"
STATIC="$DIR/internal/web/static"
mkdir -p "$STATIC"

# htmx 1.9.12（https://htmx.org）发布页提供 jsdelivr/unpkg CDN 链接：
#   https://unpkg.com/htmx.org@1.9.12/dist/htmx.min.js
#   或者 https://cdn.jsdelivr.net/npm/htmx.org@1.9.12/dist/htmx.min.js
HTMX_URL="https://unpkg.com/htmx.org@1.9.12/dist/htmx.min.js"
HTMX_SHA256="449317ade7881e949510db614991e195c3a099c4c791c24dacec55f9f4a2a452"
HTMX_OUT="$STATIC/htmx.min.js"

echo "[1/1] 下载 htmx 1.9.12 → $HTMX_OUT"
if [ -f "$HTMX_OUT" ] && [ "$(sha256sum "$HTMX_OUT" | cut -d' ' -f1)" = "$HTMX_SHA256" ]; then
  echo "      已存在且校验通过，跳过下载。"
else
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$HTMX_URL" -o "$HTMX_OUT"
  else
    wget -qO "$HTMX_OUT" "$HTMX_URL"
  fi
fi

ACTUAL="$(sha256sum "$HTMX_OUT" | cut -d' ' -f1)"
if [ "$ACTUAL" != "$HTMX_SHA256" ]; then
  echo "!! sha256 校验失败: 期望 $HTMX_SHA256，实际 $ACTUAL" >&2
  exit 1
fi

echo "      校验通过 ($ACTUAL)。"
echo "完成。前端第三方依赖已就位，构建时由 go:embed 打进二进制。"