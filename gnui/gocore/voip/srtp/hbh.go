// Ported from whatsapp-rust 5e72baf wacore/src/voip/hbh_srtp.rs, MIT.

package srtp

import (
	"crypto/aes"
	"encoding/binary"

	"zapfast/gocore/voip/kdf"
)

// HBHKeyLen is the only valid relay hbh_key length: 14 bytes of salt
// material followed by 16 of key material.
const HBHKeyLen = 30

const hbhSaltSeedLen = 14

// HKDF info labels of the SFU hop-by-hop derivation (KAT-pinned).
const (
	hbhUplinkSaltLabel           = "uplink hbh srtcp salt"
	hbhUplinkKeyLabel            = "uplink hbh srtcp key"
	hbhDownlinkSaltLabel         = "downlink hbh srtcp salt"
	hbhDownlinkKeyLabel          = "downlink hbh srtcp key"
	hbhSharedSRTCPSaltLabel      = "hbh srtcp salt"
	hbhSharedSRTCPKeyLabel       = "hbh srtcp key"
	hbhSharedSRTPSaltLabel       = "hbh srtp salt"
	hbhSharedSRTPKeyLabel        = "hbh srtp key"
	labelRTPEncryption      byte = 0x00
	labelRTPAuth            byte = 0x01
	labelRTPSalt            byte = 0x02
	labelRTCPEncryption     byte = 0x03
	labelRTCPAuth           byte = 0x04
	labelRTCPSalt           byte = 0x05
)

// HBHSRTCPMode is the hop-by-hop SRTCP layout WhatsApp's transport rollout
// flag selects.
type HBHSRTCPMode int

const (
	HBHDirectional HBHSRTCPMode = iota
	HBHShared
)

// HBHSRTCPModes lists both modes.
var HBHSRTCPModes = [2]HBHSRTCPMode{HBHDirectional, HBHShared}

// KeyingMaterial is a 16-byte master key and 14-byte master salt.
type KeyingMaterial struct {
	MasterKey  [16]byte
	MasterSalt [14]byte
}

// String redacts the key.
func (KeyingMaterial) String() string { return "srtp.KeyingMaterial([redacted])" }

// GoString redacts the key.
func (KeyingMaterial) GoString() string { return "srtp.KeyingMaterial([redacted])" }

// SessionKeys are expanded AES_CM_128_HMAC_SHA1_80 session keys.
type SessionKeys struct {
	SessionKey  [16]byte
	SessionSalt [14]byte
	AuthKey     [20]byte
}

// String redacts the keys.
func (SessionKeys) String() string { return "srtp.SessionKeys([redacted])" }

// GoString redacts the keys.
func (SessionKeys) GoString() string { return "srtp.SessionKeys([redacted])" }

func keyingFromCryptoKey(k []byte) KeyingMaterial {
	var m KeyingMaterial
	copy(m.MasterKey[:], k[:16])
	copy(m.MasterSalt[:], k[16:HBHKeyLen])
	return m
}

func deriveHBHKey(hbhKey []byte, saltLabel, keyLabel string) ([]byte, bool) {
	if len(hbhKey) != HBHKeyLen {
		return nil, false
	}
	salt := kdf.HKDFSHA256(make([]byte, 32), hbhKey[:hbhSaltSeedLen], []byte(saltLabel), 32)
	return kdf.HKDFSHA256(salt, hbhKey[hbhSaltSeedLen:], []byte(keyLabel), HBHKeyLen), true
}

// DeriveHBHSRTCPKeyUplink is the 30-byte uplink SRTCP crypto key.
func DeriveHBHSRTCPKeyUplink(hbhKey []byte) ([]byte, bool) {
	return deriveHBHKey(hbhKey, hbhUplinkSaltLabel, hbhUplinkKeyLabel)
}

// DeriveHBHSRTCPKeyDownlink is the 30-byte downlink SRTCP crypto key.
func DeriveHBHSRTCPKeyDownlink(hbhKey []byte) ([]byte, bool) {
	return deriveHBHKey(hbhKey, hbhDownlinkSaltLabel, hbhDownlinkKeyLabel)
}

// DeriveHBHSRTPKeyShared is the 30-byte shared SRTP crypto key.
func DeriveHBHSRTPKeyShared(hbhKey []byte) ([]byte, bool) {
	return deriveHBHKey(hbhKey, hbhSharedSRTPSaltLabel, hbhSharedSRTPKeyLabel)
}

// DeriveHBHSRTCPKeyShared is the 30-byte shared SRTCP crypto key.
func DeriveHBHSRTCPKeyShared(hbhKey []byte) ([]byte, bool) {
	return deriveHBHKey(hbhKey, hbhSharedSRTCPSaltLabel, hbhSharedSRTCPKeyLabel)
}

func keying(k []byte, ok bool) (KeyingMaterial, bool) {
	if !ok {
		return KeyingMaterial{}, false
	}
	return keyingFromCryptoKey(k), true
}

// KeyingFromHBHSRTCPKeyUplink splits the uplink SRTCP key.
func KeyingFromHBHSRTCPKeyUplink(hbhKey []byte) (KeyingMaterial, bool) {
	return keying(DeriveHBHSRTCPKeyUplink(hbhKey))
}

// KeyingFromHBHSRTCPKeyDownlink splits the downlink SRTCP key.
func KeyingFromHBHSRTCPKeyDownlink(hbhKey []byte) (KeyingMaterial, bool) {
	return keying(DeriveHBHSRTCPKeyDownlink(hbhKey))
}

