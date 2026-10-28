package composer

import (
	"bytes"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"

	"zapfast/i18n"
	"zapfast/mentions"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The completions of the composer (zf:src/ui/conversation.rs:915-1323):
// group members after "@", emoji after ":". Each is decided from the text's
// changes and the caret (bytes), shown by rexui.Composer at the caret, and
// accepted with one Replace, one undo step.

type compKind uint8

const (
	compNone compKind = iota
	compMention
	compEmoji
)

// emojiLimit is how many emoji the list offers (zf:src/ui/conversation.rs:1010).
const emojiLimit = 6

// mentionCand is a member the mention list offers.
type mentionCand struct {
	id, name, detail model.Str // in completion.arena
	all              bool      // @all
}

// completion is the open list: its trigger, the query's candidates and the
// list's state.
type completion struct {
	kind  compKind
	start int // the trigger's byte
	end   int // the caret: the query is text[start+1:end]
	list  rexui.CompletionState
	items []rexui.CompletionItem

	query   []byte // the query of the items, lowercased
	gen     uint64 // the model's Gen they were built at
	emoji   []int  // the emoji rows: indexes into emojiTable
	members []mentionCand
	named   []mentionCand
	numbers []mentionCand
	arena   []byte // the members' ids, names and details
	digits  []byte
	low     []byte
	low2    []byte
	scores  []emojiScore
}

type emojiScore struct{ score, order int }

func (c *completion) open() bool { return c.kind != compNone }

// close ends the completion.
func (c *completion) close(s *Screen) {
	c.kind = compNone
	c.items = c.items[:0]
}

// isAlnum is Rust's char::is_alphanumeric.
func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Other_Alphabetic, r)
}

// standaloneTrigger is the byte of a trigger typed just before the caret,
// on its own: at the start or after a character that is not a letter or a
// digit, so the colons in times and links and the @ in addresses stay text
// (zf:src/ui/conversation.rs:918).
func standaloneTrigger(text []byte, caret int, trigger byte) (int, bool) {
	if caret <= 0 || caret > len(text) || text[caret-1] != trigger {
		return 0, false
	}
	at := caret - 1
	if at == 0 {
		return at, true
	}
	r, _ := utf8.DecodeLastRune(text[:at])
	return at, !isAlnum(r)
}

// activeMention is the query from the "@" at start to the caret, ok while it
// is one (zf:src/ui/conversation.rs:936).
func activeMention(text []byte, start, caret int) ([]byte, bool) {
	if start < 0 || start >= len(text) || text[start] != '@' || caret < start+1 || caret > len(text) {
		return nil, false
	}
	q := text[start+1 : caret]
	if bytes.ContainsAny(q, "@\n") {
		return nil, false
	}
	return q, true
}

// activeEmoji is the query from the ":" at start to the caret: letters,
// digits, _, - and + (zf:src/ui/conversation.rs:948).
func activeEmoji(text []byte, start, caret int) ([]byte, bool) {
	if start < 0 || start >= len(text) || text[start] != ':' || caret < start+1 || caret > len(text) {
		return nil, false
	}
	q := text[start+1 : caret]
	for i := 0; i < len(q); {
		r, w := utf8.DecodeRune(q[i:])
		if !isAlnum(r) && r != '_' && r != '-' && r != '+' {
			return nil, false
		}
		i += w
	}
	return q, true
}

// caret is the editor's caret, in bytes.
func (s *Screen) caret() int { return s.st.Selection().Focus }

// textChanged opens a completion for a trigger just typed, and refines or
// closes the open one.
func (c *completion) textChanged(s *Screen, e *screen.Env, a *state.Account, ch model.Chat, typed bool) {
	text, caret := s.st.Text(), s.caret()
	if typed {
		if at, ok := standaloneTrigger(text, caret, ':'); ok {
			e.S.Panes.Picker = state.PickerNone
			c.kind, c.start = compEmoji, at
			c.query = c.query[:0]
			c.items = c.items[:0]
			c.refresh(s, e, a, ch, true)
			return
		}
		if at, ok := standaloneTrigger(text, caret, '@'); ok && ch.Group() && s.rowOK && len(s.row.Participants) > 0 {
			c.kind, c.start = compMention, at
			c.query = c.query[:0]
			c.items = c.items[:0]
			c.refresh(s, e, a, ch, true)
			return
		}
	}
	c.caretMoved(s, e, a, ch)
}

