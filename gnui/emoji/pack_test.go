package emoji

import (
	"bytes"
	"compress/flate"
	"flag"
	"io"
	"os"
	"testing"
)

var updatePacked = flag.Bool("update", false, "rewrite emoji.tsv.z from emoji.tsv")

// The binary embeds emoji.tsv deflated; the two files must agree.
func TestTableIsPacked(t *testing.T) {
	tsv, err := os.ReadFile("emoji.tsv")
	if err != nil {
		t.Fatal(err)
	}
	if *updatePacked {
		var out bytes.Buffer
		w, _ := flate.NewWriter(&out, flate.BestCompression)
		w.Write(tsv)
		w.Close()
		if err := os.WriteFile("emoji.tsv.z", out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	z, err := os.ReadFile("emoji.tsv.z")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(flate.NewReader(bytes.NewReader(z)))
	if err != nil || !bytes.Equal(got, tsv) {
		t.Fatalf("emoji.tsv.z is not emoji.tsv deflated (%v): go test -run TestTableIsPacked -update", err)
	}
}
