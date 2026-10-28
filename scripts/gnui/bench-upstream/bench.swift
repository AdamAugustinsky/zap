// bench: launch a GUI executable and measure it from outside, the same way
// for any app.
//
//   bench <label> <settle-s> <idle-s> <active-s> -- <exe> [args...]
//
// Prints one JSON line:
//   window_ms   spawn -> an on-screen layer-0 window of the pid, >= 300x300
//   painted_ms  spawn -> that window's image has non-uniform content
//               (-1 if screen capture is not permitted)
//   idle_*      over idle-s seconds after settle-s: CPU ms, package-idle
//               and interrupt wakeups per second, phys_footprint MiB
//   active_*    over the next active-s seconds, without input (a tour the
//               app plays by itself): CPU ms per second, footprint
//   peak_footprint_mib  the process's lifetime maximum
import AppKit
import CoreGraphics
import Darwin
import Foundation

func now() -> Double {
    var t = timespec()
    clock_gettime(CLOCK_MONOTONIC_RAW, &t)
    return Double(t.tv_sec) * 1000 + Double(t.tv_nsec) / 1e6
}

struct Usage { var cpuMs: Double; var idleW: UInt64; var intW: UInt64; var footprint: UInt64 }

func usage(_ pid: pid_t) -> Usage? {
    var ri = rusage_info_v4()
    let r = withUnsafeMutablePointer(to: &ri) { p in
        p.withMemoryRebound(to: rusage_info_t?.self, capacity: 1) {
            proc_pid_rusage(pid, RUSAGE_INFO_V4, $0)
        }
    }
    if r != 0 { return nil }
    var tb = mach_timebase_info_data_t()
    mach_timebase_info(&tb)
    let ticks = Double(ri.ri_user_time + ri.ri_system_time)
    let ns = ticks * Double(tb.numer) / Double(tb.denom)
    return Usage(cpuMs: ns / 1e6, idleW: ri.ri_pkg_idle_wkups, intW: ri.ri_interrupt_wkups, footprint: ri.ri_phys_footprint)
}

func window(of pid: pid_t) -> (CGWindowID, CGRect)? {
    guard let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly], kCGNullWindowID) as? [[String: Any]] else { return nil }
    for w in list {
        guard (w[kCGWindowOwnerPID as String] as? Int32) == pid,
              (w[kCGWindowLayer as String] as? Int) == 0,
              let b = w[kCGWindowBounds as String] as? [String: CGFloat],
              let id = w[kCGWindowNumber as String] as? CGWindowID else { continue }
        let r = CGRect(x: b["X"] ?? 0, y: b["Y"] ?? 0, width: b["Width"] ?? 0, height: b["Height"] ?? 0)
        if r.width >= 300 && r.height >= 300 { return (id, r) }
    }
    return nil
}

// CGWindowListCreateImage is obsoleted in the SDK but still exported.
typealias CreateImage = @convention(c) (CGRect, UInt32, CGWindowID, UInt32) -> Unmanaged<CGImage>?
let createImage = unsafeBitCast(dlsym(UnsafeMutableRawPointer(bitPattern: -2), "CGWindowListCreateImage")!, to: CreateImage.self)

// painted reports whether the window's image has more than a handful of
// distinct colours in a coarse grid: a blank or single-colour window does not.
func painted(_ id: CGWindowID) -> Bool? {
    guard let img = createImage(CGRect.null, CGWindowListOption.optionIncludingWindow.rawValue, id,
                                CGWindowImageOption([.boundsIgnoreFraming, .nominalResolution]).rawValue)?.takeRetainedValue() else { return nil }
    let w = 64, h = 64
    var px = [UInt32](repeating: 0, count: w * h)
    guard let ctx = CGContext(data: &px, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                              space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return nil }
    ctx.draw(img, in: CGRect(x: 0, y: 0, width: w, height: h))
    var seen = Set<UInt32>()
    for p in px { seen.insert(p & 0xF0F0F0F0); if seen.count > 12 { return true } }
    return false
}

