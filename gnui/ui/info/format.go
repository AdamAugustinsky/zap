package info

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"zapfast/format"
	"zapfast/i18n"
)

// The text formats of zf:src/util.rs the panel shows. They allocate, so they
// run when the data changes (a rebuild), never in a frame.

// chatStamp is the chat-row stamp of a Unix time (format.AppendChatStamp).
func chatStamp(now time.Time, unix int64) string {
	return string(format.AppendChatStamp(nil, i18n.Current(), time.Unix(unix, 0), now))
}

// lastSeen is "last seen today at 14:05" and its kin (format.AppendLastSeen).
func lastSeen(now time.Time, unix int64) string {
	return string(format.AppendLastSeen(nil, i18n.Current(), time.Unix(unix, 0), now))
}

// disappearingLine is "Disappearing messages: 24 hours", "…: Off", or "" when
// the timer is not known.
func disappearingLine(seconds uint32, known bool) string {
	if !known {
		return ""
	}
	var v string
	switch seconds {
	case 0:
		v = i18n.T("Off")
	case 86400:
		v = i18n.T("24 hours")
	case 604800:
		v = i18n.T("7 days")
	case 7776000:
		v = i18n.T("90 days")
	default:
		v = strconv.FormatUint(uint64(seconds), 10) + "s"
	}
	return i18n.T("Disappearing messages") + ": " + v
}

// duration is "0:12".
func duration(seconds uint32) string { return string(format.AppendDuration(nil, seconds)) }

// bytesText is "1.2 MB".
func bytesText(size uint64) string {
	units := [...]string{"B", "KB", "MB", "GB"}
	v, u := float64(size), 0
	for v >= 1000 && u < len(units)-1 {
		v /= 1024
		u++
	}
	if u == 0 {
		return strconv.FormatUint(size, 10) + " B"
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[u]
}

// domain is the host a link opens, without a leading "www.", parsed so that
// userinfo cannot pose as the site.
func domain(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		u, err = url.Parse("https://" + raw)
	}
	if err != nil || u.Hostname() == "" {
		return raw
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// externalURL is raw if it is a link ZapFast opens in the browser (http,
// https, mailto), else "".
func externalURL(raw string) string {
	if strings.ContainsAny(raw, "\\") || strings.IndexFunc(raw, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "http", "https":
		if u.Hostname() != "" {
			return u.String()
		}
	case "mailto":
		if u.Opaque != "" || u.Path != "" {
			return u.String()
		}
	}
	return ""
}

// inviteCode is the code of a chat.whatsapp.com invite link, or "".
func inviteCode(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Hostname(), "chat.whatsapp.com") {
		return ""
	}
	code := strings.TrimPrefix(strings.Trim(u.Path, "/"), "invite/")
	if len(code) < 10 || len(code) > 40 || strings.Contains(code, "/") {
		return ""
	}
	for _, r := range code {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_') {
			return ""
		}
	}
	return code
}
