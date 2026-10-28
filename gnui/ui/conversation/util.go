package conversation

import (
	"hash/maphash"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
)

// seed hashes message ids into the transcript's item ids.
var seed = maphash.MakeSeed()

// itemID is the VList id of a message id. NoID is reserved.
func itemID(msg []byte) uint64 {
	h := maphash.Bytes(seed, msg)
	if h == base.NoID {
		h--
	}
	return h
}

// idTable maps item ids to model indexes: open addressing over a reused
// slice, pointer-free and allocation-free once grown.
type idTable struct {
	keys []uint64
	vals []int32
	used []bool
	n    int
}

func (t *idTable) reset(n int) {
	size := 64
	for size < n*2 {
		size <<= 1
	}
	if cap(t.keys) < size {
		t.keys, t.vals, t.used = make([]uint64, size), make([]int32, size), make([]bool, size)
	}
	t.keys, t.vals, t.used = t.keys[:size], t.vals[:size], t.used[:size]
	clear(t.used)
	t.n = 0
}

// put records id at index v; it reports false when id is there already.
func (t *idTable) put(id uint64, v int32) bool {
	mask := uint64(len(t.keys) - 1)
	for i := (id * 0x9E3779B97F4A7C15) & mask; ; i = (i + 1) & mask {
		if !t.used[i] {
			t.used[i], t.keys[i], t.vals[i] = true, id, v
			t.n++
			return true
		}
		if t.keys[i] == id {
			return false
		}
	}
}

// get is the index of id, -1 when it is not there.
func (t *idTable) get(id uint64) int32 {
	if len(t.keys) == 0 {
		return -1
	}
	mask := uint64(len(t.keys) - 1)
	for i := (id * 0x9E3779B97F4A7C15) & mask; ; i = (i + 1) & mask {
		if !t.used[i] {
			return -1
		}
		if t.keys[i] == id {
			return t.vals[i]
		}
	}
}

// fill appends tmpl to f with each placeholder pairs[2i] replaced by
// pairs[2i+1] (upstream's chained str::replace, for translated strings).
func fill(f *ui.Fmt, tmpl string, pairs ...string) *ui.Fmt {
	for i := 0; i < len(tmpl); {
		hit := false
		for p := 0; p+1 < len(pairs); p += 2 {
			k := pairs[p]
			if len(k) > 0 && len(tmpl)-i >= len(k) && tmpl[i:i+len(k)] == k {
				f.Str(pairs[p+1])
				i += len(k)
				hit = true
				break
			}
		}
		if !hit {
			f.Byte(tmpl[i])
			i++
		}
	}
	return f
}

// itoa is a small integer as a string without allocating (the frame's
// arena).
func itoa(c *ui.Context, n int) string { return c.Fmt().Int(n).String() }

// Times, from package format (zf:src/util.rs), on the system's 12- or
// 24-hour clock (format.Hour12).

func local(ts int64) time.Time { return time.Unix(ts, 0).Local() }

// dayOf is the local calendar day of ts, as y*10000+m*100+d.
func dayOf(ts int64) int32 {
	y, m, d := local(ts).Date()
	return int32(y*10000 + int(m)*100 + d)
}

// fmtBuf is where a time or a number is formatted before it is copied into
// a frame's Fmt. Frames are built on one goroutine.
var fmtBuf []byte

// clock appends the time of day: "14:05", or "2:05 PM" on a 12-hour clock.
func clock(f *ui.Fmt, ts int64) *ui.Fmt {
	fmtBuf = format.AppendClock(fmtBuf[:0], local(ts))
	return f.Str(model.S(fmtBuf))
}

// copyStamp appends WhatsApp's transcript stamp: "22:41, 8/18/2026".
func copyStamp(dst []byte, ts int64) []byte { return format.AppendCopyStamp(dst, local(ts)) }

// dayLabel appends the day separator's label: Today, Yesterday, a weekday
// within the week, else "Wednesday, 23 September 2026".
func dayLabel(f *ui.Fmt, ts int64, now time.Time) *ui.Fmt {
	fmtBuf = format.AppendDayLabel(fmtBuf[:0], i18n.Current(), time.Unix(ts, 0), now.Local())
	return f.Str(model.S(fmtBuf))
}

// chatStamp appends a search hit's stamp: the time today, Yesterday, a
// weekday this week, else "23 Sep 2026".
func chatStamp(f *ui.Fmt, ts int64, now time.Time) *ui.Fmt {
	fmtBuf = format.AppendChatStamp(fmtBuf[:0], i18n.Current(), time.Unix(ts, 0), now.Local())
	return f.Str(model.S(fmtBuf))
}

// lastSeen appends the presence line: "last seen today at 14:05" and its kin.
func lastSeen(f *ui.Fmt, ts int64, now time.Time) *ui.Fmt {
	fmtBuf = format.AppendLastSeen(fmtBuf[:0], i18n.Current(), time.Unix(ts, 0), now.Local())
	return f.Str(model.S(fmtBuf))
}

func indexOf(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

// fillInt appends tmpl to f with key replaced by n.
func fillInt(f *ui.Fmt, tmpl, key string, n int) *ui.Fmt {
	for len(tmpl) > 0 {
		i := index(tmpl, key)
		if i < 0 {
			f.Str(tmpl)
			break
		}
		f.Str(tmpl[:i]).Int(n)
		tmpl = tmpl[i+len(key):]
	}
	return f
}

// fillBytes appends tmpl to f with key replaced by b.
func fillBytes(f *ui.Fmt, tmpl, key string, b []byte) *ui.Fmt {
	for len(tmpl) > 0 {
		i := index(tmpl, key)
		if i < 0 {
			f.Str(tmpl)
			break
		}
		f.Str(tmpl[:i]).Str(model.S(b))
		tmpl = tmpl[i+len(key):]
	}
	return f
}

func index(s, sub string) int {
	if len(sub) == 0 {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// duration appends "0:12".
func duration(dst []byte, seconds uint32) []byte { return format.AppendDuration(dst, seconds) }
