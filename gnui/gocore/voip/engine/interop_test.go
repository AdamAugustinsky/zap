package engine

// Interop: a Go engine and whatsapp-rust's engine (Zap's libzfcalls, loaded
// with purego) on one call, over a fake relay on 127.0.0.1 that speaks the
// real relay stack (UDP, DTLS, SCTP, the pre-negotiated data channel). The
// Rust side runs without devices ("silent": exact-zero frames, which MLow
// sends as comfort noise). Skipped when the library is not built; set
// ZAPFAST_CALLS to its path, or CARGO_TARGET_DIR to the target holding a
// debug or core-release build.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/pion/datachannel"
	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	"github.com/pion/logging"
	"github.com/pion/sctp"
	waBinary "go.mau.fi/whatsmeow/binary"

	"gonativeui/clib"

	"zapfast/gocore/voip/relay"
	"zapfast/gocore/voip/stun"
)

// --- libzfcalls through purego (test only) -----------------------------------

type rustLib struct {
	newCtx, free, open, command, close, next clib.Func
}

func loadRust(t *testing.T) *rustLib {
	t.Helper()
	var candidates []string
	if p := os.Getenv("ZAPFAST_CALLS"); p != "" {
		candidates = append(candidates, p)
	}
	var roots []string
	if d := os.Getenv("CARGO_TARGET_DIR"); d != "" {
		roots = append(roots, d)
	}
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
			roots = append(roots, filepath.Join(dir, "target"))
		}
	}
	for _, r := range roots {
		for _, profile := range []string{"debug", "core-release", "release"} {
			candidates = append(candidates, filepath.Join(r, profile, "libzfcalls.dylib"))
		}
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			continue
		}
		cl, err := clib.Open(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		l := &rustLib{}
		var abi clib.Func
		for _, f := range []struct {
			dst  *clib.Func
			name string
		}{{&abi, "zc_abi_version"}, {&l.newCtx, "zc_new"}, {&l.free, "zc_free"}, {&l.open, "zc_open"},
			{&l.command, "zc_command"}, {&l.close, "zc_close"}, {&l.next, "zc_next_event"}} {
			if *f.dst, err = cl.Func(f.name); err != nil {
				t.Fatalf("%s: %v", p, err)
			}
		}
		if v := clib.NewCall().Do(abi).Uint32(); v != 1 {
			t.Fatalf("%s has ZC_ABI %d", p, v)
		}
		t.Logf("whatsapp-rust's engine from %s", p)
		return l
	}
	t.Skip("libzfcalls.dylib is not built (cargo build -p zfcalls; or set ZAPFAST_CALLS)")
	return nil
}

type zcErr struct {
	code int32
	n    uint32
	msg  [256]byte
}

func (e *zcErr) String() string { return string(e.msg[:min(int(e.n), len(e.msg))]) }

// rustCall is one context of the library with its call open.
type rustCall struct {
	l   *rustLib
	ctx unsafe.Pointer
	mu  sync.Mutex
	evs []rustEvent
	run sync.WaitGroup
	end chan struct{}
}

type rustEvent struct {
	Generation uint64 `json:"generation"`
	Kind       string `json:"kind"`
	Reason     string `json:"reason"`
}

func openRust(t *testing.T, l *rustLib, params map[string]any) *rustCall {
	t.Helper()
	var e zcErr
	ctx := clib.NewCall().Ptr(unsafe.Pointer(&e)).Do(l.newCtx).Ptr()
	if ctx == nil {
		t.Fatalf("zc_new: %s", e.String())
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if st := clib.NewCall().Ptr(ctx).Ptr(unsafe.Pointer(&data[0])).Uint(uint64(len(data))).Ptr(unsafe.Pointer(&e)).Do(l.open).Int32(); st != 0 {
		clib.NewCall().Ptr(ctx).Do(l.free)
		t.Fatalf("zc_open: %d %s", st, e.String())
	}
	r := &rustCall{l: l, ctx: ctx, end: make(chan struct{})}
	r.run.Add(1)
	go func() {
		defer r.run.Done()
		buf := make([]byte, 4096)
		var ne zcErr
		c := clib.NewCall()
		for {
			n := c.Reset().Ptr(ctx).Uint32(200).Ptr(unsafe.Pointer(&buf[0])).Uint(uint64(len(buf))).Ptr(unsafe.Pointer(&ne)).Do(l.next).Int()
			if n <= 0 {
				continue
			}
			var ev rustEvent
			if json.Unmarshal(buf[:n], &ev) == nil {
				r.mu.Lock()
				r.evs = append(r.evs, ev)
				r.mu.Unlock()
				if ev.Kind == "ended" {
					close(r.end)
					return
				}
			}
		}
	}()
	t.Cleanup(func() { r.closeAndFree(t, params["generation"].(uint64)) })
	return r
}

func (r *rustCall) command(t *testing.T, generation uint64, cmd map[string]any) {
	t.Helper()
	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	var e zcErr
	if st := clib.NewCall().Ptr(r.ctx).Uint(generation).Ptr(unsafe.Pointer(&data[0])).Uint(uint64(len(data))).Ptr(unsafe.Pointer(&e)).Do(r.l.command).Int32(); st != 0 {
		t.Fatalf("zc_command: %d %s", st, e.String())
	}
}

func (r *rustCall) events() []rustEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]rustEvent(nil), r.evs...)
}

