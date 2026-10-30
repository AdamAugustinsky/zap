package wa

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	xdraw "golang.org/x/image/draw"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/av"
	"zapfast/gocore/out"
	"zapfast/gocore/store"
)

// Stickers are files, as upstream keeps them (src/backend/sticker_store.rs,
// worker/stickers.rs): favorites are WebP files named by their content
// hash under state/stickers, every pack a folder of WebP files under
// state/stickers/packs, with a pack.json when it was put together here.
// Recent and Received are the archive's sticker messages whose files are
// downloaded. Made stickers and packs opened from a chat stay in
// cache/stickers.

const (
	// packLimit is the most stickers a WhatsApp pack holds.
	packLimit = 60
	// receivedShelf is how many stickers others sent the picker lists.
	receivedShelf = 200
	// recentShelf is how many of ours it looks at for Recent.
	recentShelf = 80
	// stickerMax bounds a sticker file read to send or keep.
	stickerMax = 4 << 20
	// packManifest describes a pack put together here.
	packManifest = "pack.json"
	// removedRecentKey keeps the stickers taken out of Recent: their hash
	// and when.
	removedRecentKey = "removed-recent-stickers"
	// thumbnailSide is a shared pack's thumbnail (upstream's pack_art).
	thumbnailSide = 252
)

func (e *Engine) favoritesDir() string { return filepath.Join(e.stateDir, "stickers") }
func (e *Engine) packsDir() string     { return filepath.Join(e.stateDir, "stickers", "packs") }
func (e *Engine) stickerCache() string { return filepath.Join(e.cacheDir, "stickers") }

// contentHash is a sticker's identity: the hex SHA-256 of its bytes.
func contentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func isWebP(data []byte) bool {
	return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

// within reports whether path is inside root, and not root itself.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// --- packs on disk -----------------------------------------------------------

// manifest is a pack's pack.json.
type manifest struct {
	Name string `json:"name"`
	// Local is a pack put together here rather than imported.
	Local bool `json:"local"`
	// Created orders the local packs: Unix seconds.
	Created int64 `json:"created"`
	// Stickers are the file names, in the order they were added.
	Stickers []string `json:"stickers"`
}

func readManifest(dir string) (manifest, bool) {
	var m manifest
	data, err := os.ReadFile(filepath.Join(dir, packManifest))
	if err != nil || json.Unmarshal(data, &m) != nil {
		return manifest{}, false
	}
	return m, true
}

func writeManifest(dir string, m manifest) error {
	if m.Stickers == nil {
		m.Stickers = []string{}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, packManifest), data, 0o600)
}

type stickerPack struct {
	name     string
	dir      string
	stickers []string
	local    bool
}

// webpFiles are a folder's .webp files, sorted.
func webpFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, f := range entries {
		if f.Type().IsRegular() && strings.EqualFold(filepath.Ext(f.Name()), ".webp") {
			files = append(files, filepath.Join(dir, f.Name()))
		}
	}
	slices.Sort(files)
	return files
}

// listPacks is upstream's sticker_store::packs: every pack under root,
// newest first; a local pack keeps its creation order and lists empty.
func listPacks(root string) []stickerPack {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	type dated struct {
		when int64
		pack stickerPack
	}
	var packs []dated
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		m, has := readManifest(dir)
		files := webpFiles(dir)
		if has {
			rank := func(path string) int {
				if i := slices.Index(m.Stickers, filepath.Base(path)); i >= 0 {
					return i
				}
				return len(m.Stickers)
			}
			slices.SortStableFunc(files, func(a, b string) int { return rank(a) - rank(b) })
		}
		local := has && m.Local
		if len(files) == 0 && !local {
			continue
		}
		when := m.Created
		if !local {
			if info, err := os.Stat(dir); err == nil {
				when = info.ModTime().Unix()
			}
		}
		name := entry.Name()
		if has && strings.TrimSpace(m.Name) != "" {
			name = m.Name
		}
		packs = append(packs, dated{when, stickerPack{name: name, dir: dir, stickers: files, local: local}})
	}
	slices.SortStableFunc(packs, func(a, b dated) int {
		if a.when != b.when {
			return int(b.when - a.when)
		}
		return strings.Compare(a.pack.name, b.pack.name)
	})
	out := make([]stickerPack, len(packs))
	for i, p := range packs {
		out[i] = p.pack
	}
	return out
}

