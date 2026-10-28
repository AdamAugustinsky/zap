package wa

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
)

// shelves are a Stickers event's rows by kind: the paths of each, and the
// emojis of the tagged ones.
type shelves struct {
	favorites, recent, received, packStickers []string
	packs                                     []abi.StickerEntryPack
	tagged                                    map[string][]string
}

func stickersOf(t *testing.T, evs []abi.Event) shelves {
	t.Helper()
	s := shelves{tagged: map[string][]string{}}
	for _, en := range one(t, evs, abi.EventTagStickers).Stickers.Entries {
		switch en.Tag {
		case abi.StickerEntryTagFavorite:
			s.favorites = append(s.favorites, string(en.Favorite.Path))
		case abi.StickerEntryTagPack:
			s.packs = append(s.packs, en.Pack)
		case abi.StickerEntryTagPackSticker:
			s.packStickers = append(s.packStickers, string(en.PackSticker.Path))
		case abi.StickerEntryTagRecent:
			s.recent = append(s.recent, string(en.Recent.Path))
		case abi.StickerEntryTagReceived:
			s.received = append(s.received, string(en.Received.Path))
		case abi.StickerEntryTagTagged:
			for _, x := range en.Tagged.Emojis {
				s.tagged[string(en.Tagged.Path)] = append(s.tagged[string(en.Tagged.Path)], string(x))
			}
		}
	}
	return s
}

func TestStickerShelves(t *testing.T) {
	h := linked(t)
	s1 := writeSticker(t, t.TempDir(), "s1.webp", 1)
	data, _ := os.ReadFile(s1)

	h.send(abi.Command{Tag: abi.CommandTagSaveSticker, SaveSticker: abi.CommandSaveSticker{Path: []byte(s1)}})
	s := stickersOf(t, h.take())
	favorite := filepath.Join(h.e.favoritesDir(), contentHash(data)+".webp")
	if len(s.favorites) != 1 || s.favorites[0] != favorite {
		t.Fatalf("favorites: %+v", s)
	}

	h.send(abi.Command{Tag: abi.CommandTagSendSticker, SendSticker: abi.CommandSendSticker{Chat: chatID(peer), Path: []byte(s1)}})
	h.take()
	st := h.f.lastSent().msg.GetStickerMessage()
	if st.GetMimetype() != "image/webp" || st.GetWidth() != 64 || st.GetHeight() != 64 || st.GetIsAnimated() || h.f.uploads[len(h.f.uploads)-1] != whatsmeow.MediaImage {
		t.Fatalf("sent: %v", st)
	}

	h.send(abi.Command{Tag: abi.CommandTagRecentStickers})
	s = stickersOf(t, h.take())
	if len(s.recent) != 1 || len(s.favorites) != 1 {
		t.Fatalf("recent: %+v", s)
	}
	recent := s.recent[0]

	h.send(abi.Command{Tag: abi.CommandTagRemoveRecentSticker, RemoveRecentSticker: abi.CommandRemoveRecentSticker{Path: []byte(recent)}})
	if s := stickersOf(t, h.take()); len(s.recent) != 0 {
		t.Fatalf("removed: %+v", s)
	}
	if p := h.f.patches[len(h.f.patches)-1].Mutations[0]; p.Index[0] != "removeRecentSticker" || p.Value.GetRemoveRecentStickerAction() == nil {
		t.Fatalf("patch: %+v", p)
	}

	// A sticker someone sent, once downloaded.
	h.receive(peer, peer, "R1", t0, receivedSticker())
	h.f.downloaded = stickerBytes(t, 2)
	h.send(abi.Command{Tag: abi.CommandTagDownload, Download: abi.CommandDownload{Chat: chatID(peer), Message: []byte("R1")}})
	h.take()
	h.send(abi.Command{Tag: abi.CommandTagRecentStickers})
	if s := stickersOf(t, h.take()); len(s.received) != 1 {
		t.Fatalf("received: %+v", s)
	}

	// Only a favorite's file may be forgotten.
	h.send(abi.Command{Tag: abi.CommandTagForgetSticker, ForgetSticker: abi.CommandForgetSticker{Path: []byte(s1)}})
	expectTags(t, h.take(), "")
	h.send(abi.Command{Tag: abi.CommandTagForgetSticker, ForgetSticker: abi.CommandForgetSticker{Path: []byte(favorite)}})
	if s := stickersOf(t, h.take()); len(s.favorites) != 0 {
		t.Fatalf("forgotten: %+v", s)
	}
	if _, err := os.Stat(s1); err != nil {
		t.Fatal("the original went")
	}

	// Offline, the sticker goes back to the composer.
	h.f.setConnected(false)
	h.send(abi.Command{Tag: abi.CommandTagSendSticker, SendSticker: abi.CommandSendSticker{Chat: chatID(peer), Path: []byte(s1)}})
	if r := one(t, h.take(), abi.EventTagSendRefused).SendRefused; r.Reason != abi.RefusalOffline || r.Unsent.Tag != abi.UnsentTagSticker {
		t.Fatalf("refused: %+v", r)
	}
}

