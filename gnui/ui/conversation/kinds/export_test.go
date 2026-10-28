package kinds

import (
	"gonativeui/img"

	"zapfast/abi"
	"zapfast/ui/screen"
)

// SetOpeners replaces what asks the system to open a path, open a link and
// show a file in its folder, and returns what restores them.
func SetOpeners(path func(string) error, url func(string) error, reveal func([]string) error) (restore func()) {
	op, ou, or := openPathFn, openURLFn, revealFn
	openPathFn, openURLFn, revealFn = path, url, reveal
	return func() { openPathFn, openURLFn, revealFn = op, ou, or }
}

// Forget empties the size and Has caches, which tests that reuse a message id
// with other content (and the same generation) need between runs.
func Forget() { sizes, hases = [2048]sizeEnt{}, [2048]hasEnt{} }

func ThumbnailHandle(e *screen.Env, ref abi.BlobRef, w, h int32) img.Handle {
	b := blk{a: e.Acct(), e: e, k: e.K.Kit, c: e.C()}
	hd, _ := b.thumbImage(ref, w, h, 0)
	return hd
}

// ChurnRawThumbnails exercises real FIFO eviction without populating the
// decoded image cache. Only fake cores are passed by these tests.
func ChurnRawThumbnails(e *screen.Env, original abi.BlobRef) (bool, error) {
	b := blk{a: e.Acct(), e: e}
	for range maxThumbs {
		ref, err := b.a.Conn.PutBlob([]byte{1})
		if err != nil {
			return false, err
		}
		if l := b.thumbnail(ref); l != nil {
			l.Release()
		}
	}
	return !thumbs.Contains(b.a.Serial, original.ID), nil
}

func RawThumbnailReady(e *screen.Env, ref abi.BlobRef) bool {
	b := blk{a: e.Acct(), e: e}
	if l := b.thumbnail(ref); l != nil {
		l.Release()
		return true
	}
	return false
}

// CountRawLookups probes decoded-cache hits. Emptying the raw cache before
// each draw makes any thumbnail lookup observable as a new entry. Workers
// only fill existing entries, so cannot contribute to this count.
func CountRawLookups(draw func(), frames int) (n int) {
	for range frames {
		thumbs.Reset()
		draw()
		entries, _, _ := thumbs.Stats()
		n += entries
	}
	return n
}
