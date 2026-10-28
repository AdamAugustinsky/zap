// Package lockhash is the only implementation of ZapFast's two password
// verifiers, ported from upstream (zf:src/app_lock.rs, zf:src/settings.rs):
//
//   - the app lock's password: "pbkdf2-sha256$rounds$salt$hash", 600,000
//     rounds for new verifiers (OWASP's current advice), kept in settings.json
//     as app_lock_hash;
//   - the locked chats' code: "salt$hash", 200,000 rounds, the code trimmed of
//     surrounding white space, kept as chat_lock_code_hash.
//
// Both are PBKDF2-HMAC-SHA256 over the password's UTF-8 bytes, with a 16-byte
// salt from crypto/rand and a 32-byte hash, both in hex. The password is never
// stored.
//
// Checking:
//
//   - a stored value is parsed strictly: the exact number of '$'-separated
//     fields, the scheme name, a 16-byte salt and a 32-byte hash in hex (an
//     empty or short hash would make the comparison meaningless), and for the
//     app lock a decimal round count from 1 to 10 × 600,000 (a hand-edited
//     count must not hang the checking goroutine for hours). Anything else
//     matches no password;
//   - the derived hash is compared in constant time.
//
// The password bytes belong to the caller, who zeroes them when done (clear).
// This package never copies, keeps or zeroes them: it hands the caller's bytes
// to PBKDF2 without a string copy. The standard library's HMAC still keeps
// its own derived key pads, which it does not zero; Go offers no way to erase
// those on this platform.
//
// Deriving takes tens of milliseconds on purpose: call these off the
// interface thread where the caller can.
package lockhash

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"unsafe"
)

const (
	// MinPasswordChars is the shortest app lock password Settings accepts, in
	// characters (zf:src/app_lock.rs MIN_PASSWORD_CHARS).
	MinPasswordChars = 6

	// AppRounds is the round count of new app lock verifiers.
	AppRounds uint32 = 600_000
	// MaxAppRounds is the largest round count a stored app lock verifier may
	// name.
	MaxAppRounds = 10 * AppRounds
	// AppScheme leads an app lock verifier, so a later scheme can be told
	// apart and the rounds can change without breaking stored ones.
	AppScheme = "pbkdf2-sha256"

	// ChatRounds is the locked chats' fixed round count
	// (zf:src/settings.rs CHAT_LOCK_ROUNDS); it is not stored.
	ChatRounds = 200_000

	saltLen = 16
	hashLen = 32
)

// AppVerifier is a new app lock verifier for password.
func AppVerifier(password []byte) string { return AppVerifierWithRounds(password, AppRounds) }

// AppVerifierWithRounds is AppVerifier with a chosen round count, at least
// one and at most MaxAppRounds; tests use few.
func AppVerifierWithRounds(password []byte, rounds uint32) string {
	rounds = min(max(rounds, 1), MaxAppRounds)
	salt, hash := derive(password, int(rounds))
	return AppScheme + "$" + strconv.FormatUint(uint64(rounds), 10) + "$" + hex.EncodeToString(salt[:]) + "$" + hex.EncodeToString(hash[:])
}

// VerifiesApp reports whether password matches the stored app lock
// verifier.
func VerifiesApp(stored string, password []byte) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != AppScheme {
		return false
	}
	rounds, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || rounds == 0 || rounds > uint64(MaxAppRounds) {
		return false
	}
	return verifies(parts[2], parts[3], int(rounds), password)
}

// ChatVerifier is a new locked chats verifier for code, trimmed of white
// space; "" when nothing but white space is left, which means no code.
func ChatVerifier(code []byte) string {
	code = bytes.TrimSpace(code)
	if len(code) == 0 {
		return ""
	}
	salt, hash := derive(code, ChatRounds)
	return hex.EncodeToString(salt[:]) + "$" + hex.EncodeToString(hash[:])
}

// VerifiesChat reports whether code, trimmed of white space, matches the
// stored locked chats verifier. A blank code matches nothing.
func VerifiesChat(stored string, code []byte) bool {
	code = bytes.TrimSpace(code)
	salt, hash, ok := strings.Cut(stored, "$")
	if !ok || len(code) == 0 {
		return false
	}
	return verifies(salt, hash, ChatRounds, code)
}

// derive makes a fresh salt and the hash of password under it.
func derive(password []byte, rounds int) (salt [saltLen]byte, hash [hashLen]byte) {
	// crypto/rand.Read never returns an error: without the system generator
	// the program ends, as upstream's expect does.
	rand.Read(salt[:])
	k := key(password, salt[:], rounds)
	if k == nil {
		panic("lockhash: PBKDF2 refused its parameters")
	}
	copy(hash[:], k)
	return salt, hash
}

// verifies checks password against a stored salt and hash in hex, both of
// their exact lengths.
func verifies(saltHex, hashHex string, rounds int, password []byte) bool {
	var salt [saltLen]byte
	var want [hashLen]byte
	if !decode(salt[:], saltHex) || !decode(want[:], hashHex) {
		return false
	}
	got := key(password, salt[:], rounds)
	return got != nil && subtle.ConstantTimeCompare(got, want[:]) == 1
}

// decode fills dst from s, which must be exactly len(dst) bytes in hex.
func decode(dst []byte, s string) bool {
	if len(s) != 2*len(dst) {
		return false
	}
	n, err := hex.Decode(dst, []byte(s))
	return err == nil && n == len(dst)
}

// key is PBKDF2-HMAC-SHA256 of password, nil if PBKDF2 refuses the
// parameters (it does not for the ones used here). The password goes in as a
// string over the caller's own bytes rather than a copy, so zeroing the
// caller's slice leaves no copy of ours behind; PBKDF2 neither keeps nor
// changes it, and the string does not outlive the call.
func key(password, salt []byte, rounds int) []byte {
	pw := unsafe.String(unsafe.SliceData(password), len(password))
	k, err := pbkdf2.Key(sha256.New, pw, salt, rounds, hashLen)
	if err != nil {
		return nil
	}
	return k
}
