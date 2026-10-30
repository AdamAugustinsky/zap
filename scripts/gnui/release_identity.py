#!/usr/bin/env python3
"""Resolves the framework Zap.app is built against and writes the build manifest.

`gnui/release-deps.json` pins the framework (go-native-ui) by commit and by
that commit's tree. Two steps, around building the gnui CLI:

    release_identity.py resolve --shell OUT
    release_identity.py manifest --variant release|dev --framework DIR \\
        --gnui CLI --manifest OUT --shell OUT [--gnui-cli-override]

`resolve` finds what `gnui/` resolves `gonativeui` to (go.mod's replace, or
an ignored gnui/go.work), or, with `$ZAP_FRAMEWORK_DIR`, writes a go.work
that builds against that directory instead (a development override). It
writes `framework` (the directory) and `gowork` (the generated go.work, or
empty) to --shell, for the packaging script to source.

`manifest` reads the framework's identity, refuses a framework that is not
the pinned commit or has uncommitted changes (unless overridden, which is
announced and recorded), writes the build manifest
(Contents/Resources/build-manifest.json), and adds `ldflags` (the -X stamps)
to --shell. The identity comes from `gnui provenance -json` when the CLI has
that command (gnui.provenance/v1, provisional), else from git, else, for a
directory without git, from its tree hash compared with the pin's.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
GNUI = ROOT / "gnui"
DEPS = GNUI / "release-deps.json"
CONFIG = GNUI / "gnui.json"
TYPESETTING = "github.com/go-text/typesetting"
BUILDINFO = "zapfast/internal/buildinfo"
PROVENANCE_SCHEMA = "gnui.provenance/v1"
PROVENANCE_PACKAGE = "gonativeui/provenance"


class Refused(Exception):
    pass


def warn(message: str) -> None:
    print(f"warning: {message}", file=sys.stderr, flush=True)


def git(directory: Path, *args: str, env: dict | None = None) -> str:
    # Read-only: status must not refresh another checkout's index.
    env = dict(env or os.environ, GIT_OPTIONAL_LOCKS="0")
    return subprocess.run(
        ["git", *args], cwd=directory, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=env
    ).stdout.strip()


def toplevel(directory: Path) -> Path | None:
    """The directory's git work tree root, or None outside one."""
    try:
        return Path(git(directory, "rev-parse", "--show-toplevel")).resolve()
    except subprocess.CalledProcessError:
        return None


def export_tree(directory: Path) -> str:
    """Git's tree hash of every file under a directory without git.

    The files are hashed into a scratch object store under .cache/ (never
    /tmp, AGENTS.md), ignored files included, so an export of a commit
    matches that commit's tree only when it has nothing more and nothing less.
    """
    scratch_root = ROOT / ".cache"
    scratch_root.mkdir(exist_ok=True)
    scratch = Path(tempfile.mkdtemp(prefix="framework-tree.", dir=scratch_root))
    try:
        env = dict(os.environ, GIT_DIR=str(scratch / "git"), GIT_WORK_TREE=str(directory))
        env.pop("GIT_INDEX_FILE", None)
        subprocess.run(["git", "init", "-q", "--bare", str(scratch / "git")], check=True)
        git(directory, "-c", "core.autocrlf=false", "-c", "core.fileMode=true", "add", "-A", "-f", ".", env=env)
        return git(directory, "write-tree", env=env)
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def go_tool() -> str:
    """The go gnui package builds with: $GNUI_GO, or go from PATH."""
    return os.environ.get("GNUI_GO") or "go"


def go_env() -> dict:
    return dict(os.environ, CGO_ENABLED="0")


def go_directive() -> str:
    match = re.search(r"^go (\S+)$", (GNUI / "go.mod").read_text(encoding="utf-8"), re.M)
    if not match:
        raise Refused("gnui/go.mod has no go directive")
    return match.group(1)


# ---- resolve ----


