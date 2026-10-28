#!/usr/bin/env python3
"""Fails when a built library carries one of the GPL-2.0 Pidgin sounds.

For each WAV in `assets/sounds/`, the gate parses the RIFF file and takes a
distinctive 64-byte window from its `data` chunk: the first one after the
first 4 KiB of samples that is not silence and occurs exactly once in all of
the sounds. A library containing any window carries that sound. Searching for
payload bytes rather than hashing whole files also finds a copy embedded with
`include_bytes!` or with its header stripped.

    scripts/gnui/check_gpl_sounds.py               # builds the headless rlib and checks it
    scripts/gnui/check_gpl_sounds.py LIB [LIB...]  # checks the given libraries
    scripts/gnui/check_gpl_sounds.py --self-test   # a fixture embedding a sound must fail
"""

from __future__ import annotations

import argparse
import os
import struct
import sys
from pathlib import Path

from common import HEADLESS, ROOT, run, target_directory

SOUNDS = ROOT / "assets" / "sounds"
FIXTURE = Path(__file__).resolve().parent / "fixtures" / "gpl_sound"
WINDOW = 64
SKIP = 4096
# Below this peak a 16-bit window counts as silence (about -40 dBFS).
QUIET = 328


def chunks(riff: bytes) -> dict[bytes, tuple[int, int]]:
    """The offset and length of each top-level chunk of a RIFF/WAVE file."""
    if riff[:4] != b"RIFF" or riff[8:12] != b"WAVE":
        raise ValueError("not a RIFF/WAVE file")
    found = {}
    at = 12
    while at + 8 <= len(riff):
        name = riff[at : at + 4]
        (length,) = struct.unpack("<I", riff[at + 4 : at + 8])
        found.setdefault(name, (at + 8, length))
        at += 8 + length + (length & 1)
    return found


def silent(window: bytes, bits: int) -> bool:
    if len(set(window)) <= 2:
        return True
    if bits == 16:
        samples = struct.unpack(f"<{len(window) // 2}h", window)
        return max(abs(sample) for sample in samples) < QUIET
    if bits == 8:
        return max(abs(byte - 128) for byte in window) < QUIET >> 8
    return False


def occurrences(haystack: bytes, needle: bytes) -> int:
    count, at = 0, haystack.find(needle)
    while at != -1:
        count += 1
        at = haystack.find(needle, at + 1)
    return count


def windows() -> dict[str, bytes]:
    """A distinctive payload window for each sound, by file name."""
    files = {path.name: path.read_bytes() for path in sorted(SOUNDS.glob("*.wav"))}
    if not files:
        raise SystemExit(f"no WAV files in {SOUNDS}")
    chosen = {}
    for name, riff in files.items():
        table = chunks(riff)
        if b"data" not in table or b"fmt " not in table:
            raise SystemExit(f"{name}: no fmt or data chunk")
        fmt_at, _ = table[b"fmt "]
        _, _, _, _, align, bits = struct.unpack("<HHIIHH", riff[fmt_at : fmt_at + 16])
        start, length = table[b"data"]
        data = riff[start : start + length]
        step = max(align, 1)
        for at in range(SKIP - SKIP % step, len(data) - WINDOW + 1, step):
            window = data[at : at + WINDOW]
            if silent(window, bits):
                continue
            if sum(occurrences(other, window) for other in files.values()) == 1:
                chosen[name] = window
                break
        else:
            raise SystemExit(f"{name}: no distinctive window after the first {SKIP} bytes")
    return chosen


def carried(library: Path, chosen: dict[str, bytes]) -> list[str]:
    contents = library.read_bytes()
    return [name for name, window in chosen.items() if window in contents]


def check(libraries: list[Path]) -> bool:
    chosen = windows()
    for name, window in chosen.items():
        print(f"     {name}: window {window[:8].hex()}…")
    clean = True
    for library in libraries:
        found = carried(library, chosen)
        if found:
            clean = False
            print(f"FAIL {library}: carries {', '.join(found)} (GPL-2.0)")
        else:
            print(f"ok   {library}: none of {', '.join(chosen)}")
    return clean


def headless_rlib() -> Path:
    run(["cargo", "build", "--locked", "--lib"] + HEADLESS)
    return target_directory() / "debug" / "libzapfast.rlib"


def fixture_library() -> Path:
    """Builds a cdylib that embeds alert.wav, to prove the gate can fail."""
    target = target_directory() / "gnui-fixtures"
    run(
        ["cargo", "build", "--manifest-path", str(FIXTURE / "Cargo.toml")],
        env={**os.environ, "CARGO_TARGET_DIR": str(target)},
    )
    names = ["libgpl_sound_fixture.dylib", "libgpl_sound_fixture.so", "gpl_sound_fixture.dll"]
    for name in names:
        if (target / "debug" / name).exists():
            return target / "debug" / name
    raise SystemExit(f"the fixture library is not in {target / 'debug'}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("libraries", nargs="*", type=Path)
    parser.add_argument(
        "--self-test", action="store_true", help="check that a library embedding a sound fails"
    )
    options = parser.parse_args()
    if options.self_test:
        if check([fixture_library()]):
            print("FAIL self-test: the fixture embeds alert.wav and passed")
            return 1
        print("ok   self-test: the fixture that embeds a sound fails the gate")
        return 0
    return 0 if check(options.libraries or [headless_rlib()]) else 1


if __name__ == "__main__":
    sys.exit(main())