func TestMakeSticker(t *testing.T) {
	h := linked(t)
	png := writePNG(t, t.TempDir(), 40, 30, true)

	h.send(abi.Command{Tag: abi.CommandTagPickStickerPicture})
	if req := h.pick(png); string(req.Title) != "Make a sticker" {
		t.Fatalf("dialog: %+v", req)
	}
	if p := one(t, h.take(), abi.EventTagStickerPicture).StickerPicture; string(p.Path) != png || p.Width != 40 || p.Height != 30 || !p.Transparent {
		t.Fatalf("picture: %+v", p)
	}

	mk := abi.CommandMakeSticker{Source: []byte(png), Crop: abi.StickerCrop{X: 5, Side: 30}, Transparent: true, Emojis: [][]byte{[]byte("😀")}}
	h.send(abi.Command{Tag: abi.CommandTagMakeSticker, MakeSticker: mk})
	evs := h.take()
	s := stickersOf(t, evs)
	if len(s.favorites) != 1 || strings.Join(s.tagged[s.favorites[0]], "") != "😀" || string(one(t, evs, abi.EventTagInfo).Info.Text) != "Added to favorites" {
		t.Fatalf("made: %+v", s)
	}

	mk.Chat, mk.HasChat = chatID(peer), true
	sent := h.f.sentCount()
	h.send(abi.Command{Tag: abi.CommandTagMakeSticker, MakeSticker: mk})
	h.take()
	if h.f.sentCount() != sent+1 || h.f.lastSent().msg.GetStickerMessage() == nil {
		t.Fatal("the made sticker was not sent")
	}
}

