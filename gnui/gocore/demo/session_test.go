package demo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
)

// A session is one demo core and the events it queued, each as a
// normalised tree (rec): its tags and fields by name, a union's live variant
// only, an absent optional left out, an event blob as its length and hash.
// Batch ids, call generations and timestamps are relative to the session, so
// two runs' records compare equal when they say the same (show writes one as
// a line of a golden file).

type conn interface {
	core.Conn
	core.Frames
}

type session struct {
	t    *testing.T
	name string
	c    conn
	wake chan struct{}
	base int64 // Unix seconds at open

	events []rec
	raw    []abi.EventTag
	match  func(*abi.Event) bool
	hit    bool

	batches map[uint32]int
	gens    map[uint64]int
}

type rec = map[string]any

func newSession(t *testing.T, name string, open func(core.Config) (conn, error)) *session {
	t.Helper()
	s := &session{t: t, name: name, wake: make(chan struct{}, 1), batches: map[uint32]int{}, gens: map[uint64]int{}}
	s.base = time.Now().Unix()
	c, err := open(core.Config{Demo: true, Wake: func() {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.c = c
	t.Cleanup(func() { c.Close(5 * time.Second) })
	return s
}

func openGo(cfg core.Config) (conn, error) { return Open(cfg) }

func (s *session) Event(e *abi.Event) {
	s.raw = append(s.raw, e.Tag)
	s.events = append(s.events, s.norm(reflect.ValueOf(e).Elem(), "").(rec))
	if s.match != nil && s.match(e) {
		s.hit = true
	}
}

func (s *session) send(cmd *abi.Command) {
	s.t.Helper()
	if err := s.c.Send(cmd); err != nil {
		s.t.Fatalf("%s: send %v: %v", s.name, cmd.Tag, err)
	}
}

// until drains until an event satisfies match.
func (s *session) until(what string, match func(*abi.Event) bool) {
	s.t.Helper()
	s.match, s.hit = match, false
	deadline := time.After(10 * time.Second)
	for {
		if _, err := s.c.Drain(s, 0); err != nil {
			s.t.Fatalf("%s: drain: %v", s.name, err)
		}
		if s.hit {
			s.match = nil
			return
		}
		select {
		case <-s.wake:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			s.t.Fatalf("%s: no %s within 10 s; saw %v", s.name, what, s.raw)
		}
	}
}

// settle drains for d more.
func (s *session) settle(d time.Duration) {
	s.t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if _, err := s.c.Drain(s, 0); err != nil {
			s.t.Fatalf("%s: drain: %v", s.name, err)
		}
	}
}

// count is how many events of tag have come so far.
func (s *session) count(tag abi.EventTag) int {
	n := 0
	for _, t := range s.raw {
		if t == tag {
			n++
		}
	}
	return n
}

// The fields compared relative to the session, and within two seconds.
var seconds = map[string]bool{"Timestamp": true, "LastActivity": true, "DeliveredAt": true, "ReadAt": true, "PlayedAt": true}

var (
	sentVoiceName = regexp.MustCompile(`sent-\d+-\d+\.ogg$`)
	callID        = regexp.MustCompile(`^(zapfast-call-demo-(?:busy-)?)(\d+)$`)
)

func (s *session) gen(g uint64) string {
	n, ok := s.gens[g]
	if !ok {
		n = len(s.gens) + 1
		s.gens[g] = n
	}
	return "gen" + strconv.Itoa(n)
}

func (s *session) norm(v reflect.Value, field string) any {
	switch v.Type() {
	case reflect.TypeFor[abi.BlobRef]():
		ref := v.Interface().(abi.BlobRef)
		b := make([]byte, ref.Len)
		n, err := s.c.ReadBlob(ref.ID, 0, b)
		if err != nil || uint64(n) != ref.Len {
			return fmt.Sprintf("blob %d: %v (%d of %d)", ref.ID, err, n, ref.Len)
		}
		h := sha256.Sum256(b)
		return fmt.Sprintf("blob %d bytes %s", ref.Len, hex.EncodeToString(h[:6]))
	case reflect.TypeFor[[]byte]():
		str := string(v.Bytes())
		str = sentVoiceName.ReplaceAllString(str, "sent-N.ogg")
		if m := callID.FindStringSubmatch(str); m != nil {
			g, _ := strconv.ParseUint(m[2], 10, 64)
			str = m[1] + s.gen(g)
		}
		return str
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		r := rec{}
		variant := ""
		if f := v.FieldByName("Tag"); f.IsValid() {
			variant = fmt.Sprint(f.Interface())
			r["Tag"] = variant
		}
		for i := range t.NumField() {
			name := t.Field(i).Name
			if name == "Tag" || strings.HasPrefix(name, "Has") && v.FieldByName(strings.TrimPrefix(name, "Has")).IsValid() {
				continue
			}
			if has := v.FieldByName("Has" + name); has.IsValid() && !has.Bool() {
				continue
			}
			if variant != "" && t.Field(i).Type.Kind() == reflect.Struct && t.Field(i).Type != reflect.TypeFor[abi.BlobRef]() && name != variant {
				// Another variant's fields.
				if _, isVariant := t.FieldByName(name); isVariant && strings.HasPrefix(t.Field(i).Type.Name(), t.Name()) {
					continue
				}
			}
			r[name] = s.norm(v.Field(i), name)
		}
		return r
	case reflect.Slice:
		l := make([]any, v.Len())
		for i := range l {
			l[i] = s.norm(v.Index(i), field)
		}
		return l
	case reflect.Uint32:
		if field == "Batch" {
			b := uint32(v.Uint())
			n, ok := s.batches[b]
			if !ok {
				n = len(s.batches) + 1
				s.batches[b] = n
			}
			return "batch" + strconv.Itoa(n)
		}
		return v.Uint()
	case reflect.Uint64:
		switch field {
		case "Generation":
			return s.gen(v.Uint())
		case "ActiveForMS":
			return "set"
		}
		return v.Uint()
	case reflect.Int64:
		switch {
		case seconds[field]:
			return relative(v.Int() - s.base)
		case field == "PinnedAt" && v.Int() != 0:
			return relative(v.Int()/1000 - s.base)
		}
		return v.Int()
	case reflect.Uint8, reflect.Uint16:
		if st, ok := v.Interface().(fmt.Stringer); ok {
			return st.String()
		}
		return v.Uint()
	}
	return v.Interface()
}

// relative is a time in seconds from the session's start: show writes it
// t+N, and goldenLine compares it within two seconds.
type relative int64

func show(v any) string {
	switch x := v.(type) {
	case rec:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(k + ":" + show(x[k]))
		}
		return b.String() + "}"
	case []any:
		parts := make([]string, len(x))
		for i := range x {
			parts[i] = show(x[i])
		}
		return "[" + strings.Join(parts, " ") + "]"
	case string:
		return strconv.Quote(x)
	case relative:
		return fmt.Sprintf("t%+d", int64(x))
	}
	return fmt.Sprint(v)
}
