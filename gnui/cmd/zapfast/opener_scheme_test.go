package main

import (
	"errors"
	"slices"
	"testing"

	"zapfast/core"
)

// The opener hands on web and mail addresses and files' absolute paths only:
// a file: URL, another scheme's handler (a man page, a VNC client) or a
// relative path is refused before anything runs, even past the test-binary
// guard (fakes stand behind it here: nothing is launched).
func TestTheOpenerRefusesOtherSchemes(t *testing.T) {
	var urls, files []string
	oldURL, oldFile := openURL, openFilePath
	openURL = func(u string) error { urls = append(urls, u); return nil }
	openFilePath = func(p string) error { files = append(files, p); return nil }
	t.Cleanup(func() { openURL, openFilePath = oldURL, oldFile })
	t.Setenv("ZAPFAST_ALLOW_OPEN", "1")

	for _, p := range []string{"file:///tmp/x", "FILE:///Applications/Calculator.app", "x-man-page://ls", "vnc://host",
		"javascript:alert(1)", "Report.pdf", "~/run.command", ""} {
		if err := (opener{}).Open(p); !errors.Is(err, core.ErrOpenRefused) {
			t.Errorf("%q: %v", p, err)
		}
	}
	if len(urls) != 0 || len(files) != 0 {
		t.Fatalf("opened links %q, files %q", urls, files)
	}
	links := []string{"https://example.com/a", "http://example.com", "mailto:ada@example.com", "HTTPS://EXAMPLE.COM"}
	for _, u := range links {
		if err := (opener{}).Open(u); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
	if err := (opener{}).Open("/tmp/Report.pdf"); err != nil {
		t.Errorf("a file: %v", err)
	}
	if !slices.Equal(urls, links) || !slices.Equal(files, []string{"/tmp/Report.pdf"}) {
		t.Errorf("opened links %q, files %q", urls, files)
	}
}
