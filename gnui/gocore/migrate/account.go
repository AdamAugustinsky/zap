package migrate

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gonativeui/app/secret"

	"zapfast/gocore/store"
)

// The files of one account folder (state/accounts/<id>) the step reads and
// writes.
const (
	// SessionFile is the Rust core's whatsapp-rust store, retired into the
	// backup once the Go store is in place.
	SessionFile = "session.db"
	// RustArchiveFile is the Rust core's archive. It is only ever read.
	RustArchiveFile = "archive.db"
	// MarkerFile records where the step got to (marker).
	MarkerFile = "migration.json"
	// BackupPrefix starts the backup folder's name; the UTC time follows.
	BackupPrefix = "rust-backup-"
	// ManifestFile, in the backup, lists every file copied with its hash.
	ManifestFile = "manifest.json"
	// RetiredDir, in the backup, receives the retired session store.
	RetiredDir = "retired"

	stageDir      = ".zap-migrate-stage"
	partialSuffix = ".partial"
)

// The step's states, in order, as the marker records them.
const (
	// StateBackedUp: the backup is complete; nothing else is written.
	StateBackedUp = "backed-up"
	// StateStaged: both stores are converted and verified in staging.
	StateStaged = "staged"
	// StateConverted: the Go stores are published beside the Rust ones.
	StateConverted = "converted"
	// StateActive: the Rust session store is retired; the Go backend owns
	// the account.
	StateActive = "active"
)

var (
	// ErrNoRustSession means the folder holds no Rust session store to
	// take over: link the account afresh.
	ErrNoRustSession = errors.New("migrate: the account has no Rust session store")
	// errCrash is what a test's crash point returns: the run stops where
	// it is, as if the process died, and cleans nothing up.
	errCrash = errors.New("migrate: simulated crash")
)

var backupName = regexp.MustCompile(`^` + BackupPrefix + `\d{8}T\d{6}Z(-\d+)?$`)

// Options are one account's migration.
type Options struct {
	// Dir is the account's folder.
	Dir string
	// Keys is the app's Keychain (store.Keychain): the archive key is
	// read, and whatsmeow's store gets a new key of its own.
	Keys store.Keys
	// Now names the backup; nil is time.Now.
	Now func() time.Time

	// crash, in tests, is called at each crash point; an error stops the
	// run there as a crash would.
	crash func(point string) error
}

// Result is what the step did. Reports hold counts only.
type Result struct {
	State string `json:"state"`
	// Backup is the backup folder's name, in the account folder.
	Backup  string         `json:"backup"`
	Session *SessionReport `json:"session,omitempty"`
	// Archive is nil when the account had no Rust archive.
	Archive *ArchiveReport `json:"archive,omitempty"`
}

// marker is MarkerFile: the step's state and what it checked, so a run
// after a crash resumes rather than redoes, and never acts on files that
// changed since. It holds hashes and counts, never keys.
type marker struct {
	Version int    `json:"version"`
	State   string `json:"state"`
	Backup  string `json:"backup"`
	// Sources are the SHA-256 of the Rust files as backed up.
	Sources map[string]string `json:"sources"`
	// Staged are the SHA-256 of the converted stores.
	Staged  map[string]string `json:"staged,omitempty"`
	Session *SessionReport    `json:"session,omitempty"`
	Archive *ArchiveReport    `json:"archive,omitempty"`
	Updated string            `json:"updated"`
}

// tracked are the Rust files whose contents the step depends on.
var tracked = []string{SessionFile, SessionFile + "-wal", RustArchiveFile, RustArchiveFile + "-wal"}

// account is one run.
type account struct {
	Options
	ctx                    context.Context
	dir                    string
	archiveKey, devicesKey []byte
	hasArchive             bool
	outputs                []string
	m                      *marker
}

