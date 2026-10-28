package settings

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// fmtBytes appends a size as zf:src/util.rs bytes writes it: "12 B",
// "1.2 MB".
func fmtBytes(f *ui.Fmt, n uint64) *ui.Fmt {
	units := [...]string{"B", "KB", "MB", "GB"}
	v, u := float64(n), 0
	for v >= 1000 && u < len(units)-1 {
		v /= 1024
		u++
	}
	if u == 0 {
		return f.Uint64(n).Str(" B")
	}
	return f.Float(v, 1).Str(" ").Str(units[u])
}

// storageBlock is the space the account's media cache takes, split by kind,
// with the button that empties it (SE-071).
func (s *Screen) storageBlock(e *screen.Env) {
	k := e.K.Kit
	c := e.C()
	a := e.Acct()
	in := s.info(a)
	st := &in.storage
	total := st.ImageBytes + st.VideoBytes + st.StickerBytes + st.OtherBytes

	k.BeginContainer(ui.AxisX, ui.CrossCenter, "storage head", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k, s.tx.storageUsed, rexui.LabelOpts{Style: rexui.TextSemibold})
	k.Spacer()
	if in.measured { // a reading that never arrived shows no size rather than zero
		bytesIn(e, 1, ui.Children(1), fmtBytes(c.Fmt(), total).Bytes(), rexui.LabelOpts{Style: rexui.TextSemibold})
	}
	k.BeginDisabled(!in.measured || total == 0)
	if button(e, s.tx.clearMedia) {
		openDialog(e, state.Dialog{Kind: state.DialogConfirmClearMediaCache, Account: a.Index})
	}
	k.EndDisabled()
	k.EndContainer()

	// each number has a cache of its own: say whose this is
	rexui.Label(k, s.storageHint(a, in), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2), Wrap: true})
	if !in.measured {
		return
	}
	rows := [...]struct {
		title        string
		count, bytes uint64
		always       bool
	}{
		{s.tx.images, st.Images, st.ImageBytes, true},
		{s.tx.videos, st.Videos, st.VideoBytes, true},
		{s.tx.stickersName, st.Stickers, st.StickerBytes, true},
		{s.tx.other, st.Other, st.OtherBytes, false},
	}
	for i, r := range rows {
		if !r.always && r.count == 0 {
			continue
		}
		k.NextGap(2)
		k.BeginContainer(ui.AxisY, ui.CrossStart, r.title, ui.Pct(1), ui.Children(1), 0)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "head", ui.Pct(1), ui.Children(1), 0)
		rexui.Label(k, r.title, rexui.LabelOpts{})
		k.Spacer()
		bytesIn(e, uint64(10+i), ui.Children(1), fmtBytes(c.Fmt().Uint64(r.count).Str(" · "), r.bytes).Bytes(),
			rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
		k.EndContainer()
		frac := float32(0)
		if total > 0 {
			frac = float32(float64(r.bytes) / float64(total))
		}
		rexui.ProgressBar(k, r.title, frac, rexui.ProgressOpts{})
		k.EndContainer()
	}
}

// storageHint says what the cache holds and whose it is, rebuilt when the
// language or the account's name changes.
func (s *Screen) storageHint(a *state.Account, in *acctInfo) string {
	me := &a.Model.Me
	if in.hint == "" || in.hintGen != me.Gen() || in.hintLoc != i18n.Current() {
		who := string(me.Name())
		if who == "" {
			who = string(in.phone)
		}
		if who == "" {
			who = a.ID
		}
		in.hint = i18n.Format(i18n.T("Photos, videos, stickers and files {account} downloaded to this computer. Clearing them keeps your messages; attachments download again when you open them."), "{account}", who)
		in.hintGen, in.hintLoc = me.Gen(), i18n.Current()
	}
	return in.hint
}
