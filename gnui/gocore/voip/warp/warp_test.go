// Ported from whatsapp-rust 5e72baf wacore/src/voip/warp.rs (tests), MIT.

package warp

import (
	"bytes"
	"encoding/hex"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

func vectors(t *testing.T) (key, packet []byte, roc uint32, want string) {
	k := kat.KATs(t)
	return k.Hex(t, "e2e_srtp", "peer_authKey"), k.Hex(t, "inputs", "samplePacket"),
		uint32(k.Uint(t, "inputs", "roc")), k.Str(t, "e2e_srtp", "warp_mi_tag4")
}

func TestMITagMatchesKAT(t *testing.T) {
	key, packet, roc, want := vectors(t)
	tag := MITag(key, packet, roc)
	if got := hex.EncodeToString(tag[:MITagLen]); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestVerifyRejectsForgedAndMisshapenTags(t *testing.T) {
	key, packet, roc, _ := vectors(t)
	tag := MITag(key, packet, roc)
	good := tag[:MITagLen]
	if !VerifyMITag(key, packet, roc, MITagLen, good) {
		t.Fatal("good tag rejected")
	}
	forged := bytes.Clone(good)
	forged[0] ^= 1
	if VerifyMITag(key, packet, roc, MITagLen, forged) {
		t.Fatal("forged tag accepted")
	}
	if VerifyMITag(key, packet, roc+1, MITagLen, good) {
		t.Fatal("tag accepted under another ROC")
	}
	if VerifyMITag(key, packet, roc, MITagLen, good[:3]) {
		t.Fatal("short tag accepted")
	}
	if VerifyMITag(key, packet, roc, MITagMaxLen+1, tag[:]) {
		t.Fatal("over-long tag_len accepted")
	}
}

func TestAppendInPlaceWritesExactlyTheTag(t *testing.T) {
	key, packet, roc, _ := vectors(t)
	framed := AppendMITag(key, bytes.Clone(packet), roc, MITagLen)
	if len(framed) != len(packet)+MITagLen || !bytes.Equal(framed[:len(packet)], packet) {
		t.Fatal("framing")
	}
	if !VerifyMITag(key, packet, roc, MITagLen, framed[len(packet):]) {
		t.Fatal("appended tag does not verify")
	}
}

func TestAudioPiggybackExtensionFor(t *testing.T) {
	if _, ok := AudioPiggybackExtensionFor(1, true, PiggybackStartPacket); ok {
		t.Fatal("packet 1 piggybacks")
	}
	if w, ok := AudioPiggybackExtensionFor(2, true, PiggybackStartPacket); !ok || w != 0x3001_0000 {
		t.Fatal("packet 2")
	}
	if _, ok := AudioPiggybackExtensionFor(5, false, PiggybackStartPacket); ok {
		t.Fatal("disabled")
	}
}
