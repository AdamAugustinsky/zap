#!/bin/bash
# Check the Go UI's Zap.app as users receive it (gnui-package.sh runs it):
#
#   packaging/macos/gnui-verify.sh dist/Zap.app
#
# The Info.plist keys upstream's bundle has (usage strings, version,
# category, copyright, minimum macOS) plus the URL types; the login agent;
# the core in Frameworks, signed by the app's signer; the icon; no stray
# files; arm64 only; the hardened runtime and the entitlements; codesign's
# strict verification. Prints the sizes and Gatekeeper's verdict, which
# rejects anything but a notarized Developer ID build (reported, not failed).
set -euo pipefail

app="${1:?usage: $0 path/to/Zap.app}"
app="${app%/}"
root="$(cd "$(dirname "$0")/../.." && pwd)"
config="$root/gnui/gnui.json"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT

codesign --verify --deep --strict --verbose=2 "$app"
exe="$app/Contents/MacOS/zapfast"
lib="$app/Contents/Frameworks/libzapfast_core.dylib"
for binary in "$exe" "$lib"; do
    archs="$(lipo -archs "$binary")"
    if [ "$archs" != arm64 ]; then
        echo "$binary: architectures $archs, want arm64 only" >&2
        exit 1
    fi
    codesign --verify --strict "$binary"
    codesign --display --verbose=4 "$binary" > "$temporary/$(basename "$binary").sig" 2>&1
done
codesign --display --entitlements - --xml "$app" > "$temporary/entitlements.plist" 2>/dev/null
otool -D "$lib" | sed -n 2p > "$temporary/install-name"

python3 - "$app" "$config" "$temporary" <<'PY'
import json
import os
import plistlib
import sys

app, config_path, tmp = sys.argv[1:4]
contents = os.path.join(app, "Contents")
with open(config_path, encoding="utf-8") as f:
    config = json.load(f)
with open(os.path.join(contents, "Info.plist"), "rb") as f:
    info = plistlib.load(f)
with open(os.path.join(tmp, "entitlements.plist"), "rb") as f:
    entitlements = plistlib.load(f)
failures = []

def check(ok, message):
    if not ok:
        failures.append(message)

app_id = config["id"]
version = config["version"]
expected = {
    "CFBundleIdentifier": app_id,
    "CFBundleName": config.get("displayName", config["name"]),
    "CFBundleDisplayName": config.get("displayName", config["name"]),
    "CFBundleExecutable": "zapfast",
    "CFBundlePackageType": "APPL",
    "CFBundleShortVersionString": version,
    "CFBundleVersion": config.get("build", version),
    "LSApplicationCategoryType": "public.app-category.social-networking",
    "NSHighResolutionCapable": True,
}
for key, value in expected.items():
    check(info.get(key) == value, f"Info.plist {key} is {info.get(key)!r}, want {value!r}")
check(info.get("LSMinimumSystemVersion", "") >= "14", f"LSMinimumSystemVersion {info.get('LSMinimumSystemVersion')!r}")
check("Not affiliated with WhatsApp or Meta" in info.get("NSHumanReadableCopyright", ""), "Info.plist has no copyright line")
for key, text in config["usage"].items():
    check(info.get(key) == text, f"Info.plist {key} is {info.get(key)!r}")
for key in ("NSMicrophoneUsageDescription", "NSCameraUsageDescription"):
    check(info.get(key, "").strip() != "", f"Info.plist is missing {key}")
schemes = [s for t in info.get("CFBundleURLTypes", []) for s in t.get("CFBundleURLSchemes", [])]
check(schemes == ["whatsapp", "wa"], f"URL schemes {schemes}, want whatsapp and wa")

for key in ("com.apple.security.device.audio-input", "com.apple.security.device.camera"):
    check(entitlements.get(key) is True, f"the signed app is missing {key}")

# The login item: SMAppService's agent, generated from the id in use.
label = f"{app_id}.autostart"
agent_path = os.path.join(contents, "Library", "LaunchAgents", label + ".plist")
if os.path.isfile(agent_path):
    with open(agent_path, "rb") as f:
        agent = plistlib.load(f)
    check(agent.get("Label") == label, f"agent label {agent.get('Label')!r}")
    check(agent.get("BundleProgram") == "Contents/MacOS/zapfast", f"agent program {agent.get('BundleProgram')!r}")
    check(agent.get("ProgramArguments") == ["zapfast", "--start-hidden"], f"agent arguments {agent.get('ProgramArguments')!r}")
    check(agent.get("AssociatedBundleIdentifiers") == [app_id], f"agent bundle {agent.get('AssociatedBundleIdentifiers')!r}")
    check(agent.get("RunAtLoad") is True and agent.get("LimitLoadToSessionType") == "Aqua", "agent must run at load in the Aqua session")
