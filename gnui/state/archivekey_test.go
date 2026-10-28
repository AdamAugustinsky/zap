package state_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gonativeui/app/secret"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// keyring is a fake OS keyring of archive keys: never the real Keychain.
type keyring struct {
	mu      sync.Mutex
	items   map[string]bool
	deleted []string
}

func (k *keyring) Delete(account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.deleted = append(k.deleted, account)
	if !k.items[account] {
		return secret.ErrNotFound
	}
	delete(k.items, account)
	return nil
}

func (k *keyring) has(account string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.items[account]
}

// removeSecond links two accounts over settings in dir, gives the second an
// archive and its key, removes it and waits for the removal to finish.
func removeSecond(t *testing.T, dir string, prepare func(folder string)) (*keyring, string) {
	t.Helper()
	l, ok := state.LoadSettings(state.Paths{Config: dir, Data: dir, Cache: filepath.Join(dir, "cache")}).Wait(5 * time.Second)
	if !ok {
		t.Fatal("not loaded")
	}
	s := state.New()
	woke := make(chan struct{}, 4)
	s.Wake = func() { woke <- struct{}{} }
	s.UseSettings(l)
	t.Cleanup(s.CloseSettings)
	one, two := fake.New(nil), fake.New(nil)
	s.AddAccount("1", nil, one)
	s.AddAccount(s.NextAccountID(), nil, two)
	s.Pump(0)
	folder := filepath.Join(dir, "accounts", "2")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(folder, "archive.db"), []byte("x"), 0o600)
	id, err := state.ArchiveKeyIdentity(folder)
	if err != nil {
		t.Fatal(err)
	}
	keys := &keyring{items: map[string]bool{id: true, "archive-of-account-one": true}}
	s.ArchiveKeys = keys
	if prepare != nil {
		prepare(folder)
	}
	s.RemoveAccount(1)
	two.Emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
	s.Pump(0)
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("the removal never finished")
	}
	if !keys.has("archive-of-account-one") {
		t.Error("another account's key was deleted")
	}
	return keys, id
}

// The identity is the core's: "archive-" and the SHA-256 of the canonical
// folder (zf:src/archive/encryption.rs identity_for).
func TestArchiveKeyIdentityIsTheCores(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(real))
	if got, _ := state.ArchiveKeyIdentity(dir); got != "archive-"+hex.EncodeToString(sum[:]) {
		t.Errorf("identity %q", got)
	}
	if _, err := state.ArchiveKeyIdentity(filepath.Join(dir, "gone")); err == nil {
		t.Error("a missing folder has an identity")
	}
}

// A removed account's archive key goes once its archive is gone
// (zf:src/app.rs:1771).
func TestRemovingAnAccountDeletesItsArchiveKey(t *testing.T) {
	keys, id := removeSecond(t, t.TempDir(), nil)
	if keys.has(id) {
		t.Errorf("the removed account's archive key is kept; deleted %v", keys.deleted)
	}
}

// When the archive could not be deleted, its key stays: the archive would
// be unreadable without it.
func TestAFailedRemovalKeepsTheArchiveKey(t *testing.T) {
	keys, id := removeSecond(t, t.TempDir(), func(folder string) {
		os.Chmod(folder, 0o500) // its files cannot be unlinked
		t.Cleanup(func() { os.Chmod(folder, 0o700) })
	})
	if !keys.has(id) {
		t.Error("the key of an archive still on disk was deleted")
	}
}
