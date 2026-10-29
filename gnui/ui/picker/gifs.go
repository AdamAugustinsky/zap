package picker

import (
	"strings"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/ui/screen"
)

// The GIF tab (zf:src/ui/picker.rs:939): search through the core, trending
// first, and send the chosen one.

type gifItem struct {
	id, still, mp4 string
	w, h           uint32
}

type gifState struct {
	query    ui.TextBuffer
	cmd      fieldCmd
	key      ui.TextBuffer // the GIPHY API key being typed
	keyCmd   fieldCmd
	keyWas   bool // the key field had the focus last frame
	editing  bool // a key is being typed: the prompt stays until it is tried
	sent     string
	results  []gifItem
	pending  bool
	err      string
	badKey   bool
	batch    uint32
	building bool
	scroll   base.ScrollState
	focus    bool
}

func (g *gifState) init() {
	g.query.Max, g.key.Max = 256, 256
	g.query.Reserve(256)
	g.key.Reserve(256)
}

// reset is the tab opening: the search clears, the results stay.
func (g *gifState) reset() {
	g.query.Reset()
	g.focus, g.editing = true, false
}

// apply takes a Gifs event (zf:src/app.rs, Event::Gifs). Results of a
// search that is no longer the one asked for are dropped.
func (g *gifState) apply(e *abi.EventGifs) {
	if string(e.Query) != g.sent {
		return
	}
	if !g.building || e.Batch != g.batch {
		g.batch, g.building = e.Batch, true
		g.results = g.results[:0]
	}
	if e.HasError {
		g.results = g.results[:0]
		g.err, g.badKey = string(e.Error.Message), e.Error.BadKey
	} else {
		g.err, g.badKey = "", false
	}
	for i := range e.Gifs {
		x := &e.Gifs[i]
		it := gifItem{id: string(x.ID), mp4: string(x.Mp4), w: x.Width, h: x.Height}
		if x.HasStill {
			it.still = string(x.Still)
		}
		g.results = append(g.results, it)
	}
	if e.Last {
		g.pending, g.building = false, false
	}
}

// search asks the core for query's GIFs (Action::SearchGifs).
func (s *Screen) searchGifs(e *screen.Env, query string) {
	a := e.Acct()
	if a == nil {
		return
	}
	g := &s.gif
	g.sent, g.pending, g.err, g.badKey = query, true, "", false
	cmd := a.Cmd(abi.CommandTagSearchGifs)
	cmd.SearchGifs.Query = []byte(query)
	cmd.SearchGifs.Key = []byte(strings.TrimSpace(string(e.S.Settings.GiphyKey)))
	a.Send(cmd)
}

// sendGif sends the chosen GIF to the open chat (Action::SendGif).
func (s *Screen) sendGif(e *screen.Env, it *gifItem) {
	a := e.Acct()
	chat, ok := a.OpenChatView()
	if !ok {
		return
	}
	e.S.Toast([]byte(i18n.T("Sending GIF…")), false)
	cmd := a.Cmd(abi.CommandTagSendGif)
	cmd.SendGif.Chat = chat.ID()
	cmd.SendGif.Gif = abi.Gif{ID: []byte(it.id), Mp4: []byte(it.mp4), Width: it.w, Height: it.h}
	if it.still != "" {
		cmd.SendGif.Gif.Still, cmd.SendGif.Gif.HasStill = []byte(it.still), true
	}
	s.quote(e, &cmd.SendGif.Quoting, &cmd.SendGif.HasQuoting)
	a.Send(cmd)
	s.afterSend(e)
}