// caretMoved closes the completion when the caret left its query, and
// refines it otherwise.
func (c *completion) caretMoved(s *Screen, e *screen.Env, a *state.Account, ch model.Chat) {
	if !c.open() {
		return
	}
	c.refresh(s, e, a, ch, false)
}

// refresh rebuilds the candidates for the query at the caret; force
// rebuilds them even when the query is the same.
func (c *completion) refresh(s *Screen, e *screen.Env, a *state.Account, ch model.Chat, force bool) {
	text, caret := s.st.Text(), s.caret()
	var q []byte
	var ok bool
	if sel := s.st.Selection(); sel.Anchor != sel.Focus {
		ok = false
	} else if c.kind == compEmoji {
		q, ok = activeEmoji(text, c.start, caret)
	} else {
		q, ok = activeMention(text, c.start, caret)
	}
	if !ok {
		c.close(s)
		return
	}
	c.end = caret
	c.low = lowerInto(c.low[:0], q)
	if !force && bytes.Equal(c.low, c.query) && c.gen == a.Model.Gen() {
		return
	}
	c.query = lowerInto(c.query[:0], q)
	c.gen = a.Model.Gen()
	if c.kind == compEmoji {
		c.emojiCandidates(e.S)
	} else {
		c.mentionCandidates(s, a, ch, q)
	}
	// The first row is highlighted again whenever the candidates change.
	c.list.Reset(len(c.items), true)
}

// lowerInto appends b lowercased to dst.
func lowerInto(dst, b []byte) []byte {
	for i := 0; i < len(b); {
		r, w := utf8.DecodeRune(b[i:])
		if r < utf8.RuneSelf {
			if 'A' <= r && r <= 'Z' {
				r += 'a' - 'A'
			}
			dst = append(dst, byte(r))
		} else {
			dst = utf8.AppendRune(dst, unicode.ToLower(r))
		}
		i += w
	}
	return dst
}

// emojiCandidates are the emoji for the query: the recent ones, then the
// table's, when it is empty; else the best matches (zf:src/ui/conversation.rs:1009).
func (c *completion) emojiCandidates(st *state.State) {
	c.emoji = c.emoji[:0]
	q := c.query
	if len(q) == 0 {
		for _, r := range st.Settings.RecentEmoji {
			if len(c.emoji) == emojiLimit {
				break
			}
			if i := emojiIndex(r); i >= 0 && !slices.Contains(c.emoji, i) {
				c.emoji = append(c.emoji, i)
			}
		}
		table := emojiTable()
		for i := 0; i < len(table) && len(c.emoji) < emojiLimit; i++ {
			if !slices.Contains(c.emoji, i) {
				c.emoji = append(c.emoji, i)
			}
		}
	} else {
		c.scores = c.scores[:0]
		table := emojiTable()
		for i := range table {
			if sc, ok := table[i].score(model.S(q)); ok {
				c.scores = append(c.scores, emojiScore{sc, i})
			}
		}
		slices.SortFunc(c.scores, func(x, y emojiScore) int {
			if x.score != y.score {
				return x.score - y.score
			}
			return x.order - y.order
		})
		for _, sc := range c.scores {
			if len(c.emoji) == emojiLimit {
				break
			}
			c.emoji = append(c.emoji, sc.order)
		}
	}
	c.items = c.items[:0]
	for _, i := range c.emoji {
		r := &emojiTable()[i]
		c.items = append(c.items, rexui.CompletionItem{Title: r.row, Detail: r.name})
	}
}

