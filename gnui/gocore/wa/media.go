// Parts from zapfast-go 3104c46 (internal/backend/media.go: mediaSource,
// downloadTarget, avatar), MIT. Changed for Zap: a download answers with
// Media (and the row's state), an avatar with Avatar, as upstream's worker
// does.

package wa

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // pictures sent as files
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/av"
	"zapfast/gocore/store"
)

const (
	// downloadLimit is the largest attachment fetched without the reader
	// asking for it (upstream's ATTACHMENT_DOWNLOAD_LIMIT).
	downloadLimit = 64 << 20
	// avatarFresh is how long a fetched profile picture is reused
	// (upstream's AVATAR_FRESH).
	avatarFresh = 24 * time.Hour
	// avatarMax is the largest profile picture fetched.
	avatarMax = 4 << 20
	// avatarTries is how often a picture is asked for again once connected.
	avatarTries = 3
	// thumbSide is a sent picture's thumbnail (upstream's THUMBNAIL_SIDE).
	thumbSide = 96
)

type downloadKey struct {
	chat, id string
	card     int
}

type avatarKey struct {
	id   string
	full bool
}

// mediaState is a message's attachment state: downloading, failed, idle.
func (e *Engine) mediaState(chat, id string) abi.MediaState { return e.cardState(chat, id, -1) }

// cardState is the state of a message's attachment (card -1) or of one
// carousel card's picture.
func (e *Engine) cardState(chat, id string, card int) abi.MediaState {
	if e.downloads[downloadKey{chat, id, card}] {
		return abi.MediaState{Tag: abi.MediaStateTagDownloading}
	}
	if err := e.failed[failKey(chat, id, card)]; err != nil {
		return failedState(err)
	}
	return abi.MediaState{Tag: abi.MediaStateTagIdle}
}

func failKey(chat, id string, card int) string {
	if card < 0 {
		return chat + "\x00" + id
	}
	return fmt.Sprintf("%s\x00%s\x00%d", chat, id, card)
}

func cacheName(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

func extension(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/jpeg"):
		return ".jpg"
	case strings.HasPrefix(mime, "image/png"):
		return ".png"
	case strings.HasPrefix(mime, "image/gif"):
		return ".gif"
	case strings.Contains(mime, "webp"):
		return ".webp"
	case strings.Contains(mime, "ogg"):
		return ".ogg"
	case strings.Contains(mime, "mp4"):
		return ".mp4"
	case strings.Contains(mime, "mpeg"):
		return ".mp3"
	case strings.Contains(mime, "pdf"):
		return ".pdf"
	}
	return ".bin"
}

// keepMedia writes an attachment into the account's media cache.
func (e *Engine) keepMedia(chat, id, mime string, data []byte) (string, error) {
	return keepMediaIn(e.mediaCache(), chat, id, mime, data)
}

// mediaCache is the account's media cache folder.
func (e *Engine) mediaCache() string { return filepath.Join(e.cacheDir, "media") }

// downloadFolder is where downloads go: the folder SetDownloadFolder
// named, or the media cache when there is none or it cannot be made, as
// upstream's download_dir.
func (e *Engine) downloadFolder() string {
	if e.downloadDir != "" {
		if err := os.MkdirAll(e.downloadDir, 0o700); err == nil {
			return e.downloadDir
		}
		slog.Warn("zapfast wa: the download folder is unavailable; using the cache")
	}
	return e.mediaCache()
}

func keepMediaIn(dir, chat, id, mime string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, cacheName(chat, id)+extension(mime))
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

// mediaSource is what whatsmeow downloads of a message.
func mediaSource(p *waE2E.Message) whatsmeow.DownloadableMessage {
	if h := p.GetInteractiveMessage().GetHeader(); h != nil {
		switch {
		case h.GetImageMessage() != nil:
			return h.GetImageMessage()
		case h.GetVideoMessage() != nil:
			return h.GetVideoMessage()
		case h.GetDocumentMessage() != nil:
			return h.GetDocumentMessage()
		}
	}
	switch {
	case p.GetImageMessage() != nil:
		return p.GetImageMessage()
	case p.GetVideoMessage() != nil:
		return p.GetVideoMessage()
	case p.GetAudioMessage() != nil:
		return p.GetAudioMessage()
	case p.GetDocumentMessage() != nil:
		return p.GetDocumentMessage()
	case p.GetStickerPackMessage() != nil:
		return p.GetStickerPackMessage()
	case p.GetStickerMessage() != nil:
		return p.GetStickerMessage()
	case p.GetPtvMessage() != nil:
		return p.GetPtvMessage()
	}
	return nil
}

