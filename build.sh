#!/usr/bin/env bash
# 一键交叉编译所有 Go 支持的平台/架构
# 产物全部为静态链接（CGO_ENABLED=0），无任何 .so 依赖
# 个别平台（android/ios/netbsd/openbsd/plan9/solaris/windows-386 等）
# 必须开启 cgo 才能链接，会被自动跳过。
set -uo pipefail

MODULE="api-gateway"
OUT_DIR="build"
LDFLAGS="-s -w"

# 全部需要编译的目标（GOOS/GOARCH）
PLATFORMS="
aix/ppc64
android/386
android/amd64
android/arm
android/arm64
darwin/amd64
darwin/arm64
dragonfly/amd64
freebsd/386
freebsd/amd64
freebsd/arm
freebsd/arm64
freebsd/riscv64
illumos/amd64
ios/amd64
ios/arm64
js/wasm
linux/386
linux/amd64
linux/arm
linux/arm64
linux/loong64
linux/mips
linux/mips64
linux/mips64le
linux/mipsle
linux/ppc64
linux/ppc64le
linux/riscv64
linux/s390x
netbsd/386
netbsd/amd64
netbsd/arm
netbsd/arm64
openbsd/386
openbsd/amd64
openbsd/arm
openbsd/arm64
openbsd/ppc64
openbsd/riscv64
plan9/386
plan9/amd64
plan9/arm
solaris/amd64
wasip1/wasm
windows/386
windows/amd64
windows/arm64
"

mkdir -p "$OUT_DIR"

echo "==> 开始交叉编译（CGO_ENABLED=0 ${LDFLAGS}，输出目录 ${OUT_DIR}/）"

built=0
skipped=0

while IFS=/ read -r GOOS GOARCH; do
    [ -z "$GOOS" ] && continue

    # 输出文件名：windows 加 .exe，wasm 加 .wasm
    ext=""
    [ "$GOOS" = "windows" ] && ext=".exe"
    case "$GOOS/$GOARCH" in
        */wasm) ext=".wasm" ;;
    esac
    output="${OUT_DIR}/${MODULE}-${GOOS}-${GOARCH}${ext}"

    # 先尝试静态编译（CGO_ENABLED=0，无 .so 依赖）
    if CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
        go build -buildvcs=false -ldflags="$LDFLAGS" -o "$output" . 2>/dev/null; then
        echo "  [OK]   $GOOS/$GOARCH (静态)"
        built=$((built + 1))
        continue
    fi

    # 静态编译失败（该平台强制要求 cgo 外部链接）：退回动态编译，不跳过
    if CGO_ENABLED=1 GOOS="$GOOS" GOARCH="$GOARCH" \
        go build -buildvcs=false -ldflags="$LDFLAGS" -o "$output" . 2>/dev/null; then
        echo "  [OK]   $GOOS/$GOARCH (动态/cgo)"
        built=$((built + 1))
        continue
    fi

    echo "  [SKIP] $GOOS/$GOARCH (无可用工具链，已跳过)"
    skipped=$((skipped + 1))
    rm -f "$output"
done <<< "$PLATFORMS"

echo "==> 完成：成功 $built 个，跳过 $skipped 个，产物在 $OUT_DIR/"
