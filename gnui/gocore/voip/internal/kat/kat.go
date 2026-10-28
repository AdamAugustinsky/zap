// Package kat loads the VoIP test vectors for the gocore/voip tests:
// testdata/kats.json, copied verbatim from whatsapp-rust 5e72baf
// wacore/src/voip/testdata (MIT), and testdata/golden.json, written by the
// real Rust code through zapfast_core's zfcalls/tests/voip_golden.rs. Both
// are committed data: zfcalls went with the Rust tree in WP9, so golden.json
// can no longer be regenerated here.
package kat

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Dir is gocore/voip/testdata.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata")
}

// Doc is a parsed JSON vector file, addressed by key paths.
type Doc struct{ v any }

func load(t testing.TB, name string) Doc {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(Dir(), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return Doc{v}
}

// KATs is kats.json.
func KATs(t testing.TB) Doc { return load(t, "kats.json") }

// Golden is golden.json.
func Golden(t testing.TB) Doc { return load(t, "golden.json") }

// Get walks the key path and returns the raw value.
func (d Doc) Get(t testing.TB, path ...string) any {
	t.Helper()
	cur := d.v
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("kat path %v: %q is not an object", path, k)
		}
		cur, ok = m[k]
		if !ok {
			t.Fatalf("kat path %v: missing %q", path, k)
		}
	}
	return cur
}

// Sub returns the document at path.
func (d Doc) Sub(t testing.TB, path ...string) Doc { return Doc{d.Get(t, path...)} }

// Str is the string at path.
func (d Doc) Str(t testing.TB, path ...string) string {
	t.Helper()
	s, ok := d.Get(t, path...).(string)
	if !ok {
		t.Fatalf("kat path %v is not a string", path)
	}
	return s
}

// Hex hex-decodes the string at path.
func (d Doc) Hex(t testing.TB, path ...string) []byte {
	t.Helper()
	b, err := hex.DecodeString(d.Str(t, path...))
	if err != nil {
		t.Fatalf("kat path %v: %v", path, err)
	}
	return b
}

// Uint is the number at path.
func (d Doc) Uint(t testing.TB, path ...string) uint64 {
	t.Helper()
	f, ok := d.Get(t, path...).(float64)
	if !ok {
		t.Fatalf("kat path %v is not a number", path)
	}
	return uint64(f)
}

// List is the array at path.
func (d Doc) List(t testing.TB, path ...string) []Doc {
	t.Helper()
	a, ok := d.Get(t, path...).([]any)
	if !ok {
		t.Fatalf("kat path %v is not an array", path)
	}
	out := make([]Doc, len(a))
	for i, v := range a {
		out[i] = Doc{v}
	}
	return out
}