// mentionCandidates are the group's members whose name holds the query, or
// whose number holds its digits, and "all" first where we may mention
// everyone (zf:src/app.rs:2685).
func (c *completion) mentionCandidates(s *Screen, a *state.Account, ch model.Chat, raw []byte) {
	c.members, c.named, c.numbers = c.members[:0], c.named[:0], c.numbers[:0]
	c.arena = c.arena[:0]
	me := a.Model.Me.ID()
	needle := bytes.TrimSpace(c.query)
	c.digits = c.digits[:0]
	for _, b := range raw {
		if '0' <= b && b <= '9' {
			c.digits = append(c.digits, b)
		}
	}
	for _, id := range s.row.Participants {
		if len(id) == 0 || bytes.Equal(id, me) {
			continue
		}
		name := a.Model.DisplayName(id)
		number := len(name) == 0 || name[0] == '+' || string(name) == "Unknown"
		phone := model.Phone(id)
		m := mentionCand{id: c.putArena(id)}
		if len(name) > 0 {
			m.name = c.putArena(name)
		} else if len(phone) > 0 {
			m.name = c.putArena2('+', phone)
		} else {
			m.name = m.id
		}
		if len(needle) > 0 {
			c.low = lowerInto(c.low[:0], bytes.TrimLeft(c.at(m.name), "~"))
			user, _, _ := bytes.Cut(id, []byte("@"))
			if !bytes.Contains(c.low, needle) && (len(c.digits) == 0 || !bytes.Contains(user, c.digits)) {
				c.arena = c.arena[:m.id.Off]
				continue
			}
		}
		if len(phone) > 0 {
			m.detail = c.putArena2('+', phone)
		}
		if number {
			c.numbers = append(c.numbers, m)
		} else {
			c.named = append(c.named, m)
		}
	}
	slices.SortStableFunc(c.named, func(x, y mentionCand) int {
		c.low = lowerInto(c.low[:0], bytes.TrimLeft(c.at(x.name), "~"))
		c.low2 = lowerInto(c.low2[:0], bytes.TrimLeft(c.at(y.name), "~"))
		return bytes.Compare(c.low, c.low2)
	})
	slices.SortStableFunc(c.numbers, func(x, y mentionCand) int { return bytes.Compare(c.at(x.name), c.at(y.name)) })
	alias := mentions.EveryoneAlias(i18n.Current())
	if s.canMentionEveryone(ch) && mentions.EveryoneQueryMatches(model.S(raw), alias) {
		m := mentionCand{id: c.putArena([]byte(mentions.AllID)), name: c.putArena([]byte(mentions.AllUser)), all: true}
		if !strings.EqualFold(alias, mentions.AllUser) {
			m.detail = c.putArena([]byte(alias))
		}
		c.members = append(c.members, m)
	}
	c.members = append(c.members, c.named...)
	c.members = append(c.members, c.numbers...)
	// The rows' strings view the arena, which no longer grows.
	c.items = c.items[:0]
	for _, m := range c.members {
		c.items = append(c.items, rexui.CompletionItem{Title: model.S(c.at(m.name)), Detail: model.S(c.at(m.detail))})
	}
}

func (c *completion) putArena2(b byte, rest []byte) model.Str {
	off := len(c.arena)
	c.arena = append(append(c.arena, b), rest...)
	return model.Str{Off: uint32(off), Len: uint32(len(rest) + 1)}
}

func (c *completion) putArena(b []byte) model.Str {
	off := len(c.arena)
	c.arena = append(c.arena, b...)
	return model.Str{Off: uint32(off), Len: uint32(len(b))}
}

func (c *completion) at(s model.Str) []byte { return c.arena[s.Off : s.Off+s.Len] }

// canMentionEveryone is upstream's Chat::can_mention_everyone
// (zf:src/model.rs:410).
func (s *Screen) canMentionEveryone(ch model.Chat) bool {
	return ch.Group() && canSend(&ch) && s.rowOK && len(s.row.Participants) > 0 &&
		(len(s.row.Participants) <= mentions.OpenLimit || ch.Admin())
}

