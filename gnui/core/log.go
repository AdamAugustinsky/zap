package core

import (
	"log/slog"
	"os"
	"unsafe"

	"gonativeui/clib"
)

// coreLevel is zf_set_log's level for an slog level: a record at level or
// above is logged.
func coreLevel(level slog.Level) int32 {
	switch {
	case level < slog.LevelDebug:
		return 5 // trace
	case level < slog.LevelInfo:
		return 4
	case level < slog.LevelWarn:
		return 3
	case level < slog.LevelError:
		return 2
	}
	return 1
}

// SetLog sends the core's log (zf_set_log) to f, which the app's own log
// writes to: the core keeps a duplicate of its descriptor, so their lines
// share one offset, and writes one line a record as slog's text handler
// does (time in UTC, level, source=core, target, msg), from level up. A nil
// f stops it. It is the process's, for every core of every library: call
// it once the log is open, before or after Open, and again to move it. The
// WhatsApp library's lines are reduced to fixed categories, and other
// crates log warnings and errors only. Against a library built before
// zf_set_log it returns ErrUnsupported.
func (l *Lib) SetLog(f *os.File, level slog.Level) error {
	if l.fn.setLog == (clib.Func{}) {
		return ErrUnsupported
	}
	s := &struct {
		call *clib.Call
		err  zfErr
	}{call: clib.NewCall()}
	do := func(fd int32) int32 {
		return s.call.Reset().Int32(fd).Int32(coreLevel(level)).Ptr(unsafe.Pointer(&s.err)).Do(l.fn.setLog).Int32()
	}
	var st int32
	if f == nil {
		st = do(-1)
	} else {
		rc, err := f.SyscallConn()
		if err != nil {
			return err
		}
		// Inside Control, so the descriptor stays open while the core
		// duplicates it.
		if err := rc.Control(func(fd uintptr) { st = do(int32(fd)) }); err != nil {
			return err
		}
	}
	if st != StatusOK {
		return s.err.error("zf_set_log", st)
	}
	return nil
}
