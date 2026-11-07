#!/bin/bash
# 控制台访问密码端到端（v1.8.0）：真的起一个守护进程，用 curl 走登录门。
#
#   bash tests/console-auth-e2e.sh [二进制路径]
#
# 校验点：
#   1) 未登录：页面 302 到 /login；/api/* 一律 401
#   2) 默认密码 admin/admin 能登录，登录后带 cookie 访问 /api/status 通
#   3) 当前密码填错不能改密码；改成功后旧 cookie 立刻失效、旧密码登不进、新密码能登
#   4) config.json 里只存 PBKDF2 哈希，不含明文
#   5) /dav/ 与 /api/strm/play 不受控制台密码影响（各有各的凭据）
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
BIN="${1:-${BIN:-/tmp/cmccyun-build/cmccyun}}"
ROOT="${ROOT:-/tmp/cct-auth-console}"
WEB_PORT="${WEB_PORT:-19961}"
API="http://127.0.0.1:$WEB_PORT"
JAR="$ROOT/cookies.txt"

pass=0; fail=0
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }
chk() { if [ "$2" = "$3" ]; then ok "$1"; else no "$1（得到 '$2'，期望 '$3'）"; fi; }

[ -x "$BIN" ] || { echo "找不到二进制: $BIN" >&2; exit 1; }

cleanup() {
    [ -n "${DAEMON_PID:-}" ] && kill "$DAEMON_PID" 2>/dev/null
    sleep 0.5
    fusermount3 -u "$ROOT/mnt" 2>/dev/null
    fusermount -u "$ROOT/mnt" 2>/dev/null
}
trap cleanup EXIT

echo "== 0. 环境准备（登录门开着的配置）=="
rm -rf "$ROOT"; mkdir -p "$ROOT/etc" "$ROOT/var/staging" "$ROOT/mnt" "$ROOT/backend"
printf 'hello console auth\n' >"$ROOT/backend/readme.txt"
MYUID=$(id -u); MYGID=$(id -g)
cat >"$ROOT/etc/config.json" <<JSON
{
  "accounts": [{"id": "default", "mount_point": "${ROOT}/mnt", "read_only": false}],
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
  "auto_mount": false
}
JSON
CMCCYUN_ETC="$ROOT/etc" CMCCYUN_DEBUG_FAKE="$ROOT/backend" \
    nohup "$BIN" run -c "$ROOT/etc/config.json" >"$ROOT/daemon.out" 2>&1 &
DAEMON_PID=$!
for i in $(seq 1 60); do
    curl -s -o /dev/null "$API/login" && break
    sleep 0.3
done
curl -s -o /dev/null "$API/login" && ok "守护进程已就绪" || { no "守护进程没起来"; tail -5 "$ROOT/daemon.out" | sed 's/^/     /'; exit 1; }

echo "== 1. 未登录：页面跳登录页，接口 401 =="
code=$(curl -s -o /dev/null -w '%{http_code}' "$API/")
loc=$(curl -s -D - -o /dev/null "$API/" | tr -d '\r' | awk -F': ' 'tolower($1)=="location"{print $2}')
chk "GET / 未登录 → 302" "$code" "302"
case "$loc" in */login*) ok "跳转到 /login（$loc）";; *) no "没有跳 /login（$loc）";; esac
chk "GET /login 可访问" "$(curl -s -o /dev/null -w '%{http_code}' "$API/login")" "200"
if curl -s "$API/login" | grep -q "控制台"; then ok "登录页有中文标题"; else no "登录页内容不对"; fi
chk "未登录 GET /api/status → 401" "$(curl -s -o /dev/null -w '%{http_code}' "$API/api/status")" "401"

echo "== 2. 默认密码 admin/admin =="
chk "错误密码 → 401" "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    -d '{"user":"admin","password":"nope"}' "$API/api/login")" "401"
rm -f "$JAR"
code=$(curl -s -o /dev/null -w '%{http_code}' -c "$JAR" -X POST -H 'Content-Type: application/json' \
    -d '{"user":"admin","password":"admin"}' "$API/api/login")
chk "默认密码登录 → 200" "$code" "200"
grep -q "cmcc_auth" "$JAR" && ok "下发了会话 cookie" || no "没有 cookie"
chk "带 cookie 访问 /api/status → 200" "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' "$API/api/status")" "200"
if curl -s -b "$JAR" "$API/api/status" | python3 -c "
import json,sys
d=json.load(sys.stdin); c=d.get('config') or {}
sys.exit(0 if c.get('web_password_set') is False and c.get('web_user')=='admin' else 1)"; then
    ok "状态里标着「还在用默认密码」"
else
    no "状态里的 web_password_set / web_user 不对"
fi

echo "== 3. 改密码：旧会话失效、旧密码登不进、新密码能用 =="
chk "当前密码错 → 400" "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    -d '{"current":"wrong","password":"s3cret","confirm":"s3cret"}' "$API/api/console-password")" "400"
chk "两次不一致 → 400" "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    -d '{"current":"admin","password":"s3cret","confirm":"s3cre"}' "$API/api/console-password")" "400"
chk "改密码 → 200" "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    -d '{"current":"admin","password":"s3cret","confirm":"s3cret"}' "$API/api/console-password")" "200"
chk "旧 cookie 立刻失效 → 401" "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' "$API/api/status")" "401"
chk "旧密码 admin → 401" "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    -d '{"user":"admin","password":"admin"}' "$API/api/login")" "401"
rm -f "$JAR"
chk "新密码登录 → 200" "$(curl -s -c "$JAR" -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    -d '{"user":"admin","password":"s3cret"}' "$API/api/login")" "200"
chk "新 cookie 能访问 → 200" "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' "$API/api/status")" "200"

echo "== 4. 配置里只存哈希 =="
python3 - "$ROOT/etc/config.json" >"$ROOT/hash_checks.txt" <<'PY'
import json, sys
cfg = json.load(open(sys.argv[1]))
h = cfg.get("web_password", "")
def chk(name, cond):
    print(("PASS  " if cond else "FAIL  ") + name)
chk("存的是 pbkdf2-sha256 哈希", h.startswith("pbkdf2-sha256$"))
chk("配置里没有明文密码", "s3cret" not in h)
chk("哈希字段四段（算法$迭代$盐$摘要）", len(h.split("$")) == 4)
PY
sed 's/^/  /' "$ROOT/hash_checks.txt"
pc=$(grep -c '^PASS' "$ROOT/hash_checks.txt" || true); pc=${pc:-0}
fc=$(grep -c '^FAIL' "$ROOT/hash_checks.txt" || true); fc=${fc:-0}
pass=$((pass + pc)); fail=$((fail + fc))

echo "== 5. /dav/ 与 STRM 直链不受控制台密码影响 =="
c=$(curl -s -o /dev/null -w '%{http_code}' -X PROPFIND "$API/dav/")
[ "$c" != "302" ] && ok "/dav/ 没有被登录门重定向（$c）" || no "/dav/ 被登录门拦了"
body=$(curl -s "$API/api/strm/play?id=x&k=y")
case "$body" in *"需要登录控制台"*) no "/api/strm/play 被登录门拦了";; *) ok "/api/strm/play 直链自己处理凭据";; esac

echo
echo "RESULT: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