// completionSignals applies what the list did: a row accepted (a key or a
// click) or the list cancelled.
func (s *Screen) completionSignals(e *screen.Env, a *state.Account, ch model.Chat, sig *rexui.ComposerSignal) {
	c := &s.comp
	if !c.open() {
		return
	}
	row := -1
	switch {
	case sig.Chosen >= 0:
		row = sig.Chosen
	case sig.Completion == rexui.ResultAccept:
		row = c.list.Sel
	case a.Mode == state.ListStarred && !sig.Dismissed && (sig.Completion == rexui.ResultCancel || sig.Cancelled):
		// Starred comes before the suggestions in Escape's order
		// (zf:src/ui/keys.rs:164): the list waits for the next Escape.
		a.SetMode(state.ListChats)
		e.S.Touch()
		return
	case sig.Completion == rexui.ResultCancel, sig.Dismissed, sig.Cancelled:
		c.close(s)
		return
	}
	if row < 0 || row >= len(c.items) {
		return
	}
	if c.kind == compEmoji {
		s.acceptEmoji(e, row)
	} else {
		s.acceptMention(e, a, ch, row)
	}
	c.close(s)
}

// acceptEmoji puts the emoji in place of ":query" (zf:src/app.rs:7243).
func (s *Screen) acceptEmoji(e *screen.Env, row int) {
	c := &s.comp
	em := emojiTable()[c.emoji[row]].emoji
	r := platform.TextRange{Start: c.start, End: c.end}
	at := c.start + len(em)
	if err := s.st.Replace(r, []byte(em), platform.TextRange{Start: at, End: at}); err != nil {
		return
	}
	e.S.UseEmoji([]byte(em))
	e.S.Touch()
}

// acceptMention puts "@Name " in place of "@query" and remembers whom it
// names; @all goes in as the wire token (zf:src/app.rs:7264).
func (s *Screen) acceptMention(e *screen.Env, a *state.Account, ch model.Chat, row int) {
	c := &s.comp
	m := c.members[row]
	id, name := c.at(m.id), c.at(m.name)
	everyone := m.all && s.canMentionEveryone(ch)
	member := false
	for _, p := range s.row.Participants {
		if bytes.Equal(p, id) {
			member = true
			break
		}
	}
	if !everyone && !(member && ch.Group()) {
		return
	}
	ins := append(s.scratch[:0], '@')
	if everyone {
		ins = append(ins, mentions.AllUser...)
	} else {
		ins = append(ins, name...)
	}
	ins = append(ins, ' ')
	s.scratch = ins
	at := c.start + len(ins)
	if err := s.st.Replace(platform.TextRange{Start: c.start, End: c.end}, ins, platform.TextRange{Start: at, End: at}); err != nil {
		return
	}
	if !everyone {
		s.mentions = append(s.mentions, mention{id: append([]byte(nil), id...), name: append([]byte(nil), name...)})
	}
	s.styleMentions(e)
}

// findNamedMention is the byte of the token "@Name" in text where no letter
// or digit follows it (zf:src/app.rs:9480).
func findNamedMention(text, token []byte) int {
	for off := 0; ; {
		i := bytes.Index(text[off:], token)
		if i < 0 {
			return -1
		}
		at := off + i
		end := at + len(token)
		if end == len(text) {
			return at
		}
		r, _ := utf8.DecodeRune(text[end:])
		if !isAlnum(r) {
			return at
		}
		off = at + 1
	}
}

// styleMentions colours the mentions in the text.
func (s *Screen) styleMentions(e *screen.Env) {
	text := s.st.Text()
	s.spans = s.spans[:0]
	col := e.K.Theme().Colors[theme.Accent]
	for _, m := range s.mentions {
		tok := append(append(s.scratch[:0], '@'), m.name...)
		s.scratch = tok
		if at := findNamedMention(text, tok); at >= 0 && len(s.spans) < cap(s.spans) {
			s.spans = append(s.spans, platform.TextSpan{Range: platform.TextRange{Start: at, End: at + len(tok)}, Color: col})
		}
	}
	_ = s.st.SetSpans(s.spans)
}
