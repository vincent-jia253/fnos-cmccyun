#!/bin/bash
# 多账号端到端：**一个进程同时挂两棵 FUSE 树**（假后端，不需要真云盘）。
#
#   bash tests/multi-account-e2e.sh [二进制路径]
#
# 校验点：
#   1) 两台账号各自的挂载点同时出现在 /proc/self/mountinfo 里（不是先后挂、不是覆盖）
#   2) 两棵都读得动：目录列表一致、dd 64K 都是 65536 字节（同一份假后端，两个挂载）
#   3) 只读账号写不进去、读写账号能写（read_only 是分账号的，不是全局开关）
#   4) /api/status 一次报出两台（mounted / read_only / mount_point 各自正确），
#      ?account=<id> 只描述那一台
#   5) 删除第二台：只卸它自己，第一台仍然挂着；mounted_at 里的记录一并清掉
#
# 说明：WebDAV 的分账号凭据在这里测不了 —— dav.FS 是从 api.Client 建的，而本脚本
# 走的是 CMCCYUN_DEBUG_FAKE（没有云端 client）。那部分由单测 TestDAVDispatchPerAccount
# 与真机手动验证覆盖。
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
BIN="${1:-${BIN:-/tmp/cmccyun-build/cmccyun}}"
ROOT="${ROOT:-/tmp/cct-multi}"
WEB_PORT="${WEB_PORT:-19952}"     # 避开 19896/19897/19898/19899/19960
API="http://127.0.0.1:$WEB_PORT"

pass=0; fail=0
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }

[ -x "$BIN" ] || { echo "找不到二进制: $BIN（先 go build -o /tmp/cmccyun-build/cmccyun .）" >&2; exit 1; }

cleanup() {
    [ -n "${DAEMON_PID:-}" ] && kill "$DAEMON_PID" 2>/dev/null
    sleep 1
    for m in "$ROOT/mnt1" "$ROOT/mnt2"; do
        fusermount3 -u "$m" 2>/dev/null
        fusermount -u "$m" 2>/dev/null
    done
}
trap cleanup EXIT

echo "== 0. 环境准备 =="
for m in "$ROOT/mnt1" "$ROOT/mnt2"; do
    fusermount3 -u "$m" 2>/dev/null
    fusermount -u "$m" 2>/dev/null
done
rm -rf "$ROOT"
mkdir -p "$ROOT/etc" "$ROOT/var/staging" "$ROOT/mnt1" "$ROOT/mnt2" \
         "$ROOT/backend/电影" "$ROOT/backend/文档"
head -c 1048576 /dev/urandom >"$ROOT/backend/电影/测试片.mkv"
printf 'hello multi-account\n' >"$ROOT/backend/文档/readme.txt"

ACC2="acc$(python3 -c 'import secrets;print(secrets.token_hex(6))')"
MYUID=$(id -u); MYGID=$(id -g)
cat >"$ROOT/etc/config.json" <<JSON
{
  "accounts": [
    {"id": "default", "mount_point": "${ROOT}/mnt1", "read_only": false},
    {"id": "${ACC2}", "mount_point": "${ROOT}/mnt2", "read_only": true}
  ],
  "default_account": "default",
  "dav_enabled": false,
  "uid": ${MYUID},
  "gid": ${MYGID},
  "file_perm": "0644",
  "dir_perm": "0755",
  "stage_dir": "${ROOT}/var/staging",
  "log_file": "${ROOT}/var/cmccyun.log",
  "web_listen": "127.0.0.1",
  "web_port": ${WEB_PORT},
  "auto_mount": true
}
JSON
echo "二进制: $BIN"
echo "第二账号 ID: $ACC2"

echo "== 1. 启动（假后端，自动挂载两台） =="
CMCCYUN_ETC="$ROOT/etc" \
CMCCYUN_DEBUG_FAKE="$ROOT/backend" \
    nohup "$BIN" run -c "$ROOT/etc/config.json" >"$ROOT/daemon.out" 2>&1 &
DAEMON_PID=$!
for i in $(seq 1 60); do
    a=$(grep -cF "$ROOT/mnt1" /proc/self/mountinfo 2>/dev/null)
    b=$(grep -cF "$ROOT/mnt2" /proc/self/mountinfo 2>/dev/null)
    [ "${a:-0}" -ge 1 ] && [ "${b:-0}" -ge 1 ] && break
    sleep 0.3
done
curl -s -o /dev/null "$API/" && ok "控制台已就绪" || { no "控制台没起来"; tail -5 "$ROOT/daemon.out" | sed 's/^/     /'; exit 1; }

inmount() { grep -qF "$1" /proc/self/mountinfo 2>/dev/null && echo yes || echo no; }
chk() { if [ "$2" = "$3" ]; then ok "$1"; else no "$1（得到 '$2'，期望 '$3'）"; fi; }

