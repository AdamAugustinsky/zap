package demo

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"

	"zapfast/abi"
	"zapfast/gocore/av"
)

// MediaDir is where the demo keeps its media, its voice notes and the ones
// it is sent: the system's temporary directory, never the account's, and
// the only folder a demo Conn's DecodeVoice reads. It is zfcore's folder.
func MediaDir() string { return filepath.Join(os.TempDir(), "zapfast-demo-media") }

// AvatarDir is where the demo's profile pictures are written for Avatar to
// name.
func AvatarDir() string { return filepath.Join(os.TempDir(), "zapfast-demo-avatars") }

// writes numbers the partial files, as demo cores in one process may write
// the same file at once.
var writes atomic.Uint64

// writeOnce is b as the file name in dir, written once: kept when it is
// there with its length, else written through a name unique to this write,
// so a reader never sees half of one. "" when it cannot be written.
func writeOnce(dir, name string, b []byte) string {
	path := filepath.Join(dir, name)
	if fi, err := os.Stat(path); err == nil && fi.Size() == int64(len(b)) {
		return path
	}
	if writeFile(dir, name, b) != nil {
		return ""
	}
	return path
}

// writeFile writes b whole to dir/name or not at all.
func writeFile(dir, name string, b []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	partial := filepath.Join(dir, fmt.Sprintf(".%s.%d.%d", name, os.Getpid(), writes.Add(1)-1))
	if err := os.WriteFile(partial, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(partial, filepath.Join(dir, name)); err != nil {
		os.Remove(partial)
		return err
	}
	return nil
}

// stock is a stock file of the demo's in MediaDir, by name.
func stock(name string, b []byte) string { return writeOnce(MediaDir(), name, b) }

// avatarPath is the picture of id as a file, written once; "" for anyone
// without one.
func avatarPath(id string) string {
	for _, a := range avatars {
		if a.owner == id {
			return writeOnce(AvatarDir(), a.name, asset("avatars/"+a.name))
		}
	}
	return ""
}

func stickerFile(name string) string {
	for _, s := range stickers {
		if s.name == name {
			return writeOnce(filepath.Join(MediaDir(), "stickers"), name+".webp", asset("stickers/"+name+".webp"))
		}
	}
	return ""
}

func fileSize(path string) uint64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return uint64(fi.Size())
}

// speech is something like speech: a voiced tone whose pitch drifts, in
// syllables of about a fifth of a second, with a pause every few. It is
// zfcore's, in float32 operation for operation (each product rounded, so
// none is fused), so its waveform comes out the same.
func speech(seconds, pitch float32) []float32 {
	const rate = float32(av.Rate)
	const pi = float32(math.Pi)
	const tau = float32(2 * math.Pi)
	n := int(float32(seconds * rate))
	out := make([]float32, n)
	var phase float32
	for i := range n {
		t := float32(i) / rate
		syllable := fract(float32(t * 5))
		word := fract(float32(t / 1.6))
		var envelope float32
		if word <= 0.82 {
			swing := float32(float32(0.4) * abs(sin(float32(t*0.9))))
			envelope = float32(pow(sin(float32(syllable*pi)), 0.7) * float32(float32(0.6)+swing))
		}
		drift := float32(1+float32(float32(0.12)*sin(float32(t*2.3)))) + float32(float32(0.05)*sin(float32(t*7)))
		f0 := float32(pitch * drift)
		phase += float32(f0 / rate)
		voiced := sin(float32(tau*phase)) +
			float32(float32(0.5)*sin(float32(float32(2*tau)*phase)))
		voiced = voiced + float32(float32(0.25)*sin(float32(float32(3*tau)*phase)))
		voiced = voiced + float32(float32(0.12)*sin(float32(float32(5*tau)*phase)))
		out[i] = float32(float32(float32(0.3)*envelope) * voiced)
	}
	return out
}

func sin(x float32) float32    { return float32(math.Sin(float64(x))) }
func abs(x float32) float32    { return float32(math.Abs(float64(x))) }
func fract(x float32) float32  { return x - float32(math.Trunc(float64(x))) }
func pow(x, y float32) float32 { return float32(math.Pow(float64(x), float64(y))) }

// sentVoice numbers the voice notes this process's demos were sent.
var sentVoice atomic.Uint64

// voiceNote is a voice note's content for samples (mono 48 kHz), its file
// name in MediaDir: encoded when the file is not there yet (or fresh), and
// written whole or not at all. Without a file the note has no path, as one
// not downloaded.
func voiceNote(name string, samples []float32, fresh bool) abi.Content {
	dir := MediaDir()
	path := filepath.Join(dir, name)
	var size uint64
	ok := false
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() && !fresh {
		size, ok = uint64(fi.Size()), true
	} else if b, err := av.EncodeOpus(samples); err == nil && writeFile(dir, name, b) == nil {
		size, ok = uint64(len(b)), true
	}
	c := abi.Content{Tag: abi.ContentTagAudio}
	a := &c.Audio
	a.Media = abi.Media{Mime: []byte(av.VoiceMIME), Size: size}
	if ok {
		a.Media.Path, a.Media.HasPath = []byte(path), true
	}
	// As the worker counts them.
	a.Seconds = uint32(max(math.Round(float64(len(samples))/float64(av.Rate)), 1))
	a.HasSeconds = true
	a.VoiceNote = true
	w := av.Waveform(samples)
	a.Waveform = w[:]
	return c
}
