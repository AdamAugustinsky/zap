# Zap agent guide

Zap is a small native WhatsApp client for macOS, written in Go: the app on
[go-native-ui](https://github.com/AdamAugustinsky/go-native-ui) and its
backend, gocore, on [whatsmeow](https://github.com/tulir/whatsmeow). It began
as a fork of [ZapFast](https://github.com/crmne/zapfast) (Rust and egui);
none of that code is in this repository any more. References written
`zf:path` point into upstream ZapFast's repository. These notes are for
coding agents and new contributors.

## Product boundaries

- Keep it a small native client. No browser engine, no telemetry, no
  hosted backend, no Zap-operated account system. Features never send
  message content to a third party.
- The protocol comes from whatsmeow (patched only in
  `gnui/third_party/whatsmeow`, every change marked `ZAP PATCH`). Do not
  reimplement pieces of it here, and do not advertise a capability merely
  because a protobuf field for it exists.
- Screenshots, captures, comparison pages and recordings made for a review
  go in the pull request, never the repository.
- Do not broaden a task into adjacent features or a general refactor.
  Preserve existing user behaviour unless the task changes it.

## Privacy

- The user's archive is personal data. Do not read chat rows, message
  bodies, contacts, or other user content out of an account's databases or
  any exported log, not even read-only. Schema, column existence, and row
  counts are fine; message contents are not.
- Tests never use a real WhatsApp account or connect to WhatsApp: they run
  on the demo (`gnui/gocore/demo`), `core/fake`, injected whatsmeow events
  and synthetic fixtures.
- When a bug report or feature needs the user's data, hand the user the
  query or command to run and let them report the result back.
- Never log message contents, phone numbers, keys, or QR payloads at a
  level that ships. The log file is meant to be attached to bug reports,
  and everything logged goes through `sys.RedactHandler`.

## Layout

`gnui/README.md` maps the module. The rules of the backend (no cgo, native
libraries loaded with purego, the bundled ones built by pinned scripts under
`packaging/macos/`) are in `docs/go-backend/PLAN.md`.

## Definition of done

- Add focused tests for changed behaviour. The demo carries sample data;
  extend it when a new kind of content or state is added, and use
  `-demo-shot` to look at the result.
- Run, from `gnui/`, with `CGO_ENABLED=0`:

  ```sh
  go vet ./...
  go vet -tags gnui_release ./...
  go test ./...
  ```

  Do not weaken a check or delete a test merely to make it pass without
  explaining why the rule does not apply.
- Before handing over a build, render every screen the change touches
  (`-demo-page <part>,light|dark -demo-shot`) and check it at pixel level:
  glyphs centred in their buttons, nothing past its container or under a
  scroll bar, and siblings in the same Rex style; a large change also gets
  an independent QA pass. Place a box laid over another with
  `c.NextCenter()` or `c.NextAnchor(...)`, never with positions computed from
  sizes (`theme.At(c, (d-w)/2, ...)`).
- Report platform coverage honestly: say what was run and what was only
  compiled.

## Licence

Zap is GPL-3.0 (`COPYING`, `LICENSE`), because whatsmeow links
go.mau.fi/libsignal (GPL-3.0). Code taken from an MIT, MPL or BSD project
keeps its notice: say where it came from, and at which commit, in the file's
header, and list the project in `THIRD-PARTY-NOTICES.md`.
