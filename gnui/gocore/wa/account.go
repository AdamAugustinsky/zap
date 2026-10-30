// Parts from zapfast-go 3104c46 (internal/backend/profile.go: privacy and
// profile actions), MIT. Changed for Zap: the privacy rows are
// upstream's PrivacyKind and PrivacyChoice (src/privacy.rs), and saving
// answers with AccountPrivacySaved or AccountPrivacyFailed.

package wa

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"

	"zapfast/abi"
)

const (
	meNameKey  = "me-name"
	meAboutKey = "me-about"
)

// privacyWire are the privacy categories' names on the wire, in the
// phone's order.
var privacyWire = []struct {
	kind abi.PrivacyKind
	name types.PrivacySettingType
}{
	{abi.PrivacyKindLastSeen, types.PrivacySettingTypeLastSeen},
	{abi.PrivacyKindOnline, types.PrivacySettingTypeOnline},
	{abi.PrivacyKindProfile, types.PrivacySettingTypeProfile},
	{abi.PrivacyKindAbout, types.PrivacySettingTypeStatus},
	{abi.PrivacyKindGroupAdd, types.PrivacySettingTypeGroupAdd},
	{abi.PrivacyKindReadReceipts, types.PrivacySettingTypeReadReceipts},
	{abi.PrivacyKindCallAdd, types.PrivacySettingTypeCallAdd},
}

var choiceWire = map[abi.PrivacyChoice]types.PrivacySetting{
	abi.PrivacyChoiceEveryone:       types.PrivacySettingAll,
	abi.PrivacyChoiceContacts:       types.PrivacySettingContacts,
	abi.PrivacyChoiceExcept:         types.PrivacySettingContactBlacklist,
	abi.PrivacyChoiceNobody:         types.PrivacySettingNone,
	abi.PrivacyChoiceSameAsLastSeen: types.PrivacySettingMatchLastSeen,
	abi.PrivacyChoiceKnown:          types.PrivacySettingKnown,
}

// offered are the choices the settings offer for a category, the phone's
// (upstream's PrivacyKind::choices). The account may hold others (an
// Except list), which are shown but not offered.
func offered(kind abi.PrivacyKind) []abi.PrivacyChoice {
	switch kind {
	case abi.PrivacyKindLastSeen, abi.PrivacyKindProfile, abi.PrivacyKindAbout:
		return []abi.PrivacyChoice{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceContacts, abi.PrivacyChoiceNobody}
	case abi.PrivacyKindGroupAdd:
		return []abi.PrivacyChoice{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceContacts}
	case abi.PrivacyKindOnline:
		return []abi.PrivacyChoice{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceSameAsLastSeen}
	case abi.PrivacyKindReadReceipts:
		return []abi.PrivacyChoice{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceNobody}
	case abi.PrivacyKindCallAdd:
		return []abi.PrivacyChoice{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceKnown}
	}
	return nil
}

// privacyValues are the categories the account's settings hold, with a
// value that category takes (upstream's values_from_response).
func privacyValues(s *types.PrivacySettings) []abi.PrivacyValue {
	held := map[types.PrivacySettingType]types.PrivacySetting{
		types.PrivacySettingTypeLastSeen: s.LastSeen, types.PrivacySettingTypeOnline: s.Online,
		types.PrivacySettingTypeProfile: s.Profile, types.PrivacySettingTypeStatus: s.Status,
		types.PrivacySettingTypeGroupAdd: s.GroupAdd, types.PrivacySettingTypeReadReceipts: s.ReadReceipts,
		types.PrivacySettingTypeCallAdd: s.CallAdd,
	}
	var values []abi.PrivacyValue
	for _, w := range privacyWire {
		for choice, wire := range choiceWire {
			if held[w.name] == wire && validPrivacy(w.kind, choice) {
				values = append(values, abi.PrivacyValue{Kind: w.kind, Choice: choice})
			}
		}
	}
	return values
}

// validPrivacy is whether a category can hold a value at all.
func validPrivacy(kind abi.PrivacyKind, choice abi.PrivacyChoice) bool {
	switch kind {
	case abi.PrivacyKindLastSeen, abi.PrivacyKindProfile, abi.PrivacyKindAbout, abi.PrivacyKindGroupAdd:
		return choice == abi.PrivacyChoiceEveryone || choice == abi.PrivacyChoiceContacts || choice == abi.PrivacyChoiceExcept || choice == abi.PrivacyChoiceNobody
	case abi.PrivacyKindReadReceipts:
		return choice == abi.PrivacyChoiceEveryone || choice == abi.PrivacyChoiceNobody
	case abi.PrivacyKindOnline:
		return choice == abi.PrivacyChoiceEveryone || choice == abi.PrivacyChoiceSameAsLastSeen
	case abi.PrivacyKindCallAdd:
		return choice == abi.PrivacyChoiceEveryone || choice == abi.PrivacyChoiceKnown
	}
	return false
}

func wireKind(kind abi.PrivacyKind) (types.PrivacySettingType, bool) {
	for _, w := range privacyWire {
		if w.kind == kind {
			return w.name, true
		}
	}
	return "", false
}

