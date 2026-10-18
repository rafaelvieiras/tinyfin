#!/usr/bin/env python3
"""Writes a tiny HDMV PGS (.sup) subtitle stream for the conformance media tree.

  conformance-pgs.py <out.sup> <width> <height> <text> <start-s> <end-s> [<start-s> <end-s> ...]

Each start/end pair is one cue showing <text> as big white block letters with a
black outline at the bottom of the picture. No dependencies: ffmpeg has no PGS
encoder, so the stream is assembled by hand (segments PCS/WDS/PDS/ODS/END).
"""
import struct
import sys

GLYPHS = {  # 5x7
    "A": ["01110", "10001", "10001", "11111", "10001", "10001", "10001"],
    "B": ["11110", "10001", "10001", "11110", "10001", "10001", "11110"],
    "D": ["11110", "10001", "10001", "10001", "10001", "10001", "11110"],
    "I": ["11111", "00100", "00100", "00100", "00100", "00100", "11111"],
    "M": ["10001", "11011", "10101", "10101", "10001", "10001", "10001"],
    "N": ["10001", "11001", "10101", "10011", "10001", "10001", "10001"],
    "P": ["11110", "10001", "10001", "11110", "10000", "10000", "10000"],
    "R": ["11110", "10001", "10001", "11110", "10100", "10010", "10001"],
    "U": ["10001", "10001", "10001", "10001", "10001", "10001", "01110"],
    "T": ["11111", "00100", "00100", "00100", "00100", "00100", "00100"],
    "G": ["01110", "10001", "10000", "10111", "10001", "10001", "01111"],
    "S": ["01111", "10000", "10000", "01110", "00001", "00001", "11110"],
    " ": ["00000"] * 7,
}
SCALE = 4


def render(text):
    """Pixel rows (palette index per pixel): 1 white, 2 black outline, 0 clear."""
    cols = []
    for ch in text.upper():
        g = GLYPHS[ch]
        for gx in range(5):
            cols.append([g[y][gx] == "1" for y in range(7)])
        cols.append([False] * 7)
    cols = cols[:-1]
    w, h = len(cols) * SCALE + 4, 7 * SCALE + 4
    img = [[0] * w for _ in range(h)]
    for cx, col in enumerate(cols):
        for cy, on in enumerate(col):
            if on:
                for dy in range(SCALE):
                    for dx in range(SCALE):
                        img[2 + cy * SCALE + dy][2 + cx * SCALE + dx] = 1
    out = [r[:] for r in img]
    for y in range(h):
        for x in range(w):
            if img[y][x] == 0:
                for dy in (-1, 0, 1):
                    for dx in (-1, 0, 1):
                        yy, xx = y + dy, x + dx
                        if 0 <= yy < h and 0 <= xx < w and img[yy][xx] == 1:
                            out[y][x] = 2
    return w, h, out


def rle(img):
    data = bytearray()
    for row in img:
        i = 0
        while i < len(row):
            c = row[i]
            n = 1
            while i + n < len(row) and row[i + n] == c and n < 63:
                n += 1
            if c == 0:
                data += bytes([0, n])
            else:
                data += bytes([0, 0x80 | n, c])
            i += n
        data += bytes([0, 0])
    return bytes(data)


def seg(kind, pts, payload):
    return b"PG" + struct.pack(">II", pts, 0) + bytes([kind]) + struct.pack(">H", len(payload)) + payload


def main():
    out, sw, sh, text = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4]
    times = [float(v) for v in sys.argv[5:]]
    w, h, img = render(text)
    x, y = (sw - w) // 2, sh - h - 12
    body = rle(img)
    comp = 0
    stream = b""
    # An empty display set at time 0 first: ffmpeg rebases an input to its
    # first timestamp, which would pull a cue starting at 0.5 s back to 0.
    stream += seg(0x16, 0, struct.pack(">HHBHBBBB", sw, sh, 0x10, comp, 0x80, 0, 0, 0)) + seg(0x80, 0, b"")
    comp += 1
    for start, end in zip(times[0::2], times[1::2]):
        t0, t1 = int(start * 90000), int(end * 90000)
        pcs = struct.pack(">HHBHBBBB", sw, sh, 0x10, comp, 0x80, 0, 0, 1) + struct.pack(">HBBHH", 0, 0, 0, x, y)
        wds = bytes([1, 0]) + struct.pack(">HHHH", x, y, w, h)
        pal = bytes([0, 0]) + bytes([0, 16, 128, 128, 0]) + bytes([1, 235, 128, 128, 255]) + bytes([2, 16, 128, 128, 255])
        ods = struct.pack(">HBB", 0, 0, 0xC0) + struct.pack(">I", len(body) + 4)[1:] + struct.pack(">HH", w, h) + body
        stream += seg(0x16, t0, pcs) + seg(0x17, t0, wds) + seg(0x14, t0, pal) + seg(0x15, t0, ods) + seg(0x80, t0, b"")
        comp += 1
        pcs = struct.pack(">HHBHBBBB", sw, sh, 0x10, comp, 0x00, 0, 0, 0)
        stream += seg(0x16, t1, pcs) + seg(0x17, t1, bytes([1, 0]) + struct.pack(">HHHH", x, y, w, h)) + seg(0x80, t1, b"")
        comp += 1
    with open(out, "wb") as f:
        f.write(stream)


main()
