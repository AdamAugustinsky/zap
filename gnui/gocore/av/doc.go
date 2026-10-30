// Package av is the Go backend's audio and pictures: what zfcore and
// upstream's Rust do outside the WhatsApp protocol for voice notes, stickers
// and picture edits.
//
// Voice notes are Ogg/Opus, mono at 48 kHz, as upstream sends them. Their
// level, length and 64-bar waveform are computed as upstream's voice.rs
// does ([Normalize], [Waveform], [PrepareVoice]); [EncodeOpus] and
// [DecodeOpus] are its encode and decode, and [DecodeVoiceFile] is zfcore's
// DecodeVoice. The Opus codec is AudioToolbox's (AudioConverter with
// kAudioFormatOpus), reached at run time with purego; the Ogg layer is Go.
//
// Pictures are decoded with the standard library and golang.org/x/image
// (PNG, JPEG, GIF, WebP, BMP, TIFF) and, for anything else, the system's
// decoder through gonativeui/img. They are measured and edited upright, by
// their EXIF orientation ([InspectPicture], [EditPicture]). Stickers
// ([MakeSticker]) are encoded by libwebp, from libzfwebp.dylib
// (packaging/macos/libwebp/build.sh), loaded with gonativeui/clib.
//
// A video being sent is measured from its MP4 header ([InspectVideo]: size,
// length and when its poster shows), as upstream's mp4_frames poster; its
// poster frame is decoded by AVFoundation ([VideoPoster]) where upstream
// decoded H.264 with openh264.
//
// Nothing here needs cgo. A codec or library that is missing gives an error
// matching [ErrUnavailable] (and errors.ErrUnsupported), never a crash.
package av
