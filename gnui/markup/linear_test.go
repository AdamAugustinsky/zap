package markup

import (
	"bytes"
	"math/rand/v2"
	"testing"
	"time"
)

// linkScan's test agrees with linkAt itself at every place of every text:
// where it says no address starts, linkAt finds none, and where it says one
// does, linkAt finds it (so plain never scans a token in vain).
func TestLinkScanAgreesWithLinkAt(t *testing.T) {
	rngs := []*rand.Rand{rand.New(rand.NewPCG(1, 2)), rand.New(rand.NewPCG(7, 11)), rand.New(rand.NewPCG(42, 99))}
	parts := []string{"a", "b", "1", "9", ".", "-", "/", "@", ":", "(", ")", "[", "]", "<", " ", ",", ";", "_", "~", "*", "!", "?",
		"com", "io", "x.com", "www.", "http://", "https://", "mailto:", "é", " ", "'", "{", "?q=1", "user", "rs"}
	var b []byte
	for n := 0; n < 600_000; n++ {
		rng := rngs[n%len(rngs)]
		b = b[:0]
		for k := rng.IntN(12) + 1; k > 0; k-- {
			b = append(b, parts[rng.IntN(len(parts))]...)
		}
		var ls linkScan
		for j := range b {
			if c := b[j]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
				continue
			}
			_, _, ok := linkAt(b, j)
			if got := ls.possible(b, j); got != ok {
				t.Fatalf("%q at %d: linkScan says %v, linkAt %v", b, j, got, ok)
			}
		}
	}
}

// A long message with no spaces parses in time proportional to its length:
// the shapes that once cost the square of it (a CSV row, hyphens, paths,
// stray markers, mail-like tokens) each take a few milliseconds at 64 KB,
// where one took 15 s.
func TestALongTokenParsesInLinearTime(t *testing.T) {
	var dst []Span
	for _, unit := range []string{"name,", "12,", "a-", "dir/", "*a ", "a.", "a@", "a(", "a@b(", "x@b.com,", "~a_"} {
		s := bytes.Repeat([]byte(unit), 64<<10/len(unit))
		start := time.Now()
		dst = Parse(dst[:0], s)
		if d := time.Since(start); d > 150*time.Millisecond {
			t.Errorf("%q × %d: parsed in %v", unit, len(s)/len(unit), d)
		}
	}
}
