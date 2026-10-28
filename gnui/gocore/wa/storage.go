package wa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	xdraw "golang.org/x/image/draw"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/av"
	"zapfast/gocore/out"
	"zapfast/gocore/store"
)

// Shared media, the media cache, motion photos, and the files the UI asks
// the core to pick, copy or read: upstream's worker does these with its
// file dialogs, which here are the Pick broker's PickRequests.

// --- shared media ------------------------------------------------------------

// shared reports whether a message is one of a chat's shared media,
// documents or links.
func shared(m store.Message) bool {
	switch m.Kind {
	case store.KindImage, store.KindVideo:
		return m.Media == nil || !m.Media.Animated
	case store.KindDocument:
		return true
	case kindText:
		return m.LinkPreview != nil || strings.Contains(m.Text, "http://") || strings.Contains(m.Text, "https://") || strings.Contains(m.Text, "www.")
	}
	return false
}

// sharedChanged tells the info panel to list a chat's shared media again.
func (e *Engine) sharedChanged(chat string) {
	ev := abi.Event{Tag: abi.EventTagChatSharedChanged}
	ev.ChatSharedChanged.Chat = b(chat)
	e.out.Emit(&ev)
}

// hasShared reports whether a chat lists any shared media now.
func (e *Engine) hasShared(chat string) bool {
	sh, err := e.arch.Shared(chat, 1)
	return err == nil && (len(sh.Media) > 0 || len(sh.Docs) > 0 || len(sh.Links) > 0)
}

// listChatShared sends a chat's media, documents and links, newest first,
// at most store.GalleryPage of each, answering request.
func (e *Engine) listChatShared(chat string, request uint64) {
	sh, err := e.arch.Shared(chat, store.GalleryPage)
	if err != nil {
		ev := abi.Event{Tag: abi.EventTagChatShared}
		ev.ChatShared = abi.EventChatShared{Request: request, Chat: b(""), Last: true}
		ev.ChatShared.Error, ev.ChatShared.HasError = b(out.Toast(err.Error())), true
		e.out.Emit(&ev)
		return
	}
	type row struct {
		m    store.Message
		doc  bool
		link *store.Link
	}
	var rows []row
	for _, m := range sh.Media {
		rows = append(rows, row{m: m})
	}
	for _, m := range sh.Docs {
		rows = append(rows, row{m: m, doc: true})
	}
	for i := range sh.Links {
		if len(sh.Links[i].URL) <= 4096 {
			rows = append(rows, row{link: &sh.Links[i]})
		}
	}
	cv := e.conv()
	c := b(chat)
	out.Chunks(e.out, rows, func(r *out.Record, x row) abi.SharedEntry {
		switch {
		case x.link != nil:
			l := abi.SharedEntryLink{Message: b(x.link.Message), Timestamp: x.link.Timestamp, URL: b(x.link.URL)}
			if x.link.Title != "" {
				l.Title, l.HasTitle = b(out.Clip(x.link.Title, 1024)), true
			}
			return abi.SharedEntry{Tag: abi.SharedEntryTagLink, Link: l}
		case x.doc:
			en := abi.SharedEntry{Tag: abi.SharedEntryTagDoc}
			en.Doc.Message = cv.message(r, x.m)
			return en
		}
		en := abi.SharedEntry{Tag: abi.SharedEntryTagMedia}
		en.Media.Message = cv.message(r, x.m)
		return en
	}, func(batch uint32, last bool, chunk []abi.SharedEntry) *abi.Event {
		return &abi.Event{Tag: abi.EventTagChatShared, ChatShared: abi.EventChatShared{
			Request: request, Chat: c, Batch: batch, Last: last,
			MediaTruncated: sh.MediaTruncated && last, DocsTruncated: sh.DocsTruncated && last, LinksTruncated: sh.LinksTruncated && last,
			Entries: chunk,
		}}
	})
}

