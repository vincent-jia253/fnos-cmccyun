#!/bin/bash
# Restart the cmccyun debug daemon against the fake backend at /tmp/cct.
set -u
BIN=${BIN:-/tmp/cmccyun-build/cmccyun}
ROOT=${ROOT:-/tmp/cct}
pkill -x cmccyun 2>/dev/null
sleep 1
fusermount3 -u "$ROOT/mnt" 2>/dev/null
fusermount -u "$ROOT/mnt" 2>/dev/null
sleep 0.5
rm -f "$ROOT/daemon.log"
nohup env CMCCYUN_DEBUG_FAKE="$ROOT/backend" CMCCYUN_ETC="$ROOT/etc" "$BIN" run >"$ROOT/daemon.log" 2>&1 &
for i in $(seq 1 30); do
  if grep -q "$ROOT/mnt" /proc/self/mountinfo 2>/dev/null; then
    echo "mounted after ${i}00ms"; break
  fi
  sleep 0.3
done
grep "$ROOT/mnt" /proc/self/mountinfo | head -1
echo "--- log ---"; cat "$ROOT/daemon.log"
