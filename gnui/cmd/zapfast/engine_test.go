package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"gonativeui/app/secret"

	"zapfast/core"
	"zapfast/gocore/demo"
	"zapfast/gocore/migrate"
	"zapfast/gocore/store"
	"zapfast/gocore/store/sqlite"
	"zapfast/gocore/wa"
)

func TestTheEngineIsTheFlagsThenTheEnvironments(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "ZAPFAST_ENGINE" {
				return v
			}
			return ""
		}
	}
	for _, c := range []struct{ flag, env, want string }{
		{"", "", ""}, {"", "go", engineGo}, {"", "rust", engineRust}, {"", "zfcore", ""},
		{"go", "rust", engineGo}, {"rust", "go", engineRust},
	} {
		if got := chooseEngine(c.flag, env(c.env)); got != c.want {
			t.Errorf("-engine=%q, $ZAPFAST_ENGINE=%q: %q, want %q", c.flag, c.env, got, c.want)
		}
	}
	for _, c := range []struct {
		args []string
		want string
	}{{[]string{"-engine=go"}, engineGo}, {[]string{"-engine", "rust"}, engineRust}, {[]string{"--engine=go", "--start-hidden"}, engineGo}, {nil, ""}} {
		if o, err := parseOptions(c.args); err != nil || o.engine != c.want {
			t.Errorf("%q: %q, %v", c.args, o.engine, err)
		}
	}
	if _, err := parseOptions([]string{"-engine=zfcore"}); err == nil {
		t.Error("an unknown engine was accepted")
	}
}

// Without a choice, an account keeps the backend its folder belongs to,
// and a new one runs on Go; a choice applies to every account.
func TestEachAccountKeepsItsBackendAndNewOnesRunOnGo(t *testing.T) {
	root := t.TempDir()
	folder := func(name string, files ...string) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	rust := folder("rust", migrate.SessionFile, migrate.RustArchiveFile)
	gone := folder("go", store.DevicesFile, store.ArchiveFile)
	moving := folder("moving", migrate.SessionFile, migrate.MarkerFile)
	fresh := filepath.Join(root, "new") // not created yet
	for _, c := range []struct {
		choice, dir, want string
		migrate           bool
	}{
		{"", rust, engineRust, true}, {"", gone, engineGo, false}, {"", moving, engineGo, true}, {"", fresh, engineGo, false},
		{engineGo, rust, engineGo, true}, {engineRust, gone, engineRust, false},
	} {
		if got := accountEngine(c.choice, c.dir); got != c.want {
			t.Errorf("%q on %s: %q, want %q", c.choice, filepath.Base(c.dir), got, c.want)
		}
		if got := needsMigration(c.dir); got != c.migrate {
			t.Errorf("%s needs a migration: %v, want %v", filepath.Base(c.dir), got, c.migrate)
		}
	}
}

func TestTheDemoRunsOnTheChosenEngine(t *testing.T) {
	o := &coreOpener{demo: true}
	p, conn := o.open("1")
	if conn != nil || p == nil || p.Timing.Engine != engineGo {
		t.Fatalf("the default demo: %v, %+v", conn, p)
	}
	if !p.Wait(30 * time.Second) {
		t.Fatal("the Go demo did not open")
	}
	c, err := p.Conn()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(5 * time.Second)
	if _, ok := c.(*demo.Conn); !ok {
		t.Fatalf("the default demo is a %T", c)
	}
	if _, ok := c.(core.Frames); !ok {
		t.Error("the Go demo has no frames")
	}
	o.choice = engineRust
	if p, _ := o.open("1"); p.Timing.Engine != "rust" {
		t.Errorf("-engine=rust opened %q", p.Timing.Engine)
	}
}

// ---- taking an account over from the Rust core ----

// memKeys is a Keychain in memory.
type memKeys struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *memKeys) Get(name string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[name]
	if !ok {
		return nil, secret.ErrNotFound
	}
	return bytes.Clone(v), nil
}

func (k *memKeys) Set(name string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[name] = bytes.Clone(v)
	return nil
}

// stubClient stands in for whatsmeow while the account opens: it never
// connects (the test never gives the startup permit), and records the
// device it was made over.
type stubClient struct {
	wa.Client // anything else panics: the open must not need it
	device    *wastore.Device
}

func (s *stubClient) OwnID() (types.JID, types.JID) {
	if s.device.ID == nil {
		return types.JID{}, types.JID{}
	}
	return s.device.ID.ToNonAD(), s.device.LID.ToNonAD()
}
func (s *stubClient) PushName() string          { return "" }
func (s *stubClient) AddEventHandler(func(any)) {}
func (s *stubClient) Disconnect()               {}
func (s *stubClient) IsConnected() bool         { return false }

const fixtures = "../../gocore/migrate/testdata"

