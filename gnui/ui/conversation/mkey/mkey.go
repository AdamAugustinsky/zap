// Package mkey names a message for the caches of the transcript's packages
// (kinds, av, the album runs): by the account and the chat it is in as
// well as by its id, since a group's message has the same id in every
// account that is in it, and a chat's id can repeat across accounts. A
// cache keyed by the id alone would serve one account's card, size or
// waveform for another's message.
package mkey

import (
	"zapfast/model"
	"zapfast/state"
)

// K identifies one message of one chat of one account. It is a value, as
// comparable as it is cheap: a cache keeps it in its slots and compares
// them whole, and no key makes a string.
type K struct {
	Acct uint64        // the account's Serial, 0 for none
	Chat model.ChatRef // the chat open in it
	ID   uint64        // a hash of the message's id
}

// Hash is a hash of a message id (FNV-1a).
func Hash(id []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range id {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// Of is the key of the message with id in the chat that a has open: the
// transcript draws that chat's messages only.
func Of(a *state.Account, id []byte) K {
	if a == nil {
		return K{ID: Hash(id)}
	}
	return K{Acct: a.Serial, Chat: a.Open, ID: Hash(id)}
}

// In is the key of the message with id in the chat chat of account a.
func In(a *state.Account, chat model.ChatRef, id []byte) K {
	if a == nil {
		return K{Chat: chat, ID: Hash(id)}
	}
	return K{Acct: a.Serial, Chat: chat, ID: Hash(id)}
}

// Slot is where a direct-mapped cache of n slots keeps k.
func (k K) Slot(n int) int {
	h := k.ID ^ k.Acct*0x9e3779b97f4a7c15 ^ uint64(k.Chat)*0xc2b2ae3d27d4eb4f
	return int((h ^ h>>29) % uint64(n))
}

// here is the account the transcript is being built for (Enter), for the
// callers that are given a message and nothing else (kinds.Has).
var here *state.Account

// Enter says which account the frame's transcript is built for. The
// screen calls it before it builds, sizes or asks anything of a message.
func Enter(a *state.Account) { here = a }

// Here is the key of the message with id in the transcript being built.
func Here(id []byte) K { return Of(here, id) }
