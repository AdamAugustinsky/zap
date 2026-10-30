# Zap for Windows: plan

Status: plan, 2026-10-28. Nothing here is built yet. It starts after the
Go backend passes its live test on the Mac and lands on `main`
([../go-backend/PLAN.md](../go-backend/PLAN.md)).

## The outcome

A signed `Zap-Setup` for Windows 10 and 11, on x64 and Arm64, offered on
zap.a3lab.ai next to the Mac download. It installs per user without admin
rights and updates itself. It does what the Mac app does: chats, media,
voice notes, notifications, and one-to-one and group calls with camera.

## Where things stand

Zap is Go on go-native-ui with no cgo, so `GOOS=windows` already builds:
a 43 MB `zapfast.exe` today. The WhatsApp side (whatsmeow, the archive,
sync, the call signalling and the MLow codec) is plain Go and needs nothing
per OS. What doesn't work on Windows is everything that reaches the OS for
media, plus the two bundled C libraries.

**The framework already has on `main`, for Windows:** windows, input, IME,
the clipboard, DPI, the C-library loader (`clib`, on `LoadLibrary`), the
secret store (Credential Manager, so the archive key works as is), the
single-instance lock, preferences, locale and the shell.

**The framework's Phase 8 is building, on branches that are close to
landing:**
- toasts with an AUMID (W6, D71);
- the tray (W5);
- file dialogs and drag and drop (W8);
- the drawn Rex titlebar (W9);
- `app/install` and URL schemes (W10);
- self-update on Windows (S4: helper copy, swap, health marker, rollback);
- packaging from any host (T1, D77): `gnui package -target windows/amd64,windows/arm64`
  with the icon, manifest and version resource written in Go, a signing hook
  (`-win-sign`) and a zip.

**Zap's macOS-only code** (each has an `_other.go` stub today):

| Package | macOS uses | Feeds |
|---|---|---|
| `gocore/av` (`opus_darwin.go`, `opus_stream_darwin.go`) | AudioToolbox Opus | voice notes in and out, Opus in calls |
| `gocore/voip/audio` (`coreaudio_darwin.go`) | the voice-processing I/O unit, with echo cancellation | call audio |
| `gocore/voip/camera` (`avf_darwin.go`) | AVFoundation capture | call video |
| `gocore/voip/video` (`vt_darwin.go`, `codec_darwin.go`) | VideoToolbox H.264 | call video |
| `cmd/zapfast/machine_darwin.go`, `internal/procinfo` | the device name; memory figures | the linked-device name, diagnostics |

**The framework's media package is macOS-only and is not in Phase 8:**
`media.Player`, `media.Recorder`, video playback (`media.Video`) and posters
are `ErrUnsupported` off macOS. Zap uses them for:
- voice note recording (`ui/composer`) and playback (`ui/conversation/av`);
- video messages, GIFs and motion photos in the transcript and the viewer;
- the call tones.

Phase 8's capture work (M2: WASAPI, Media Foundation's sink writer, D3D11)
is the closest neighbour, and its COM and WinRT helpers (`internal/com`,
`internal/win32`) are what this would be built on.

## The work

Effort is in agent sessions of the kind that ported the call engine. Items
marked F are framework work: they live in go-native-ui, help a3-kb and a3-app
too, and land through its CI. Items marked Z are in this repository.

1. **F: media on Windows.** Media Foundation behind the existing
   `media` interfaces:
   - `Player`: `IMFMediaEngine`, audio and video;
   - video frames into the D3D11 swap chain through the media engine's
     frame server;
   - `Recorder`: WASAPI shared-mode capture to WAV, as on macOS (D57);
   - posters: `IMFSourceReader`, first frame.

   This is the largest item, at two to three sessions. Tests run on the
   Windows CI runners with generated media files (the runners have no
   speakers or camera, so devices are faked or skipped and checked on the VM).

2. **Z: the bundled libraries for Windows.** All built from pinned sources
   by scripts like `packaging/macos/sqlcipher/build.sh`, cross-compiled on
   the Mac mini with `zig cc` (already in `~/.local/share/zig`) for
   `x86_64-windows` and `aarch64-windows`:
   - **SQLCipher** with LibTomCrypt compiled in (public domain), since
     CommonCrypto is Apple's. The archive format is unchanged.
   - **libwebp**, as on the Mac.
   - **libopus** (BSD-3), new for Windows: Windows has no system Opus codec
     a desktop app can rely on.

   `store/sqlite` and `av` get a library name per OS. About one session,
   including a test that the Windows SQLCipher opens a database the Mac
   build wrote.

3. **Z: Opus through libopus** (`opus_windows.go`, `opus_stream_windows.go`).
   This is the same contract as the AudioToolbox code, with the existing
   golden vectors in `gocore/av/testdata` as the test. Half a session.