// loadChatMedia sends a page of a chat's photos and clips, oldest first,
// around a message when the viewer opened on one.
func (e *Engine) loadChatMedia(chat string, around string, hasAround bool) {
	items, err := e.arch.ChatMedia(chat, around, store.GalleryPage)
	if err != nil {
		e.emitError(err.Error())
		return
	}
	c, a := b(chat), b(around)
	out.Chunks(e.out, items, func(r *out.Record, m store.Message) abi.MediaItem {
		x := abi.MediaItem{ID: b(m.ID), Timestamp: m.Timestamp, Video: m.Kind == store.KindVideo}
		if md := m.Media; md != nil {
			if md.Path != "" && len(md.Path) <= 4096 {
				if _, err := os.Stat(md.Path); err == nil {
					x.Path, x.HasPath = b(md.Path), true
				}
			}
			if len(md.Thumbnail) > 0 {
				x.Thumbnail, x.HasThumbnail = r.Blob(md.Thumbnail), true
			}
		}
		return x
	}, func(batch uint32, last bool, chunk []abi.MediaItem) *abi.Event {
		return &abi.Event{Tag: abi.EventTagChatMedia, ChatMedia: abi.EventChatMedia{Chat: c, Around: a, HasAround: hasAround, Batch: batch, Last: last, Items: chunk}}
	})
}

// --- the media cache ---------------------------------------------------------

// storageKind is upstream's StorageStats::add: a cached file counts under
// the kind its name says; GIFs arrive as MP4 and count with the videos.
func storageKind(s *abi.StorageStats, path string, size uint64) {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "jpg", "jpeg", "png", "heic", "heif", "bmp", "tif", "tiff":
		s.Images++
		s.ImageBytes += size
	case "mp4", "m4v", "mov", "3gp", "3gpp", "webm", "mkv", "gif":
		s.Videos++
		s.VideoBytes += size
	case "webp":
		s.Stickers++
		s.StickerBytes += size
	default:
		s.Other++
		s.OtherBytes += size
	}
}

// staging is a download still being written.
func staging(name string) bool {
	return strings.HasPrefix(name, ".download-") || strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".part")
}

// walkMedia visits every finished file under dir; an absent dir is empty.
func walkMedia(dir string, visit func(path string, info fs.FileInfo)) error {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() && !staging(d.Name()) {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			visit(path, info)
		}
		return nil
	})
	return err
}

// measureStorage measures the media cache (after emptying it, with clear)
// and answers token. Emptying makes the archive forget the files that are
// gone, so they download again rather than draw broken.
func (e *Engine) measureStorage(token uint64, empty bool) {
	dir := e.mediaCache()
	e.job(func(context.Context) func() {
		if empty {
			var failed bool
			walkMedia(dir, func(path string, _ fs.FileInfo) {
				if os.Remove(path) != nil {
					failed = true
				}
			})
			if failed {
				slog.Warn("zapfast wa: could not delete every cached attachment")
			}
		}
		var stats abi.StorageStats
		err := walkMedia(dir, func(path string, info fs.FileInfo) { storageKind(&stats, path, uint64(info.Size())) })
		return func() {
			if empty {
				if _, err := e.arch.ForgetMissingMedia(); err != nil {
					slog.Warn("zapfast wa: forgetting deleted attachments", "err", err)
				}
				clear(e.failed)
			}
			ev := abi.Event{Tag: abi.EventTagStorageStats}
			ev.StorageStats = abi.EventStorageStats{Token: token, Cleared: empty}
			if err == nil {
				ev.StorageStats.Stats, ev.StorageStats.HasStats = stats, true
			} else {
				slog.Warn("zapfast wa: could not measure the attachment cache")
			}
			e.out.Emit(&ev)
		}
	})
}

// --- motion photos -----------------------------------------------------------

// motionKey is where a clip waits for a photo not archived yet.
func motionKey(chat, id string) string { return "motion/" + cacheName(chat, id) }

