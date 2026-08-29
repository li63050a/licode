#!/bin/bash
# licode 一键构建脚本
# 完全静态编译（CGO_ENABLED=0），产出多平台二进制到 dist/
set -e

cd "$(dirname "$0")"

VERSION="${VERSION:-0.1.0}"
LDFLAGS="-s -w -X main.version=${VERSION}"
DIST="dist"
mkdir -p "$DIST"

echo "==> go test ./..."
go test ./...

echo "==> 编译 linux/amd64"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "$DIST/licode-linux-amd64" .

echo "==> 编译 linux/arm64"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o "$DIST/licode-linux-arm64" .

echo "==> 编译 darwin/amd64"
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "$DIST/licode-darwin-amd64" .

echo "==> 编译 darwin/arm64"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o "$DIST/licode-darwin-arm64" .

echo "==> 编译 windows/amd64"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o "$DIST/licode-windows-amd64.exe" .

echo "==> 编译本机版本"
CGO_ENABLED=0 go build -ldflags="${LDFLAGS}" -o "$DIST/licode" .

# 可选 UPX 压缩
if command -v upx >/dev/null 2>&1; then
  echo "==> UPX 压缩"
  upx --best --lzma "$DIST"/licode-* 2>/dev/null || true
fi

echo "==> 产物列表"
ls -lh "$DIST"
echo "构建完成 ✔"