package dialogs

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
)

// Poll limits (zf:src/model.rs:928).
const (
	maxQuestion = 255
	maxAnswer   = 100
	maxAnswers  = 12
)

// pollDraft is the poll being written: the question, the answers (two to
// twelve) and whether several may be chosen.
type pollDraft struct {
	question ui.TextBuffer
	options  [maxAnswers]ui.TextBuffer
	n        int
	multiple bool
	chat     []byte
	removeIc icon.ID
	hints    [maxAnswers]string // "Answer 1" …, made once
}

// reset starts a fresh draft, unless a poll is still being sent.
func (p *pollDraft) reset(a *state.Account, d *state.Dialog) {
	if c, ok := a.Model.Chats.Get(d.Chat); ok {
		p.chat = append(p.chat[:0], c.ID()...)
	}
	if a.Model.PollCreating() {
		return
	}
	p.question.Reset()
	for i := range p.options {
		p.options[i].Reset()
	}
	p.n, p.multiple = 2, false
}

// answerGapPt is the space between two answers' fields, in points.
const answerGapPt = 5

// validate is upstream's PollDraft::validated: why the poll cannot be sent
// yet, or "".
func (p *pollDraft) validate() string {
	q := strings.TrimSpace(p.question.String())
	if q == "" || utf8.RuneCountInString(q) > maxQuestion {
		return i18n.T("Enter a question of up to 255 characters.")
	}
	seen := map[string]bool{}
	bad := p.n < 2 || p.n > maxAnswers
	for i := 0; i < p.n && !bad; i++ {
		o := strings.TrimSpace(p.options[i].String())
		bad = o == "" || utf8.RuneCountInString(o) > maxAnswer
	}
	if bad {
		return i18n.T("Add 2–12 answers, each with 1–100 characters.")
	}
	for i := 0; i < p.n; i++ {
		o := strings.TrimSpace(p.options[i].String())
		if seen[o] {
			return i18n.T("Each answer must be different.")
		}
		seen[o] = true
	}
	return ""
}

// remove takes answer i out, moving the later ones up.
func (p *pollDraft) remove(i int) {
	for j := i; j < p.n-1; j++ {
		p.options[j].SetString(p.options[j+1].String())
	}
	p.options[p.n-1].Reset()
	p.n--
}

func (s *Screen) createPoll(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c, p := e.K, e.C(), &s.poll
	creating := a.Model.PollCreating()
	rexui.Label(k.Kit, i18n.T("Question"), rexui.LabelOpts{Style: rexui.TextSemibold, Color: secondary})
	k.BeginDisabled(creating)
	qf := rexui.TextField(k.Kit, i18n.T("Ask a question"), &p.question, i18n.T("Ask a question"), rexui.FieldOpts{})
	if s.age == 2 {
		c.SetFocus(qf.Key)
	}
	rexui.Label(k.Kit, i18n.T("Answers"), rexui.LabelOpts{Style: rexui.TextSemibold, Color: secondary})
	_, h := winPt(e)
	// in a short window the answers scroll rather than push the dialog out
	k.NextHeight(k.Fixed(min(max(h-320, 76), 330, float32(p.n)*(36+answerGapPt)+4)))
	v := k.BeginScroll("poll-answers", &s.scroll, widget.ScrollVertical)
	// the answers apart, each field its own
	k.NextGap(answerGapPt)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "answers", ui.Pct(1), ui.Children(1), 0)
	remove := -1
	for i := 0; i < p.n; i++ {
		k.NextGap(6)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, c.Fmt().Str("answer").Int(i).String(), ui.Pct(1), ui.Children(1), 0)
		k.NextWidth(widget.Fill)
		if p.hints[i] == "" {
			p.hints[i] = i18n.Format(i18n.T("Answer {}"), "{}", strconv.Itoa(i+1))
		}
		hint := p.hints[i]
		rexui.TextField(k.Kit, c.Fmt().Str(hint).Str("###answer").Int(i).String(), &p.options[i], hint, rexui.FieldOpts{})
		if p.n > 2 && rexui.IconButton(k.Kit, p.removeIc, c.Fmt().Str(i18n.T("Remove answer")).Str("###remove").Int(i).String(), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
			remove = i
		}
		k.EndContainer()
	}
	k.EndContainer()
	k.EndScroll(v)
	if remove >= 0 {
		p.remove(remove)
	}
	if p.n < maxAnswers && rexui.Button(k.Kit, i18n.T("Add answer"), rexui.ButtonOpts{}).Clicked {
		p.n++
	}
	rexui.Switch(k.Kit, i18n.T("Allow multiple answers"), &p.multiple, rexui.SwitchOpts{})
	k.EndDisabled()

	problem := p.validate()
	para(k, problem, rexui.TextCaption, secondary)
	connected := a.Model.Link.Connected()
	beginActions(e)
	dl.Action(i18n.T("Cancel"), rexui.ButtonDefault)
	k.BeginDisabled(problem != "" || creating || !connected)
	label := i18n.T("Send poll")
	if creating {
		label = i18n.T("Sending…")
	}
	if dl.Action(label, rexui.ButtonPrimary) {
		s.sendPoll(a)
		s.open = true // the sheet stays until the core has the poll
	}
	k.EndDisabled()
	endActions(e)
}