// motionArrived files a motion photo's clip on its photo, or keeps it for
// the photo when it is not here yet.
func (e *Engine) motionArrived(chat, parent string, clip []byte) error {
	err := e.arch.UpdateMessage(chat, parent, func(m *store.Message) {
		path := ""
		if m.Motion != nil {
			path = m.Motion.Path
		}
		m.Motion = &store.Motion{Clip: clip, Path: path}
	})
	if noRows(err) {
		return e.arch.SetMetadata(motionKey(chat, parent), clip)
	}
	if err == nil {
		e.emitMessage(chat, parent)
	}
	return err
}

// pendingMotion gives a photo that just arrived the clip that came before
// it.
func (e *Engine) pendingMotion(chat, id string) {
	clip, err := e.arch.Metadata(motionKey(chat, id))
	if err != nil || len(clip) == 0 {
		return
	}
	if err := e.arch.UpdateMessage(chat, id, func(m *store.Message) { m.Motion = &store.Motion{Clip: clip} }); err == nil {
		e.arch.SetMetadata(motionKey(chat, id), nil)
	}
}

func (e *Engine) motionState(chat, id string) abi.MediaState {
	return e.cardState(chat, id, motionCard)
}

// motionCard is the downloads' card number of a motion clip.
const motionCard = -2

// downloadMotion fetches a motion photo's clip into the cache: Motion
// answers, and the photo's row gets its path.
func (e *Engine) downloadMotion(chat, id string) {
	key := downloadKey{chat, id, motionCard}
	if e.downloads[key] {
		return
	}
	answer := func(path string, err error) {
		ev := abi.Event{Tag: abi.EventTagMotion}
		ev.Motion = abi.EventMotion{Chat: b(chat), Message: b(id), Result: pathResult(path, err)}
		e.out.Emit(&ev)
	}
	m, err := e.arch.Message(chat, id)
	var video waE2E.Message
	switch {
	case err != nil || m.Motion == nil || len(m.Motion.Clip) == 0 || proto.Unmarshal(m.Motion.Clip, &video) != nil || video.GetVideoMessage() == nil:
		answer("", errors.New("The clip has not arrived"))
		return
	case !e.cli.IsConnected():
		answer("", errors.New("Not connected to WhatsApp"))
		return
	case video.GetVideoMessage().GetFileLength() > downloadLimit:
		answer("", fmt.Errorf("Attachments over %d MB download when you open them", downloadLimit>>20))
		return
	}
	e.downloads[key] = true
	delete(e.failed, failKey(chat, id, motionCard))
	e.emitMessage(chat, id)
	cli, gen := e.cli, e.gen
	source := video.GetVideoMessage()
	e.job(func(ctx context.Context) func() {
		data, err := cli.Download(ctx, source)
		return func() {
			delete(e.downloads, key)
			if gen != e.gen {
				return
			}
			path := ""
			if err == nil {
				path, err = e.keepMedia(chat, id+"-motion", "video/mp4", data)
			}
			if err == nil {
				err = e.arch.UpdateMessage(chat, id, func(m *store.Message) {
					if m.Motion != nil {
						m.Motion.Path = path
					}
				})
			}
			if err != nil {
				e.failed[failKey(chat, id, motionCard)] = err
			}
			answer(path, err)
			e.emitMessage(chat, id)
		}
	})
}

// --- pickers and files -------------------------------------------------------

// ask shows a dialog and hands its answer to then on the engine's
// goroutine; a cancelled dialog does nothing.
func (e *Engine) ask(req abi.EventPickRequest, then func(paths []string)) {
	e.job(func(ctx context.Context) func() {
		paths := e.h.Pick(ctx, &req)
		if len(paths) == 0 {
			return nil
		}
		return func() { then(paths) }
	})
}

func filters(name string, exts ...string) []abi.PickFilter {
	f := abi.PickFilter{Name: b(name)}
	for _, x := range exts {
		f.Extensions = append(f.Extensions, b(x))
	}
	return []abi.PickFilter{f}
}

var audioFilter = filters("Audio", "wav", "mp3", "ogg", "oga")

