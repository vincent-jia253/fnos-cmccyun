#!/bin/bash
# 移动云盘挂载（cmccyun）公共函数
# 由 cmd/ 下的生命周期脚本 source 使用。
# 依赖飞牛 fnOS 注入的环境变量：TRIM_APPDEST / TRIM_PKGETC / TRIM_PKGVAR /
# TRIM_SERVICE_PORT / TRIM_DATA_SHARE_PATHS / TRIM_USERNAME / TRIM_TEMP_LOGFILE

# /var/apps/<app>/{etc,var} 是指向 @appconf/@appdata 的软链接：统一解析成规范
# 路径，配置里就不会因为调用方不同而在两种写法间来回跳。
cmccyun_realpath() {
    local p="${1:-}" r=""
    [ -n "${p}" ] || { printf ''; return 0; }
    if r="$(readlink -f "${p}" 2>/dev/null)" && [ -n "${r}" ]; then
        printf '%s' "${r}"
    else
        printf '%s' "${p}"
    fi
}

CMCCYUN_CFG_DIR="$(cmccyun_realpath "${TRIM_PKGETC:-/etc/cmccyun}")"
CMCCYUN_CFG="${CMCCYUN_CFG_DIR}/config.json"
CMCCYUN_VAR_DIR="$(cmccyun_realpath "${TRIM_PKGVAR:-/tmp/cmccyun}")"
CMCCYUN_PIDFILE="${CMCCYUN_VAR_DIR}/cmccyun.pid"
CMCCYUN_STDOUT="${CMCCYUN_VAR_DIR}/stdout.log"
CMCCYUN_STAGE="${CMCCYUN_VAR_DIR}/staging"
CMCCYUN_LOG="${CMCCYUN_VAR_DIR}/cmccyun.log"
CMCCYUN_PORT="${TRIM_SERVICE_PORT:-19898}"
CMCCYUN_DEFAULT_SUBDIR="移动云盘"
export CMCCYUN_CFG_DIR CMCCYUN_CFG CMCCYUN_VAR_DIR CMCCYUN_PIDFILE CMCCYUN_LOG CMCCYUN_STAGE

cmccyun_bin() {
    case "$(uname -m)" in
        aarch64 | arm64) echo "${TRIM_APPDEST}/cmccyun-linux-arm64" ;;
        *) echo "${TRIM_APPDEST}/cmccyun-linux-amd64" ;;
    esac
}

# 运行守护进程的身份：默认 **root**。
# 为什么：挂载位置现在由用户任选（例如挂进自己的个人空间 /vol4/1000/移动云盘），
# 这需要「在任意目录里建目录 + 挂载」的权限。飞牛自带的网盘挂载与 CloudDrive2
# 同样是 root 身份挂载（mountinfo 里 user_id=0,group_id=0,allow_other）。
# 想降权运行的话，把用户名写进 $TRIM_PKGETC/run_as（例如 cmccyun），重启应用生效。
cmccyun_run_user() {
    local f="${CMCCYUN_CFG_DIR}/run_as" u=""
    if [ -r "${f}" ]; then
        u="$(head -n1 "${f}" 2>/dev/null | tr -d '[:space:]')"
        if [ -n "${u}" ] && id "${u}" >/dev/null 2>&1; then
            echo "${u}"
            return 0
        fi
    fi
    echo ""
}

# 供电给守护进程的环境变量（fnOS 变量名 -> cmccyun 变量名）
cmccyun_env() {
    CMCCYUN_ENVS=(
        "CMCCYUN_ETC=${CMCCYUN_CFG_DIR}"
        "CMCCYUN_STAGE=${CMCCYUN_STAGE}"
        "CMCCYUN_LOG=${CMCCYUN_LOG}"
        "CMCCYUN_PIDFILE=${CMCCYUN_PIDFILE}"
        "CMCCYUN_PORT=${CMCCYUN_PORT}"
    )
    local share="${TRIM_DATA_SHARE_PATHS:-}"
    share="${share%%:*}"
    if [ -n "${share}" ]; then
        CMCCYUN_ENVS+=("CMCCYUN_MOUNT=${share}/${CMCCYUN_DEFAULT_SUBDIR}")
    fi
}

