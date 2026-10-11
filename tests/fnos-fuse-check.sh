#!/bin/bash
# 在真实 fnOS 上用 FakeBackend 验证 FUSE 挂载、allow_other 与交叉用户访问
# 用法: sudo tests/fnos-fuse-check.sh
set -u
# 用于交叉用户访问验证的「其它用户」，可用环境变量覆盖
OTHER_USER="${OTHER_USER:-nobody}"
ROOT=/tmp/cmccyun-fuse-test
BIN=/vol4/@appcenter/cmccyun/cmccyun-linux-amd64
RUNUSER=cmccyun
PORT="${PORT:-19899}"
# 端口被占用时自动换一个（开发机上可能常驻着 FakeBackend 调试进程）
for _p in 19899 19901 19902 19903; do
    if ! ss -ltn 2>/dev/null | grep -q "127.0.0.1:${_p} \|0.0.0.0:${_p} "; then PORT="${_p}"; break; fi
done

rm -rf "$ROOT"
mkdir -p "$ROOT/backend/docs" "$ROOT/mnt" "$ROOT/etc"
echo "hello from cmccyun fake drive" > "$ROOT/backend/docs/readme.txt"
head -c 3000000 /dev/urandom > "$ROOT/backend/blob.bin"
find "$ROOT/backend" -type f -exec md5sum {} \; > "$ROOT/md5.txt"
chown -R "$RUNUSER" "$ROOT"
chmod 755 "$ROOT" "$ROOT/backend" "$ROOT/mnt"

echo "=== 以 $RUNUSER 身份启动（FakeBackend） ==="
sudo -u "$RUNUSER" env CMCCYUN_DEBUG_FAKE="$ROOT/backend" CMCCYUN_ETC="$ROOT/etc" \
    CMCCYUN_PORT="$PORT" CMCCYUN_MOUNT="$ROOT/mnt" CMCCYUN_LOG="$ROOT/daemon.log" \
    "$BIN" run >"$ROOT/stdout.log" 2>&1 &
sleep 3

echo "=== mountinfo ==="
grep -F " $ROOT/mnt " /proc/self/mountinfo || echo "!! 未挂载"
echo "=== 日志 ==="
cat "$ROOT/daemon.log" 2>/dev/null
cat "$ROOT/stdout.log" 2>/dev/null

echo "=== root 读取 ==="
cat "$ROOT/mnt/docs/readme.txt"
echo "=== 其他用户 ($OTHER_USER) 读取 ==="
sudo -u "$OTHER_USER" cat "$ROOT/mnt/docs/readme.txt"
echo "=== 其他用户写入测试 ==="
if echo test >"$ROOT/mnt/from-other-user.txt" 2>/dev/null; then echo "root 写入成功"; fi
if sudo -u "$OTHER_USER" sh -c "echo hello > $ROOT/mnt/from-other-user2.txt" 2>/dev/null; then
    echo "$OTHER_USER 写入成功"
else
    echo "!! $OTHER_USER 写入失败（权限或 allow_other 问题）"
fi
ls -l "$ROOT/mnt/" 2>/dev/null

echo "=== md5 校验（挂载点 vs 后端） ==="
md5sum "$ROOT/mnt/blob.bin" "$ROOT/backend/blob.bin"

echo "=== 停止并卸载（只针对本次测试的进程） ==="
for PID in $(pgrep -f "CMCCYUN_ETC=$ROOT/etc"); do
    [ "$PID" = "$$" ] && continue
    kill -TERM "$PID" 2>/dev/null
done
sleep 3
if grep -qF " $ROOT/mnt " /proc/self/mountinfo; then
    fusermount3 -u "$ROOT/mnt" 2>/dev/null || umount "$ROOT/mnt" 2>/dev/null
    sleep 1
fi
echo "剩余挂载: $(grep -cF " $ROOT/mnt " /proc/self/mountinfo)"
rm -rf "$ROOT"
