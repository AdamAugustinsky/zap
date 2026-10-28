# Zap (Go UI) against upstream ZapFast, macOS

Zap, the Go UI on go-native-ui, against upstream ZapFast (egui and
fastframe), both version 0.19.0 over the same Rust core, measured on
2026-10-27.

## Results

Medians of six runs per app, interleaved, with the range in brackets.

| | Zap (Go) | ZapFast (egui) |
|---|---:|---:|
| Launch to window on screen, warm | **137 ms** [133–141] | 274 ms [268–294] |
| Memory at idle, 50–60 s after launch (`phys_footprint`) | **85 MiB** [85–86] | 186 MiB [186–193] |
| Highest memory over that run | 180 MiB | 259 MiB |
| CPU at idle | ≈ 0 ms/s | ≈ 0 ms/s |
| Wakeups at idle | 0.3/s | 0.3/s |
| Apple-silicon code shipped | 45.2 MB: 12.3 MB Go UI + 32.9 MB core | 48.8 MB: the arm64 slice of the official 0.19.0 app |

Zap's window is on screen in half the time. It idles in less than half the
memory, and both apps are equally quiet at idle. They ship about the same
amount of code, because both carry the same Rust core.

**Not yet valid: the 41-second launch tour.** Each app plays upstream's
scripted tour by itself (`--demo-tour --demo-tour-delay 3000`). During the
tour, Zap used 113 ms of CPU per second and ZapFast 794 ms/s. Zap's memory
was 185 MiB and ZapFast's 382 MiB. These numbers stay out of the table above
because the Mac mini's screen was locked for the whole run. Zap's own frame
report (`-bench`) showed every frame unpaced: no display events arrived. An
egui app with no pacing may draw as fast as it can, so the tour has to be
re-run on a screen that is on and unlocked before its numbers mean anything.
The raw rows are in the JSONL file.

## Method

- **Machine.** Mac mini, Apple M4, 16 GB, macOS 27.2, a 5K display. Other
  agents' work kept the load average at 1.2–4.2. The screen was locked.
- **Apps.** Both apps ran as bare executables on the demo's sample chats,
  with no account, network or archive.
  - Zap: `go build -trimpath -ldflags="-s -w" ./cmd/zapfast` at 8178624, a
    development build, because release builds have no `-demo`. It ran with
    `-demo` on the `core-release` `libzapfast_core.dylib`.
  - ZapFast: `cargo build --release --locked --features demo` from this
    repository's upstream sources at the same version, run with `--demo`.
- **Harness.** `scripts/gnui/bench-upstream/bench.swift` measures every app
  from outside, the same way.
  - Startup: the time from `posix_spawn` until the process has a layer-0
    window of at least 300×300 points in the on-screen window list. This is
    not the first painted frame: the runner had no screen-capture permission.
  - Idle: CPU, package-idle and interrupt wakeups, and `phys_footprint`, all
    from `proc_pid_rusage`, over 10 s starting 50 s after the window appears.
    macOS charges a process for drawables it has trimmed until about 30 s
    after launch.
- **Runs.** `scripts/gnui/bench-upstream/run.sh 6` launches each app once
  unmeasured, so the measured runs are warm. It then runs six rounds,
  alternating which app goes first.
- **Size.** For Zap: the release executable (`-tags gnui_release`) plus the
  core. For ZapFast: `lipo -detailed_info` on `/Applications/ZapFast.app`.

Raw rows: [zap-vs-zapfast-2026-10-27.jsonl](zap-vs-zapfast-2026-10-27.jsonl).
