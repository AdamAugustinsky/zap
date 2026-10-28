package viewer

import (
	"bufio"
	"encoding/binary"
	"image"
	_ "image/jpeg" // registered for image.DecodeConfig
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gonativeui/img"
)

// probeResult is the answer of a header probe: a picture's own pixel size,
// read on a worker so the main thread never touches the file. The decoder
// scales to the size shown and does not report the original's.
type probeResult struct {
	mu   sync.Mutex
	path string
	w, h int32
	ok   bool
	done bool
}

// startProbe reads the header of path on a worker.
func (s *Screen) startProbe(path string) {
	pr := &probeResult{path: path}
	s.probe = pr
	wake := s.wake
	go func() {
		w, h, ok := probeSize(path)
		pr.mu.Lock()
		pr.w, pr.h, pr.ok, pr.done = int32(w), int32(h), ok, true
		pr.mu.Unlock()
		wake()
	}()
}

// takeProbe is the probe of path: whether it finished and what it found.
func (s *Screen) takeProbe(path string) (w, h int32, done, ok bool) {
	pr := s.probe
	if pr == nil || pr.path != path {
		return 0, 0, false, false
	}
	pr.mu.Lock()
	defer pr.mu.Unlock()
	return pr.w, pr.h, pr.done, pr.ok
}

// probeFailed reports a finished probe that could not read the size (a
// format the standard library does not know: the decoder's size stands).
func (s *Screen) probeFailed(path string) bool {
	_, _, done, ok := s.takeProbe(path)
	return done && !ok
}

// probeSize reads a JPEG's, PNG's or WebP's pixel size from its header.
func probeSize(path string) (w, h int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	head := make([]byte, 128<<10) // the EXIF segment lies in the first of a JPEG
	n, _ := f.ReadAt(head, 0)
	head = head[:n]
	if w, h, ok := webpSize(head); ok {
		return w, h, true
	}
	cfg, _, err := image.DecodeConfig(bufio.NewReader(f))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	// The decoder turns a JPEG as its EXIF orientation says (5 to 8 turn it a
	// quarter): the sides on screen are the other way round.
	if img.Orientation(head) >= 5 {
		return cfg.Height, cfg.Width, true
	}
	return cfg.Width, cfg.Height, true
}

// webpSize reads the canvas size from the first chunk of a WebP file (lossy,
// lossless or extended).
func webpSize(b []byte) (w, h int, ok bool) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8 ":
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, false
		}
		return int(binary.LittleEndian.Uint16(b[26:])) & 0x3fff, int(binary.LittleEndian.Uint16(b[28:])) & 0x3fff, true
	case "VP8L":
		if b[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(b[21:])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, true
	case "VP8X":
		return int(b[24]) | int(b[25])<<8 | int(b[26])<<16 + 1, int(b[27]) | int(b[28])<<8 | int(b[29])<<16 + 1, true
	}
	return 0, 0, false
}

// isVideoName reports a clip from its file name alone, for an item the
// album does not hold yet (image_preview.rs is_video).
func isVideoName(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v", ".mov", ".webm", ".mkv", ".3gp", ".3gpp":
		return true
	}
	return false
}
