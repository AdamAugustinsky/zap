package procinfo

import (
	"runtime"
	"testing"
	"time"
)

func TestAgeIsPlausible(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	a := AgeNs()
	time.Sleep(5 * time.Millisecond)
	b := AgeNs()
	if a <= 0 || b-a < int64(4*time.Millisecond) || b > int64(time.Hour) {
		t.Fatalf("ages %d then %d", a, b)
	}
}
