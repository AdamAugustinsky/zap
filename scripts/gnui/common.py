"""Shared helpers for the headless gate scripts (docs/GNUI-MIGRATION-PLAN.md, "The gate")."""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

# The two architectures the Go UI's bundle carries.
MAC_TARGETS = ["aarch64-apple-darwin", "x86_64-apple-darwin"]

# How the core is built until `zfcore` exists: upstream's crate without `ui`.
HEADLESS = ["--no-default-features", "--features", "headless"]


def selection(package: str | None) -> list[str]:
    """Cargo arguments that pick the crate under test.

    With a package (the `zfcore` crate, `zapfast_core`), its own features
    apply; without one, upstream's crate is built headless.
    """
    if package:
        return ["-p", package]
    return HEADLESS


def run(command: list[str], **kwargs) -> subprocess.CompletedProcess:
    print("+ " + " ".join(command), file=sys.stderr, flush=True)
    return subprocess.run(command, cwd=kwargs.pop("cwd", ROOT), check=True, **kwargs)


def output(command: list[str], **kwargs) -> str:
    print("+ " + " ".join(command), file=sys.stderr, flush=True)
    return subprocess.run(
        command, cwd=kwargs.pop("cwd", ROOT), check=True, stdout=subprocess.PIPE, text=True, **kwargs
    ).stdout


def target_directory() -> Path:
    if "CARGO_TARGET_DIR" in os.environ:
        return Path(os.environ["CARGO_TARGET_DIR"]).resolve()
    metadata = json.loads(
        output(["cargo", "metadata", "--format-version", "1", "--no-deps", "--locked"])
    )
    return Path(metadata["target_directory"])
