package testbin

import "testing"

func TestATestBinaryIsToldFromTheApp(t *testing.T) {
	if !Is() {
		t.Fatal("this test binary was not taken for one")
	}
	for arg0, want := range map[string]bool{
		"/tmp/go-build1/b001/format.test":                  true,
		"format.test":                                      true,
		"/Applications/ZapFast.app/Contents/MacOS/zapfast": false,
		"./zapfast":           false,
		"/tmp/x.test/zapfast": false,
	} {
		if named(arg0) != want {
			t.Errorf("named(%q) = %v, want %v", arg0, !want, want)
		}
	}
}
