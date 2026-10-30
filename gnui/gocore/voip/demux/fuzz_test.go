package demux

// Native fuzz target for the first reader every relay packet meets.
//
//	CGO_ENABLED=0 go test ./gocore/voip/demux -run '^$' -fuzz '^FuzzUnwrapGroupForwarding$' -fuzztime 45s

import (
	"bytes"
	"errors"
	"testing"
	"unsafe"

	"zapfast/gocore/voip/rtcp"
	"zapfast/gocore/voip/rtp"
)

// FuzzUnwrapGroupForwarding unwraps and classifies an arbitrary packet. A
// packet not starting 0x09 passes through whole; an unwrapped one is the
// tail of the input after a header of the length its version names, and is
// RTP or RTCP; any failure is ErrGroupForwarding with nothing passed on.
// Classify agrees with the STUN, RTCP and RTP predicates, and wrapping a
// packet again unwraps to the same payload.
func FuzzUnwrapGroupForwarding(f *testing.F) {
	rtpPacket := rtp.Header{PayloadType: 120, SequenceNumber: 1, SSRC: 2}.Encode()
	sr := rtcp.SenderReport(1, rtcp.SenderStats{}, 0)
	for _, v := range []byte{2, 4, 7} {
		hdr := map[byte]int{2: 8, 4: 12, 7: 18}[v]
		f.Add(append(append([]byte{0x09, v}, make([]byte, hdr-2)...), rtpPacket...))
		f.Add(append(append([]byte{0x09, v}, make([]byte, hdr-2)...), sr[:]...))
	}
	f.Add(rtpPacket)
	f.Add([]byte{0x00, 0x01, 0, 0, 0x21, 0x12, 0xa4, 0x42})
	f.Add([]byte{0x09, 0x05})
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := UnwrapGroupForwarding(data)
		if err != nil {
			if !errors.Is(err, ErrGroupForwarding) || p.Payload != nil || p.GroupForwarded {
				t.Fatalf("error %v with %+v", err, p)
			}
			if len(data) == 0 || data[0] != 0x09 {
				t.Fatalf("refused a packet that is not forwarded")
			}
		} else if !p.GroupForwarded {
			if p.HeaderLen != 0 || len(p.Payload) != len(data) ||
				(len(data) > 0 && unsafe.SliceData(p.Payload) != unsafe.SliceData(data)) {
				t.Fatalf("a plain packet did not pass through whole")
			}
		} else {
			want := map[byte]int{2: 8, 4: 12, 7: 18}[data[1]]
			if data[0] != 0x09 || p.HeaderLen != want || !bytes.Equal(p.Payload, data[want:]) ||
				&p.Payload[0] != &data[want] {
				t.Fatalf("unwrapped %d bytes after a %d-byte header of version %d", len(p.Payload), p.HeaderLen, data[1])
			}
			if k := Classify(p.Payload); k != KindRtp && k != KindRtcp {
				t.Fatalf("a forwarded payload classifies as %v", k)
			}
			rewrapped := append(append([]byte(nil), data[:want]...), p.Payload...)
			if q, err := UnwrapGroupForwarding(rewrapped); err != nil || !bytes.Equal(q.Payload, p.Payload) {
				t.Fatalf("rewrapping: %v", err)
			}
		}

		// Each class is what its bytes say, and the RTP/RTCP split follows
		// RFC 5761's payload-type ranges rather than byte 0.
		switch Classify(data) {
		case KindStun:
			if len(data) < 2 || data[0]&0xc0 != 0 {
				t.Fatalf("%x is not STUN", data)
			}
		case KindRtcp:
			if len(data) < rtcp.HeaderLen || data[0]>>6 != 2 || data[1] < 192 || data[1] > 223 {
				t.Fatalf("%x is not RTCP", data)
			}
		case KindRtp:
			if len(data) < rtp.FixedHeaderLen || data[0]>>6 != 2 || (data[1] >= 192 && data[1] <= 223) {
				t.Fatalf("%x is not RTP", data)
			}
		default:
			if len(data) >= rtp.FixedHeaderLen && data[0]>>6 == 2 || len(data) >= 2 && data[0]&0xc0 == 0 {
				t.Fatalf("%x left unclassified", data)
			}
		}
	})
}
