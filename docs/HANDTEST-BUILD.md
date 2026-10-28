# Hand-test build (macOS, Apple silicon)

How to build ZapFast's Go UI as an app bundle on the Mac mini and run it on
the MacBook. The bundle is `Zap.app` (gnui.json's bundleName; the app shows
as Zap), id `ai.a3lab.zap`, version 0.19.0, arm64 only, macOS 14 or
later.

**Delivery status (2026-10-23).** Certified ZapFast source `b8789025` with shared
framework `693d3b38` now has verified development and release app/DMG/ZIP files
on the Mac mini's SSD. See [delivery paths, hashes and limits](measurements/final-delivery-2026-10-23.md).
Both use the existing self-signed `gnui-dev` identity, not Developer ID, and are
not notarized. The KeepCorrupt and IA-006 blockers are resolved; see
[PARITY.md](PARITY.md#not-done) for the remaining evidence gaps. The approved
temporary SSH route is now recovered and the exact dev/test artifacts are
verified under `~/gnui-test/final-2026-10-23/` on the MacBook, without installation.
Fullscreen, settled idle, empty-framework memory, strict 1M-row and two-window
checks passed. The parent-authorized fresh-child startup set passed 10/10
(median 141.171 ms); original guard-contaminated samples remain invalid evidence.
Pilot passed after clipboard authorization. ZapFast's full gate attempt failed
on a missing “Newest message” control and emitted no allocation report. See the
delivery report for every sample, environment and limits. The Developer ID
now exists on the MacBook only (2026-10-27; see [Before distribution](#before-distribution)),
but no Zap build has been signed with it or notarized yet;
the final bundle ID and update-feed/key decisions remain pending. Do not change
them as part of hand-test preparation. These Go-bundle instructions are separate
from the upstream Rust release workflow in [PACKAGING.md](../PACKAGING.md).

## 1. Build (Mac mini)

```sh
export PATH=$HOME/sdk/go1.27.1/bin:$HOME/.rustup/toolchains/1.98.0-aarch64-apple-darwin/bin:$PATH
export CGO_ENABLED=0
export CARGO_TARGET_DIR=/Volumes/SSD/dev-toolchains/build/zf-integ-package-target   # this worktree's SSD build directory
packaging/macos/gnui-package.sh --dev --dmg   # the hand-test bundle, in dist/dev/
packaging/macos/gnui-package.sh --dmg         # the release bundle, for the hand test's last group, in dist/
```

**The hand test uses a development build.** Upstream compiles its demo and
measurement flags out of release builds, as this release build does, and the
hand test needs them. `--dev` makes the same bundle (id, signature,
entitlements, core) without `gnui_release`: it differs only by those
diagnostics and the framework's Debug menu, and `TestReleaseOptions` covers
the release entrypoint.

Each command builds the release core (`cargo build --locked -p zapfast_core --profile
core-release`), copies it to `target/core-release/` (where `gnui/gnui.json`
expects it), builds the framework's `gnui` CLI, runs `gnui package` (the Go
binary with `-trimpath -ldflags "-s -w" -tags gnui_release`, the tag left out
with `--dev`; the core in `Contents/Frameworks`, signed inside out with the
hardened runtime) and then `packaging/macos/gnui-verify.sh`. Output, in
`dist/dev/` with `--dev` and in `dist/` without:

- `Zap.app`
- `ZapFast-0.19.0.zip`: the update archive (its name is the one installed
  updaters accept, so it keeps ZapFast's); the pinned framework supports
  `-zip`, which the wrapper enables automatically. A local/dev ZIP is not a
  distribution-ready update or proof of an operational update feed.
- `ZapFast-0.19.0.dmg`: the app and an Applications link, as upstream's
  DMG. Unsigned: only a Developer ID can sign it.

`dist/` and `target/` are ignored by git.

**Signing.** Without `CODESIGN_IDENTITY` the build is signed with the
framework's development identity, `gnui-dev`, a self-signed certificate on
the Mac mini only. Because a self-signed certificate has no Team ID, the
hardened runtime's library validation would refuse the core ("different Team
IDs"), so this build adds `com.apple.security.cs.disable-library-validation`
to the app's entitlements. The hardened runtime stays on, and the app loads
only `Contents/Frameworks/libzapfast_core.dylib` (`core.ResolveLibrary`). A
Developer ID build signs both binaries with one Team ID and does not get that
entitlement; `gnui-verify.sh` fails a Developer ID bundle that carries it.

Do not copy the `gnui-dev` keychain or key to the MacBook. Copy the bundle
only. Privacy grants (notifications, microphone, camera) are tied to the
certificate and the bundle id, so later builds from the Mac mini keep them.
Here `--dev` is the ZapFast wrapper's diagnostics-enabled package mode: it
keeps `ai.a3lab.zap`. It is not `gnui run`, whose development bundle
uses the `.dev` suffix. Do not mix the two sets of data paths or permissions.
The wrapper rejects `--dev --notarize`; use the release command for notarization.

## 2. Copy to the MacBook

This build installs as `/Applications/Zap.app` (or `~/Applications/`), beside
upstream ZapFast's `/Applications/ZapFast.app` (id `me.paolino.fastsapp`) if
that is there: the two keep separate data, Keychain items and login items.
App switchers and the Dock show the folder's name, so an earlier copy
installed as `ZapFast Go.app` should be renamed `Zap.app` (the folder name is
not part of the signature, and the data follows the id). Below, `APP` is the
path you chose.

**Over SSH, from the Mac mini** (no quarantine, so nothing for Gatekeeper to
check):

The command below is a historical installation example, **not** the route or
scope used for current final measurements. Use the [explicit approved SSH
override and dedicated test directory](measurements/final-delivery-2026-10-23.md#macbook-access-recovered-measurements-collected)
for that task; do not overwrite Applications during measurement preparation.

```sh
rsync -a --delete dist/dev/Zap.app/ macbook:"/Applications/Zap.app/"
```

**Or by AirDrop, Messages or a download** of `dist/dev/ZapFast-0.19.0.dmg`. Open
the DMG on the MacBook and drag Zap to Applications. Files that arrive this way are quarantined, so see
step 3.

**The release bundle** is only for the hand test's last group (Release
build). Quit Zap and copy `dist/Zap.app/` the same way, to the same
`APP`, in place of the hand-test build. It has the same id, so it keeps the
same data, Keychain items and privacy grants. Copy `dist/dev/Zap.app/`
back afterwards if you test again.

Then, on the MacBook:

```sh
APP="/Applications/Zap.app"
codesign --verify --deep --strict --verbose=2 "$APP"   # valid on disk, satisfies its Designated Requirement
spctl -a -vv "$APP"                     # "rejected, origin=gnui-dev": expected, the build is not notarized
```

## 3. Gatekeeper (only if the app was quarantined)

A quarantined app that isn't notarized is blocked at first open ("Apple could
not verify…"). There are three ways past this:

- **Simplest:** `xattr -dr com.apple.quarantine "$APP"`, then open it
  normally.
- **System Settings:** open the app once and dismiss the warning. Then go to
  System Settings → Privacy & Security, find "ZapFast was blocked…" and click
  **Open Anyway**. Confirm with your password.
- **Control-click (right-click) → Open:** this only works on macOS 14. From
  macOS 15 on it no longer bypasses Gatekeeper; use one of the two above.

`xattr -l "$APP"` shows whether `com.apple.quarantine` is set.

## 4. Run

```sh
open -n "$APP" --args -demo     # the demo core: a synthetic account, no network, nothing saved
open "$APP"                     # dedicated test account only (link it with the QR code)
```

- **Data.** The test account uses `~/Library/Application Support/ai.a3lab.zap`
  and `~/Library/Caches/ai.a3lab.zap`. Its archive key is in the
  Keychain under the service `ai.a3lab.zap`. None of this is
  shared with upstream ZapFast.
- **Log.** The log is `~/Library/Application Support/ai.a3lab.zap/zapfast.log`,
  holding warnings and errors from the last run. A Go crash also writes
  `panic.log` beside it.
- **Command line.** `"$APP/Contents/MacOS/zapfast" quit` quits the running
  copy, and `… wa.me/<number>` opens a chat in it.
- **Permissions.** Notifications are asked for at the first notification.
  The microphone and camera are asked for at the first voice message or call.
- **Start at login.** Settings → Start at login registers
  `ai.a3lab.zap.autostart`. Approve it in System Settings → General
  → Login Items if asked. It launches the app hidden (`--start-hidden`, the
  status item only). Turn it off again after testing.

## 5. Remove

Turn Start at login off, quit ZapFast, confirm `APP` names this test bundle
(not upstream ZapFast), then move only that bundle to the Trash in Finder.
Preserve data, cache and Keychain items by default. For the disposable second
test account, use [HT-42 in HANDTEST.md](HANDTEST.md#16-system-behaviour-real)
before removing the bundle: the app must delete its own exact archive key.
Do not bulk-delete the service's Keychain items or wipe the data directories;
they may contain a retained account, and manual deletion would invalidate the
check. A complete data/permission reset needs a separately confirmed test-only
scope. Never reset Adam's personal account.

## Before distribution

- **Signing and notarization (macOS).** The identity is
  `Developer ID Application: A3 Direct Solutions LLP (JPXN94NG33)`, valid
  until 2027-11-26. Its private key is in the MacBook's login keychain and
  nowhere else: it is not on the Mac mini or in CI, and exporting it needs
  Adam's agreement first. So a release is signed on the MacBook. The MacBook
  has the Command Line Tools (notarytool, stapler) but no Go or Rust.
  `gnui package` builds and signs in one step and cannot sign a bundle built
  elsewhere, so a release goes like this:
  1. On the Mac mini, build the core
     (`target/core-release/libzapfast_core.dylib`) and run the usual checks.
  2. On the MacBook, Adam stores the notary credentials once (this asks for
     an app-specific password from account.apple.com, and only the MacBook's
     keychain keeps it):
     `xcrun notarytool store-credentials zapfast-notary --apple-id <Apple ID> --team-id JPXN94NG33`.
  3. Install Go on the MacBook (Adam approves this first). Copy over the
     checkout at the release commit, the framework checkout it resolves to,
     and the core. Then run
     `CODESIGN_IDENTITY="Developer ID Application: A3 Direct Solutions LLP (JPXN94NG33)" GNUI_NOTARY_PROFILE=zapfast-notary packaging/macos/gnui-package.sh --dmg --notarize --skip-core`.
     A Developer ID build signs the core and the app with one Team ID and
     does not get the `disable-library-validation` exception.
     `gnui-verify.sh` fails the build if it has that exception.
  4. Check: `xcrun stapler validate dist/Zap.app`;
     `spctl -a -vv dist/Zap.app` and `spctl -a -t open --context
     context:primary-signature -vv dist/ZapFast-<version>.dmg` should print
     `source=Notarized Developer ID`. Then open a quarantined copy of the
     downloaded DMG on a Mac that has never run Zap.

  The other route is to sign a bundle the Mac mini built, on the MacBook,
  with a new sign-only mode in `gnui package`. That mode would be a
  framework change, and it is not built.

  **Changing signers once.** Installed copies are signed with `gnui-dev`
  and have no Team ID. The updater accepts only an app with the running
  app's own signature (`app/selfupdate`), so they cannot update themselves
  to the Developer ID build. Install that build by hand, once. The archive
  key is a Keychain item whose access list trusts the old signature, so
  expect macOS to ask once to let the new build read it. Check this on the
  MacBook, with the backup copy at hand, before anyone else gets the build:
  the app must open its existing archive, and microphone and camera access
  must still work.

  Before public distribution, also settle the bundle id (it names the data
  folder, the Keychain service and the login item, so change it before
  anyone installs, never after) and the OpenH264 patent question in
  `docs/GNUI-MIGRATION-PLAN.md` (Packaging). `NSHumanReadableCopyright`
  names upstream's author. The MIT licence requires keeping that notice; the
  publisher's name can be added beside it.
- **Windows signing.** There is no Authenticode certificate yet. The Go UI
  is macOS-only for now (upstream's egui build is the Windows client), so
  nothing is blocked today. A Windows Go build must not be published until
  a certificate exists (an OV/EV certificate or Azure Trusted Signing):
  without one, SmartScreen blocks the installer.
- **The update feed.** The pinned framework already writes the ZIP that
  `app/selfupdate` installs; the wrapper enables it for both output directories.
  Feed hosting, the approved update-signing key and a newer signed/notarized
  release for an end-to-end update remain pending. Never publish `dist/dev/`
  as an update. Existing UP-003/UP-006 hand checks stay blocked without their
  prerequisites; generating a ZIP does not satisfy them.
- **The DMG's signature.** Only a Developer ID can sign the DMG. With
  `--notarize` it is also notarized and stapled.
