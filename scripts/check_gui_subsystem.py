#!/usr/bin/env python3
"""Assert that a Windows executable is linked as a GUI app, not a console app.

A Go program links a console subsystem by default, so a tray app built without
-H=windowsgui opens a black console window on every launch. That is invisible in
a headless build: the exe runs, the tests pass, and only the person who launches
it sees the window.

The subsystem is a uint16 in the PE optional header. IMAGE_OPTIONAL_HEADER64
lays out its fields in this order, with Subsystem at offset 68:

    Magic, linker versions, sizes, entry point, base of code, image base,
    alignments, version fields, win32 version, image size, headers size,
    checksum, Subsystem.

    python check_gui_subsystem.py path/to/AirFlash.exe

Exits 1, with the reason, when the executable is anything but a GUI app.
"""

from __future__ import annotations

import struct
import sys

# IMAGE_SUBSYSTEM_WINDOWS_GUI.
WINDOWS_GUI = 2
# IMAGE_SUBSYSTEM_WINDOWS_CUI, the one that opens a console window.
WINDOWS_CUI = 3

# Layout of the header, from the start of the file:
#
#   0x3C            e_lfanew: the offset of the PE signature
#   e_lfanew        "PE\0\0"
#   +4              the 20-byte COFF header
#   +24             the optional header, whose field at offset 68 is Subsystem
#
# So Subsystem sits at e_lfanew + 4 + 20 + 68, and only the whole path from
# e_lfanew is read, because every step is a separate structure.
COFF_HEADER_SIZE = 20
SUBSYSTEM_OFFSET = 68


def subsystem(path: str) -> int:
    with open(path, "rb") as handle:
        data = handle.read()

    if data[:2] != b"MZ":
        raise SystemExit(f"{path}: not a portable executable (no MZ header)")

    pe_offset = struct.unpack_from("<I", data, 0x3C)[0]
    if data[pe_offset : pe_offset + 4] != b"PE\x00\x00":
        raise SystemExit(f"{path}: no PE signature")

    optional_header = pe_offset + 4 + COFF_HEADER_SIZE
    magic = struct.unpack_from("<H", data, optional_header)[0]
    if magic != 0x20B:
        # Only PE32+ is read here; a Go windows/amd64 build is always PE32+.
        raise SystemExit(f"{path}: optional header magic {magic:#x} is not PE32+")

    return struct.unpack_from(
        "<H", data, optional_header + SUBSYSTEM_OFFSET
    )[0]


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("usage: check_gui_subsystem.py <exe>", file=sys.stderr)
        return 2

    found = subsystem(argv[1])
    if found == WINDOWS_GUI:
        print(f"{argv[1]}: subsystem {found} (Windows GUI), no console window")
        return 0

    if found == WINDOWS_CUI:
        print(
            f"{argv[1]}: subsystem {found} (Windows console), so launching it would "
            "open a console window: build with -H=windowsgui",
            file=sys.stderr,
        )
    else:
        print(
            f"{argv[1]}: unexpected subsystem {found}, want {WINDOWS_GUI}",
            file=sys.stderr,
        )
    return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