// rustFolder is a synthetic account folder as the Rust core leaves it (the
// migration fixtures, copied into a temporary folder), and a Keychain
// holding its archive key.
func rustFolder(t *testing.T) (root, dir string, keys *memKeys) {
	t.Helper()
	if _, err := sqlite.Load(); err != nil {
		if errors.Is(err, sqlite.ErrNoLibrary) {
			t.Skipf("%v; build it with packaging/macos/sqlcipher/build.sh", err)
		}
		t.Fatal(err)
	}
	root = t.TempDir()
	dir = filepath.Join(root, "state", "accounts", "1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{migrate.SessionFile, migrate.RustArchiveFile} {
		copyFixture(t, filepath.Join(fixtures, name), filepath.Join(dir, name))
	}
	var f struct {
		ArchiveKey string `json:"archive_key"`
	}
	b, err := os.ReadFile(filepath.Join(fixtures, "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	key, err := hex.DecodeString(f.ArchiveKey)
	if err != nil {
		t.Fatal(err)
	}
	name, err := store.ArchiveKeyName(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root, dir, &memKeys{m: map[string][]byte{name: key}}
}

func copyFixture(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// goOpener opens real accounts under root on the Go backend, with keys and
// the stub client; devices receives each device a client was made over.
func goOpener(root string, keys store.Keys, locked bool) (*coreOpener, chan *wastore.Device) {
	devices := make(chan *wastore.Device, 4)
	return &coreOpener{
		choice: engineGo, stateDir: filepath.Join(root, "state"), cacheDir: filepath.Join(root, "cache"),
		keys:      keys,
		holdsLock: func() bool { return locked },
		newClient: func(d *wastore.Device) wa.Client {
			devices <- d
			return &stubClient{device: d}
		},
	}, devices
}

func openNow(t *testing.T, o *coreOpener) (core.Conn, error) {
	t.Helper()
	p, conn := o.open("1")
	if conn != nil || p.Timing.Engine != engineGo {
		t.Fatalf("not opening on Go: %v, %+v", conn, p.Timing)
	}
	if !p.Wait(60 * time.Second) {
		t.Fatal("the open did not end")
	}
	return p.Conn()
}

func linkedDevice(t *testing.T, devices chan *wastore.Device) *wastore.Device {
	t.Helper()
	select {
	case d := <-devices:
		return d
	case <-time.After(30 * time.Second):
		t.Fatal("the engine made no client")
	}
	return nil
}

// -engine=go takes the Rust core's account over before opening it, then
// opens it on the converted session; a second open finds it done.
func TestTheGoEngineTakesARustAccountOver(t *testing.T) {
	root, dir, keys := rustFolder(t)
	o, devices := goOpener(root, keys, true)
	c, err := openNow(t, o)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := c.(*goConn)
	if !ok {
		t.Fatalf("a %T", c)
	}
	if _, ok := c.(core.Frames); ok {
		t.Error("the Go engine claims video frames while it refuses calls")
	}
	if n := g.CallGeneration(); n == 0 || g.CallGeneration() == n {
		t.Error("call numbers repeat")
	}
	if d := linkedDevice(t, devices); d.ID == nil || d.ID.User == "" {
		t.Error("the engine opened an unlinked device, not the converted session")
	}
	if err := c.Close(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{migrate.SessionFile: false, store.DevicesFile: true, store.ArchiveFile: true, migrate.RustArchiveFile: true, migrate.MarkerFile: true} {
		if got := exists(filepath.Join(dir, name)); got != want {
			t.Errorf("%s exists: %v, want %v", name, got, want)
		}
	}
	if accountEngine("", dir) != engineGo {
		t.Error("the migrated account is not the Go backend's by default")
	}
	// Again: the marker says it is done.
	c, err = openNow(t, o)
	if err != nil {
		t.Fatal(err)
	}
	if d := linkedDevice(t, devices); d.ID == nil {
		t.Error("the second open lost the session")
	}
	c.Close(10 * time.Second)
}

// Without the single-instance lock nothing is taken over, and the account
// shows why.
func TestNoMigrationWithoutTheInstanceLock(t *testing.T) {
	root, dir, keys := rustFolder(t)
	o, _ := goOpener(root, keys, false)
	_, err := openNow(t, o)
	var m *migrationError
	if !errors.As(err, &m) || !errors.Is(err, errNoInstance) || m.Relink {
		t.Fatalf("%v", err)
	}
	if !exists(filepath.Join(dir, migrate.SessionFile)) || exists(filepath.Join(dir, store.DevicesFile)) || exists(filepath.Join(dir, migrate.MarkerFile)) {
		t.Error("the folder changed")
	}
}

// A session store this build cannot convert fails with a typed error,
// changes nothing, and Try again links the account afresh on Go.
func TestAnUnconvertibleSessionIsLinkedAfreshOnTryAgain(t *testing.T) {
	root, dir, keys := rustFolder(t)
	session := filepath.Join(dir, migrate.SessionFile)
	if err := os.Remove(session); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(sqlite.Config{Path: session, Plaintext: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE something_else (x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	o, devices := goOpener(root, keys, true)
	_, err = openNow(t, o)
	var m *migrationError
	if !errors.As(err, &m) || !m.Relink || !errors.Is(err, migrate.ErrUnknownSchema) {
		t.Fatalf("%v", err)
	}
	if exists(filepath.Join(dir, store.DevicesFile)) || exists(filepath.Join(dir, migrate.MarkerFile)) {
		t.Fatal("a failed migration left its files")
	}
	c, err := openNow(t, o)
	if err != nil {
		t.Fatal(err)
	}
	if d := linkedDevice(t, devices); d.ID != nil {
		t.Error("linking afresh opened a linked device")
	}
	c.Close(10 * time.Second)
	if !exists(session) || !exists(filepath.Join(dir, store.DevicesFile)) {
		t.Error("linking afresh must keep the Rust files and make whatsmeow's store")
	}
}
