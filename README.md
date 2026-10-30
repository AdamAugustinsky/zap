<p align="center">
  <img src="site/public/assets/icon-256.png" alt="Zap's icon" width="96" height="96">
</p>

<h1 align="center">Zap</h1>

<p align="center"><strong>WhatsApp, native on your Mac.</strong><br>
<a href="https://zap.a3lab.ai"><strong>zap.a3lab.ai</strong></a> ·
<a href="https://github.com/AdamAugustinsky/zap/releases/latest/download/Zap.dmg">Download</a> ·
<a href="https://github.com/AdamAugustinsky/zap/releases">Releases</a></p>

Zap is a WhatsApp app for macOS. Its interface is written in Go on
[go-native-ui](https://github.com/AdamAugustinsky/go-native-ui), the native UI
framework, in its Rex look, and so is everything underneath: the backend
links to your phone as a companion device through
[whatsmeow](https://github.com/tulir/whatsmeow), keeps the archive in
SQLCipher, and runs calls on its own Go call engine. There is no browser
engine and no server in between.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="site/public/assets/chat-dark.jpg">
  <img src="site/public/assets/chat-light.jpg" alt="Zap showing the chat list and a conversation">
</picture>

Measured against upstream ZapFast's egui app on the same core, on an Apple M4
([method and raw data](docs/measurements/zap-vs-zapfast-2026-10-27.md)).
These runs were of the earlier build whose backend was ZapFast's Rust core;
the Go backend has not been measured this way yet:

| | Zap | ZapFast (egui) |
|---|---:|---:|
| Launch to window on screen | **137 ms** | 274 ms |
| Memory at idle | **85 MiB** | 186 MiB |
| CPU at idle | ≈ 0 | ≈ 0 |

## Install

Download [Zap.dmg](https://github.com/AdamAugustinsky/zap/releases/latest/download/Zap.dmg),
open it and drag Zap to Applications. Zap is signed with A3 Direct Solutions'
Developer ID and notarized by Apple. It needs macOS 14 or later and Apple
silicon.

Open Zap. It shows a QR code. On your phone, open WhatsApp, then Settings,
Linked devices, Link a device, and scan it.

## What is in this repository

Zap began as a fork of [ZapFast](https://github.com/crmne/zapfast) by Carmine
Paolino, an egui app over a Rust core. Both are gone from this repository:
the app and its backend are Go.

| Path | What |
|---|---|
| `gnui/` | Zap: the Go app on go-native-ui and its backend, `gocore` ([gnui/README.md](gnui/README.md)) |
| `packaging/macos/` | the macOS bundle: `gnui-package.sh`, the checks, the two C libraries' build scripts, the icon |
| `docs/` | the backend plan (`docs/go-backend/`), parity tables, hand tests and measurements (`docs/GNUI-MIGRATION-PLAN.md`, `docs/PARITY.md`) |
| `site/` | zap.a3lab.ai: the static site (`site/public/`), a Cloudflare Worker |
| `scripts/render-demo.py` | captions a recorded demo tour (`-demo-tour-events`) |

## Building

You need Go 1.27 and the Xcode Command Line Tools, which build the two C
libraries the app bundles: SQLCipher (the archive and the session store)
and libwebp (stickers). Nothing else is native; the Go code builds with
`CGO_ENABLED=0` and loads those libraries at run time. Check out
go-native-ui beside this repository (`gnui/go.mod` resolves it at
`../go-native-ui`):

```sh
git clone https://github.com/AdamAugustinsky/go-native-ui
git clone https://github.com/AdamAugustinsky/zap
cd zap
packaging/macos/sqlcipher/build.sh                    # target/sqlcipher/libsqlcipher.dylib
packaging/macos/libwebp/build.sh                      # target/libwebp/libzfwebp.dylib
cd gnui && CGO_ENABLED=0 go run ./cmd/zapfast -demo   # the app on the demo's sample chats
CGO_ENABLED=0 go test ./...                           # the tests, on the demo and fakes
```

`packaging/macos/gnui-package.sh` builds the signed `Zap.app`, the disk image
and the update archive ([docs/HANDTEST-BUILD.md](docs/HANDTEST-BUILD.md)).

## Licence and notices

Zap is distributed under the GNU General Public License, version 3
([COPYING](COPYING), [LICENSE](LICENSE)): it links go.mau.fi/libsignal,
which is GPL-3.0. It contains code from ZapFast (MIT, © Carmine Paolino) and
other MIT, MPL-2.0 and BSD components, whose notices are in
[THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

Zap is an unofficial client and is not affiliated with WhatsApp or Meta.
Using an unofficial client may be against WhatsApp's terms of service and
could get an account suspended. Use it at your own risk. WhatsApp is a
trademark of WhatsApp LLC.