func (s *Screen) sendPoll(a *state.Account) {
	p := &s.poll
	cmd := a.Cmd(abi.CommandTagCreatePoll)
	cp := &cmd.CreatePoll
	cp.Chat = p.chat
	cp.Draft.Question = []byte(strings.TrimSpace(p.question.String()))
	cp.Draft.Multiple = p.multiple
	for i := 0; i < p.n; i++ {
		cp.Draft.Options = append(cp.Draft.Options, []byte(strings.TrimSpace(p.options[i].String())))
	}
	if a.Send(cmd) == nil {
		a.Model.MarkPollCreating()
	}
}

// ---- Poll results ----

func (s *Screen) pollResults(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c := e.K, e.C()
	m := &s.msg
	if ok, _ := s.loadMessage(a, &e.S.Dialog, m); !ok || m.Content.Tag != abi.ContentTagPoll {
		para(k, i18n.T("This poll is no longer available."), rexui.TextBody, secondary)
		beginActions(e)
		dl.Action(i18n.T("Close"), rexui.ButtonPrimary)
		endActions(e)
		return
	}
	poll := &m.Content.Poll
	rexui.LabelBytes(k.Kit, poll.Question, rexui.LabelOpts{Style: rexui.TextHeadline, Wrap: true})
	if !poll.State.HistoryComplete {
		para(k, i18n.T("Earlier votes may still be missing. Results update as they arrive."), rexui.TextCaption, secondary)
	}
	_, h := winPt(e)
	k.NextHeight(k.Fixed(min(max(h-200, 100), 420)))
	v := k.BeginScroll("poll-results", &s.scroll, widget.ScrollVertical)
	for i, option := range poll.Options {
		count := 0
		if i < len(poll.State.Counts) {
			count = int(poll.State.Counts[i])
		}
		k.NextGap(2)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, c.Fmt().Str("option").Int(i).String(), ui.Pct(1), ui.Children(1), 0)
		k.NextWidth(widget.Fill)
		rexui.LabelBytes(k.Kit, option, rexui.LabelOpts{Style: rexui.TextSemibold})
		rexui.Label(k.Kit, i18n.Format(i18n.N("{count} vote", "{count} votes", count), "{count}", strconv.Itoa(count)), rexui.LabelOpts{Style: rexui.TextCallout, Color: secondary})
		k.EndContainer()
		shown := 0
		for j := range poll.State.Votes {
			vote := &poll.State.Votes[j]
			if !hasChoice(vote.Choices, i) {
				continue
			}
			shown++
			k.NextGap(8)
			k.BeginContainer(ui.AxisX, ui.CrossCenter, c.Fmt().Str("vote").Int(i).Str("-").Int(j).String(), ui.Pct(1), ui.Children(1), 0)
			rexui.Avatar(k.Kit, string(vote.Name), rexui.AvatarOpts{Size: 34, Image: s.avatar(a, vote.ID)})
			k.BeginContainer(ui.AxisY, ui.CrossStart, "who", ui.Children(1), ui.Children(1), 0)
			rexui.LabelBytes(k.Kit, vote.Name, rexui.LabelOpts{})
			s.tbuf = format.AppendMomentStamp(s.tbuf[:0], i18n.Current(), time.Unix(vote.Timestamp, 0), e.S.Now())
			rexui.Label(k.Kit, c.Fmt().Str(model.S(s.tbuf)).String(), rexui.LabelOpts{Style: rexui.TextCallout, Color: secondary})
			k.EndContainer()
			k.EndContainer()
		}
		if shown < count {
			para(k, i18n.T("Participant details are not available yet"), rexui.TextCallout, secondary)
		}
	}
	k.EndScroll(v)
	beginActions(e)
	dl.Action(i18n.T("Close"), rexui.ButtonPrimary)
	endActions(e)
}

func hasChoice(choices []uint64, i int) bool {
	for _, c := range choices {
		if int(c) == i {
			return true
		}
	}
	return false
}

// loadMessage decodes the dialog's message (IDs[0] of its chat) into m. A
// carousel card's id is "<message>-card-<i>": the message is the parent and
// card is i (-1 for a message that is not a card).
func (s *Screen) loadMessage(a *state.Account, d *state.Dialog, m *abi.Message) (ok bool, card int) {
	c, found := a.Model.Chats.Get(d.Chat)
	if !found || d.IDs.Len() == 0 {
		return false, -1
	}
	conv := a.Model.Conversation(c.ID())
	if conv == nil {
		return false, -1
	}
	id := d.IDs.At(0)
	card = -1
	i := conv.Find(id)
	if i < 0 {
		parent, n, isCard := splitCard(id)
		if !isCard {
			return false, -1
		}
		id, card = parent, n
		i = conv.Find(id)
	}
	return i >= 0 && conv.Decode(i, m) == nil, card
}

// splitCard splits a carousel card's message id "<message>-card-<i>".
func splitCard(id []byte) (parent []byte, card int, ok bool) {
	const mark = "-card-"
	i := len(id) - 1
	for i >= 0 && id[i] >= '0' && id[i] <= '9' {
		i--
	}
	digits := len(id) - 1 - i
	if digits == 0 || digits > 4 || i+1 < len(mark) || string(id[i+1-len(mark):i+1]) != mark {
		return nil, -1, false
	}
	for _, d := range id[i+1:] {
		card = card*10 + int(d-'0')
	}
	return id[:i+1-len(mark)], card, true
}
