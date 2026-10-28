package settings

import (
	"slices"
	"testing"
	"time"
)

// The window's links, the update feed's release page among them (the feed is
// not signed), open only as web addresses: a file's path or another scheme
// is refused with a note, never handed to the system.
func TestOnlyAWebAddressOpensAsALink(t *testing.T) {
	r := newRig(t, 720, 900, nil)
	sh := r.s.Shell.(*fakeShell)
	for _, link := range []string{"/Applications/Calculator.app", "file:///etc/passwd", "x-man-page://ls", "vnc://host", "https://example.com/r"} {
		r.scr.openURL(link)
	}
	for i := 0; i < 500 && len(sh.all()) == 0; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	r.Frames(2)
	if got := sh.all(); !slices.Equal(got, []string{"https://example.com/r"}) {
		t.Errorf("opened %q", got)
	}
	n := 0
	for _, ts := range r.s.Toasts {
		if string(ts.Text) == "This link type cannot be opened from Zap" && ts.Error {
			n++
		}
	}
	if n == 0 {
		t.Errorf("no note: toasts %+v", r.s.Toasts)
	}
}
