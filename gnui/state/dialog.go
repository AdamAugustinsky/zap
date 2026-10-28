package state

import "zapfast/model"

// DialogKind is the one open dialog (zf:src/model.rs:2012). Package
// ui/dialogs draws them all; ui/cropper and ui/stickermaker draw theirs.
type DialogKind uint8

const (
	DialogNone DialogKind = iota
	DialogShortcuts
	DialogAbout
	DialogConfirmUnlink
	DialogConfirmRemoveAccount   // Account: the account's index
	DialogConfirmClearMediaCache // Account
	DialogPairWithPhone
	DialogNewChat
	DialogNewContact
	DialogUnlockLockedChats
	DialogConfirmLockChat   // Chat
	DialogLabels            // the label manager
	DialogConfirmDeleteChat // Chat
	DialogConfirmClearChat  // Chat
	DialogConfirmLeaveGroup // Chat
	DialogConfirmDeleteMessage
	DialogConfirmRemoveStickerPack // Text: the pack's name, Path: its folder
	DialogConfirmDeleteSelection   // Chat, IDs, ForEveryone
	DialogForward                  // Chat, IDs: the messages
	DialogCreatePoll               // Chat
	DialogSendLocation             // Chat
	DialogPollResults              // Chat, IDs: the poll message
	DialogInteractiveList          // Chat, IDs: the message, N: the button
	DialogJoinGroup
	DialogConfirmStartOver
	DialogStickerPack
	DialogStickerMaker // ui/stickermaker
	DialogCropper      // ui/cropper: N is the staged attachment
	DialogMessageInfo  // Chat, IDs: the message (the pane, as a dialog where it has no room)
)

// Dialog is the open dialog and what it is about. Its fields are values:
// a dialog that needs more keeps it in its own package.
type Dialog struct {
	Kind DialogKind
	// Chat is the chat it concerns; IDs the messages (or one message).
	Chat model.ChatRef
	IDs  model.IDList
	// ForEveryone revokes instead of deleting our copy.
	ForEveryone bool
	// Account is an account's index; N a button or attachment.
	Account, N int
	// Text and Path carry a name and a file path where a dialog needs them.
	Text, Path []byte
}

// Open reports whether a dialog is open.
func (d *Dialog) Open() bool { return d.Kind != DialogNone }
