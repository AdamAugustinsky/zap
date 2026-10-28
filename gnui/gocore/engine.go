package gocore

import (
	"context"
	"errors"
	"path/filepath"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/blobs"
	"zapfast/gocore/out"
)

// Engine is a backend behind a Conn: the Go demo (gocore/demo) or the
// WhatsApp engine (gocore/wa). The Conn owns the goroutine that calls
// Start, Command and Stop, one call at a time, in the order the UI made
// them; Accept runs on the UI's goroutines. An engine emits through its
// Host from any goroutine.
//
// The interface is frozen: the demo and the WhatsApp engine implement it.
type Engine interface {
	// Name names the engine in the refusal of a command it does not
	// answer: "the <Name> core does not answer <Command>" (ZF_E_STATE),
	// as zfcore words it for "demo".
	Name() string

	// Accept decides whether cmd goes to the engine, on the goroutine
	// that called Send, before any blob cmd names is taken: nil queues it
	// for Command; ErrUnsupported refuses it as a command this engine does
	// not answer (ZF_E_STATE); an error from Refuse refuses it as
	// inconsistent (ZF_E_REFUSED). It runs concurrently with other Sends
	// and with the engine goroutine, so it must not block, and it may read
	// cmd but not keep it. A Table answers the usual case.
	Accept(cmd *abi.Command) error

	// Open is called once, by Open, before anything else: the engine keeps
	// h, and may emit already (the cached chat list before Start) and
	// start goroutines with h.Go. An error fails Open.
	Open(h *Host) error

	// Start is the startup permit (Conn.Start: migrations, then the
	// connection), on the engine goroutine, in order with the commands.
	Start()

	// Command handles one accepted command on the engine goroutine, in
	// the order Send queued them. The engine owns cmd and its byte fields.
	// It should return soon: slow work (an upload, a history sync, a Pick)
	// goes to a goroutine of h.Go so later commands are not held up.
	Command(cmd *Command)

	// Stop is called once, on the engine goroutine, when Close begins,
	// after the last Command (queued commands not handled yet are
	// dropped); h's context is already cancelled. Close then waits, up to
	// its timeout, for Stop and every goroutine of h.Go to return.
	Stop()
}

// Command is one accepted command, for Engine.Command.
type Command struct {
	// Command is a copy decoded from the Conn's own encoding: the engine
	// may keep it and its byte fields.
	abi.Command
	// Blob is the bytes of the blob the command names, taken from the
	// table by Send (SendImage.RGBA, SendVoice.Samples, SetGroupPicture's
	// JPEG, ApplyPictureEdit's pasted RGBA); nil when it names none. The
	// engine owns it.
	Blob []byte
}

// ErrUnsupported is what Accept returns for a command the engine does not
// answer: Send refuses it with ZF_E_STATE, as the demo core refuses one.
var ErrUnsupported = errors.New("gocore: the engine does not answer this command")

// Refuse is what Accept returns for a command whose fields are
// inconsistent: Send refuses it with ZF_E_REFUSED and msg.
func Refuse(msg string) error {
	return &core.Error{Op: "zf_send", Code: core.ERefused, Msg: msg}
}

// Table is a set of command tags, for an Accept that answers by tag.
type Table map[abi.CommandTag]bool

// Answers returns the table of tags.
func Answers(tags ...abi.CommandTag) Table {
	t := make(Table, len(tags))
	for _, tag := range tags {
		t[tag] = true
	}
	return t
}

// Accept is nil for a tag in the table, ErrUnsupported for the rest.
func (t Table) Accept(cmd *abi.Command) error {
	if t[cmd.Tag] {
		return nil
	}
	return ErrUnsupported
}

// Host is what a Conn gives its engine. Its methods are safe from any
// goroutine.
type Host struct{ c *Conn }

// Config is the configuration the Conn was opened with, resolved: Account
// is set, and for a real core so are StateDir, CacheDir and
// KeyringService.
func (h *Host) Config() core.Config { return h.c.cfg }

// AccountDirs are the account's own state and cache folders, under
// accounts/<Account> in the roots (as upstream's AppDirs.account lays
// them out). They are not created. Empty for the demo.
func (h *Host) AccountDirs() (state, cache string) {
	cfg := h.c.cfg
	if cfg.Demo {
		return "", ""
	}
	return filepath.Join(cfg.StateDir, "accounts", cfg.Account), filepath.Join(cfg.CacheDir, "accounts", cfg.Account)
}

// Out emits events: once the Conn is closed, nothing more is queued.
func (h *Host) Out() *out.Out { return h.c.out }

// Blobs is the Conn's blob table: event blobs go in through Out's
// Records; the engine may also read the caller's.
func (h *Host) Blobs() *blobs.Table { return h.c.blobs }

// Context is cancelled when Close begins.
func (h *Host) Context() context.Context { return h.c.ctx }

// Go runs f on a goroutine of its own, with Context, and reports whether
// it did: not once Close has begun. Close waits for it. A panic in f
// becomes a Fatal event naming thread, and poisons the Conn.
func (h *Host) Go(thread string, f func(ctx context.Context)) bool {
	c := h.c
	c.goMu.Lock()
	if c.goClosed {
		c.goMu.Unlock()
		return false
	}
	c.wg.Add(1)
	c.goMu.Unlock()
	go func() {
		defer c.wg.Done()
		c.guard("engine", thread, func() { f(c.ctx) })
	}()
	return true
}

// Pick asks the UI for files with a PickRequest (an id is given to req)
// and waits for its PickAnswer: the paths chosen, nil when the reader
// cancels, when ctx ends, or when the Conn closes. Text over its bound is
// cut, filters past the bounds are left off and a directory too long to
// carry is dropped, never the request. It blocks: call it from a goroutine
// of Go, not the engine goroutine's Command, unless holding later commands
// up is meant.
func (h *Host) Pick(ctx context.Context, req *abi.EventPickRequest) []string {
	return h.c.picks.ask(ctx, h.c, req)
}
