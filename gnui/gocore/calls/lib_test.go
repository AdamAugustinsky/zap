package calls

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
)

// The binding against the real libzfcalls, offline: no device is opened
// (Silent) and the relay is a local port nothing answers on. Skipped when
// the library is not built; CARGO_TARGET_DIR (or $ZAPFAST_CALLS) finds a
// build outside the repository's target/.

func realLib(t *testing.T) *Lib {
	t.Helper()
	l, err := LoadLib()
	if errors.Is(err, ErrNoLibrary) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func unreachableRelay(t *testing.T) []byte {
	t.Helper()
	sock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := sock.LocalAddr().(*net.UDPAddr).Port
	sock.Close()
	addr := binary.BigEndian.AppendUint16([]byte{127, 0, 0, 1}, uint16(port))
	r := relayNode()
	r.GetChildren()[2].Content = addr
	packed, err := waBinary.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return packed
}

func TestTheLibraryRunsACallsMediaThroughTheBinding(t *testing.T) {
	l := realLib(t)
	m, err := l.New()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Free()
	p := OpenParams{CallID: "00112233445566778899aabbccddeeff", Generation: 9, Outgoing: true,
		SelfLID: ownLID.String(), PeerLID: peerLID.String(), CallKey: callKey, Relay: unreachableRelay(t), MLow: true, Silent: true}
	if err := m.Open(p); err != nil {
		t.Fatal(err)
	}
	var e *Error
	if err := m.Open(p); !errors.As(err, &e) || e.Code != -10 {
		t.Fatalf("a second open: %v", err)
	}
	if err := m.Command(9, muteCommand(true)); err != nil {
		t.Fatal(err)
	}
	if err := m.Command(8, muteCommand(true)); !errors.As(err, &e) || e.Code != -8 {
		t.Fatalf("another generation's command: %v", err)
	}
	if _, ok := m.Frame(1); ok {
		t.Fatal("a frame before any video")
	}
	var kinds []string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && (len(kinds) == 0 || kinds[len(kinds)-1] != MediaEnded) {
		ev, ok, err := m.Next(200 * time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			if ev.Generation != 9 {
				t.Fatalf("event %+v", ev)
			}
			kinds = append(kinds, ev.Kind)
		}
	}
	if len(kinds) != 2 || kinds[0] != MediaFailed || kinds[1] != MediaEnded {
		t.Fatalf("events %q", kinds)
	}
	// The call is over: another may open.
	p.Generation = 10
	if err := m.Close(9); err != nil {
		t.Fatal(err)
	}
	if err := m.Open(p); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(10); err != nil {
		t.Fatal(err)
	}
}

func TestTheLibraryRefusesBadParameters(t *testing.T) {
	l := realLib(t)
	m, err := l.New()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Free()
	var e *Error
	err = m.Open(OpenParams{CallID: "x", Generation: 1, CallKey: callKey, Relay: []byte{0, 0xf8, 0x01, 0x03}, Silent: true})
	if !errors.As(err, &e) || e.Code != -13 {
		t.Fatalf("no relay: %v", err)
	}
	t.Log(err)
}
