// Snapshot and publish helpers. copySourceSnapshot, copyFile and hashReader
// are from zapfast-go 3104c46 (internal/migration/import.go), MIT; publish
// replaces its renameatx_np with a hard link, which never overwrites either.

package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

var (
	// ErrDestinationExists means a file the migration would publish is
	// already there and is not the one it staged. Nothing was replaced.
	ErrDestinationExists = errors.New("migrate: the destination already exists")
	// ErrSourceChanged means a Rust file changed while it was being read:
	// the Rust core (or another program) is still using the account.
	ErrSourceChanged = errors.New("migrate: the Rust core's files changed while being read; quit it and retry")
)

type sourceFile struct {
	path string
	info os.FileInfo
	hash [32]byte
}

// copySourceSnapshot copies a SQLite file and its write-ahead log to
// target(-wal) and checks, by a second read, that neither changed. The
// source is never opened by SQLite, so not even its -shm read marks move.
func copySourceSnapshot(ctx context.Context, source, target string) error {
	files := []sourceFile{}
	for _, suffix := range []string{"", "-wal"} {
		path := source + suffix
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("migrate: the source must be a regular file")
		}
		if suffix == "" && info.Size() == 0 {
			return errors.New("migrate: the source is empty")
		}
		hash, err := copyFile(ctx, path, target+suffix, 0)
		if err != nil {
			if errors.Is(err, ErrSourceChanged) {
				return err
			}
			return errors.New("migrate: could not snapshot the source")
		}
		files = append(files, sourceFile{path, info, hash})
	}
	for _, file := range files {
		info, err := os.Lstat(file.path)
		if err != nil || !sameFileSnapshot(file.info, info) {
			return ErrSourceChanged
		}
		f, err := os.Open(file.path)
		if err != nil {
			return ErrSourceChanged
		}
		hash, err := hashReader(ctx, f)
		f.Close()
		if err != nil || hash != file.hash {
			return ErrSourceChanged
		}
	}
	// A log created between the passes is a change too.
	hadWAL := len(files) > 1
	if _, err := os.Lstat(source + "-wal"); (err == nil) != hadWAL {
		return ErrSourceChanged
	}
	return nil
}

func sameFileSnapshot(before, after os.FileInfo) bool {
	return os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func hashReader(ctx context.Context, r io.Reader) ([32]byte, error) {
	h := sha256.New()
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return [32]byte{}, err
		}
		n, err := r.Read(buffer)
		if n > 0 {
			h.Write(buffer[:n])
		}
		if err == io.EOF {
			var result [32]byte
			copy(result[:], h.Sum(nil))
			return result, nil
		}
		if err != nil {
			return [32]byte{}, err
		}
	}
}

// copyFile copies source to a new file target (0600, never replacing one)
// and returns the SHA-256 of what it copied.
func copyFile(ctx context.Context, source, target string, limit int64) ([32]byte, error) {
	in, err := os.Open(source)
	if err != nil {
		return [32]byte{}, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil || !info.Mode().IsRegular() || limit > 0 && info.Size() > limit {
		return [32]byte{}, errors.New("migrate: the file is unavailable or too large")
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return [32]byte{}, err
	}
	defer out.Close()
	hash, err := hashReader(ctx, io.TeeReader(io.LimitReader(in, info.Size()+1), out))
	if err != nil {
		return [32]byte{}, err
	}
	if copied, statErr := out.Stat(); statErr != nil || copied.Size() != info.Size() {
		return [32]byte{}, ErrSourceChanged
	}
	if final, statErr := in.Stat(); statErr != nil || !sameFileSnapshot(info, final) {
		return [32]byte{}, ErrSourceChanged
	}
	if err = out.Sync(); err != nil {
		return [32]byte{}, err
	}
	return hash, nil
}

// fileHash is the hex SHA-256 of the file at path.
func fileHash(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum, err := hashReader(ctx, f)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum[:]), nil
}

// publish puts staged at final without ever replacing a file there: a hard
// link fails when final exists. A final that is already the staged file
// (a run that stopped after linking) counts as published. The staged name
// is left for the caller's cleanup.
func publish(staged, final string) error {
	err := os.Link(staged, final)
	if err == nil {
		return syncDir(filepath.Dir(final))
	}
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	a, errA := os.Stat(staged)
	b, errB := os.Stat(final)
	if errA == nil && errB == nil && os.SameFile(a, b) {
		return nil
	}
	return ErrDestinationExists
}

// syncDir makes the directory's entries durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// exists reports whether path names anything (a broken link included).
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}