// uniquePackDir makes a new pack folder under root named after title.
func uniquePackDir(root, title string) (string, error) {
	var clean strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(title) {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' || r == ':' {
			continue
		}
		if n++; n > 60 {
			break
		}
		clean.WriteRune(r)
	}
	base := strings.TrimSpace(clean.String())
	if base == "" || strings.Trim(base, ".") == "" {
		base = "Stickers"
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("Could not create the pack folder: %w", err)
	}
	for attempt := range 100 {
		name := base
		if attempt > 0 {
			name = fmt.Sprintf("%s %d", base, attempt+1)
		}
		dir := filepath.Join(root, name)
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("Could not create the pack folder: %w", err)
		}
	}
	return "", errors.New("Too many sticker packs have this name")
}

// writePack writes stickers into a new pack folder named after title and
// returns its name.
func writePack(packs, title string, files [][]byte) (string, error) {
	if len(files) == 0 {
		return "", errors.New("No stickers could be read from this pack")
	}
	dir, err := uniquePackDir(packs, title)
	if err != nil {
		return "", err
	}
	for i, data := range files {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%03d.webp", i)), data, 0o600); err != nil {
			return "", fmt.Errorf("Could not write the sticker pack: %w", err)
		}
	}
	return filepath.Base(dir), nil
}

// createLocalPack makes an empty pack to file stickers into.
func createLocalPack(root, name string, now int64) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("A pack needs a name")
	}
	dir, err := uniquePackDir(root, name)
	if err != nil {
		return "", err
	}
	return dir, writeManifest(dir, manifest{Name: name, Local: true, Created: now})
}

// setMember files a sticker into a local pack under its content hash, or
// takes it out. Imported packs stay as they came.
func setMember(pack, sticker string, member bool) error {
	m, ok := readManifest(pack)
	if !ok || !m.Local {
		return errors.New("Only packs made in Zap can change")
	}
	data, err := readBounded(sticker, stickerMax)
	if err != nil {
		return err
	}
	file := contentHash(data) + ".webp"
	target := filepath.Join(pack, file)
	if member {
		if _, err := os.Stat(target); err != nil {
			if err := os.WriteFile(target, data, 0o600); err != nil {
				return err
			}
		}
		if !slices.Contains(m.Stickers, file) {
			m.Stickers = append(m.Stickers, file)
		}
	} else {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		m.Stickers = slices.DeleteFunc(m.Stickers, func(s string) bool { return s == file })
	}
	return writeManifest(pack, m)
}

// copyPack copies a pack's stickers into a new pack named name.
func copyPack(from, packs, name string) (string, error) {
	var files [][]byte
	for _, path := range webpFiles(from) {
		data, err := readBounded(path, stickerMax)
		if err != nil {
			return "", err
		}
		files = append(files, data)
	}
	return writePack(packs, name, files)
}

// savedStickers are the favorites, newest first.
func savedStickers(dir string) []string {
	files := webpFiles(dir)
	when := map[string]time.Time{}
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			when[f] = info.ModTime()
		}
	}
	slices.SortStableFunc(files, func(a, b string) int { return when[b].Compare(when[a]) })
	return files
}

// saveSticker keeps a copy of a sticker under its content hash.
func saveSticker(dir, path string) (string, error) {
	data, err := readBounded(path, stickerMax)
	if err != nil {
		return "", err
	}
	if !isWebP(data) {
		return "", errors.New("this is not a sticker")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	hash := contentHash(data)
	target := filepath.Join(dir, hash+".webp")
	if _, err := os.Stat(target); err != nil {
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return "", err
		}
	}
	return hash, nil
}

// --- converting --------------------------------------------------------------

