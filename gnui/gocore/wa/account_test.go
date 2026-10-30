package wa

import (
	"errors"
	"slices"
	"testing"

	"go.mau.fi/whatsmeow/types"

	"zapfast/abi"
)

func TestAccountPrivacy(t *testing.T) {
	h := linked(t)
	x := h.f.more()
	x.privacySettings = &types.PrivacySettings{
		LastSeen: types.PrivacySettingContacts, Online: types.PrivacySettingMatchLastSeen, Profile: types.PrivacySettingAll,
		Status: types.PrivacySettingContactBlacklist, GroupAdd: types.PrivacySettingAll, ReadReceipts: types.PrivacySettingNone,
		CallAdd: types.PrivacySettingKnown,
	}
	h.send(abi.Command{Tag: abi.CommandTagFetchAccountPrivacy})
	evs := h.take()
	expectTags(t, evs, "AccountPrivacy ReceiptsPrivacy")
	want := []abi.PrivacyValue{
		{Kind: abi.PrivacyKindLastSeen, Choice: abi.PrivacyChoiceContacts},
		{Kind: abi.PrivacyKindOnline, Choice: abi.PrivacyChoiceSameAsLastSeen},
		{Kind: abi.PrivacyKindProfile, Choice: abi.PrivacyChoiceEveryone},
		{Kind: abi.PrivacyKindAbout, Choice: abi.PrivacyChoiceExcept},
		{Kind: abi.PrivacyKindGroupAdd, Choice: abi.PrivacyChoiceEveryone},
		{Kind: abi.PrivacyKindReadReceipts, Choice: abi.PrivacyChoiceNobody},
		{Kind: abi.PrivacyKindCallAdd, Choice: abi.PrivacyChoiceKnown},
	}
	if p := evs[0].AccountPrivacy; p.Failed || !slices.Equal(p.Values, want) || !evs[1].ReceiptsPrivacy.Disabled {
		t.Fatalf("privacy: %+v %+v", p, evs[1])
	}

	x.privacyErr = errors.New("timeout")
	h.send(abi.Command{Tag: abi.CommandTagFetchAccountPrivacy})
	if p := one(t, h.take(), abi.EventTagAccountPrivacy).AccountPrivacy; !p.Failed || len(p.Values) != 0 {
		t.Fatalf("failed fetch: %+v", p)
	}

	h.send(abi.Command{Tag: abi.CommandTagSetAccountPrivacy, SetAccountPrivacy: abi.CommandSetAccountPrivacy{Kind: abi.PrivacyKindReadReceipts, Choice: abi.PrivacyChoiceEveryone}})
	evs = h.take()
	expectTags(t, evs, "AccountPrivacySaved ReceiptsPrivacy")
	if evs[0].AccountPrivacySaved.Kind != abi.PrivacyKindReadReceipts || evs[1].ReceiptsPrivacy.Disabled || x.privacySet[0] != types.PrivacySettingTypeReadReceipts {
		t.Fatalf("saved: %+v", evs)
	}

	// A choice the category does not offer, and WhatsApp refusing.
	h.send(abi.Command{Tag: abi.CommandTagSetAccountPrivacy, SetAccountPrivacy: abi.CommandSetAccountPrivacy{Kind: abi.PrivacyKindOnline, Choice: abi.PrivacyChoiceNobody}})
	expectTags(t, h.take(), "AccountPrivacyFailed Error")
	x.setPrivacyErr = errors.New("no")
	h.send(abi.Command{Tag: abi.CommandTagSetAccountPrivacy, SetAccountPrivacy: abi.CommandSetAccountPrivacy{Kind: abi.PrivacyKindLastSeen, Choice: abi.PrivacyChoiceNobody}})
	evs = h.take()
	if one(t, evs, abi.EventTagAccountPrivacyFailed).AccountPrivacyFailed.Kind != abi.PrivacyKindLastSeen || lastError(t, evs) != "Could not update privacy settings." {
		t.Fatalf("refused: %s", tags(evs))
	}
}

func TestProfile(t *testing.T) {
	h := linked(t)
	h.send(abi.Command{Tag: abi.CommandTagSetProfile, SetProfile: abi.CommandSetProfile{Name: []byte(" Ada L "), HasName: true, About: []byte("Counting"), HasAbout: true}})
	m := one(t, h.take(), abi.EventTagMe).Me
	if string(m.Name) != "Ada L" || string(m.About) != "Counting" || h.f.more().about != "Counting" {
		t.Fatalf("me: %+v", m)
	}
	if p := h.f.patches[len(h.f.patches)-1].Mutations[0]; p.Index[0] != "setting_pushName" || p.Value.GetPushNameSetting().GetName() != "Ada L" {
		t.Fatalf("patch: %+v", p)
	}

	h.send(abi.Command{Tag: abi.CommandTagSetProfile, SetProfile: abi.CommandSetProfile{Name: []byte("a name far longer than twenty-five"), HasName: true}})
	if evs := h.take(); lastError(t, evs) != "Your name must have 1 to 25 characters." || len(only(evs, abi.EventTagMe)) != 0 {
		t.Fatalf("too long: %s", tags(evs))
	}

	h.send(abi.Command{Tag: abi.CommandTagPickProfilePicture})
	h.pick(writePNG(t, t.TempDir(), 40, 30, false))
	evs := h.take()
	expectTags(t, evs, "Avatar Avatar Me")
	if string(evs[0].Avatar.ID) != me.String() || h.f.more().photos[me] == nil {
		t.Fatalf("profile picture: %+v", evs[0])
	}
}
