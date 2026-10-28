package lockhash

import (
	"strconv"
	"strings"
	"testing"
)

// ---- the app lock (zf:src/app_lock.rs tests) ----

func TestAVerifierIsSaltedSlowAndAcceptsOnlyItsPassword(t *testing.T) {
	t.Parallel()
	first := AppVerifierWithRounds([]byte("correct horse"), 1_000)
	second := AppVerifierWithRounds([]byte("correct horse"), 1_000)
	if first == second {
		t.Error("each verifier has its own salt")
	}
	if !strings.HasPrefix(first, "pbkdf2-sha256$1000$") {
		t.Errorf("verifier %q", first)
	}
	if strings.Contains(first, "correct horse") {
		t.Error("the verifier holds the password")
	}
	if !VerifiesApp(first, []byte("correct horse")) || !VerifiesApp(second, []byte("correct horse")) {
		t.Error("the right password was refused")
	}
	for _, wrong := range []string{"correct hors", "correct horse ", ""} {
		if VerifiesApp(first, []byte(wrong)) {
			t.Errorf("%q accepted", wrong)
		}
	}
}

func TestNewVerifiersUseTheFullRoundCount(t *testing.T) {
	t.Parallel()
	stored := AppVerifier([]byte("secret-password"))
	if !strings.HasPrefix(stored, "pbkdf2-sha256$600000$") {
		t.Errorf("verifier %q", stored)
	}
	if !VerifiesApp(stored, []byte("secret-password")) {
		t.Error("refused")
	}
}

func TestMalformedOrForeignVerifiersMatchNothing(t *testing.T) {
	t.Parallel()
	good := AppVerifierWithRounds([]byte("password"), 10)
	parts := strings.Split(good, "$")
	salt, hash := parts[2], parts[3]
	for _, stored := range []string{
		// upstream's cases
		"",
		"password",
		"scrypt$10$" + salt + "$" + hash,
		"pbkdf2-sha256$0$" + salt + "$" + hash,
		"pbkdf2-sha256$99999999$" + salt + "$" + hash,
		"pbkdf2-sha256$10$zz$" + hash,
		good + "$extra",
		// ours: lengths, hex, round counts, the shape
		"pbkdf2-sha256$10$" + salt + "$",
		"pbkdf2-sha256$10$" + salt + "$00",
		"pbkdf2-sha256$10$" + salt + "$" + hash[:62],
		"pbkdf2-sha256$10$" + salt + "$" + hash + "00",
		"pbkdf2-sha256$10$" + salt[:30] + "$" + hash,
		"pbkdf2-sha256$10$" + salt + "00$" + hash,
		"pbkdf2-sha256$10$$" + hash,
		"pbkdf2-sha256$10$" + salt + "$" + hash[:63] + "g",
		"pbkdf2-sha256$10$" + salt + "$" + hash[:63],
		"pbkdf2-sha256$10$" + salt + "$" + hash + " ",
		"pbkdf2-sha256$+10$" + salt + "$" + hash,
		"pbkdf2-sha256$-10$" + salt + "$" + hash,
		"pbkdf2-sha256$ 10$" + salt + "$" + hash,
		"pbkdf2-sha256$1e3$" + salt + "$" + hash,
		"pbkdf2-sha256$$" + salt + "$" + hash,
		"pbkdf2-sha256$" + strconv.FormatUint(uint64(MaxAppRounds)+1, 10) + "$" + salt + "$" + hash,
		"pbkdf2-sha256$4294967296$" + salt + "$" + hash,
		"PBKDF2-SHA256$10$" + salt + "$" + hash,
		"pbkdf2-sha256$10$" + salt + hash,
		salt + "$" + hash, // a locked chats verifier
		"$$$",
		"pbkdf2-sha256$10$" + salt + "$" + hash + "\x00",
	} {
		if VerifiesApp(stored, []byte("password")) {
			t.Errorf("%q verified", stored)
		}
	}
	if !VerifiesApp(good, []byte("password")) {
		t.Error("the well-formed one was refused")
	}
}

// A verifier made by the Rust core (Python's hashlib computed this one with
// the same scheme) opens here: settings.json written by upstream keeps
// working.
func TestAVerifierFromUpstreamsSchemeVerifies(t *testing.T) {
	t.Parallel()
	const stored = "pbkdf2-sha256$1000$000102030405060708090a0b0c0d0e0f$70d0f36fbc8faa37ba8dba90ba77d8d46a435f736493c28b4bbccd517c704154"
	if !VerifiesApp(stored, []byte("correct horse é")) {
		t.Error("an upstream verifier was refused")
	}
	if VerifiesApp(stored, []byte("correct horse e")) {
		t.Error("a wrong password was accepted")
	}
	// Upstream reads its hex in either case.
	if !VerifiesApp("pbkdf2-sha256$1000$000102030405060708090A0B0C0D0E0F$70D0F36FBC8FAA37BA8DBA90BA77D8D46A435F736493C28B4BBCCD517C704154", []byte("correct horse é")) {
		t.Error("upper-case hex was refused")
	}
}