func TestStickerPacks(t *testing.T) {
	h := linked(t)
	s1 := writeSticker(t, t.TempDir(), "s1.webp", 1)

	h.send(abi.Command{Tag: abi.CommandTagCreateStickerPack, CreateStickerPack: abi.CommandCreateStickerPack{Name: []byte("Mine")}})
	s := stickersOf(t, h.take())
	if len(s.packs) != 1 || string(s.packs[0].Name) != "Mine" || !s.packs[0].Local {
		t.Fatalf("created: %+v", s)
	}
	pack := string(s.packs[0].Dir)

	h.send(abi.Command{Tag: abi.CommandTagSetStickerPack, SetStickerPack: abi.CommandSetStickerPack{Pack: []byte(pack), Sticker: []byte(s1), Member: true}})
	if s := stickersOf(t, h.take()); len(s.packStickers) != 1 || filepath.Dir(s.packStickers[0]) != pack {
		t.Fatalf("filed: %+v", s)
	}

	h.send(abi.Command{Tag: abi.CommandTagSendStickerPack, SendStickerPack: abi.CommandSendStickerPack{Chat: chatID(peer), Dir: []byte(pack)}})
	h.take()
	sent := h.f.lastSent()
	p := sent.msg.GetStickerPackMessage()
	if p.GetName() != "Mine" || p.GetPublisher() != "Ada" || len(p.GetStickers()) != 1 || p.GetThumbnailDirectPath() != "/v/thumb" || p.GetDirectPath() != "/v/pack" {
		t.Fatalf("sent: %v", p)
	}
	zipped := h.f.more().packZip
	r, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil || len(r.File) != 2 || r.File[0].Name != p.GetTrayIconFileName() || r.File[1].Name != p.GetStickers()[0].GetFileName() {
		t.Fatalf("zip: %v", err)
	}

	// Opened from the chat: downloaded into the cache once.
	h.f.downloaded = zipped
	h.send(abi.Command{Tag: abi.CommandTagViewStickerPack, ViewStickerPack: abi.CommandViewStickerPack{Chat: chatID(peer), Message: []byte(sent.id)}})
	v := one(t, h.take(), abi.EventTagStickerPackPreview).StickerPackPreview.Result
	if v.Tag != abi.StickerPackResultTagOk || string(v.Ok.Pack.Name) != "Mine" || string(v.Ok.Publisher) != "Ada" || len(v.Ok.Pack.Stickers) != 1 {
		t.Fatalf("preview: %+v", v)
	}
	downloads := h.f.called("Download")
	h.send(abi.Command{Tag: abi.CommandTagViewStickerPack, ViewStickerPack: abi.CommandViewStickerPack{Chat: chatID(peer), Message: []byte(sent.id)}})
	if v := one(t, h.take(), abi.EventTagStickerPackPreview).StickerPackPreview.Result; v.Tag != abi.StickerPackResultTagOk || h.f.called("Download") != downloads {
		t.Fatalf("again: %+v", v)
	}
	h.send(abi.Command{Tag: abi.CommandTagViewStickerPack, ViewStickerPack: abi.CommandViewStickerPack{Chat: chatID(peer), Message: []byte("M1")}})
	if v := one(t, h.take(), abi.EventTagStickerPackPreview).StickerPackPreview.Result; v.Tag != abi.StickerPackResultTagErr {
		t.Fatalf("not a pack: %+v", v)
	}

	h.send(abi.Command{Tag: abi.CommandTagAddStickerPack, AddStickerPack: abi.CommandAddStickerPack{Dir: v.Ok.Pack.Dir, Name: []byte("Copy")}})
	evs := h.take()
	if s := stickersOf(t, evs); len(s.packs) != 2 || string(one(t, evs, abi.EventTagInfo).Info.Text) != `Added sticker pack "Copy"` {
		t.Fatalf("added: %+v", s)
	}

	// Only packs here may be deleted.
	outside := t.TempDir()
	h.send(abi.Command{Tag: abi.CommandTagDeleteStickerPack, DeleteStickerPack: abi.CommandDeleteStickerPack{Dir: []byte(outside)}})
	expectTags(t, h.take(), "")
	h.send(abi.Command{Tag: abi.CommandTagDeleteStickerPack, DeleteStickerPack: abi.CommandDeleteStickerPack{Dir: []byte(pack)}})
	if s := stickersOf(t, h.take()); len(s.packs) != 1 || string(s.packs[0].Name) != "Copy" {
		t.Fatalf("deleted: %+v", s)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal(err)
	}
}

func TestStickerArchive(t *testing.T) {
	h := linked(t)
	path := filepath.Join(t.TempDir(), "fun.wastickers")
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range map[string][]byte{"title.txt": []byte("Fun"), "1.webp": stickerBytes(t, 1), "2.webp": stickerBytes(t, 2), "tray.png": []byte("x")} {
		f, _ := w.Create(name)
		f.Write(data)
	}
	w.Close()
	os.WriteFile(path, buf.Bytes(), 0o600)

	h.send(abi.Command{Tag: abi.CommandTagPickStickerArchive})
	h.pick(path)
	evs := h.take()
	s := stickersOf(t, evs)
	if len(s.packs) != 1 || string(s.packs[0].Name) != "Fun" || len(s.packStickers) != 2 || string(one(t, evs, abi.EventTagInfo).Info.Text) != `Added sticker pack "Fun"` {
		t.Fatalf("imported: %+v", s)
	}
}

