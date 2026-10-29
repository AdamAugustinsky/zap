package kinds

import (
	"slices"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// Polls (zf:src/ui/polls.rs ballot and results_button, app.rs VotePoll and
// RefreshPoll).

// ---- what is in flight ----

// idSet is a small set of (account, chat, message) hashes.
type idSet struct {
	keys [64]uint64
	n    int
}

func (s *idSet) has(k uint64) bool {
	for i := 0; i < s.n; i++ {
		if s.keys[i] == k {
			return true
		}
	}
	return false
}

func (s *idSet) add(k uint64) {
	if s.has(k) {
		return
	}
	if s.n == len(s.keys) {
		copy(s.keys[:], s.keys[1:])
		s.n--
	}
	s.keys[s.n] = k
	s.n++
}

func (s *idSet) remove(k uint64) {
	for i := 0; i < s.n; i++ {
		if s.keys[i] == k {
			s.keys[i] = s.keys[s.n-1]
			s.n--
			return
		}
	}
}

func flightKey(acct uint64, chat, msg []byte) uint64 {
	h := idHash(chat)*1099511628211 ^ idHash(msg)
	return h ^ acct*0x9e3779b97f4a7c15
}

// local is what the blocks keep for one state: the polls whose vote is on
// its way (poll_voting), the interactive messages whose reply is
// (interactive_pending), the polls a refresh was asked for in their current
// state, and the downloads asked for.
type local struct {
	voting, replying idSet
	refreshed        [64]struct {
		k   uint64
		gen uint32
	}
	requested [64]struct {
		id   mkey.K
		card int
		gen  uint32
	}
}

type tracker struct{ l *local }

func (t tracker) Event(a *state.Account, e *abi.Event) {
	switch e.Tag {
	case abi.EventTagPollVoted:
		t.l.voting.remove(flightKey(a.Serial, e.PollVoted.Chat, e.PollVoted.Message))
	case abi.EventTagInteractiveReplyState:
		k := flightKey(a.Serial, e.InteractiveReplyState.Chat, e.InteractiveReplyState.Message)
		if e.InteractiveReplyState.Pending {
			t.l.replying.add(k)
		} else {
			t.l.replying.remove(k)
		}
	}
}

var (
	locals = map[*state.State]*local{}
	lastS  *state.State
	lastL  *local
)

// loc is the state's local, made (with its listener) on first use.
func (b *blk) loc() *local {
	s := b.e.S
	if s == lastS {
		return lastL
	}
	l := locals[s]
	if l == nil {
		l = new(local)
		locals[s] = l
		s.Listen(tracker{l}, abi.EventTagPollVoted, abi.EventTagInteractiveReplyState)
	}
	lastS, lastL = s, l
	return l
}

// ---- layout ----

type pollGeom struct {
	w, hint, detail, cant, results float32
	opts                           [64]float32 // row heights; polls have at most 12 answers
	labelW                         float32
	n                              int
	detailText, cantText           []byte
	selectOne                      bool
}

const (
	pollRowPad   = 34.0 // grid:ok the marker column, an offset
	pollLabelX   = 25.0
	pollLabelPad = 58.0 // grid:ok the marker and count columns, an offset
	pollResultsH = 39.0
)

func (b *blk) pollDetail(ps *abi.PollState, pending bool) []byte {
	switch {
	case pending:
		return base.StrBytes(i18n.T("Sending vote…"))
	case ps.RefreshFailed:
		return base.StrBytes(i18n.T("Waiting for your phone · earlier votes may be missing"))
	case ps.Refreshing && !ps.HistoryComplete:
		return base.StrBytes(i18n.T("Loading earlier votes from your phone…"))
	case !ps.HistoryComplete:
		return base.StrBytes(i18n.T("Earlier votes have not been loaded yet"))
	}
	f := b.c.Fmt().Uint64(ps.Voters)
	if ps.Voters == 1 {
		return f.Str(" voter").Bytes()
	}
	return f.Str(" voters").Bytes()
}

func (b *blk) pollGeom(maxW float32) (g pollGeom) {
	d := b.decoded()
	p := &d.Content.Poll
	ps := &p.State
	g.w = b.cardW(maxW)
	g.selectOne = ps.Selectable == 1
	g.hint = b.lineH(fSub) + b.pt(4)
	g.labelW = max(g.w-b.pt(pollLabelPad), b.pt(50))
	g.n = min(len(p.Options), len(g.opts))
	for i := 0; i < g.n; i++ {
		_, lh, _ := b.wrapSize(p.Options[i], fBody, g.labelW, 3)
		g.opts[i] = max(lh, b.pt(18)) + b.pt(pollRowPad)
	}
	pending := b.a != nil && b.loc().voting.has(flightKey(b.a.Serial, d.Chat, b.m.ID()))
	g.detailText = b.pollDetail(ps, pending)
	_, g.detail, _ = b.wrapSize(g.detailText, fSub, g.w, 0)
	connected := b.variant()&1 != 0
	switch {
	case !ps.CanVote:
		g.cantText = base.StrBytes(i18n.T("Voting key unavailable · use your phone"))
	case !connected:
		g.cantText = base.StrBytes(i18n.T("Reconnect to vote"))
	}
	_, g.cant, _ = b.wrapSize(g.cantText, fSub, g.w, 0)
	g.results = b.pt(pollResultsH)
	return g
}

func (g *pollGeom) height(b *blk) float32 {
	h := g.hint
	for i := 0; i < g.n; i++ {
		h += g.opts[i]
	}
	h += g.detail + g.cant + b.pt(4) + g.results
	return h
}

func (b *blk) pollSize(maxW float32) (w, h float32) {
	g := b.pollGeom(maxW)
	return g.w, g.height(b)
}

// selectionAfterClick is the vote a click on option index makes, nil when the
// poll allows no more answers.
func selectionAfterClick(ps *abi.PollState, index uint64, dst []uint64) ([]uint64, bool) {
	dst = append(dst[:0], ps.Selected...)
	if i := slices.Index(dst, index); i >= 0 {
		dst = slices.Delete(dst, i, i+1)
	} else {
		if ps.Selectable == 1 {
			dst = dst[:0]
		}
		if ps.Selectable > 0 && uint64(len(dst)) >= ps.Selectable {
			return dst, false
		}
		dst = append(dst, index)
	}
	slices.Sort(dst)
	return dst, true
}

func (b *blk) pollBuild(r screen.Rect) {
	d := b.decoded()
	p := &d.Content.Poll
	ps := &p.State
	g := b.pollGeom(r.W)
	w := r.W
	connected := b.variant()&1 != 0
	pk := uint64(0)
	pending := false
	if b.a != nil {
		pk = flightKey(b.a.Serial, d.Chat, b.m.ID())
		pending = b.loc().voting.has(pk)
	}
	enabled := connected
	active := enabled && ps.CanVote && !pending

	hint := i18n.T("Select answers")
	if g.selectOne {
		hint = i18n.T("Select one answer")
	}
	b.label(hint, fSub, b.dim(), 0, 0, w)
	y := g.hint
	for i := 0; i < g.n; i++ {
		rowH := g.opts[i]
		selected := slices.Contains(ps.Selected, uint64(i))
		b.pollOption(p, ps, i, selected, active, y, w, rowH, g.labelW, d)
		y += rowH
	}
	if g.detail > 0 {
		b.wrapped(40, g.detailText, fSub, b.faint(), 0, y, w, g.detail, 0)
	}
	y += g.detail
	if g.cant > 0 {
		b.wrapped(41, g.cantText, fSub, b.faint(), 0, y, w, g.cant, 0)
		y += g.cant
	}
	y += b.pt(4)
	b.showVotes(y, w, g.results, ps.Voters > 0)

	// a poll that needs its votes asks for them when it is on screen
	if enabled && ps.RefreshNeeded && !ps.Refreshing && b.a != nil {
		slot := &b.loc().refreshed[pk%64]
		if slot.k != pk || slot.gen != b.m.Gen() {
			slot.k, slot.gen = pk, b.m.Gen()
			cmd := b.a.Cmd(abi.CommandTagRefreshPoll)
			cmd.RefreshPoll.Chat, cmd.RefreshPoll.Message = d.Chat, b.m.ID()
			b.a.Send(cmd)
		}
	}
}

var voteBuf []uint64

// pollOption is one answer: its marker, its text, its count and its bar.
func (b *blk) pollOption(p *abi.ContentPoll, ps *abi.PollState, i int, selected, active bool, y, w, h, labelW float32, d *abi.Message) {
	k, c := b.k, b.c
	n := uint64(100 + i*8)
	opt := p.Options[i]
	radius := b.pt(ztheme.Nested())
	name := b.c.Fmt().Str(model.S(opt)).Str("###opt").Int(i).String()
	c.NextPos(0, y)
	c.NextFlags(base.Floating)
	pr := k.PressState(name, base.HoverOn)
	st := ui.SemState(0)
	if selected {
		st = ui.SemChecked
	}
	c.NextSemState(st)
	k.BeginDisabled(!active)
	role := ui.RoleCheckbox
	if ps.Selectable == 1 {
		role = ui.RoleRadio
	}
	k.BeginPress(&pr, role, ui.Px(w), ui.Px(h), 0, nil)
	if active && pr.Hot > 0.01 {
		b.fillBox(n, 0, 0, w, h, radius, theme.WithAlpha(b.hoverFill(false), pr.Hot))
	}
	// the marker
	m := b.pt(16)
	mx, my := b.pt(2), b.pt(8)
	if selected {
		b.fillBox(n+1, mx, my, m, m, m/2, b.accent())
		b.icon(icVoteDone, b.col(theme.OnAccent), mx, my, m, m) // on the accent disc
	} else {
		b.ring(n+1, mx, my, m, m, m/2, b.dim(), b.pt(1.5))
	}
	// the text, the count and the bar
	b.wrapped(n+2, opt, fBody, b.ink(), b.pt(pollLabelX), b.pt(6), labelW, h-b.pt(pollRowPad)+0, 3)
	var count uint64
	if i < len(ps.Counts) {
		count = ps.Counts[i]
	}
	cb := c.Fmt().Uint64(count).Bytes()
	cw := b.k.TextWidthBytes(b.font(fCallout), cb)
	b.labelB(cb, fCallout, b.dim(), w-b.pt(5)-cw-b.pt(2), b.pt(8), cw+b.pt(2))
	tx := b.pt(pollLabelX)
	tw := max(w-b.pt(30), 0)
	ty := h - b.pt(12)
	th := b.pt(6)
	b.fillBox(n+3, tx, ty, tw, th, th/2, theme.WithAlpha(b.ink(), 0.12))
	if ps.Voters > 0 && count > 0 {
		frac := min(float32(count)/float32(ps.Voters), 1)
		col := b.dim()
		if selected {
			col = b.accent()
		}
		b.fillBox(n+4, tx, ty, max(tw*frac, th), th, th/2, col)
	}
	sig := b.endHit(&pr, radius)
	k.EndDisabled()
	if active && sig.Clicked && b.a != nil {
		if sel, ok := selectionAfterClick(ps, uint64(i), voteBuf); ok {
			voteBuf = sel
			cmd := b.a.Cmd(abi.CommandTagVotePoll)
			cmd.VotePoll.Chat, cmd.VotePoll.Message = d.Chat, b.m.ID()
			cmd.VotePoll.Choices = append(cmd.VotePoll.Choices[:0], sel...)
			if b.a.Send(cmd) == nil {
				b.loc().voting.add(flightKey(b.a.Serial, d.Chat, b.m.ID()))
			}
		}
	}
}

// showVotes is the row under the poll that opens its results; the time
// stays above it, like interactive cards.
func (b *blk) showVotes(y, w, h float32, enabled bool) {
	k := b.k
	b.fillBox(60, 0, y, w, b.pt(1), 0, b.hairline())
	b.c.NextPos(0, y)
	b.c.NextFlags(base.Floating)
	k.BeginDisabled(!enabled)
	pr := k.PressState(i18n.T("Show votes"), base.HoverOn)
	k.BeginPress(&pr, ui.RoleButton, ui.Px(w), ui.Px(h), 0, nil)
	if enabled && pr.Hot > 0.01 {
		b.fillBox(61, 0, 0, w, h, b.pt(10), theme.WithAlpha(b.hoverFill(false), pr.Hot))
	}
	col := b.faint()
	if enabled {
		col = b.link()
	}
	label := i18n.T("Show votes")
	lw := b.widthS(fBold, label)
	b.label(label, fBold, col, (w-lw)/2, (h-b.lineH(fBold))/2, lw+b.pt(2))
	sig := b.endHit(&pr, b.pt(10))
	k.EndDisabled()
	if enabled && sig.Clicked && b.a != nil {
		b.e.S.OpenDialog(b.msgDialog(state.DialogPollResults))
	}
}
