//go:build gnui_release

package main

import "testing"

func TestReleaseOptions(t *testing.T) {
	for _, args := range [][]string{
		{"-core", "/x/lib.dylib"}, {"--core", "/x/lib.dylib"},
		{"-core=/x/lib.dylib"}, {"--core=/x/lib.dylib"},
	} {
		o, err := parseOptions(append(args, "--start-hidden", "reload-themes"))
		if err != nil || o.core != "/x/lib.dylib" || len(o.args) != 1 || o.args[0] != "reload-themes" {
			t.Fatalf("%q: %+v, %v", args, o, err)
		}
	}
	for _, args := range [][]string{
		{"-demo"}, {"-demo-page", "chat"}, {"-demo-tour"},
		{"-demo-tour-script", "whats-new"}, {"-demo-shot", "x.png"},
		{"-o", "x.png"}, {"-startup"}, {"-exit-first"},
		{"-idle", "1s"}, {"-bench"}, {"-gate"}, {"-cpuprofile", "x.pprof"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("release accepted development flags %q", args)
		}
	}
}
