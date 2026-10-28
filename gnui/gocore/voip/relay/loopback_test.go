// Loopback of the whole media transport: two relay channels dialled to a
// fake relay on 127.0.0.1, which forwards every message of one to the other,
// carrying E2E SRTP-protected RTP (and SRTCP) both ways. Everything is
// in-process and synthetic.

package relay

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"zapfast/gocore/voip/demux"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/srtp"
)

const (
	lidA     = "111111111111111:0@lid"
	lidB     = "222222222222222@lid" // bare: formats to :0@lid
	ssrcA    = 0x5741_0001
	ssrcB    = 0x5741_0002
	tagLen   = 4
	nPackets = 50
)

// forward pipes every message one relay side receives to the other.
func forward(from, to *relaySide) {
	for p := range from.packets {
		_ = to.st.write(p)
	}
}

// peer is one end of the call: its relay channel and its SRTP state.
type peer struct {
	name     string
	ch       *Channel
	stream   *rtp.Stream
	sendROC  srtp.SendROC
	sendKeys srtp.Keys
	recvKeys srtp.Keys
	recv     srtp.RecvStreams
	srtcp    srtp.Keys
	peerRTCP srtp.Keys
	ssrc     uint32
}

func newPeer(t *testing.T, name string, ch *Channel, callKey []byte, self, other string, ssrc uint32) *peer {
	t.Helper()
	send, ok := srtp.DeriveE2EKeys(callKey, kdf.FormatParticipantID(self))
	if !ok {
		t.Fatal("send keys")
	}
	recv, ok := srtp.DeriveE2EKeys(callKey, kdf.FormatParticipantID(other))
	if !ok {
		t.Fatal("recv keys")
	}
	srtcpSelf, ok := srtp.DeriveSRTCPKeys(callKey, kdf.FormatParticipantID(self))
	if !ok {
		t.Fatal("srtcp keys")
	}
	srtcpPeer, ok := srtp.DeriveSRTCPKeys(callKey, kdf.FormatParticipantID(other))
	if !ok {
		t.Fatal("srtcp keys")
	}
	return &peer{
		name: name, ch: ch, ssrc: ssrc,
		stream:   rtp.NewStream(ssrc, 960, false),
		sendKeys: send, recvKeys: recv, srtcp: srtcpSelf, peerRTCP: srtcpPeer,
	}
}

func payloadFor(name string, i int) []byte {
	// Opus-sized speech-like payloads, distinct per sender and packet.
	p := bytes.Repeat([]byte(fmt.Sprintf("%s-%03d|", name, i)), 8)
	p[0] = 0x48
	return p
}

func (p *peer) sendAudio(t *testing.T, i int) {
	t.Helper()
	payload := payloadFor(p.name, i)
	h := p.stream.NextPacket(payload, false)
	roc := p.sendROC.Advance(h.SequenceNumber)
	packet := srtp.ProtectRTP(&p.sendKeys, h, roc, tagLen, payload)
	if bytes.Contains(packet, payload[1:]) {
		t.Fatal("the payload went out in the clear")
	}
	if err := p.ch.Send(context.Background(), packet); err != nil {
		t.Fatalf("%s send: %v", p.name, err)
	}
}