func (r *rustCall) has(kind, reason string) bool {
	for _, ev := range r.events() {
		if ev.Kind == kind && (reason == "" || ev.Reason == reason) {
			return true
		}
	}
	return false
}

func (r *rustCall) closeAndFree(t *testing.T, generation uint64) {
	if r.ctx == nil {
		return
	}
	var e zcErr
	clib.NewCall().Ptr(r.ctx).Uint(generation).Ptr(unsafe.Pointer(&e)).Do(r.l.close)
	select {
	case <-r.end:
	case <-time.After(10 * time.Second):
		t.Error("whatsapp-rust's call did not end")
		return // leak the context rather than free it under a running poll
	}
	r.run.Wait()
	clib.NewCall().Ptr(r.ctx).Do(r.l.free)
	r.ctx = nil
}

// --- a relay on the real stack -------------------------------------------------

// quietPion keeps pion's logs to errors.
var quietPion = func() logging.LoggerFactory {
	f := logging.NewDefaultLoggerFactory()
	f.DefaultLogLevel = logging.LogLevelError
	return f
}()

// udpRelay accepts relay channels on 127.0.0.1 as a WhatsApp relay would
// (DTLS server, SCTP server, the pre-negotiated stream 0), answers the
// allocate and the ping, and forwards every other packet to the other
// channels.
type udpRelay struct {
	ln       net.Listener
	mu       sync.Mutex
	ends     []*datachannel.DataChannel
	allocs   int
	forwards map[int]int
}

func newUDPRelay(t *testing.T) *udpRelay {
	t.Helper()
	cert, err := selfsign.GenerateSelfSignedWithDNS("wa-voip")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := dtls.Listen("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, &dtls.Config{
		Certificates: []tls.Certificate{cert}, InsecureSkipVerify: true, LoggerFactory: quietPion,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := &udpRelay{ln: ln, forwards: map[int]int{}}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go r.serve(conn.(*dtls.Conn))
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return r
}

func (r *udpRelay) addr() netip.AddrPort { return r.ln.Addr().(*net.UDPAddr).AddrPort() }

func (r *udpRelay) serve(conn *dtls.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return
	}
	assoc, err := sctp.Server(sctp.Config{NetConn: conn, MTU: 1191, MaxMessageSize: 65536, MaxReceiveBufferSize: 1 << 20, LoggerFactory: quietPion})
	if err != nil {
		_ = conn.Close()
		return
	}
	defer assoc.Close()
	stream, err := assoc.OpenStream(relay.MediaStreamID, sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		return
	}
	stream.SetReliabilityParams(true, sctp.ReliabilityTypeRexmit, relay.MediaMaxRetransmits)
	dc, err := datachannel.Client(stream, &datachannel.Config{
		ChannelType: relay.MediaChannelType, Negotiated: true,
		ReliabilityParameter: relay.MediaMaxRetransmits, Label: relay.DataChannelLabel, LoggerFactory: quietPion,
	})
	if err != nil {
		return
	}
	r.mu.Lock()
	index := len(r.ends)
	r.ends = append(r.ends, dc)
	r.mu.Unlock()
	buf := make([]byte, 65536)
	for {
		n, _, err := dc.ReadDataChannel(buf)
		if err != nil {
			return
		}
		p := buf[:n]
		if typ, ok := stun.MessageType(p); ok && stun.IsSTUN(p) {
			id, _ := stun.TransactionID(p)
			var tx [12]byte
			copy(tx[:], id)
			switch typ {
			case stun.MsgAllocateRequest:
				r.mu.Lock()
				r.allocs++
				r.mu.Unlock()
				_, _ = dc.WriteDataChannel(stun.EncodeRequest(stun.MsgAllocateSuccess, &tx, nil, nil, false), false)
			case stun.MsgWhatsAppPing:
				_, _ = dc.WriteDataChannel(stun.EncodeRequest(stun.MsgWhatsAppPong, &tx, nil, nil, false), false)
			}
			continue
		}
		r.mu.Lock()
		r.forwards[index]++
		others := make([]*datachannel.DataChannel, 0, len(r.ends))
		for i, o := range r.ends {
			if i != index {
				others = append(others, o)
			}
		}
		r.mu.Unlock()
		for _, o := range others {
			_, _ = o.WriteDataChannel(append([]byte(nil), p...), false)
		}
	}
}

// --- the call -------------------------------------------------------------------

