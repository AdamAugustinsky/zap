// Ported from whatsapp-rust 5e72baf wacore/src/voip/hbh_srtp.rs (tests), MIT.

package srtp

import (
	"bytes"
	"encoding/hex"
	"testing"

	"zapfast/gocore/voip/internal/kat"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/rtcp"
)

func hx(b []byte) string { return hex.EncodeToString(b) }

func TestHBHSFUDerivationMatchesNativeKAT(t *testing.T) {
	k := kat.KATs(t)
	h := k.Hex(t, "inputs", "hbhKey")
	want := func(key string) string { return k.Str(t, "hbh_srtp", key) }
	if got := hx(kdf.HKDFSHA256(make([]byte, 32), h[:hbhSaltSeedLen], []byte(hbhUplinkSaltLabel), 32)); got != want("uplinkSalt32") {
		t.Fatal("uplink salt", got)
	}
	for name, f := range map[string]func([]byte) ([]byte, bool){
		"uplinkKey30": DeriveHBHSRTCPKeyUplink, "sharedSrtpKey30": DeriveHBHSRTPKeyShared,
		"sharedSrtcpKey30": DeriveHBHSRTCPKeyShared, "downlinkKey30": DeriveHBHSRTCPKeyDownlink,
	} {
		if got, ok := f(h); !ok || hx(got) != want(name) {
			t.Fatal(name, hx(got))
		}
	}
	keying, _ := KeyingFromHBHSRTCPKeyUplink(h)
	if hx(keying.MasterKey[:]) != want("masterKey") || hx(keying.MasterSalt[:]) != want("masterSalt") {
		t.Fatal("keying")
	}
	s := ExpandSessionKeys(&keying)
	if hx(s.SessionKey[:]) != want("sessionKey") || hx(s.SessionSalt[:]) != want("sessionSalt") || hx(s.AuthKey[:]) != want("authKey") {
		t.Fatal("session keys")
	}
	c := ExpandSRTCPSessionKeys(&keying)
	if hx(c.SessionKey[:]) != want("srtcpSessionKey") || hx(c.SessionSalt[:]) != want("srtcpSessionSalt") || hx(c.AuthKey[:]) != want("srtcpAuthKey") {
		t.Fatal("srtcp session keys")
	}
	if _, ok := DeriveHBHSRTCPKeyUplink(h[:29]); ok {
		t.Fatal("29-byte hbh_key accepted")
	}
}

func TestHBHSRTCPDirectionAndLabelsAreIsolated(t *testing.T) {
	h := kat.KATs(t).Hex(t, "inputs", "hbhKey")
	up, _ := DeriveHBHSRTCPKeysUplink(h)
	down, _ := DeriveHBHSRTCPKeysDownlink(h)
	keying, _ := KeyingFromHBHSRTCPKeyUplink(h)
	r := ExpandSessionKeys(&keying)
	if up.CipherKey == r.SessionKey || up.AuthKey == r.AuthKey || up.CipherKey == down.CipherKey {
		t.Fatal("keys not isolated")
	}
	ssrc := uint32(0x1234_5678)
	sr := rtcp.SenderReport(ssrc, rtcp.SenderStats{PacketsSent: 3, OctetsSent: 900, RTPTimestamp: 18_000}, 1_718_000_000_000)
	p := ProtectSRTCP(&up, ssrc, 7, sr[:])
	if plain, index, ok := UnprotectSRTCP(&up, ssrc, p); !ok || !bytes.Equal(plain, sr[:]) || index != 7 {
		t.Fatal("round trip")
	}
	if _, _, ok := UnprotectSRTCP(&down, ssrc, p); ok {
		t.Fatal("downlink keys open uplink SRTCP")
	}
	for _, mode := range HBHSRTCPModes {
		u, _ := DeriveHBHSRTCPKeysUplinkForMode(h, mode)
		d, _ := DeriveHBHSRTCPKeysDownlinkForMode(h, mode)
		if (mode == HBHDirectional) == (u.AuthKey == d.AuthKey) {
			t.Fatal("mode", mode)
		}
	}
}

func TestHBHSRTCPPacketMatchesLibsrtp(t *testing.T) {
	k := kat.KATs(t)
	keys, _ := DeriveHBHSRTCPKeysUplink(k.Hex(t, "inputs", "hbhKey"))
	p := ProtectSRTCP(&keys, 0x1234_5678, 1, k.Hex(t, "rtcp", "senderReport"))
	if hx(p) != k.Str(t, "hbh_srtp", "srtcpProtectedSrIndex1") {
		t.Fatal(hx(p))
	}
}

func TestHBHICMNonceAndCipherMatchKAT(t *testing.T) {
	k := kat.KATs(t)
	keying, _ := KeyingFromHBHSRTCPKeyUplink(k.Hex(t, "inputs", "hbhKey"))
	s := ExpandSessionKeys(&keying)
	ssrc := uint32(k.Uint(t, "inputs", "ssrc"))
	index := k.Uint(t, "inputs", "roc")<<16 | k.Uint(t, "inputs", "seq")
	nonce := RTPICMNonce(ssrc, index)
	if hx(nonce[:]) != k.Str(t, "hbh_srtp", "rtpIcmNonce") {
		t.Fatal("nonce")
	}
	payload := k.Hex(t, "inputs", "payload")
	ct := bytes.Clone(payload)
	CryptHBHRTPPayload(&s, ssrc, index, ct)
	if hx(ct) != k.Str(t, "hbh_srtp", "cipher_out") {
		t.Fatal(hx(ct))
	}
	CryptHBHRTPPayload(&s, ssrc, index, ct)
	if !bytes.Equal(ct, payload) {
		t.Fatal("round trip")
	}
	icm := append(s.SessionKey[:], s.SessionSalt[:]...)
	if hx(icm) != k.Str(t, "hbh_srtp", "icmKey30") {
		t.Fatal("icm key")
	}
}

// libsrtp's ICM carries from byte 15 into byte 14 only: block 256 reuses
// byte 13 unchanged, unlike a 128-bit CTR.
func TestICMCounterCarriesIntoByte14Only(t *testing.T) {
	key, salt := bytes.Repeat([]byte{1}, 16), make([]byte, 14)
	iv := make([]byte, 16)
	iv[14], iv[15] = 0xff, 0xff
	data := make([]byte, 32)
	aesICMCrypt(key, salt, iv, data)
	// Second block's counter is ...00 00 (byte 13 untouched), not ...01 00 00.
	want := make([]byte, 16)
	aesICMCrypt(key, salt, make([]byte, 16), want)
	if !bytes.Equal(data[16:], want) {
		t.Fatal("carry went past byte 14")
	}
}
