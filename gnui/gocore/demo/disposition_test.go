package demo

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"zapfast/abi"
)

// rustTable is zfcore's disposition table, read from demo.rs: each arm's
// command names and what it does with them.
func rustTable(t *testing.T) map[string]Disposition {
	t.Helper()
	src, err := os.ReadFile("../../../zfcore/src/demo.rs")
	if err != nil {
		t.Skipf("no zfcore source: %v", err)
	}
	s := string(src)
	start := strings.Index(s, "pub fn disposition(")
	if start < 0 {
		t.Fatal("demo.rs has no disposition")
	}
	body := s[start:]
	body = body[:strings.Index(body, "\n}\n")]
	// Comments name no command.
	body = regexp.MustCompile(`//[^\n]*`).ReplaceAllString(body, "")
	arm := regexp.MustCompile(`(?s)((?:\|?\s*C::\w+(?:\s*\{\s*\.\.\s*\})?\s*)+)=>\s*(Answers|Accepts|Refuses)`)
	name := regexp.MustCompile(`C::(\w+)`)
	table := map[string]Disposition{}
	for _, m := range arm.FindAllStringSubmatch(body, -1) {
		d := map[string]Disposition{"Answers": Answers, "Accepts": Accepts, "Refuses": Refuses}[m[2]]
		for _, n := range name.FindAllStringSubmatch(m[1], -1) {
			if _, dup := table[n[1]]; dup {
				t.Errorf("%s is in two arms", n[1])
			}
			table[n[1]] = d
		}
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
		t.Errorf("demo.rs classifies %d commands, the schema has %d", len(rust), len(tags))
	}
	counts := map[Disposition]int{}
	for _, tag := range tags {
		want, ok := rust[tag.String()]
		if !ok {
			t.Errorf("%v is not in demo.rs's table", tag)
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
