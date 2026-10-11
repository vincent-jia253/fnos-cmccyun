#!/usr/bin/env python3
"""Generate the cmccyun app icons (no external assets needed).

Draws a rounded blue tile with a white cloud and a download arrow, at 8x
supersampling, then writes:
  ICON.PNG, ICON_256.PNG, app/ui/images/icon_64.png, app/ui/images/icon_256.png
"""
import os
from PIL import Image, ImageDraw

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "fnos-cmccyun")
OUT = os.path.normpath(OUT)
SS = 8  # supersampling factor


def rounded_tile(size, radius_ratio=0.22):
    w = size * SS
    img = Image.new("RGBA", (w, w), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    # vertical gradient background inside a rounded rect
    grad = Image.new("RGB", (1, w))
    gd = ImageDraw.Draw(grad)
    top, bot = (66, 165, 245), (21, 101, 192)  # #42A5F5 -> #1565C0
    for y in range(w):
        t = y / max(1, w - 1)
        gd.point((0, y), fill=tuple(int(top[i] + (bot[i] - top[i]) * t) for i in range(3)))
    grad = grad.resize((w, w))
    mask = Image.new("L", (w, w), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, w - 1, w - 1], radius=int(w * radius_ratio), fill=255)
    img.paste(grad, (0, 0), mask)
    d = ImageDraw.Draw(img)
    return img, d, w


def cloud_shape(w, cx, cy, scale):
    """Return the drawings for a cloud: three circles plus a base bar."""
    r1 = 0.145 * scale  # left puff
    r2 = 0.185 * scale  # top puff
    r3 = 0.130 * scale  # right puff
    base_h = 0.115 * scale
    circles = [
        (cx - 0.135 * scale, cy + 0.030 * scale, r1),
        (cx - 0.010 * scale, cy - 0.045 * scale, r2),
        (cx + 0.140 * scale, cy + 0.030 * scale, r3),
    ]
    bar = [cx - 0.185 * scale, cy + 0.020 * scale, cx + 0.190 * scale, cy + 0.020 * scale + base_h]
    return circles, bar


def draw_icon(size):
    img, d, w = rounded_tile(size)
    cx, cy = w / 2, w * 0.47
    scale = w
    white = (255, 255, 255, 255)
    circles, bar = cloud_shape(w, cx, cy, scale)
    for (x, y, r) in circles:
        d.ellipse([x - r, y - r, x + r, y + r], fill=white)
    d.rounded_rectangle(bar, radius=int(w * 0.012), fill=white)

    # download arrow carved into the cloud (background colour reads as "cloud drive")
    blue = (23, 108, 199, 255)
    ax, ay = cx, cy + 0.005 * scale
    shaft_w = 0.052 * scale
    shaft_top, shaft_bot = ay - 0.085 * scale, ay + 0.045 * scale
    d.rounded_rectangle(
        [ax - shaft_w / 2, shaft_top, ax + shaft_w / 2, shaft_bot],
        radius=int(shaft_w / 2), fill=blue,
    )
    head = 0.115 * scale
    d.polygon(
        [(ax - head, shaft_bot - 0.010 * scale), (ax + head, shaft_bot - 0.010 * scale), (ax, shaft_bot + 0.105 * scale)],
        fill=blue,
    )
    return img.resize((size, size), Image.LANCZOS)


def main():
    imgs = {s: draw_icon(s) for s in (64, 256)}
    targets = [
        (os.path.join(OUT, "ICON.PNG"), 64),
        (os.path.join(OUT, "ICON_256.PNG"), 256),
        (os.path.join(OUT, "app", "ui", "images", "icon_64.png"), 64),
        (os.path.join(OUT, "app", "ui", "images", "icon_256.png"), 256),
    ]
    for path, size in targets:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        imgs[size].save(path, "PNG")
        print("wrote", os.path.relpath(path, OUT))


if __name__ == "__main__":
    main()
