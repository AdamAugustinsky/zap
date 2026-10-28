package sys

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The run's log never holds message text, phone numbers or keys, whoever
// logs them: the app, the Go backend or whatsmeow.
func TestTheLogRedactsMessageTextNumbersAndKeys(t *testing.T) {
	dir := t.TempDir()
	l, err := StartLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	const (
		phone   = "5511987654321"
		pretty  = "+55 11 98765-4321"
		lid     = "123456789012345"
		text    = "meet me at the usual place"
		hexKey  = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
		b64Key  = "q83vEjRWeJCrze8SNFZ4kKvN7xI0VniQ8Ae1m2Lp0Zk="
		pairing = "K7Q2-9XWD"
	)
	slog.Warn("could not send to "+phone+"@s.whatsapp.net", "text", text, "err", errors.New("no session for "+lid+"@lid"))
	slog.Error("a contact card", "phone", pretty, "detail", "call "+pretty+" today", "key", hexKey)
	slog.Warn("a token", "url", "https://example.invalid/x?auth="+b64Key, "pair_code", pairing, "raw", []byte(text))
	slog.Warn("kept", "account", "1", "n", 42, "path", "/tmp/accounts/1/archive-go.db")
	wl := WhatsmeowLog().Sub("Client")
	wl.Warnf("failed to decrypt message from %s: %s", phone+":12@s.whatsapp.net", hexKey)
	wl.Errorf("bad mac for %s", lid+"@lid")
	wl.Debugf("received %q from %s", text, phone)
	wl.Infof("message body %q", text)
	l.Close()

	b, err := os.ReadFile(filepath.Join(dir, "zapfast.log"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(b)
	for _, secret := range []string{phone, "98765", lid, text, "usual place", hexKey, b64Key[:20], pairing} {
		if strings.Contains(log, secret) {
			t.Errorf("the log holds %q:\n%s", secret, log)
		}
	}
	for _, kept := range []string{
		"could not send to [number]@s.whatsapp.net", "no session for [number]@lid", "call [number] today",
		"auth=[key]", "raw=\"[26 bytes]\"", "account=1", "n=42", "/tmp/accounts/1/archive-go.db",
		"source=whatsmeow", "module=whatsmeow/Client", "failed to decrypt message from [number]:12@s.whatsapp.net: [key]",
	} {
		if !strings.Contains(log, kept) {
			t.Errorf("the log lacks %q:\n%s", kept, log)
		}
	}
	if n := strings.Count(log, "\n"); n != 6 {
		t.Errorf("%d lines, want 6 (whatsmeow's debug and info dropped):\n%s", n, log)
	}
}

func TestRedactLeavesOrdinaryTextAlone(t *testing.T) {
	for _, s := range []string{
		"the core has already started",
		"zapfast wa: reading older messages",
		"took 123456 µs",
		"accounts/12/archive.db",
		"someVeryLongGoIdentifierWithoutAnyDigitsInIt",
	} {
		if got := Redact(s); got != s {
			t.Errorf("Redact(%q) = %q", s, got)
		}
	}
}
