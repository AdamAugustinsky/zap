package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"gonativeui/app/secret"
)

// One account's storage, in its own folder (state/accounts/<id>): the
// archive and whatsmeow's device and Signal store, each in its own
// SQLCipher file under its own Keychain key. Accounts share nothing: the
// key names derive from the folder's canonical path, as the Rust core's do.
const (
	// ArchiveFile is the Go archive, beside the Rust core's archive.db,
	// which it never writes.
	ArchiveFile = "archive-go.db"
	// DevicesFile is whatsmeow's store. Unlinking deletes it and keeps the
	// archive.
	DevicesFile = "whatsmeow.db"
	// rustArchiveFile is the Rust core's archive, under the same key.
	rustArchiveFile = "archive.db"
)

var (
	// ErrKeyMissing means a key is not in the Keychain while data it
	// encrypts is on disk. Nothing is created or changed: restore the
	// Keychain, or start the account over.
	ErrKeyMissing = errors.New("store: the Keychain has no key for data on disk")
	// ErrKeyInvalid means the Keychain holds a key that is not 32 bytes for
	// data on disk.
	ErrKeyInvalid = errors.New("store: the Keychain's key is not a 32-byte key")
)

// Keys keeps the per-account keys: the app's Keychain (Keychain) or, in
// tests, a map. Get returns secret.ErrNotFound for a key never set. Calls
// may block on the user: never call them on the UI thread.
type Keys interface {
	Get(account string) ([]byte, error)
	Set(account string, secret []byte) error
}

// Keychain is the Keychain's generic passwords under service, which is
// the app's keyring service (core.KeyringService, "ai.a3lab.zap").
func Keychain(service string) Keys { return secret.Open(service) }

// ArchiveKeyName is the Keychain account of the archive key of the account
// folder dir: "archive-" and the hex SHA-256 of the canonical path. It is
// the Rust core's name for the same folder (src/archive/encryption.rs
// identity_for), so the Go archive takes the key the Rust archive already
// has. The name holds a digest, never a path or a phone number.
func ArchiveKeyName(dir string) (string, error) { return keyName("archive-", dir) }

// DevicesKeyName is the Keychain account of whatsmeow's store key of the
// account folder dir: "session-" and the same digest. The Rust core keeps
// its session store unencrypted, so this key is new with the Go backend.
func DevicesKeyName(dir string) (string, error) { return keyName("session-", dir) }

func keyName(prefix, dir string) (string, error) {
	canonical, err := canonicalDir(dir)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return prefix + hex.EncodeToString(sum[:]), nil
}

// canonicalDir is dir absolute with its symlinks resolved, as Rust's
// canonicalize makes it.
func canonicalDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// accountKey returns the key named name, creating one when there is none
// and none of protects holds encrypted data. A new key is read back before
// anything is encrypted with it.
func accountKey(keys Keys, name string, protects ...string) ([]byte, error) {
	key, err := keys.Get(name)
	switch {
	case err == nil && len(key) == 32:
		return key, nil
	case err == nil:
		if p := encryptedData(protects); p != "" {
			return nil, fmt.Errorf("%w (%s, %d bytes)", ErrKeyInvalid, filepath.Base(p), len(key))
		}
	case errors.Is(err, secret.ErrNotFound):
		if p := encryptedData(protects); p != "" {
			return nil, fmt.Errorf("%w (%s)", ErrKeyMissing, filepath.Base(p))
		}
	default:
		return nil, fmt.Errorf("store: reading the key from the Keychain: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := keys.Set(name, key); err != nil {
		return nil, fmt.Errorf("store: saving a new key in the Keychain: %w", err)
	}
	saved, err := keys.Get(name)
	if err != nil {
		return nil, fmt.Errorf("store: reading back a new key: %w", err)
	}
	if !bytes.Equal(saved, key) {
		return nil, errors.New("store: the Keychain did not keep the new key")
	}
	return key, nil
}

// encryptedData returns the first of paths that holds data a key protects:
// a file that is not empty and not plain SQLite.
func encryptedData(paths []string) string {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		var h [16]byte
		n, _ := io.ReadFull(f, h[:])
		f.Close()
		if n > 0 && !bytes.Equal(h[:n], []byte("SQLite format 3\x00")[:n]) {
			return p
		}
	}
	return ""
}

// Account is one account's open storage.
type Account struct {
	// Dir is the account's folder.
	Dir     string
	Archive *Archive
	// Devices is whatsmeow's store, upgraded.
	Devices *sqlstore.Container
}

// OpenAccount opens (creating) the account folder dir's archive and
// whatsmeow store, with their keys from keys. A key that is missing for
// data on disk, or a wrong key, fails closed and changes nothing;
// sqlite.ErrWrongKey and ErrKeyMissing are matched with errors.Is. log may
// be nil.
func OpenAccount(ctx context.Context, dir string, keys Keys, log waLog.Logger) (*Account, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	canonical, err := canonicalDir(dir)
	if err != nil {
		return nil, err
	}
	archiveName, err := ArchiveKeyName(canonical)
	if err != nil {
		return nil, err
	}
	devicesName, err := DevicesKeyName(canonical)
	if err != nil {
		return nil, err
	}
	archivePath := filepath.Join(canonical, ArchiveFile)
	archiveKey, err := accountKey(keys, archiveName, archivePath, filepath.Join(canonical, rustArchiveFile))
	if err != nil {
		return nil, err
	}
	devicesPath := filepath.Join(canonical, DevicesFile)
	devicesKey, err := accountKey(keys, devicesName, devicesPath)
	if err != nil {
		return nil, err
	}
	archive, err := OpenArchive(archivePath, archiveKey)
	if err != nil {
		return nil, fmt.Errorf("store: archive: %w", err)
	}
	devices, err := OpenDevices(ctx, devicesPath, devicesKey, log)
	if err != nil {
		archive.Close()
		return nil, fmt.Errorf("store: whatsmeow store: %w", err)
	}
	return &Account{Dir: canonical, Archive: archive, Devices: devices}, nil
}

// Close closes the archive and the whatsmeow store.
func (a *Account) Close() error {
	return errors.Join(a.Devices.Close(), a.Archive.Close())
}

// devicesConns bounds the whatsmeow store's pool: WAL lets readers run
// beside the one writer, and transactions take the write lock at once
// (BEGIN IMMEDIATE), waiting out the busy timeout rather than failing.
const devicesConns = 4

// OpenDevices opens (creating) whatsmeow's device and Signal store at path,
// encrypted with key, and upgrades its schema. Foreign keys are on for
// every connection, as sqlstore requires.
func OpenDevices(ctx context.Context, path string, key []byte, log waLog.Logger) (*sqlstore.Container, error) {
	db, err := openDatabase(path, key, devicesConns)
	if err != nil {
		return nil, err
	}
	c := sqlstore.NewWithDB(db, "sqlite3", log)
	if err := c.Upgrade(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
