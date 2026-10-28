// Package video is a call's video on the device: H.264 both ways and the
// frames the UI draws, what upstream Zap's calls.rs VideoPipeline (openh264)
// and zfcore's frames.rs do.
//
// [Encoder] turns I420 pictures into Annex-B access units (an AUD first, a
// keyframe's SPS and PPS before its slices), Constrained Baseline at a
// real-time bitrate that moves without a keyframe ([Encoder.SetBitrate]),
// with a keyframe every two seconds and on request
// ([Encoder.RequestKeyframe]). [Decoder] takes Annex-B access units back to
// I420, waits for a keyframe ([ErrNeedKeyframe]) and follows a peer that
// changes size. Both are VideoToolbox's, reached at run time with purego:
// nothing is bundled and nothing needs cgo. Off macOS they fail with
// [ErrUnavailable]; a framework call that fails is a [*StatusError].
//
// [I420] is the picture both sides and the UI share: the layout of
// gnui/core.Frame (Y, then U, then V, each Stride[i] a row), with the
// conversions a call needs (NV12, scaling, rotation by the peer's
// orientation). [Frames] keeps the newest picture of each stream for the
// UI and implements core.Frames, as zfcore's frames.rs does.
package video
