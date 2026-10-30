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