// webpBytes is a picture as a sticker file: a WebP as it is, anything else
// decoded and, when larger, scaled to fit 512 pixels.
func webpBytes(data []byte) ([]byte, bool) {
	if isWebP(data) {
		return data, true
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	if r := m.Bounds(); max(r.Dx(), r.Dy()) > av.StickerSide {
		w, h := av.StickerSide, av.StickerSide
		if r.Dx() > r.Dy() {
			h = max(1, r.Dy()*av.StickerSide/r.Dx())
		} else {
			w = max(1, r.Dx()*av.StickerSide/r.Dy())
		}
		scaled := image.NewNRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(scaled, scaled.Rect, m, r, xdraw.Src, nil)
		m = scaled
	}
	webp, err := av.EncodeWebP(m, 90)
	if err != nil {
		return nil, false
	}
	return webp, true
}

// webpInfo is a sticker's size and whether it moves, from its header.
func webpInfo(data []byte) (width, height uint32, animated bool) {
	if len(data) >= 30 && string(data[12:16]) == "VP8X" {
		flags := data[20]
		w := uint32(data[24]) | uint32(data[25])<<8 | uint32(data[26])<<16
		h := uint32(data[27]) | uint32(data[28])<<8 | uint32(data[29])<<16
		return w + 1, h + 1, flags&0x02 != 0
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return uint32(cfg.Width), uint32(cfg.Height), false
	}
	return av.StickerSide, av.StickerSide, false
}

// packArt is upstream's pack_art: a shared pack's tray icon (a 96-pixel
// WebP) and thumbnail (a 252-pixel JPEG on white), from its first sticker.
func packArt(first []byte) (tray, thumb []byte, err error) {
	m, _, err := image.Decode(bytes.NewReader(first))
	if err != nil {
		return nil, nil, errors.New("Could not draw the pack's cover")
	}
	icon := image.NewNRGBA(image.Rect(0, 0, 96, 96))
	xdraw.CatmullRom.Scale(icon, icon.Rect, m, m.Bounds(), xdraw.Src, nil)
	if tray, err = av.EncodeWebP(icon, 90); err != nil {
		return nil, nil, err
	}
	small := image.NewRGBA(image.Rect(0, 0, thumbnailSide, thumbnailSide))
	draw.Draw(small, small.Rect, image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(small, small.Rect, m, m.Bounds(), xdraw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: 85}); err != nil {
		return nil, nil, err
	}
	return tray, buf.Bytes(), nil
}

// --- the picker's lists ------------------------------------------------------

// stickerTags are a file's emojis, as of its size and time.
type stickerTags struct {
	size   int64
	mod    time.Time
	emojis []string
}

// emojisOf reads a sticker file's emojis once per version of it.
func (e *Engine) emojisOf(path string) []string {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if t, ok := e.stickerTags[path]; ok && t.size == info.Size() && t.mod.Equal(info.ModTime()) {
		return t.emojis
	}
	var emojis []string
	if info.Size() <= stickerMax {
		if data, err := os.ReadFile(path); err == nil {
			emojis = av.StickerEmojis(data)
		}
	}
	e.stickerTags[path] = stickerTags{info.Size(), info.ModTime(), emojis}
	return emojis
}

// stickerHash is an archived sticker's identity: its file's SHA-256, as
// WhatsApp gives it, else its path.
func stickerHash(m store.Message) string {
	var p waE2E.Message
	if len(m.Raw) > 0 && proto.Unmarshal(m.Raw, &p) == nil {
		if h := p.GetStickerMessage().GetFileSHA256(); len(h) == 32 {
			return hex.EncodeToString(h)
		}
	}
	if m.Media != nil {
		return m.Media.Path
	}
	return ""
}

// stickerFile is an archived sticker's downloaded file, "" when there is
// none.
func stickerFile(m store.Message) string {
	if m.Media == nil || m.Media.Path == "" {
		return ""
	}
	if _, err := os.Stat(m.Media.Path); err != nil {
		return ""
	}
	return m.Media.Path
}

func (e *Engine) removedRecent() map[string]int64 {
	removed := map[string]int64{}
	if data, err := e.arch.Metadata(removedRecentKey); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &removed); err != nil {
			slog.Warn("zapfast wa: reading the removed stickers", "err", err)
		}
	}
	return removed
}

