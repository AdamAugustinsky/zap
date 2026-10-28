package engine

// Ported from whatsapp-rust 5e72baf wacore/src/voip_control/relay_parse.rs
// and spec_build.rs tests (the cases a 1:1 call depends on), MIT.

import (
	"errors"
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"

	"zapfast/gocore/voip/kdf"
)

func te2(ip [4]byte, port uint16, attrs waBinary.Attrs) waBinary.Node {
	return waBinary.Node{Tag: "te2", Attrs: attrs, Content: []byte{ip[0], ip[1], ip[2], ip[3], byte(port >> 8), byte(port)}}
}

func TestTheMediaEndpointPrefersTheWebClientPortThenAUsableCandidate(t *testing.T) {
	n := waBinary.Node{Tag: "relay", Content: []waBinary.Node{
		{Tag: "key", Content: []byte("a2V5a2V5a2V5")},
		{Tag: "warp_mi_tag_len", Content: []byte("4")},
		{Tag: "token", Attrs: waBinary.Attrs{"id": "1"}, Content: []byte("t1")},
		te2([4]byte{1, 1, 1, 1}, 3478, waBinary.Attrs{"relay_id": "1", "relay_name": "a", "token_id": "0", "auth_token_id": "1"}),
		te2([4]byte{2, 2, 2, 2}, 3478, waBinary.Attrs{"relay_id": "2", "relay_name": "b", "token_id": "1", "auth_token_id": "1"}),
		te2([4]byte{3, 3, 3, 3}, 3480, waBinary.Attrs{"relay_id": "3", "relay_name": "c", "token_id": "1", "is_fna": "1"}),
	}}
	r := ParseRelay(FindRelay(&waBinary.Node{Tag: "call", Content: []waBinary.Node{{Tag: "offer", Content: []waBinary.Node{n}}}}))
	if len(r.Tokens) != 2 || len(r.Tokens[0]) != 0 || string(r.Tokens[1]) != "t1" {
		t.Fatalf("tokens %q", r.Tokens)
	}
	ep, ok := r.MediaEndpoint()
	if ip, port, _ := ep.PrimaryIPv4(); !ok || ip != "3.3.3.3" || port != 3480 {
		t.Fatalf("picked %v", ep)
	}
	// Without the 3480 endpoint, the first one's padded-empty token makes
	// it unusable: the second carries the call.
	n.Content = n.Content.([]waBinary.Node)[:5]
	r = ParseRelay(&n)
	cfg, err := NewConfig(Outgoing, "CID", "1:2@lid", "3@lid", make([]byte, 32), r)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RelayIP != "2.2.2.2" || string(cfg.RelayToken) != "t1" || string(cfg.IntegrityKey) != "a2V5a2V5a2V5" || cfg.WarpMITagLen != 4 {
		t.Errorf("config %v", cfg)
	}
	if cfg.SSRC != kdf.ParticipantSSRC("CID", "1:2@lid", 0) || !cfg.SFrame || cfg.Audio != MLow16k60ms {
		t.Errorf("config %v", cfg)
	}
}

func TestABadRelayOrKeyFailsTheConfig(t *testing.T) {
	good := waBinary.Node{Tag: "relay", Content: []waBinary.Node{
		{Tag: "key", Content: []byte("a2V5")},
		{Tag: "token", Content: []byte("t0")},
		te2([4]byte{1, 1, 1, 1}, 3478, waBinary.Attrs{"token_id": "0"}),
	}}
	if _, err := NewConfig(Incoming, "CID", "1@lid", "2@lid", make([]byte, 32), ParseRelay(&good)); err != nil {
		t.Fatal(err)
	}
	var se *SetupError
	if _, err := NewConfig(Incoming, "CID", "1@lid", "2@lid", make([]byte, 16), ParseRelay(&good)); !errors.As(err, &se) {
		t.Errorf("a short key: %v", err)
	}
	noKey := good
	noKey.Content = good.Content.([]waBinary.Node)[1:]
	if _, err := NewConfig(Incoming, "CID", "1@lid", "2@lid", make([]byte, 32), ParseRelay(&noKey)); !errors.As(err, &se) {
		t.Errorf("no <key>: %v", err)
	}
	badTag := good
	badTag.Content = append([]waBinary.Node{{Tag: "warp_mi_tag_len", Content: []byte("21")}}, good.Content.([]waBinary.Node)...)
	if _, err := NewConfig(Incoming, "CID", "1@lid", "2@lid", make([]byte, 32), ParseRelay(&badTag)); !errors.As(err, &se) {
		t.Errorf("a 21-byte tag: %v", err)
	}
	if _, err := New(Config{CallKey: make([]byte, 32), RelayIP: "::1", RelayPort: 1, WarpMITagLen: 4, Audio: MLow16k60ms}, nil); !errors.Is(err, ErrBadEndpoint) {
		t.Errorf("an IPv6 relay: %v", err)
	}
}

func TestPlayoutPrimesThenDrainsAndReprimesOnUnderrun(t *testing.T) {
	p := &pcmState{priming: true, packetSamps: opusFrame60ms, cap: playoutBounds(opusFrame60ms).cap}
	p.jitter = make([]int16, 960)
	for i := range p.jitter {
		p.jitter[i] = 1
	}
	if f := drainPlayout(p); f[0] != 0 || len(p.jitter) != 960 {
		t.Fatal("one frame is below the prime target: silence, nothing drained")
	}
	p.jitter = append(p.jitter, make([]int16, 960)...)
	if f := drainPlayout(p); f[0] != 1 || len(p.jitter) != 1920-playoutDrain {
		t.Fatal("two frames prime it: audio")
	}
	for len(p.jitter) >= playoutDrain {
		drainPlayout(p)
	}
	drainPlayout(p)
	if !p.priming {
		t.Error("an underrun must re-prime")
	}
	if c := effectivePlayoutCap(playoutBounds(1920).cap, 960, 4000); c != playoutBounds(1920).cap-960 {
		t.Errorf("a shrinking packet gives back one packet per packet: %d", c)
	}
}