4. **Z: call audio on WASAPI** (`audio_windows.go`). Full duplex at 16 or
   48 kHz with echo cancellation from the Voice Capture DSP
   (`CLSID_CWMAudioAEC`, in every Windows since Vista), in its source mode:
   it owns the microphone and speaker endpoints and returns
   echo-cancelled capture. Device switching follows the macOS behaviour that
   `TestDeviceSwitchKeepsTheChannels` checks. No Go runs on an audio engine's
   real-time thread (D57's rule): a locked goroutine pulls with a fixed
   buffer. One to two sessions; echo cancellation is checked by hand on the
   VM.

5. **Z: camera and H.264 on Media Foundation** (`camera_windows.go`,
   `video_windows.go`):
   - capture through `IMFSourceReader` on the default video device, NV12;
   - the system's H.264 encoder and decoder MFTs (hardware where present).

   The macOS tests' PSNR check carries over. N editions of Windows ship
   without Media Foundation's codecs: Zap says so and offers voice-only calls
   instead of failing. One to two sessions.

6. **Z: the app on Windows.**
   - the device name (`machine_windows.go`) and memory figures;
   - toasts through `sys/notifications.go`, including reply and mark-read
     actions where W6 supports them;
   - the unread count as a taskbar overlay badge (`ITaskbarList3`), in place
     of the Dock badge (a small framework item if Phase 8 lacks it);
   - start at login, the tray and file dialogs on Phase 8's pieces;
   - the window chrome on W9;
   - a `whatsapp:` URL scheme only if you want it.

   Plus a pass over the UI for macOS assumptions (⌘ in labels, the menu bar,
   the Transparency setting) and the layout goldens on Windows. One session.

7. **Z: packaging and updates.**
   - `packaging/windows/zap-package.ps1`, or its shell equivalent run on the
     mini, over `gnui package -target`;
   - an installer, per-user, under `%LOCALAPPDATA%\Programs\Zap`, with a
     Start-menu shortcut that carries the AUMID, from `app/install`;
   - the update feed: an Ed25519-signed `update.json` per platform, served
     from zap.a3lab.ai.

   This also gives the Mac app the update feed it doesn't have yet. One
   session.

8. **Z: the website.** A Windows download next to the Mac one. The page
   picks the right one from the visitor's system and lists both; release
   assets are `Zap-Setup-x64.exe` and `Zap-Setup-arm64.exe`. Small.

Items 1 and 2 can start in parallel. 3 needs 2. 4 and 5 are independent of
each other and of 1. 6 needs Phase 8 landed. 7 needs T1 and S4. With
parallel agents, that is roughly one to two days of agent time plus CI
rounds; the slow part is likely to be the VM checks, not the code.

## Signing

Unsigned or self-signed installers are blocked by SmartScreen, so Windows
isn't released without a certificate. Two routes:

- **Azure Artifact Signing** (formerly Trusted Signing; about $10 a month).
  This is the framework plan's choice and works unattended from CI or the
  mini.
  - Public certificates go only to organisations registered in the United
    States, Canada, the EU, the UK, Australia, New Zealand, Japan, South
    Korea, Singapore, Switzerland, Norway or Israel. Individuals must be in
    the US or Canada.
  - The old three-years-in-business rule no longer applies.
  - So A3 Direct Solutions LLP qualifies only if it is registered in one of
    those places.
- **An OV certificate from a CA with cloud signing** (SSL.com eSigner,
  Certum SimplySign and similar; about $150 to $300 a year plus the signing
  service). This is available worldwide and can also sign unattended. Since
  2024 an EV certificate buys nothing more against SmartScreen.

Either way, the first releases show a SmartScreen warning until the publisher
builds reputation; consistent signing of each release builds it.

The Microsoft Store would avoid the warning and re-sign for free, but an
unofficial WhatsApp client is unlikely to pass Store review, so it isn't
planned.

## Verifying

- **CI:** go-native-ui's Windows runners (`windows-2025` amd64,
  `windows-11-arm`). Zap has no workflows yet, so this adds one for
  `GOOS=windows` vet, tests and packaging, on push to `main` and on demand.
- **A Windows 11 VM with a desktop,** for what CI can't see: toasts, the
  tray, sound, the microphone and camera, echo cancellation, and a real
  WhatsApp link. Phase 8 asked for one too (D78), so one VM serves both.
  Options:
  - Windows 11 on Arm in Parallels or UTM on the MacBook, where Parallels
    passes the camera and microphone through;
  - an x64 VM on a3-box under KVM.
- **The hand test:** link a phone, history, messages and media both ways,
  voice notes, one-to-one and group calls with video, notifications,
  update from one build to the next.

## What needs you

1. **The signing route.** Is A3 Direct Solutions LLP registered in a
   country Artifact Signing accepts? If so, enrol it (identity validation
   takes a few days). If not, choose a CA for an OV certificate.
2. **A Windows machine to test on:** the VM option you prefer, or a real PC.
3. **Whether Windows 10 matters,** or Windows 11 only. Windows 10 support
   ended in October 2025. Dropping it removes one VM image from the checks.
