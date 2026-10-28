package settings

import (
	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// acctInfo is what the window keeps of one account that the model does not:
// the answers to storage and privacy questions (CONTRACT §2).
type acctInfo struct {
	privacy      privacy
	receiptsOff  bool
	storage      abi.StorageStats
	measured     bool
	storageAsked bool
	privacyAsked bool
	storageToken uint64
	hint         string // the storage hint with the account's name in it
	hintGen      uint32
	hintLoc      i18n.Locale
	phone        []byte
	meGen        uint32
	meOK         bool
	pic          picture // our profile picture
}

// info is the window's record of account a.
func (s *Screen) info(a *state.Account) *acctInfo {
	in := s.acct[a]
	if in == nil {
		in = &acctInfo{}
		s.acct[a] = in
	}
	return in
}

// privacy is the account's privacy as the phone last reported it, with the
// writes in flight applied on top (zf:src/privacy.rs Snapshot).
type privacy struct {
	vals    [7]abi.PrivacyChoice
	known   [7]bool
	pending [7]bool
	revert  [7]abi.PrivacyChoice
	failed  bool // the last fetch failed; the values may be stale
	loaded  bool // a fetch has succeeded
}

func (p *privacy) has(i rowID) bool { return p.known[i] }

// editable: the values are loaded and the last fetch did not fail.
func (p *privacy) editable() bool { return p.loaded && !p.failed }

func (p *privacy) applyFetch(values []abi.PrivacyValue, failed bool) {
	p.failed = failed
	if failed {
		return
	}
	var fresh [7]abi.PrivacyChoice
	var known [7]bool
	for _, v := range values {
		if int(v.Kind) < len(fresh) {
			fresh[v.Kind], known[v.Kind] = v.Choice, true
		}
	}
	for i := range p.pending {
		if p.pending[i] {
			if known[i] {
				p.revert[i] = fresh[i]
			}
			if p.known[i] {
				fresh[i], known[i] = p.vals[i], true
			}
		}
	}
	p.vals, p.known, p.loaded = fresh, known, true
}

// offered reports whether the picker offers choice for category i.
func offered(i int, choice abi.PrivacyChoice) bool {
	for _, c := range privChoices[i] {
		if c == choice {
			return true
		}
	}
	return false
}

// beginSet shows choice at once and remembers what to restore; false when
// nothing should be written.
func (p *privacy) beginSet(i int, choice abi.PrivacyChoice) bool {
	if !p.known[i] || p.vals[i] == choice || p.pending[i] || !offered(i, choice) {
		return false
	}
	p.revert[i], p.vals[i], p.pending[i] = p.vals[i], choice, true
	return true
}

// listen registers for the answers the model does not keep. The picked
// download folder is the state's (it applies it).
func (s *Screen) listen(e *screen.Env) {
	e.S.Listen(s, abi.EventTagStorageStats, abi.EventTagAccountPrivacy, abi.EventTagAccountPrivacySaved,
		abi.EventTagAccountPrivacyFailed, abi.EventTagReceiptsPrivacy, abi.EventTagNotificationSoundPicked)
	// An unlinked number's privacy, storage and picture are not the next
	// one's (zf:src/app.rs:4153: account_privacy, account_receipts_off).
	e.S.OnUnlinked(s, func(_ *state.State, a *state.Account) { delete(s.acct, a) })
}

// Event implements state.Listener.
func (s *Screen) Event(a *state.Account, ev *abi.Event) {
	e := s.env
	if e == nil {
		return
	}
	in := s.info(a)
	switch ev.Tag {
	case abi.EventTagStorageStats:
		if ev.StorageStats.HasStats {
			in.storage, in.measured = ev.StorageStats.Stats, true
		}
	case abi.EventTagAccountPrivacy:
		in.privacy.applyFetch(ev.AccountPrivacy.Values, ev.AccountPrivacy.Failed)
	case abi.EventTagAccountPrivacySaved:
		if i := int(ev.AccountPrivacySaved.Kind); i < 7 {
			in.privacy.pending[i] = false
		}
	case abi.EventTagAccountPrivacyFailed:
		if i := int(ev.AccountPrivacyFailed.Kind); i < 7 && in.privacy.pending[i] {
			in.privacy.pending[i] = false
			in.privacy.vals[i] = in.privacy.revert[i]
		}
	case abi.EventTagReceiptsPrivacy:
		in.receiptsOff = ev.ReceiptsPrivacy.Disabled
	case abi.EventTagNotificationSoundPicked:
		if !a.Active() {
			break
		}
		sc := &e.S.Settings.MessageSound
		if ev.NotificationSoundPicked.Mention {
			sc = &e.S.Settings.MentionSound
		}
		*sc = state.SoundChoice{Sound: state.SoundCustom, Path: append([]byte(nil), ev.NotificationSoundPicked.Path...)}
		playSound(*sc)
		s.changed(e)
	default:
		return
	}
	s.wake()
}

// ask sends the questions an account on screen has not been asked yet: the
// storage measurement when the window opens, the privacy values once
// connected.
func (s *Screen) ask(e *screen.Env, a *state.Account) {
	in := s.info(a)
	if a.Conn == nil {
		return
	}
	if !in.storageAsked {
		in.storageAsked = true
		s.measure(a)
	}
	switch connected := a.Model.Link.Connected(); {
	case connected && !in.privacyAsked && !in.privacy.loaded:
		in.privacyAsked = true
		a.Send(a.Cmd(abi.CommandTagFetchAccountPrivacy))
	case !connected:
		in.privacyAsked = false
	}
}

var storageToken uint64

// measure asks for the size of the account's media cache.
func (s *Screen) measure(a *state.Account) {
	storageToken++
	cmd := a.Cmd(abi.CommandTagLoadStorageStats)
	cmd.LoadStorageStats.Token = storageToken
	a.Send(cmd)
}

// privacyNote is what stands over the privacy values while they cannot be
// changed (zf:src/ui/settings.rs).
func (s *Screen) privacyNote(e *screen.Env, a *state.Account) string {
	p := &s.info(a).privacy
	switch {
	case p.failed:
		return s.tx.privFailed
	case !a.Model.Link.Connected():
		return s.tx.privOffline
	case !p.loaded:
		return s.tx.privLoading
	}
	return ""
}

// privacyHint is the description of category i: its hint, and for a value
// that is edited on the phone, a note on that.
func (s *Screen) privacyHint(e *screen.Env, i int) string {
	a := e.Acct()
	if a != nil {
		if p := &s.info(a).privacy; p.known[i] && p.vals[i] == abi.PrivacyChoiceExcept {
			return s.tx.privExcept[i]
		}
	}
	return s.tx.desc[rPriv0+rowID(i)]
}

// privacyControl is category i's pop-up: what the phone holds and the values
// it takes; disabled while it cannot be changed or a write is in flight.
func (s *Screen) privacyControl(e *screen.Env, d *rowDef, title string) {
	a := e.Acct()
	i := int(d.id - rPriv0)
	p := &s.info(a).privacy
	n := len(privChoices[i])
	opts := append(s.privOpts[:0], s.tx.priv[i][:n]...)
	sel := n
	for j, c := range privChoices[i] {
		if c == p.vals[i] {
			sel = j
		}
	}
	if sel == n { // a value the picker does not offer: shown, not offered
		opts = append(opts, s.tx.privTail[p.vals[i]])
	}
	s.privOpts = opts
	k := e.K.Kit
	k.BeginDisabled(!p.editable() || !a.Model.Link.Connected() || p.pending[i])
	v, ok := s.popup(e, title, sel, opts, d.ctrl)
	k.EndDisabled()
	if ok && v < n {
		s.setPrivacy(e, a, i, privChoices[i][v])
	}
}

// setPrivacy writes category i (zf:src/app.rs SetAccountPrivacy): the value
// lives on the phone, so nothing is written without a connection and a
// snapshot to write against.
func (s *Screen) setPrivacy(e *screen.Env, a *state.Account, i int, choice abi.PrivacyChoice) {
	p := &s.info(a).privacy
	if !a.Model.Link.Connected() || !p.editable() || !p.beginSet(i, choice) {
		return
	}
	cmd := a.Cmd(abi.CommandTagSetAccountPrivacy)
	cmd.SetAccountPrivacy.Kind, cmd.SetAccountPrivacy.Choice = privKinds[i], choice
	a.Send(cmd)
}
