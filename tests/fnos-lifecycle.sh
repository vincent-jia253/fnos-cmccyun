#!/bin/bash
# 生命周期脚本验证：status / start(FakeBackend) / 挂载访问 / stop
# 用法: sudo tests/fnos-lifecycle.sh
set -u
# 用于交叉用户访问验证的「其它用户」，可用环境变量覆盖
OTHER_USER="${OTHER_USER:-nobody}"
APP_DIR=/var/apps/cmccyun
MAIN=$APP_DIR/cmd/main
LC=/tmp/cmccyun-lc
TEST_PORT=19899

echo "== 1. status（未运行应返回 3） =="
# 注意：必须带上本次测试的 TRIM_PKGVAR，否则会去看真正已安装应用的状态
TRIM_APPDEST=/vol4/@appcenter/cmccyun TRIM_PKGETC="$LC/etc" TRIM_PKGVAR="$LC/var" \
TRIM_SERVICE_PORT="$TEST_PORT" TRIM_USERNAME=cmccyun TRIM_TEMP_LOGFILE="$LC/tmp.log" \
    "$MAIN" status
echo "status exit=$?"

echo "== 2. 以 FakeBackend 启动（模拟云盘后端） =="
rm -rf "$LC"
mkdir -p "$LC/backend/docs" "$LC/etc" "$LC/var" "$LC/mnt"
echo "lifecycle test" > "$LC/backend/docs/readme.txt"
mkdir -p "$LC/backend/dir1"
chown -R cmccyun "$LC"
TRIM_APPDEST=/vol4/@appcenter/cmccyun \
TRIM_PKGETC="$LC/etc" TRIM_PKGVAR="$LC/var" TRIM_SERVICE_PORT="$TEST_PORT" \
TRIM_USERNAME=cmccyun TRIM_TEMP_LOGFILE="$LC/tmp.log" \
CMCCYUN_DEBUG_FAKE="$LC/backend" CMCCYUN_MOUNT="$LC/mnt" \
    "$MAIN" start
echo "start exit=$?"

sleep 1
echo "== 3. status（应返回 0） =="
TRIM_APPDEST=/vol4/@appcenter/cmccyun TRIM_PKGETC="$LC/etc" TRIM_PKGVAR="$LC/var" \
TRIM_SERVICE_PORT="$TEST_PORT" TRIM_USERNAME=cmccyun TRIM_TEMP_LOGFILE="$LC/tmp.log" \
    "$MAIN" status
echo "status exit=$?"
echo "-- pid 文件: $(cat $LC/var/cmccyun.pid 2>/dev/null)"
echo "-- 挂载: $(grep -F " $LC/mnt " /proc/self/mountinfo || echo 无)"
echo "-- 内容: $(ls "$LC/mnt")"
echo "-- 以 cmccyun 读取: $(sudo -u cmccyun cat "$LC/mnt/docs/readme.txt" 2>&1)"
echo "-- 以 $OTHER_USER 读取: $(sudo -u "$OTHER_USER" cat "$LC/mnt/docs/readme.txt" 2>&1)"
echo "-- 以 $OTHER_USER 建目录:"
sudo -u "$OTHER_USER" mkdir -p "$LC/mnt/from-other" && echo "   ok"
ls "$LC/mnt"
echo "-- 后端实际内容: $(ls "$LC/backend")"

echo "== 4. 重复 start（应幂等） =="
TRIM_APPDEST=/vol4/@appcenter/cmccyun TRIM_PKGETC="$LC/etc" TRIM_PKGVAR="$LC/var" \
TRIM_SERVICE_PORT="$TEST_PORT" TRIM_USERNAME=cmccyun TRIM_TEMP_LOGFILE="$LC/tmp.log" \
CMCCYUN_DEBUG_FAKE="$LC/backend" CMCCYUN_MOUNT="$LC/mnt" \
    "$MAIN" start
echo "start#2 exit=$?"

echo "== 5. stop（应卸载并退出 0） =="
TRIM_APPDEST=/vol4/@appcenter/cmccyun TRIM_PKGETC="$LC/etc" TRIM_PKGVAR="$LC/var" \
TRIM_SERVICE_PORT="$TEST_PORT" TRIM_USERNAME=cmccyun TRIM_TEMP_LOGFILE="$LC/tmp.log" \
CMCCYUN_DEBUG_FAKE="$LC/backend" CMCCYUN_MOUNT="$LC/mnt" \
    "$MAIN" stop
echo "stop exit=$?"
echo "-- 剩余挂载: $(grep -cF " $LC/mnt " /proc/self/mountinfo)"
TRIM_APPDEST=/vol4/@appcenter/cmccyun TRIM_PKGETC="$LC/etc" TRIM_PKGVAR="$LC/var" \
TRIM_SERVICE_PORT="$TEST_PORT" TRIM_USERNAME=cmccyun TRIM_TEMP_LOGFILE="$LC/tmp.log" \
    "$MAIN" status
echo "-- stop 后 status exit=$?"

echo "== 6. stdout.log =="
cat "$LC/var/stdout.log"
