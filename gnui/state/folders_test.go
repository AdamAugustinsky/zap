package state

import "testing"

// Folders live in the account's settings file and come back as they went.
func TestFoldersRoundTrip(t *testing.T) {
	a := AccountSettings{Folders: []Folder{
		{ID: "f1", Name: "Work", Icon: "briefcase", Chats: [][]byte{[]byte("1@s.whatsapp.net")}, Space: true},
		{ID: "f2", Name: "Family", Icon: "house", Collapsed: true},
	}}
	f := a.toFile()
	back := f.settings()
	if len(back.Folders) != 2 || back.Folders[0].Name != "Work" || !back.Folders[0].Space ||
		string(back.Folders[0].Chats[0]) != "1@s.whatsapp.net" || !back.Folders[1].Collapsed || back.Folders[1].Icon != "house" {
		t.Fatalf("folders came back as %+v", back.Folders)
	}
}

// A space's whole colour (tint, mix, strength), the account's and a
// folder's, goes to the file and comes back.
func TestSpaceThemesRoundTrip(t *testing.T) {
	a := AccountSettings{SpaceTint: "blue", SpaceTintMix: "complementary", SpaceTintLevel: 0.8,
		Folders: []Folder{{ID: "f1", Name: "Work", Tint: "teal", TintMix: "triadic", TintLevel: 0.3}}}
	f := a.toFile()
	back := f.settings()
	if back.SpaceTint != "blue" || back.SpaceTintMix != "complementary" || back.SpaceTintLevel != 0.8 {
		t.Errorf("the account's theme came back %q %q %v", back.SpaceTint, back.SpaceTintMix, back.SpaceTintLevel)
	}
	if w := back.Folders[0]; w.Tint != "teal" || w.TintMix != "triadic" || w.TintLevel != 0.3 {
		t.Errorf("the folder's theme came back %q %q %v", w.Tint, w.TintMix, w.TintLevel)
	}
	// colours of one's own, as "#rrggbb"; what does not read is left out
	own := AccountSettings{SpaceTintDots: [3]uint32{0x8B5CF6FF, 0x22C55EFF}}
	of := own.toFile()
	if len(of.SpaceTintColors) != 2 || of.SpaceTintColors[0] != "#8b5cf6" {
		t.Errorf("own colours went to the file as %v", of.SpaceTintColors)
	}
	if b := of.settings(); b.SpaceTintDots != own.SpaceTintDots {
		t.Errorf("own colours came back %08X", b.SpaceTintDots)
	}
	if d := dotsFromFile([]string{"#zz0000", "#00ff00", "red"}); d != [3]uint32{0x00FF00FF} {
		t.Errorf("bad colours read as %08X", d)
	}
	// a space's own appearance
	m := AccountSettings{SpaceTintMode: "light", SpaceTintGrain: 0.5, Folders: []Folder{{ID: "f1", Name: "Work", TintMode: "dark", TintGrain: 0.25}}}
	mf := m.toFile()
	if mb := mf.settings(); mb.SpaceTintMode != "light" || mb.Folders[0].TintMode != "dark" || mb.SpaceTintGrain != 0.5 || mb.Folders[0].TintGrain != 0.25 {
		t.Errorf("modes came back %q %q", mb.SpaceTintMode, mb.Folders[0].TintMode)
	}
	if (SpaceTheme{}).Strength() != DefaultTintLevel || (SpaceTheme{Level: 2}).Strength() != 1 {
		t.Error("Strength does not default or cap")
	}
}

// A theme's lock goes to the file only where it is not what its harmony
// meant before there was one: a file without it reads a harmony as locked
// and colours with none as free.
func TestSpaceThemeLock(t *testing.T) {
	cases := []struct {
		mix  string
		lock bool
		kept bool // written to the file
	}{{"", false, false}, {"triadic", true, false}, {"triadic", false, true}, {"", true, true}}
	for _, c := range cases {
		a := AccountSettings{SpaceTintMix: c.mix, SpaceTintLock: c.lock,
			Folders: []Folder{{ID: "f1", Name: "Work", TintMix: c.mix, TintLock: c.lock}}}
		f := a.toFile()
		if (f.SpaceTintLock != nil) != c.kept || (f.Folders[0].TintLock != nil) != c.kept {
			t.Errorf("%q locked %v: written %v", c.mix, c.lock, f.SpaceTintLock != nil)
		}
		if b := f.settings(); b.SpaceTintLock != c.lock || b.Folders[0].TintLock != c.lock {
			t.Errorf("%q locked %v came back %v, %v", c.mix, c.lock, b.SpaceTintLock, b.Folders[0].TintLock)
		}
	}
}

// A chat's boost goes to the file and comes back; what is not one of the
// choices is left out, and none takes the chat's away.
func TestChatBoosts(t *testing.T) {
	a := &Account{st: &State{}}
	jid := []byte("1@s.whatsapp.net")
	a.SetBoost(jid, ChatBoost{Font: "serif", Size: 1.25, Tint: "teal"})
	a.SetBoost([]byte("2@s.whatsapp.net"), ChatBoost{Font: "comic", Size: 3, Tint: "neon"})
	if len(a.Settings.Boosts) != 1 {
		t.Fatalf("boosts %v", a.Settings.Boosts)
	}
	back := a.Settings.toFile()
	if got := back.settings().Boosts[string(jid)]; got != (ChatBoost{Font: "serif", Size: 1.25, Tint: "teal"}) {
		t.Errorf("the boost came back %+v", got)
	}
	f := accountFile{ChatBoosts: map[string]boostFile{"3@s.whatsapp.net": {Font: "mono", Size: 7}}}
	if got := f.settings().Boosts["3@s.whatsapp.net"]; got != (ChatBoost{Font: "mono"}) {
		t.Errorf("a size not on the steps read as %+v", got)
	}
	a.SetBoost(jid, ChatBoost{Size: 1})
	if len(a.Settings.Boosts) != 0 || a.BoostOf(jid).Scale() != 1 {
		t.Errorf("none left %v", a.Settings.Boosts)
	}
}
