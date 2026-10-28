package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"gonativeui/app/secret"

	"zapfast/gocore/store/sqlite"
)

// fixtures is testdata/fixtures.json: what zfcore/tests/migration_fixtures.rs
// (zapfast_core over whatsapp-rust 5e72baf) wrote, all synthetic, with the
// session and archive beside it. They are committed data: the generator
// went with the Rust tree in WP9, so they can no longer be regenerated here.
type fixtures struct {
	ArchiveKey string `json:"archive_key"`
	Accounts   struct {
		Ours, Peer struct{ PN, LID string }
		Third      struct{ PN string }
		Group      string
	} `json:"accounts"`
	Archive struct {
		Labels []string `json:"labels"`
	} `json:"archive"`
	Messages struct {
		DMSkipped    [][2]string `json:"dm_skipped"`
		GroupSkipped [2]string   `json:"group_skipped"`
	} `json:"messages"`
	Files map[string]string `json:"files"`
}

var loadFixtures = sync.OnceValues(func() (fixtures, error) {
	var f fixtures
	data, err := os.ReadFile("testdata/fixtures.json")
	if err == nil {
		err = json.Unmarshal(data, &f)
	}
	return f, err
})

func testFixtures(t testing.TB) fixtures {
	t.Helper()
	f, err := loadFixtures()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixtures) archiveKey(t testing.TB) []byte {
	key, err := hex.DecodeString(f.ArchiveKey)
	if err != nil || len(key) != 32 {
		t.Fatalf("archive key: %v", err)
	}
	return key
}

// needSQLCipher skips a test when libsqlcipher.dylib is not built.
func needSQLCipher(t testing.TB) {
	t.Helper()
	if _, err := sqlite.Load(); err != nil {
		if errors.Is(err, sqlite.ErrNoLibrary) {
			t.Skipf("%v; build it with packaging/macos/sqlcipher/build.sh", err)
		}
		t.Fatal(err)
	}
}

// fixtureCopy copies testdata/name to dir/as and checks it is the fixture
// the generator recorded.
func fixtureCopy(t testing.TB, name, dir, as string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if want := testFixtures(t).Files[name]; hex.EncodeToString(sum[:]) != want {
		t.Fatalf("testdata/%s is not the generated fixture", name)
	}
	path := filepath.Join(dir, as)
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// tree hashes every file under dir by its path relative to dir.
func tree(t testing.TB, dir string) map[string]string {
	t.Helper()
	sums := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		h, err := fileHash(context.Background(), path)
		sums[rel] = h
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return sums
}

func sameTree(a, b map[string]string) bool {
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

// alter changes a plaintext SQLite file (a copy of a fixture).
func alter(t testing.TB, path string, statements ...string) {
	t.Helper()
	db, err := sqlite.Open(sqlite.Config{Path: path, Plaintext: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range statements {
		if _, err = db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if _, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
}

// memKeys is a Keychain in memory.
type memKeys struct {
	mu   sync.Mutex
	m    map[string][]byte
	fail error
}

func newKeys() *memKeys { return &memKeys{m: map[string][]byte{}} }

func (k *memKeys) Get(account string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fail != nil {
		return nil, k.fail
	}
	v, ok := k.m[account]
	if !ok {
		return nil, secret.ErrNotFound
	}
	return bytes.Clone(v), nil
}

func (k *memKeys) Set(account string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fail != nil {
		return k.fail
	}
	k.m[account] = bytes.Clone(v)
	return nil
}
