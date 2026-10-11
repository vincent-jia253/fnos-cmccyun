#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""抓取中国移动云盘开放平台（open.yun.139.com）帮助文档。

用法:
    python3 crawl_open139.py <输出目录>            # 默认 ../research/open-platform-139

抓取的是前端同款接口（无需登录）:
    POST /open-user/api/v4.6/HelpDocuments/getOpenTree          {"type": T}
    POST /open-user/api/v4.6/HelpDocuments/getOpenListByMenuId  {"id": menuId, "type": T}
    POST /open-user/api/v4.6/HelpDocuments/getOpenContent       {"id": docId,  "type": T}
T ∈ API / FAQ / STANDARD / CONTENT
"""
import html
import json
import os
import re
import sys
import time
import urllib.request

BASE = "https://open.yun.139.com/open-user/api/v4.6/HelpDocuments/"
TYPES = ["API", "FAQ", "STANDARD", "CONTENT"]
UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120 Safari/537.36"


def post(ep, body, tries=3):
    url = "%s%s?timestamp=%d" % (BASE, ep, int(time.time() * 1000))
    data = json.dumps(body).encode()
    last = None
    for i in range(tries):
        try:
            req = urllib.request.Request(
                url, data=data,
                headers={"Content-Type": "application/json", "User-Agent": UA},
            )
            with urllib.request.urlopen(req, timeout=30) as r:
                return json.loads(r.read().decode("utf-8"))
        except Exception as e:  # noqa: BLE001
            last = e
            time.sleep(1 + i)
    raise RuntimeError("%s %s -> %s" % (ep, body, last))


def cached(path, fn):
    if os.path.exists(path) and os.path.getsize(path) > 2:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    val = fn()
    with open(path, "w", encoding="utf-8") as f:
        json.dump(val, f, ensure_ascii=False, indent=1)
    time.sleep(0.25)
    return val


def safe(name):
    return re.sub(r"[^\w\u4e00-\u9fff.\-]+", "_", (name or "untitled")).strip("_")[:80]


def walk(nodes, out, depth=0):
    for n in nodes or []:
        out.append((depth, n))
        walk(n.get("children"), out, depth + 1)


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else os.path.join(
        os.path.dirname(os.path.abspath(__file__)), "..", "research", "open-platform-139")
    root = os.path.abspath(root)
    raw = os.path.join(root, "raw")
    docs = os.path.join(root, "docs")
    os.makedirs(raw, exist_ok=True)
    os.makedirs(docs, exist_ok=True)

    index = {}
    toc = []
    for t in TYPES:
        tree = cached(os.path.join(raw, "tree_%s.json" % t),
                      lambda t=t: post("getOpenTree", {"type": t}))
        nodes = []
        walk(tree.get("data"), nodes)
        toc.append("# %s\n" % t)
        seen = set()
        for depth, n in nodes:
            nid, name, leaf = n.get("id"), n.get("name"), n.get("isLeaf")
            toc.append("%s- [%s] (id=%s, isLeaf=%s)" % ("  " * depth, name, nid, leaf))
            if nid in seen:
                continue
            seen.add(nid)
            lst = cached(os.path.join(raw, "list_%s.json" % nid),
                         lambda nid=nid, t=t: post("getOpenListByMenuId", {"id": nid, "type": t}))
            items = lst.get("data") or []
            if not items:
                continue
            toc.append("%s  文档 %d 篇:" % ("  " * depth, len(items)))
            for it in items:
                did = it.get("id")
                title = it.get("newTitle") or it.get("name") or it.get("title")
                upd = it.get("updateTime")
                toc.append("%s    - %s | id=%s | 更新 %s" % ("  " * depth, title, did, upd))
                got = cached(os.path.join(raw, "doc_%s.json" % did),
                             lambda did=did, t=t: post("getOpenContent", {"id": did, "type": t}))
                d = got.get("data") or {}
                rawhtml = d.get("newContent") or ""
                md = ""
                if rawhtml:
                    try:
                        from markdownify import markdownify
                        md = markdownify(html.unescape(rawhtml), heading_style="ATX")
                    except Exception:  # noqa: BLE001
                        md = re.sub(r"<[^>]+>", "", html.unescape(rawhtml))
                    md = re.sub(r"\n{3,}", "\n\n", md).strip()
                fn = "%04d-%s.md" % (did, safe(title))
                with open(os.path.join(docs, fn), "w", encoding="utf-8") as f:
                    f.write("# %s\n\n- 文档 id: %s\n- 所属菜单: %s (%s)\n- 更新时间: %s\n- 来源: https://open.yun.139.com/#/help-document-new/index?type=%s&id=%s\n\n---\n\n%s\n"
                            % (title, did, name, nid, upd, t, did, md))
                index[str(did)] = {"title": title, "menu": name, "menuid": nid,
                                   "type": t, "updated": upd, "file": fn, "chars": len(md)}
                print("  doc %s %s (%d chars, 更新 %s)" % (did, title, len(md), upd))

    with open(os.path.join(root, "INDEX.md"), "w", encoding="utf-8") as f:
        f.write("# 中国移动云盘开放平台 · 帮助文档目录\n\n抓取时间: %s\n来源: https://open.yun.139.com/#/home\n\n```\n%s\n```\n"
                % (time.strftime("%Y-%m-%d %H:%M:%S"), "\n".join(toc)))
    with open(os.path.join(root, "index.json"), "w", encoding="utf-8") as f:
        json.dump(index, f, ensure_ascii=False, indent=1)
    print("共 %d 篇文档 -> %s" % (len(index), docs))


if __name__ == "__main__":
    main()
