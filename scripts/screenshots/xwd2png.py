#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
#
# The X server's image of one window as a PNG, for scripts/screenshots/gtk.sh:
#
#   xwd2png.py WINDOW OUT.png     WINDOW is an X window id (0x…) or "top",
#                                 the topmost viewable toplevel
#
# Runs xwininfo and xwd on the root window (a 24-bit TrueColor ZPixmap, as
# Xvfb gives; GTK's windows have a 32-bit visual, the screen shows them
# composed), crops to the window's rectangle on the screen and writes the
# pixels unscaled and lossless; only the standard library, so no
# ImageMagick is needed.
import re
import struct
import subprocess
import sys
import zlib


def info(win):
    out = subprocess.run(["xwininfo", "-id", win], capture_output=True, text=True, check=True).stdout
    field = lambda name: re.search(r"^\s*" + name + r":\s*(.+)$", out, re.M).group(1).strip()
    return {
        "viewable": field("Map State") == "IsViewable",
        "override": field("Override Redirect State") == "yes",
        "x": int(field("Absolute upper-left X")),
        "y": int(field("Absolute upper-left Y")),
        "w": int(field("Width")),
        "h": int(field("Height")),
        "title": out.splitlines()[1] if len(out.splitlines()) > 1 else "",
    }


def topmost():
    # xwininfo lists the root's children top to bottom.
    out = subprocess.run(["xwininfo", "-root", "-children"], capture_output=True, text=True, check=True).stdout
    best = None
    for m in re.finditer(r"^\s+(0x[0-9a-f]+) ", out, re.M):
        i = info(m.group(1))
        if i["viewable"] and not i["override"] and i["w"] > 1 and i["h"] > 1:
            print(f"xwd2png: candidate {m.group(1)} {i['w']}x{i['h']}+{i['x']}+{i['y']} {i['title']}", file=sys.stderr)
            if best is None:
                best = (m.group(1), i)
    if best is None:
        sys.exit("xwd2png: no viewable window")
    return best


def xwd_pixels(box):
    data = subprocess.run(["xwd", "-silent", "-root"], capture_output=True, check=True).stdout
    (header_size, _version, fmt, depth, width, height, _xoff, byte_order, _unit, _ubo, _pad,
     bpp, bpl, _vclass, rmask, gmask, bmask, _bits, _cmap, ncolors, _wwidth, _wheight,
     _wx, _wy, _border) = struct.unpack(">25I", data[:100])
    if fmt != 2 or bpp != 32 or depth != 24:
        sys.exit(f"xwd2png: format {fmt}, {bpp} bpp, depth {depth}: want a 24-bit ZPixmap")
    offset = header_size + ncolors * 12
    pixels = data[offset:offset + bpl * height]
    order = "<" if byte_order == 0 else ">"
    shifts = [(m & -m).bit_length() - 1 for m in (rmask, gmask, bmask)]
    x0, y0, w, h = box
    if x0 < 0 or y0 < 0 or x0 + w > width or y0 + h > height:
        sys.exit(f"xwd2png: the window {w}x{h}+{x0}+{y0} is not wholly on the {width}x{height} screen")
    rows = bytearray()
    for y in range(y0, y0 + h):
        line = pixels[y * bpl + x0 * 4:y * bpl + (x0 + w) * 4]
        words = struct.unpack(f"{order}{w}I", line)
        rows.append(0)
        rows += bytes(b for v in words for b in ((v >> shifts[0]) & 255, (v >> shifts[1]) & 255, (v >> shifts[2]) & 255))
    return w, h, bytes(rows)


def write_png(path, width, height, raw):
    def chunk(kind, body):
        return struct.pack(">I", len(body)) + kind + body + struct.pack(">I", zlib.crc32(kind + body) & 0xFFFFFFFF)
    with open(path, "wb") as f:
        f.write(b"\x89PNG\r\n\x1a\n")
        f.write(chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)))
        f.write(chunk(b"IDAT", zlib.compress(raw, 9)))
        f.write(chunk(b"IEND", b""))


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: xwd2png.py WINDOW|top OUT.png")
    win, out = sys.argv[1], sys.argv[2]
    if win == "top":
        win, i = topmost()
    else:
        i = info(win)
    print(f"xwd2png: {win} {i['w']}x{i['h']}+{i['x']}+{i['y']} {i['title']}", file=sys.stderr)
    width, height, raw = xwd_pixels((i["x"], i["y"], i["w"], i["h"]))
    write_png(out, width, height, raw)


main()