// emitStickers sends the picker its lists, as upstream's emit_stickers:
// the favorites, the packs, Recent (the stickers we sent, newest first,
// minus those taken out since their last use), Received (the ones others
// sent that are in neither), and the emojis the files are tagged with.
func (e *Engine) emitStickers() {
	removed := e.removedRecent()
	seen := map[string]bool{}
	var recent []string
	e.recentHashes = map[string]string{}
	if rows, err := e.arch.Stickers(true, recentShelf); err == nil {
		for _, m := range rows {
			path, hash := stickerFile(m), stickerHash(m)
			if path == "" || seen[hash] {
				continue
			}
			if at, ok := removed[hash]; ok && at >= m.Timestamp {
				continue
			}
			seen[hash] = true
			recent = append(recent, path)
			e.recentHashes[path] = hash
		}
	} else {
		slog.Warn("zapfast wa: listing stickers", "err", err)
	}
	favorites := savedStickers(e.favoritesDir())
	for _, f := range favorites {
		seen[strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))] = true
	}
	var received []string
	if rows, err := e.arch.Stickers(false, receivedShelf*4); err == nil {
		for _, m := range rows {
			if len(received) == receivedShelf {
				break
			}
			path, hash := stickerFile(m), stickerHash(m)
			if path != "" && !seen[hash] {
				seen[hash] = true
				received = append(received, path)
			}
		}
	}
	packs := listPacks(e.packsDir())

	var entries []abi.StickerEntry
	listed := map[string]bool{}
	note := func(path string) { listed[path] = true }
	for _, f := range favorites {
		note(f)
		entries = append(entries, abi.StickerEntry{Tag: abi.StickerEntryTagFavorite, Favorite: abi.StickerEntryFavorite{Path: b(f)}})
	}
	for _, p := range packs {
		entries = append(entries, abi.StickerEntry{Tag: abi.StickerEntryTagPack, Pack: abi.StickerEntryPack{Name: b(p.name), Dir: b(p.dir), Local: p.local}})
		for _, s := range p.stickers {
			note(s)
			entries = append(entries, abi.StickerEntry{Tag: abi.StickerEntryTagPackSticker, PackSticker: abi.StickerEntryPackSticker{Path: b(s)}})
		}
	}
	for _, r := range recent {
		note(r)
		entries = append(entries, abi.StickerEntry{Tag: abi.StickerEntryTagRecent, Recent: abi.StickerEntryRecent{Path: b(r)}})
	}
	for _, r := range received {
		note(r)
		entries = append(entries, abi.StickerEntry{Tag: abi.StickerEntryTagReceived, Received: abi.StickerEntryReceived{Path: b(r)}})
	}
	paths := make([]string, 0, len(listed))
	for p := range listed {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		emojis := e.emojisOf(p)
		if len(emojis) == 0 {
			continue
		}
		t := abi.StickerEntryTagged{Path: b(p)}
		for _, x := range emojis[:min(len(emojis), 16)] {
			t.Emojis = append(t.Emojis, b(x))
		}
		entries = append(entries, abi.StickerEntry{Tag: abi.StickerEntryTagTagged, Tagged: t})
	}
	out.List(e.out, entries, func(batch uint32, last bool, chunk []abi.StickerEntry) *abi.Event {
		return &abi.Event{Tag: abi.EventTagStickers, Stickers: abi.EventStickers{Batch: batch, Last: last, Entries: chunk}}
	})
}

// --- favorites and Recent ----------------------------------------------------

// saveFavorite makes a sticker a favorite.
func (e *Engine) saveFavorite(path string) bool {
	if _, err := saveSticker(e.favoritesDir(), path); err != nil {
		e.emitError("Could not add to favorites: " + err.Error())
		return false
	}
	e.emitStickers()
	return true
}

// forgetSticker removes a favorite; only the favorites' files may go.
func (e *Engine) forgetSticker(path string) {
	if !within(e.favoritesDir(), path) || filepath.Dir(filepath.Clean(path)) != e.favoritesDir() {
		return
	}
	if err := os.Remove(path); err != nil {
		return
	}
	e.emitStickers()
}

