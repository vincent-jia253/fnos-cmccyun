#!/bin/bash
# 构建 cmccyun.fpk：编译 x86/ARM 双架构二进制 -> 整理权限 -> fnpack build
#
#   GO=/path/to/go VERSION=1.0.0 ./build.sh
#
# 依赖：Go 1.24+（CGO_ENABLED=0，不需要 gcc）、fnpack（飞牛官方打包工具）。
# 没有 go 时可显式指定：GO=/usr/local/go/bin/go VERSION=1.3.2 ./build.sh
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
VERSION="${VERSION:-1.0.0}"

# 源码目录：优先本目录下的 cmccyun-src/，也兼容与打包工程平级的旧布局
SRC="${SRC:-}"
if [ -z "${SRC}" ]; then
    for c in "${HERE}/cmccyun-src" "${HERE}/../cmccyun-src"; do
        [ -d "${c}" ] && { SRC="${c}"; break; }
    done
fi
[ -n "${SRC}" ] || { echo "未找到 cmccyun-src（可用 SRC=/path/to/cmccyun-src 指定）" >&2; exit 1; }
PKG="github.com/vincent-jia253/fnos-cmccyun/internal/app"

find_go() {
    if [ -n "${GO:-}" ] && [ -x "${GO}" ]; then echo "${GO}"; return; fi
    if command -v go >/dev/null 2>&1; then command -v go; return; fi
    for c in /usr/local/go/bin/go /opt/go/bin/go; do
        [ -x "$c" ] && { echo "$c"; return; }
    done
    echo ""
}

GOBIN="$(find_go)"
[ -n "${GOBIN}" ] || { echo "未找到 go，请先安装 Go 工具链或设置 GO=..." >&2; exit 1; }
export GOROOT="${GOROOT:-$(dirname "$(dirname "${GOBIN}")")}"
export PATH="$(dirname "${GOBIN}"):${PATH}"
export GOFLAGS="${GOFLAGS:--mod=mod}"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-off}"
export CGO_ENABLED=0

echo "==> Go: ${GOBIN} ($("${GOBIN}" version))"
echo "==> 版本: ${VERSION}"

mkdir -p "${HERE}/app"
cd "${SRC}"
for arch in amd64 arm64; do
    echo "==> 编译 linux/${arch}"
    GOOS=linux GOARCH=${arch} "${GOBIN}" build -trimpath \
        -ldflags "-s -w -X ${PKG}.Version=${VERSION}" \
        -o "${HERE}/app/cmccyun-linux-${arch}" .
done

# 同步 manifest 版本号
sed -i "s/^version .*/version               = ${VERSION}/" "${HERE}/manifest"

# 权限整理：目录可遍历（0755），脚本可执行（0755），资源可读（0644）
for d in "${HERE}" "${HERE}/cmd" "${HERE}/config" "${HERE}/wizard" "${HERE}/app" "${HERE}/app/ui" "${HERE}/app/ui/images"; do
    [ -d "${d}" ] && chmod 755 "${d}"
done
chmod 755 "${HERE}"/cmd/* "${HERE}"/app/cmccyun-linux-* "${HERE}/app/fnos-common.sh"
chmod 644 "${HERE}/app/ui/config" "${HERE}/manifest" "${HERE}/config/privilege" "${HERE}/config/resource"
chmod 644 "${HERE}"/wizard/* "${HERE}"/ICON.PNG "${HERE}"/ICON_256.PNG "${HERE}"/app/ui/images/*

echo "==> fnpack build"
cd "${HERE}"
fnpack build

FPK="$(ls -t "${HERE}"/*.fpk 2>/dev/null | head -n1 || true)"
if [ -n "${FPK}" ]; then
    if [ "${FPK}" != "${HERE}/cmccyun.fpk" ]; then
        cp -f "${FPK}" "${HERE}/cmccyun.fpk"
    fi
    chmod 0644 "${HERE}/cmccyun.fpk"
    echo "==> 产物: ${HERE}/cmccyun.fpk ($(du -h "${HERE}/cmccyun.fpk" | cut -f1))"
else
    echo "==> 警告：未找到生成的 .fpk" >&2
    exit 1
fi
