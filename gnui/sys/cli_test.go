package sys

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/instance"
	"gonativeui/app/notify"
)

// launchIn is Launch over a lock in dir, with no notification opt-in.
func launchIn(dir string, stderr *bytes.Buffer, args ...string) (int, bool) {
	return Launch(LaunchOptions{
		AppID: "ai.a3lab.zap.test", Args: args, Stderr: stderr,
		acquire: func(id string, o instance.Options) (*instance.Primary, *instance.Secondary, error) {
			o.Dir = dir
			return instance.AcquireWith(id, o)
		},
		setup: func(notify.Options) error { return nil },
	})
}

// A short folder: a Unix-domain socket's path has a limit.
func socketDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "zi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// TH-007, NT-012: `quit` and `reload-themes` only talk to a copy that runs;
// with none running they do nothing and never start ZapFast.
func TestAControlCommandWithNoCopyRunningDoesNothingAndStartsNothing(t *testing.T) {
	for _, cmd := range []string{"quit", "reload-themes"} {
		dir := socketDir(t)
		var e bytes.Buffer
		if code, done := launchIn(dir, &e, cmd); !done || code != 0 {
			t.Errorf("%s: code %d done %v", cmd, code, done)
		}
		if launched.primary != nil {
			t.Errorf("%s: it took the lock", cmd)
		}
		// Nothing keeps the lock: the next launch is the first.
		p, sec, err := instance.AcquireWith("ai.a3lab.zap.test", instance.Options{Dir: dir})
		if err != nil || sec != nil {
			t.Fatalf("%s: %v %v", cmd, sec, err)
		}
		p.Close()
	}
}

// LN-006: an argument that names no chat opens nothing, says so, and exits 2.
func TestAnUnreadableArgumentSaysSoAndExitsWithTwo(t *testing.T) {
	var e bytes.Buffer
	code, done := launchIn(socketDir(t), &e, "hello")
	if !done || code != 2 || !strings.Contains(e.String(), "Zap cannot read a phone number from hello") {
		t.Errorf("code %d done %v: %q", code, done, e.String())
	}
}

// NT-011, LN-005, LN-007: the first launch keeps the lock and its link; a
// second hands its arguments to the first and leaves.
func TestASecondLaunchHandsItsArgumentsToTheFirstAndLeaves(t *testing.T) {
	dir := socketDir(t)
	var e bytes.Buffer
	if code, done := launchIn(dir, &e, "wa.me/20123456789"); done || code != 0 {
		t.Fatalf("the first launch: code %d done %v", code, done)
	}
	first := launched.primary
	if first == nil || len(launched.args) != 1 {
		t.Fatalf("the first launch kept %v %v", first, launched.args)
	}
	t.Cleanup(func() { first.Close(); launched.primary, launched.args = nil, nil })
	if code, done := launchIn(dir, &e, "reload-themes"); !done || code != 0 {
		t.Fatalf("the second launch: code %d done %v: %q", code, done, e.String())
	}
	reqs := first.Poll(nil)
	if len(reqs) != 1 || len(reqs[0].Args) != 1 || reqs[0].Args[0] != "reload-themes" {
		t.Errorf("the first copy was told %+v", reqs)
	}
}

// NT-010: the Keep running setting reaches the app as it changes.
func TestTheKeepRunningSettingReachesTheAppAsItChanges(t *testing.T) {
	r := newRig(t)
	var got []bool
	r.y.keep = func(on bool) { got = append(got, on) }
	r.s.Settings.KeepRunning = false
	r.frame()
	r.s.Settings.KeepRunning = true
	r.frame()
	r.frame()
	r.s.Settings.KeepRunning = false
	r.frame()
	if len(got) != 3 || got[0] || !got[1] || got[2] {
		t.Errorf("%v", got)
	}
}

// A second process's request reaches the running copy as a post of its own,
// with no window open and without polling, and is carried out once.
func TestARealSecondProcessReachesTheRunningCopyWithoutAWindow(t *testing.T) {
	if dir := os.Getenv("ZAPFAST_SECOND_PROCESS"); dir != "" {
		var e bytes.Buffer
		code, done := launchIn(dir, &e, "reload-themes")
		if !done {
			code = 3
		}
		os.Exit(code)
	}
	dir := socketDir(t)
	posts := make(chan uint64, 4)
	old := postFn
	postFn = func(id uint64) bool { posts <- id; return true }
	t.Cleanup(func() { postFn = old; launched.primary, launched.args = nil, nil })
	var e bytes.Buffer
	if _, done := launchIn(dir, &e); done || launched.primary == nil {
		t.Fatal("this process is not the first")
	}
	r := newRig(t)
	r.win = nil
	t.Cleanup(r.y.stopInstance) // newRig began the services: they took the lock

	cmd := exec.Command(os.Args[0], "-test.run=^TestARealSecondProcessReachesTheRunningCopyWithoutAWindow$")
	cmd.Env = append(os.Environ(), "ZAPFAST_SECOND_PROCESS="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the second process: %v: %s", err, out)
	}
	select {
	case id := <-posts:
		if id != postInstance {
			t.Fatalf("posted %x", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the running copy was not told")
	}
	// The app's handler gets the post and collects the request.
	if !r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvPost, ID: postInstance}) || r.reload != 1 {
		t.Errorf("reloads %d", r.reload)
	}
	r.y.AppEvent(nil, r.s, &app.Event{Kind: app.EvPost, ID: postInstance})
	r.frame()
	if r.reload != 1 {
		t.Errorf("the request was carried out %d times", r.reload)
	}
}
