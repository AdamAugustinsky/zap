package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"

	"gonativeui/app/secret"

	"zapfast/gocore/store/sqlite"
)

// memKeys is a Keychain in memory.
type memKeys struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newKeys() *memKeys { return &memKeys{m: map[string][]byte{}} }

func (k *memKeys) Get(account string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[account]
	if !ok {
		return nil, secret.ErrNotFound
	}
	return bytes.Clone(v), nil
}

func (k *memKeys) Set(account string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[account] = bytes.Clone(v)
	return nil
}

func hashFiles(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	sums := map[string][32]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sums[e.Name()] = sha256.Sum256(b)
	}
	return sums
}

func sameFiles(a, b map[string][32]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func openAccount(t *testing.T, dir string, keys Keys) *Account {
	t.Helper()
	a, err := OpenAccount(context.Background(), dir, keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestAccountStoresAndWhatsmeowUpgrade opens an account, which runs
// sqlstore.Upgrade on the driver, and round-trips a device and its Signal
// state through whatsmeow's store across a reopen.
func TestAccountStoresAndWhatsmeowUpgrade(t *testing.T) {
	needSQLCipher(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "accounts", "1")
	keys := newKeys()
	a := openAccount(t, dir, keys)
	if len(keys.m) != 2 {
		t.Fatalf("keys: %v", keys.m)
	}
	archiveName, _ := ArchiveKeyName(dir)
	devicesName, _ := DevicesKeyName(dir)
	if bytes.Equal(keys.m[archiveName], keys.m[devicesName]) || len(keys.m[archiveName]) != 32 {
		t.Fatal("the archive and the whatsmeow store share a key")
	}
	if err := a.Archive.SaveMessages([]Message{{ID: "m", ChatID: "c@s.whatsapp.net", Timestamp: 1, Text: "kept"}}); err != nil {
		t.Fatal(err)
	}
	dev := a.Devices.NewDevice()
	jid := types.NewADJID("15550001111", 0, 7)
	dev.ID = &jid
	dev.PushName = "Synthetic"
	dev.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{1}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := a.Devices.PutDevice(ctx, dev); err != nil {
		t.Fatal(err)
	}
	if err := dev.Sessions.PutSession(ctx, "15550002222.0:0", []byte("synthetic session")); err != nil {
		t.Fatal(err)
	}
	var identity [32]byte
	identity[0] = 9
	if err := dev.Identities.PutIdentity(ctx, "15550002222.0:0", identity); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ArchiveFile, DevicesFile} {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		if bytes.HasPrefix(b, []byte("SQLite format 3")) || bytes.Contains(b, []byte("Synthetic")) || bytes.Contains(b, []byte("synthetic session")) {
			t.Fatalf("%s is not encrypted", name)
		}
	}

	a = openAccount(t, dir, keys)
	defer a.Close()
	devices, err := a.Devices.GetAllDevices(ctx)
	if err != nil || len(devices) != 1 || devices[0].PushName != "Synthetic" || *devices[0].ID != jid {
		t.Fatalf("devices %v %v", devices, err)
	}
	got := devices[0]
	if s, err := got.Sessions.GetSession(ctx, "15550002222.0:0"); err != nil || string(s) != "synthetic session" {
		t.Fatalf("session %q %v", s, err)
	}
	if ok, err := got.Identities.IsTrustedIdentity(ctx, "15550002222.0:0", identity); err != nil || !ok {
		t.Fatal("identity", ok, err)
	}
	identity[0] = 10
	if ok, err := got.Identities.IsTrustedIdentity(ctx, "15550002222.0:0", identity); err != nil || ok {
		t.Fatal("a changed identity was trusted", err)
	}
	if m, err := a.Archive.Message("c@s.whatsapp.net", "m"); err != nil || m.Text != "kept" {
		t.Fatal(m, err)
	}
}

func TestAccountIsolation(t *testing.T) {
	needSQLCipher(t)
	root := t.TempDir()
	keys := newKeys()
	one := openAccount(t, filepath.Join(root, "1"), keys)
	two := openAccount(t, filepath.Join(root, "2"), keys)
	defer two.Close()
	if err := one.Archive.SaveMessages([]Message{{ID: "same", ChatID: "c", Timestamp: 1, Text: "account one"}}); err != nil {
		t.Fatal(err)
	}
	if n, err := two.Archive.Count(); err != nil || n != 0 {
		t.Fatalf("account two sees %d messages: %v", n, err)
	}
	if len(keys.m) != 4 {
		t.Fatalf("%d keys for two accounts", len(keys.m))
	}
	seen := map[string]bool{}
	for _, k := range keys.m {
		seen[string(k)] = true
	}
	if len(seen) != 4 {
		t.Fatal("two accounts share a key")
	}
	one.Close()
	twoKeyName, _ := ArchiveKeyName(filepath.Join(root, "2"))
	if _, err := OpenArchive(filepath.Join(root, "1", ArchiveFile), keys.m[twoKeyName]); !errors.Is(err, sqlite.ErrWrongKey) {
		t.Fatalf("account two's key opened account one: %v", err)
	}
}

func TestMissingOrWrongKeyFailsClosed(t *testing.T) {
	needSQLCipher(t)
	dir := t.TempDir()
	keys := newKeys()
	a := openAccount(t, dir, keys)
	if err := a.Archive.SaveMessages([]Message{{ID: "m", ChatID: "c", Timestamp: 1, Text: "x"}}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	before := hashFiles(t, dir)
	archiveName, _ := ArchiveKeyName(dir)
	devicesName, _ := DevicesKeyName(dir)

	saved := keys.m[archiveName]
	delete(keys.m, archiveName)
	if _, err := OpenAccount(context.Background(), dir, keys, nil); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("missing archive key: %v", err)
	}
	if _, ok := keys.m[archiveName]; ok {
		t.Fatal("a new key replaced the missing one")
	}
	keys.m[archiveName] = bytes.Repeat([]byte{1}, 32)
	if _, err := OpenAccount(context.Background(), dir, keys, nil); !errors.Is(err, sqlite.ErrWrongKey) {
		t.Fatalf("wrong archive key: %v", err)
	}
	keys.m[archiveName] = saved[:16]
	if _, err := OpenAccount(context.Background(), dir, keys, nil); !errors.Is(err, ErrKeyInvalid) {
		t.Fatalf("short archive key: %v", err)
	}
	keys.m[archiveName] = saved
	keys.m[devicesName] = bytes.Repeat([]byte{2}, 32)
	if _, err := OpenAccount(context.Background(), dir, keys, nil); !errors.Is(err, sqlite.ErrWrongKey) {
		t.Fatalf("wrong whatsmeow key: %v", err)
	}
	if !sameFiles(before, hashFiles(t, dir)) {
		t.Fatal("a failed open changed the account's files")
	}
}

// The Rust archive shares the archive key: with it encrypted on disk and
// the key gone, no new key is made over it.
func TestRustArchiveProtectsItsKey(t *testing.T) {
	needSQLCipher(t)
	dir := t.TempDir()
	db, err := sqlite.Open(sqlite.Config{Path: filepath.Join(dir, rustArchiveFile), Key: bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE chats(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	keys := newKeys()
	if _, err := OpenAccount(context.Background(), dir, keys, nil); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("got %v", err)
	}
	if len(keys.m) != 0 {
		t.Fatal("a key was created")
	}
}

// ArchiveKeyName is the Rust core's identity_for: "archive-" and the hex
// SHA-256 of the canonical folder path.
func TestKeyNamesMatchTheRustCore(t *testing.T) {
	// sha256("/"), the canonical path of the root.
	if name, err := ArchiveKeyName("/"); err != nil || name != "archive-8a5edab282632443219e051e4ade2d1d5bbc671c781051bf1437897cbdfea0f1" {
		t.Fatalf("root: %q %v", name, err)
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "accounts", "1")
	os.MkdirAll(real, 0o700)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "accounts"), link); err != nil {
		t.Fatal(err)
	}
	a, err := ArchiveKeyName(real)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ArchiveKeyName(filepath.Join(link, "1"))
	if err != nil || a != b {
		t.Fatalf("a symlinked path names another key: %q %q %v", a, b, err)
	}
	d, _ := DevicesKeyName(real)
	if !strings.HasPrefix(d, "session-") || d[len("session-"):] != a[len("archive-"):] {
		t.Fatalf("devices key name %q", d)
	}
}
