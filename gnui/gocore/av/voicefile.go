package av

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// Decoded is a voice note decoded for playback.
type Decoded struct {
	// Samples are mono at Rate, -1 to 1.
	Samples []float32
	// Waveform is the note's bars, as Waveform computes them.
	Waveform [Bars]byte
}

// longestSamples is the most samples a decoded note may have: LongestNote
// and a second.
const longestSamples = (int(LongestNote/time.Second) + 1) * Rate

// DecodeVoiceFile decodes the Ogg/Opus note at path, as zfcore's DecodeVoice
// does. Only a file under one of roots (not a root itself) is read, compared
// after symbolic links are resolved: any other is ErrOutside, a missing one
// ErrNotFound. A file over LargestNote, or a stream whose last page says it
// is over LongestNote, is ErrTooLong before anything is decoded; one that is
// not Ogg/Opus is ErrMalformed. It blocks while it decodes: call it from a
// worker.
func DecodeVoiceFile(path string, roots []string) (Decoded, error) {
	real, err := inside(path, roots)
	if err != nil {
		return Decoded{}, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return Decoded{}, ioError(err)
	}
	if !info.Mode().IsRegular() {
		return Decoded{}, ErrNotFound
	}
	if info.Size() > LargestNote {
		return Decoded{}, ErrTooLong
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return Decoded{}, ioError(err)
	}
	if g, ok := lastGranule(data); ok && g > uint64(longestSamples) {
		return Decoded{}, ErrTooLong
	}
	samples, err := decodeOpus(data, longestSamples+5760)
	if err != nil {
		return Decoded{}, err
	}
	if len(samples) > longestSamples {
		return Decoded{}, ErrTooLong
	}
	return Decoded{Samples: samples, Waveform: Waveform(samples)}, nil
}

// inside resolves path and reports whether it lies under one of roots,
// resolved.
func inside(path string, roots []string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", ioError(err)
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return "", ioError(err)
	}
	for _, root := range roots {
		r, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if r, err = filepath.Abs(r); err != nil {
			continue
		}
		if rel, err := filepath.Rel(r, real); err == nil && rel != "." && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return real, nil
		}
	}
	return "", ErrOutside
}

func ioError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return fmt.Errorf("av: could not read the file: %w", err)
}

// VoiceDecoder is core.VoiceDecoder for a Go backend: DecodeVoiceFile under
// the account's folders, its PCM put in a blob of the backend's.
type VoiceDecoder struct {
	// Roots gives the folders a note may be read from, at the time of the
	// call: the account's state and cache, the download folder, and the
	// demo's media folder.
	Roots func() []string
	// Put stores pcm in a new blob for the caller and names it.
	Put func(pcm []byte) abi.BlobRef
}

var _ core.VoiceDecoder = (*VoiceDecoder)(nil)

// DecodeVoice decodes the note at path (absolute) as zfcore's
// zf_voice_decode does, with its error codes: a relative path or one outside
// the account's folders is EArg, a missing file ENotFound, one too long
// ETooLarge, a damaged one EMalformed and a read that fails EIO. Without an
// Opus decoder the error matches errors.ErrUnsupported, as core.Core's does
// against an old library.
func (d *VoiceDecoder) DecodeVoice(path string) (abi.BlobRef, core.VoiceFormat, error) {
	fail := func(code int32, msg string) (abi.BlobRef, core.VoiceFormat, error) {
		return abi.BlobRef{}, core.VoiceFormat{}, &core.Error{Op: "zf_voice_decode", Code: code, Msg: msg}
	}
	if path == "" || !filepath.IsAbs(path) {
		return fail(core.EArg, "the path is not absolute")
	}
	var roots []string
	if d.Roots != nil {
		roots = d.Roots()
	}
	v, err := DecodeVoiceFile(path, roots)
	switch {
	case err == nil:
	case errors.Is(err, ErrOutside):
		return fail(core.EArg, "the file is not in the account's folders")
	case errors.Is(err, ErrNotFound):
		return fail(core.ENotFound, "no such file")
	case errors.Is(err, ErrTooLong):
		return fail(core.ETooLarge, "the voice note is over 30 minutes or 64 MiB")
	case errors.Is(err, ErrMalformed):
		return fail(core.EMalformed, strings.TrimPrefix(err.Error(), "av: "))
	case errors.Is(err, ErrUnavailable):
		return abi.BlobRef{}, core.VoiceFormat{}, fmt.Errorf("%w: %w", core.ErrUnsupported, err)
	default:
		return fail(core.EIO, strings.TrimPrefix(err.Error(), "av: "))
	}
	ref := d.Put(PCM(v.Samples))
	return ref, core.VoiceFormat{Rate: Rate, Channels: 1, Frames: int64(len(v.Samples)), Waveform: v.Waveform}, nil
}
