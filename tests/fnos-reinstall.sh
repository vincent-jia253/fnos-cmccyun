#!/bin/bash
# 干净重装测试（root 执行）
#
#   sudo bash tests/fnos-reinstall.sh [fpk路径] [环境文件]
#
# 背景：fnOS 的 `appcenter-cli uninstall` 不可用（提示请在 Web UI 卸载），
# 而直接删掉 /vol4/@appcenter/cmccyun 会让应用中心仍然认为"已安装"，
# 之后的 install-fpk 会走"升级"分支并失败（install_init 找不到 fnos-common.sh）。
# 因此测试脚本按 Web UI 卸载的等效步骤手工清理：
#   1) stop -> uninstall_init/uninstall_callback -> 删除应用目录
#   2) 停 trim_app_center 服务 -> 清掉 appcenter/trim_sac 中该应用的记录 -> 起服务
#   3) install-fpk 全新安装
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
FPK="${1:-${HERE}/../fnos-cmccyun/cmccyun.fpk}"
ENVF="${2:-/tmp/cmccyun-install.env}"
VOL="${VOL:-4}"
APP=cmccyun

echo "== 0. 前置信息 =="
echo "fpk: ${FPK}"
echo "env: ${ENVF}"
[ -r "${FPK}" ] || { echo "找不到 fpk: ${FPK}" >&2; exit 1; }

echo "== 1. 停止并执行卸载脚本 =="
appcenter-cli stop ${APP} >/dev/null 2>&1 || true
if [ -x "/var/apps/${APP}/cmd/uninstall_init" ]; then
    TRIM_APPDEST=/vol4/@appcenter/${APP} TRIM_PKGETC=/vol4/@appconf/${APP} \
    TRIM_PKGVAR=/vol4/@appdata/${APP} bash "/var/apps/${APP}/cmd/uninstall_init" || true
    TRIM_APPDEST=/vol4/@appcenter/${APP} TRIM_PKGETC=/vol4/@appconf/${APP} \
    TRIM_PKGVAR=/vol4/@appdata/${APP} wizard_keep_data=delete \
        bash "/var/apps/${APP}/cmd/uninstall_callback" || true
fi
rm -rf "/var/apps/${APP}" /vol4/@appcenter/${APP} /vol4/@appconf/${APP} \
       /vol4/@appdata/${APP} /vol4/@appmeta/${APP} /vol4/@apphome/${APP} /vol4/@apptemp/${APP}

echo "== 2. 清理应用中心记录 =="
systemctl stop trim_app_center.service
sleep 2
sudo -u postgres psql -d appcenter -q -c \
    "delete from app where app_name='${APP}';
     delete from app_service where service_name like '${APP}%';
     delete from app_package where app_name='${APP}';"
sudo -u postgres psql -d trim_sac -q -c "delete from entry where app_name='${APP}';"
systemctl start trim_app_center.service
sleep 8
echo -n "check: "; appcenter-cli check ${APP}

echo "== 3. 全新安装 =="
appcenter-cli install-fpk "${FPK}" --volume "${VOL}" --env "${ENVF}" 2>&1 | tail -1
sleep 3

echo "== 4. 校验 =="
ls -d /vol4/@appcenter/${APP} /vol4/@appconf/${APP} /vol4/@appdata/${APP} /var/apps/${APP}
echo "-- 安装日志（应无错误）"
cat /var/log/apps/${APP}.log
echo "-- 配置"
cat /vol4/@appconf/${APP}/config.json
echo -n "-- 状态: "; appcenter-cli status ${APP}
echo "-- 管理界面"
curl -s -o /dev/null -w "http=%{http_code}\n" http://127.0.0.1:19898/
