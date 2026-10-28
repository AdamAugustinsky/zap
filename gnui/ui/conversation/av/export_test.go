package av

import (
	"gonativeui/img"
	"gonativeui/media"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// Hooks are what a test puts in place of the media and the core: how
// Players are made from PCM and from files, how voice notes are decoded,
// and how videos open. A nil field keeps the app's.
type Hooks struct {
	Player     func(pcm []byte, f media.Format) (Player, error)
	OpenPlayer func(path string) (Player, error)
	Decode     func(conn core.Conn, path string) (Clip, error)
	OpenVideo  func(path string, o media.VideoOptions) (Video, error)
}

// SetHooks puts h in place and returns what restores the ones before.
func SetHooks(h Hooks) (restore func()) {
	np, op, dv, ov := newPlayer, openPlayer, decodeVoice, openVideo
	if h.Player != nil {
		newPlayer = h.Player
	}
	if h.OpenPlayer != nil {
		openPlayer = h.OpenPlayer
	}
	if h.Decode != nil {
		decodeVoice = h.Decode
	}
	if h.OpenVideo != nil {
		openVideo = h.OpenVideo
	}
	return func() { newPlayer, openPlayer, decodeVoice, openVideo = np, op, dv, ov }
}

// Forget drops the package's state: the loaded clip, a decode in flight,
// the video in place, the GIF, the clip waiting for its file, the video
// speed and sound, the caches, and the State its lock hook was added to.
func Forget() {
	stop()
	pb = playback{}
	stopVideo()
	gp.failed = mkey.K{}
	closeGIF()
	stopMotion()
	waiting.reset()
	viewerWas = false
	results.take()
	later.take()
	ticked.c, ticked.frame = nil, 0
	lockOwner.s = nil
	waves, heard = [len(waves)]waveEnt{}, [len(heard)]heardEnt{}
	for i := range requested {
		requested[i].id, requested[i].gen = mkey.K{}, 0
	}
	drag.id, drag.frac = mkey.K{}, 0
	// The posters read from another test's core: blob ids are per core.
	thumbs.Reset()
}

// VideoSpeed is the video speed (VO-004).
func VideoSpeed(s *state.State) float32 { return s.Video.Speed() }

func PosterHandle(e *screen.Env, m model.Message, w, h float32) img.Handle {
	return use(e, m).poster(w, h)
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
