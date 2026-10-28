// Package camera is a call's camera: what upstream Zap's camera.rs does on
// nokhwa, on AVFoundation.
//
// [Devices] lists the cameras (the UI's CallDevices cameras). [Open]
// starts one in the mode that best fits the call's budget ([ChooseMode],
// camera.rs's choose_mode) and delivers its pictures as I420
// (gocore/voip/video) of [Camera.Size], the camera's own shape within the
// budget, scaled by the capture output and converted from NV12. A reader
// that falls behind gets the newest frames; [Camera.Done] closes when the
// camera is unplugged or taken by another app.
//
// AVFoundation is reached with purego's Objective-C runtime: nothing needs
// cgo. Off macOS, Devices and Open fail with [ErrUnavailable]; a refused
// camera is [ErrPermission], a missing one [ErrNoCamera], and a capture
// call that fails a [*SystemError] carrying its NSError.
package camera
