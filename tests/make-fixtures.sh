#!/bin/bash
# 准备 functional.sh 需要的假云盘目录（/tmp 被清过之后重跑用）。
#   bash tests/make-fixtures.sh
set -u
ROOT=${ROOT:-/tmp/cct}
rm -rf "$ROOT"
mkdir -p "$ROOT/backend/docs" "$ROOT/backend/media" "$ROOT/mnt" "$ROOT/etc"
printf 'hello from cmccyun fake drive\n' >"$ROOT/backend/docs/readme.txt"
head -c 3000000 /dev/urandom >"$ROOT/backend/media/blob.bin"
head -c 25000000 /dev/urandom >"$ROOT/backend/media/big.bin"
chmod 777 "$ROOT" "$ROOT/backend" "$ROOT/mnt" "$ROOT/etc"
echo "已生成 $ROOT/backend（$(du -sh "$ROOT/backend" | cut -f1)）"