def resolved_dirs(env: dict) -> tuple[Path, Path]:
    """Where `gnui/` resolves gonativeui and the patched typesetting to."""
    done = subprocess.run(
        [go_tool(), "list", "-m", "-f", "{{.Path}} {{.Dir}}", "gonativeui", TYPESETTING],
        cwd=GNUI,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if done.returncode != 0:
        raise Refused(
            "gnui/ does not resolve gonativeui:\n"
            + done.stderr.strip()
            + "\nput the pinned framework where go.mod's replace points, add an ignored"
            " gnui/go.work (gnui/README.md), or set ZAP_FRAMEWORK_DIR for a development build"
        )
    dirs = {}
    for line in done.stdout.splitlines():
        path, _, directory = line.partition(" ")
        dirs[path] = directory
    for path in ("gonativeui", TYPESETTING):
        if not dirs.get(path):
            raise Refused(f"gnui/ resolves {path} to no directory: it must be replaced by a local framework")
    return Path(dirs["gonativeui"]).resolve(), Path(dirs[TYPESETTING]).resolve()


def write_gowork(framework: Path, path: Path) -> None:
    path.write_text(
        f"go {go_directive()}\n\n"
        f"use {GNUI}\n\n"
        f"replace gonativeui => {framework}\n\n"
        f"replace {TYPESETTING} => {framework / 'third_party' / 'typesetting'}\n",
        encoding="utf-8",
    )


def resolve(args: argparse.Namespace) -> None:
    override = os.environ.get("ZAP_FRAMEWORK_DIR", "")
    env = go_env()
    gowork = ""
    if override:
        wanted = Path(override).expanduser().resolve()
        if not (wanted / "go.mod").is_file():
            raise Refused(f"ZAP_FRAMEWORK_DIR={override} has no go.mod")
        gowork = str(args.shell.resolve().parent / "go.work")
        write_gowork(wanted, Path(gowork))
        env["GOWORK"] = gowork
    framework, typesetting = resolved_dirs(env)
    if not (framework / "go.mod").is_file():
        raise Refused(
            f"gnui/ resolves gonativeui to {framework}, which holds no framework: add an ignored"
            " gnui/go.work for the pinned framework (gnui/README.md), or set ZAP_FRAMEWORK_DIR"
            " for a development build"
        )
    if override and framework != wanted:
        raise Refused(f"the generated go.work resolves gonativeui to {framework}, not {wanted}")
    if typesetting != framework / "third_party" / "typesetting":
        raise Refused(
            f"{TYPESETTING} resolves to {typesetting}, not the framework's third_party/typesetting: "
            "replace both from one framework"
        )
    args.shell.write_text(
        f"framework={shlex.quote(str(framework))}\ngowork={shlex.quote(gowork)}\n", encoding="utf-8"
    )


# ---- manifest ----


def provenance_json(cli: Path, directory: Path) -> dict | None:
    """The framework's own provenance (`gnui provenance -json`), or None from a CLI without it."""
    done = subprocess.run(
        [str(cli), "provenance", "-json", "-dir", str(directory)],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if done.returncode != 0 and 'unknown command "provenance"' in done.stderr:
        return None
    if done.returncode != 0:
        raise Refused(f"gnui provenance failed:\n{done.stderr.strip()}")
    try:
        found = json.loads(done.stdout)
    except json.JSONDecodeError as err:
        raise Refused(f"gnui provenance wrote no JSON: {err}") from err
    if found.get("schema") != PROVENANCE_SCHEMA:
        raise Refused(f"gnui provenance's schema is {found.get('schema')!r}, not {PROVENANCE_SCHEMA}")
    return found


def framework_identity(directory: Path, pin: dict, cli: Path) -> tuple[dict, dict, dict | None]:
    """The manifest's framework block, how it was resolved, and the framework's own provenance.

    The one adapter that reads the framework. The block has
    gnui.provenance/v1's names (module, revision, dirty, source), absent
    facts omitted. From a CLI with `gnui provenance` the block is its
    framework block; otherwise git's (source "git"); a directory without git
    has no revision of its own, and gets the pinned one (source "tree-hash",
    Zap's own value) only when its files are exactly the pinned tree.
    """
    resolution: dict = {"checkout": "git" if toplevel(directory) == directory else "export"}
    provenance = provenance_json(cli, directory)
    if provenance is not None:
        block = dict(provenance.get("framework", {}))
    elif resolution["checkout"] == "git":
        block = {
            "module": pin["module"],
            "revision": git(directory, "rev-parse", "HEAD"),
            "dirty": git(directory, "status", "--porcelain", "--untracked-files=normal") != "",
            "source": "git",
        }
    else:
        block = {"module": pin["module"]}
    if "revision" not in block and resolution["checkout"] == "export":
        # Not a work tree of its own (an export, or a copy inside another
        # repository) and no stamp: only its contents identify it.
        resolution["tree"] = export_tree(directory)
        if resolution["tree"] == pin["tree"]:
            block.update(revision=pin["commit"], dirty=False, source="tree-hash")
    return block, resolution, provenance


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as file:
        for block in iter(lambda: file.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def whatsmeow_entry(pin: dict) -> dict:
    """whatsmeow as the build takes it: go.mod's required version, replaced by Zap's patched copy.

    The copy's identity is its tree in Zap's commit (git rev-parse
    HEAD:<dir>), with a dirty flag for uncommitted changes under it; its
    ZAP-PATCH.md says what the patch is.
    """
    done = subprocess.run(
        [go_tool(), "list", "-m", "-f", "{{.Version}} {{with .Replace}}{{.Dir}}{{end}}", pin["module"]],
        cwd=GNUI, env=go_env(), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    if done.returncode != 0:
        raise Refused(f"gnui/ does not resolve {pin['module']}:\n{done.stderr.strip()}")
    # A directory, not go.mod's path: a go.work's replace paths are relative to it.
    version, _, replaced = done.stdout.strip().partition(" ")
    wanted = (GNUI / pin["replace"]).resolve()
    if not replaced or Path(replaced).resolve() != wanted:
        raise Refused(f"gnui/ replaces {pin['module']} with {replaced or 'nothing'}, not {wanted}"
                      " (gnui/release-deps.json)")
    copy = wanted.relative_to(ROOT).as_posix()
    return {
        "module": pin["module"],
        "version": version,
        "replace": pin["replace"],
        "patch": {
            "tree": git(ROOT, "rev-parse", f"HEAD:{copy}"),
            "dirty": git(ROOT, "status", "--porcelain", "--untracked-files=normal", "--", copy) != "",
            "notes": pin["patchNotes"],
        },
    }


def script_pin(script: Path) -> dict:
    """The source release a library's build script pins and verifies (its version= and sha256= lines)."""
    text = script.read_text(encoding="utf-8")
    pinned = {"script": script.relative_to(ROOT).as_posix()}
    for key, pattern in (("version", r"^version=(\S+)$"), ("sha256", r"^sha256=([0-9a-f]{64})$")):
        match = re.search(pattern, text, re.M)
        if match:
            pinned[key] = match.group(1)
    return pinned


def native_libs(pins: list, config: dict) -> list:
    """The bundled libraries: each one's sha256 as built, and its source pin.

    builtSha256 is the file the bundle copies, before `gnui package` signs
    the copy again (which changes its bytes, so the bundled file has another
    hash). gnui.json's frameworks must be exactly the libraries
    release-deps.json expects. A missing file (gnui-package.sh --check before
    a build) leaves the hash out.
    """
    shipped = sorted((GNUI / f).resolve() for f in config.get("frameworks", []))
    expected = sorted((ROOT / lib["path"]).resolve() for lib in pins)
    if shipped != expected:
        raise Refused("gnui.json's frameworks are not the libraries gnui/release-deps.json expects: "
                      f"{[str(f) for f in shipped]}")
    out = []
    for lib in pins:
        path = ROOT / lib["path"]
        entry = {"name": path.name}
        if path.is_file():
            entry["builtSha256"] = sha256(path)
        entry["source"] = script_pin(ROOT / lib["script"])
        out.append(entry)
    return out


def check_expectations(deps: dict, config: dict) -> None:
    """What release-deps.json says Zap does not ship, it does not ship.

    libghostty-vt would be one of gnui.json's nativeLibs; Zap links neither it
    nor the terminal widget. No metallib exists until the framework's CI makes one.
    """
    if deps["ghostty"] != {"status": "absent"}:
        raise Refused("gnui/release-deps.json expects libghostty-vt, which this script cannot identify yet")
    if config.get("nativeLibs"):
        raise Refused("gnui.json now ships nativeLibs: record libghostty-vt's identity in gnui/release-deps.json")
    if deps["shaders"] != {"artifact": "none"}:
        raise Refused("gnui/release-deps.json expects a shader artifact this script cannot identify yet")


def app_entry(config: dict) -> dict:
    dirty = git(ROOT, "status", "--porcelain", "--untracked-files=normal") != ""
    if dirty:
        warn("Zap's checkout has uncommitted changes: the manifest records it as dirty")
    return {
        "name": config.get("displayName") or config.get("name"),
        "version": config.get("version"),
        "revision": git(ROOT, "rev-parse", "HEAD"),
        "dirty": dirty,
    }


def build_tags(variant: str, config: dict) -> list[str]:
    tags = [t for t in re.split(r"[ ,]+", config.get("tags", "")) if t]
    if variant == "release" and "gnui_release" not in tags:
        tags.append("gnui_release")  # gnui package always adds it; --dev's go drops it
    return tags


def manifest(args: argparse.Namespace) -> None:
    deps = json.loads(DEPS.read_text(encoding="utf-8"))
    if deps.get("schema") != 1:
        raise Refused(f"{DEPS}: unknown schema {deps.get('schema')}")
    config = json.loads(CONFIG.read_text(encoding="utf-8"))
    check_expectations(deps, config)
    pin = deps["framework"]
    override = bool(os.environ.get("ZAP_FRAMEWORK_DIR"))

    block, resolution, provenance = framework_identity(args.framework, pin, args.gnui)
    resolution = {"pin": "override" if override else "pinned", "expected": pin["commit"], **resolution}
    what = block.get("revision") or "no known revision, tree " + resolution.get("tree", "?")
    if override:
        warn(f"DEVELOPMENT OVERRIDE: building against the framework in {args.framework} ({what}"
             + (", with uncommitted changes" if block.get("dirty") else "")
             + f"), not the pinned {pin['commit']}. Not for release.")
    elif block.get("revision") != pin["commit"]:
        raise Refused(f"the framework in {args.framework} is at {what}, not the pinned {pin['commit']}"
                      " (gnui/release-deps.json)")
    elif block.get("dirty") is not False:
        raise Refused(f"the framework in {args.framework} is at the pinned commit with uncommitted changes"
                      " (or reports no dirty flag)")
    if args.gnui_cli_override:
        warn("DEVELOPMENT OVERRIDE: $GNUI is a prebuilt gnui CLI, not one built from the resolved framework")

    go = subprocess.run(
        [go_tool(), "env", "GOVERSION"], cwd=GNUI, env=go_env(), check=True, stdout=subprocess.PIPE, text=True
    ).stdout.strip()
    native = native_libs(deps["nativeLibs"], config)
    out: dict = {
        "schema": "zap.build/v1",
        "app": app_entry(config),
        "build": {
            "variant": args.variant,
            "tags": build_tags(args.variant, config),
            "gnuiCli": "override" if args.gnui_cli_override else "framework",
        },
        "framework": block,
        "frameworkResolution": resolution,
    }
    if provenance is not None:
        out["frameworkProvenance"] = provenance  # verbatim
    # The framework's reader reports the shaders Zap renders with; without it
    # there is no metallib. Its ghostty block is the framework's pin, which
    # stays in frameworkProvenance: Zap links no libghostty-vt
    # (check_expectations), so it has no ghostty entry of its own.
    out["shaders"] = (provenance or {}).get("shaders", {"artifact": "none"})
    if not override and out["shaders"].get("artifact") != deps["shaders"]["artifact"]:
        raise Refused(f"the framework reports a {out['shaders'].get('artifact')} shader artifact, which"
                      " gnui/release-deps.json does not expect")
    out["go"] = {"version": go, "goos": "darwin", "goarch": "arm64"}
    out["whatsmeow"] = whatsmeow_entry(deps["whatsmeow"])
    out["nativeLibs"] = native
    # How each field is known: build-verified ones are computed from what
    # ships; packager-asserted ones from the sources and tools this run used
    # (git, go.mod, the build scripts' pins, go env, the framework's
    # reader), which what ships cannot confirm.
    out["basis"] = {
        "build-verified": ["nativeLibs[].builtSha256"] if any("builtSha256" in lib for lib in native) else [],
        "packager-asserted": [k for k in out if k not in ("schema", "nativeLibs")] + ["nativeLibs[].source"],
    }
    args.manifest.parent.mkdir(parents=True, exist_ok=True)
    args.manifest.write_text(json.dumps(out, indent="\t") + "\n", encoding="utf-8")

    stamp = {
        f"{BUILDINFO}.revision": out["app"]["revision"],
        f"{BUILDINFO}.dirty": str(out["app"]["dirty"]).lower(),
        f"{BUILDINFO}.frameworkRevision": block.get("revision", ""),
        f"{BUILDINFO}.frameworkDirty": str(block.get("dirty", "")).lower(),
        f"{BUILDINFO}.frameworkPin": resolution["pin"],
    }
    # The framework's own stamp, where it has the package (gnui.provenance/v1).
    if block.get("revision") and (args.framework / "provenance").is_dir():
        stamp[f"{PROVENANCE_PACKAGE}.FrameworkRevision"] = block["revision"]
    ldflags = " ".join(f"-X {name}={value}" for name, value in stamp.items() if value)
    with args.shell.open("a", encoding="utf-8") as shell:
        shell.write(f"ldflags={shlex.quote(ldflags)}\n")
    print(f"framework: {resolution['pin']} {what} ({block.get('source', 'unknown')}) in {args.framework}",
          file=sys.stderr)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    steps = parser.add_subparsers(dest="step", required=True)
    step = steps.add_parser("resolve")
    step.add_argument("--shell", type=Path, required=True)
    step = steps.add_parser("manifest")
    step.add_argument("--variant", choices=["release", "dev"], required=True)
    step.add_argument("--framework", type=Path, required=True, help="what resolve found")
    step.add_argument("--gnui", type=Path, required=True, help="the gnui CLI in use")
    step.add_argument("--manifest", type=Path, required=True)
    step.add_argument("--shell", type=Path, required=True)
    step.add_argument("--gnui-cli-override", action="store_true", help="$GNUI, a prebuilt gnui CLI, is in use")
    args = parser.parse_args()
    (resolve if args.step == "resolve" else manifest)(args)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Refused as refusal:
        print(f"error: {refusal}", file=sys.stderr)
        sys.exit(1)
