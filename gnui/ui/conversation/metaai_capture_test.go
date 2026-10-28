package conversation

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"zapfast/abi"
)

// TestCaptureMetaAI writes a Meta AI reply with code and a table as the
// transcript draws it (MK-031). It is a generator, not a check: without
// ZAPFAST_CAPTURE it skips.
func TestCaptureMetaAI(t *testing.T) {
	dir := os.Getenv("ZAPFAST_CAPTURE")
	if dir == "" {
		t.Skip("set ZAPFAST_CAPTURE=<dir> to write the capture")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRig(t)
	q := msg(ada, "q", base0, true, "How do I reverse a string in Rust?")
	a := msg(ada, "a", base0+60, false, "Collect its characters in reverse order:\n\n```\nlet reversed: String = text.chars().rev().collect();\n```\n\nMethod | Handles\nchars().rev() | Unicode scalar values\ngraphemes(true).rev() | Combined emoji and accents")
	a.HasQuoted = true
	a.Quoted = abi.Quoted{ID: []byte("q"), Sender: []byte("9@s.whatsapp.net"), Summary: []byte("How do I reverse a string in Rust?")}
	r.open(ada, q, a)
	r.Frames(3)
	f, err := os.Create(filepath.Join(dir, "meta-ai.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, r.Image()); err != nil {
		t.Fatal(err)
	}
}