// signalBlob encrypts as signal.art's CDN does.
func signalBlob(t *testing.T, key, plain []byte) []byte {
	derived, err := hkdf.Key(sha256.New, key, make([]byte, 32), "Sticker Pack", 64)
	if err != nil {
		t.Fatal(err)
	}
	pad := 16 - len(plain)%16
	plain = append(bytes.Clone(plain), bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := bytes.Repeat([]byte{7}, 16)
	block, _ := aes.NewCipher(derived[:32])
	ct := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, plain)
	text := append(iv, ct...)
	mac := hmac.New(sha256.New, derived[32:])
	mac.Write(text)
	return mac.Sum(text)
}

func TestImportSignalPack(t *testing.T) {
	h := linked(t)
	id, key := "0123456789abcdef0123456789abcdef", bytes.Repeat([]byte{9}, 32)
	var manifest []byte
	manifest = protowire.AppendTag(manifest, 1, protowire.BytesType)
	manifest = protowire.AppendString(manifest, "Signal Fun")
	for i, emoji := range []string{"🐱", "🐶"} {
		var entry []byte
		entry = protowire.AppendTag(entry, 1, protowire.VarintType)
		entry = protowire.AppendVarint(entry, uint64(i))
		entry = protowire.AppendTag(entry, 2, protowire.BytesType)
		entry = protowire.AppendString(entry, emoji)
		manifest = protowire.AppendTag(manifest, 4, protowire.BytesType)
		manifest = protowire.AppendBytes(manifest, entry)
	}
	files := map[string][]byte{
		"/stickers/" + id + "/manifest.proto": signalBlob(t, key, manifest),
		"/stickers/" + id + "/full/0":         signalBlob(t, key, stickerBytes(t, 3)),
		"/stickers/" + id + "/full/1":         signalBlob(t, key, stickerBytes(t, 4)),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()
	h.e.opts.Web, h.e.signal = srv.Client(), srv.URL+"/stickers/"

	link := fmt.Sprintf("https://signal.art/addstickers/#pack_id=%s&pack_key=%s", id, hex.EncodeToString(key))
	h.send(abi.Command{Tag: abi.CommandTagImportStickerUrl, ImportStickerUrl: abi.CommandImportStickerUrl{URL: []byte(link)}})
	evs := h.take()
	s := stickersOf(t, evs)
	if len(s.packs) != 1 || string(s.packs[0].Name) != "Signal Fun" || len(s.packStickers) != 2 || strings.Join(s.tagged[s.packStickers[0]], "") != "🐱" {
		t.Fatalf("imported: %+v", s)
	}
	if string(one(t, evs, abi.EventTagInfo).Info.Text) != `Added sticker pack "Signal Fun"` {
		t.Fatalf("info: %s", tags(evs))
	}

	h.send(abi.Command{Tag: abi.CommandTagImportStickerUrl, ImportStickerUrl: abi.CommandImportStickerUrl{URL: []byte("https://example.com/#pack_id=1")}})
	if evs := h.take(); lastError(t, evs) != "Could not add sticker pack: use a complete https://signal.art pack link" {
		t.Fatalf("bad link: %s", lastError(t, evs))
	}
}

// receivedSticker is a sticker someone sent, not downloaded yet.
func receivedSticker() *waE2E.Message {
	return &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
		Mimetype: proto.String("image/webp"), DirectPath: proto.String("/v/s"), MediaKey: []byte("key"),
		FileSHA256: bytes.Repeat([]byte{4}, 32), FileEncSHA256: []byte("enc"), FileLength: proto.Uint64(100),
	}}
}