// gifBody builds the GIF tab.
func (s *Screen) gifBody(e *screen.Env) {
	k := e.K
	c := k.Context()
	g := &s.gif
	own := strings.TrimSpace(string(e.S.Settings.GiphyKey))
	if own == "" || g.badKey || g.editing {
		s.giphyKeyPrompt(e, own)
		return
	}
	g.cmd = fieldCmd{}
	c.NextFieldCommander(&g.cmd, "")
	search := i18n.T("Search GIFs via GIPHY")
	fs := rexui.SearchField(k.Kit, search, &g.query, search, rexui.FieldOpts{})
	if g.focus {
		k.Focus(fs.Key)
		g.focus = false
	}
	if g.cmd.esc {
		s.wantClose = true
	}
	if q := strings.TrimSpace(string(g.query.Bytes())); g.cmd.enter && q != strings.TrimSpace(g.sent) {
		s.searchGifs(e, string(g.query.Bytes()))
	}
	switch {
	case g.pending:
		c.NextAxis(ui.AxisX)
		c.NextGap(e.Pt(8))
		c.NextCross(ui.CrossCenter)
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.Push(c.Box(0, "searching"))
		rexui.Spinner(k.Kit, i18n.T("Searching"), rexui.SpinnerOpts{Size: theme.SizeSmall})
		rexui.Label(k.Kit, i18n.T("Searching…"), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
		c.Pop()
	case g.err != "":
		rexui.LabelBytes(k.Kit, c.Fmt().Str(g.err).Bytes(), rexui.LabelOpts{Wrap: true, Color: rexui.DangerText})
	}

	// three columns of tiles, each as tall as its picture's shape allows
	const columns = 3
	gap := e.Pt(6)
	v := k.BeginScroll("gif grid", &g.scroll, base.ScrollVertical)
	tileW := theme.Snap((s.viewWidth(e, &v) - gap*(columns-1)) / columns)
	c.NextGap(gap)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.Push(c.Box(0, "tiles"))
	for i := 0; i < len(g.results); i += columns {
		c.NextAxis(ui.AxisX)
		c.NextGap(gap)
		c.NextCross(ui.CrossStart)
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.Push(c.BoxKeyed(0, ui.KeyN(c.ParentKey(), uint64(i)+1), nil))
		for j := i; j < min(i+columns, len(g.results)); j++ {
			it := &g.results[j]
			ratio := min(max(float32(max(it.h, 1))/float32(max(it.w, 1)), 0.5), 1.4)
			th := theme.Snap(min(tileW*ratio, e.Pt(150)))
			if s.gifTile(e, it, tileW, th) {
				s.sendGif(e, it)
			}
		}
		c.Pop()
	}
	if len(g.results) == 0 && !g.pending && g.err == "" {
		c.NextSize(ui.Pct(1), ui.Px(e.Pt(60)))
		c.NextAxis(ui.AxisY)
		c.NextJustify(ui.JustifyCenter)
		c.NextCross(ui.CrossCenter)
		c.Push(c.Box(0, "empty"))
		rexui.Label(k.Kit, i18n.T("Search for a GIF or browse trending results."), rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
		c.Pop()
	}
	c.Pop()
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k.Kit, &v)
	k.CloseScroll(&v)
}

// gifTile is one result: its still, outlined under the pointer. It reports
// a click.
func (s *Screen) gifTile(e *screen.Env, it *gifItem, w, h float32) bool {
	k := e.K
	c, r := k.Context(), k.Theme()
	key := ui.HashString(c.ParentKey(), it.id)
	hot := c.HotTOf(key)
	flags := ui.Clickable | ui.Hoverable | ui.HotAnimation
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextRadius(e.Pt(6))
	c.NextRole(ui.RoleButton)
	c.NextSemName(it.id)
	if hot > 0.004 {
		c.NextBorder(theme.WithAlpha(r.Colors[theme.Accent], hot), e.Pt(2))
		flags |= ui.DrawBorder
	}
	id := c.BoxKeyed(flags, key, nil)
	c.Push(id)
	s.picture(e, 1, it.still, w, h, ui.FitFill, e.Pt(6), false, false)
	c.Pop()
	return c.Signal(id).Clicked
}

// giphyKeyPrompt asks for a GIPHY API key (no key, or one GIPHY refused) and
// saves it to the settings.
func (s *Screen) giphyKeyPrompt(e *screen.Env, own string) {
	k := e.K
	c, r := k.Context(), k.Theme()
	g := &s.gif
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(8))
	c.NextPadding(0, e.Pt(8), 0, 0)
	c.Push(c.Box(0, "api key"))
	if g.badKey && g.err != "" {
		rexui.LabelBytes(k.Kit, c.Fmt().Str(g.err).Bytes(), rexui.LabelOpts{Wrap: true, Color: rexui.DangerText})
	}
	text := "GIF search needs a GIPHY API key. Create a free key at developers.giphy.com and paste it here. It is saved in your settings."
	if g.badKey {
		text = "This GIPHY API key was rejected. Create a free key at developers.giphy.com and paste it here. It is saved in your settings."
	}
	rexui.Label(k.Kit, i18n.T(text), rexui.LabelOpts{Wrap: true})
	if rexui.Link(k.Kit, "developers.giphy.com", rexui.LinkOpts{}).Clicked {
		s.openURL(e, "https://developers.giphy.com/")
	}
	if g.key.Len() == 0 && own != "" {
		g.key.SetString(own)
	}
	g.keyCmd = fieldCmd{}
	c.NextFieldCommander(&g.keyCmd, "")
	field := rexui.TextField(k.Kit, i18n.T("GIPHY API key"), &g.key, i18n.T("GIPHY API key"), rexui.FieldOpts{})
	if g.focus {
		k.Focus(field.Key)
		g.focus = false
	}
	if field.Changed {
		g.editing = true
		e.S.Settings.GiphyKey = append(e.S.Settings.GiphyKey[:0], g.key.Bytes()...)
		e.S.Touch()
	}
	// leaving the field with a key in it tries the key
	was := g.keyWas
	g.keyWas = field.Focused
	if (g.keyCmd.enter || (was && !field.Focused)) && strings.TrimSpace(g.key.String()) != "" {
		g.badKey, g.editing = false, false
		s.searchGifs(e, "")
	}
	if g.keyCmd.esc {
		s.wantClose = true
	}
	c.Pop()
	_ = r
}
