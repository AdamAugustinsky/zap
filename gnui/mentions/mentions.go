// Package mentions is zf:src/mentions.rs: mentioning everyone in a group.
//
// The message body keeps the visible token "@all". A localized word, such as
// "@todos", is only a way to type it: the composer rewrites that word to
// "@all" when the sender is allowed to mention everyone. Bit 1 of
// ContextInfo.non_jid_mentions is what notifies the group. The stored mention
// id is AllID, which is not a JID.
//
// Text is []byte (the composer's buffer, the message body, as in markup) and
// every function appends or answers without allocating, except where noted.
// Upstream's Option<u32> of mention bits is a uint32 with 0 for none: upstream
// never distinguishes Some(0) from None either (WithoutFlag drops a zero).
package mentions

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"zapfast/abi"
	"zapfast/i18n"
)

const (
	// AllUser is the visible token, without the "@". Case-sensitive on the wire.
	AllUser = "all"
	// AllID is the stored mention id. It has no "@", so it is never parsed as
	// a chat.
	AllID = "mention-all"
	// AllFlag is the non_jid_mentions bit that notifies everyone.
	AllFlag uint32 = 1
	// OpenLimit is the group size at and below which every member who can post
	// may mention everyone.
	OpenLimit = 32
)

// EveryoneRef is the mention row stored for @all.
func EveryoneRef() abi.MentionRef {
	return abi.MentionRef{User: []byte(AllUser), ID: []byte(AllID)}
}

// IsEveryone recognizes a stored everyone mention by its reserved id.
func IsEveryone(m *abi.MentionRef) bool { return string(m.ID) == AllID }

// NamesUs reports whether a stored mention names us, under any id we go by
// (ours: the phone number and the privacy id), or everyone.
func NamesUs(m *abi.MentionRef, ours [][]byte) bool {
	if IsEveryone(m) {
		return true
	}
	for _, id := range ours {
		if bytes.Equal(id, m.ID) {
			return true
		}
		user, _, _ := bytes.Cut(id, []byte("@"))
		if bytes.Equal(user, m.User) {
			return true
		}
	}
	return false
}

// PushEveryone appends the everyone mention once.
func PushEveryone(ms []abi.MentionRef) []abi.MentionRef {
	for i := range ms {
		if IsEveryone(&ms[i]) {
			return ms
		}
	}
	return append(ms, EveryoneRef())
}

// PushEveryoneID appends AllID once, beside real participant JIDs.
func PushEveryoneID(ids [][]byte) [][]byte {
	for _, id := range ids {
		if string(id) == AllID {
			return ids
		}
	}
	return append(ids, []byte(AllID))
}

// Flagged reports whether the context requests an everyone notification.
func Flagged(bits uint32) bool { return bits&AllFlag != 0 }

// WithFlag sets bit 1 without clearing any other bit.
func WithFlag(bits uint32) uint32 { return bits | AllFlag }

// WithoutFlag clears bit 1. A zero result is "none", as upstream drops it.
func WithoutFlag(bits uint32) uint32 { return bits &^ AllFlag }

// EveryoneAlias is the one-word alias for the interface language l. English
// is "all".
//
// A translation with a space, or an empty one, is ignored: the composer would
// otherwise rewrite ordinary words.
func EveryoneAlias(l i18n.Locale) string {
	// Translators: one word, no spaces. The word after @ that mentions everyone.
	alias := strings.TrimSpace(i18n.PIn(l, "mention", "all"))
	if alias == "" || strings.IndexFunc(alias, unicode.IsSpace) >= 0 {
		return AllUser
	}
	return alias
}

// HasEveryoneToken reports whether text contains the wire token "@all" on its own.
func HasEveryoneToken(text []byte) bool {
	for at := 0; at < len(text); at++ {
		if text[at] == '@' {
			if _, ok := EveryoneTokenEnd(text, at); ok {
				return true
			}
		}
	}
	return false
}

// EveryoneTokenEnd is the end byte of an "@all" token whose "@" is at at.
func EveryoneTokenEnd(text []byte, at int) (int, bool) {
	return tokenEnd(text, at, AllUser, false)
}

// RewriteAlias appends text to dst with each bounded localized alias
// rewritten to "@all", and reports whether a real "@all" token is present
// afterwards.
//
// The alias match ignores case ("@Todos" becomes "@all"). The English alias
// does not rewrite "@ALL": only the exact wire token counts. An empty alias, a
// spaced one, or "all" itself leaves the text as it is.
func RewriteAlias(dst, text []byte, alias string) ([]byte, bool) {
	alias = strings.TrimSpace(alias)
	start := len(dst)
	if alias == "" || strings.EqualFold(alias, AllUser) || strings.IndexFunc(alias, unicode.IsSpace) >= 0 {
		dst = append(dst, text...)
	} else {
		for at := 0; at < len(text); {
			if text[at] == '@' {
				if end, ok := tokenEnd(text, at, alias, true); ok {
					dst = append(dst, "@all"...)
					at = end
					continue
				}
			}
			dst = append(dst, text[at])
			at++
		}
	}
	return dst, HasEveryoneToken(dst[start:])
}

// EveryoneQueryMatches reports whether the composer query should offer
// everyone.
//
// An empty query matches, and so does a prefix of "all" or of alias.
func EveryoneQueryMatches(query, alias string) bool {
	q := strings.ToLower(strings.TrimLeft(strings.TrimSpace(query), "@"))
	return q == "" || strings.HasPrefix(AllUser, q) || strings.HasPrefix(strings.ToLower(alias), q)
}

func tokenEnd(text []byte, at int, token string, ignoreCase bool) (int, bool) {
	if at < 0 || at >= len(text) || text[at] != '@' || !boundaryBefore(text, at) {
		return 0, false
	}
	rest := text[at+1:]
	var n int
	if ignoreCase {
		var ok bool
		if n, ok = startsWithFold(rest, token); !ok {
			return 0, false
		}
	} else {
		if !bytes.HasPrefix(rest, []byte(token)) {
			return 0, false
		}
		n = len(token)
	}
	end := at + 1 + n
	return end, boundaryAfter(text, end)
}

// boundaryBefore: characters that keep an "@" from starting a mention,
// matching the wire rule: ASCII letters and digits, plus _ @ . + -.
func boundaryBefore(text []byte, at int) bool {
	if at == 0 {
		return true
	}
	prev, _ := utf8.DecodeLastRune(text[:at])
	return !isASCIIAlnum(prev) && !strings.ContainsRune("_@.+-", prev)
}

// boundaryAfter: a following letter, digit, or "_" continues the token, so
// "@alligator" and "@all_more" stay ordinary text. Other scripts count too,
// which keeps a localized word from swallowing the next character.
func boundaryAfter(text []byte, end int) bool {
	if end >= len(text) {
		return true
	}
	next, _ := utf8.DecodeRune(text[end:])
	return next != '_' && !isAlnum(next)
}

func startsWithFold(text []byte, alias string) (int, bool) {
	n := 0
	for _, want := range alias {
		if n >= len(text) {
			return 0, false
		}
		got, w := utf8.DecodeRune(text[n:])
		if got != want && unicode.ToLower(got) != unicode.ToLower(want) {
			return 0, false
		}
		n += w
	}
	return n, true
}

func isASCIIAlnum(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// isAlnum is Rust's char::is_alphanumeric.
func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Other_Alphabetic, r)
}
