package main

// The backend every account runs on (docs/go-backend/PLAN.md): gocore, the
// Go demo with -demo, else whatsmeow (gocore/wa).
//
// An account the Rust core kept, which earlier releases ran (its session
// store, session.db, and no whatsmeow.db), is taken over before it opens:
// gocore/migrate reads the Rust core's files in Go and converts them, copy
// first, crash safe, failing closed. A migration that began (its marker,
// migration.json) is resumed the same way.
//
// Calls are WP7's bridge (gocore/calls): whatsmeow carries the 1:1
// signaling and the Go call engine (gocore/voip/engine) the media and the
// video frames (core.Frames). Group calls and call links are not bridged.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	wastore "go.mau.fi/whatsmeow/store"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore"
	"zapfast/gocore/av"
	"zapfast/gocore/calls"
	"zapfast/gocore/demo"
	"zapfast/gocore/migrate"
	"zapfast/gocore/store"
	"zapfast/gocore/store/sqlite"
	"zapfast/gocore/wa"
	"zapfast/state"
	"zapfast/sys"
)

// needsMigration reports whether the account in dir must be taken over
// from the Rust core before it opens: a migration began (its marker is
// there, whatever its state: Account resumes it, or returns at once when it
// is done), or the Rust core's session store is there and whatsmeow's is
// not.
func needsMigration(dir string) bool {
	if exists(filepath.Join(dir, migrate.MarkerFile)) {
		return true
	}
	return exists(filepath.Join(dir, migrate.SessionFile)) && !exists(filepath.Join(dir, store.DevicesFile))
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// coreOpener opens the accounts' cores (State.OpenCore).
type coreOpener struct {
	demo bool
	wake func()

	// Zero values are the app's: core.Dirs, the Keychain under
	// core.KeyringService, whatsmeow, and sys.HoldsInstance. Tests set them.
	stateDir, cacheDir string
	keys               store.Keys
	newClient          func(device *wastore.Device) wa.Client
	holdsLock          func() bool

	mu sync.Mutex
	// relink are the accounts whose session could not be taken over: the
	// next open (Try again) links them afresh.
	relink map[string]bool
}

// open is State.OpenCore: the core of account id, opening on a goroutine of
// its own.
func (o *coreOpener) open(id string) (*core.Pending, core.Conn) {
	cfg := core.Config{Demo: o.demo, Account: id, Wake: o.wake, StateDir: o.stateDir, CacheDir: o.cacheDir}
	if o.demo {
		return core.OpenAsync(o.wake, func(*core.Timing) (core.Conn, error) {
			c, err := demo.Open(cfg)
			if err != nil {
				return nil, err
			}
			return c, nil
		}), nil
	}
	return core.OpenAsync(o.wake, func(t *core.Timing) (core.Conn, error) {
		dir, err := o.accountDir(&cfg)
		if err != nil {
			return nil, err
		}
		return o.openReal(cfg, dir, t)
	}), nil
}

// accountDir is the account's state folder, cfg's roots resolved as
// gocore.Open resolves them.
func (o *coreOpener) accountDir(cfg *core.Config) (string, error) {
	if !state.SafeAccountID(cfg.Account) {
		return "", &core.Error{Op: "zf_core_open", Code: core.EArg, Msg: "the account id is not a folder name of digits"}
	}
	if cfg.StateDir == "" || cfg.CacheDir == "" {
		st, cache, err := core.Dirs()
		if err != nil {
			return "", fmt.Errorf("zapfast core: the app's directories: %w", err)
		}
		if cfg.StateDir == "" {
			cfg.StateDir = st
		}
		if cfg.CacheDir == "" {
			cfg.CacheDir = cache
		}
	}
	return filepath.Join(cfg.StateDir, "accounts", cfg.Account), nil
}

// openReal opens a real account: the SQLCipher library, then the account
// taken over from the Rust core if it must be, then the WhatsApp engine
// (which opens the stores on its own goroutine).
func (o *coreOpener) openReal(cfg core.Config, dir string, t *core.Timing) (core.Conn, error) {
	lib, err := sqlite.Load()
	if err != nil {
		return nil, fmt.Errorf("the app's storage library: %w", err)
	}
	t.Library = lib
	if o.takeRelink(cfg.Account) {
		slog.Warn("zapfast: linking the account afresh", "account", cfg.Account)
	} else if needsMigration(dir) {
		if err := o.migrate(cfg.Account, dir); err != nil {
			return nil, err
		}
	}
	t.Loaded = time.Now()
	eng := wa.New(wa.Options{Keys: o.keys, NewClient: o.newClient, DeviceName: core.DeviceName, Log: sys.WhatsmeowLog()})
	c, err := gocore.Open(cfg, eng)
	if err != nil {
		return nil, err
	}
	return newGoConn(c, eng), nil
}

func (o *coreOpener) keychain() store.Keys {
	if o.keys != nil {
		return o.keys
	}
	return store.Keychain(core.KeyringService)
}

// migrate takes the account in dir over from the Rust core (migrate.Account),
// holding the single-instance lock. A folder with nothing to take over is
// linked afresh; a failure is a *migrationError, which the account
// shows as a core that failed to open.
func (o *coreOpener) migrate(id, dir string) error {
	holds := sys.HoldsInstance
	if o.holdsLock != nil {
		holds = o.holdsLock
	}
	if !holds() {
		return &migrationError{Err: errNoInstance}
	}
	moving := exists(filepath.Join(dir, migrate.SessionFile)) // not yet retired: this run moves it
	r, err := migrate.Account(context.Background(), migrate.Options{Dir: dir, Keys: o.keychain()})
	switch {
	case err == nil:
		if r.State != migrate.StateActive {
			return &migrationError{Err: fmt.Errorf("the migration stopped at %q", r.State)}
		}
		if moving {
			slog.Warn("zapfast: the account moved to the Go backend", "account", id, "backup", r.Backup)
		}
		return nil
	case errors.Is(err, migrate.ErrNoRustSession):
		return nil // nothing to take over: link afresh
	}
	m := &migrationError{Err: err, Relink: errors.Is(err, migrate.ErrIncompatible) || errors.Is(err, migrate.ErrUnknownSchema) || errors.Is(err, migrate.ErrPreKeyGap)}
	if m.Relink {
		o.mu.Lock()
		if o.relink == nil {
			o.relink = map[string]bool{}
		}
		o.relink[id] = true
		o.mu.Unlock()
	}
	slog.Error("zapfast: the account could not move to the Go backend", "account", id, "err", err)
	return m
}

// takeRelink reports, once, whether account id is to be linked afresh.
func (o *coreOpener) takeRelink(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.relink[id]
	delete(o.relink, id)
	return r
}

// errNoInstance: the single-instance lock is not held, so another copy of
// the app may have the account's files open.
var errNoInstance = errors.New("another copy of Zap may be running")

// migrationError is why an account could not move from the Rust core to the
// Go backend. It is the core's open error, so the login screen shows it with
// Try again (State.Reopen); Err is matched with errors.Is.
type migrationError struct {
	Err error
	// Relink: the session itself cannot be carried over; Try again links
	// this computer afresh.
	Relink bool
}

func (e *migrationError) Error() string {
	const lead = "Zap could not move this account to its new backend"
	switch {
	case errors.Is(e.Err, errNoInstance):
		return lead + ": another copy of Zap may be running. Quit it, then choose Try again."
	case errors.Is(e.Err, migrate.ErrSourceChanged):
		return lead + ": its files changed while they were read. Quit any other copy of Zap, then choose Try again."
	case e.Relink:
		return lead + " (" + e.Err.Error() + "). Nothing was changed. Choose Try again to link this computer again."
	}
	return lead + " (" + e.Err.Error() + "). Nothing was changed; choose Try again."
}

func (e *migrationError) Unwrap() error { return e.Err }

// goConn is a real account's core: the Conn, with what
// the UI asks of a core besides core.Conn. Voice notes decode with
// gocore/av from the account's folders; a call's video frames are the
// engine's call media's.
type goConn struct {
	*gocore.Conn
	eng   *wa.Engine
	voice av.VoiceDecoder
}

var (
	_ core.Conn         = (*goConn)(nil)
	_ core.CallNumbers  = (*goConn)(nil)
	_ core.Frames       = (*goConn)(nil)
	_ core.VoiceDecoder = (*goConn)(nil)
)

func newGoConn(c *gocore.Conn, eng *wa.Engine) *goConn {
	g := &goConn{Conn: c, eng: eng}
	st, cache := c.Host().AccountDirs()
	g.voice = av.VoiceDecoder{
		Roots: func() []string { return []string{st, cache} },
		Put:   func(pcm []byte) abi.BlobRef { return c.Host().Blobs().Keep(pcm) },
	}
	return g
}

// CallGeneration is a generation no call has had yet (core.CallNumbers),
// from the counter the engine numbers the calls it is offered with.
func (g *goConn) CallGeneration() uint64 { return calls.NextGeneration() }

// Frame and ReleaseFrame are the call's video frames (core.Frames).
func (g *goConn) Frame(stream uint32) (core.Frame, bool) { return g.eng.Frame(stream) }
func (g *goConn) ReleaseFrame(stream uint32)             { g.eng.ReleaseFrame(stream) }

// DecodeVoice decodes a voice note in the account's folders, as zfcore's
// zf_voice_decode does.
func (g *goConn) DecodeVoice(path string) (abi.BlobRef, core.VoiceFormat, error) {
	select {
	case <-g.Done():
		return abi.BlobRef{}, core.VoiceFormat{}, core.ErrClosed
	default:
	}
	return g.voice.DecodeVoice(path)
}