func (e *Engine) receiptsPrivacy(disabled bool) {
	ev := abi.Event{Tag: abi.EventTagReceiptsPrivacy}
	ev.ReceiptsPrivacy.Disabled = disabled
	e.out.Emit(&ev)
}

// fetchAccountPrivacy asks WhatsApp for the account's privacy. A failed
// fetch says so: the rows stay disabled rather than show an invented value.
func (e *Engine) fetchAccountPrivacy() {
	if !e.cli.IsConnected() {
		return
	}
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		s, err := cli.PrivacySettings(ctx)
		return func() {
			if gen != e.gen {
				return
			}
			ev := abi.Event{Tag: abi.EventTagAccountPrivacy}
			if err != nil || s == nil {
				slog.Debug("zapfast wa: privacy settings not fetched", "err", err)
				ev.AccountPrivacy.Failed = true
				e.out.Emit(&ev)
				return
			}
			ev.AccountPrivacy.Values = privacyValues(s)
			e.out.Emit(&ev)
			e.receiptsPrivacy(s.ReadReceipts != types.PrivacySettingAll)
		}
	})
}

// setAccountPrivacy writes one category.
func (e *Engine) setAccountPrivacy(kind abi.PrivacyKind, choice abi.PrivacyChoice) {
	failed := func() {
		ev := abi.Event{Tag: abi.EventTagAccountPrivacyFailed}
		ev.AccountPrivacyFailed.Kind = kind
		e.out.Emit(&ev)
		e.emitError("Could not update privacy settings.")
	}
	name, ok := wireKind(kind)
	valid := false
	for _, c := range offered(kind) {
		valid = valid || c == choice
	}
	if !ok || !valid {
		failed()
		return
	}
	if !e.cli.IsConnected() {
		e.emitError("Not connected to WhatsApp")
		failed()
		return
	}
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		_, err := cli.SetPrivacySetting(ctx, name, choiceWire[choice])
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				slog.Debug("zapfast wa: privacy setting not written", "err", err)
				failed()
				return
			}
			ev := abi.Event{Tag: abi.EventTagAccountPrivacySaved}
			ev.AccountPrivacySaved.Kind = kind
			e.out.Emit(&ev)
			if kind == abi.PrivacyKindReadReceipts {
				e.receiptsPrivacy(choice != abi.PrivacyChoiceEveryone)
			}
		}
	})
}

// meName is the account's name: the one saved here, else the phone's.
func (e *Engine) meName() string {
	if v, err := e.arch.Metadata(meNameKey); err == nil && len(v) > 0 {
		return string(v)
	}
	return e.cli.PushName()
}

func (e *Engine) meAbout() string {
	v, _ := e.arch.Metadata(meAboutKey)
	return string(v)
}

// setProfile changes the account's name or About (or both); what WhatsApp
// accepts is kept, and Me says so.
func (e *Engine) setProfile(name string, hasName bool, about string, hasAbout bool) {
	if !e.cli.IsConnected() {
		e.emitError("Connect to WhatsApp to change your profile.")
		return
	}
	if hasName {
		name = strings.TrimSpace(name)
		if n := utf8.RuneCountInString(name); n < 1 || n > 25 {
			e.emitError("Your name must have 1 to 25 characters.")
			hasName = false
		}
	}
	if hasAbout && utf8.RuneCountInString(about) > 139 {
		e.emitError("Your About can have at most 139 characters.")
		hasAbout = false
	}
	if !hasName && !hasAbout {
		return
	}
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		var nameErr, aboutErr error
		if hasName {
			nameErr = cli.SendAppState(ctx, appstate.BuildSettingPushName(name))
		}
		if hasAbout {
			aboutErr = cli.SetAbout(ctx, about)
		}
		return func() {
			if gen != e.gen {
				return
			}
			saved := false
			if hasName {
				if nameErr != nil {
					e.emitError("Could not change your name: " + nameErr.Error())
				} else if err := e.arch.SetMetadata(meNameKey, []byte(name)); err == nil {
					saved = true
				}
			}
			if hasAbout {
				if aboutErr != nil {
					e.emitError("Could not change your About: " + aboutErr.Error())
				} else if err := e.arch.SetMetadata(meAboutKey, []byte(about)); err == nil {
					saved = true
				}
			}
			if saved {
				e.emitMe()
			}
		}
	})
}

// pickProfilePicture asks for a picture and makes it the account's.
func (e *Engine) pickProfilePicture() {
	e.pickPicture("Choose a profile picture", func(jpeg []byte) {
		me, ok := jidOf(e.me())
		if !ok || !e.cli.IsConnected() {
			e.emitError("Connect to WhatsApp to change your profile picture.")
			return
		}
		cli, gen := e.cli, e.gen
		e.job(func(ctx context.Context) func() {
			_, err := cli.SetGroupPhoto(ctx, me, jpeg)
			return func() {
				if gen != e.gen {
					return
				}
				if err != nil {
					e.emitError("Could not change your profile picture: " + err.Error())
					return
				}
				e.pictureChanged(e.me(), jpeg)
				e.emitMe()
			}
		})
	})
}
