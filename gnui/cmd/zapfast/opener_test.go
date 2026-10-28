package main

import (
	"errors"
	"testing"

	"zapfast/core"
)

// The app's opener, the only real one, refuses inside a test binary before
// it reaches the browser, open(1) or Finder; ZAPFAST_ALLOW_OPEN=1 lets a
// link and a reveal through (to fakes here: nothing is launched).
func TestTheOpenerRefusesInATestBinary(t *testing.T) {
	var urls []string
	var shown [][]string
	oldURL, oldReveal := openURL, reveal
	openURL = func(u string) error { urls = append(urls, u); return nil }
	reveal = func(p []string) error { shown = append(shown, p); return nil }
	t.Cleanup(func() { openURL, reveal = oldURL, oldReveal })

	t.Setenv("ZAPFAST_ALLOW_OPEN", "")
	for _, p := range []string{"https://example.com", "/tmp/Report.pdf"} {
		if err := (opener{}).Open(p); !errors.Is(err, core.ErrOpenRefused) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if err := (opener{}).Reveal([]string{"/tmp/run.command"}); !errors.Is(err, core.ErrOpenRefused) {
		t.Errorf("reveal: %v", err)
	}
	if len(urls) != 0 || len(shown) != 0 {
		t.Fatalf("opened %q, revealed %q", urls, shown)
	}
	t.Setenv("ZAPFAST_ALLOW_OPEN", "1")
	if err := (opener{}).Open("https://example.com"); err != nil || len(urls) != 1 || urls[0] != "https://example.com" {
		t.Errorf("allowed: %v, opened %q", err, urls)
	}
	if err := (opener{}).Reveal([]string{"/tmp/run.command"}); err != nil || len(shown) != 1 || shown[0][0] != "/tmp/run.command" {
		t.Errorf("allowed: %v, revealed %q", err, shown)
	}
}