// interopCall is one call between a Go engine and whatsapp-rust's engine
// over the relay, run for d: the Go side calls (lidA) when goCalls, else
// whatsapp-rust does. goKey is the Go side's callKey.
func interopCall(t *testing.T, l *rustLib, goCalls bool, goKey []byte, d time.Duration) (*side, *rustCall, *udpRelay) {
	t.Helper()
	r := newUDPRelay(t)
	node := relayNode(r.addr())
	packed, err := waBinary.Marshal(waBinary.Node{Tag: "offer", Content: []waBinary.Node{node}})
	if err != nil {
		t.Fatal(err)
	}
	goSelf, rustSelf := lidA, lidB
	if !goCalls {
		goSelf, rustSelf = lidB, lidA
	}
	const generation = uint64(21)
	rust := openRust(t, l, map[string]any{
		"call_id": testCallID, "generation": generation, "outgoing": !goCalls,
		"self_lid": rustSelf, "peer_lid": goSelf, "call_key": testCallKey, "relay": packed,
		"mlow": true, "video": false, "silent": true,
	})
	s := &side{ev: newEvents(), frames: &frameSink{}, audio: newFakeAudio(toneFreqs)}
	c, err := Open(Params{
		CallID: testCallID, Generation: 7, Outgoing: goCalls, SelfLID: goSelf, PeerLID: rustSelf,
		CallKey: goKey, Relay: ParseRelay(&node), MLow: true,
	}, Devices{
		Dial: func(ctx context.Context, addr netip.AddrPort) (relay.Transport, error) {
			return relay.NewFactory(addr).Connect(ctx)
		},
		OpenAudio: func(string, string) (Audio, error) { return s.audio, nil },
		Frames:    s.frames,
	}, s.ev.add)
	if err != nil {
		t.Fatal(err)
	}
	s.call = c
	t.Cleanup(func() {
		c.Close()
		<-c.Done()
		s.audio.Close()
	})
	s.ev.wait(t, CallConnected, 15*time.Second)
	// The callee answered: the caller keys its receive side to that device.
	if goCalls {
		c.Command(Command{Op: OpRekey, LID: rustSelf})
	} else {
		rust.command(t, generation, map[string]any{"op": "rekey", "answering_lid": goSelf})
	}
	time.Sleep(d)
	return s, rust, r
}

// checkInterop holds a call with the right key to its evidence: both ends
// connected, the relay carried both directions, the Go side authenticated
// every packet, and whatsapp-rust reported no silence (it does, with
// authentication_failing, when the key is wrong).
func checkInterop(t *testing.T, s *side, rust *rustCall, r *udpRelay) {
	t.Helper()
	if !rust.has("connected", "") {
		t.Fatalf("whatsapp-rust's side did not connect: %+v", rust.events())
	}
	if rust.has("silent", "") || rust.has("failed", "") || rust.has("closed", "") {
		t.Fatalf("whatsapp-rust's side reports %+v", rust.events())
	}
	st := s.call.Stats()
	r.mu.Lock()
	allocs, fwd := r.allocs, map[int]int{0: r.forwards[0], 1: r.forwards[1]}
	r.mu.Unlock()
	t.Logf("relay: %d allocates, forwarded %v; Go: sent %d RTP, received %d, SRTP failures %d, MLow SID/inactive %d; rust events %+v",
		allocs, fwd, st.RTPSent, st.RTPReceived, st.SRTPUnprotectFailed, st.MLowInactiveOrSID, rust.events())
	if allocs < 2 || fwd[0] < 30 || fwd[1] < 30 {
		t.Fatalf("the relay saw %d allocates and forwarded %v", allocs, fwd)
	}
	if st.RTPReceived < 30 || st.SRTPUnprotectFailed != 0 || st.SFrameDecryptFailed != 0 {
		t.Fatalf("the Go side's reception: %+v", st)
	}
	if s.ev.has(CallSilent) || s.ev.has(CallFailed) || s.ev.has(CallClosed) {
		t.Fatalf("the Go side reports %+v", s.ev.snapshot())
	}
}

func TestInteropAGoCallerAndWhatsappRustsCalleeAuthenticateEachOther(t *testing.T) {
	if testing.Short() {
		t.Skip("a 6 s call")
	}
	s, rust, r := interopCall(t, loadRust(t), true, testCallKey, 6*time.Second)
	checkInterop(t, s, rust, r)
}

func TestInteropWhatsappRustsCallerAndAGoCalleeAuthenticateEachOther(t *testing.T) {
	if testing.Short() {
		t.Skip("a 6 s call")
	}
	s, rust, r := interopCall(t, loadRust(t), false, testCallKey, 6*time.Second)
	checkInterop(t, s, rust, r)
}

func TestInteropWhatsappRustRejectsAGoCallerWithTheWrongKey(t *testing.T) {
	if testing.Short() {
		t.Skip("a 6 s call")
	}
	wrong := append([]byte(nil), testCallKey...)
	wrong[0] ^= 0xff
	s, rust, _ := interopCall(t, loadRust(t), true, wrong, 6*time.Second)
	if !rust.has("silent", "authentication_failing") {
		t.Fatalf("whatsapp-rust's side took a wrong key's audio: %+v", rust.events())
	}
	if st := s.call.Stats(); st.SRTPUnprotectFailed == 0 {
		t.Fatalf("the Go side took a wrong key's audio: %+v", st)
	}
}