// receive reads until it has n RTP packets from the other peer and one
// SRTCP report, checking each decrypts to what was sent.
func (p *peer) receive(from *peer, n int) error {
	deadline := time.After(10 * time.Second)
	got, rtcp := 0, false
	seen := map[int]bool{}
	for got < n || !rtcp {
		select {
		case ev, ok := <-p.ch.Events():
			if !ok {
				return fmt.Errorf("%s: event stream ended after %d packets", p.name, got)
			}
			if ev.Kind != EventPacket {
				continue
			}
			if demux.Classify(ev.Packet) == demux.KindRtcp {
				plain, index, ok := srtp.UnprotectSRTCP(&p.peerRTCP, from.ssrc, ev.Packet)
				if !ok || index != 1 || !bytes.Equal(plain, compact(from.ssrc, p.ssrc)) {
					return fmt.Errorf("%s: SRTCP from %s did not authenticate (index %d)", p.name, from.name, index)
				}
				rtcp = true
				continue
			}
			h, plain, err := p.recv.Unprotect(&p.recvKeys, tagLen, ev.Packet)
			if err != nil {
				return fmt.Errorf("%s: unprotect packet %d from %s: %v", p.name, got, from.name, err)
			}
			// The channel is unordered, so packets are matched by the index
			// they carry rather than by arrival order.
			idx, err := strconv.Atoi(string(plain[2:min(5, len(plain))]))
			if err != nil || h.SSRC != from.ssrc || !bytes.Equal(plain, payloadFor(from.name, idx)) || seen[idx] {
				return fmt.Errorf("%s: packet from %s decrypted to %q (ssrc %#x)", p.name, from.name, plain, h.SSRC)
			}
			seen[idx] = true
			got++
		case <-deadline:
			return fmt.Errorf("%s: received %d of %d packets (rtcp %v)", p.name, got, n, rtcp)
		}
	}
	return nil
}

// compact is WhatsApp's 12-byte compact RTCP report (PT 208).
func compact(local, remote uint32) []byte {
	return []byte{0x81, 208, 0, 2,
		byte(local >> 24), byte(local >> 16), byte(local >> 8), byte(local),
		byte(remote >> 24), byte(remote >> 16), byte(remote >> 8), byte(remote)}
}

func TestLoopbackRelayCarriesSRTPBothWays(t *testing.T) {
	relay := newFakeRelay(t)
	chA := dialTest(t, relay.addr())
	sideA := relay.next(t)
	chB := dialTest(t, relay.addr())
	sideB := relay.next(t)
	go forward(sideA, sideB)
	go forward(sideB, sideA)
	for _, ch := range []*Channel{chA, chB} {
		if ev := <-ch.Events(); ev.Kind != EventConnected {
			t.Fatalf("first event = %v", ev)
		}
	}

	callKey := make([]byte, 32)
	for i := range callKey {
		callKey[i] = byte(i)
	}
	a := newPeer(t, "A", chA, callKey, lidA, lidB, ssrcA)
	b := newPeer(t, "B", chB, callKey, lidB, lidA, ssrcB)

	// Send keys of one side are the receive keys of the other, and the two
	// directions are keyed apart.
	if a.sendKeys != b.recvKeys || b.sendKeys != a.recvKeys || a.sendKeys == b.sendKeys {
		t.Fatal("E2E key directions do not pair up")
	}

	done := make(chan error, 1)
	go func() { done <- b.receive(a, nPackets) }()
	for i := range nPackets {
		a.sendAudio(t, i)
		b.sendAudio(t, i)
		// Paced like audio, a little faster: the channel never retransmits,
		// so a burst far past the SCTP window could legitimately lose some.
		time.Sleep(2 * time.Millisecond)
	}
	for _, p := range []struct{ from, to *peer }{{a, b}, {b, a}} {
		report := srtp.ProtectSRTCP(&p.from.srtcp, p.from.ssrc, 1, compact(p.from.ssrc, p.to.ssrc))
		if err := p.from.ch.Send(context.Background(), report); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.receive(b, nPackets); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// A forged packet from the relay does not authenticate.
	h := a.stream.NextPacket(payloadFor("A", 999), false)
	forged := srtp.ProtectRTP(&a.sendKeys, h, a.sendROC.Advance(h.SequenceNumber), tagLen, payloadFor("A", 999))
	forged[len(forged)-1] ^= 1
	if _, _, err := b.recv.Unprotect(&b.recvKeys, tagLen, forged); err == nil {
		t.Fatal("a tampered packet must not authenticate")
	}

	_ = chA.Close()
	_ = chB.Close()
}
