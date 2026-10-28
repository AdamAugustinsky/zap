package gocore

import (
	"math"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// demoOnly are the commands only the demo core answers; any other engine
// refuses them as zfcore's real core does.
var demoOnly = map[abi.CommandTag]bool{
	abi.CommandTagDemoIncoming: true,
	abi.CommandTagDemoTyping:   true,
	abi.CommandTagDemoPanic:    true,
}

// voiceMaxBytes is the longest SendVoice: 15 minutes of f32 at 48 kHz.
const voiceMaxBytes = uint64(core.VoiceLongestSend/time.Second) * core.VoiceRate * 4

// check refuses a command whose fields are inconsistent, as zfcore's
// dto::command does before it takes any blob, with its words.
func check(cmd *abi.Command) error {
	switch cmd.Tag {
	case abi.CommandTagSendLocation:
		c := &cmd.SendLocation
		if !finite(c.Latitude) || !finite(c.Longitude) {
			return Refuse("latitude and longitude must be finite")
		}
	case abi.CommandTagSendImage:
		c := &cmd.SendImage
		if !isRGBA(c.Width, c.Height, c.RGBA.Len) {
			return Refuse("rgba is not width × height × 4 bytes")
		}
	case abi.CommandTagSendVoice:
		n := cmd.SendVoice.Samples.Len
		if n%4 != 0 {
			return Refuse("samples are whole little-endian f32s")
		}
		if n > voiceMaxBytes {
			return Refuse("a voice note is at most 15 minutes")
		}
	case abi.CommandTagApplyPictureEdit:
		c := &cmd.ApplyPictureEdit
		if c.Source.Tag == abi.PictureSourceTagPasted && !isRGBA(c.Width, c.Height, c.Source.Pasted.RGBA.Len) {
			return Refuse("rgba is not width × height × 4 bytes")
		}
	}
	return nil
}

func finite(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }

// isRGBA reports whether n bytes are exactly a w × h RGBA picture; the
// product of two u32s and 4 fits a u64 without wrapping.
func isRGBA(w, h uint32, n uint64) bool {
	return w != 0 && h != 0 && uint64(w)*uint64(h)*4 == n
}

// blobOf is the blob cmd names, which a successful Send takes; no command
// names more than one.
func blobOf(cmd *abi.Command) (abi.BlobRef, bool) {
	switch cmd.Tag {
	case abi.CommandTagSendImage:
		return cmd.SendImage.RGBA, true
	case abi.CommandTagSendVoice:
		return cmd.SendVoice.Samples, true
	case abi.CommandTagSetGroupPicture:
		return cmd.SetGroupPicture.Jpeg, cmd.SetGroupPicture.HasJpeg
	case abi.CommandTagApplyPictureEdit:
		s := &cmd.ApplyPictureEdit.Source
		return s.Pasted.RGBA, s.Tag == abi.PictureSourceTagPasted
	}
	return abi.BlobRef{}, false
}
