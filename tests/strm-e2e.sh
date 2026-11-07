#!/bin/bash
# 端到端验证「影视 STRM」的直链模式：用 tools/fake139.py 冒充 139 云端，
# 让**真实守护进程**自己解析云端目录、扫描、生成 .strm，再把 .strm 里的地址
# 直接 curl 一遍。
#
#   bash tests/strm-e2e.sh /tmp/cmccyun-build/cmccyun
#
# 为什么要有这个脚本：v1.7.0 把直链拼成了 `…/play?a=default?id=…&k=…`（两个问号），
# 单元测试当时只看「前缀 + 参数」两段各自对不对，没人把它当**整条 URL** 读回来，
# 结果 31 个 .strm 全废、到真机上才发现。这个脚本按用户的用法走一遍：
#
#   1) /api/strm/config 打开 STRM，映射 云端 /影视 → 本地目录（url 模式）
#   2) /api/strm/scan 扫一轮，产出 电影/测试片.mkv.strm
#   3) 把 .strm 的内容当作 URL 解析：只能有一个 `?`，a / id / k 三个参数都在位
#   4) curl 这个 URL：必须 302 到云端 CDN（假服务给的 /cdn/<fileId>），不是 400
#   5) 再扫一轮：内容没变 → 跳过、mtime 一个都不动（快照幂等）
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
BIN="${1:-${BIN:-/tmp/cmccyun-build/cmccyun}}"
ROOT="${ROOT:-/tmp/cct-strm}"
STUB_PORT="${STUB_PORT:-18781}"   # 假 139 服务
WEB_PORT="${WEB_PORT:-19871}"     # 守护进程自己的控制台
API="http://127.0.0.1:$WEB_PORT"
STUB="http://127.0.0.1:$STUB_PORT"

pass=0; fail=0
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }

[ -x "$BIN" ] || { echo "找不到二进制: $BIN（先 go build -o $BIN .）" >&2; exit 1; }

cleanup() {
    pkill -f "fake139.py $STUB_PORT" 2>/dev/null
    [ -n "${DAEMON_PID:-}" ] && kill "$DAEMON_PID" 2>/dev/null
    sleep 1
}
trap cleanup EXIT

echo "== 0. 环境准备 =="
pkill -f "fake139.py $STUB_PORT" 2>/dev/null
rm -rf "$ROOT"
mkdir -p "$ROOT/etc" "$ROOT/var" "$ROOT/out"
# 令牌结构是 base64("pc:账号:令牌|<b>|RCS|<到期毫秒>|密钥")：有效期给足，别让续期插进来。
TOKEN="E2E|||$(python3 -c 'import time;print(int((time.time()+25*86400)*1000))')|RCS|$(python3 -c 'import time;print(int((time.time()+25*86400)*1000))')|k"
AUTH="$(python3 -c "import base64,sys;print(base64.b64encode(('pc:****0001:'+sys.argv[1]).encode()).decode())" "$TOKEN")"
cat >"$ROOT/etc/config.json" <<JSON
{
  "authorization": "${AUTH}",
  "mount_point": "",
  "read_only": false,
  "auto_mount": false,
  "stage_dir": "${ROOT}/var/staging",
  "log_file": "${ROOT}/var/cmccyun.log",
  "web_listen": "127.0.0.1",
  "web_port": ${WEB_PORT},
  "web_auth_disabled": true,
  "dav_enabled": false
}
JSON
echo "二进制: $BIN"

echo "== 1. 启动假 139 服务 =="
nohup python3 "$HERE/../tools/fake139.py" "$STUB_PORT" "$AUTH" "$AUTH" "$ROOT/fake139.log" \
    >"$ROOT/stub.out" 2>&1 &
sleep 1
curl -s -o /dev/null "http://127.0.0.1:$STUB_PORT/" && ok "假服务已监听 :$STUB_PORT" || { no "假服务未起来"; exit 1; }

echo "== 2. 启动真实守护进程（url 模式，不挂 FUSE） =="
CMCCYUN_ROUTE_BASE="$STUB" \
CMCCYUN_REFRESH_URL="$STUB/refresh" \
CMCCYUN_ETC="$ROOT/etc" \
    nohup "$BIN" run -c "$ROOT/etc/config.json" >"$ROOT/daemon.out" 2>&1 &
DAEMON_PID=$!
ready=0
for i in $(seq 1 40); do
    code=$(curl -s -o /dev/null -w '%{http_code}' "$API/api/status" 2>/dev/null)
    [ "$code" = "200" ] && { ready=1; break; }
    sleep 0.3
done
[ "$ready" = "1" ] && ok "控制台已就绪" || { no "控制台没起来（看 $ROOT/daemon.out）"; exit 1; }

echo "== 3. 打开 STRM：云端 /影视 → ${ROOT}/out（url 模式） =="
body='{"enabled":true,"mode":"url","interval_min":60,"pairs":[{"cloud":"/影视","local":"'"$ROOT"'/out"}]}'
code=$(curl -s -o "$ROOT/cfg.json" -w '%{http_code}' -X POST "$API/api/strm/config" \
        -H 'Content-Type: application/json' -d "$body")
