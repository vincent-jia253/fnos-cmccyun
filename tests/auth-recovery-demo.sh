#!/bin/bash
# 端到端验证「鉴权失败 → 立刻强制续期 → 自动重试一次」。
#
#   bash tests/auth-recovery-demo.sh [二进制路径]
#
# 做法：用一个假 139 服务（tools/fake139.py）代替云端——它对**旧令牌**一律返回
# 真实的鉴权错误（code 10001「登录状态已失效」），对 /refresh 发一个新令牌。
# 然后让**真实守护进程**连上它：启动时第一次请求必然被拒，如果自动续期生效，
# 后续请求会用新令牌通过，FUSE 挂载点里就能列出目录内容。
#
# 校验点：
#   1) 假服务日志里出现 REJECT（旧令牌被拒）
#   2) 假服务日志里出现 REFRESH（客户端主动续期）
#   3) 后续请求是 OK（新令牌）
#   4) 守护进程日志里有「[鉴权恢复]」，说明用户可见
#   5) config.json 的 authorization 已被换成新令牌（续期可持久化）
#   6) 挂载点能列出云端内容（重试真的成功了）
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
BIN="${1:-${BIN:-/tmp/cmccyun-build/cmccyun}}"
ROOT=/tmp/cct-auth
PORT=18771
OLD_TOKEN="OLD|||$(python3 -c 'import time;print(int((time.time()+20*86400)*1000))')"
NEW_TOKEN="NEW_$(date +%s)|||$(python3 -c 'import time;print(int((time.time()+30*86400)*1000))')"
OLD_AUTH="$(python3 -c "import base64,sys;print(base64.b64encode(('pc:13800138000:'+sys.argv[1]).encode()).decode())" "$OLD_TOKEN")"
NEW_AUTH="$(python3 -c "import base64,sys;print(base64.b64encode(('pc:13800138000:'+sys.argv[1]).encode()).decode())" "$NEW_TOKEN")"

pass=0; fail=0
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }

[ -x "$BIN" ] || { echo "找不到二进制: $BIN" >&2; exit 1; }

cleanup() {
    pkill -f "fake139.py $PORT" 2>/dev/null
    [ -n "${DAEMON_PID:-}" ] && kill "$DAEMON_PID" 2>/dev/null
    sleep 1
    fusermount3 -u "$ROOT/mnt" 2>/dev/null
    fusermount -u "$ROOT/mnt" 2>/dev/null
}
trap cleanup EXIT

echo "== 0. 环境准备 =="
pkill -f "fake139.py $PORT" 2>/dev/null
fusermount3 -u "$ROOT/mnt" 2>/dev/null
rm -rf "$ROOT"
mkdir -p "$ROOT/etc" "$ROOT/var" "$ROOT/mnt"
cat >"$ROOT/etc/config.json" <<JSON
{
  "authorization": "${OLD_AUTH}",
  "mount_point": "${ROOT}/mnt",
  "read_only": false,
  "stage_dir": "${ROOT}/var/staging",
  "log_file": "${ROOT}/var/cmccyun.log",
  "web_listen": "127.0.0.1",
  "web_port": 19897,
  "auto_mount": true
}
JSON
echo "二进制: $BIN"
echo "旧令牌: pc:13800138000:${OLD_TOKEN:0:12}…（服务端会拒绝它）"

echo "== 1. 启动假 139 服务 =="
nohup python3 "$HERE/../tools/fake139.py" "$PORT" "$OLD_AUTH" "$NEW_AUTH" "$ROOT/fake139.log" \
    >"$ROOT/stub.out" 2>&1 &
sleep 1
curl -s -o /dev/null "http://127.0.0.1:$PORT/" && ok "假服务已监听 :$PORT" || no "假服务未起来"

echo "== 2. 启动真实守护进程（连假服务） =="
CMCCYUN_ROUTE_BASE="http://127.0.0.1:$PORT" \
CMCCYUN_REFRESH_URL="http://127.0.0.1:$PORT/refresh" \
CMCCYUN_ETC="$ROOT/etc" \
    nohup "$BIN" run -c "$ROOT/etc/config.json" >"$ROOT/daemon.out" 2>&1 &
DAEMON_PID=$!
for i in $(seq 1 40); do
    grep -q "$ROOT/mnt" /proc/self/mountinfo 2>/dev/null && break
    sleep 0.3
done
grep -q "$ROOT/mnt" /proc/self/mountinfo 2>/dev/null && ok "已挂载 $ROOT/mnt" || no "未挂载（看 $ROOT/daemon.out）"

echo "== 3. 触发一次 API 调用（ls 挂载点） =="
ls -l "$ROOT/mnt" >"$ROOT/ls.out" 2>&1
cat "$ROOT/ls.out" | sed 's/^/     /'

echo "== 4. 假服务侧记录 =="
sed 's/^/     /' "$ROOT/fake139.log"
grep -q "REJECT" "$ROOT/fake139.log" && ok "旧令牌确实被服务端拒绝" || no "没有出现拒绝，用例无效"
grep -q "REFRESH" "$ROOT/fake139.log" && ok "客户端主动调用了续期接口" || no "客户端未续期"
grep -q "OK " "$ROOT/fake139.log" && ok "续期后有请求用新令牌成功" || no "没有新令牌成功的请求"

echo "== 5. 守护进程日志（用户可见的鉴权恢复） =="
grep "鉴权恢复" "$ROOT/var/cmccyun.log" | sed 's/^/     /' || true
grep -q "\[鉴权恢复\]" "$ROOT/var/cmccyun.log" && ok "日志里有「[鉴权恢复]」记录" || no "日志里没有恢复记录"
grep -q "已用新令牌重试通过" "$ROOT/var/cmccyun.log" && ok "明确记录了「重试通过」" || no "没有重试通过的记录"

echo "== 6. 新令牌是否写回配置 =="
# 写回是异步的（回调在客户端请求内部触发，不能持调用方的锁），等它落地
now_auth=""
for i in $(seq 1 25); do
    now_auth="$(python3 -c "import json;print(json.load(open('$ROOT/etc/config.json'))['authorization'])" 2>/dev/null)"
    [ "$now_auth" = "$NEW_AUTH" ] && break
    sleep 0.2
done
if [ "$now_auth" = "$NEW_AUTH" ]; then ok "config.json 的 authorization 已替换为新令牌"
else no "config.json 未更新（当前 ${now_auth:0:16}…）"; fi

echo "== 7. 挂载点内容（重试是否真的成功） =="
if grep -q "鉴权恢复.txt" "$ROOT/ls.out"; then ok "挂载点列出了云端内容（重试成功）"
else no "挂载点没有云端内容"; fi

echo
echo "RESULT: ${pass} passed, ${fail} failed"
[ "$fail" = 0 ]