// removeRecentSticker takes a sticker out of Recent here and on the phone.
func (e *Engine) removeRecentSticker(path string) {
	hash, ok := e.recentHashes[path]
	if !ok {
		return
	}
	now := time.Now()
	removed := e.removedRecent()
	removed[hash] = now.Unix()
	if data, err := json.Marshal(removed); err == nil {
		if err := e.arch.SetMetadata(removedRecentKey, data); err != nil {
			slog.Warn("zapfast wa: removing a recent sticker", "err", err)
		}
	}
	e.emitStickers()
	digest, err := hex.DecodeString(hash)
	if err != nil || len(digest) != 32 || !e.cli.IsConnected() {
		return
	}
	patch := appstate.PatchInfo{Type: appstate.WAPatchRegularLow, Mutations: []appstate.MutationInfo{{
		Index:   []string{appstate.IndexRemoveRecentSticker, base64.StdEncoding.EncodeToString(digest)},
		Version: 7,
		Value: &waSyncAction.SyncActionValue{RemoveRecentStickerAction: &waSyncAction.RemoveRecentStickerAction{
			LastStickerSentTS: proto.Int64(now.UnixMilli()),
		}},
	}}}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		if err := cli.SendAppState(ctx, patch); err != nil {
			slog.Warn("zapfast wa: removing a recent sticker on the phone", "err", err)
		}
		return nil
	})
}

// --- sending -----------------------------------------------------------------

// sendSticker sends a sticker file.
func (e *Engine) sendSticker(c *abi.CommandSendSticker) {
	j, ctx, ok := e.ready(c.Chat, c.HasQuoting, c.Quoting, func(*recordBlobs) abi.Unsent {
		return abi.Unsent{Tag: abi.UnsentTagSticker}
	})
	if !ok {
		return
	}
	path := string(c.Path)
	e.sendUpload(string(c.Chat), j, func(context.Context) (*upload, error) {
		data, err := readBounded(path, stickerMax)
		if err != nil {
			return nil, err
		}
		if !isWebP(data) {
			return nil, errors.New("this is not a sticker")
		}
		w, h, animated := webpInfo(data)
		return &upload{kind: whatsmeow.MediaImage, mime: "image/webp", data: data, width: w, height: h, sticker: true, animated: animated}, nil
	}, "", ctx, nil, "sticker")
}

// pickStickerPicture asks for a picture to make a sticker of, and answers
// with its size and whether it is see-through.
func (e *Engine) pickStickerPicture() {
	e.job(func(ctx context.Context) func() {
		paths := e.h.Pick(ctx, &abi.EventPickRequest{Kind: abi.PickKindFile, Title: b("Make a sticker"), Filters: filters("Pictures", "png", "jpg", "jpeg", "webp", "gif")})
		if len(paths) == 0 {
			return nil
		}
		data, err := readBounded(paths[0], pictureMax)
		var w, h int
		var transparent bool
		if err == nil {
			w, h, transparent, err = av.InspectSticker(data)
		}
		return func() {
			if err != nil {
				e.emitError(err.Error())
				return
			}
			ev := abi.Event{Tag: abi.EventTagStickerPicture}
			ev.StickerPicture = abi.EventStickerPicture{Path: b(paths[0]), Width: uint32(w), Height: uint32(h), Transparent: transparent}
			e.out.Emit(&ev)
		}
	})
}

// makeSticker makes a sticker of a picture, then sends it to the chat, or
// without one keeps it as a favorite.
func (e *Engine) makeSticker(c *abi.CommandMakeSticker) {
	source, crop, transparent, emojis := string(c.Source), av.StickerCrop(c.Crop), c.Transparent, strs(c.Emojis)
	send := abi.CommandSendSticker{Chat: c.Chat, Quoting: c.Quoting, HasQuoting: c.HasQuoting}
	hasChat := c.HasChat
	dir := filepath.Join(e.stickerCache(), "made")
	e.job(func(context.Context) func() {
		path, err := func() (string, error) {
			data, err := readBounded(source, pictureMax)
			if err != nil {
				return "", err
			}
			sticker, err := av.MakeSticker(data, crop, transparent, emojis)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return "", err
			}
			path := filepath.Join(dir, contentHash(sticker)+".webp")
			return path, os.WriteFile(path, sticker, 0o600)
		}()
		return func() {
			switch {
			case err != nil:
				e.emitError("Could not make the sticker: " + err.Error())
			case hasChat:
				send.Path = b(path)
				e.sendSticker(&send)
			case e.saveFavorite(path):
				e.info("Added to favorites")
			}
		}
	})
}

