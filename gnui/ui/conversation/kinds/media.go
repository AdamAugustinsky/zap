package kinds

import (
	"errors"
	"net/url"
	"path/filepath"
	"strings"

	"gonativeui/img"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/calls"
	"zapfast/ui/conversation/av/thumbnail"
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// ---- the image cache ----

// images is the window's image cache, made here when no screen made one, and
// polled once per frame.
func (b *blk) images() *img.Cache {
	thumbnail.Frame(b.c.Frame())
	ca := b.k.Images()
	if ca == nil {
		ca = img.NewCache(img.Options{Renderer: b.e.A.R})
		b.k.SetImages(ca)
	}
	screen.PollImages(b.e.K, b.c)
	return ca
}

// file requests the image file at path, decoded to fit w×h pixels.
func (b *blk) file(path []byte, w, h int32, fl img.Flags) (img.Handle, img.State) {
	ca := b.images()
	key := img.Key{Hash: img.HashBytes(path), W: w, H: h, Flags: fl}
	if hd, st, ok := ca.Get(key); ok {
		return hd, st
	}
	return ca.Request(key, img.File(string(path)))
}

// ---- inline thumbnails ----

var thumbs = thumbnail.New()

const maxThumbs = 512

// thumbnail snapshots an inline thumbnail's bytes, nil while
// they are read (on a worker; the window is woken when they are in).
func (b *blk) thumbnail(ref abi.BlobRef) img.Lease {
	return thumbs.Get(b.e.S, b.a, ref, maxThumbs)
}

// thumbImage requests the thumbnail of the message as an image of w×h pixels.
func (b *blk) thumbImage(ref abi.BlobRef, w, h int32, fl img.Flags) (img.Handle, bool) {
	ca := b.images()
	key := img.Key{Hash: ref.ID ^ b.a.Serial*0x9e3779b97f4a7c15 ^ 0x7468756d62, W: w, H: h, Flags: fl}
	if hd, st, ok := ca.Get(key); ok {
		return hd, st != img.Failed
	}
	t := b.thumbnail(ref)
	if t == nil {
		return 0, false
	}
	hd, st := ca.Request(key, img.Leased(t))
	return hd, st != img.Failed
}

// ---- downloads ----

// MaxDownload is the largest attachment fetched without being asked
// (zf:src/model.rs ATTACHMENT_DOWNLOAD_LIMIT).
const maxDownload = 64 << 20

// download asks the core for the message's media (Action::Download). An
// automatic request is made once per message state.
func (b *blk) download(manual bool, card int) {
	if b.a == nil {
		return
	}
	d := b.decoded()
	id, gen := mkey.Of(b.a, b.m.ID()), b.m.Gen()
	slot := &b.loc().requested[(id.Slot(64)+(card+1)*7)%64]
	if !manual && slot.id == id && slot.card == card && slot.gen == gen {
		return
	}
	slot.id, slot.card, slot.gen = id, card, gen
	cmd := b.a.Cmd(abi.CommandTagDownload)
	cmd.Download.Manual = manual
	if card >= 0 {
		cmd.Download.HasCard, cmd.Download.Card = true, uint64(card)
	}
	cmd.Download.Chat, cmd.Download.Message = d.Chat, b.m.ID()
	b.a.Send(cmd)
}

// autoDownload reports whether a message that scrolled into view may fetch
// its media without being asked (auto_download_allowed).
func (b *blk) autoDownload(size uint64, sticker bool) bool {
	return size <= maxDownload && (sticker || (b.a != nil && b.a.Settings.AutoDownload))
}

// ---- opening things ----

// What asks the system to open things; tests replace them.
var (
	openPathFn func(string) error   // nil: the state's Shell
	openURLFn  func(string) error   // nil: the state's Shell
	revealFn   func([]string) error // nil: the state's Shell, if it reveals
)

// opener is what opens a path or a link: fn when a test set one
// (SetOpeners), else the state's Shell, as every screen opens through it.
func opener(s *state.State, fn func(string) error) func(string) error {
	switch {
	case fn != nil:
		return fn
	case s.Shell != nil:
		return s.Shell.Open
	}
	return func(string) error { return errors.ErrUnsupported }
}

// openViewer shows the message's media in the viewer window: card is a
// carousel card's index, model.PartMain for the message's own picture.
func (b *blk) openViewer(card int) {
	if b.a == nil {
		return
	}
	chat := b.a.Open
	if ref := b.a.Model.Chats.Find(b.decoded().Chat); ref != 0 {
		chat = ref
	}
	b.e.S.OpenViewer(chat, b.m.ID(), card)
}

// viewable is open_target's can_view: the formats the viewer shows (a GIF
// goes to the system, which animates it).
func viewable(path []byte) bool {
	switch strings.ToLower(filepath.Ext(string(path))) {
	case ".jpg", ".jpeg", ".png", ".webp", ".heic", ".heif", ".bmp", ".tif", ".tiff":
		return true
	}
	return false
}

// openFile is Action::OpenFile, through the state's one checked route.
func (b *blk) openFile(path []byte) { b.e.S.OpenFileVia(string(path), openPathFn, revealFn) }

// ---- links ----

// externalURL is zf:src/safety.rs external_url: web and mail addresses only.
func externalURL(s string) (string, bool) {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return "", false
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", false
	}
	switch u.Scheme {
	case "http", "https":
		if u.Host == "" {
			return "", false
		}
	case "mailto":
		if u.Opaque == "" && u.Path == "" {
			return "", false
		}
	default:
		return "", false
	}
	return u.String(), true
}

