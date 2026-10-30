package sys

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"gonativeui/app"
	"gonativeui/app/instance"
	"gonativeui/app/login"
	"gonativeui/app/notify"
	"gonativeui/platform"

	"zapfast/i18n"
	"zapfast/theme"
)

// The command line and the single instance (zf:src/single_instance.rs,
// zf:src/main.rs): zapfast [quit | reload-themes | <number or link>].
//
// LaunchServices keeps one instance of a bundled app, but the binary inside
// the bundle can be run from a terminal, which is how the command line
// reaches a running copy: app/instance hands it the arguments.

type command uint8

const (
	cmdShow         command = iota // no argument: bring the window forward
	cmdQuit                        // quit the running copy
	cmdReloadThemes                // refresh the themes of the running copy
	cmdLink                        // open a chat, an invite or a call link
)

// parseArgs reads the positional arguments of a launch. An argument that
// names no chat says so rather than opening one it guessed at.
func parseArgs(args []string) (cmd command, arg string, err error) {
	switch len(args) {
	case 0:
		return cmdShow, "", nil
	case 1:
		switch args[0] {
		case "quit":
			return cmdQuit, "", nil
		case "reload-themes":
			return cmdReloadThemes, "", nil
		}
		if _, ok := parseLink(args[0]); ok {
			return cmdLink, args[0], nil
		}
		return 0, "", fmt.Errorf("%s", i18n.Format(i18n.T("Zap cannot read a phone number from {argument}"), "{argument}", args[0]))
	}
	return 0, "", errors.New("zapfast: one number, link or command at a time")
}

// reloadThemes refreshes the theme list and the chosen theme (TH-007).
func reloadThemes() { theme.Reload() }

// launched is what Launch decided for this process.
var launched struct {
	primary *instance.Primary
	args    []string // a first launch's positional arguments: a link to open
}

// holding is whether this process holds the single-instance lock, from
// Launch taking it to stopInstance letting it go: launched.primary moves to
// the System when its services start (startInstance), and the lock is still
// held then.
var holding atomic.Bool

// LaunchOptions are Launch's inputs.
type LaunchOptions struct {
	// AppID is the app's identity (the instance lock's name).
	AppID string
	// Args are the positional arguments (flags removed).
	Args []string
	// Demo runs take no instance: they use no disk.
	Demo bool
	// Stderr is where it says what it did (os.Stderr).
	Stderr io.Writer

	// Tests replace the lock and the notification opt-in.
	acquire func(id string, o instance.Options) (*instance.Primary, *instance.Secondary, error)
	setup   func(notify.Options) error
}

// Launch runs before the core is opened and before app.Main, on the main
// thread: it keeps one linked copy, hands a second launch's command line to
// the first, and opts in to notifications so that a click that launched the
// app is not lost. done means the process should exit with code: the
// command was sent to the running copy, or was not readable.
func Launch(o LaunchOptions) (code int, done bool) {
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	cmd, _, err := parseArgs(o.Args)
	if err != nil {
		fmt.Fprintln(o.Stderr, err)
		return 2, true
	}
	if !o.Demo {
		id := o.AppID
		if b := platform.BundleID(); b != "" {
			id = b // a .dev bundle must not share a lock with the release app
		}
		acquire := instance.AcquireWith
		if o.acquire != nil {
			acquire = o.acquire
		}
		// A second launch's request reaches the app as a post of its own, with
		// or without a window; the handler then collects it.
		p, sec, err := acquire(id, instance.Options{Notify: func() { postFn(postInstance) }})
		switch {
		case err != nil:
			// Cannot tell whether another copy runs: run anyway.
			logWarn("single instance", "err", err)
		case sec != nil:
			return sendToRunning(sec, o, cmd), true
		default:
			if cmd == cmdQuit || cmd == cmdReloadThemes {
				// A control command only talks to a running copy: with none
				// running it does nothing and never starts ZapFast.
				p.Close()
				return 0, true
			}
			launched.primary = p
			holding.Store(true)
		}
	}
	launched.args = o.Args
	setup := notify.Setup
	if o.setup != nil {
		setup = o.setup
	}
	if err := setup(notify.Options{Foreground: true}); err != nil {
		logWarn("notifications are off", "err", err)
	}
	return 0, false
}

// HoldsInstance reports whether this process holds the single-instance lock
// (Launch took it): what taking an account over from the Rust core needs, so
// that no other copy runs on the same files.
// It stays true once the services have taken the lock over (startInstance),
// so that an account taken over on Try again, after a first try failed, is
// not refused as if another copy ran.
func HoldsInstance() bool { return holding.Load() }

// sendToRunning hands the arguments to the running copy.
func sendToRunning(sec *instance.Secondary, o LaunchOptions, cmd command) int {
	if login.StartHidden() && cmd == cmdShow {
		fmt.Fprintln(o.Stderr, "Zap is already running")
		return 0
	}
	if err := sec.Send(o.Args); err != nil {
		switch {
		case errors.Is(err, instance.ErrNoAnswer):
			fmt.Fprintln(o.Stderr, "Zap is already running but did not answer")
		default:
			fmt.Fprintf(o.Stderr, "Zap is already running, but did not take the request: %v\n", err)
		}
		return 1
	}
	return 0
}

// Config is the app's configuration: its identity, and whether closing the
// last window keeps it running (the "keep running" setting). A start at
// login passes --start-hidden, which app.Main reads itself: the main window
// opens hidden.
func Config(id string, keepRunning bool) app.AppConfig {
	return app.AppConfig{ID: id, KeepRunning: keepRunning}
}

// instances is the second launches' queue.
type instances struct {
	p      *instance.Primary
	serial uint32
	buf    []instance.Request
}

func (y *System) startInstance() {
	i := &y.i
	i.p, launched.primary = launched.primary, nil
}

func (y *System) stopInstance() {
	i := &y.i
	if i.p == nil {
		return
	}
	i.p.Close()
	i.p = nil
	holding.Store(false)
}

// pollInstance does what second launches asked.
func (y *System) pollInstance() {
	i := &y.i
	if i.p == nil || i.p.Serial() == i.serial {
		return
	}
	i.serial = i.p.Serial()
	i.buf = i.p.Poll(i.buf[:0])
	for _, r := range i.buf {
		if y.openArgs(r.Args) {
			y.showMain()
		}
	}
}
