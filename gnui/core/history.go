package core

import (
	"context"

	"zapfast/abi"
)

// History is a core that reads a chat's stored messages for something other
// than the screens: the agents' door (package agent), for a chat the owner
// gave AI access. Nothing is emitted, so the UI learns nothing of the read,
// and the messages are the caller's, without their thumbnails. A locked
// chat is refused. Both calls may block, and any goroutine may make them.
type History interface {
	// History reads at most limit of chat's messages, oldest first: the
	// newest, or those before the message `before`. more: earlier ones are
	// stored.
	History(ctx context.Context, chat, before string, limit int) (page []abi.Message, more bool, err error)
	// SearchHistory finds at most limit of chat's messages matching query,
	// newest first.
	SearchHistory(ctx context.Context, chat, query string, limit int) ([]abi.Message, error)
}

// ErrNoHistory is History's refusal: the chat is locked, unknown, or the
// account is not open.
var ErrNoHistory = &Error{Op: "history", Code: ERefused, Msg: "the chat cannot be read"}
