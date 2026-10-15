#!/usr/bin/env python3
"""Rasteriza o selo do Hanko (internal/ui/static/favicon.svg) nos PNG do PWA.

Uso: python3 scripts/gen-pwa-icons.py   (requer Pillow)
Gera em internal/ui/static/icons/: icon-192.png, icon-512.png (transparentes),
icon-maskable-512.png e apple-touch-icon.png (fundo opaco, selo na zona segura).
"""
import math
import os
from PIL import Image, ImageDraw

PETAL = [((50, 50), (38, 43), (33, 27), (39, 15)), ((39, 15), (42, 8), (47, 10), (50, 17)),
         ((50, 17), (53, 10), (58, 8), (61, 15)), ((61, 15), (67, 27), (62, 43), (50, 50))]
FILL, INK, BG = (244, 201, 212, 255), (28, 25, 23, 255), (250, 250, 249, 255)
SS = 4  # supersampling


def bez(p, n=24):
    out = []
    for i in range(n + 1):
        t = i / n
        u = 1 - t
        out.append(tuple(u**3 * p[0][k] + 3 * u * u * t * p[1][k] + 3 * u * t * t * p[2][k] + t**3 * p[3][k] for k in (0, 1)))
    return out


def petal():
    pts = []
    for seg in PETAL:
        pts += bez(seg)
    return pts


def rot(pts, deg):
    a = math.radians(deg)
    c, s = math.cos(a), math.sin(a)
    return [(50 + (x - 50) * c - (y - 50) * s, 50 + (x - 50) * s + (y - 50) * c) for x, y in pts]


def render(size, scale, bg):
    """Selo ocupando `scale` da largura, centrado; bg None = transparente."""
    big = size * SS
    img = Image.new("RGBA", (big, big), bg or (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    k = big * scale / 100.0
    off = (big - big * scale) / 2

    def tx(p):
        return (off + p[0] * k, off + p[1] * k)

    w = 6 * k
    for i in range(5):
        pts = [tx(p) for p in rot(petal(), 72 * i)]
        d.polygon(pts, fill=FILL)
        d.line(pts + [pts[0]], fill=INK, width=round(w))
        for x, y in pts:
            d.ellipse((x - w / 2, y - w / 2, x + w / 2, y + w / 2), fill=INK)
    cx, cy = tx((50, 50))
    r = 5.4 * k
    d.ellipse((cx - r, cy - r, cx + r, cy + r), fill=INK)
    return img.resize((size, size), Image.LANCZOS)


def main():
    out = os.path.join(os.path.dirname(__file__), "..", "internal", "ui", "static", "icons")
    os.makedirs(out, exist_ok=True)
    render(192, 0.9, None).save(os.path.join(out, "icon-192.png"), optimize=True)
    render(512, 0.9, None).save(os.path.join(out, "icon-512.png"), optimize=True)
    render(512, 0.6, BG).save(os.path.join(out, "icon-maskable-512.png"), optimize=True)
    render(180, 0.72, BG).convert("RGB").save(os.path.join(out, "apple-touch-icon.png"), optimize=True)


if __name__ == "__main__":
    main()
