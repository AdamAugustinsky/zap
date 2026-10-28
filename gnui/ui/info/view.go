package info

import (
	"bytes"
	"slices"
	"strings"
	"time"
	"unicode"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
)

// member is one row of the group's member list.
type member struct {
	jid, name, label span
	me               bool
}

// view is what the overview shows of the chat, read from the model when it
// changes (zf:src/ui/info.rs's Shown). Its strings are in its own buffers,
// so a frame draws them without allocating.
type view struct {
	ok      bool
	hasChat bool
	ref     model.ChatRef

	name         []byte // the title the chat list shows
	subject      []byte // a group's subject as the phone named it
	subjectKnown bool
	group        bool
	channel      bool
	mine         bool // the panel is of our own number
	locked       bool
	phone        string
	phoneDigits  string
	members      int
	canLeave     bool
	canEdit      bool
	muted        bool
	hasMuted     bool
	mutedUntil   int64
	pinned       bool
	archived     bool
	description  []byte
	hasDesc      bool
	timer        string
	mutedLine    string
	status       string
	memberLine   string
	savedName    bool // the address book holds a name for the contact
	first        []byte

	list    []member
	listBuf []byte

	// what the strings were made from
	chatGen, contactGen, meGen uint32
	acct                       int
	mutedFor                   int64
	presence                   struct {
		online bool
		seen   int64
		ok     bool
	}
	scratch abi.Chat
	names   []nameKey
}

type nameKey struct {
	m    member
	sort string
}

func (v *view) text(s span) []byte { return v.listBuf[s.off : s.off+s.n : s.off+s.n] }

// isMe reports whether jid is one of our ids.
func isMe(st *model.Store, jid []byte) bool {
	return len(jid) > 0 && (bytes.Equal(st.Me.ID(), jid) || bytes.Equal(st.Me.LID(), jid))
}

func displayName(st *model.Store, jid []byte) string {
	if isMe(st, jid) {
		return i18n.T("You")
	}
	return string(st.DisplayName(jid))
}

func selfTitle(st *model.Store) string {
	if n := strings.TrimSpace(string(st.Me.Name())); n != "" {
		return i18n.Format(i18n.T("{name} (You)"), "{name}", n)
	}
	return i18n.T("You")
}

func shortName(st *model.Store, jid []byte, name string, me bool) string {
	if me || strings.HasPrefix(name, "+") {
		return name
	}
	if i := st.Contacts.Find(jid); i >= 0 {
		if f := st.Contacts.At(i).FirstName(); len(f) > 0 {
			return string(f)
		}
	}
	if f := strings.Fields(name); len(f) > 0 {
		return f[0]
	}
	return name
}

