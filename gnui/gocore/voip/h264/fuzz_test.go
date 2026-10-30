package h264

// Native fuzz targets for H.264 reassembly from received RTP payloads, the
// packetizer read back by it, and the Annex-B access-unit splitter.
//
//	CGO_ENABLED=0 go test ./gocore/voip/h264 -run '^$' -fuzz '^FuzzDepacketizer$' -fuzztime 45s

import (
	"bytes"
	"encoding/hex"
	"slices"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

// fuzzGoldenAUs are the access units and payloads in golden.json's h264
// section.
func fuzzGoldenAUs(f *testing.F) (aus [][]byte, payloads [][]byte) {
	for _, v := range kat.Golden(f).List(f, "h264") {
		aus = append(aus, v.Hex(f, "au"))
		for _, p := range v.List(f, "payloads") {
			b, _ := hex.DecodeString(p.Get(f).(string))
			payloads = append(payloads, b)
		}
	}
	return aus, payloads
}

type fuzzAU struct {
	ts uint32
	au []byte
}

// fuzzPushAll feeds a script of packets to a fresh depacketizer: each is a
// sequence step, a timestamp step, a flags byte (bit 0: marker) and a
// length, then the payload. It returns every AU that came out.
func fuzzPushAll(t *testing.T, script []byte) []fuzzAU {
	var d Depacketizer
	var out []fuzzAU
	var seq uint16
	var ts uint32
	collect := func(ts uint32, au []byte, ok bool) {
		for ok {
			if len(au) == 0 || len(au) > MaxAUBytes || !bytes.HasPrefix(au, startCode[:]) {
				t.Fatalf("an AU of %d bytes starting %x", len(au), au[:min(len(au), 4)])
			}
			for nal := range SplitAnnexB(au) {
				if len(nal) == 0 {
					t.Fatalf("an empty NAL in a returned AU")
				}
			}
			out = append(out, fuzzAU{ts, append([]byte(nil), au...)})
			ts, au, ok = d.PopReady()
		}
	}
	for len(script) >= 4 {
		seq += uint16(script[0])
		ts += uint32(int32(int8(script[1]))) * 3000
		marker := script[2]&1 != 0
		n := min(int(script[3])*int(1+script[2]>>4), len(script)-4)
		payload := script[4 : 4+n]
		script = script[4+n:]
		collect(d.Push(seq, ts, payload, marker))
		if len(d.ready) > maxReadyAUs || len(d.auBuf) > MaxAUBytes || len(d.fuBuf) > MaxAUBytes {
			t.Fatalf("backlog %d, AU buffer %d, FU buffer %d", len(d.ready), len(d.auBuf), len(d.fuBuf))
		}
	}
	return out
}

// FuzzDepacketizer reassembles an arbitrary run of received payloads with
// arbitrary sequence numbers, timestamps and markers. Every AU returned is
// non-empty Annex-B within MaxAUBytes made of non-empty NALs; the backlog
// and both buffers stay inside their caps; and a second depacketizer fed
// the same run returns the same AUs.
func FuzzDepacketizer(f *testing.F) {
	_, payloads := fuzzGoldenAUs(f)
	var script []byte
	for i, p := range payloads {
		if len(p) > 255 {
			continue
		}
		var flags byte
		if i%3 == 2 {
			flags = 1
		}
		script = append(script, 1, 0, flags, byte(len(p)))
		script = append(script, p...)
	}
	f.Add(script)
	f.Add([]byte{1, 0, 0, 3, 0x7c, 0x85, 0xaa, 1, 0, 1, 3, 0x7c, 0x45, 0xbb})
	f.Add([]byte{1, 0, 1, 7, 0x78, 0, 2, 0x67, 0x42, 0, 1, 0x68})
	f.Fuzz(func(t *testing.T, script []byte) {
		a, b := fuzzPushAll(t, script), fuzzPushAll(t, script)
		if len(a) != len(b) {
			t.Fatalf("%d AUs, then %d", len(a), len(b))
		}
		for i := range a {
			if a[i].ts != b[i].ts || !bytes.Equal(a[i].au, b[i].au) {
				t.Fatalf("AU %d differs between two runs", i)
			}
		}
	})
}

// FuzzPacketizeRoundTrip packetizes an access unit and reassembles it.
// Every payload fits SingleNALMax, and the AU that comes back is the input's
// NALs (less AUDs and SEI before the first slice) behind 4-byte start
// codes, with nothing else returned. The check is made when every NAL type
// is one an encoder emits (1..23): 0 and 24..31 are not H.264 NAL types an
// encoder writes, and a bare one is read back as an RTP aggregate.
func FuzzPacketizeRoundTrip(f *testing.F) {
	aus, _ := fuzzGoldenAUs(f)
	for _, au := range aus {
		f.Add(au, false)
	}
	f.Add(append([]byte{0, 0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x06, 5, 0, 0, 1, 0x65}, bytes.Repeat([]byte{0x88}, 1700)...), true)
	f.Fuzz(func(t *testing.T, au []byte, keepSEI bool) {
		var p PacketizedAU
		if keepSEI {
			PacketizeKeepSEI(au, &p)
		} else {
			Packetize(au, &p)
		}
		var want []byte
		seenVCL, checkable := false, true
		for nal := range SplitAnnexB(au) {
			ty := NALUnitType(nal)
			if ty == 0 || ty >= 24 {
				checkable = false
			}
			if ty == nalAUD || (!keepSEI && !seenVCL && ty == nalSEI) {
				continue
			}
			seenVCL = seenVCL || (ty >= 1 && ty <= 5)
			want = append(append(want, startCode[:]...), nal...)
		}
		var d Depacketizer
		var got [][]byte
		for i := range p.Len() {
			payload := p.At(i)
			if len(payload) == 0 || len(payload) > SingleNALMax {
				t.Fatalf("payload %d of %d bytes", i, len(payload))
			}
			if _, a, ok := d.Push(uint16(100+i), 9000, payload, i == p.Len()-1); ok {
				got = append(got, a)
			}
		}
		if !checkable || len(want) > MaxAUBytes {
			return
		}
		switch {
		case len(want) == 0 && len(got) != 0:
			t.Fatalf("an AU with nothing to send came back as %d AUs", len(got))
		case len(want) > 0 && (len(got) != 1 || !bytes.Equal(got[0], want)):
			t.Fatalf("%d payloads reassembled into %d AUs, want the %d-byte AU", p.Len(), len(got), len(want))
		}
	})
}

// FuzzAnnexBAUSplitter splits a byte stream into access units, once in one
// push and once in the chunks the second input cuts. Nothing is lost or
// added: the AUs and the flushed tail concatenate back to the stream. Every
// AU is non-empty, each after the first starts at a start code, and the
// chunking does not change the AUs.
func FuzzAnnexBAUSplitter(f *testing.F) {
	aus, _ := fuzzGoldenAUs(f)
	f.Add(bytes.Join(aus, nil), []byte{1, 7, 30})
	f.Add([]byte{0, 0, 0, 1, 0x67, 1, 0, 0, 1, 0x68, 2, 0, 0, 1, 0x65, 0x88, 0, 0, 0, 1, 0x06, 9, 0, 0, 1, 0x67, 1, 0, 0, 1, 0x41, 0x9a}, []byte{3, 3, 3})
	f.Add([]byte{0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x41, 0x80, 0, 0, 1, 0x09, 0xf0, 0, 0, 1, 0x41, 0x80}, []byte{1})
	f.Fuzz(func(t *testing.T, stream, cuts []byte) {
		split := func(chunks [][]byte) [][]byte {
			var s AnnexBAUSplitter
			var out [][]byte
			for _, c := range chunks {
				for _, au := range s.Push(c) {
					out = append(out, append([]byte(nil), au...))
				}
			}
			if tail, ok := s.Finish(); ok {
				out = append(out, tail)
			}
			return out
		}
		whole := split([][]byte{stream})
		if !bytes.Equal(bytes.Join(whole, nil), stream) {
			t.Fatalf("the AUs do not add up to the stream")
		}
		for i, au := range whole {
			if len(au) == 0 {
				t.Fatalf("AU %d is empty", i)
			}
			if i > 0 && !bytes.HasPrefix(au, []byte{0, 0, 1}) && !bytes.HasPrefix(au, startCode[:]) {
				t.Fatalf("AU %d starts %x, not at a start code", i, au[:min(len(au), 4)])
			}
		}
		var chunks [][]byte
		rest := stream
		for _, c := range cuts {
			n := min(int(c), len(rest))
			chunks = append(chunks, rest[:n])
			rest = rest[n:]
		}
		chunks = append(chunks, rest)
		if chunked := split(chunks); !slices.EqualFunc(whole, chunked, bytes.Equal) {
			t.Fatalf("pushed whole: %d AUs; pushed in %d chunks: %d AUs", len(whole), len(chunks), len(chunked))
		}
	})
}
