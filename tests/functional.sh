#!/bin/bash
# End-to-end FUSE functional test against the fake backend.
# Usage: functional.sh [mountpoint] [backend]
set -u
M=${1:-/tmp/cct/mnt}
B=${2:-/tmp/cct/backend}
pass=0; fail=0
ok()   { echo "  PASS  $1"; pass=$((pass+1)); }
no()   { echo "  FAIL  $1"; fail=$((fail+1)); }
chk()  { if [ "$2" = "$3" ]; then ok "$1"; else no "$1 (got '$2' want '$3')"; fi; }

echo "== A. read integrity =="
chk "text file content" "$(cat "$M/docs/readme.txt")" "$(cat "$B/docs/readme.txt")"
chk "md5 3MB blob"      "$(md5sum <"$M/media/blob.bin" | cut -d' ' -f1)" "$(md5sum <"$B/media/blob.bin" | cut -d' ' -f1)"
chk "md5 25MB big"      "$(md5sum <"$M/media/big.bin"  | cut -d' ' -f1)" "$(md5sum <"$B/media/big.bin"  | cut -d' ' -f1)"
chk "cmp 25MB big"      "$(cmp "$M/media/big.bin" "$B/media/big.bin" >/dev/null 2>&1 && echo same)" "same"

echo "== B. repeated piped reads (10x, 64K blocks) =="
ref=$(md5sum <"$B/media/big.bin" | cut -d' ' -f1)
badloop=0
for i in $(seq 1 10); do
  h=$(dd if="$M/media/big.bin" bs=65536 2>/dev/null | md5sum | cut -d' ' -f1)
  [ "$h" = "$ref" ] || { badloop=$((badloop+1)); echo "     run$i -> $h"; }
done
chk "10x dd|md5 identical" "$badloop" "0"

echo "== C. parallel reads (4 concurrent md5 of 25MB) =="
rm -f /tmp/_par_*.md5
for i in 1 2 3 4; do ( md5sum <"$M/media/big.bin" | cut -d' ' -f1 >/tmp/_par_$i.md5 ) & done
wait
uniq_count=$(cat /tmp/_par_*.md5 | sort -u | wc -l)
chk "all 4 identical" "$uniq_count" "1"
chk "parallel == ref" "$(cat /tmp/_par_1.md5)" "$ref"

echo "== D. offset reads (python pread sweep) =="
python3 - "$M" "$B" <<'PY'
import os,sys,random
M,B=sys.argv[1],sys.argv[2]
for name in ("media/blob.bin","media/big.bin"):
    ref=open(os.path.join(B,name),'rb').read(); size=len(ref)
    fd=os.open(os.path.join(M,name),os.O_RDONLY)
    assert os.fstat(fd).st_size==size, "size mismatch %s"%name
    bad=0
    for off in range(0,size,131072):
        n=min(131072,size-off)
        if os.pread(fd,n,off)!=ref[off:off+n]: bad+=1
    random.seed(7)
    for _ in range(40):
        off=random.randrange(0,max(1,size-65536)); n=min(65536,size-off)
        if os.pread(fd,n,off)!=ref[off:off+n]: bad+=1
    os.close(fd)
    print("  %-18s sweeps bad=%d" % (name,bad))
PY
chk "pread sweeps clean" "$?" "0"