// download fetches a message's attachment (or a carousel card's) into the
// media cache; Media answers it, and the row shows its progress. A
// manual download, asked for by the reader, has no size limit.
func (e *Engine) download(chat, id string, card int, manual bool) {
	key := downloadKey{chat, id, card}
	if e.downloads[key] {
		return
	}
	fail := func(msg string) { e.downloaded(key, "", errors.New(msg)) }
	m, err := e.arch.Message(chat, id)
	if err != nil {
		fail("This message is not stored on this computer")
		return
	}
	var p waE2E.Message
	if len(m.Raw) == 0 || proto.Unmarshal(m.Raw, &p) != nil {
		fail("Attachment download keys are missing")
		return
	}
	body := &p
	if card >= 0 {
		cards := p.GetInteractiveMessage().GetCarouselMessage().GetCards()
		if card >= len(cards) {
			fail("This card has no attachment")
			return
		}
		body = &waE2E.Message{InteractiveMessage: cards[card]}
	}
	source := mediaSource(body)
	if source == nil || m.Kind == kindViewOnce {
		fail("This message has no attachment to download")
		return
	}
	if sized, ok := source.(interface{ GetFileLength() uint64 }); ok && !manual && sized.GetFileLength() > downloadLimit {
		fail(fmt.Sprintf("Attachments over %d MB download when you open them", downloadLimit>>20))
		return
	}
	if !e.cli.IsConnected() {
		fail("Not connected to WhatsApp")
		return
	}
	mime := ""
	if m.Media != nil {
		mime = m.Media.MIME
	}
	e.downloads[key] = true
	delete(e.failed, failKey(chat, id, card))
	if card < 0 {
		e.emitMessage(chat, id)
	}
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		data, err := cli.Download(ctx, source)
		if err == nil && !manual && len(data) > downloadLimit {
			err = errors.New("the attachment is larger than it said")
		}
		return func() {
			if gen != e.gen {
				delete(e.downloads, key)
				return
			}
			path := ""
			if err == nil {
				name := id
				if card >= 0 {
					name = fmt.Sprintf("%s/card:%d", id, card)
				}
				path, err = keepMediaIn(e.downloadFolder(), chat, name, mime, data)
			}
			e.downloaded(key, path, err)
		}
	})
}

// downloaded settles a download: Media, and the row with its path or its
// failure.
func (e *Engine) downloaded(key downloadKey, path string, err error) {
	delete(e.downloads, key)
	if err == nil {
		err = e.arch.UpdateMessage(key.chat, key.id, func(m *store.Message) {
			switch {
			case key.card < 0 && m.Media != nil:
				m.Media.Path = path
			case key.card >= 0 && key.card < len(m.Cards) && m.Cards[key.card].Media != nil:
				m.Cards[key.card].Media.Path = path
			}
		})
	}
	if err != nil {
		e.failed[failKey(key.chat, key.id, key.card)] = err
	}
	ev := abi.Event{Tag: abi.EventTagMedia}
	ev.Media = abi.EventMedia{Chat: b(key.chat), Message: b(key.id), Result: pathResult(path, err)}
	if key.card >= 0 {
		ev.Media.Card, ev.Media.HasCard = uint64(key.card), true
	}
	e.out.Emit(&ev)
	if key.card < 0 {
		e.emitMessage(key.chat, key.id)
	}
}

// avatarFile is where a profile picture is kept: an empty file says there
// is none.
func (e *Engine) avatarFile(id string, full bool) string {
	name := cacheName(id)
	if full {
		name += "-full"
	}
	return filepath.Join(e.cacheDir, "avatars", name+".jpg")
}

func (e *Engine) avatar(id string, full bool, path string) {
	ev := abi.Event{Tag: abi.EventTagAvatar}
	ev.Avatar = abi.EventAvatar{ID: b(id), Full: full}
	if path != "" {
		ev.Avatar.Path, ev.Avatar.HasPath = b(path), true
	}
	e.out.Emit(&ev)
}

// fetchAvatar answers with a chat's profile picture: the cached one while
// fresh, else WhatsApp's, once connected.
func (e *Engine) fetchAvatar(id string, full bool) {
	path := e.avatarFile(id, full)
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < avatarFresh {
		if info.Size() == 0 {
			path = ""
		}
		e.avatar(id, full, path)
		return
	}
	if isChannel(id) {
		e.fetchChannelAvatar(id, full)
		return
	}
	target := id
	if e.isMe(id) {
		target = e.me()
	}
	j, ok := jidOf(target)
	if !ok {
		e.avatar(id, full, "")
		return
	}
	if !e.cli.IsConnected() {
		e.avatarLater(id, full)
		return
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		info, err := cli.ProfilePicture(ctx, j, !full)
		var data []byte
		if err == nil && info != nil {
			data, err = fetchPicture(ctx, cli, info.URL)
		}
		return func() { e.avatarFetched(id, full, data, err) }
	})
}

// avatarLater asks for a picture again once connected.
func (e *Engine) avatarLater(id string, full bool) {
	if _, waiting := e.avatars[avatarKey{id, full}]; !waiting {
		e.avatars[avatarKey{id, full}] = 0
	}
}

