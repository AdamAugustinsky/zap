#!/usr/bin/env python3
"""Fails when the headless core builds or depends on anything of the egui client.

Builds the library headless, then reads `cargo tree` (normal and build
dependencies, no dev ones) for each macOS architecture the Go UI ships, and
fails on egui, eframe, winit, glow, wgpu, rfd, arboard, or a fastframe crate
that depends on egui.

    scripts/gnui/check_headless_tree.py                   # upstream's crate, headless
    scripts/gnui/check_headless_tree.py -p zapfast_core   # the cdylib, with its features
    scripts/gnui/check_headless_tree.py --self-test       # the egui build must fail it
"""

from __future__ import annotations

import argparse
import sys

from common import MAC_TARGETS, output, run, selection

FORBIDDEN = {
    "eframe",
    "egui",
    "egui_extras",
    "egui-winit",
    "egui_glow",
    "egui-wgpu",
    "epaint",
    "epaint_default_fonts",
    "emath",
    "ecolor",
    "winit",
    "glow",
    "glutin",
    "glutin-winit",
    "wgpu",
    "rfd",
    "arboard",
    # fastframe's crates that depend on egui (each checked in Cargo.lock).
    "fastframe-shell",
    "fastframe-titlebar",
    "fastframe-text",
    "fastframe-theme",
    "fastframe-fonts",
    "fastframe-icons",
    "fastframe-emoji",
    "fastframe-scroll",
}

# Families named by prefix: wgpu-core, wgpu-hal, egui-*, ...
FORBIDDEN_PREFIXES = ("egui", "wgpu", "accesskit_winit")


def forbidden(name: str) -> bool:
    return name in FORBIDDEN or name.startswith(FORBIDDEN_PREFIXES)


def packages(arguments: list[str], target: str) -> set[str]:
    tree = output(
        ["cargo", "tree", "--locked", "-e", "normal,build", "--prefix", "none", "--target", target]
        + arguments
    )
    return {line.split()[0] for line in tree.splitlines() if line.strip()}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("-p", "--package", help="check this package with its own features")
    parser.add_argument("--target", action="append", help="instead of both macOS targets")
    parser.add_argument("--no-build", action="store_true", help="only read the tree")
    parser.add_argument(
        "--self-test", action="store_true", help="check that the default (ui) build fails"
    )
    options = parser.parse_args()
    if options.self_test:
        found = [name for name in packages([], MAC_TARGETS[0]) if forbidden(name)]
        if not found:
            print("FAIL self-test: the egui build passed")
            return 1
        print(f"ok   self-test: the egui build fails on {len(found)} crates, egui among them")
        return 0
    arguments = selection(options.package)

    if not options.no_build:
        kind = [] if options.package else ["--lib"]
        run(["cargo", "build", "--locked"] + kind + arguments)

    failed = False
    for target in options.target or MAC_TARGETS:
        found = sorted(name for name in packages(arguments, target) if forbidden(name))
        if found:
            failed = True
            print(f"FAIL {target}: the headless core depends on {', '.join(found)}")
        else:
            print(f"ok   {target}: no egui, eframe, winit, glow, wgpu, rfd, arboard or egui-dependent fastframe crate")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
