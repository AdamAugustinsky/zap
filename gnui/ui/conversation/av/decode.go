package av

import (
	"errors"
	"strings"

	"gonativeui/media"

	"zapfast/core"
	"zapfast/i18n"
)

// Clip is a decoded voice note: its PCM, the PCM's format and, when the
// decoder measured it, its 64-bar waveform (0 to 100 each).
type Clip struct {
	PCM     []byte
	Format  media.Format
	Bars    [bars]byte
	HasBars bool
}

// errUndecodable marks a file the core does not decode (it decodes Ogg/Opus
// only; upstream also decodes other audio files with rodio).
var errUndecodable = errors.New("not Ogg/Opus")

// decodeVoice decodes the note at path through the account's core
// (core.VoiceDecoder), and reads and frees the PCM blob it gives. It runs on
// a worker: both block. Tests replace it.
var decodeVoice = func(conn core.Conn, path string) (Clip, error) {
	d, ok := conn.(core.VoiceDecoder)
	if !ok || conn == nil {
		return Clip{}, readable(core.ErrUnsupported)
	}
	blob, vf, err := d.DecodeVoice(path)
	if err != nil {
		return Clip{}, readable(err)
	}
	defer conn.FreeBlob(blob.ID)
	f := media.Format{SampleRate: vf.Rate, Channels: max(vf.Channels, 1), Sample: media.Float32}
	n := int(blob.Len)
	if fb := int64(f.FrameBytes()); vf.Frames > 0 && vf.Frames*fb < int64(n) {
		n = int(vf.Frames * fb)
	}
	pcm := make([]byte, n)
	for off := 0; off < n; {
		k, err := conn.ReadBlob(blob.ID, uint64(off), pcm[off:])
		if err != nil {
			return Clip{}, readable(err)
		}
		if k <= 0 {
			pcm = pcm[:off]
			break
		}
		off += k
	}
	pcm = pcm[:len(pcm)/f.FrameBytes()*f.FrameBytes()]
	if len(pcm) == 0 {
		return Clip{}, errors.New(i18n.T("The clip is empty"))
	}
	return Clip{PCM: pcm, Format: f, Bars: vf.Waveform, HasBars: true}, nil
}

// readable is the core's error as upstream's player words it: "Could not
// read the audio: …" for a file that is not there, "Could not decode the
// audio: …" for the rest; a file that is not Ogg/Opus also wraps
// errUndecodable.
func readable(err error) error {
	msg := err.Error()
	var ce *core.Error
	if errors.As(err, &ce) {
		msg = ce.Msg
		if ce.Code == core.ENotFound {
			return errors.New(strings.Replace(i18n.T("Could not read the audio: {error}"), "{error}", msg, 1))
		}
	}
	text := strings.Replace(i18n.T("Could not decode the audio: {error}"), "{error}", msg, 1)
	if ce != nil && ce.Code == core.EMalformed {
		return &undecodable{text}
	}
	return errors.New(text)
}

type undecodable struct{ text string }

func (u *undecodable) Error() string        { return u.text }
func (u *undecodable) Is(target error) bool { return target == errUndecodable }
