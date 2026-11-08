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
#   6) WebDAV 凭据是分账号的：/dav/ 只认默认账号，/dav/<id>/ 只认它自己的；
#      无凭据或串号一律 401（fake 模式没有云端 client，dav.FS 是空的，所以这里只断言
#      「鉴权过没过」，不断言 PROPFIND 的返回体）
#   7) 「全部重挂」逐台重登重挂、逐台汇报结果
#   8) 「按账号导出」只导一台；默认不带 authorization/dav_password，include_auth=1 才带
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
mkdir -p "$ROOT/etc" "$ROOT/var/staging" "$ROOT/mnt1" "$ROOT/mnt2" "$ROOT/strm2" \
         "$ROOT/backend/电影" "$ROOT/backend/文档"
head -c 1048576 /dev/urandom >"$ROOT/backend/电影/测试片.mkv"
printf 'hello multi-account\n' >"$ROOT/backend/文档/readme.txt"

ACC2="acc$(python3 -c 'import secrets;print(secrets.token_hex(6))')"
MYUID=$(id -u); MYGID=$(id -g)
cat >"$ROOT/etc/config.json" <<JSON
{
  "accounts": [
    {"id": "default", "mount_point": "${ROOT}/mnt1", "read_only": false,
     "authorization": "cGM6MTM4MDAwMDAwMDA6dG9rZW4=", "dav_user": "u-one", "dav_password": "p-one"},
    {"id": "${ACC2}", "mount_point": "${ROOT}/mnt2", "read_only": true,
     "authorization": "cGM6MTM4MDAwMDAwMDA6dG9rZW4=", "dav_user": "u-two", "dav_password": "p-two",
     "strm_enabled": true, "strm_mode": "path", "strm_interval_min": 5,
     "strm_pairs": [{"cloud": "/", "local": "${ROOT}/strm2"}]}
  ],
  "default_account": "default",
  "dav_enabled": true,
  "uid": ${MYUID},
  "gid": ${MYGID},
  "file_perm": "0644",
  "dir_perm": "0755",
  "stage_dir": "${ROOT}/var/staging",
  "log_file": "${ROOT}/var/cmccyun.log",
  "web_listen": "127.0.0.1",
  "web_port": ${WEB_PORT},
  "web_auth_disabled": true,
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

echo "== 4c. WebDAV 凭据是分账号的 =="
dav() { curl -s -o /dev/null -w '%{http_code}' -X PROPFIND "$@"; }
chk "/dav/ 无凭据 → 401" "$(dav "$API/dav/")" "401"
c=$(dav -u u-one:p-one "$API/dav/")
if [ "$c" != "401" ]; then ok "/dav/ 收默认账号凭据（$c，非 401）"; else no "/dav/ 拒了默认账号凭据"; fi
chk "/dav/ 收第二账号凭据 → 401（不能串号）" "$(dav -u u-two:p-two "$API/dav/")" "401"
c=$(dav -u u-two:p-two "$API/dav/$ACC2/")
if [ "$c" != "401" ]; then ok "/dav/<第二账号>/ 收自己的凭据（$c，非 401）"; else no "/dav/<第二账号>/ 拒了自己的凭据"; fi
chk "/dav/<第二账号>/ 收默认账号凭据 → 401" "$(dav -u u-one:p-one "$API/dav/$ACC2/")" "401"
chk "/dav/<第二账号>/ 无凭据 → 401" "$(dav "$API/dav/$ACC2/")" "401"
c=$(dav -u u-one:p-one "$API/dav/01影视/")
if [ "$c" != "401" ]; then ok "/dav/01影视/ 落回默认账号（$c，非 401）"; else no "/dav/01影视/ 被当成账号 ID 拒了"; fi

echo "== 4d. 全部重挂 =="
curl -s -X POST "$API/api/accounts" -H 'Content-Type: application/json' \
     -d '{"action":"remount_all"}' >"$ROOT/remount_all.json"
python3 - "$ROOT/remount_all.json" "$ACC2" >"$ROOT/rma_checks.txt" <<'PYEOF'
import json, sys
d = json.load(open(sys.argv[1]))
acc2 = sys.argv[2]
def chk(n, c): print(("PASS  " if c else "FAIL  ") + n)
rs = d.get("results") or []
chk("remount_all 返回 ok", d.get("ok") is True)
chk("逐台汇报（2 条）", len(rs) == 2)
chk("两台都重挂成功", bool(rs) and all(r.get("ok") for r in rs)
    and {r.get("id") for r in rs} == {"default", acc2})
PYEOF
sed 's/^/  /' "$ROOT/rma_checks.txt"
pc=$(grep -c '^PASS' "$ROOT/rma_checks.txt" || true); pc=${pc:-0}
fc=$(grep -c '^FAIL' "$ROOT/rma_checks.txt" || true); fc=${fc:-0}
pass=$((pass + pc)); fail=$((fail + fc))
chk "重挂后 mnt1 还在" "$(inmount "$ROOT/mnt1")" "yes"
chk "重挂后 mnt2 还在" "$(inmount "$ROOT/mnt2")" "yes"

echo "== 4e. 按账号导出配置 =="
curl -s -D "$ROOT/exp.hdr" -o "$ROOT/exp.json" "$API/api/accounts/export?id=$ACC2"
python3 - "$ROOT/exp.json" "$ROOT/exp.hdr" "$ACC2" >"$ROOT/exp_checks.txt" <<'PYEOF'
import json, sys
body = json.load(open(sys.argv[1]))
hdr = open(sys.argv[2], encoding="utf-8", errors="replace").read()
acc2 = sys.argv[3]
def chk(n, c): print(("PASS  " if c else "FAIL  ") + n)
a = body.get("account") or {}
chk("导出带 app/schema 标记", body.get("app") == "cmccyun" and body.get("schema") == 1)
chk("导出的就是指定账号", a.get("id") == acc2)
chk("默认不含 authorization", not a.get("authorization"))
chk("默认不含 dav_password", not a.get("dav_password"))
chk("注明省略了哪些凭据", set(body.get("secrets_omitted") or []) == {"authorization", "dav_password"})
chk("下载文件名带账号 ID", "cmccyun-" + acc2 + ".json" in hdr)
PYEOF
sed 's/^/  /' "$ROOT/exp_checks.txt"
pc=$(grep -c '^PASS' "$ROOT/exp_checks.txt" || true); pc=${pc:-0}
fc=$(grep -c '^FAIL' "$ROOT/exp_checks.txt" || true); fc=${fc:-0}
pass=$((pass + pc)); fail=$((fail + fc))
curl -s "$API/api/accounts/export?id=$ACC2&include_auth=1" >"$ROOT/exp2.json"
python3 - "$ROOT/exp2.json" >"$ROOT/exp2_checks.txt" <<'PYEOF'
import json, sys
b = json.load(open(sys.argv[1])); a = b.get("account") or {}
def chk(n, c): print(("PASS  " if c else "FAIL  ") + n)
chk("include_auth=1 带出 authorization", bool(a.get("authorization")))
chk("include_auth=1 带出 dav_password", bool(a.get("dav_password")))
chk("含凭据时带 warning", isinstance(b.get("warning"), str) and bool(b.get("warning")))
PYEOF
sed 's/^/  /' "$ROOT/exp2_checks.txt"
pc=$(grep -c '^PASS' "$ROOT/exp2_checks.txt" || true); pc=${pc:-0}
fc=$(grep -c '^FAIL' "$ROOT/exp2_checks.txt" || true); fc=${fc:-0}
pass=$((pass + pc)); fail=$((fail + fc))

echo "== 4f. 单账号日志 + 单账号 STRM 扫描 =="
curl -s "$API/api/logs?n=300&account=$ACC2" >"$ROOT/logs2.json"
python3 - "$ROOT/logs2.json" "$ROOT" "$ACC2" >"$ROOT/logs_checks.txt" <<'LOGCHK'
import json, sys
d = json.load(open(sys.argv[1]))
root, acc2 = sys.argv[2], sys.argv[3]
def chk(n, c): print(("PASS  " if c else "FAIL  ") + n)
lines = d.get("lines") or []
chk("日志接口标了 filtered", d.get("filtered") is True)
chk("按账号取到日志行", len(lines) > 0)
chk("不含另一台账号的挂载点", not any(root + "/mnt1" in ln for ln in lines))
chk("行里都提到这台账号的线索",
    all((acc2 in ln) or (root + "/mnt2" in ln) or ("u-two" in ln) or (root + "/strm2" in ln)
        for ln in lines))
LOGCHK
sed 's/^/  /' "$ROOT/logs_checks.txt"
pc=$(grep -c '^PASS' "$ROOT/logs_checks.txt" || true); pc=${pc:-0}
fc=$(grep -c '^FAIL' "$ROOT/logs_checks.txt" || true); fc=${fc:-0}
pass=$((pass + pc)); fail=$((fail + fc))

curl -s -X POST "$API/api/strm/scan?account=$ACC2" -o "$ROOT/scan2.json"
# 注意：这个脚本走 CMCCYUN_DEBUG_FAKE，没有云端 client，所以扫描只能验「打到哪台」，
# 不能验「生成 .strm」（那部分由 strm-e2e.sh 用假 139 服务覆盖）。
python3 - "$ROOT/scan2.json" "$ROOT" "$ACC2" >"$ROOT/scan_checks.txt" <<'SCANCHK'
import json, os, sys
d = json.load(open(sys.argv[1]))
root, acc2 = sys.argv[2], sys.argv[3]
def chk(n, c): print(("PASS  " if c else "FAIL  ") + n)
st = d.get("strm") or {}
chk("scan?account 只作用在第二台（strm.account）", st.get("account") == acc2)
chk("扫描触发了这台账号的定时器状态（last_at 落上）", bool(st.get("last_at")))
chk("没有云端 client 时如实报错（不假装成功）", d.get("ok") is False and "云盘" in (d.get("error") or ""))
chk("默认账号没被牵连（没有 strm1 输出）", not os.path.exists(os.path.join(root, "strm1")))
SCANCHK
sed 's/^/  /' "$ROOT/scan_checks.txt"
pc=$(grep -c '^PASS' "$ROOT/scan_checks.txt" || true); pc=${pc:-0}
fc=$(grep -c '^FAIL' "$ROOT/scan_checks.txt" || true); fc=${fc:-0}
pass=$((pass + pc)); fail=$((fail + fc))

echo "== 4g. 全部账号的 STRM 总览 =="
curl -s "$API/api/strm/accounts" >"$ROOT/strm_all.json"
python3 - "$ROOT/strm_all.json" "$ACC2" >"$ROOT/strm_all_checks.txt" <<'STRMALL'
import json, sys
d = json.load(open(sys.argv[1])); acc2 = sys.argv[2]
def chk(n, c): print(("PASS  " if c else "FAIL  ") + n)
rows = d.get("accounts") or []
by = {r.get("id"): r for r in rows}
chk("总览返回两台", len(rows) == 2)
chk("第二台标着已启用 STRM", by.get(acc2, {}).get("enabled") is True)
chk("默认账号没开 STRM", by.get("default", {}).get("enabled") is False)
chk("第二台映射 1 条、模式 path", by.get(acc2, {}).get("pairs") == 1 and by.get(acc2, {}).get("mode") == "path")
chk("每行都带自己的扫描状态字段", all("last_at" in r and "strm_count" in r for r in rows))
STRMALL
sed 's/^/  /' "$ROOT/strm_all_checks.txt"
pc=$(grep -c '^PASS' "$ROOT/strm_all_checks.txt" || true); pc=${pc:-0}
fc=$(grep -c '^FAIL' "$ROOT/strm_all_checks.txt" || true); fc=${fc:-0}
pass=$((pass + pc)); fail=$((fail + fc))

echo "== 4h. 全部账号的 WebDAV 总览 =="
curl -s "$API/api/dav/accounts" >"$ROOT/dav_all.json"
python3 - "$ROOT/dav_all.json" "$ACC2" >"$ROOT/dav_all_checks.txt" <<'DAVCHK'
import json, sys
d = json.load(open(sys.argv[1])); acc2 = sys.argv[2]
def chk(n, c): print(("PASS  " if c else "FAIL  ") + n)
rows = d.get("accounts") or []
by = {r.get("id"): r for r in rows}
chk("总览返回两台", len(rows) == 2)
chk("默认账号沿用 /dav/", by.get("default", {}).get("path") == "/dav/")
chk("第二账号用自己的 /dav/<id>/", by.get(acc2, {}).get("path") == "/dav/" + acc2 + "/")
chk("两台用户名各自独立", by.get("default", {}).get("user") == "u-one"
    and by.get(acc2, {}).get("user") == "u-two")
chk("每行都带密码/读写/下载三项", all(
    ("password_set" in r and "read_only" in r and "redirect" in r) for r in rows))
DAVCHK
sed 's/^/  /' "$ROOT/dav_all_checks.txt"
pc=$(grep -c '^PASS' "$ROOT/dav_all_checks.txt" || true); pc=${pc:-0}
fc=$(grep -c '^FAIL' "$ROOT/dav_all_checks.txt" || true); fc=${fc:-0}
pass=$((pass + pc)); fail=$((fail + fc))

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