// fetchPicture reads a profile picture from WhatsApp's servers.
func fetchPicture(ctx context.Context, cli Client, url string) ([]byte, error) {
	data, err := cli.Fetch(ctx, url, avatarMax)
	if err == nil && !strings.HasPrefix(httpType(data), "image/") {
		err = errors.New("not a picture")
	}
	return data, err
}

// avatarFetched keeps and sends a fetched picture (empty data: there is
// none), or counts a failed try.
func (e *Engine) avatarFetched(id string, full bool, data []byte, err error) {
	if err != nil {
		tries := e.avatars[avatarKey{id, full}] + 1
		if tries >= avatarTries {
			delete(e.avatars, avatarKey{id, full})
			e.avatar(id, full, "")
			return
		}
		e.avatars[avatarKey{id, full}] = tries
		return
	}
	delete(e.avatars, avatarKey{id, full})
	path := e.avatarFile(id, full)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.avatar(id, full, "")
		return
	}
	// No picture is kept as an empty file, so it is not asked for
	// again until it is stale.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		e.avatar(id, full, "")
		return
	}
	if len(data) == 0 {
		path = ""
	}
	e.avatar(id, full, path)
}

// retryAvatars asks again for the pictures wanted while offline.
func (e *Engine) retryAvatars() {
	for k := range e.avatars {
		e.fetchAvatar(k.id, k.full)
	}
}

func httpType(data []byte) string {
	if len(data) > 3 && data[0] == 0xff && data[1] == 0xd8 {
		return "image/jpeg"
	}
	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		return "image/png"
	}
	if bytes.HasPrefix(data, []byte("RIFF")) && len(data) > 12 && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return "application/octet-stream"
}

// encodeRGBA is a pasted picture (straight-alpha RGBA rows) as a JPEG.
func encodeRGBA(rgba []byte, width, height, quality int) ([]byte, error) {
	if width <= 0 || height <= 0 || len(rgba) != 4*width*height {
		return nil, errors.New("the picture's pixels do not match its size")
	}
	m := &image.NRGBA{Pix: rgba, Stride: 4 * width, Rect: image.Rect(0, 0, width, height)}
	// JPEG has no transparency: see-through pixels go on white, as a
	// pasted screenshot shows.
	flat := image.NewRGBA(m.Rect)
	draw.Draw(flat, flat.Rect, image.White, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Rect, m, image.Point{}, draw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pictureSize is a picture's size from its header.
func pictureSize(data []byte) (uint32, uint32, bool) {
	c, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || c.Width <= 0 || c.Height <= 0 {
		return 0, 0, false
	}
	return uint32(c.Width), uint32(c.Height), true
}

// thumbnail is a small JPEG of a picture, nil when it cannot be read.
func thumbnail(data []byte) []byte {
	w, h, ok := pictureSize(data)
	if !ok || uint64(w)*uint64(h) > 100_000_000 {
		return nil
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return thumbOf(m)
}

// thumbOf is a small JPEG of a decoded picture, nil when it cannot be
// encoded: upstream's thumbnail_jpeg, for pictures and video posters alike.
func thumbOf(m image.Image) []byte {
	w, h := m.Bounds().Dx(), m.Bounds().Dy()
	if w < 1 || h < 1 {
		return nil
	}
	scale := float64(thumbSide) / float64(max(w, h))
	tw, th := max(int(float64(w)*scale), 1), max(int(float64(h)*scale), 1)
	small := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.Draw(small, small.Rect, image.White, image.Point{}, draw.Src)
	draw.ApproxBiLinear.Scale(small, small.Rect, m, m.Bounds(), draw.Over, nil)
	var buf bytes.Buffer
	if jpeg.Encode(&buf, small, &jpeg.Options{Quality: 70}) != nil {
		return nil
	}
	return buf.Bytes()
}

// posterSide bounds a video's decoded poster before it is made a thumbnail:
// the Rust core's MAX_WIDTH (zf:src/mp4_frames.rs:11).
const posterSide = 320

// preview is what a sent video carries before it is downloaded: its size,
// length and poster thumbnail. Each is zero when the MP4 does not say, and
// the thumbnail when its frame does not decode
// (zf:src/backend/worker/video_preview.rs:18). A video without a path (a
// GIF's clip) is read from a temporary copy.
func preview(path string, data []byte) (w, h, seconds uint32, thumb []byte) {
	v, err := av.InspectVideo(data)
	if err != nil {
		return 0, 0, 0, nil
	}
	if path == "" {
		f, err := os.CreateTemp("", "zap-video-*.mp4")
		if err != nil {
			return v.Width, v.Height, v.Seconds, nil
		}
		path = f.Name()
		defer os.Remove(path)
		_, err = f.Write(data)
		if err = errors.Join(err, f.Close()); err != nil {
			return v.Width, v.Height, v.Seconds, nil
		}
	}
	if m, err := av.VideoPoster(path, v.Poster, posterSide); err == nil {
		thumb = thumbOf(m)
	} else {
		slog.Debug("zapfast wa: a sent video's poster", "err", err)
	}
	return v.Width, v.Height, v.Seconds, thumb
}