// What the copies this package replaced wrote still opens: each value was made
// by that copy before it was deleted and checked with Python's hashlib.
func TestVerifiersFromTheReplacedCopiesStillVerify(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		from, stored, right, wrong string
		chat                       bool
	}{
		{"ui/settings appLockVerifier", "pbkdf2-sha256$600000$07e1bdc1a0f0a5a56a8ac9fc2a125d44$6422f92cc491c1737121b088a5470e05faca909cd0ec2a0c32ad81d899f5a4cf", "correct horse é", "correct horse e", false},
		{"ui/lock/applock Verifier", "pbkdf2-sha256$600000$0368b0c2c8164623c320b719cd7f2462$b398bd30a6a01f34bf138ae7584028bfee372a19396e317872c7ad944765fb92", "tr0ub4dor&3", "tr0ub4dor&4", false},
		{"ui/settings chatLockVerifier", "e29e2f639649f237d7d5ba01edcb537c$0248a4280797ee1096835d0fb31c1c94ce542d35522d734c50a3337e102d45e0", "2468", "2469", true},
		{"ui/dialogs SetChatLockCode", "78d7c4c3f9405745401869fcfba7a827$693baf9c3a0e2c4ae585813b6f58fed725b7f6935d42ecd9e0941310f6415162", " 1357\t", "1358", true},
		{"state Settings.SetChatLockCode", "bc9ee0e1c63bc52f0e31eeef9ccbcd2a$42277569dbceedf68cddbe7a523a76036b9d43f4c663a6fa75667388f3817460", "9753", "975", true},
	} {
		verifies := VerifiesApp
		if c.chat {
			verifies = VerifiesChat
		}
		if !verifies(c.stored, []byte(c.right)) {
			t.Errorf("%s: the right password was refused", c.from)
		}
		if verifies(c.stored, []byte(c.wrong)) {
			t.Errorf("%s: a wrong password was accepted", c.from)
		}
	}
}

func TestTheRoundCountStaysInBounds(t *testing.T) {
	t.Parallel()
	v := AppVerifierWithRounds([]byte("password"), 0)
	if !strings.HasPrefix(v, "pbkdf2-sha256$1$") || !VerifiesApp(v, []byte("password")) {
		t.Errorf("verifier %q", v)
	}
}

// ---- the locked chats' code (zf:src/settings.rs tests) ----

func TestTheLockedChatVerifierIsSaltedAndRejectsOtherCodes(t *testing.T) {
	t.Parallel()
	first := ChatVerifier([]byte(" 1234 "))
	if !VerifiesChat(first, []byte("1234")) {
		t.Error("the code was refused")
	}
	for _, wrong := range []string{"1235", "", "  ", "12345", "123"} {
		if VerifiesChat(first, []byte(wrong)) {
			t.Errorf("%q accepted", wrong)
		}
	}
	// A fresh salt every time, so the same code never stores the same value.
	second := ChatVerifier([]byte("1234"))
	if first == second || !VerifiesChat(second, []byte("1234")) {
		t.Errorf("verifiers %q %q", first, second)
	}
	if parts := strings.Split(first, "$"); len(parts) != 2 || len(parts[0]) != 32 || len(parts[1]) != 64 {
		t.Errorf("verifier %q is not upstream's salt$hash", first)
	}
	// No code is no verifier; nothing verifies against none.
	if v := ChatVerifier([]byte(" \t\n")); v != "" {
		t.Errorf("blank code made %q", v)
	}
	if VerifiesChat("", []byte("1234")) {
		t.Error("an unset code verified")
	}
	// The code is trimmed when it is checked too, as upstream does.
	if !VerifiesChat(first, []byte("\t1234\n")) {
		t.Error("a code with white space around it was refused")
	}
}

func TestMalformedLockedChatVerifiersMatchNothing(t *testing.T) {
	t.Parallel()
	good := ChatVerifier([]byte("1234"))
	salt, hash, _ := strings.Cut(good, "$")
	for _, stored := range []string{
		"",
		"1234",
		"$",
		salt + "$",
		"$" + hash,
		salt + hash,
		salt + "$" + hash + "$",
		salt + "$" + hash + "$extra",
		salt + "$" + hash[:62],
		salt + "$" + hash + "00",
		salt[:30] + "$" + hash,
		salt + "00$" + hash,
		salt[:31] + "z$" + hash,
		salt + "$" + hash[:63] + "z",
		" " + good,
		good + "\n",
		"pbkdf2-sha256$200000$" + salt + "$" + hash, // an app lock verifier
	} {
		if VerifiesChat(stored, []byte("1234")) {
			t.Errorf("%q verified", stored)
		}
	}
}

// The caller owns the password: the functions neither change nor keep it, so
// zeroing it afterwards is the caller's whole job.
func TestThePasswordIsLeftToTheCaller(t *testing.T) {
	t.Parallel()
	pw := []byte("  open sesame  ")
	app := AppVerifierWithRounds(pw, 10)
	chat := ChatVerifier(pw)
	VerifiesApp(app, pw)
	VerifiesChat(chat, pw)
	if string(pw) != "  open sesame  " {
		t.Fatalf("the password was changed: %q", pw)
	}
	clear(pw)
	if VerifiesApp(app, pw) || VerifiesChat(chat, pw) {
		t.Error("a zeroed password still verifies")
	}
}

// Whatever is stored, checking never panics.
func FuzzVerifiersRefuseWithoutPanicking(f *testing.F) {
	good := AppVerifierWithRounds([]byte("pw"), 3)
	f.Add(good, []byte("pw"))
	f.Add(ChatVerifier([]byte("pw")), []byte("pw"))
	f.Add("pbkdf2-sha256$$$", []byte(""))
	f.Add("$", []byte{0xff, 0xfe})
	f.Add(strings.Repeat("$", 100), []byte("pw"))
	f.Fuzz(func(t *testing.T, stored string, pw []byte) {
		// Keep a fuzzed round count cheap: only the parsing is under test.
		if parts := strings.Split(stored, "$"); len(parts) == 4 {
			if n, err := strconv.ParseUint(parts[1], 10, 32); err == nil && n > 100 {
				return
			}
		}
		VerifiesApp(stored, pw)
		VerifiesChat(stored, pw)
	})
}
