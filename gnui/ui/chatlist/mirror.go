package chatlist

import (
	"bytes"
	"slices"
	"strings"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
)

// What the list shows that the model keeps (labels, the starred list, the
// search's message hits, ui/CONTRACT.md §2) is read from the model; mirror
// holds the strings the rows draw, rebuilt when the model's generation of
// each moves. A frame in which nothing moved reads them and builds nothing.

type label struct{ id, name, tag, color string }

type hit struct {
	chat, id, sender, text string
	ts                     int64
	fromMe                 bool
}

type starred struct {
	chat, id, sender, text string
	ts, at                 int64
	fromMe                 bool
}

type mirror struct {
	serial uint64 // the account's (state.Account.Serial)
	gen    uint64

	labels []label
	labGen uint32
	labOK  bool

	hits   []hit
	hitGen uint32
	hitQ   string
	hitOK  bool

	starred []starred
	starGen uint32
	starOK  bool
}

func (m *mirror) labelIndex(id []byte) int {
	for i := range m.labels {
		if m.labels[i].id == string(id) {
			return i
		}
	}
	return -1
}

// refreshMirror brings the account's mirror up to the model's answers.
func (s *Screen) refreshMirror(a *state.Account, m *mirror) {
	st := a.Model
	if g := st.LabelGen(); !m.labOK || g != m.labGen {
		m.labOK, m.labGen = true, g
		m.labels = m.labels[:0]
		for i := 0; i < st.Labels(); i++ {
			l := st.LabelAt(i)
			id := string(l.ID)
			m.labels = append(m.labels, label{id: id, name: string(l.Name), tag: string(l.Name) + "###label-" + id, color: string(l.Color)})
		}
		m.gen++
	}
	// the message hits: the answer to the text asked, not an earlier one
	if ans := st.Answer(abi.EventTagSearchHits); !m.hitOK || ans.Gen() != m.hitGen || m.hitQ != s.sentQuery {
		m.hitOK, m.hitGen, m.hitQ = true, ans.Gen(), s.sentQuery
		m.hits = m.hits[:0]
		if ans.Has() && s.sentQuery != "" {
			if ev := ans.Event(); ev != nil && string(ev.SearchHits.Query) == s.sentQuery {
				for k := 0; k < ans.Len(); k++ {
					ev, j := ans.Item(k)
					if ev == nil {
						break
					}
					x := &ev.SearchHits.Messages[j]
					m.hits = append(m.hits, hit{chat: string(x.Chat), id: string(x.ID), ts: x.Timestamp, fromMe: x.FromMe,
						sender: senderOf(x), text: summarize(x)})
				}
			}
		}
		m.gen++
	}
	if ans := st.Answer(abi.EventTagStarredList); !m.starOK || ans.Gen() != m.starGen {
		m.starOK, m.starGen = true, ans.Gen()
		m.starred = m.starred[:0]
		for k := 0; k < ans.Len(); k++ {
			ev, j := ans.Item(k)
			if ev == nil {
				break
			}
			x := &ev.StarredList.Starred[j]
			m.starred = append(m.starred, starred{chat: string(x.Message.Chat), id: string(x.Message.ID), ts: x.Message.Timestamp,
				at: x.StarredAt, fromMe: x.Message.FromMe, sender: senderOf(&x.Message), text: summarize(&x.Message)})
		}
		slices.SortStableFunc(m.starred, func(a, b starred) int {
			switch {
			case a.at > b.at:
				return -1
			case a.at < b.at:
				return 1
			}
			return 0
		})
		m.gen++
	}
}

// senderOf is the name a message was sent under, else its sender's id.
func senderOf(x *abi.Message) string {
	if x.HasSenderName && len(x.SenderName) > 0 {
		return string(x.SenderName)
	}
	return string(x.Sender)
}

// oneLine is text on one line, its runs of space collapsed.
func oneLine(b []byte) string {
	if len(bytes.TrimSpace(b)) == 0 {
		return ""
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

var _ model.ChatRef
