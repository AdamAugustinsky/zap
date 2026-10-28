package core

import (
	"errors"
	"fmt"
	"runtime"
	"testing"
)

// The real file opener refuses inside a test binary, so a test that reaches
// it opens nothing; ZAPFAST_ALLOW_OPEN=1 lets it through. open(1) is a fake
// here: nothing is launched either way.
func TestExecShellRefusesInATestBinary(t *testing.T) {
	var ran []string
	old := runOpen
	runOpen = func(args ...string) error { ran = append(ran, fmt.Sprint(args)); return nil }
	t.Cleanup(func() { runOpen = old })

	t.Setenv("ZAPFAST_ALLOW_OPEN", "")
	if err := (ExecShell{}).Open("/tmp/Report.pdf"); !errors.Is(err, ErrOpenRefused) {
		t.Errorf("in a test binary: %v", err)
	}
	if len(ran) != 0 {
		t.Fatalf("open(1) ran: %q", ran)
	}
	if runtime.GOOS != "darwin" {
		return
	}
	t.Setenv("ZAPFAST_ALLOW_OPEN", "1")
	if err := (ExecShell{}).Open("/tmp/Report.pdf"); err != nil {
		t.Errorf("allowed: %v", err)
	}
	if len(ran) != 1 || ran[0] != "[-- /tmp/Report.pdf]" {
		t.Errorf("open(1) ran %q", ran)
	}
}