// --- packs -------------------------------------------------------------------

func (e *Engine) createStickerPack(name string) {
	if _, err := createLocalPack(e.packsDir(), name, time.Now().Unix()); err != nil {
		e.emitError("Could not create the pack: " + err.Error())
		return
	}
	e.emitStickers()
}

func (e *Engine) setStickerPack(pack, sticker string, member bool) {
	if !within(e.packsDir(), pack) {
		return
	}
	if err := setMember(pack, sticker, member); err != nil {
		slog.Warn("zapfast wa: filing a sticker in its pack", "err", err)
	}
	e.emitStickers()
}

func (e *Engine) deleteStickerPack(dir string) {
	if within(e.packsDir(), dir) && os.RemoveAll(dir) == nil {
		e.emitStickers()
	}
}

// packAdded announces an imported pack, or why it is not.
func (e *Engine) packAdded(name string, err error) {
	if err != nil {
		e.emitError("Could not add sticker pack: " + err.Error())
		return
	}
	e.emitStickers()
	e.info(fmt.Sprintf("Added sticker pack %q", name))
}

// addStickerPack copies a pack opened from a chat into the packs here.
func (e *Engine) addStickerPack(dir, name string) {
	if !within(e.stickerCache(), dir) {
		return
	}
	added, err := copyPack(dir, e.packsDir(), name)
	e.packAdded(added, err)
}

// importStickerURL imports a signal.art pack.
func (e *Engine) importStickerURL(link string) {
	web := e.opts.Web
	if web == nil {
		var err error
		if web, err = signalWeb(e.proxy); err != nil {
			e.packAdded("", err)
			return
		}
	}
	cdn, packs := e.signal, e.packsDir()
	e.job(func(ctx context.Context) func() {
		name, err := importSignalPack(ctx, web, cdn, link, packs)
		return func() { e.packAdded(name, err) }
	})
}

// pickStickerArchive imports a .wastickers or zip archive.
func (e *Engine) pickStickerArchive() {
	packs := e.packsDir()
	e.job(func(ctx context.Context) func() {
		paths := e.h.Pick(ctx, &abi.EventPickRequest{Kind: abi.PickKindFile, Title: b("Add a sticker pack"), Filters: filters("Sticker packs", "wastickers", "zip")})
		if len(paths) == 0 {
			return nil
		}
		name, err := importArchive(paths[0], packs)
		return func() { e.packAdded(name, err) }
	})
}

// archiveMax bounds what an imported archive may unpack to.
const archiveMax = 64 << 20

// importArchive is upstream's import_archive: the archive's pictures, in
// name order and without the tray icon, titled by its title.txt or its
// file name.
func importArchive(path, packs string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("This file is not a sticker archive: %w", err)
	}
	defer r.Close()
	title := ""
	type picture struct {
		name string
		data []byte
	}
	var pictures []picture
	var total uint64
	for _, f := range r.File {
		if f.FileInfo().IsDir() || f.UncompressedSize64 > stickerMax {
			continue
		}
		if total += f.UncompressedSize64; total > archiveMax {
			return "", errors.New("the archive is too large")
		}
		name := strings.ToLower(f.Name)
		isPicture := false
		for _, x := range []string{".webp", ".png", ".jpg", ".jpeg"} {
			isPicture = isPicture || strings.HasSuffix(name, x)
		}
		if !strings.HasSuffix(name, "title.txt") && (!isPicture || strings.Contains(name, "tray")) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(rc, stickerMax+1))
		rc.Close()
		if err != nil || len(data) > stickerMax {
			continue
		}
		if strings.HasSuffix(name, "title.txt") {
			if t := strings.TrimSpace(string(data)); t != "" {
				title = t
			}
			continue
		}
		pictures = append(pictures, picture{name, data})
	}
	slices.SortFunc(pictures, func(a, b picture) int { return strings.Compare(a.name, b.name) })
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	var files [][]byte
	for _, p := range pictures {
		if webp, ok := webpBytes(p.data); ok {
			files = append(files, webp)
		}
	}
	return writePack(packs, title, files)
}

