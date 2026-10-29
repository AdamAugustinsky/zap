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