else:
    failures.append(f"no login agent at {agent_path}")

# The icon.
icon = os.path.join(contents, "Resources", info.get("CFBundleIconFile", "") + ".icns")
check(os.path.isfile(icon) and os.path.getsize(icon) > 0, f"no icon at {icon}")

# Every file in the bundle is one of these.
allowed = {
    "Info.plist",
    "MacOS/zapfast",
    "Frameworks/libzapfast_core.dylib",
    f"Library/LaunchAgents/{label}.plist",
    "Resources/" + os.path.basename(icon),
    "Resources/LICENSE",  # MIT: upstream's notice travels with the app
    "Resources/THIRD-PARTY-NOTICES.md",
    "_CodeSignature/CodeResources",
}
found = set()
for directory, dirs, files in os.walk(contents):
    for name in files + [d for d in dirs if os.path.islink(os.path.join(directory, d))]:
        found.add(os.path.relpath(os.path.join(directory, name), contents))
check(found == allowed, f"bundle files: stray {sorted(found - allowed)}, missing {sorted(allowed - found)}")
check(sorted(os.listdir(app)) == ["Contents"], f"stray entries beside Contents: {sorted(os.listdir(app))}")

# The core: signed by the app's signer, hardened, loaded through its rpath name.
def signature(name):
    fields = {}
    with open(os.path.join(tmp, name + ".sig"), encoding="utf-8") as f:
        for line in f:
            key, sep, value = line.rstrip("\n").partition("=")
            if sep:
                fields.setdefault(key, value)
    return fields

exe_sig, lib_sig = signature("zapfast"), signature("libzapfast_core.dylib")
# Only a signer without a Team ID (gnui-dev) needs library validation off to
# load the core under the hardened runtime; a Developer ID build must not
# carry the exception.
wanted = dict(config["entitlements"])
if exe_sig.get("TeamIdentifier", "not set") == "not set":
    wanted["com.apple.security.cs.disable-library-validation"] = True
check(entitlements == wanted, f"entitlements {entitlements}, want {wanted}")
check(exe_sig.get("Authority") == lib_sig.get("Authority"), f"core signed by {lib_sig.get('Authority')!r}, app by {exe_sig.get('Authority')!r}")
check(exe_sig.get("TeamIdentifier") == lib_sig.get("TeamIdentifier"), "core and app have different Team IDs")
for name, sig in (("app", exe_sig), ("core", lib_sig)):
    check("runtime" in sig.get("CodeDirectory v", ""), f"the {name} is not signed with the hardened runtime")
    check(sig.get("Signature") != "adhoc", f"the {name} is signed ad-hoc")
with open(os.path.join(tmp, "install-name"), encoding="utf-8") as f:
    check(f.read().strip() == "@rpath/libzapfast_core.dylib", "the core's install name is not @rpath/libzapfast_core.dylib")

if failures:
    sys.exit("\n".join(["The bundle fails its checks:"] + failures))
print(f"Verified {app}: {app_id} {version}, signed by {exe_sig.get('Authority', 'ad-hoc')} (Team ID {exe_sig.get('TeamIdentifier')}), hardened runtime")
PY

# Sizes: the plan's budget is a Go executable under 10 MB.
exe_bytes="$(stat -f %z "$exe")"
lib_bytes="$(stat -f %z "$lib")"
bundle_kib="$(du -sk "$app" | cut -f1)"
printf 'sizes: executable %s B (%.1f MiB), core %s B (%.1f MiB), bundle %s KiB on disk (%.1f MiB)\n' \
    "$exe_bytes" "$(echo "$exe_bytes / 1048576" | bc -l)" \
    "$lib_bytes" "$(echo "$lib_bytes / 1048576" | bc -l)" \
    "$bundle_kib" "$(echo "$bundle_kib / 1024" | bc -l)"
if [ "$exe_bytes" -ge 10000000 ]; then
    echo "note: the executable is over the 10 MB budget"
fi

# Gatekeeper: only a notarized Developer ID build passes.
echo "spctl --assess:"
spctl --assess --type execute -vv "$app" 2>&1 | sed 's/^/  /' || true