// previewPack answers ViewStickerPack.
func (e *Engine) previewPack(p *stickerPack, publisher string, err error) {
	ev := abi.Event{Tag: abi.EventTagStickerPackPreview}
	r := &ev.StickerPackPreview.Result
	if err != nil {
		r.Tag, r.Err.Error = abi.StickerPackResultTagErr, b(err.Error())
		e.out.Emit(&ev)
		return
	}
	r.Tag = abi.StickerPackResultTagOk
	r.Ok = abi.StickerPackResultOk{Pack: abi.StickerPackInfo{Name: b(p.name), Dir: b(p.dir), Local: p.local}, Publisher: b(publisher)}
	for _, s := range p.stickers[:min(len(p.stickers), packLimit)] {
		r.Ok.Pack.Stickers = append(r.Ok.Pack.Stickers, b(s))
	}
	e.out.Emit(&ev)
}

// viewStickerPack downloads a pack shared in a chat into the cache and
// shows it; a pack opened before shows again without downloading.
func (e *Engine) viewStickerPack(chat, id string) {
	var p waE2E.Message
	m, err := e.arch.Message(chat, id)
	if err == nil && len(m.Raw) > 0 {
		err = proto.Unmarshal(m.Raw, &p)
	}
	pack := p.GetStickerPackMessage()
	if err != nil || pack == nil {
		e.previewPack(nil, "", errors.New("This sticker pack is no longer available"))
		return
	}
	name, publisher := pack.GetName(), pack.GetPublisher()
	packID := pack.GetStickerPackID()
	if packID == "" {
		packID = id
	}
	shared := filepath.Join(e.stickerCache(), "shared")
	dir := filepath.Join(shared, sanitizeName(packID))
	for _, cached := range listPacks(shared) {
		if cached.dir == dir {
			cached.name = name
			e.previewPack(&cached, publisher, nil)
			return
		}
	}
	if !e.cli.IsConnected() {
		e.previewPack(nil, "", errors.New("Connect to WhatsApp to open this sticker pack"))
		return
	}
	if pack.GetFileLength() > fileMax {
		e.previewPack(nil, "", fmt.Errorf("Attachments larger than %d MiB cannot be opened", fileMax>>20))
		return
	}
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		var files []string
		data, err := cli.Download(ctx, pack)
		if err != nil {
			err = fmt.Errorf("Could not download the sticker pack: %w", err)
		} else {
			files, err = extractPack(data, pack, dir)
		}
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				e.previewPack(nil, "", err)
				return
			}
			e.previewPack(&stickerPack{name: name, dir: dir, stickers: files}, publisher, nil)
		}
	})
}

// extractPack is upstream's extract_whatsapp_pack: a pack's zip unpacked
// into dir in the message's order, without its tray icon, each sticker
// tagged with the emojis the message gives it.
func extractPack(data []byte, pack *waE2E.StickerPackMessage, dir string) ([]string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("This is not a sticker pack: %w", err)
	}
	emojis := map[string][]string{}
	var order []string
	for _, s := range pack.GetStickers() {
		emojis[s.GetFileName()] = s.GetEmojis()
		order = append(order, s.GetFileName())
	}
	type file struct {
		name string
		data []byte
	}
	var files []file
	var total uint64
	for _, f := range r.File {
		if f.Name == pack.GetTrayIconFileName() || !strings.HasSuffix(strings.ToLower(f.Name), ".webp") || f.UncompressedSize64 > stickerMax {
			continue
		}
		if total += f.UncompressedSize64; total > archiveMax || len(files) == packLimit*2 {
			break
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(rc, stickerMax+1))
		rc.Close()
		if err == nil && len(b) <= stickerMax {
			files = append(files, file{f.Name, b})
		}
	}
	rank := func(name string) int {
		if i := slices.Index(order, name); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortStableFunc(files, func(a, b file) int { return rank(a.name) - rank(b.name) })
	if len(files) == 0 {
		return nil, errors.New("No stickers could be read from this pack")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var written []string
	for i, f := range files {
		data := f.data
		if tags := emojis[f.name]; len(tags) > 0 {
			info, _ := av.ReadStickerInfo(data)
			info.PackName, info.Emojis = pack.GetName(), tags
			if tagged, err := av.WriteStickerInfo(data, info); err == nil {
				data = tagged
			}
		}
		path := filepath.Join(dir, fmt.Sprintf("%03d.webp", i))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return nil, err
		}
		written = append(written, path)
	}
	return written, nil
}

