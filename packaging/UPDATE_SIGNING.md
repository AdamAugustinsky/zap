# Update signatures

ZapFast verifies `checksums.txt.sig`, a raw 64-byte Ed25519 signature over the
exact bytes of `checksums.txt`, before parsing checksums or downloading a package.
The trusted 32-byte public key is embedded from `assets/update-public-key.hex`
as the `publisher_key` of ZapFast's configuration for
[fastframe-update](https://github.com/crmne/fastframe/tree/main/crates/fastframe-update),
which performs the verification.
Missing, truncated, or invalid signatures fail closed. There is no unsigned
fallback, runtime key override, or key fetched from the release being verified.
Exact filenames bind each signed checksum to its version and platform.

The `release-signing` GitHub environment holds `ZAPFAST_UPDATE_SIGNING_KEY`
(an Ed25519 PKCS#8 PEM private key). It is restricted to `v*` tags and requires
maintainer approval. Only the final signing/publishing job uses it, never PR
builds or compilation. Approve the candidate's exact commit and build results
before allowing that job to run. Do not bypass the approval gate.

The pinned `native-packages` `sign-release` action generates the manifest,
checks that the secret matches the embedded public key, checks the artifact
hashes, signs the checksums, and verifies its output. The key is read in-process;
no temporary private-key file is created. Never enable shell tracing in this
step or print the signing secret. Signing and attestation machinery are shared;
ZapFast still owns its key, approval environment and updater verification.
See the [shared signing guide](https://github.com/crmne/native-packages/blob/dc0cb1586894ca0239c53fd02d4da8064712671c/docs/_guides/release-signing.md).

Build jobs also publish GitHub artifact attestations. These identify the
repository, workflow and commit that built each file; they do not replace the
updater's publisher-signature check or prove the source is safe. Consumers can
run `gh attestation verify FILE -R crmne/zapfast`. See
[GitHub's attestation documentation](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations).

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
trusted installation. Older clients that only checked hashes do not gain
signature protection until upgraded. This is separate from Apple notarization
and Windows Authenticode. CI-held signing also remains dependent on GitHub and
the protected workflow, unlike a genuinely offline signing authority.

## The Go app's update feed (gnui)

The Go app's updater (`gnui/ui/settings/updater.go`) reads a feed folder or
address that serves `latest.json` and the archive it names:

```json
{"version": "0.20.0", "url": "<release page>", "notes": "…",
 "file": "ZapFast-0.20.0.zip", "sha256": "<hex>", "signature": "<hex>"}
```

`signature` is a raw Ed25519 signature, in hex, over the exact bytes
`<sha256>  <file>\n` (two spaces), made with the same publisher key as above.
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
packaging/macos/gnui-sign-update.sh dist/ZapFast-0.20.0.zip "$KEYFILE" 0.20.0 \
    https://github.com/crmne/zapfast/releases/tag/v0.20.0 "Release notes"
```

It writes `dist/latest.json` beside the zip, and refuses a key that matches
neither embedded public key (the current or the next). The key file is the PKCS#8 PEM held by the
`release-signing` environment (never in the checkout); the script needs an
OpenSSL that signs Ed25519 (3.x, Homebrew's; not the system LibreSSL). Serve
both files from the feed address, and set `ZAPFAST_UPDATE_FEED` to it to try it.
The release feed's address and the move to upstream's `checksums.txt` format are
still undecided; until then the Updates section says the feed is a test feed.
