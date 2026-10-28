//go:build gnui_release

package main

import "testing"

func TestReleaseOptions(t *testing.T) {
	for _, args := range [][]string{{"-start-hidden"}, {"--start-hidden"}} {
		o, err := parseOptions(append(args, "reload-themes"))
		if err != nil || len(o.args) != 1 || o.args[0] != "reload-themes" {
			t.Fatalf("%q: %+v, %v", args, o, err)
		}
	}
	for _, args := range [][]string{
		{"-core", "/x/lib.dylib"}, {"-engine=rust"},
		{"-demo"}, {"-demo-page", "chat"}, {"-demo-tour"},
		{"-demo-tour-script", "whats-new"}, {"-demo-shot", "x.png"},
		{"-o", "x.png"}, {"-startup"}, {"-exit-first"},
		{"-idle", "1s"}, {"-bench"}, {"-gate"}, {"-cpuprofile", "x.pprof"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("release accepted %q", args)
		}
	}
}