// sendStickerPack sends one of our packs to a chat as a WhatsApp sticker
// pack, as upstream's send_sticker_pack does.
func (e *Engine) sendStickerPack(chat, dir string) {
	if !e.writable(chat) {
		e.emitError(readOnly)
		return
	}
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		e.emitError("Not connected to WhatsApp")
		return
	}
	var pack *stickerPack
	for _, p := range listPacks(e.packsDir()) {
		if p.dir == filepath.Clean(dir) {
			pack = &p
			break
		}
	}
	if pack == nil {
		return
	}
	publisher := e.meName()
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		p, err := prepareStickerPack(ctx, cli, pack.name, publisher, pack.stickers)
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				e.emitError("Could not send the sticker pack: " + err.Error())
				return
			}
			e.outgoing(chat, j, p, nil)
		}
	})
}

// prepareStickerPack zips a pack's stickers with its tray icon (zapfast-go
// 3104c46's prepareStickerPack, MIT), uploads it with its thumbnail, and
// builds the pack message.
func prepareStickerPack(ctx context.Context, cli Client, name, publisher string, paths []string) (*waE2E.Message, error) {
	paths = paths[:min(len(paths), packLimit)]
	if len(paths) == 0 {
		return nil, errors.New("This pack has no stickers")
	}
	var random [16]byte
	rand.Read(random[:])
	id := hex.EncodeToString(random[:])
	tray := id + ".webp"
	var zipped bytes.Buffer
	w := zip.NewWriter(&zipped)
	write := func(name string, data []byte) error {
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err == nil {
			_, err = f.Write(data)
		}
		return err
	}
	var stickers []*waE2E.StickerPackMessage_Sticker
	var thumb []byte
	seen := map[string]bool{}
	total := 0
	for i, path := range paths {
		data, err := readBounded(path, stickerMax)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			icon, small, err := packArt(data)
			if err != nil {
				return nil, err
			}
			if err := write(tray, icon); err != nil {
				return nil, err
			}
			thumb = small
		}
		if total += len(data); total > fileMax {
			return nil, errors.New("the sticker pack is too large")
		}
		digest := sha256.Sum256(data)
		file := base64.RawURLEncoding.EncodeToString(digest[:]) + ".webp"
		if !seen[file] {
			seen[file] = true
			if err := write(file, data); err != nil {
				return nil, err
			}
		}
		_, _, animated := webpInfo(data)
		stickers = append(stickers, &waE2E.StickerPackMessage_Sticker{FileName: proto.String(file), IsAnimated: proto.Bool(animated), Emojis: av.StickerEmojis(data), Mimetype: proto.String("image/webp")})
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	up, th, err := cli.UploadStickerPack(ctx, zipped.Bytes(), thumb)
	if err != nil {
		return nil, err
	}
	return &waE2E.Message{StickerPackMessage: &waE2E.StickerPackMessage{
		StickerPackID: proto.String(id), Name: proto.String(name), Publisher: proto.String(publisher), Stickers: stickers,
		FileLength: proto.Uint64(up.FileLength), FileSHA256: up.FileSHA256, FileEncSHA256: up.FileEncSHA256, MediaKey: up.MediaKey,
		DirectPath: proto.String(up.DirectPath), MediaKeyTimestamp: proto.Int64(time.Now().Unix()), TrayIconFileName: proto.String(tray),
		ThumbnailDirectPath: proto.String(th.DirectPath), ThumbnailSHA256: th.FileSHA256, ThumbnailEncSHA256: th.FileEncSHA256,
		ThumbnailHeight: proto.Uint32(thumbnailSide), ThumbnailWidth: proto.Uint32(thumbnailSide), StickerPackSize: proto.Uint64(up.FileLength),
	}}, nil
}
