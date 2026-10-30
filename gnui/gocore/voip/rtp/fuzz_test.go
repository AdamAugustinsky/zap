package rtp

// Native fuzz targets for the RTP readers a received packet goes through.
//
//	CGO_ENABLED=0 go test ./gocore/voip/rtp -run '^$' -fuzz '^FuzzParse$' -fuzztime 45s

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
	"unsafe"

	pionrtp "github.com/pion/rtp"

	"zapfast/gocore/voip/internal/kat"
)

// fuzzGoldenHex is every hex string under the named golden.json sections:
// the packets, headers and payloads the Rust code wrote.
func fuzzGoldenHex(f *testing.F, sections ...string) [][]byte {
	var out [][]byte
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			if b, err := hex.DecodeString(v); err == nil && len(b) > 0 && len(b) <= 4096 {
				out = append(out, b)
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	g := kat.Golden(f)
	for _, s := range sections {
		walk(g.Get(f, s))
	}
	return out
}

// fuzzOffset is where sub starts in whole, or -1 when it is not inside it.
func fuzzOffset(sub, whole []byte) int {
	if len(whole) == 0 {
		return -1
	}
	s := uintptr(unsafe.Pointer(unsafe.SliceData(sub)))
	w := uintptr(unsafe.Pointer(unsafe.SliceData(whole)))
	if s < w || s+uintptr(len(sub)) > w+uintptr(len(whole)) {
		return -1
	}
	return int(s - w)
}

// FuzzParse runs every reader over one received packet. The header length
// and every view the readers return lie inside the packet; the extension
// block sits inside the header; the payload starts after it; a piggyback
// split never makes bytes up; a parsed header encodes and parses back to
// itself; and wherever pion/rtp reads the fixed header, it agrees on the
// standard fields and the header length.
func FuzzParse(f *testing.F) {
	for _, b := range fuzzGoldenHex(f, "rtp_headers", "audio_pipeline", "video_pipeline") {
		f.Add(b)
	}
	f.Add([]byte{0x90, 0x61, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0xde, 0xbe, 0, 2, 0x31, 0x00, 0x08, 0x71, 0x00, 0x05, 0, 0, 1, 2, 3, 4, 5, 6})
	f.Add([]byte{0xb0, 0x78, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0xbe, 0xde, 0, 1, 0x71, 0x00, 0x02, 0, 9, 9, 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		n, ok := HeaderByteLength(data)
		if ok && !IsVersion2(data) {
			t.Fatalf("a header length for a packet that is not V=2")
		}
		if ok && (n < FixedHeaderLen || n > len(data)) {
			t.Fatalf("header length %d of %d bytes", n, len(data))
		}
		profile, ext, hasExt, eok := ExtensionProfileAndData(data)
		if eok != ok {
			t.Fatalf("ExtensionProfileAndData ok %v, HeaderByteLength ok %v", eok, ok)
		}
		if hasExt {
			// The block follows the fixed header, the CSRCs and its own
			// 4-byte profile and length.
			at := FixedHeaderLen + int(data[0]&0x0f)*4 + 4
			if len(ext) > 0 && fuzzOffset(ext, data) != at || at+len(ext) != n || len(ext)%4 != 0 {
				t.Fatalf("extension block %d+%d outside header of %d", at, len(ext), n)
			}
			if binary.BigEndian.Uint16(data[at-4:]) != profile {
				t.Fatalf("profile %#x", profile)
			}
		}
		if payload, pok := PlainPayload(data); pok {
			if !ok || len(payload) == 0 || fuzzOffset(payload, data) != n {
				t.Fatalf("payload of %d bytes not right after the %d-byte header", len(payload), n)
			}
		}
		if off, pok := ParseAudioPiggybackOffset(data); pok {
			if off < 0 || off > 0xffff || !hasExt {
				t.Fatalf("piggyback offset %d", off)
			}
		}
		if _, iok := ParseMediaFrameInfo(data); iok && (!hasExt || profile != WhatsAppExtensionProfile) {
			t.Fatalf("frame info without a 0xdebe block")
		}
		if ok {
			payload := data[n:]
			media, audio, sok := SplitAudioPiggyback(data, payload)
			if sok {
				off, _ := ParseAudioPiggybackOffset(data)
				if !bytes.Equal(audio, payload[off:]) || len(media) > off || fuzzOffset(media, payload) != 0 {
					t.Fatalf("split at %d: media %d, audio %d of %d", off, len(media), len(audio), len(payload))
				}
			} else if !bytes.Equal(media, payload) || audio != nil {
				t.Fatalf("an unsplit packet changed its payload")
			}
		}

		h, hok := ParseHeader(data)
		if hok != ok {
			t.Fatalf("ParseHeader ok %v, HeaderByteLength ok %v", hok, ok)
		}
		if !hok {
			return
		}
		if h.HasVideo {
			if v, vok := ParseVideoExtension(data); !vok || v != h.Video {
				t.Fatalf("video extension %+v %v", v, vok)
			}
			if info, iok := ParseMediaFrameInfo(data); !iok || info != h.Video.MediaFrameInfo {
				t.Fatalf("frame info %d %v, video extension says %d", info, iok, h.Video.MediaFrameInfo)
			}
		}
		enc := h.Encode()
		if len(enc) != h.ByteSize() {
			t.Fatalf("encoded %d bytes, ByteSize %d", len(enc), h.ByteSize())
		}
		back, bok := ParseHeader(enc)
		if !bok || back != h {
			t.Fatalf("%+v encodes to %x, which parses to %+v", h, enc, back)
		}

		var p pionrtp.Header
		if pn, err := p.Unmarshal(data); err == nil {
			if pn != n || p.Marker != h.Marker || p.PayloadType != h.PayloadType ||
				p.SequenceNumber != h.SequenceNumber || p.Timestamp != h.Timestamp || p.SSRC != h.SSRC {
				t.Fatalf("pion reads %d header bytes and %+v; we read %d and %+v", pn, p, n, h)
			}
		}
	})
}

// FuzzHeaderRoundTrip encodes a header built from arbitrary fields and
// parses it back: the same standard fields and video extension, the length
// ByteSize said, and the frame info readable on its own.
func FuzzHeaderRoundTrip(f *testing.F) {
	f.Add(true, uint8(120), uint16(1), uint32(960), uint32(0x01020304), false, uint32(0), false, uint8(0), uint16(0), int16(0), uint16(0))
	f.Add(false, uint8(97), uint16(65535), uint32(90000), uint32(7), false, uint32(0), true, uint8(8), uint16(500), int16(-3), uint16(9))
	f.Add(false, uint8(120), uint16(3), uint32(1920), uint32(7), true, ExtensionDTXWord, false, uint8(0), uint16(0), int16(0), uint16(0))
	f.Fuzz(func(t *testing.T, marker bool, pt uint8, seq uint16, ts, ssrc uint32, hasWord bool, word uint32,
		hasVideo bool, info uint8, bw uint16, off int16, tseq uint16) {
		h := Header{
			Marker: marker, PayloadType: pt & 0x7f, SequenceNumber: seq, Timestamp: ts, SSRC: ssrc,
			HasExtensionWord: hasWord, ExtensionWord: word, HasVideo: hasVideo,
		}
		if hasVideo {
			h.Video = VideoExtension{MediaFrameInfo: info, InitialBandwidth: bw, ShortOffset: off, TransportSequence: tseq}
		}
		enc := h.Encode()
		if n, ok := HeaderByteLength(enc); !ok || n != h.ByteSize() || n != len(enc) {
			t.Fatalf("%x: header length %d %v, ByteSize %d", enc, n, ok, h.ByteSize())
		}
		back, ok := ParseHeader(enc)
		want := h
		want.HasExtensionWord, want.ExtensionWord = false, 0 // ParseHeader does not decode the word
		if !ok || back != want {
			t.Fatalf("%+v encodes to %x, which parses to %+v", h, enc, back)
		}
		if hasVideo {
			if got, ok := ParseMediaFrameInfo(enc); !ok || got != info {
				t.Fatalf("frame info %d %v, wrote %d", got, ok, info)
			}
		}
		if hasWord && !hasVideo {
			if _, ext, _, _ := ExtensionProfileAndData(enc); binary.BigEndian.Uint32(ext) != word {
				t.Fatalf("extension word %x, wrote %x", ext, word)
			}
		}
	})
}
