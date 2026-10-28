package sys

import (
	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
)

// What links, notifications and the command line ask the app to open. They
// wait here until the account on screen has its core open and is linked,
// until the app lock is lifted, and until the chat list has arrived: links
// "wait for the unlock rather than opening anything the lock hides, and then
// open in the order they arrived" (the guide, Links).

type targetKind uint8

const (
	tChat   targetKind = iota + 1 // start a chat a link named, with a template for its composer
	tOpen                         // a notification's click: the chat at its message, or the call
	tReply                        // send text to a chat (a reply from a notification)
	tInvite                       // show a group invite
	tJoin                         // join a call link
)

// maxPending is how many targets wait; the oldest past it is dropped.
const maxPending = 16

type target struct {
	kind targetKind
	acct string // the account's ID; "" for the one on screen when it opens
	chat string // tChat, tOpen, tReply: the chat's JID
	msg  string // tOpen: the message to land on; empty for a call
	text string // tChat: the template; tReply: the reply
	code string // tInvite
	// tJoin
	token string
	video bool
}

type links struct {
	pending []target
}

// queue adds a target to open when the app can.
func (y *System) queue(t target) {
	k := &y.k
	if len(k.pending) >= maxPending {
		k.pending = append(k.pending[:0], k.pending[1:]...)
	}
	k.pending = append(k.pending, t)
	if y.s != nil {
		y.s.Wake()
	}
}

// openArg queues what a link, a number or a URL asks for. An unreadable one
// opens nothing and says so (LN-006).
func (y *System) openArg(arg string) bool {
	l, ok := parseLink(arg)
	if !ok {
		if y.s != nil {
			y.s.Toast([]byte(i18n.Format(i18n.T("Zap cannot read a phone number from {argument}"), "{argument}", arg)), true)
		}
		return false
	}
	switch l.kind {
	case linkChat:
		y.queue(target{kind: tChat, chat: l.chat, text: l.text})
	case linkInvite:
		y.queue(target{kind: tInvite, code: l.code})
	case linkCall:
		y.queue(target{kind: tJoin, token: l.token, video: l.video})
	}
	return true
}

// openArgs does what the command line of a launch asks (the first launch's,
// or a second launch's, handed to this copy). It reports whether the window
// should come forward.
func (y *System) openArgs(args []string) (show bool) {
	cmd, arg, err := parseArgs(args)
	if err != nil {
		if y.s != nil {
			y.s.Toast([]byte(err.Error()), true)
		}
		return false
	}
	switch cmd {
	case cmdQuit:
		y.quit()
		return false
	case cmdReloadThemes:
		y.reload()
		return false
	case cmdLink:
		return y.openArg(arg)
	case cmdShow:
		return true
	}
	return false
}

// flush opens the targets that can open, in order, and keeps the rest.
func (y *System) flush(s *state.State) {
	k := &y.k
	if len(k.pending) == 0 || s.Lock.Locked {
		return
	}
	n := 0
	for n < len(k.pending) && y.run(s, &k.pending[n]) {
		n++
	}
	if n > 0 {
		k.pending = append(k.pending[:0], k.pending[n:]...)
	}
}

// ready is whether the account has its core open and is linked.
func ready(a *state.Account) bool { return a != nil && a.Conn != nil && a.Model.Linked() }

// run opens t; it reports false when t has to wait.
func (y *System) run(s *state.State, t *target) (done bool) {
	var a *state.Account
	if t.acct == "" {
		a = s.Acct()
	} else {
		for _, x := range s.Accounts {
			if x.ID == t.acct {
				a = x
			}
		}
		if a == nil {
			return true // the account is gone
		}
	}
	if a != nil && a.Conn == nil && a.Pending == nil && a.Err != "" {
		return true // its core failed to open: nothing will come
	}
	if !ready(a) {
		return false
	}
	switch t.kind {
	case tOpen:
		if !a.Model.Chats.Loaded() {
			return false
		}
		var msg []byte
		if t.msg != "" {
			msg = []byte(t.msg)
		}
		s.OpenMessageIn(t.acct, []byte(t.chat), msg)
		if msg == nil {
			// A call's notification: its window, while the call is still up.
			if u := a.Model.CallUpdate(); u != nil && model.CallLive(u.Phase) {
				s.OpenWindow(state.WinCall)
			}
		}
		s.RaiseMain()
		return true
	case tReply:
		y.reply(a, t.chat, t.text)
		return true
	case tInvite:
		y.show(s, a)
		s.OpenDialog(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte(t.code)})
		s.RaiseMain()
		return true
	case tJoin:
		y.show(s, a)
		if y.JoinCall != nil {
			y.JoinCall(s, a, []byte(t.token), t.video)
		} else {
			logWarn("a call link cannot be joined: no hook")
		}
		s.RaiseMain()
		return true
	}
	// tChat: StartChat opens the chat, asking the core for it first when
	// the account does not have it, and prefills the composer.
	if !a.Model.Chats.Loaded() {
		return false
	}
	y.show(s, a)
	if s.Dialog.Open() {
		s.CloseDialog() // a link starts the chat over a dialog that was open
	}
	var text []byte
	if t.text != "" {
		text = []byte(t.text)
	}
	a.StartChat([]byte(t.chat), []byte(phoneName(t.chat)), text)
	s.RaiseMain()
	return true
}

// show puts the account on screen.
func (y *System) show(s *state.State, a *state.Account) {
	if a.Index != s.Active {
		s.SwitchAccount(a.Index)
	}
	s.AddingAccount = false
	s.Touch()
}

// reply sends the text a person typed into a notification to its chat, and
// marks the chat read: answering is reading (NT-015).
func (y *System) reply(a *state.Account, chat, text string) {
	cmd := a.Cmd(abi.CommandTagSendText)
	cmd.SendText.Chat, cmd.SendText.Text = []byte(chat), []byte(text)
	if a.Send(cmd) != nil {
		return
	}
	cmd = a.Cmd(abi.CommandTagMarkRead)
	cmd.MarkRead.Chat = []byte(chat)
	cmd.MarkRead.Receipts = a.Settings.SendReadReceipts && !a.Model.ReceiptsOff()
	a.Send(cmd)
}

// phoneName is the name a chat shows until the contact is known: the number.
func phoneName(jid string) string {
	n, _ := numberOfJID(jid)
	if n == "" {
		return jid
	}
	return "+" + n
}