// Account takes the account in opts.Dir over from the Rust core, in one
// step that is idempotent and survives a crash at any point:
//
//  1. Back up: the whole folder (but caches, which stay where they are) is
//     copied into BackupPrefix+<UTC time>, with a SHA-256 manifest.
//  2. Convert the backed-up session store (ConvertSession) and archive
//     (ImportArchive) into staging, each verified there.
//  3. Publish whatsmeow.db and archive-go.db beside the Rust files,
//     never replacing a file.
//  4. Retire session.db (and its -wal, -shm) by renaming it into the
//     backup's RetiredDir, so no Rust build resumes the forked ratchet.
//
// MarkerFile records each step. archive.db is only ever read. A failure
// before the stores are published leaves the folder as it was; the
// Keychain may keep whatsmeow's new key, which a later run reuses. If the
// Rust files change while the step runs (the Rust core ran), it stops with
// ErrSourceChanged and undoes its own work. Missing or wrong keys fail with
// store.ErrKeyMissing, store.ErrKeyInvalid or sqlite.ErrWrongKey, before
// anything is written.
//
// The caller holds the app's single-instance lock, has no Rust core open on
// the account, and connects only after StateActive.
func Account(ctx context.Context, opts Options) (Result, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	abs, err := filepath.Abs(opts.Dir)
	if err != nil {
		return Result{}, err
	}
	dir, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Result{}, err
	}
	a := &account{Options: opts, ctx: ctx, dir: dir, outputs: []string{store.DevicesFile}}
	if a.m, err = a.readMarker(); err != nil {
		return Result{}, err
	}
	if a.m != nil && a.m.State == StateActive {
		return a.result(), nil
	}
	if err = a.keys(); err != nil {
		return Result{}, err
	}
	if a.hasArchive {
		a.outputs = append(a.outputs, store.ArchiveFile)
	}
	if err = a.run(); err != nil {
		if !errors.Is(err, errCrash) {
			err = errors.Join(err, a.undo())
		}
		return a.result(), err
	}
	return a.result(), nil
}

func (a *account) path(name string) string { return filepath.Join(a.dir, name) }

func (a *account) at(point string) error {
	if a.crash != nil {
		return a.crash(point)
	}
	return nil
}

func (a *account) result() Result {
	if a.m == nil {
		return Result{}
	}
	return Result{State: a.m.State, Backup: a.m.Backup, Session: a.m.Session, Archive: a.m.Archive}
}

// keys reads the archive key and makes (or reuses) whatsmeow's store key,
// before anything is written to the folder.
func (a *account) keys() error {
	archiveName, err := store.ArchiveKeyName(a.dir)
	if err != nil {
		return err
	}
	devicesName, err := store.DevicesKeyName(a.dir)
	if err != nil {
		return err
	}
	if a.hasArchive, err = exists(a.path(RustArchiveFile)); err != nil {
		return err
	}
	if a.hasArchive {
		key, err := a.Keys.Get(archiveName)
		switch {
		case errors.Is(err, secret.ErrNotFound):
			return fmt.Errorf("%w (%s)", store.ErrKeyMissing, RustArchiveFile)
		case err != nil:
			return fmt.Errorf("migrate: reading the archive key: %w", err)
		case len(key) != 32:
			return fmt.Errorf("%w (%s, %d bytes)", store.ErrKeyInvalid, RustArchiveFile, len(key))
		}
		a.archiveKey = key
	}
	key, err := a.Keys.Get(devicesName)
	switch {
	case err == nil && len(key) == 32:
		a.devicesKey = key
		return nil
	case err == nil:
		if published, _ := exists(a.path(store.DevicesFile)); published {
			return fmt.Errorf("%w (%s, %d bytes)", store.ErrKeyInvalid, store.DevicesFile, len(key))
		}
	case !errors.Is(err, secret.ErrNotFound):
		return fmt.Errorf("migrate: reading the session key: %w", err)
	}
	if published, err := exists(a.path(store.DevicesFile)); err != nil || published {
		// A store on disk whose key is gone: never encrypt anew over it.
		return errors.Join(fmt.Errorf("%w (%s)", store.ErrKeyMissing, store.DevicesFile), err)
	}
	key = make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return err
	}
	if err = a.Keys.Set(devicesName, key); err != nil {
		return fmt.Errorf("migrate: saving the session key: %w", err)
	}
	saved, err := a.Keys.Get(devicesName)
	if err != nil || !bytes.Equal(saved, key) {
		return errors.Join(errors.New("migrate: the Keychain did not keep the session key"), err)
	}
	a.devicesKey = key
	return nil
}