[ "$code" = "200" ] && ok "STRM 配置已保存" || { no "STRM 配置失败：HTTP $code $(cat "$ROOT/cfg.json")"; exit 1; }

echo "== 4. 扫一轮 =="
code=$(curl -s -o "$ROOT/scan.json" -w '%{http_code}' -X POST "$API/api/strm/scan")
[ "$code" = "200" ] && ok "扫描接口返回 200" || { no "扫描失败：HTTP $code $(cat "$ROOT/scan.json")"; exit 1; }
python3 - "$ROOT/scan.json" "$ROOT/out/电影/测试片.mkv.strm" <<'PY'
import json,sys,os
d=json.load(open(sys.argv[1]))
r=(d.get("results") or [{}])[0]
print("    结果：total=%s added=%s updated=%s err=%r" % (r.get("total"), r.get("added"), r.get("updated"), r.get("error")))
ok = d.get("ok") and (r.get("total") or 0) == 1 and not r.get("error")
sys.exit(0 if ok else 1)
PY
[ $? -eq 0 ] && ok "云端只扫出 1 个视频、没有报错" || no "扫描结果不对（看 $ROOT/scan.json）"

STRM="$ROOT/out/电影/测试片.mkv.strm"
[ -f "$STRM" ] && ok "生成了 .strm（按云端目录层级镜像）" || { no "没生成 $STRM"; ls -R "$ROOT/out"; }

echo "== 5. 把 .strm 当 URL 读回来 =="
python3 - "$STRM" <<'PY'
import sys
from urllib.parse import urlparse, parse_qs
u=open(sys.argv[1]).read().strip()
print("    内容：%s" % u)
bad=[]
if u.count("?") != 1: bad.append("问号 %d 个（应 1 个）" % u.count("?"))
parsed=urlparse(u)
if not parsed.path.endswith("/api/strm/play"): bad.append("路径不是 /api/strm/play")
q=parse_qs(parsed.query)
if q.get("id") != ["file-v"]: bad.append("id=%r" % q.get("id"))
if q.get("a") != ["default"]: bad.append("a=%r" % q.get("a"))
if not (q.get("k") or [""])[0]: bad.append("缺 k 口令")
if len(q) != 3: bad.append("参数 %d 个（应 3 个：a/id/k）" % len(q))
if bad:
    print("    ❌ " + "；".join(bad)); sys.exit(1)
PY
[ $? -eq 0 ] && ok "直链格式正确（单问号 + a/id/k）" || no "直链格式不对（v1.7.0 的回归点）"

echo "== 6. 真的点开它 =="
# .strm 里写的是本机对外的局域网地址（给播放器/影视用）；测试实例只监听
# 127.0.0.1，所以把 host 换回本地再 curl —— 路径和参数一个不改。
URL=$(cat "$STRM" | sed -E "s#^http://[^/]+#http://127.0.0.1:$WEB_PORT#")
code=$(curl -s -o /dev/null -w '%{http_code}' "$URL")
loc=$(curl -s -o /dev/null -w '%{redirect_url}' "$URL")
[ "$code" = "302" ] && ok "302（播放器能拿到云盘直链）" || no "期望 302，实际 $code"
echo "$loc" | grep -q "/cdn/file-v" && ok "跳到了云端 CDN：$loc" || no "跳转地址不对：$loc"
badk="${URL%%k=*}k=wrong"
code=$(curl -s -o /dev/null -w '%{http_code}' "$badk")
[ "$code" = "403" ] && ok "口令错 → 403" || no "口令错应 403，实际 $code"

echo "== 7. 再扫一轮：内容没变就不该动文件 =="
before=$(find "$ROOT/out" -name '*.strm' -printf '%T@ %p\n' | sort | md5sum | cut -d' ' -f1)
sleep 1
curl -s -o "$ROOT/scan2.json" -X POST "$API/api/strm/scan"
after=$(find "$ROOT/out" -name '*.strm' -printf '%T@ %p\n' | sort | md5sum | cut -d' ' -f1)
[ "$before" = "$after" ] && ok "mtime 一个都没动" || no "第二轮又把 .strm 重写了（媒体库会整库重扫）"
skipped=$(python3 -c "import json;d=json.load(open('$ROOT/scan2.json'));print((d.get('results') or [{}])[0].get('skipped'))")
[ "$skipped" = "1" ] && ok "第二轮判定「未变 1」" || no "第二轮 skipped=$skipped（应 1）"

echo "== 8. 关闭 STRM 后接口立刻返回 503/停用 =="
curl -s -X POST "$API/api/strm/config" -H 'Content-Type: application/json' -d '{"enabled":false}' >/dev/null
on=$(curl -s "$API/api/strm/status" | python3 -c "import json,sys;print(json.load(sys.stdin).get('strm',{}).get('enabled'))")
[ "$on" = "False" ] && ok "已关闭" || no "关闭失败：enabled=$on"

echo
echo "RESULT: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
