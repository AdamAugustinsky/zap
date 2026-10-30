# Update signatures

Zap's updates are signed with an Ed25519 publisher key whose public half is
embedded in the app (`gnui/ui/settings/update-public-key.hex`, and the next
release key in `update-public-key-next.hex`). Both are A3's keys since
2026-10-30:

| key | public half |
| --- | --- |
| current | `c5adf2e534da47ab5dfd6064a3c7d8eb66f913203f950254d2515c6958edb563` |
| next | `c18503262212bbcdfba02846da2cf1caf2b4409f44b4f7c077718a9c502853cf` |

They replaced upstream ZapFast's (`c2256da7…bdbe35` and `c3b4b0b9…58e888`),
which A3 does not hold, so releases signed by upstream no longer verify and
A3's are the only ones that do. Missing, truncated, or invalid signatures fail
closed. There is no unsigned fallback, runtime key override, or key fetched
from the release being verified.

The private halves are in the release Mac's `a3-release` keychain, as the
generic passwords `zap-update-ed25519` and `zap-update-ed25519-next` (base64
Ed25519 PKCS#8 DER), never in the checkout; never enable shell tracing while
one is in use, or print it.

## The update feed

Zap's updater (`gnui/ui/settings/updater.go`) reads a feed folder or
address that serves `latest.json` and the archive it names. Release builds
read `https://github.com/AdamAugustinsky/zap/releases/latest/download`, the
latest GitHub release's assets (`gnui/ui/settings/feed_release.go`);
development builds read none. `ZAPFAST_UPDATE_FEED` names another feed (set
but empty: none), and Settings → Updates then says it is a test feed. When a
newer version is offered, the titlebar shows an update button, which downloads
it and then restarts into it.

```json
{"version": "0.20.0", "url": "<release page>", "notes": "…",
 "file": "ZapFast-0.20.0.zip", "sha256": "<hex>", "signature": "<hex>"}
```

`signature` is a raw Ed25519 signature, in hex, over the exact bytes
`<sha256>  <file>\n` (two spaces), made with the publisher key.
The app checks the archive's SHA-256 and that signature against the key
embedded in `gnui/ui/settings/update-public-key.hex` or the next release key in
`update-public-key-next.hex` (upstream's `assets/update-public-key-next.hex`, byte
for byte; a signature by either is accepted). The manifest's signature is checked
before a byte of the archive is fetched, the archive is capped at 200 MB, and
the digest and signature are checked again just before it hands the archive to the framework's `app/selfupdate`,
which then checks that the unpacked app is signed by the running app's own
signer (same designated requirement and bundle identifier) and is newer.

`gnui package -zip` (run by `packaging/macos/gnui-package.sh`) writes
`dist/ZapFast-<version>.zip`, an app made with `ditto -c -k --keepParent`, which
is what `selfupdate.Prepare` takes. To make the feed entry:

```sh
packaging/macos/gnui-sign-update.sh dist/ZapFast-0.21.0.zip "$KEYFILE" 0.21.0 \
    https://github.com/AdamAugustinsky/zap/releases/tag/v0.21.0 "Release notes"
```

It writes `dist/latest.json` beside the zip, and refuses a key that matches
neither embedded public key (the current or the next). The key file is a
PKCS#8 PEM (never in the checkout); the script needs an OpenSSL that signs
Ed25519 (3.x, Homebrew's; not the system LibreSSL). To try a feed, serve both
files and set `ZAPFAST_UPDATE_FEED` to it.

## Releasing

`packaging/macos/gnui-release.sh` does the whole release on the Mac that holds
the `a3-release` keychain: it builds with `gnui-package.sh --dmg --notarize`
(A3's Developer ID, the keychain's `a3-notary` profile), refuses a dirty
checkout or another team's signature, writes the update key to a 0600 file
under `.cache/release` only while `gnui-sign-update.sh` runs (removed on exit,
error included), and leaves the assets in `.cache/release/<version>`. It then
prints the `gh release create` command and stops. `--publish` runs it, which
makes the release the latest one, the one every installed release build
updates to: publish only with Adam's go-ahead. Bump `gnui/gnui.json` and
`state.Version` first; the script refuses a tag that exists.

**0.20.0 and earlier have no feed**, so they never offer an update: moving a
copy onto A3's feed takes one manual install of the first A3-signed release
(its DMG), after which updates come through the app.

## Key custody and rotation

Keep a protected backup of the private key outside the checkout, and back it up
in a password manager or encrypted storage. GitHub does not permit retrieving
an uploaded secret. Never commit a private key, including a retired one.

The current format trusts one key. Before planned rotation, implement and test
a backward-compatible multi-key/signature transition. Ship an old-key-authorized
bridge version that also trusts the new key, and retain old-key signatures while
older clients migrate. Do not simply replace the secret: already-installed
clients only trust the key embedded in their binary.

If the key is lost or compromised, stop publishing automatic updates, revoke
the environment secret, investigate the affected builds, and publish an
independently verified manual installer with a new key. Old clients cannot
safely discover a replacement trust root from unsigned release metadata.

The initial signature-enforcing version must itself be obtained through a
trusted installation. This is separate from Apple notarization.
