package stun

// Native fuzz targets for the STUN readers a relay response goes through,
// and the request encoder read back.
//
//	CGO_ENABLED=0 go test ./gocore/voip/stun -run '^$' -fuzz '^FuzzParse$' -fuzztime 45s

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"testing"
	"unsafe"

	"zapfast/gocore/voip/internal/kat"
)

// fuzzGoldenHex is every hex string under the named golden.json sections.
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

func fuzzOffset(sub, whole []byte) int {
	s := uintptr(unsafe.Pointer(unsafe.SliceData(sub)))
	w := uintptr(unsafe.Pointer(unsafe.SliceData(whole)))
	if len(whole) == 0 || s < w || s+uintptr(len(sub)) > w+uintptr(len(whole)) {
		return -1
	}
	return int(s - w)
}

// FuzzParse runs the response readers over arbitrary bytes. Attributes lie
// after the header, in order, each value inside the packet; there are no
// more of them than 4-byte headers fit; an error code is only read from a
// complete error response; and the classifiers agree with each other.
func FuzzParse(f *testing.F) {
	for _, b := range fuzzGoldenHex(f, "stun") {
		f.Add(b)
	}
	errResp := EncodeRequest(MsgAllocateError, &[12]byte{1}, appendAttr(nil, attrErrorCode, []byte{0, 0, 4, 1}), nil, false)
	f.Add(errResp)
	pong := WhatsAppPing(&[12]byte{9})
	pong[1] = 0x02
	f.Add(pong[:])
	f.Fuzz(func(t *testing.T, data []byte) {
		attrs := ParseAttributes(data)
		if len(data) >= 20 && len(attrs) > (len(data)-20)/4 {
			t.Fatalf("%d attributes in %d bytes", len(attrs), len(data))
		}
		next := 20
		for i, a := range attrs {
			at := next + 4
			if len(a.Value) > 0 {
				at = fuzzOffset(a.Value, data)
			}
			if at != next+4 || binary.BigEndian.Uint16(data[next:]) != a.Type ||
				int(binary.BigEndian.Uint16(data[next+2:])) != len(a.Value) || at+len(a.Value) > len(data) {
				t.Fatalf("attribute %d (type %#x, %d bytes) at %d, expected at %d", i, a.Type, len(a.Value), at, next+4)
			}
			next = at + len(a.Value) + pad4(len(a.Value))
		}
		if code, ok := ParseErrorCode(data); ok {
			typ, _ := MessageType(data)
			if !isComplete(data) || (typ != MsgAllocateError && typ != MsgBindingError) {
				t.Fatalf("error code %d from type %#x", code, typ)
			}
			if code > 255*100+255 {
				t.Fatalf("error code %d", code)
			}
		}
		if IsAllocateError(data) && IsAllocateOrBindingSuccess(data) {
			t.Fatalf("both an error and a success")
		}
		if (IsAllocateError(data) || IsAllocateOrBindingSuccess(data)) && !isComplete(data) {
			t.Fatalf("a response that is not complete")
		}
		if IsWhatsAppPong(data, nil) && !IsSTUN(data) {
			t.Fatalf("a pong that is not STUN")
		}
		if len(data) >= 20 {
			txid, _ := TransactionID(data)
			if IsWhatsAppPong(data, nil) != IsWhatsAppPong(data, txid) {
				t.Fatalf("a pong does not match its own transaction id")
			}
		}
	})
}

// FuzzEncodeRequest encodes a request with arbitrary attributes and reads
// it back: a complete message of the type asked for, the declared length
// the body's, the same attributes in order followed by MESSAGE-INTEGRITY
// and FINGERPRINT, and both of those verify the way the relay checks them.
func FuzzEncodeRequest(f *testing.F) {
	f.Add(MsgAllocateRequest, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, []byte{0x40, 0x00, 3, 'a', 'b', 'c'}, []byte("key"), true)
	f.Add(MsgBindingRequest, []byte{}, []byte{}, []byte{}, false)
	f.Add(MsgWhatsAppPing, []byte{7}, []byte{0x80, 0x5a, 0, 0x00, 0x16, 6, 1, 2, 3, 4, 5, 6}, []byte(nil), true)
	f.Fuzz(func(t *testing.T, typ uint16, txidRaw, script, key []byte, fingerprint bool) {
		typ &= 0x3fff
		var txid [12]byte
		copy(txid[:], txidRaw)
		var want []Attribute
		var attrs []byte
		for len(script) >= 3 && len(want) < 64 {
			a := Attribute{Type: binary.BigEndian.Uint16(script)}
			n := min(int(script[2]), len(script)-3)
			a.Value = script[3 : 3+n]
			script = script[3+n:]
			want = append(want, a)
			attrs = appendAttr(attrs, a.Type, a.Value)
		}
		var integrity []byte
		if key != nil {
			integrity = key
		}
		msg := EncodeRequest(typ, &txid, attrs, integrity, fingerprint)
		if got, ok := MessageType(msg); !ok || got != typ || !isComplete(msg) {
			t.Fatalf("type %#x %v, complete %v", got, ok, isComplete(msg))
		}
		if int(binary.BigEndian.Uint16(msg[2:])) != len(msg)-20 {
			t.Fatalf("declared %d, body %d", binary.BigEndian.Uint16(msg[2:]), len(msg)-20)
		}
		if id, _ := TransactionID(msg); !bytes.Equal(id, txid[:]) {
			t.Fatalf("transaction id %x", id)
		}
		got := ParseAttributes(msg)
		extra := 0
		if integrity != nil {
			extra++
		}
		if fingerprint {
			extra++
		}
		if len(got) != len(want)+extra {
			t.Fatalf("%d attributes back, wrote %d+%d", len(got), len(want), extra)
		}
		for i, a := range want {
			if got[i].Type != a.Type || !bytes.Equal(got[i].Value, a.Value) {
				t.Fatalf("attribute %d: %#x %x, wrote %#x %x", i, got[i].Type, got[i].Value, a.Type, a.Value)
			}
		}
		end := 20 + len(attrs) // where MESSAGE-INTEGRITY starts
		if integrity != nil {
			mi := got[len(want)]
			mac := hmac.New(sha1.New, integrity)
			mac.Write(pseudoHeader(typ, uint16(len(attrs)+24), &txid))
			mac.Write(msg[20:end])
			if mi.Type != attrMessageIntegrity || !hmac.Equal(mi.Value, mac.Sum(nil)) {
				t.Fatalf("MESSAGE-INTEGRITY does not verify")
			}
			end += 24
		}
		if fingerprint {
			fp := got[len(got)-1]
			crc := crc32.ChecksumIEEE(append(pseudoHeader(typ, uint16(end-20+8), &txid), msg[20:end]...))
			if fp.Type != attrFingerprint || binary.BigEndian.Uint32(fp.Value) != crc^fingerprintXOR {
				t.Fatalf("FINGERPRINT does not verify")
			}
		}
	})
}
