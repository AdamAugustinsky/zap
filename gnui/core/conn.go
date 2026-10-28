package core

import (
	"time"

	"zapfast/abi"
)

// Config opens one account's core.
type Config struct {
	// Demo opens the demo core: a synthetic account, no network, no disk,
	// no keyring. Tests use nothing else.
	Demo bool
	// StateDir and CacheDir are the app's roots; empty: Dirs (the app's
	// own). The demo ignores them.
	StateDir, CacheDir string
	// Account is the account's folder name, digits; empty: "1".
	Account string
	// KeyringService names the OS keyring items of the archive keys; empty:
	// KeyringService. The core refuses upstream ZapFast's. The demo
	// ignores it.
	KeyringService string
	// Wake is called from the core's goroutines when events wait (and by
	// OpenAsync once the core is open): platform.Wake in an app. It must not
	// block. Nil: the caller polls Drain.
	Wake func()
}

// Conn is what the UI uses of one account's core: gocore.Conn (the demo's
// or WhatsApp's), or fake.Core (package core/fake) in tests.
type Conn interface {
	Start() error
	Send(cmd *abi.Command) error
	Drain(h Handler, budget time.Duration) (int, error)
	ReadBlob(id, off uint64, dst []byte) (int, error)
	PutBlob(src []byte) (abi.BlobRef, error)
	FreeBlob(id uint64)
	Close(timeout time.Duration) error
}

// Handler receives drained events, in order, on the goroutine that called
// Drain. The event and its byte fields are valid only during the call.
// Event may Send, but must not call Drain or Close.
type Handler interface {
	Event(e *abi.Event)
}

// Stats counts what Drain did.
type Stats struct {
	Events    uint64 // records decoded and handed out
	Malformed uint64 // records skipped because they did not decode
	Drains    uint64 // drains that returned records
}