// KeyingFromHBHSRTCPKeyShared splits the shared SRTCP key.
func KeyingFromHBHSRTCPKeyShared(hbhKey []byte) (KeyingMaterial, bool) {
	return keying(DeriveHBHSRTCPKeyShared(hbhKey))
}

// KeyingFromHBHSRTPKeyShared splits the shared SRTP key.
func KeyingFromHBHSRTPKeyShared(hbhKey []byte) (KeyingMaterial, bool) {
	return keying(DeriveHBHSRTPKeyShared(hbhKey))
}

// aesICMCrypt is libsrtp's AES-ICM in place: counter = (salt zero-padded to
// 16) XOR iv, keystream = AES(counter), and the counter increments byte 15
// with a single carry into byte 14 (not a 128-bit add).
func aesICMCrypt(aesKey, salt, iv16, data []byte) {
	var counter [16]byte
	copy(counter[:14], salt[:14])
	for i := range counter {
		counter[i] ^= iv16[i]
	}
	block, err := aes.NewCipher(aesKey[:16])
	if err != nil {
		panic("srtp: " + err.Error())
	}
	var ks [16]byte
	for off := 0; off < len(data); off += 16 {
		block.Encrypt(ks[:], counter[:])
		end := min(off+16, len(data))
		for i := off; i < end; i++ {
			data[i] ^= ks[i-off]
		}
		counter[15]++
		if counter[15] == 0 {
			counter[14]++
		}
	}
}

// deriveSessionBytes is libsrtp srtp_kdf_generate: IV all zero but byte 7.
func deriveSessionBytes(masterKey, masterSalt []byte, label byte, out []byte) {
	var iv [16]byte
	iv[7] = label
	clear(out)
	aesICMCrypt(masterKey, masterSalt, iv[:], out)
}

func expand(k *KeyingMaterial, enc, salt, auth byte) SessionKeys {
	var s SessionKeys
	deriveSessionBytes(k.MasterKey[:], k.MasterSalt[:], enc, s.SessionKey[:])
	deriveSessionBytes(k.MasterKey[:], k.MasterSalt[:], salt, s.SessionSalt[:])
	deriveSessionBytes(k.MasterKey[:], k.MasterSalt[:], auth, s.AuthKey[:])
	return s
}

// ExpandSessionKeys is libsrtp's RTP session-key expansion (labels 0, 1, 2).
func ExpandSessionKeys(k *KeyingMaterial) SessionKeys {
	return expand(k, labelRTPEncryption, labelRTPSalt, labelRTPAuth)
}

// ExpandSRTCPSessionKeys expands with the RTCP labels (3, 4, 5).
func ExpandSRTCPSessionKeys(k *KeyingMaterial) SessionKeys {
	return expand(k, labelRTCPEncryption, labelRTCPSalt, labelRTCPAuth)
}

func (s SessionKeys) asKeys() Keys {
	return Keys{CipherKey: s.SessionKey, Salt: s.SessionSalt, AuthKey: s.AuthKey}
}

// DeriveHBHSRTCPKeysUplinkForMode is the SRTCP keys for RTCP this client
// sends to the relay, in mode.
func DeriveHBHSRTCPKeysUplinkForMode(hbhKey []byte, mode HBHSRTCPMode) (Keys, bool) {
	var k KeyingMaterial
	var ok bool
	if mode == HBHDirectional {
		k, ok = KeyingFromHBHSRTCPKeyUplink(hbhKey)
	} else {
		k, ok = KeyingFromHBHSRTCPKeyShared(hbhKey)
	}
	if !ok {
		return Keys{}, false
	}
	return ExpandSRTCPSessionKeys(&k).asKeys(), true
}

// DeriveHBHSRTCPKeysDownlinkForMode is the SRTCP keys for RTCP received from
// the relay, in mode.
func DeriveHBHSRTCPKeysDownlinkForMode(hbhKey []byte, mode HBHSRTCPMode) (Keys, bool) {
	var k KeyingMaterial
	var ok bool
	if mode == HBHDirectional {
		k, ok = KeyingFromHBHSRTCPKeyDownlink(hbhKey)
	} else {
		k, ok = KeyingFromHBHSRTCPKeyShared(hbhKey)
	}
	if !ok {
		return Keys{}, false
	}
	return ExpandSRTCPSessionKeys(&k).asKeys(), true
}

// DeriveHBHSRTCPKeysUplink is the directional uplink SRTCP keys.
func DeriveHBHSRTCPKeysUplink(hbhKey []byte) (Keys, bool) {
	return DeriveHBHSRTCPKeysUplinkForMode(hbhKey, HBHDirectional)
}

// DeriveHBHSRTCPKeysDownlink is the directional downlink SRTCP keys.
func DeriveHBHSRTCPKeysDownlink(hbhKey []byte) (Keys, bool) {
	return DeriveHBHSRTCPKeysDownlinkForMode(hbhKey, HBHDirectional)
}

// RTPICMNonce is the RTP AES-ICM nonce: SSRC at bytes 4-7 and
// packetIndex<<16 at bytes 8-15, big-endian.
func RTPICMNonce(ssrc uint32, packetIndex uint64) [16]byte {
	var iv [16]byte
	binary.BigEndian.PutUint32(iv[4:], ssrc)
	binary.BigEndian.PutUint64(iv[8:], packetIndex<<16)
	return iv
}

// CryptHBHRTPPayload encrypts or decrypts an RTP payload in place with the
// expanded session key.
func CryptHBHRTPPayload(s *SessionKeys, ssrc uint32, packetIndex uint64, payload []byte) {
	nonce := RTPICMNonce(ssrc, packetIndex)
	aesICMCrypt(s.SessionKey[:], s.SessionSalt[:], nonce[:], payload)
}
