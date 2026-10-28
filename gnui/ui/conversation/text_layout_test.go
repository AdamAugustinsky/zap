package conversation

import (
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
)

// Fixed messages and times let the two layout paths be compared pixel for
// pixel. ZF_TEXT_SHOTS optionally saves the rendered transcript for review.
func TestTranscriptTextLayout(t *testing.T) {
	bodies := []string{
		"A short *bold* reply, with _italic_ and ~struck~ words.",
		strings.Repeat("The Analytical Engine weaves algebraic patterns. ", 12) + "Done.",
		strings.Repeat("abcdefghijklmnopqrstuvwxyz", 9),
		"שלום עולם שלום עולם שלום עולם שלום עולם שלום עולם שלום עולם\nשלום",
	}
	for _, width := range []int{620, 1100} {
		name := strconv.Itoa(width)
		t.Run(name, func(t *testing.T) {
			r := newRigSized(t, width, 2200)
			var msgs []abi.Message
			for i, body := range bodies {
				msgs = append(msgs, msg(ada, strconv.Itoa(i), base0+int64(i)*60, true, body))
			}
			r.open(ada, msgs...)
			r.Frames(8)
			// A wrapped RTL paragraph must keep the footer below its text:
			// the inline footer's right alignment is not its reading direction.
			label := r.node(ui.RoleLabel, bodies[len(bodies)-1]).Bounds
			footers := r.all(ui.RoleButton, "Message info")
			if len(footers) != len(bodies) {
				t.Fatalf("%d footers, want %d", len(footers), len(bodies))
			}
			if footer := footers[len(footers)-1].Bounds; footer[1] < label[3]-0.5 {
				t.Errorf("RTL footer %v overlaps text %v", footer, label)
			}
			if dir := os.Getenv("ZF_TEXT_SHOTS"); dir != "" {
				f, err := os.Create(filepath.Join(dir, strings.ReplaceAll(name, "/", "-")+".png"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, r.Image()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