// inviteCode is group_invite_code: the code of a chat.whatsapp.com link.
func inviteCode(s string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Hostname(), "chat.whatsapp.com") {
		return "", false
	}
	segs := splitPath(u.Path)
	if len(segs) == 0 {
		return "", false
	}
	code := segs[0]
	if code == "invite" {
		segs = segs[1:]
		if len(segs) == 0 {
			return "", false
		}
		code = segs[0]
	}
	if len(segs) != 1 || len(code) < 10 || len(code) > 40 {
		return "", false
	}
	for _, c := range code {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return "", false
		}
	}
	return code, true
}

// callLink is call_link: the token of a call.whatsapp.com link and whether
// it is a video call's.
func callLink(s string) (token string, video, ok bool) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Hostname(), "call.whatsapp.com") {
		return "", false, false
	}
	segs := splitPath(u.Path)
	if len(segs) != 2 {
		return "", false, false
	}
	switch segs[0] {
	case "voice", "audio":
	case "video":
		video = true
	default:
		return "", false, false
	}
	token = segs[1]
	if len(token) < 8 || len(token) > 64 {
		return "", false, false
	}
	for _, c := range token {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_') {
			return "", false, false
		}
	}
	return token, video, true
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// openURL is Action::OpenUrl for the block's links.
func (b *blk) openURL(link string) { OpenLink(b.e.S, b.a, link) }

// OpenLink is Action::OpenUrl, for any link in a message (the transcript's
// text too): invites and call links open in the app, web and mail
// addresses in the system's handler, anything else is refused with a toast.
// a may be nil (no account: invites and call links are refused too).
func OpenLink(s *state.State, a *state.Account, link string) {
	if tok, video, ok := callLink(link); ok && a != nil {
		// Through the call hub, as a link opened from the system is: it
		// numbers the call and owns its screen and controls.
		calls.JoinLink(s, a, []byte(tok), video)
		return
	}
	if code, ok := inviteCode(link); ok && a != nil {
		cmd := a.Cmd(abi.CommandTagPreviewInvite)
		cmd.PreviewInvite.Code = []byte(code)
		a.Send(cmd)
		s.OpenDialog(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte(code)})
		return
	}
	if u, ok := externalURL(link); ok {
		failed := i18n.T("Could not open the link: {error}")
		open := opener(s, openURLFn)
		go func() {
			if err := open(u); err != nil {
				s.Toast([]byte(strings.Replace(failed, "{error}", err.Error(), 1)), true)
				s.Wake()
			}
		}()
		return
	}
	s.Toast([]byte(i18n.T("This link type cannot be opened from Zap")), true)
}

// chatOf is the open-chat reference of a JID, 0 when the account has none.
func (b *blk) chatOf(jid []byte) model.ChatRef {
	if b.a == nil {
		return 0
	}
	return b.a.Model.Chats.Find(jid)
}
