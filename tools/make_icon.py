#!/usr/bin/env python3
"""Generate app.ico without third-party dependencies."""
import math
import struct
import zlib
from pathlib import Path

OUT = Path(__file__).resolve().parent.parent / "app.ico"


def lerp(a, b, t):
    return tuple(int(round(a[i] + (b[i] - a[i]) * t)) for i in range(3))


def in_rounded_rect(x, y, x0, y0, x1, y1, r):
    if x < x0 or x > x1 or y < y0 or y > y1:
        return False
    cx = min(max(x, x0 + r), x1 - r)
    cy = min(max(y, y0 + r), y1 - r)
    return (x - cx) ** 2 + (y - cy) ** 2 <= r * r


def point_in_triangle(px, py, a, b, c):
    def sign(p1, p2, p3):
        return (p1[0] - p3[0]) * (p2[1] - p3[1]) - (p2[0] - p3[0]) * (p1[1] - p3[1])

    d1 = sign((px, py), a, b)
    d2 = sign((px, py), b, c)
    d3 = sign((px, py), c, a)
    has_neg = d1 < 0 or d2 < 0 or d3 < 0
    has_pos = d1 > 0 or d2 > 0 or d3 > 0
    return not (has_neg and has_pos)


def dist_to_segment(px, py, ax, ay, bx, by):
    dx, dy = bx - ax, by - ay
    if dx == 0 and dy == 0:
        return math.hypot(px - ax, py - ay)
    t = max(0.0, min(1.0, ((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy)))
    return math.hypot(px - (ax + t * dx), py - (ay + t * dy))


def render(size):
    ss = 4
    n = size * ss
    scale = n / 64.0
    bg = bytearray(n * n * 4)
    top = (37, 99, 235)
    bottom = (124, 58, 237)

    for y in range(n):
        for x in range(n):
            u, v = x / scale, y / scale
            i = (y * n + x) * 4
            if not in_rounded_rect(u, v, 2, 2, 62, 62, 14):
                continue
            t = max(0.0, min(1.0, (v - 6) / 52))
            c = lerp(top, bottom, t)
            # subtle top highlight
            if v < 14:
                c = lerp(c, (255, 255, 255), (14 - v) / 14 * 0.12)
            bg[i : i + 4] = bytes((c[0], c[1], c[2], 255))

    def paint(x, y, color):
        i = (y * n + x) * 4
        bg[i : i + 4] = bytes((color[0], color[1], color[2], 255))

    white = (255, 255, 255)
    # right arrow
    for y in range(n):
        for x in range(n):
            u, v = x / scale, y / scale
            shaft = dist_to_segment(u, v, 17, 26, 46, 26) <= 2.6
            head = point_in_triangle(u, v, (43, 18), (56, 26), (43, 34))
            if shaft or head:
                paint(x, y, white)
    # left arrow
    for y in range(n):
        for x in range(n):
            u, v = x / scale, y / scale
            shaft = dist_to_segment(u, v, 47, 40, 18, 40) <= 2.6
            head = point_in_triangle(u, v, (21, 32), (8, 40), (21, 48))
            if shaft or head:
                paint(x, y, white)

    # downsample
    out = bytearray(size * size * 4)
    for y in range(size):
        for x in range(size):
            r = g = b = a = 0
            for dy in range(ss):
                for dx in range(ss):
                    i = ((y * ss + dy) * n + (x * ss + dx)) * 4
                    r += bg[i]
                    g += bg[i + 1]
                    b += bg[i + 2]
                    a += bg[i + 3]
            j = (y * size + x) * 4
            out[j : j + 4] = bytes((r // 16, g // 16, b // 16, a // 16))
    return out


def png_bytes(size, pixels):
    def chunk(tag, data):
        return (
            struct.pack(">I", len(data))
            + tag
            + data
            + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
        )

    raw = bytearray()
    stride = size * 4
    for y in range(size):
        raw.append(0)
        raw += pixels[y * stride : (y + 1) * stride]
    ihdr = struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", ihdr)
        + chunk(b"IDAT", zlib.compress(bytes(raw), 9))
        + chunk(b"IEND", b"")
    )


def main():
    images = []
    for size in (64, 256):
        images.append((size, png_bytes(size, render(size))))

    header = struct.pack("<HHH", 0, 1, len(images))
    offset = 6 + 16 * len(images)
    entries = bytearray()
    payload = bytearray()
    for size, data in images:
        w = 0 if size >= 256 else size
        h = 0 if size >= 256 else size
        entries += struct.pack("<BBBBHHII", w, h, 0, 0, 1, 32, len(data), offset)
        payload += data
        offset += len(data)
    OUT.write_bytes(header + bytes(entries) + bytes(payload))
    print(f"wrote {OUT} ({OUT.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