echo "== E. write operations =="
# Fresh names per run: the kernel/dir cache remembers anything created through
# the mount, so reusing a name would make mkdir report EEXIST (not a bug).
D="newdir_$$"; D2="newdir2_$$"
mkdir "$M/$D" 2>/dev/null && [ -d "$B/$D" ] && ok "mkdir -> backend dir" || no "mkdir"
printf 'hello cmccyun\n' > "$M/$D/a.txt"
chk "create+write -> backend content" "$(cat "$B/$D/a.txt" 2>/dev/null)" "hello cmccyun"
printf 'line2\n' >> "$M/$D/a.txt"
chk "append" "$(wc -c <"$B/$D/a.txt" 2>/dev/null)" "$(wc -c <"$M/$D/a.txt")"
mv "$M/$D/a.txt" "$M/$D/b.txt"
chk "rename visible in backend" "$(cat "$B/$D/b.txt" 2>/dev/null)" "$(printf 'hello cmccyun\nline2')"
mkdir "$M/$D2"
mv "$M/$D"/* "$M/$D2/" 2>/dev/null
[ -f "$B/$D2/b.txt" ] && ok "move across dirs" || no "move across dirs"
rm -f "$M/$D2/b.txt"
[ ! -e "$B/$D2/b.txt" ] && ok "unlink -> backend file gone" || no "unlink"
rmdir "$M/$D2" "$M/$D" 2>/dev/null
[ ! -d "$B/$D2" ] && ok "rmdir -> backend dir gone" || no "rmdir"

echo "== F. upload (12MB copy into mount) =="
head -c 12000000 /dev/urandom > /tmp/_up_src.bin
cp /tmp/_up_src.bin "$M/newdir_up.bin" && ok "cp into mount" || no "cp into mount"
chk "uploaded md5" "$(md5sum <"$B/newdir_up.bin" 2>/dev/null | cut -d' ' -f1)" "$(md5sum </tmp/_up_src.bin | cut -d' ' -f1)"
rm -f "$M/newdir_up.bin" "$B/newdir_up.bin" /tmp/_up_src.bin

echo
echo "== G. in-place edit of a cloud file (overlay write model) =="
# 30MB upload, then patch 18 bytes at 10MB. A correct implementation must not
# copy the whole object into the staging directory (CloudDrive2's model).
STAGE=${STAGE:-/tmp/cct/etc/staging}
PB="patch_$$.bin"
head -c 30000000 /dev/urandom > /tmp/_patch_src.bin
cp /tmp/_patch_src.bin "$M/$PB" && ok "seed 30MB file" || no "seed 30MB file"
sleep 0.5
python3 - "$M/$PB" "$STAGE" >/tmp/_patch_report.txt 2>&1 <<'PY'
import glob, os, sys
path, stage = sys.argv[1], sys.argv[2]
fd = os.open(path, os.O_RDWR)
os.pread(fd, 1 << 20, 0)                      # reads must keep working mid-edit
os.pwrite(fd, b"CMCCYUN-PATCH-2026", 10_000_000)
alloc = 0
for p in glob.glob(os.path.join(stage, "*")):
    try:
        alloc += os.stat(p).st_blocks * 512
    except OSError:
        pass
print("ALLOC %d" % alloc)
os.close(fd)                                  # triggers the upload
PY
awk '{print "  " $0}' /tmp/_patch_report.txt
chk "staging stayed small during 30MB edit" "$(awk '/^ALLOC/{print ($2<1048576)?"small":"big"}' /tmp/_patch_report.txt)" "small"
sleep 1
chk "patched object uploaded intact" "$(python3 - "$B/$PB" <<'PY'
import sys
ref = bytearray(open('/tmp/_patch_src.bin', 'rb').read())
ref[10_000_000:10_000_000 + 18] = b"CMCCYUN-PATCH-2026"
got = open(sys.argv[1], 'rb').read()
if len(got) != len(ref):
    print("size %d vs %d" % (len(got), len(ref))); sys.exit()
bad = sum(1 for i in range(0, len(ref), 65536) if got[i:i + 65536] != bytes(ref[i:i + 65536]))
print("ok" if not bad else "%d blocks differ" % bad)
PY
)" "ok"
chk "read back through the mount matches" "$(cmp -s "$M/$PB" "$B/$PB" && echo same || echo diff)" "same"

echo
echo "== H. truncate through the mount =="
python3 - "$M/$PB" <<'PY'
import os, sys
fd = os.open(sys.argv[1], os.O_RDWR)
os.ftruncate(fd, 5_000_000)
os.close(fd)
PY
sleep 1
chk "truncate reached the backend" "$(stat -c %s "$B/$PB" 2>/dev/null)" "5000000"
chk "truncated content correct" "$(python3 - "$B/$PB" <<'PY'
import sys
ref = open('/tmp/_patch_src.bin', 'rb').read()[:5_000_000]
got = open(sys.argv[1], 'rb').read()
print("ok" if got == ref else "mismatch")
PY
)" "ok"
rm -f "$M/$PB" /tmp/_patch_src.bin /tmp/_patch_report.txt

echo
echo "RESULT: $pass passed, $fail failed"

exit $(( fail > 0 ))