func (a *account) run() error {
	if a.m == nil {
		if err := a.start(); err != nil {
			return err
		}
	}
	for {
		var err error
		switch a.m.State {
		case StateBackedUp:
			err = a.stage()
		case StateStaged:
			err = a.publish()
		case StateConverted:
			err = a.retire()
		case StateActive:
			return nil
		default:
			return fmt.Errorf("migrate: %s holds an unknown state %q", MarkerFile, a.m.State)
		}
		if err != nil {
			return err
		}
	}
}

// start checks there is something to take over and backs it up.
func (a *account) start() error {
	if found, err := exists(a.path(SessionFile)); err != nil {
		return err
	} else if !found {
		return ErrNoRustSession
	}
	for _, name := range a.outputs {
		if found, err := exists(a.path(name)); err != nil || found {
			return errors.Join(fmt.Errorf("%w: %s", ErrDestinationExists, name), err)
		}
	}
	// What a crashed run left before it recorded anything is its own.
	if err := a.removeOwn(); err != nil {
		return err
	}
	name, err := a.backup()
	if err != nil {
		return err
	}
	sources := map[string]string{}
	for _, file := range tracked {
		if found, _ := exists(filepath.Join(a.dir, name, file)); found {
			if sources[file], err = fileHash(a.ctx, filepath.Join(a.dir, name, file)); err != nil {
				return err
			}
		}
	}
	a.m = &marker{Version: 1, State: StateBackedUp, Backup: name, Sources: sources}
	if err = a.writeMarker(); err != nil {
		return err
	}
	return a.at(StateBackedUp)
}

// removeOwn removes the staging and partial backup folders of an earlier
// run that stopped before writing its marker.
func (a *account) removeOwn() error {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if name == stageDir || strings.HasPrefix(name, "."+BackupPrefix) && strings.HasSuffix(name, partialSuffix) {
			if err = os.RemoveAll(a.path(name)); err != nil {
				return err
			}
		}
	}
	return nil
}