// pickFiles asks for files to send; Picked hands them to the composer.
func (e *Engine) pickFiles(chat string, documents bool) {
	e.job(func(ctx context.Context) func() {
		paths := e.h.Pick(ctx, &abi.EventPickRequest{Kind: abi.PickKindFiles, Title: b("Send to WhatsApp")})
		return func() {
			ev := abi.Event{Tag: abi.EventTagPicked}
			ev.Picked = abi.EventPicked{Chat: b(chat), Documents: documents}
			for _, p := range paths[:min(len(paths), 64)] {
				ev.Picked.Paths = append(ev.Picked.Paths, b(p))
			}
			e.out.Emit(&ev)
		}
	})
}

// downloads is the folder a save dialog opens in.
func downloads() ([]byte, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, false
	}
	return b(filepath.Join(home, "Downloads")), true
}

func (e *Engine) info(text string) {
	ev := abi.Event{Tag: abi.EventTagInfo}
	ev.Info.Text = b(out.Toast(text))
	e.out.Emit(&ev)
}

// saveAttachmentAs copies an attachment where the reader chooses.
func (e *Engine) saveAttachmentAs(source, name string) {
	req := abi.EventPickRequest{Kind: abi.PickKindSave, Title: b("Save attachment"), FileName: b(name), HasFileName: true}
	req.Directory, req.HasDirectory = downloads()
	e.job(func(ctx context.Context) func() {
		paths := e.h.Pick(ctx, &req)
		if len(paths) == 0 {
			return nil
		}
		err := copyFile(source, paths[0])
		return func() {
			if err != nil {
				e.emitError("Could not save the attachment: " + err.Error())
				return
			}
			e.info("Saved " + filepath.Base(paths[0]))
		}
	})
}

// saveAttachments copies an album's files into a folder, never over a
// file already there.
func (e *Engine) saveAttachments(list []abi.SaveFile) {
	type saveFile struct{ source, name string }
	files := make([]saveFile, len(list))
	for i, f := range list {
		files[i] = saveFile{string(f.Source), string(f.Name)}
	}
	req := abi.EventPickRequest{Kind: abi.PickKindFolder, Title: b("Save album")}
	req.Directory, req.HasDirectory = downloads()
	e.job(func(ctx context.Context) func() {
		paths := e.h.Pick(ctx, &req)
		if len(paths) == 0 {
			return nil
		}
		saved := 0
		var failed []string
		for _, f := range files {
			name := filepath.Base(f.name)
			if name == "." || name == "/" || name == "" {
				name = filepath.Base(f.source)
			}
			if err := copyFile(f.source, freeName(paths[0], name)); err != nil {
				failed = append(failed, name+": "+err.Error())
			} else {
				saved++
			}
		}
		return func() {
			if len(failed) == 0 {
				e.info(fmt.Sprintf("Saved %d album attachments", saved))
				return
			}
			e.emitError(fmt.Sprintf("Saved %d album attachments; %d could not be saved: %s", saved, len(failed), strings.Join(failed, "; ")))
		}
	})
}

// freeName is name in dir, numbered as file managers do when it is taken:
// "a.jpg", "a 2.jpg", "a 3.jpg".
func freeName(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	path := filepath.Join(dir, name)
	for n := 2; ; n++ {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return path
		}
		path = filepath.Join(dir, fmt.Sprintf("%s %d%s", stem, n, ext))
	}
}