let a = CommandLine.arguments
guard let sep = a.firstIndex(of: "--"), sep == 5, a.count > 6 else {
    FileHandle.standardError.write("usage: bench <label> <settle-s> <idle-s> <active-s> -- <exe> [args...]\n".data(using: .utf8)!)
    exit(2)
}
let label = a[1], settle = Double(a[2])!, idle = Double(a[3])!, activeS = Double(a[4])!
let argv = Array(a[(sep + 1)...])
let capture = CGPreflightScreenCaptureAccess()

var pid: pid_t = 0
var cargs: [UnsafeMutablePointer<CChar>?] = argv.map { strdup($0) } + [nil]
let t0 = now()
let rc = posix_spawn(&pid, argv[0], nil, nil, &cargs, environ)
if rc != 0 { print("{\"label\":\"\(label)\",\"error\":\"spawn \(rc)\"}"); exit(1) }

var windowMs = -1.0, paintedMs = -1.0
var wid: CGWindowID = 0, frame = CGRect.zero
while now() - t0 < 15000 {
    if wid == 0, let (id, r) = window(of: pid) { wid = id; frame = r; windowMs = now() - t0 }
    if wid != 0 {
        if !capture { break }
        if painted(wid) == true { paintedMs = now() - t0; break }
    }
    usleep(1000)
}

func finish(_ fields: [String: Any]) -> Never {
    kill(pid, SIGTERM)
    var st: Int32 = 0
    for _ in 0..<200 { if waitpid(pid, &st, WNOHANG) == pid { break }; usleep(10000) }
    kill(pid, SIGKILL); waitpid(pid, &st, 0)
    var out = fields; out["label"] = label
    let d = try! JSONSerialization.data(withJSONObject: out, options: [.sortedKeys])
    print(String(data: d, encoding: .utf8)!)
    exit(0)
}

if wid == 0 { finish(["error": "no window in 15 s"]) }
usleep(UInt32(settle * 1e6))
guard let u0 = usage(pid) else { finish(["error": "process gone"]) }
usleep(UInt32(max(idle, 0.001) * 1e6))
guard let u1 = usage(pid) else { finish(["error": "process gone"]) }
var out: [String: Any] = idle <= 0 ? ["window_ms": (windowMs * 10).rounded() / 10] : [
    "window_ms": (windowMs * 10).rounded() / 10, "painted_ms": (paintedMs * 10).rounded() / 10,
    "idle_cpu_ms_per_s": ((u1.cpuMs - u0.cpuMs) / idle * 100).rounded() / 100,
    "idle_pkg_wakeups_per_s": (Double(u1.idleW - u0.idleW) / idle * 100).rounded() / 100,
    "idle_int_wakeups_per_s": (Double(u1.intW - u0.intW) / idle * 100).rounded() / 100,
    "idle_footprint_mib": (Double(u1.footprint) / 1048576 * 10).rounded() / 10,
]
if activeS > 0 {
    // The active window: whatever the app does by itself (a scripted tour
    // started by its own delay flag), measured without input.
    let s0 = usage(pid)!
    usleep(UInt32(activeS * 1e6))
    guard let s1 = usage(pid) else { finish(["error": "process gone in the active window"]) }
    out["active_cpu_ms_per_s"] = ((s1.cpuMs - s0.cpuMs) / activeS * 100).rounded() / 100
    out["active_footprint_mib"] = (Double(s1.footprint) / 1048576 * 10).rounded() / 10
}
var ri = rusage_info_v4()
_ = withUnsafeMutablePointer(to: &ri) { p in p.withMemoryRebound(to: rusage_info_t?.self, capacity: 1) { proc_pid_rusage(pid, RUSAGE_INFO_V4, $0) } }
out["peak_footprint_mib"] = (Double(ri.ri_lifetime_max_phys_footprint) / 1048576 * 10).rounded() / 10
finish(out)
