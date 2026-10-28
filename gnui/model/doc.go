// Package model is one account's pointer-free stores, applied from the
// core's events on the UI thread (docs/GNUI-MIGRATION-PLAN.md, "The UI").
//
// # Records and arenas
//
// Every row (a chat, a message, a contact) is a fixed record with no Go
// pointers. Its variable data lives in a byte arena: the record keeps the
// row's whole encoded abi record there (raw) and the offsets of the fields
// the lists draw. So a list reads names and previews without decoding, and a
// screen that needs any other field decodes the row on demand
// ([Chats.Decode], [Conversation.Decode]) into an abi value it reuses: every
// field the core sends is reachable, none is copied twice.
//
// # Views
//
// Accessors return values: a [Chat] or [Message] view, []byte views into the
// arenas, and Str ranges. A view and its bytes are valid until the store is
// next applied (the next [Store.Event]); never keep one across frames. Keep
// a [ChatRef] (stable for the store's life) or a message id instead.
//
// # Generations
//
// Each store counts its changes. [Store.Gen] moves on any change; the
// chats ([Chats.Gen]), the list order ([Chats.OrderGen]), each chat
// ([Chat.Gen]), each loaded conversation ([Conversation.Gen]), the
// contacts and the link status have their own. A screen that caches what it
// built from the model rebuilds when the generation it saw has moved.
//
// # Applying
//
// [Store] is a core.Handler: core.Conn.Drain(store, budget) applies events
// within the frame's budget (package state drains every account, 2 ms per
// frame in all). Apply allocates only when a store grows past its high-water
// mark.
//
// Deliverable 1 (this commit) models the link status, me, chats, messages
// and contacts. The accessors marked TODO(deliverable 2) are the API the
// other events will fill; until then they return zero values.
package model
