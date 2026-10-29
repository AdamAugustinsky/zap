package conversation

import (
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/conversation/av"
	"zapfast/ui/conversation/kinds"
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// Albums (MK-028..030, zf:src/ui/conversation.rs album_run,
// album_content): pictures and ordinary videos sent together (the same
// album, sender and day) show as one grid in the first one's bubble; the
// others' rows draw nothing. Each tile is the item's own block, downloads,
// fails and opens on its own, and has its own menu.

const (
	albumCardPt = 320 // CARD_WIDTH
	albumGapPt  = 4
	albumMinPt  = 80
)

type albumEnt struct {
	ok    bool
	id    mkey.K
	gen   uint32
	album uint64
}

// albumOf is a hash of m's album (0: none): a picture's or an ordinary
// video's. It is kept per message and generation, so a frame decodes
// nothing.
func (s *Screen) albumOf(m model.Message) uint64 {
	switch m.Kind() {
	case abi.ContentTagImage, abi.ContentTagVideo:
	default:
		return 0
	}
	if md, ok := m.Media(); !ok || md.VideoNote {
		return 0
	}
	id := mkey.Here(m.ID())
	slot := &s.albums[id.Slot(len(s.albums))]
	if slot.ok && slot.id == id && slot.gen == m.Gen() {
		return slot.album
	}
	al := uint64(0)
	if m.Decode(&s.adec) == nil {
		md := &s.adec.Content.Image.Media
		if m.Kind() == abi.ContentTagVideo {
			md = &s.adec.Content.Video.Media
		}
		if md.HasAlbum && len(md.Album) > 0 {
			al = img.HashBytes(md.Album) | 1
		}
	}
	*slot = albumEnt{ok: true, id: id, gen: m.Gen(), album: al}
	return al
}

// sameAlbum reports whether b belongs to the album a is in.
func (s *Screen) sameAlbum(a, b model.Message) bool {
	al := s.albumOf(a)
	return al != 0 && s.albumOf(b) == al && a.FromMe() == b.FromMe() &&
		sameID(a.Sender(), b.Sender()) && dayOf(a.Time()) == dayOf(b.Time())
}

// albumMember reports whether message i is in an album begun before it:
// the album's first draws it.
func (s *Screen) albumMember(v *model.Conversation, i int) bool {
	return i > 0 && s.sameAlbum(v.At(i-1), v.At(i))
}

// albumLen is how many messages from i make one album (1: no album).
func (s *Screen) albumLen(v *model.Conversation, i int) int {
	n := 1
	for i+n < v.Len() && s.sameAlbum(v.At(i), v.At(i+n)) {
		n++
	}
	return n
}

// albumGrid builds the album of s.albN messages from s.albI in the bubble:
// two columns of square tiles, each its item's block with its own menu,
// the items' captions, Save all… and Download all, and the last item's
// footer.
func (s *Screen) albumGrid(e *screen.Env, a *state.Account, ch model.Chat, v *model.Conversation, out bool, key ui.Key) {
	c := e.C()
	i0, n := s.albI, s.albN
	gap := e.Pt(albumGapPt)
	width := min(e.Pt(albumCardPt), s.maxW)
	tile := theme.Snap(max((width-gap)/2, e.Pt(albumMinPt)))
	rows := (n + 1) / 2
	c.NextSize(ui.Px(2*tile+gap), ui.Px(float32(rows)*tile+float32(rows-1)*gap))
	c.Push(c.BoxKeyed(0, ui.KeyN(key, 200), nil))
	for j := 0; j < n; j++ {
		m := v.At(i0 + j)
		tk := ui.KeyN(key, 201+uint64(j))
		theme.At(c, float32(j%2)*(tile+gap), float32(j/2)*(tile+gap), tile, tile)
		c.Push(c.BoxKeyed(base.Floating|ui.Clip, tk, nil))
		rr := screen.Rect{W: tile, H: tile}
		switch {
		case kinds.Has(m):
			kinds.Build(e, m, rr)
		case av.Has(m):
			av.Build(e, m, rr)
		}
		c.Pop()
		// the item's own menu (MK-030)
		if at, ok := rightAt(e, c.RectOf(tk)); ok && !e.S.Panes.Selecting {
			s.openMenu(m.ID(), at)
		}
	}
	c.Pop()
	for j := 0; j < n; j++ {
		if m := v.At(i0 + j); len(m.Text()) > 0 {
			s.text(e, a, m, s.t.ids[i0+j], ui.KeyN(key, 300+uint64(j)), false)
		}
	}
	s.albumActions(e, a, ch, v, key)
	s.footer(e, a, ch, v.At(i0+n-1), out, key)
}

// albumActions are the album's batch buttons (MK-029): Save all… copies
// the downloaded items to a folder, Download all fetches every missing one
// whatever its size.
func (s *Screen) albumActions(e *screen.Env, a *state.Account, ch model.Chat, v *model.Conversation, key ui.Key) {
	c, k := e.C(), e.K.Kit
	i0, n := s.albI, s.albN
	have, missing, downloading := 0, 0, 0
	for j := 0; j < n; j++ {
		md, _ := v.At(i0 + j).Media()
		switch {
		case len(md.Path) > 0:
			have++
		case md.State == abi.MediaStateTagDownloading:
			missing++
			downloading++
		default:
			missing++
		}
	}
	if have == 0 && missing == 0 {
		return
	}
	c.NextSize(ui.Children(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextGap(e.Pt(8))
	c.NextPadding(0, e.Pt(4), 0, 0)
	c.Push(c.BoxKeyed(0, ui.KeyN(key, 400), nil))
	if have > 0 && rexui.Button(k, i18n.T("Save all…"), rexui.ButtonOpts{Size: theme.SizeSmall}).Clicked {
		cmd := a.Cmd(abi.CommandTagSaveAttachments)
		cmd.SaveAttachments.Files = cmd.SaveAttachments.Files[:0]
		for j := 0; j < n; j++ {
			m := v.At(i0 + j)
			if md, _ := m.Media(); len(md.Path) > 0 {
				cmd.SaveAttachments.Files = append(cmd.SaveAttachments.Files, abi.SaveFile{Source: md.Path, Name: attachmentName(m, md.Path)})
			}
		}
		a.Send(cmd)
	}
	if missing > 0 {
		all := downloading == missing
		label := i18n.T("Download all")
		if all {
			label = i18n.T("Downloading…")
		}
		k.BeginDisabled(all)
		clicked := rexui.Button(k, label, rexui.ButtonOpts{Size: theme.SizeSmall}).Clicked
		k.EndDisabled()
		if clicked && !all {
			for j := 0; j < n; j++ {
				m := v.At(i0 + j)
				if md, _ := m.Media(); len(md.Path) == 0 && md.State != abi.MediaStateTagDownloading {
					cmd := a.Cmd(abi.CommandTagDownload)
					cmd.Download.Manual, cmd.Download.Chat, cmd.Download.Message = true, ch.ID(), m.ID()
					a.Send(cmd)
				}
			}
		}
	}
	c.Pop()
}