chk "mnt1 已挂载" "$(inmount "$ROOT/mnt1")" "yes"
chk "mnt2 已挂载" "$(inmount "$ROOT/mnt2")" "yes"
n_mount=$(grep -cF "$ROOT/mnt" /proc/self/mountinfo 2>/dev/null)
chk "同一进程里两棵 FUSE 树并存" "$n_mount" "2"

echo "== 2. 两棵都读得动 =="
chk "mnt1 目录列表" "$(ls "$ROOT/mnt1" | tr '\n' ' ')" "文档 电影 "
chk "mnt2 目录列表" "$(ls "$ROOT/mnt2" | tr '\n' ' ')" "文档 电影 "
chk "mnt1 读 64K" "$(dd if="$ROOT/mnt1/电影/测试片.mkv" bs=65536 count=1 2>/dev/null | wc -c)" "65536"
chk "mnt2 读 64K" "$(dd if="$ROOT/mnt2/电影/测试片.mkv" bs=65536 count=1 2>/dev/null | wc -c)" "65536"
chk "mnt1 文本内容" "$(cat "$ROOT/mnt1/文档/readme.txt")" "hello multi-account"

echo "== 3. 只读是分账号的 =="
if touch "$ROOT/mnt2/probe" 2>/dev/null; then no "只读账号竟然能写"; rm -f "$ROOT/mnt2/probe"; else ok "只读账号写不进去"; fi
if touch "$ROOT/mnt1/probe" 2>/dev/null; then ok "读写账号能写"; rm -f "$ROOT/mnt1/probe"; else no "读写账号写不进去"; fi

echo "== 4. /api/status 一次报两台 =="
curl -s "$API/api/status" >"$ROOT/status.json"
python3 - "$ROOT/status.json" "$ROOT" "$ACC2" >"$ROOT/status_checks.txt" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
root, acc2 = sys.argv[2], sys.argv[3]
def chk(name, cond):
    print(("PASS  " if cond else "FAIL  ") + name)
accts = d.get("accounts") or []
chk("accounts 长度 2", len(accts) == 2)
by = {a.get("id"): a for a in accts}
def_m = by.get("default", {})
two = by.get(acc2, {})
chk("默认账号挂着 mnt1", def_m.get("mount_point") == root + "/mnt1")
chk("默认账号 mounted", def_m.get("mounted") is True)
chk("第二账号挂着 mnt2", two.get("mount_point") == root + "/mnt2")
chk("第二账号 mounted", two.get("mounted") is True)
chk("第二账号只读", two.get("read_only") is True)
chk("默认账号读写", def_m.get("read_only") is False)
PY
sed 's/^/  /' "$ROOT/status_checks.txt"
pc=$(grep -c '^PASS' "$ROOT/status_checks.txt" 2>/dev/null || true); pc=${pc:-0}
fc=$(grep -c '^FAIL' "$ROOT/status_checks.txt" 2>/dev/null || true); fc=${fc:-0}
pass=$((pass + pc)); fail=$((fail + fc))

echo "== 4b. ?account= 只描述那一台 =="
m2=$(curl -s "$API/api/status?account=$ACC2" | python3 -c "import json,sys;print(json.load(sys.stdin)['config'].get('mount_point'))")
chk "第二账号的 mount_point" "$m2" "$ROOT/mnt2"

echo "== 5. 删除第二台：只卸它自己 =="
curl -s -X POST "$API/api/accounts" -H 'Content-Type: application/json' \
     -d "{\"action\":\"delete\",\"id\":\"$ACC2\"}" >"$ROOT/del.json"
python3 -c "import json,sys;d=json.load(open('$ROOT/del.json'));sys.exit(0 if d.get('ok') else 1)" \
    && ok "删除接口返回 ok" || { no "删除失败"; sed 's/^/     /' "$ROOT/del.json"; }
sleep 2
chk "mnt2 已卸载" "$(inmount "$ROOT/mnt2")" "no"
chk "mnt1 还挂着" "$(inmount "$ROOT/mnt1")" "yes"
chk "mnt1 仍可读 64K" "$(dd if="$ROOT/mnt1/电影/测试片.mkv" bs=65536 count=1 2>/dev/null | wc -c)" "65536"
if [ -f "$ROOT/etc/mounted_at" ]; then
    if grep -qF "$ACC2" "$ROOT/etc/mounted_at"; then no "mounted_at 里还留着被删账号"; else ok "mounted_at 已清掉被删账号"; fi
else
    ok "mounted_at 不存在（没有残留记录）"
fi
left=$(curl -s "$API/api/status" | python3 -c "import json,sys;print(json.load(sys.stdin)['config'].get('account_count'))")
chk "剩 1 条账号" "$left" "1"

echo
echo "RESULT: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
