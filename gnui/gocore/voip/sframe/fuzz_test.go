package sframe

// Native fuzz targets for SFrame decryption of a received frame.
//
//	CGO_ENABLED=0 go test ./gocore/voip/sframe -run '^$' -fuzz '^FuzzDecrypt$' -fuzztime 45s

import (
	"bytes"
	"encoding/hex"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

// fuzzSessions are the golden vectors' sender and receiver: what sender
// encrypts, receiver decrypts.
func fuzzSessions(f *testing.F) (sender, receiver *Session, frames [][]byte) {
	vs := kat.Golden(f).List(f, "sframe")
	v := vs[0]
	ck := v.Hex(f, "call_key")
	sender, _ = NewSession(ck, v.Str(f, "self_lid"), v.Str(f, "peer_lid"))
	receiver, _ = NewSession(ck, v.Str(f, "peer_lid"), v.Str(f, "self_lid"))
	for _, v := range vs {
		for _, o := range v.List(f, "out") {
			b, _ := hex.DecodeString(o.Get(f).(string))
			frames = append(frames, b)
		}
	}
	return sender, receiver, frames
}

// FuzzDecrypt decrypts an arbitrary received frame. Only Decrypted carries
// a plaintext; that plaintext is exactly as long as the ciphertext before
// the tag and header, and is what the sender's own encryption under the
// frame's counter turns back into the frame's ciphertext and tag. Decrypting
// twice gives the same answer.
func FuzzDecrypt(f *testing.F) {
	sender, receiver, frames := fuzzSessions(f)
	for _, fr := range frames {
		f.Add(fr)
	}
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0x80}, 40))
	f.Fuzz(func(t *testing.T, frame []byte) {
		plain, res := receiver.Decrypt(frame)
		again, res2 := receiver.Decrypt(frame)
		if res != res2 || !bytes.Equal(plain, again) {
			t.Fatalf("Decrypt is not deterministic")
		}
		if res != Decrypted {
			if plain != nil {
				t.Fatalf("result %d with a plaintext", res)
			}
			return
		}
		hlen := int(frame[len(frame)-1])
		start := len(frame) - hlen
		if len(plain) != start-gcmTagLen || len(plain) == 0 {
			t.Fatalf("%d plaintext bytes from a %d-byte ciphertext", len(plain), start-gcmTagLen)
		}
		counter, _, ok := parseHeader(frame[start:])
		if !ok {
			t.Fatalf("decrypted a frame whose header does not parse")
		}
		sender.SetCounter(counter)
		if enc := sender.Encrypt(plain); !bytes.Equal(enc[:start], frame[:start]) {
			t.Fatalf("re-encrypting the plaintext under counter %d gives other bytes", counter)
		}
	})
}

// FuzzRoundTrip encrypts a frame and decrypts it, then changes one byte of
// its ciphertext or tag. The untouched frame decrypts to the plaintext; the
// changed one never decrypts. (The header is not authenticated by design,
// see the package comment, so a change there is not asserted on.)
func FuzzRoundTrip(f *testing.F) {
	sender, receiver, _ := fuzzSessions(f)
	f.Add([]byte{0xa4}, uint64(0), uint16(0), byte(1))
	f.Add(bytes.Repeat([]byte{0x48}, 64), uint64(127), uint16(5), byte(0x80))
	f.Add([]byte{1, 2, 3}, uint64(1<<63), uint16(17), byte(0xff))
	f.Fuzz(func(t *testing.T, plain []byte, counter uint64, at uint16, flip byte) {
		if len(plain) == 0 {
			// An empty frame is 16 tag bytes, below the 17 Decrypt requires
			// before it reads a frame as SFrame at all: the media path never
			// sends one.
			return
		}
		sender.SetCounter(counter)
		frame := sender.Encrypt(plain)
		if len(frame) > len(plain)+gcmTagLen+headerMax {
			t.Fatalf("%d-byte frame for %d bytes", len(frame), len(plain))
		}
		got, res := receiver.Decrypt(frame)
		if res != Decrypted || !bytes.Equal(got, plain) {
			t.Fatalf("round trip under counter %d: result %d", counter, res)
		}
		if flip == 0 {
			return
		}
		body := len(plain) + gcmTagLen
		frame[int(at)%body] ^= flip
		if _, res := receiver.Decrypt(frame); res == Decrypted {
			t.Fatalf("a frame changed at byte %d still decrypts", int(at)%body)
		}
	})
}
