#!/usr/bin/env bash
# 纯静态交叉编译常用 Go 平台（CGO_ENABLED=0）
# 任何需要 cgo 的平台都会被自动跳过，绝不出产动态链接的二进制
set -uo pipefail

MODULE="licode"
OUT_DIR="build"
LDFLAGS="-s -w"

# ===== 常用平台列表 =====
PLATFORMS="
linux/amd64
linux/arm64
linux/386
windows/amd64
windows/386
windows/arm64
darwin/amd64
darwin/arm64
freebsd/amd64
"

mkdir -p "$OUT_DIR"

# 刷新 Nuxt 前端静态产物（无 node 时跳过，使用已提交的 internal/web/nuxt 产物）
bash scripts/sync-nuxt.sh

echo "==> 纯静态编译（CGO_ENABLED=0，LDFLAGS=${LDFLAGS}，输出目录 ${OUT_DIR}/）"

built=0
skipped=0

while IFS=/ read -r GOOS GOARCH; do
    [ -z "$GOOS" ] && continue

    ext=""
    [ "$GOOS" = "windows" ] && ext=".exe"
    output="${OUT_DIR}/${MODULE}-${GOOS}-${GOARCH}${ext}"

    # 仅尝试一次静态编译，失败则跳过（绝不用 cgo）
    if CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
        go build -buildvcs=false -ldflags="$LDFLAGS" -o "$output" . 2>/dev/null; then
        echo "  [OK]   $GOOS/$GOARCH (静态)"
        built=$((built + 1))
    else
        echo "  [SKIP] $GOOS/$GOARCH (无法静态编译，已跳过)"
        skipped=$((skipped + 1))
        rm -f "$output" 2>/dev/null
    fi
done <<< "$PLATFORMS"

echo "==> 完成：成功 $built 个，跳过 $skipped 个，产物在 $OUT_DIR/"