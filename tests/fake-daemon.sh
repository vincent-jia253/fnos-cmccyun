#!/bin/bash
# 用假后端跑一个隔离的调试守护进程（不碰真机上那个真挂载）。
#
#   bash tests/fake-daemon.sh start|stop|status
#
# 关键点：二进制复制成 cmccyun-test 再跑。线上应用停止时执行的是
# `pkill -x cmccyun`（按进程名精确匹配），换成别的名字就不会误杀真挂载。
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
GEN="$(cd "${HERE}/.." && pwd)"
SRC="${BIN:-${GEN}/fnos-cmccyun/app/cmccyun-linux-amd64}"
ROOT=${ROOT:-/tmp/cct}
PORT=${PORT:-19896}
BINDIR=/tmp/cmccyun-build
BIN="${BINDIR}/cmccyun-test"
PROC=cmccyun-test

start() {
    [ -d "${ROOT}/backend" ] || { echo "先跑 tests/make-fixtures.sh"; exit 1; }
    mkdir -p "${BINDIR}" "${ROOT}/etc" "${ROOT}/mnt"
    cp -f "${SRC}" "${BIN}"; chmod 755 "${BIN}"
    pkill -x "${PROC}" 2>/dev/null; sleep 0.5
    fusermount3 -u "${ROOT}/mnt" 2>/dev/null
    rm -f "${ROOT}/daemon.log" "${ROOT}/stdout.log"
    ln -sf "${BIN}" "${BINDIR}/cmccyun"
    nohup env CMCCYUN_DEBUG_FAKE="${ROOT}/backend" CMCCYUN_ETC="${ROOT}/etc" \
        CMCCYUN_PORT="${PORT}" CMCCYUN_MOUNT="${ROOT}/mnt" CMCCYUN_LOG="${ROOT}/daemon.log" \
        "${BIN}" run >>"${ROOT}/stdout.log" 2>&1 &
    for i in $(seq 1 30); do
        grep -qsF " ${ROOT}/mnt " /proc/self/mounts && { echo "已挂载 ${ROOT}/mnt (${i}00ms, 端口 ${PORT})"; return 0; }
        sleep 0.3
    done
    echo "!! 未挂载，日志："; tail -20 "${ROOT}/stdout.log" "${ROOT}/daemon.log" 2>/dev/null
    return 1
}

stop() {
    pkill -x "${PROC}" 2>/dev/null
    sleep 0.8
    fusermount3 -u "${ROOT}/mnt" 2>/dev/null
    echo "已停止"
}

case "${1:-start}" in
    start) start ;;
    stop) stop ;;
    restart) stop; start ;;
    status) pgrep -x "${PROC}" >/dev/null && echo "运行中 $(pgrep -x "${PROC}")" || echo "未运行" ;;
    *) echo "用法: $0 start|stop|restart|status"; exit 2 ;;
esac
