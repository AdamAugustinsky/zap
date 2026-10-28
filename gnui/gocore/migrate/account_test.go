package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zapfast/gocore/store"
	"zapfast/gocore/store/sqlite"
)

// crashPoints are every point Account can stop at, in order.
var crashPoints = []string{
	"backup-copied", "backup-renamed", StateBackedUp, "session-staged", "archive-staged",
	StateStaged, "published-" + store.DevicesFile, "published-" + store.ArchiveFile,
	StateConverted, "retiring-" + SessionFile, "retired",
}

// rustAccount is an account folder as the Rust core leaves it, with the
// fixtures and a few files of its own, and a Keychain holding the archive
// key under the folder's name.
func rustAccount(t *testing.T) (string, *memKeys) {
	t.Helper()
	needSQLCipher(t)
	dir := filepath.Join(t.TempDir(), "accounts", "1")
	if err := os.MkdirAll(filepath.Join(dir, "stickers"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixtureCopy(t, "session.db", dir, SessionFile)
	fixtureCopy(t, "archive.db", dir, RustArchiveFile)
	for name, data := range map[string]string{"settings.json": `{"synthetic":true}`, "stickers/synthetic.webp": "RIFF synthetic"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keys := newKeys()
	name, err := store.ArchiveKeyName(dir)
	if err != nil {
		t.Fatal(err)
	}
	keys.m[name] = testFixtures(t).archiveKey(t)
	return dir, keys
}

func options(dir string, keys store.Keys) Options {
	return Options{Dir: dir, Keys: keys, Now: func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }}
}

// checkActive checks a migrated folder: the Go backend opens it with its
// Keychain keys, the Rust session store is retired unchanged into the
// backup, archive.db is untouched, and nothing of the step's is left over.
func checkActive(t *testing.T, dir string, keys *memKeys, original map[string]string, r Result) {
	t.Helper()
	if r.State != StateActive || r.Session == nil || r.Archive == nil || r.Session.Sessions != 2 {
		t.Fatalf("result %+v", r)
	}
	now := tree(t, dir)
	if now[RustArchiveFile] != original[RustArchiveFile] {
		t.Fatal("archive.db changed")
	}
	if _, ok := now[SessionFile]; ok {
		t.Fatal("session.db not retired")
	}
	if got := now[filepath.Join(r.Backup, RetiredDir, SessionFile)]; got != original[SessionFile] {
		t.Fatal("the retired session.db is not the original")
	}
	for name := range now {
		top := strings.SplitN(name, string(filepath.Separator), 2)[0]
		switch {
		case top == r.Backup, top == store.ArchiveFile, top == store.DevicesFile, top == MarkerFile, top == RustArchiveFile, top == "settings.json", top == "stickers":
		case strings.HasPrefix(top, BackupPrefix):
			// An orphan backup of a run that stopped before its marker.
		default:
			t.Fatalf("left over: %s", name)
		}
	}
	var manifest struct{ Files map[string]manifestFile }
	data, err := os.ReadFile(filepath.Join(dir, r.Backup, ManifestFile))
	if err == nil {
		err = json.Unmarshal(data, &manifest)
	}
	if err != nil || len(manifest.Files) != len(original) {
		t.Fatalf("manifest %v: %v", manifest.Files, err)
	}
	for name, sum := range original {
		if manifest.Files[filepath.ToSlash(name)].SHA256 != sum || now[filepath.Join(r.Backup, name)] != sum && name != SessionFile {
			t.Fatalf("backup of %s", name)
		}
	}
	// The Go backend's own open: both stores, keys from the Keychain.
	acct, err := store.OpenAccount(context.Background(), dir, keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer acct.Close()
	device, err := acct.Devices.GetFirstDevice(context.Background())
	if err != nil || device.ID == nil || device.ID.String() != testFixtures(t).Accounts.Ours.PN {
		t.Fatalf("device %v: %v", device, err)
	}
	if n, err := acct.Archive.Count(); err != nil || n != r.Archive.Messages {
		t.Fatalf("%d messages: %v", n, err)
	}
}

func TestAccount(t *testing.T) {
	dir, keys := rustAccount(t)
	original := tree(t, dir)
	r, err := Account(context.Background(), options(dir, keys))
	if err != nil {
		t.Fatal(err)
	}
	checkActive(t, dir, keys, original, r)
	// Again: nothing to do, nothing changes.
	before := tree(t, dir)
	again, err := Account(context.Background(), options(dir, keys))
	if err != nil || again.State != StateActive || again.Backup != r.Backup {
		t.Fatalf("%+v: %v", again, err)
	}
	if !sameTree(before, tree(t, dir)) {
		t.Fatal("a second run changed files")
	}
}

// A crash at any point leaves either the Rust account as it was or a state
// the next run completes, and archive.db is never changed.
func TestAccountCrashAtEveryPoint(t *testing.T) {
	for _, point := range crashPoints {
		t.Run(point, func(t *testing.T) {
			dir, keys := rustAccount(t)
			original := tree(t, dir)
			opts := options(dir, keys)
			hit := false
			opts.crash = func(p string) error {
				if p == point && !hit {
					hit = true
					return errCrash
				}
				return nil
			}
			if _, err := Account(context.Background(), opts); !errors.Is(err, errCrash) {
				t.Fatalf("got %v", err)
			}
			if !hit {
				t.Fatal("never reached")
			}
			now := tree(t, dir)
			if now[RustArchiveFile] != original[RustArchiveFile] {
				t.Fatal("archive.db changed")
			}
			if s, ok := now[SessionFile]; ok && s != original[SessionFile] {
				t.Fatal("session.db changed")
			}
			opts.crash = nil
			r, err := Account(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			checkActive(t, dir, keys, original, r)
		})
	}
}

func TestAccountFailsClosed(t *testing.T) {
	fail := func(t *testing.T, dir string, keys *memKeys, want error) {
		t.Helper()
		before := tree(t, dir)
		_, err := Account(context.Background(), options(dir, keys))
		if !errors.Is(err, want) {
			t.Fatalf("got %v, want %v", err, want)
		}
		if after := tree(t, dir); !sameTree(before, after) {
			t.Fatalf("files changed:\n%v\n%v", before, after)
		}
	}
	t.Run("a missing Keychain item", func(t *testing.T) {
		dir, keys := rustAccount(t)
		clear(keys.m)
		fail(t, dir, keys, store.ErrKeyMissing)
	})
	t.Run("a Keychain item of the wrong size", func(t *testing.T) {
		dir, keys := rustAccount(t)
		for k := range keys.m {
			keys.m[k] = []byte("short")
		}
		fail(t, dir, keys, store.ErrKeyInvalid)
	})
	t.Run("a locked Keychain", func(t *testing.T) {
		dir, keys := rustAccount(t)
		keys.fail = errors.New("synthetic: the Keychain is locked")
		fail(t, dir, keys, keys.fail)
	})
	t.Run("a wrong archive key", func(t *testing.T) {
		dir, keys := rustAccount(t)
		for k := range keys.m {
			keys.m[k] = bytes.Repeat([]byte{0x42}, 32)
		}
		fail(t, dir, keys, sqlite.ErrWrongKey)
	})
	t.Run("a prekey gap", func(t *testing.T) {
		dir, keys := rustAccount(t)
		alter(t, filepath.Join(dir, SessionFile), "DELETE FROM prekeys WHERE id=12")
		fail(t, dir, keys, ErrPreKeyGap)
	})
	t.Run("an unknown schema", func(t *testing.T) {
		dir, keys := rustAccount(t)
		alter(t, filepath.Join(dir, SessionFile), "INSERT INTO __diesel_schema_migrations(version) VALUES('20261101000000')")
		fail(t, dir, keys, ErrUnknownSchema)
	})
	t.Run("a Go store already there", func(t *testing.T) {
		dir, keys := rustAccount(t)
		if err := os.WriteFile(filepath.Join(dir, store.ArchiveFile), []byte("someone else's"), 0o600); err != nil {
			t.Fatal(err)
		}
		fail(t, dir, keys, ErrDestinationExists)
	})
	t.Run("no Rust session", func(t *testing.T) {
		dir, keys := rustAccount(t)
		if err := os.Remove(filepath.Join(dir, SessionFile)); err != nil {
			t.Fatal(err)
		}
		fail(t, dir, keys, ErrNoRustSession)
	})
}

// The Rust core runs between a crash and the next run: the converted
// copies are stale, so the step undoes itself and starts again later.
func TestAccountSourceChanged(t *testing.T) {
	for _, point := range []string{StateBackedUp, StateStaged, "published-" + store.DevicesFile, StateConverted} {
		t.Run(point, func(t *testing.T) {
			dir, keys := rustAccount(t)
			opts := options(dir, keys)
			opts.crash = func(p string) error {
				if p == point {
					return errCrash
				}
				return nil
			}
			if _, err := Account(context.Background(), opts); !errors.Is(err, errCrash) {
				t.Fatalf("got %v", err)
			}
			alter(t, filepath.Join(dir, SessionFile), "UPDATE device SET push_name='Synthetic Rust ran'")
			changed := tree(t, dir)
			opts.crash = nil
			if _, err := Account(context.Background(), opts); !errors.Is(err, ErrSourceChanged) {
				t.Fatalf("got %v", err)
			}
			after := tree(t, dir)
			for name, sum := range after {
				top := strings.SplitN(name, string(filepath.Separator), 2)[0]
				if top != RustArchiveFile && top != SessionFile && top != "settings.json" && top != "stickers" {
					t.Fatalf("left over: %s", name)
				}
				if changed[name] != sum {
					t.Fatalf("%s changed", name)
				}
			}
			r, err := Account(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			acct, err := store.OpenAccount(context.Background(), dir, keys, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer acct.Close()
			device, err := acct.Devices.GetFirstDevice(context.Background())
			if err != nil || device.PushName != "Synthetic Rust ran" || r.State != StateActive {
				t.Fatalf("not the newer store: %v %v", device, err)
			}
		})
	}
}
