package sys

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"runtime"
	"strings"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// The run's log is the user's to read and to send with a bug report, so
// what goes into it is redacted on the way in: message text, phone numbers
// and keys never reach the file, whatever a caller or a library put in a
// message or an error.
//
//   - An attribute whose key names content or an identity (text, body,
//     caption, phone, jid, key, token…) is replaced whole.
//   - Everywhere else (the message, string and error values, whatsmeow's
//     formatted lines), a run of 7 or more digits (a phone number, the
//     user part of a JID or a LID) and a number written with a leading +
//     become [number], and a run of 32 or more hex or base64 characters (a
//     key, a token, a digest) becomes [key].
//   - Bytes are logged as their length only.
//
// The backend's lines (gocore) carry source=gocore, and whatsmeow's
// source=whatsmeow.

const redacted = "[redacted]"

// sensitiveKeys are attribute keys whose values are content or identities.
var sensitiveKeys = map[string]bool{
	"text": true, "body": true, "caption": true, "content": true, "draft": true, "message": true,
	"phone": true, "number": true, "jid": true, "lid": true, "chat": true, "sender": true, "peer": true,
	"key": true, "secret": true, "token": true, "password": true, "passphrase": true,
	"pair_code": true, "paircode": true, "qr": true,
}

var (
	// keyLike: 32+ characters of hex or base64 (standard or URL), with
	// padding.
	keyLike = regexp.MustCompile(`[A-Za-z0-9+/_-]{32,}={0,2}`)
	// numberLike: 7+ digits in a row, or a number written with a leading +
	// and separators ("+55 11 99999-9999").
	numberLike = regexp.MustCompile(`\+\d[\d \-().]{5,}\d|\d{7,}`)
)

// Redact returns s with what looks like a key or a phone number replaced.
func Redact(s string) string {
	s = keyLike.ReplaceAllStringFunc(s, func(m string) string {
		// A long word of letters alone (a Go identifier, a path's name) is
		// not a key: keys and tokens mix in digits.
		if strings.IndexFunc(m, func(r rune) bool { return r >= '0' && r <= '9' }) < 0 {
			return m
		}
		return "[key]"
	})
	return numberLike.ReplaceAllString(s, "[number]")
}

// redactAttr is a redacted copy of a.
func redactAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	if sensitiveKeys[strings.ToLower(a.Key)] {
		return slog.String(a.Key, redacted)
	}
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Redact(v.String()))
	case slog.KindGroup:
		attrs := v.Group()
		out := make([]slog.Attr, len(attrs))
		for i, g := range attrs {
			out[i] = redactAttr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	case slog.KindAny:
		switch x := v.Any().(type) {
		case nil:
			return a
		case []byte:
			return slog.String(a.Key, fmt.Sprintf("[%d bytes]", len(x)))
		case error:
			return slog.String(a.Key, Redact(x.Error()))
		default:
			return slog.String(a.Key, Redact(fmt.Sprint(x)))
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}

// redactHandler redacts every record before its handler writes it.
type redactHandler struct{ next slog.Handler }

// RedactHandler wraps next so that every record it writes is redacted (the
// rules above).
func RedactHandler(next slog.Handler) slog.Handler { return &redactHandler{next: next} }

func (h *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, Redact(r.Message), r.PC)
	if fromGoCore(r.PC) {
		out.AddAttrs(slog.String("source", "gocore"))
	}
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return &redactHandler{next: h.next.WithAttrs(out)}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{next: h.next.WithGroup(name)}
}

// fromGoCore reports whether the record at pc was logged by the Go backend.
func fromGoCore(pc uintptr) bool {
	if pc == 0 {
		return false
	}
	f := runtime.FuncForPC(pc)
	return f != nil && strings.HasPrefix(f.Name(), "zapfast/gocore")
}

// WhatsmeowLog is whatsmeow's logger over the default slog logger (the
// run's log once StartLog ran): its warnings and errors only, formatted
// and redacted, with source=whatsmeow and the module. Its debug and info
// lines, which name messages and contacts freely, are dropped.
func WhatsmeowLog() waLog.Logger { return waLogger{module: "whatsmeow"} }

type waLogger struct{ module string }

func (l waLogger) Warnf(msg string, args ...any)  { l.log(slog.LevelWarn, msg, args) }
func (l waLogger) Errorf(msg string, args ...any) { l.log(slog.LevelError, msg, args) }
func (waLogger) Infof(string, ...any)             {}
func (waLogger) Debugf(string, ...any)            {}
func (l waLogger) Sub(module string) waLog.Logger {
	return waLogger{module: l.module + "/" + module}
}

func (l waLogger) log(level slog.Level, msg string, args []any) {
	ctx := context.Background()
	lg := slog.Default()
	if !lg.Enabled(ctx, level) {
		return
	}
	// Redacted here too: the default logger may not be the run's (tests,
	// the demo), and a formatted line is all text.
	lg.Log(ctx, level, Redact(fmt.Sprintf(msg, args...)), "source", "whatsmeow", "module", l.module)
}
