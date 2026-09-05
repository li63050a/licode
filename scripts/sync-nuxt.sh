#!/usr/bin/env bash
# 重新生成 Nuxt 静态前端产物（nuxtweb/dist）并同步到 internal/web/nuxt（go:embed 目录）。
# 无 node 时跳过，使用已提交的 internal/web/nuxt 产物，保证 go build 始终可用。
set -euo pipefail
cd "$(dirname "$0")/.."

if ! command -v node >/dev/null 2>&1; then
    echo "[nuxt] node 不可用，跳过（使用已提交的 internal/web/nuxt 产物）" >&2
    exit 0
fi

if [ ! -d "nuxtweb" ]; then
    echo "[nuxt] 未找到 nuxtweb/，跳过" >&2
    exit 0
fi

echo "==> nuxt generate ..."
( cd nuxtweb && npm run generate )

echo "==> 同步 nuxtweb/dist -> internal/web/nuxt ..."
rm -rf internal/web/nuxt
mkdir -p internal/web/nuxt
cp -r nuxtweb/dist/. internal/web/nuxt/

echo "完成：internal/web/nuxt/ 共 $(find internal/web/nuxt -type f | wc -l) 个文件"
