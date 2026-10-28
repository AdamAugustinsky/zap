package settings

import (
	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/state"
)

// textCache holds every string a frame shows, translated once per language,
// and the folded words the search looks in, so that a frame allocates
// nothing.
type textCache struct {
	strs
	loc  i18n.Locale
	done bool

	sec     [numSections]string
	secFold [numSections][]byte
	title   [numRows]string
	desc    [numRows]string
	fold    [numRows][]byte // title, description and keywords, folded, 0 between

	feedNote, receiptsOff, approval string
	block                           [numRows]string

	theme    [3]string // Follow system, Light, Dark
	font     [2]string
	lang     [1 + len(i18n.All)]string
	limit    [4]string
	lockOpts [3]string
	sound    [4]string // Pidgin, Pidgin alert, System default, None
	choose   string
	priv     [7][4]string // the choices each category offers
	privOf   [7][]abi.PrivacyChoice
	privTail [6]string // a value the picker does not offer: Except, Nobody…

	off, on, auto string
}

// themeChoices are the choices Settings offers, in menu order
// (zf:src/settings.rs ThemeChoice::ALL).
var themeChoices = [3]state.Theme{state.ThemeSystem, state.ThemeLight, state.ThemeDark}

var limitChoices = [4]state.NotificationLimit{state.LimitOff, state.LimitDirect, state.LimitGroup, state.LimitBoth}

// privChoices are the values each category offers (zf:src/privacy.rs
// PrivacyKind::choices).
var privChoices = [7][]abi.PrivacyChoice{
	{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceContacts, abi.PrivacyChoiceNobody},
	{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceSameAsLastSeen},
	{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceContacts, abi.PrivacyChoiceNobody},
	{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceContacts, abi.PrivacyChoiceNobody},
	{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceContacts},
	{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceNobody},
	{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceKnown},
}

func privChoiceText(c abi.PrivacyChoice) tx {
	switch c {
	case abi.PrivacyChoiceEveryone:
		return tx{"privacy", "Everyone"}
	case abi.PrivacyChoiceContacts:
		return tx{"privacy", "My contacts"}
	case abi.PrivacyChoiceExcept:
		return tx{"privacy", "My contacts except…"}
	case abi.PrivacyChoiceNobody:
		return tx{"privacy", "Nobody"}
	case abi.PrivacyChoiceSameAsLastSeen:
		return tx{"privacy", "Same as last seen"}
	}
	return tx{"privacy", "Silence unknown callers"}
}

func (t *textCache) ensure(loc i18n.Locale) {
	if t.done && t.loc == loc {
		return
	}
	t.done, t.loc = true, loc
	for i := range t.sec {
		t.sec[i] = i18n.T(secTitle[i])
		t.secFold[i] = format.AppendFold(format.AppendFold(t.secFold[i][:0], t.sec[i]), "\x00"+secTitle[i])
	}
	for i := range rowDefs {
		d := &rowDefs[i]
		t.title[d.id], t.desc[d.id] = "", ""
		t.block[d.id] = secTitle[d.sec] + "###block" + string(rune('A'+d.id))
		f := t.fold[d.id][:0]
		add := func(x tx) {
			if x.id == "" {
				return
			}
			f = format.AppendFold(format.AppendFold(format.AppendFold(f, x.shown()), "\x00"), x.id)
			f = append(f, 0)
		}
		add(d.title)
		add(d.desc)
		for _, k := range d.kw {
			add(k)
		}
		t.fold[d.id] = f
		if d.title.id != "" {
			t.title[d.id] = d.title.shown()
		}
		if d.desc.id != "" {
			t.desc[d.id] = keyed(d.desc.shown())
		}
	}
	t.receiptsOff = i18n.T("Off for your account, so only groups get them.")
	t.approval = i18n.T("Allow Zap in System Settings, under Login Items.")
	t.feedNote = i18n.T("Updates come from a local test feed until the first release feed exists.")
	t.theme = [3]string{i18n.T("Follow system"), i18n.T("Light"), i18n.T("Dark")}
	t.font = [2]string{i18n.T("System"), "Inter"}
	t.auto = i18n.T("Auto")
	t.lang[0] = t.auto
	for i, l := range i18n.All {
		t.lang[1+i] = l.Label()
	}
	t.limit = [4]string{i18n.T("Off"), i18n.T("Direct messages"), i18n.T("Groups"), i18n.T("Direct messages and groups")}
	t.lockOpts = [3]string{i18n.T("After 1 minute"), i18n.T("After 15 minutes"), i18n.T("After 1 hour")}
	t.sound = [4]string{"Pidgin", i18n.T("Pidgin alert"), i18n.T("System default"), i18n.P("sound", "None")}
	t.choose = i18n.T("Choose a file…")
	for i := range t.priv {
		t.privOf[i] = privChoices[i]
		for j, c := range privChoices[i] {
			t.priv[i][j] = privChoiceText(c).shown()
		}
	}
	t.privTail = [6]string{}
	t.privTail[abi.PrivacyChoiceExcept] = privChoiceText(abi.PrivacyChoiceExcept).shown()
	t.privTail[abi.PrivacyChoiceNobody] = privChoiceText(abi.PrivacyChoiceNobody).shown()
	t.off, t.on = i18n.T("Off"), i18n.T("On")
	t.strs.build()
}
