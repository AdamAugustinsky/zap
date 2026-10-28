package sys

import (
	"net/url"
	"strings"
)

// Links: the chat a launch argument or an opened URL names, and the text it
// asks to start with. Ported from zf:src/target.rs and the invite and call
// link parsers of zf:src/safety.rs.
//
// ZapFast is asked to open a chat by a number on its own, a wa.me link, the
// whatsapp: or wa: URI (a URL type of the bundle), or api.whatsapp.com's
// send link. All name the same chat, filed under the number's JID, and any
// may carry a text= template to start the conversation with.

const (
	// phoneServer is where a one-to-one chat is filed. A chat behind a
	// privacy id keeps that id in the archive until the mapping is known, so
	// a link opens the phone-number chat, which the archive merges once
	// WhatsApp answers.
	phoneServer = "s.whatsapp.net"
	// minDigits is the fewest digits a number may have. E.164 allows up to
	// fifteen; the lower bound keeps a year, a short code or a stray digit
	// out of a chat.
	minDigits = 6
	maxDigits = 15
)

// chatLink is what a chat link asks for.
type chatLink struct {
	// chat is the chat to open, as <digits>@s.whatsapp.net.
	chat string
	// text is the template to start the conversation with, from text=; ""
	// when the link carries none or only blanks.
	text string
}

// parseChat reads the chat target names: a number on its own in any
// formatting, a wa.me or api.whatsapp.com/send?phone= link, or the
// whatsapp:// (or wa://) URI, with an optional text= template on any of
// them. Anything that does not carry a number where one of those keeps it is
// refused, so an argument that is not a chat opens nothing rather than the
// wrong one.
func parseChat(target string) (chatLink, bool) {
	t := strings.TrimSpace(target)
	if t == "" {
		return chatLink{}, false
	}
	_, query := splitQuery(t)
	digits, ok := numberOf(t)
	if !ok {
		return chatLink{}, false
	}
	l := chatLink{chat: digits + "@" + phoneServer}
	if v, ok := parameter(query, "text"); ok {
		if text := decodeEscapes(v); strings.TrimSpace(text) != "" {
			l.text = text
		}
	}
	return l, true
}

// numberOf is the number target names, read where its own shape keeps it:
// a wa.me link keeps it in the path, the web send link and the whatsapp: URI
// keep it in the query, and a number on its own is the whole argument. A
// link of any other shape names no chat, however its query is spelled.
func numberOf(target string) (string, bool) {
	head, query := splitQuery(target)
	lower := strings.ToLower(head)
	for _, scheme := range [...]string{"whatsapp:", "whatsapp-send:", "wa:"} {
		if strings.HasPrefix(lower, scheme) {
			v, ok := parameter(query, "phone")
			if !ok {
				return "", false
			}
			return numberIn(v)
		}
	}
	if host, path, ok := httpHostPath(head); ok {
		switch strings.ToLower(host) {
		case "wa.me", "www.wa.me":
			if seg := lastSegment(path); seg != "" {
				if n, ok := numberIn(seg); ok {
					return n, true
				}
			}
			if v, ok := parameter(query, "phone"); ok {
				return numberIn(v)
			}
		case "api.whatsapp.com", "web.whatsapp.com":
			if v, ok := parameter(query, "phone"); ok {
				return numberIn(v)
			}
		}
		return "", false
	}
	// A number on its own, or a wa.me link typed without its scheme.
	bare := head
	if strings.HasPrefix(lower, "wa.me/") {
		bare = head[len("wa.me/"):]
	}
	return numberIn(bare)
}

// splitQuery splits a target at its query, so a query never lends its digits
// to a number that lives in the path. A fragment is neither path nor query
// and is dropped first, so wa.me/20123456789#section names its chat and a #
// after a template does not reach the composer. A # inside a template is
// written %23, as a link must write it.
func splitQuery(target string) (head, query string) {
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	head, query, _ = strings.Cut(target, "?")
	return head, query
}

// httpHostPath is the host and path of an http(s) link, the scheme off.
func httpHostPath(target string) (host, path string, ok bool) {
	rest, ok := strings.CutPrefix(target, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(target, "http://")
	}
	if !ok {
		return "", "", false
	}
	host, path, _ = strings.Cut(rest, "/")
	return host, path, true
}

// lastSegment is the last segment of a path, which is where a wa.me link
// keeps its number.
func lastSegment(path string) string {
	path = strings.TrimRight(path, "/")
	return path[strings.LastIndexByte(path, '/')+1:]
}

