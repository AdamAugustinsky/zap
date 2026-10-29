package state

import (
	"bytes"
	"strconv"
)

// Folder is a group of chats in the chat list, as Arc's folders: a name, an
// icon, and the chats in it, each chat in one folder at most. Folders are
// Zap's own, kept in the account's settings (AccountSettings.Folders); the
// phone never sees them, as it never sees the local labels.
type Folder struct {
	ID        string   // unique in the account, kept when renamed
	Name      string   // as the user gave it
	Icon      string   // an SF Symbol (FolderIcons)
	Chats     [][]byte // the chats in it (JIDs), in the order they were put
	Collapsed bool     // its chats are hidden under its row
	Space     bool     // it is a space of its own (Spaces: ⌃1–9, the swipe)
}

// FolderIcons are the symbols a folder can take, the first the default.
var FolderIcons = []string{
	"folder", "briefcase", "house", "person.2", "heart", "star", "bag", "cart",
	"airplane", "book", "graduationcap", "gamecontroller", "music.note", "camera",
	"hammer", "chart.bar", "leaf", "pawprint", "globe", "flame", "sparkles", "tag",
	"bubble.left.and.bubble.right", "bell",
}

// FolderOf is the index in Settings.Folders of the folder chat is in, or -1.
func (a *Account) FolderOf(chat []byte) int {
	for i := range a.Settings.Folders {
		for _, c := range a.Settings.Folders[i].Chats {
			if bytes.Equal(c, chat) {
				return i
			}
		}
	}
	return -1
}

// FolderByID is the index of the folder with id, or -1.
func (a *Account) FolderByID(id string) int {
	for i := range a.Settings.Folders {
		if a.Settings.Folders[i].ID == id {
			return i
		}
	}
	return -1
}

// NewFolder adds a folder named name with icon, after the others, and
// returns its ID.
func (a *Account) NewFolder(name, icon string) string {
	n := 1
	for {
		id := "f" + strconv.Itoa(n)
		if a.FolderByID(id) < 0 {
			if icon == "" {
				icon = FolderIcons[0]
			}
			a.Settings.Folders = append(a.Settings.Folders, Folder{ID: id, Name: name, Icon: icon})
			a.SettingsChanged()
			return id
		}
		n++
	}
}

// MoveToFolder puts chat in folder i, out of the one it was in; i < 0 takes
// it out of every folder.
func (a *Account) MoveToFolder(chat []byte, i int) {
	if from := a.FolderOf(chat); from >= 0 {
		f := &a.Settings.Folders[from]
		for j, c := range f.Chats {
			if bytes.Equal(c, chat) {
				f.Chats = append(f.Chats[:j], f.Chats[j+1:]...)
				break
			}
		}
	}
	if i >= 0 && i < len(a.Settings.Folders) {
		f := &a.Settings.Folders[i]
		f.Chats = append(f.Chats, append([]byte(nil), chat...))
	}
	a.SettingsChanged()
}

// EditFolder runs edit on folder i and saves it.
func (a *Account) EditFolder(i int, edit func(*Folder)) {
	if i < 0 || i >= len(a.Settings.Folders) {
		return
	}
	edit(&a.Settings.Folders[i])
	a.SettingsChanged()
}

// DeleteFolder removes folder i; its chats go back to the list. A space it
// was leaves with it.
func (a *Account) DeleteFolder(i int) {
	if i < 0 || i >= len(a.Settings.Folders) {
		return
	}
	if a.SpaceFolder == a.Settings.Folders[i].ID {
		a.SpaceFolder = ""
	}
	a.Settings.Folders = append(a.Settings.Folders[:i], a.Settings.Folders[i+1:]...)
	a.SettingsChanged()
}

// InSpace reports whether chat belongs to the account's space: every chat
// when the space is the whole account, the folder's when it is a folder.
func (a *Account) InSpace(chat []byte) bool {
	if a.SpaceFolder == "" {
		return true
	}
	i := a.FolderByID(a.SpaceFolder)
	return i >= 0 && a.FolderOf(chat) == i
}

// Space is one of the window's spaces, as Arc's: an account, or one of its
// folders shown alone (Folder.Space).
type Space struct {
	Acct   int    // its account's index in State.Accounts
	Folder string // the folder's ID; "" for the whole account
}

// Spaces are the window's spaces in order: each account, then its folders
// that are spaces. ⌃1–9 are the first nine.
func (s *State) Spaces(dst []Space) []Space {
	dst = dst[:0]
	for i, a := range s.Accounts {
		if !a.Model.Linked() && len(s.Accounts) > 1 {
			continue
		}
		dst = append(dst, Space{Acct: i})
		for _, f := range a.Settings.Folders {
			if f.Space {
				dst = append(dst, Space{Acct: i, Folder: f.ID})
			}
		}
	}
	return dst
}

// SpaceAt is the space on screen and its place in Spaces (-1: none).
func (s *State) SpaceAt(spaces []Space) (Space, int) {
	a := s.Acct()
	if a == nil {
		return Space{}, -1
	}
	cur := Space{Acct: s.Active, Folder: a.SpaceFolder}
	for i, sp := range spaces {
		if sp == cur {
			return cur, i
		}
	}
	return cur, -1
}

// GoToSpace puts space sp on screen: its account, and within it the whole
// list or the folder's chats. The chat open stays when it belongs to it.
func (s *State) GoToSpace(sp Space) {
	if sp.Acct < 0 || sp.Acct >= len(s.Accounts) {
		return
	}
	if sp.Acct != s.Active {
		s.SwitchAccount(sp.Acct)
	}
	a := s.Accounts[sp.Acct]
	if a.SpaceFolder != sp.Folder {
		a.SpaceFolder = sp.Folder
		if c, ok := a.OpenChatView(); ok && !a.InSpace(c.ID()) {
			a.OpenChat(0)
		}
	}
	s.Page = PageChats
	a.SetMode(ListChats)
	s.SpaceGen++
	s.Touch()
}
