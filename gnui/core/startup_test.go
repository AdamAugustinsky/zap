package core

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zapfast/abi"
)

// OpenAsync never holds up its caller (the first frame), and the core's
// chat list follows. Cold is a fresh copy of the library, loaded once, as
// the first launch after an install is; the in-process numbers are
// reported. The exec-relative ones come from cmd/zapfast -startup.
func TestOpenAsyncDoesNotBlockAndTheChatsFollow(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fresh copy of the library")
	}
	src, err := FindLibrary()
	if err != nil {
		t.Skipf("no core library: %v", err)
	}
	cold := filepath.Join(t.TempDir(), LibName)
	if err := copyFile(cold, src); err != nil {
		t.Fatal(err)
	}
	w := newWakes()
	t0 := time.Now()
	p := OpenAsync(Config{Library: cold, Demo: true, Wake: w.wake})
	returned := time.Since(t0)
	if returned > 5*time.Millisecond {
		t.Errorf("OpenAsync held its caller for %v", returned)
	}
	if !p.Wait(30 * time.Second) {
		t.Fatal("the core did not open within 30 s")
	}
	c, err := p.Result()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(5 * time.Second)
	s := &session{t: t, c: c, w: w, h: &recorder{}}
	s.until("the chat list", func(e *abi.Event) bool { return e.Tag == abi.EventTagChats && e.Chats.Last })
	chats := time.Since(t0)
	tm := p.Timing
	t.Logf("%s (%s): OpenAsync returned in %v; cold load %v, zf_core_open %v, OpenAsync → whole chat list drained %v",
		filepath.Base(filepath.Dir(src)), src, returned, tm.Loaded.Sub(tm.Began), tm.Opened.Sub(tm.Loaded), chats)
}

func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