// numberIn is the digits of the number written in text, when every other
// character is formatting a phone number may wear. A letter, a slash or
// anything else is refused rather than dropped, so "call 20123456789" names
// no chat instead of one nobody asked for.
func numberIn(text string) (string, bool) {
	text = decodeEscapes(text)
	var b strings.Builder
	for _, r := range text {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' || r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
		default:
			return "", false
		}
	}
	d := b.String()
	if len(d) < minDigits || len(d) > maxDigits {
		return "", false
	}
	return d, true
}

// parameter is one parameter of a query, still escaped.
func parameter(query, key string) (string, bool) {
	for query != "" {
		var pair string
		pair, query, _ = strings.Cut(query, "&")
		if name, v, ok := strings.Cut(pair, "="); ok && name == key {
			return v, true
		}
	}
	return "", false
}

// decodeEscapes percent-decodes one query value. A + stays a plus rather
// than becoming a space: a number writes it as %2B, so this query is
// escaped, not form-encoded. An escape that is not two hex digits is kept as
// written, so a malformed link still opens its chat and still carries
// whatever template it could.
func decodeEscapes(v string) string {
	if strings.IndexByte(v, '%') < 0 {
		return v
	}
	out := make([]byte, 0, len(v))
	for i := 0; i < len(v); i++ {
		if v[i] == '%' && i+2 < len(v) {
			if h, ok := hexDigit(v[i+1]); ok {
				if l, ok := hexDigit(v[i+2]); ok {
					out = append(out, h<<4|l)
					i += 2
					continue
				}
			}
		}
		out = append(out, v[i])
	}
	return strings.ToValidUTF8(string(out), "\uFFFD")
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// numberOfJID is the digits of the phone number id names, when id is one.
func numberOfJID(id string) (string, bool) {
	user, server, ok := strings.Cut(id, "@")
	if !ok || server != phoneServer || len(user) < minDigits || len(user) > maxDigits {
		return "", false
	}
	for i := 0; i < len(user); i++ {
		if user[i] < '0' || user[i] > '9' {
			return "", false
		}
	}
	return user, true
}

// inviteCode is the code of a WhatsApp group invite link such as
// https://chat.whatsapp.com/AbCd123, which ZapFast opens itself
// (zf:src/safety.rs group_invite_code).
func inviteCode(value string) (string, bool) {
	segs, ok := hostSegments(value, "chat.whatsapp.com")
	if !ok || len(segs) == 0 {
		return "", false
	}
	code := segs[0]
	if code == "invite" {
		segs = segs[1:]
		if len(segs) == 0 {
			return "", false
		}
		code = segs[0]
	}
	if len(segs) != 1 || len(code) < 10 || len(code) > 40 || !alnum(code, false) {
		return "", false
	}
	return code, true
}

// callLink is the token of a WhatsApp call link such as
// https://call.whatsapp.com/voice/AbCd123, and whether it is a video call's
// (/video/) rather than a voice call's (/voice/, or /audio/ as
// whatsapp-rust writes it); ZapFast joins these itself.
func callLink(value string) (token string, video, ok bool) {
	segs, ok := hostSegments(value, "call.whatsapp.com")
	if !ok || len(segs) != 2 {
		return "", false, false
	}
	switch segs[0] {
	case "voice", "audio":
	case "video":
		video = true
	default:
		return "", false, false
	}
	token = segs[1]
	if len(token) < 8 || len(token) > 64 || !alnum(token, true) {
		return "", false, false
	}
	return token, video, true
}

// hostSegments is the non-empty path segments of an http(s) URL on host.
func hostSegments(value, host string) ([]string, bool) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Hostname(), host) {
		return nil, false
	}
	var segs []string
	for _, s := range strings.Split(u.EscapedPath(), "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	return segs, true
}

func alnum(s string, dashes bool) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || dashes && (c == '-' || c == '_') {
			continue
		}
		return false
	}
	return true
}

// linkKind is what a link or argument asks for.
type linkKind uint8

const (
	linkChat   linkKind = iota + 1 // open a chat, with a template for the composer
	linkInvite                     // show a group invite
	linkCall                       // join a call link
)

// link is a parsed link.
type link struct {
	kind  linkKind
	chat  string // linkChat: <digits>@s.whatsapp.net
	text  string // linkChat: the template
	code  string // linkInvite: the invite code
	token string // linkCall: the call link's token
	video bool   // linkCall
}

// parseLink reads an argument or URL: a chat, a group invite or a call link.
func parseLink(arg string) (link, bool) {
	if c, ok := parseChat(arg); ok {
		return link{kind: linkChat, chat: c.chat, text: c.text}, true
	}
	if code, ok := inviteCode(arg); ok {
		return link{kind: linkInvite, code: code}, true
	}
	if token, video, ok := callLink(arg); ok {
		return link{kind: linkCall, token: token, video: video}, true
	}
	return link{}, false
}
