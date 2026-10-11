#!/bin/bash
# 模拟「应用中心」升级：把新 fpk 的程序文件覆盖到已安装的应用上，再跑
# upgrade_init / upgrade_callback，最后启动并自检。
#
#   sudo bash tests/fnos-upgrade-sim.sh [fpk路径]
#
# 说明：`appcenter-cli install-fpk` 在「已安装」时会直接拒绝（需在 Web UI 里点升级），
# 所以命令行无法验证升级流程；本脚本按 Web UI 的等效步骤手工执行，用来确认
# 升级脚本不会覆盖用户配置、二进制确实被替换、服务能重新起来。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
FPK="${1:-${HERE}/../fnos-cmccyun/cmccyun.fpk}"
APP=cmccyun
DEST=/vol4/@appcenter/${APP}
ETC=/vol4/@appconf/${APP}
VAR=/vol4/@appdata/${APP}
RUN=/var/apps/${APP}
WORK="$(mktemp -d)"

[ -r "${FPK}" ] || { echo "找不到 fpk: ${FPK}" >&2; exit 1; }

echo "== 0. 升级前 =="
"${RUN}/cmd/main" stop 2>&1 | tail -1 || true
cp -a "${ETC}" "${WORK}/etc-before"
cat "${ETC}/config.json" >"${WORK}/config-before.json"
md5sum "${DEST}/${APP}-linux-amd64" | tee "${WORK}/bin-before.md5"

echo "== 1. 解包并覆盖程序文件 =="
tar -xf "${FPK}" -C "${WORK}"
mkdir -p "${WORK}/app"
tar -xzf "${WORK}/app.tgz" -C "${WORK}/app"
cp -a "${WORK}/app/." "${DEST}/"
cp -a "${WORK}/cmd/." "${RUN}/cmd/"
cp -a "${WORK}/manifest" "${RUN}/manifest"
mkdir -p "${RUN}/wizard"
cp -a "${WORK}/wizard/." "${RUN}/wizard/"
cp -a "${WORK}/ICON.PNG" "${WORK}/ICON_256.PNG" "${RUN}/"
chmod 0755 "${RUN}/cmd/"*
chmod 0755 "${DEST}/${APP}-linux-"*

echo "== 2. 执行升级脚本 =="
# TRIM_USERNAME 由 fnOS 注入（这里照样带上，贴近真实升级）。注意守护进程的运行
# 身份现在默认是 root（可在 $ETC/run_as 里改成包用户），不再跟着 TRIM_USERNAME 走。
E=(env "TRIM_APPDEST=${DEST}" "TRIM_PKGETC=${ETC}" "TRIM_PKGVAR=${VAR}"
   "TRIM_TEMP_LOGFILE=${WORK}/temp.log" "TRIM_DATA_SHARE_PATHS=/vol4/@appshare/${APP}"
   "TRIM_USERNAME=${APP}")
"${E[@]}" bash "${RUN}/cmd/upgrade_init"
"${E[@]}" bash "${RUN}/cmd/upgrade_callback"
sed 's/^/    /' "${WORK}/temp.log" 2>/dev/null || true

echo "== 3. 校验 =="
"${DEST}/${APP}-linux-amd64" version
cat "${ETC}/config.json" >"${WORK}/config-after.json"
if cmp -s "${WORK}/config-before.json" "${WORK}/config-after.json"; then
    echo "  PASS  配置原样保留"
else
    echo "  FAIL  配置被改动："; diff -u "${WORK}/config-before.json" "${WORK}/config-after.json" || true
    exit 1
fi
md5sum "${DEST}/${APP}-linux-amd64"

echo "== 4. 启动 =="
"${RUN}/cmd/main" start 2>&1 | tail -1
sleep 3
if "${RUN}/cmd/main" status; then echo "  PASS  服务运行中"; else echo "  FAIL  服务未运行"; exit 1; fi
code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:19898/ || true)
echo -n "  控制台 HTTP ${code}: "
[ "${code}" = "200" ] && echo "PASS" || { echo "FAIL"; exit 1; }
curl -s http://127.0.0.1:19898/api/status | head -c 200; echo

rm -rf "${WORK}"
echo "== 升级模拟完成 =="