type manifestFile struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// backup copies the folder, but for earlier backups, staging and the Go
// stores, into a new backup folder with a manifest, and returns its name.
// It is written under a partial name and renamed when complete.
func (a *account) backup() (string, error) {
	base := BackupPrefix + a.Now().UTC().Format("20060102T150405Z")
	name := base
	for i := 2; ; i++ {
		if found, err := exists(a.path(name)); err != nil {
			return "", err
		} else if !found {
			break
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	partial := a.path("." + name + partialSuffix)
	if err := os.Mkdir(partial, 0o700); err != nil {
		return "", err
	}
	files := map[string]manifestFile{}
	err := filepath.WalkDir(a.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(a.dir, path)
		if err != nil || rel == "." {
			return err
		}
		top := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if top == stageDir || top == MarkerFile || top == MarkerFile+".tmp" || backupName.MatchString(top) || strings.HasPrefix(top, "."+BackupPrefix) || top == store.DevicesFile || top == store.ArchiveFile || strings.HasPrefix(top, store.ArchiveFile+"-") || strings.HasPrefix(top, store.DevicesFile+"-") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(partial, rel)
		switch {
		case d.IsDir():
			return os.Mkdir(target, 0o700)
		case d.Type().IsRegular():
			sum, err := copyFile(a.ctx, path, target, 0)
			if err != nil {
				return err
			}
			info, err := os.Stat(target)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = manifestFile{Size: info.Size(), SHA256: fmt.Sprintf("%x", sum)}
			return nil
		default:
			// Links and the like are not followed; the manifest says so.
			files[filepath.ToSlash(rel)] = manifestFile{Size: -1}
			return nil
		}
	})
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(struct {
		Created string                  `json:"created"`
		Files   map[string]manifestFile `json:"files"`
	}{a.Now().UTC().Format(time.RFC3339), files}, "", "  ")
	if err != nil {
		return "", err
	}
	if err = writeFileSync(filepath.Join(partial, ManifestFile), data); err != nil {
		return "", err
	}
	if err = syncTree(partial); err != nil {
		return "", err
	}
	if err = a.at("backup-copied"); err != nil {
		return "", err
	}
	if err = os.Rename(partial, a.path(name)); err != nil {
		return "", err
	}
	if err = syncDir(a.dir); err != nil {
		return "", err
	}
	return name, a.at("backup-renamed")
}

// unchanged reports whether the Rust files are still the ones backed up.
func (a *account) unchanged() error {
	for _, file := range tracked {
		path := a.path(file)
		if file == SessionFile || file == SessionFile+"-wal" {
			if found, _ := exists(path); !found && a.m.State == StateConverted {
				// Already retired.
				path = filepath.Join(a.dir, a.m.Backup, RetiredDir, file)
			}
		}
		found, err := exists(path)
		if err != nil {
			return err
		}
		want, tracked := a.m.Sources[file]
		if !found {
			if tracked {
				return ErrSourceChanged
			}
			continue
		}
		if !tracked {
			return ErrSourceChanged
		}
		got, err := fileHash(a.ctx, path)
		if err != nil {
			return err
		}
		if got != want {
			return ErrSourceChanged
		}
	}
	return nil
}

// stage converts the backed-up stores into staging and verifies them.
func (a *account) stage() error {
	if err := a.unchanged(); err != nil {
		return err
	}
	stage := a.path(stageDir)
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.Mkdir(stage, 0o700); err != nil {
		return err
	}
	backup := a.path(a.m.Backup)
	session, err := convertSessionInto(a.ctx, filepath.Join(backup, SessionFile), filepath.Join(stage, store.DevicesFile), a.devicesKey)
	if err != nil {
		return err
	}
	a.m.Session = &session
	if err = a.at("session-staged"); err != nil {
		return err
	}
	if a.hasArchive {
		archive, err := importArchiveInto(a.ctx, filepath.Join(backup, RustArchiveFile), a.archiveKey, a.archiveKey, filepath.Join(stage, store.ArchiveFile))
		if err != nil {
			return err
		}
		a.m.Archive = &archive
		if err = a.at("archive-staged"); err != nil {
			return err
		}
	}
	// Only the stores themselves stay staged.
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if contains(a.outputs, e.Name()) {
			continue
		}
		// A log not checkpointed would hold data the published file lacks.
		if info, err := e.Info(); err != nil || strings.HasSuffix(e.Name(), "-wal") && info.Size() > 0 {
			return errors.Join(fmt.Errorf("migrate: staging left %s with data", e.Name()), err)
		}
		if err = os.RemoveAll(filepath.Join(stage, e.Name())); err != nil {
			return err
		}
	}
	a.m.Staged = map[string]string{}
	for _, name := range a.outputs {
		if a.m.Staged[name], err = fileHash(a.ctx, filepath.Join(stage, name)); err != nil {
			return err
		}
	}
	if err = syncTree(stage); err != nil {
		return err
	}
	// The Rust core did not run while the copies were converted.
	if err = a.unchanged(); err != nil {
		return err
	}
	a.m.State = StateStaged
	if err = a.writeMarker(); err != nil {
		return err
	}
	return a.at(StateStaged)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// publish links the staged stores into the folder, never replacing a file.
func (a *account) publish() error {
	if err := a.unchanged(); err != nil {
		return err
	}
	for _, name := range a.outputs {
		final, staged := a.path(name), filepath.Join(a.path(stageDir), name)
		if found, err := exists(final); err != nil {
			return err
		} else if found {
			// Published by a run that stopped: it must be what was staged.
			if got, err := fileHash(a.ctx, final); err != nil || got != a.m.Staged[name] {
				return errors.Join(fmt.Errorf("%w: %s", ErrDestinationExists, name), err)
			}
			continue
		}
		if got, err := fileHash(a.ctx, staged); err != nil || got != a.m.Staged[name] {
			return errors.Join(fmt.Errorf("migrate: the staged %s is not the one verified", name), err)
		}
		if err := publish(staged, final); err != nil {
			return err
		}
		if err := a.at("published-" + name); err != nil {
			return err
		}
	}
	a.m.State = StateConverted
	if err := a.writeMarker(); err != nil {
		return err
	}
	if err := a.at(StateConverted); err != nil {
		return err
	}
	return os.RemoveAll(a.path(stageDir))
}

// retire moves the Rust session store into the backup, by link and unlink,
// so nothing is ever replaced or lost.
func (a *account) retire() error {
	if err := a.unchanged(); err != nil {
		return err
	}
	// Staging of a run that stopped after publishing: the stores are in
	// place under their own names.
	if err := os.RemoveAll(a.path(stageDir)); err != nil {
		return err
	}
	retired := filepath.Join(a.path(a.m.Backup), RetiredDir)
	if err := os.MkdirAll(retired, 0o700); err != nil {
		return err
	}
	for _, name := range []string{SessionFile, SessionFile + "-wal", SessionFile + "-shm"} {
		from, to := a.path(name), filepath.Join(retired, name)
		if found, err := exists(from); err != nil {
			return err
		} else if !found {
			continue
		}
		if err := os.Link(from, to); err != nil {
			if !errors.Is(err, fs.ErrExist) {
				return err
			}
			// Linked by a run that stopped before unlinking.
			x, errX := os.Stat(from)
			y, errY := os.Stat(to)
			if errX != nil || errY != nil || !os.SameFile(x, y) {
				return errors.Join(fmt.Errorf("%w: %s", ErrDestinationExists, to), errX, errY)
			}
		}
		if err := a.at("retiring-" + name); err != nil {
			return err
		}
		if err := os.Remove(from); err != nil {
			return err
		}
	}
	if err := syncDir(retired); err != nil {
		return err
	}
	if err := syncDir(a.dir); err != nil {
		return err
	}
	if err := a.at("retired"); err != nil {
		return err
	}
	a.m.State = StateActive
	return a.writeMarker()
}

// undo removes what this step wrote, after a failure: never the Rust
// files, and the published stores only while they are still the ones it
// staged and the Rust session store has not been retired.
func (a *account) undo() error {
	if a.m == nil {
		return a.removeOwn()
	}
	if a.m.State == StateActive || a.m.State == StateConverted && a.retiredAny() {
		// The Go stores are the account's now; keep everything.
		return nil
	}
	var errs []error
	for _, name := range a.outputs {
		final := a.path(name)
		if found, _ := exists(final); !found {
			continue
		}
		if got, err := fileHash(a.ctx, final); err == nil && got == a.m.Staged[name] {
			errs = append(errs, os.Remove(final))
		}
	}
	errs = append(errs, os.RemoveAll(a.path(stageDir)))
	if backupName.MatchString(a.m.Backup) {
		errs = append(errs, os.RemoveAll(a.path(a.m.Backup)))
	}
	errs = append(errs, os.Remove(a.path(MarkerFile)), a.removeOwn())
	a.m = nil
	return errors.Join(errs...)
}

func (a *account) retiredAny() bool {
	found, _ := exists(filepath.Join(a.path(a.m.Backup), RetiredDir, SessionFile))
	return found
}

func (a *account) readMarker() (*marker, error) {
	data, err := os.ReadFile(a.path(MarkerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m marker
	if err = json.Unmarshal(data, &m); err != nil || m.Version != 1 || !backupName.MatchString(m.Backup) {
		return nil, errors.Join(fmt.Errorf("migrate: %s is not a marker this build wrote", MarkerFile), err)
	}
	return &m, nil
}

// writeMarker replaces the marker atomically.
func (a *account) writeMarker() error {
	a.m.Updated = a.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(a.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.path(MarkerFile + ".tmp")
	if err = writeFileSync(tmp, data); err != nil {
		return err
	}
	if err = os.Rename(tmp, a.path(MarkerFile)); err != nil {
		return err
	}
	return syncDir(a.dir)
}

func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

// syncTree makes every directory under root durable.
func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		return syncDir(path)
	})
}
