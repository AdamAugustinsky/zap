package demo

import (
	"strings"
	"testing"

	"zapfast/abi"
)

// rustTable is zfcore's disposition table, each command's name and what
// zfcore's demo did with it: testdata/disposition.txt, read from
// zfcore/src/demo.rs's disposition (zapfast_core over whatsapp-rust 5e72baf)
// before WP9 removed the Rust core. It cannot be regenerated here.
func rustTable(t *testing.T) map[string]Disposition {
	t.Helper()
	table := map[string]Disposition{}
	for _, line := range readLines(t, "testdata/disposition.txt") {
		name, what, ok := strings.Cut(line, "\t")
		d, known := map[string]Disposition{"Answers": Answers, "Accepts": Accepts, "Refuses": Refuses}[what]
		if !ok || !known {
			t.Fatalf("disposition.txt: %q", line)
		}
		if _, dup := table[name]; dup {
			t.Errorf("%s is in the table twice", name)
		}
		table[name] = d
	}
	return table
}

func allTags() []abi.CommandTag {
	var tags []abi.CommandTag
	for i := range 1 << 16 {
		if abi.CommandTag(i).String() != "unknown" {
			tags = append(tags, abi.CommandTag(i))
		}
	}
	return tags
}

// The demo's table is zfcore's, command for command, and covers every
// command of the schema, as zfcore's exhaustive match does.
func TestTheDispositionTableIsZfcores(t *testing.T) {
	rust := rustTable(t)
	tags := allTags()
	if len(rust) != len(tags) {
		t.Errorf("zfcore classified %d commands, the schema has %d", len(rust), len(tags))
	}
	counts := map[Disposition]int{}
	for _, tag := range tags {
		want, ok := rust[tag.String()]
		if !ok {
			t.Errorf("%v is not in zfcore's table", tag)
			continue
		}
		counts[want]++
		if got := DispositionOf(tag); got != want {
			t.Errorf("%v: %v, zfcore %v", tag, got, want)
		}
	}
	// backend-map.md §3: 44 command variants answer, 8 accept (plus the
	// demo commands, which answer).
	if counts[Answers] != len(answers) || counts[Accepts] != len(accepts) {
		t.Errorf("zfcore answers %d and accepts %d; the demo %d and %d", counts[Answers], counts[Accepts], len(answers), len(accepts))
	}
}
