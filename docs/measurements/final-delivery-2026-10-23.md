# Final hand-test packages and budget status (2026-10-23)

Packages built and verified; startup, idle, memory, 1M-row, two-window and
Pilot budgets pass on the MacBook. ZapFast's full allocation gate did not run
to a report (script step 11), so its allocations are unmeasured. These are
development-signed test artifacts, not a notarized release.

## Source and artifacts

- ZapFast `b87890256523de8be00a66f853262d52876b97de`, framework
  `693d3b38a740f2b726ff25058558dce939dd4212`; Go 1.27.1, darwin/arm64,
  CGO_ENABLED=0, trimpath, stripped.
- Existing core, not rebuilt (`--skip-core`), SHA-256
  `e330c04c0b97d99434453837eddd64f714325e14fdae911f7e89ee90e51f64e3`.
- Outputs on the Mac mini under
  `/Volumes/SSD/dev-toolchains/build/zf-deliverables-b8789025-fw693d3b38/`:
  `dev/` (diagnostics build) and `release/` (`gnui_release`, for
  [HANDTEST §19](../HANDTEST.md#19-release-build-real)) each with
  `ZapFast.app`, `ZapFast-0.19.0.dmg` and `.zip`; `bin/framework-spike`,
  `bin/framework-vlistbench`, `provenance.json`, `SHA256SUMS`, `evidence/`.

| DMG | Bytes | SHA-256 |
|---|---:|---|
| `dev/ZapFast-0.19.0.dmg` | 22,916,153 | `d2e1adef75ffe051452c23a14f77faf4270be8771121208f3172d9970ef3f2b5` |
| `release/ZapFast-0.19.0.dmg` | 22,469,365 | `a502e62217d086eb3573c1eed2109845f0fa4f5a4c76e709a3ed71e4896c375a` |

| Executable | Bytes | Budget |
|---|---:|---|
| ZapFast release | 12,180,960 | under the ~12.5 MB ceiling |
| ZapFast dev | 13,304,544 | includes diagnostics; not the size measurement |
| `cmd/spike` | 3,530,962 | under 10 MB |
| `cmd/vlistbench` | 6,000,226 | under 10 MB |

Both bundles pass `gnui-verify.sh` (arm64, metadata, bundled core,
entitlements, hardened runtime, strict signatures); a changed Info.plist fails
it. Both DMGs pass `hdiutil verify`, both ZIPs `unzip -t`. `spctl` rejects
both (`origin=gnui-dev`): no Developer ID, notarization or staple. Release
rejects the nine diagnostic flags with exit 2; dev accepts them. The dev
bundle loads its signed embedded core with `ZAPFAST_CORE` unset. Signing
changes only two bytes of the core's `__LINKEDIT` virtual size
(`core-code-verification.json`). Go recorded `vcs.modified=true` because of
an untracked output symlink; tracked source matched the commits above.

## MacBook access recovered, measurements collected

Final measurements use this approved route (no permanent SSH change) and
`~/gnui-test/final-2026-10-23/`; nothing is installed in Applications:

```sh
ssh <the MacBook> ...   # the route itself is private
```

Each run holds `~/gnui-test/final-acceptance.lock` under `caffeinate -d`, on
AC and unlocked, and launches the measured executable as a fresh child.
Raw evidence is under `macbook-final/evidence/` in the delivery root.

Machine: Mac15,6, 11 CPUs, 18 GiB, macOS 27.2 (26B5091g), built-in display
3024×1964 px at 120 Hz. Measured under the actual background load (load
average 2.4–4.1, CPU idle 66–92 %); HID input was not idle throughout.

| Measurement | Result |
|---|---|
| Warm startup ≤150 ms, 10 runs | **PASS 10/10**: median 141.171 ms, 135.304–148.020 ms; six rows, no skeleton, in every first frame |
| App idle, bridge off | **PASS**: 0 builds, 0 allocations in 10 s; 0.100 package-idle wakeups/s, 0.800 interrupt/s; 0.0175 % CPU; footprint 66.267 MiB |
| App idle, bridge on | **PASS**: 0 builds, 0 allocations; 0.200 package-idle/s, 1.300 interrupt/s; 0.00623 % CPU; footprint 63.892 MiB |
| Empty framework ≤40 MB | **PASS**: 32.032 MiB after 60 s idle, 0 idle presents; 600 paced frames, 0 allocations |
| 1M rows, <4 ms/frame | **PASS**: whole-frame P99 2.551 ms, max 2.751 ms, mean 0.909 ms; list work P99 1.085 ms, max 1.170 ms; edits P99 1.063 ms; 0 mallocs in 3×600 frames |
| Two windows | **PASS**: 0 idle presents; two-window CPU 0.066 %, 0 package-idle/s, 14.20 interrupt/s; open 60.8 ms; +6.78 MiB after 2 s, +4.48 MiB idle |
| Pilot allocation gate | **PASS**: 529 frames, 0 mallocs/bytes; main thread 1.359 ms/frame, P99 3.532 ms |
| ZapFast allocation gate | **Not measured**: exit 1, script step 11 found no Button "Newest message"; no report |

Commands: app idle `-demo -idle 10s -settle 50s` (footprint is
`proc_pid_rusage` physical footprint after the settle and window; it does not
cover HT-34's post-scroll memory); spike `-bench -idle 60s -gate -quiet
-timeout 180s`; vlist `-bench -gate -items 1000000 -allocsites -timeout
240s`; Pilot `-bench -gate -allocsites -timeout 180s` on 100,000 synthetic rows
with synthetic clipboard text; ZapFast `-demo -bench -gate -allocsites
-timeout 240s`. Startup is one warm-up (431.998 ms, excluded) then ten
`-demo -exit-first` launches; the interval is kernel process start to first
present on glass.

An earlier startup set (median ~190 ms) is invalid: its launcher exec'd the
measured binary in the guard's process, so the kernel start time included the
guard's own work. Its raw output is kept in the evidence.

The ZapFast gate's failure at step 11 (return to newest after the wheel) is
open: the semantic tree at the failure was not captured. Diagnose it with a
bounded reproduction; the acceptance script is unchanged.

Historical framework incidents, rechecked once each: fullscreen readiness
(`TestGreenButtonWaitsForNativeReadiness`) passes; two-window idle wakeups,
the Pilot allocation and vlist's zero-history session pass now, their old
failures unexplained.

Pending: human checks (native Full Screen, release launch against a test
account, HT-34 memory), Apple signing and notarization, final identity, feed
and keys. See [HANDTEST-BUILD](../HANDTEST-BUILD.md) and
[PARITY](../PARITY.md#not-done).