// copyFile copies a regular file through a temporary file beside target.
func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if info, err := in.Stat(); err != nil || !info.Mode().IsRegular() {
		return errors.New("not a file")
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".zap-copy-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

func (e *Engine) pickNotificationSound(mention bool) {
	e.ask(abi.EventPickRequest{Kind: abi.PickKindFile, Title: b("Choose a notification sound"), Filters: audioFilter}, func(paths []string) {
		ev := abi.Event{Tag: abi.EventTagNotificationSoundPicked}
		ev.NotificationSoundPicked = abi.EventNotificationSoundPicked{Mention: mention, Path: b(paths[0])}
		e.out.Emit(&ev)
	})
}

func (e *Engine) pickChatSound(chat string) {
	e.ask(abi.EventPickRequest{Kind: abi.PickKindFile, Title: b("Choose a notification sound"), Filters: audioFilter}, func(paths []string) {
		ev := abi.Event{Tag: abi.EventTagChatSoundPicked}
		ev.ChatSoundPicked = abi.EventChatSoundPicked{Chat: b(chat), Path: b(paths[0])}
		e.out.Emit(&ev)
	})
}

func (e *Engine) pickDownloadFolder() {
	e.ask(abi.EventPickRequest{Kind: abi.PickKindFolder, Title: b("Choose a folder for downloads")}, func(paths []string) {
		ev := abi.Event{Tag: abi.EventTagDownloadFolderPicked}
		ev.DownloadFolderPicked.Path = b(paths[0])
		e.out.Emit(&ev)
	})
}

// wallpaperSide is the longest side a wallpaper keeps (upstream's
// wallpaper_files::MAX_SIDE).
const wallpaperSide = 2560

var wallpaperExts = []string{"jpg", "png", "webp", "gif"}

// pickWallpaperImage copies a chosen picture to the account's own
// wallpaper.<ext>, scaled down past wallpaperSide, so the original may move.
func (e *Engine) pickWallpaperImage() {
	state, _ := e.h.AccountDirs()
	req := abi.EventPickRequest{Kind: abi.PickKindFile, Title: b("Choose a wallpaper image"), Filters: filters("Images", "jpg", "jpeg", "png", "webp", "gif")}
	e.job(func(ctx context.Context) func() {
		paths := e.h.Pick(ctx, &req)
		if len(paths) == 0 {
			return nil
		}
		path, err := importWallpaper(paths[0], state)
		return func() {
			ev := abi.Event{Tag: abi.EventTagWallpaperImagePicked}
			ev.WallpaperImagePicked.Result = pathResult(path, err)
			e.out.Emit(&ev)
		}
	})
}

func importWallpaper(source, state string) (string, error) {
	data, err := readBounded(source, pictureMax)
	if err != nil {
		return "", err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("not an image")
	}
	ext := map[string]string{"jpeg": "jpg", "png": "png", "webp": "webp", "gif": "gif"}[format]
	if ext == "" {
		return "", errors.New("unsupported image format")
	}
	if max(cfg.Width, cfg.Height) > wallpaperSide {
		m, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		data, ext, err = scaledDown(m, wallpaperSide)
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(state, "wallpaper."+ext)
	if err := writePrivate(target, data); err != nil {
		return "", err
	}
	for _, other := range wallpaperExts {
		if other != ext {
			os.Remove(filepath.Join(state, "wallpaper."+other))
		}
	}
	return target, nil
}

// scaledDown is a picture with its longest side cut to side: a PNG when
// it has transparency, else a JPEG.
func scaledDown(m image.Image, side int) ([]byte, string, error) {
	bnd := m.Bounds()
	scale := float64(side) / float64(max(bnd.Dx(), bnd.Dy()))
	r := image.Rect(0, 0, max(int(float64(bnd.Dx())*scale), 1), max(int(float64(bnd.Dy())*scale), 1))
	small := image.NewNRGBA(r)
	xdraw.CatmullRom.Scale(small, r, m, bnd, draw.Src, nil)
	var buf bytes.Buffer
	for i := 3; i < len(small.Pix); i += 4 {
		if small.Pix[i] < 0xff {
			if err := png.Encode(&buf, small); err != nil {
				return nil, "", err
			}
			return buf.Bytes(), "png", nil
		}
	}
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: 90}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "jpg", nil
}

func (e *Engine) removeWallpaperImage() {
	state, _ := e.h.AccountDirs()
	for _, ext := range wallpaperExts {
		if err := os.Remove(filepath.Join(state, "wallpaper."+ext)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("zapfast wa: could not delete the wallpaper image", "err", err)
		}
	}
}

// writePrivate writes data to path through a temporary file, readable by
// the user only.
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".zap-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// prepareClipboardImage decodes a picture the clipboard saved to a file
// into RGBA rows for the composer.
func (e *Engine) prepareClipboardImage(path string) {
	e.job(func(context.Context) func() {
		w, h, rgba, err := decodeRGBA(path)
		return func() {
			r := e.out.Record()
			ev := abi.Event{Tag: abi.EventTagClipboardImage}
			res := &ev.ClipboardImage.Result
			if err != nil {
				res.Tag = abi.ClipboardImageResultTagErr
				res.Err.Error = b(out.Toast(err.Error()))
			} else {
				res.Tag = abi.ClipboardImageResultTagOk
				res.Ok.Image = abi.DecodedImage{Width: uint32(w), Height: uint32(h), RGBA: r.Blob(rgba)}
			}
			r.Emit(&ev)
		}
	})
}

func decodeRGBA(path string) (int, int, []byte, error) {
	data, err := readBounded(path, pictureMax)
	if err != nil {
		return 0, 0, nil, err
	}
	w, h, ok := pictureSize(data)
	if !ok {
		return 0, 0, nil, errors.New("not a picture")
	}
	if uint64(w)*uint64(h) > 100_000_000 {
		return 0, 0, nil, errors.New("the picture is too large")
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, 0, nil, err
	}
	bnd := m.Bounds()
	n := image.NewNRGBA(image.Rect(0, 0, bnd.Dx(), bnd.Dy()))
	draw.Draw(n, n.Rect, m, bnd.Min, draw.Src)
	return bnd.Dx(), bnd.Dy(), n.Pix, nil
}

// openLog hands the log to the UI to open: the core opens no files.
func (e *Engine) openLog(path []byte) {
	ev := abi.Event{Tag: abi.EventTagOpenPath}
	ev.OpenPath.Path = path
	e.out.Emit(&ev)
}

// --- picture edits -----------------------------------------------------------

func (e *Engine) inspectPicture(target uint64, path string) {
	e.job(func(context.Context) func() {
		var res abi.SizeResult
		data, err := readBounded(path, pictureMax)
		var w, h int
		if err == nil {
			w, h, err = av.InspectPicture(data)
		}
		if err != nil {
			res.Tag = abi.SizeResultTagErr
			res.Err.Error = b(out.Toast(err.Error()))
		} else {
			res.Tag = abi.SizeResultTagOk
			res.Ok.Width, res.Ok.Height = uint32(w), uint32(h)
		}
		return func() {
			ev := abi.Event{Tag: abi.EventTagPictureInspected}
			ev.PictureInspected = abi.EventPictureInspected{Target: target, Result: res}
			e.out.Emit(&ev)
		}
	})
}

// applyPictureEdit turns and crops a picture (a file, or pasted RGBA) and
// writes it under the media cache's edited/.
func (e *Engine) applyPictureEdit(c *abi.CommandApplyPictureEdit, pasted []byte) {
	dir := filepath.Join(e.mediaCache(), "edited")
	crop := av.PictureCrop{X: c.Crop.X, Y: c.Crop.Y, Width: c.Crop.Width, Height: c.Crop.Height}
	turns := int(c.Turns)
	target := c.Target
	isPasted, file := c.Source.Tag == abi.PictureSourceTagPasted, string(c.Source.File.Path)
	w, h := int(c.Width), int(c.Height)
	e.job(func(context.Context) func() {
		var edited av.Edited
		var err error
		if isPasted {
			edited, err = av.EditPasted(pasted, w, h, crop, turns)
		} else {
			var data []byte
			if data, err = readBounded(file, pictureMax); err == nil {
				edited, err = av.EditPicture(data, crop, turns)
			}
		}
		path := ""
		if err == nil {
			path, err = av.WriteEdited(edited, dir)
		}
		return func() {
			ev := abi.Event{Tag: abi.EventTagPictureEdited}
			ev.PictureEdited = abi.EventPictureEdited{Target: target, Result: pathResult(path, err)}
			e.out.Emit(&ev)
		}
	})
}