// participantNames is the members' names for an unnamed group, "Ada, Grace".
func participantNames(st *model.Store, parts [][]byte) string {
	var names, numbers []string
	seen := map[string]bool{}
	for _, p := range parts {
		if isMe(st, p) || seen[string(p)] {
			continue
		}
		seen[string(p)] = true
		n := string(st.DisplayName(p))
		if strings.HasPrefix(n, "+") || n == "Unknown" {
			numbers = append(numbers, n)
		} else {
			names = append(names, shortName(st, p, strings.TrimPrefix(n, "~"), false))
		}
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	out := append(names, numbers...)
	return strings.Join(out, ", ")
}

func sortKey(name string) string {
	return strings.ToLower(strings.TrimLeftFunc(name, func(r rune) bool { return r == '~' || unicode.IsSpace(r) }))
}

// load fills the view for jid from the account's model.
func (v *view) load(a *state.Account, jid []byte, now time.Time) {
	st := a.Model
	ref := st.Chats.Find(jid)
	v.acct = a.Index
	v.ok = true
	v.hasChat = ref != 0
	v.ref = ref
	v.contactGen = st.Contacts.Gen()
	v.meGen = st.Me.Gen()
	v.mine = isMe(st, jid)
	v.group, v.channel, v.locked = false, bytes.HasSuffix(jid, []byte("@newsletter")), false
	v.members, v.canLeave, v.canEdit = 0, false, false
	v.hasMuted, v.muted, v.mutedUntil = false, false, 0
	v.pinned, v.archived, v.hasDesc = false, false, false
	v.description = v.description[:0]
	v.subject = v.subject[:0]
	v.subjectKnown = false
	v.list = v.list[:0]
	v.listBuf = v.listBuf[:0]
	v.timer = ""
	v.first = v.first[:0]
	v.savedName = false

	var parts [][]byte
	if v.hasChat {
		ch := st.Chats.At(ref)
		v.chatGen = ch.Gen()
		v.group = ch.Group()
		v.locked = ch.Locked()
		v.pinned, v.archived = ch.Pinned(), ch.Archived()
		v.subject = append(v.subject, ch.Name()...)
		v.subjectKnown = ch.SubjectKnown()
		v.mutedUntil, v.hasMuted = ch.MutedUntil()
		v.muted = ch.Muted(now.Unix())
		if sec, ok := ch.Ephemeral(); ok {
			v.timer = disappearingLine(sec, true)
		}
		if ch.Decode(&v.scratch) == nil {
			parts = v.scratch.Participants
			if v.group && v.scratch.HasGroupDescription && len(bytes.TrimSpace(v.scratch.GroupDescription)) > 0 {
				v.description = append(v.description, v.scratch.GroupDescription...)
				v.hasDesc = true
			}
		}
		v.members = len(parts)
		switch {
		case ch.Left():
			// nothing to leave
		case v.channel:
			v.canLeave = !ch.ReadOnly()
		case v.group:
			v.canLeave = len(parts) == 0 || listsAny(parts, st.Me.ID(), st.Me.LID())
		}
		locked, known := ch.InfoLocked()
		v.canEdit = v.group && !ch.Left() && (ch.Admin() || (known && !locked))
	}
	if i := st.Contacts.Find(jid); i >= 0 {
		c := st.Contacts.At(i)
		v.savedName = len(c.FullName()) > 0
		v.first = append(v.first, c.FirstName()...)
	}

	// the title
	title := ""
	switch {
	case v.group && (len(bytes.TrimSpace(v.subject)) == 0 || (string(v.subject) == "Group" && !v.subjectKnown)):
		title = participantNames(st, parts)
		if title == "" {
			title = i18n.T("Group")
		}
	case v.mine:
		title = selfTitle(st)
	case v.group:
		title = string(v.subject)
	default:
		title = string(st.DisplayName(jid))
		if !v.hasChat {
			title = displayName(st, jid)
		}
	}
	v.name = append(v.name[:0], title...)
	v.phone, v.phoneDigits = "", ""
	if p := model.Phone(jid); p != nil {
		v.phoneDigits = string(p)
		v.phone = format.Phone(v.phoneDigits)
	}
	v.memberLine = ""
	if v.group && v.members > 0 {
		v.memberLine = i18n.Format(i18n.N("{} member", "{} members", v.members), "{}", itoa(v.members))
	}
	v.mutedLine = ""
	if v.hasMuted {
		if v.mutedUntil == 0 {
			v.mutedLine = i18n.T("Muted")
		} else {
			v.mutedLine = i18n.T("Muted until") + " " + chatStamp(now, v.mutedUntil)
		}
	}
	v.loadMembers(st, parts)
}

func listsAny(parts [][]byte, ours ...[]byte) bool {
	for _, p := range parts {
		for _, o := range ours {
			if len(o) > 0 && bytes.Equal(p, o) {
				return true
			}
		}
	}
	return false
}

// loadMembers sorts the member list once per change: names first, A to Z,
// then the numbers, and ourselves last as "You".
func (v *view) loadMembers(st *model.Store, parts [][]byte) {
	if !v.group {
		return
	}
	v.names = v.names[:0]
	var meRow *member
	for _, p := range parts {
		if isMe(st, p) {
			continue
		}
		n := string(st.DisplayName(p))
		v.names = append(v.names, nameKey{m: member{jid: v.putList(p), name: v.putListS(n), label: v.putListS(n + "###" + string(p))}, sort: sortKey(n)})
		if strings.HasPrefix(n, "+") || n == "Unknown" {
			v.names[len(v.names)-1].sort = "\xff" + n
		}
	}
	slices.SortStableFunc(v.names, func(a, b nameKey) int { return strings.Compare(a.sort, b.sort) })
	for _, p := range parts {
		if isMe(st, p) {
			me := member{jid: v.putList(p), name: v.putListS(i18n.T("You")), label: v.putListS(i18n.T("You") + "###" + string(p)), me: true}
			meRow = &me
			break
		}
	}
	for i := range v.names {
		v.list = append(v.list, v.names[i].m)
	}
	if meRow != nil {
		v.list = append(v.list, *meRow)
	}
}

func (v *view) putList(b []byte) span {
	off := len(v.listBuf)
	v.listBuf = append(v.listBuf, b...)
	return span{uint32(off), uint32(len(b))}
}

func (v *view) putListS(s string) span {
	off := len(v.listBuf)
	v.listBuf = append(v.listBuf, s...)
	return span{uint32(off), uint32(len(s))}
}

// presenceLine is "online" or the last-seen line; "" when nothing is known.
func presenceLine(online bool, seen int64, ok bool, now time.Time) string {
	switch {
	case !ok:
		return ""
	case online:
		return "online"
	case seen > 0:
		return lastSeen(now, seen)
	}
	return ""
}
