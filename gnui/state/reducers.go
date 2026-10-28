package state

import (
	"time"

	"zapfast/model"
)

// The state package logic's reducers keep (package logic changes it;
// screens read it and call logic's functions).

// Forward is the forwarding dialog's state (zf:src/app.rs:6717-6788):
// the chosen recipients, the limit the core reported for the messages,
// and whether several recipients are being confirmed.
type Forward struct {
	Recipients model.IDList // chat JIDs, in the order they were picked
	Limit      int          // how many chats the messages may go to
	Reviewing  bool
}

// Voice is the run of voice messages playing on into the next unheard one
// (zf:src/app.rs:8936-9039).
type Voice struct {
	// Chat is the chat the run plays in (0: none): leaving it ends the run.
	Chat model.ChatRef
	// Want is a message being downloaded to play next, since WantAt.
	Want   model.IDList
	WantAt time.Time
	// Next is the clip to play now, the message and its file (two entries:
	// id, path): ui/conversation/av plays it and resets it.
	Next model.IDList
}

// Notice is a desktop notification logic decided to show; package sys
// posts it (zf:src/app.rs:2011-2124, zf:src/notify.rs).
type Notice struct {
	Account       string // the account's ID
	Chat, Message []byte
	Title, Body   string
	// Picture is the chat's (else the sender's) picture file, nil for none.
	Picture []byte
	Sound   SoundChoice
	// Limited: the chat notifies once, then waits ten minutes; Hidden: the
	// app lock or Settings hide who wrote and what ("New message").
	Limited, Hidden bool
}

// NoticeClear asks sys to take a chat's notifications off the desktop: the
// chat was read (its messages' only), or it was locked (Locked: its calls'
// too, and the ringtone of its ringing call), or all of them (All).
type NoticeClear struct {
	Account string
	Chat    []byte
	// Locked widens a chat's clear to everything that names it: the chat
	// became private.
	Locked bool
	// All takes every notification ZapFast showed off the desktop (the app
	// locked); Account and Chat are empty then.
	All bool
}
