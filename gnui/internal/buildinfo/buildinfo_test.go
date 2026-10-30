package buildinfo

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func TestStampsWinOverTheToolchainsRevision(t *testing.T) {
	bi := &debug.BuildInfo{GoVersion: "go1.27.2", Settings: []debug.BuildSetting{
		{Key: "-tags", Value: "gnui_release"},
		{Key: "vcs.revision", Value: "1111111111111111111111111111111111111111"},
		{Key: "vcs.modified", Value: "true"},
	}}
	i := Info{Revision: "4f36cc40d87241ec3db85542e1747d16e46e3139"}
	i.fill(bi)
	if i.Revision != "4f36cc40d87241ec3db85542e1747d16e46e3139" || i.Dirty || i.Go != "go1.27.2" || i.Tags != "gnui_release" {
		t.Fatalf("stamped: %+v", i)
	}
	var plain Info
	plain.fill(bi)
	if plain.Short() != "111111111111-dirty" {
		t.Fatalf("unstamped: %+v, short %q", plain, plain.Short())
	}
	if (Info{}).Short() != "" {
		t.Fatal("an unknown commit has a short form")
	}
}

func TestLogIsOneInfoRecord(t *testing.T) {
	var b bytes.Buffer
	Log(&b, "0.19.0")
	line := b.String()
	if strings.Count(line, "\n") != 1 || !strings.Contains(line, "level=INFO msg=build version=0.19.0 ") ||
		!strings.Contains(line, " framework_pin=") {
		t.Fatalf("log line %q", line)
	}
	Log(nil, "0.19.0") // no log file: nothing, no panic
}

// A build the packaging script signed with the development identity says so,
// and one it signed for release does not.
func TestDevSigned(t *testing.T) {
	was := devSigned
	defer func() { devSigned = was }()
	devSigned = "true"
	if !Read().DevSigned {
		t.Error("a development-signed build reads as a release")
	}
	devSigned = ""
	if Read().DevSigned {
		t.Error("a release reads as development-signed")
	}
}