cmccyun_log() {
    printf '%s %s\n' "$(date '+%F %T')" "$*" >>"${CMCCYUN_STDOUT}" 2>/dev/null
}

cmccyun_fail() {
    cmccyun_log "ERROR: $*"
    [ -n "${TRIM_TEMP_LOGFILE:-}" ] && echo "移动云盘：$*" >>"${TRIM_TEMP_LOGFILE}" 2>/dev/null
    echo "移动云盘：$*" >&2
}

# 非 root 用户挂载 FUSE 时，allow_other 需要 /etc/fuse.conf 中的 user_allow_other。
# 安装脚本以 root 身份运行，这里顺手补上（已存在则不动）。
cmccyun_ensure_fuse_conf() {
    local f=/etc/fuse.conf
    if [ ! -f "$f" ]; then
        printf 'user_allow_other\n' >"$f" 2>/dev/null && cmccyun_log "已创建 $f (user_allow_other)"
        return 0
    fi
    if grep -qE '^[[:space:]]*user_allow_other' "$f" 2>/dev/null; then
        return 0
    fi
    printf '\nuser_allow_other\n' >>"$f" 2>/dev/null && cmccyun_log "已在 $f 中启用 user_allow_other"
    return 0
}

# 套用向导(安装/设置)里填写的值
cmccyun_apply_wizard() {
    local bin="$1"
    [ -x "$bin" ] || return 1
    "$bin" fnos-init -c "${CMCCYUN_CFG}" >>"${CMCCYUN_STDOUT}" 2>&1
}

# 建目录 / 交属主
cmccyun_prepare() {
    local bin="$1" ru
    [ -x "$bin" ] || return 1
    mkdir -p "${CMCCYUN_VAR_DIR}" "${CMCCYUN_CFG_DIR}" "${CMCCYUN_STAGE}" 2>/dev/null
    touch "${CMCCYUN_STDOUT}" "${CMCCYUN_LOG}" 2>/dev/null
    ru="$(cmccyun_run_user)"
    if [ -n "${ru}" ]; then
        "$bin" prepare -c "${CMCCYUN_CFG}" "${ru}" >>"${CMCCYUN_STDOUT}" 2>&1
    else
        "$bin" prepare -c "${CMCCYUN_CFG}" >>"${CMCCYUN_STDOUT}" 2>&1
    fi
}

# 启动失败时，把最近的日志附到用户可见的错误信息里
cmccyun_dump_log() {
    local n="${1:-15}"
    [ -n "${TRIM_TEMP_LOGFILE:-}" ] || return 0
    {
        echo "---- 最近 ${n} 行日志 ----"
        tail -n "${n}" "${CMCCYUN_LOG}" 2>/dev/null
        tail -n "${n}" "${CMCCYUN_STDOUT}" 2>/dev/null
    } >>"${TRIM_TEMP_LOGFILE}" 2>/dev/null
}

# 读取守护进程 pid 文件（由 cmccyun run 自己写入）
cmccyun_pid() {
    [ -r "${CMCCYUN_PIDFILE}" ] || return 0
    head -n1 "${CMCCYUN_PIDFILE}" | tr -d '[:space:]'
}

cmccyun_pid_alive() {
    [ -n "${1:-}" ] && kill -0 "$1" 2>/dev/null
}

# 卸载残留挂载点（必要时懒卸载），供 stop / uninstall 复用
cmccyun_cleanup_mount() {
    local bin="$1" mp
    mp="$("${bin}" path -c "${CMCCYUN_CFG}" 2>/dev/null | head -n1)"
    [ -n "${mp}" ] || return 0
    grep -qsF " ${mp} " /proc/self/mountinfo || return 0
    cmccyun_log "清理挂载点 ${mp}"
    "${bin}" unmount -c "${CMCCYUN_CFG}" >>"${CMCCYUN_STDOUT}" 2>&1 || cmccyun_log "卸载 ${mp} 失败"
}
