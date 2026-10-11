#!/usr/bin/env python3
"""把 fpk 内容同步到 /var/apps/<app> 与 target 目录（模拟一次升级安装）。

用法: sudo python3 sync_fpk.py <fpk> [appname]
"""
import io
import os
import shutil
import sys
import tarfile

fpk = sys.argv[1]
app = sys.argv[2] if len(sys.argv) > 2 else "cmccyun"
app_dir = f"/var/apps/{app}"
target = f"/vol4/@appcenter/{app}"

with tarfile.open(fpk) as t:
    members = t.getmembers()
    # app.tgz -> target
    app_tgz = t.extractfile("app.tgz").read()
    for m in members:
        if m.name == "app.tgz":
            continue
        dest = os.path.join(app_dir, m.name)
        if m.isdir():
            os.makedirs(dest, exist_ok=True)
            os.chmod(dest, 0o755)
            continue
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        with open(dest, "wb") as fh:
            shutil.copyfileobj(t.extractfile(m), fh)
        mode = m.mode & 0o777
        os.chmod(dest, mode if mode else 0o644)
        print("updated", dest, oct(mode))

with tarfile.open(fileobj=io.BytesIO(app_tgz)) as inner:
    for m in inner.getmembers():
        if m.name.startswith("config/"):
            continue  # 打包附带的 config 副本不需要
        dest = os.path.join(target, m.name)
        if m.isdir():
            os.makedirs(dest, exist_ok=True)
            os.chmod(dest, 0o755)
            continue
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        with open(dest, "wb") as fh:
            shutil.copyfileobj(inner.extractfile(m), fh)
        mode = m.mode & 0o777
        os.chmod(dest, mode if mode else 0o644)
        print("updated", dest, oct(mode))

# 属主与 target 目录保持一致（应用共享目录由 fnOS 以 ACL 授权）
for root in (app_dir, target):
    for dirpath, dirnames, filenames in os.walk(root):
        try:
            st = os.stat(dirpath)
            if root == target or (dirpath != app_dir and dirpath != app_dir + "/cmd"):
                pass
        except OSError:
            pass
print("done")
