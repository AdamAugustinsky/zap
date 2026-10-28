package calls

import (
	"time"
	"unicode"
	"unicode/utf8"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
)

// The words of a call, from its phase and outcome and nothing else
// (zf:src/ui/call.rs:373-476, zf:src/ui/calls.rs:51-124). Every function
// appends to dst and returns it, so that a frame formats into a buffer the
// screen reuses.

// live is whether a call in phase p is up or ringing.
func live(p abi.CallPhase) bool { return p <= abi.CallPhaseActive }

// connected is whether mute, the camera and the device pickers apply.
func connected(p abi.CallPhase) bool {
	return p == abi.CallPhaseConnecting || p == abi.CallPhaseAccepted || p == abi.CallPhaseActive
}

// appendStatus appends the line under the peer's name: the phase's word,
// the length of a call that is up (secs, when known), or how it ended.
func appendStatus(dst []byte, u *abi.CallUpdate, secs int64, known bool, caller []byte) []byte {
	switch u.Phase {
	case abi.CallPhasePlacing:
		return append(dst, i18n.T("Connecting…")...)
	case abi.CallPhaseDialing:
		return append(dst, i18n.T("Calling…")...)
	case abi.CallPhaseRinging:
		return append(dst, i18n.T("Ringing…")...)
	case abi.CallPhaseConnecting, abi.CallPhaseAccepted:
		return append(dst, i18n.T("Connecting…")...)
	case abi.CallPhaseActive:
		if !known {
			return append(dst, i18n.T("Connecting…")...)
		}
		return appendDuration(dst, secs)
	case abi.CallPhaseEnded, abi.CallPhaseFailed:
		if !u.HasOutcome {
			return append(dst, i18n.T("Call ended")...)
		}
		return append(dst, outcomeText(u.Outcome, u.Phase)...)
	}
	return appendIncoming(dst, u, caller)
}

// outcomeText is how a call that is over reads.
func outcomeText(o abi.CallStatus, p abi.CallPhase) string {
	switch o {
	case abi.CallStatusAnswered:
		return i18n.T("Call ended")
	case abi.CallStatusMissed:
		return i18n.T("Missed call")
	case abi.CallStatusDeclined:
		return i18n.T("Declined")
	case abi.CallStatusBusy:
		return i18n.T("Busy")
	case abi.CallStatusNoAnswer:
		return i18n.T("No answer")
	case abi.CallStatusConnectionLost:
		if p == abi.CallPhaseEnded {
			return i18n.T("Connection lost")
		}
	case abi.CallStatusAnsweredElsewhere:
		return i18n.T("Answered on another device")
	case abi.CallStatusDeclinedElsewhere:
		return i18n.T("Declined on another device")
	}
	return i18n.T("Could not connect")
}

// appendIncoming appends what a ringing call says it is: a one-to-one call
// its kind, a group call also who is calling (caller, nil when unknown or
// hidden by the lock).
func appendIncoming(dst []byte, u *abi.CallUpdate, caller []byte) []byte {
	switch {
	case !u.HasGroup && !u.Video:
		return append(dst, i18n.T("Incoming voice call")...)
	case !u.HasGroup:
		return append(dst, i18n.T("Incoming video call")...)
	case caller != nil && !u.Video:
		return appendFill(dst, i18n.T("Group voice call from {name}"), "{name}", caller)
	case caller != nil:
		return appendFill(dst, i18n.T("Group video call from {name}"), "{name}", caller)
	case !u.Video:
		return append(dst, i18n.T("Incoming group voice call")...)
	}
	return append(dst, i18n.T("Incoming group video call")...)
}

// peerAudioText is what the engine says about the peer's audio.
func peerAudioText(a *abi.PeerAudio) string {
	if a.Tag == abi.PeerAudioTagStalled {
		return i18n.T("No audio is arriving from this call")
	}
	if a.Silent.Reason == abi.SilenceReasonNoDecoder {
		return i18n.T("This call's audio cannot be decoded")
	}
	return i18n.T("The other side's audio is not getting through")
}

// appendLost appends the note for a device that went away.
func appendLost(dst []byte, d *abi.LostDevice) []byte {
	switch d.Kind {
	case abi.DeviceKindMicrophone:
		return appendFill(dst, i18n.T("Microphone “{name}” is not available; using the system default"), "{name}", d.Name)
	case abi.DeviceKindSpeaker:
		return appendFill(dst, i18n.T("Speaker “{name}” is not available; using the system default"), "{name}", d.Name)
	}
	return appendFill(dst, i18n.T("Camera “{name}” is not available"), "{name}", d.Name)
}

// appendDuration appends m:ss, or h:mm:ss past an hour
// (format.AppendCallDuration).
func appendDuration(dst []byte, secs int64) []byte {
	return format.AppendCallDuration(dst, uint64(max(secs, 0)))
}

// appendLength appends a finished call's length in words, "12 min 34 s"
// (format.AppendCallLength).
func appendLength(dst []byte, secs uint64) []byte {
	return format.AppendCallLength(dst, i18n.Current(), secs)
}

// appendFill appends tmpl with every ph replaced by val, as upstream's
// str::replace does.
func appendFill(dst []byte, tmpl, ph string, val []byte) []byte {
	for {
		i := index(tmpl, ph)
		if i < 0 {
			return append(dst, tmpl...)
		}
		dst = append(dst, tmpl[:i]...)
		dst = append(dst, val...)
		tmpl = tmpl[i+len(ph):]
	}
}

// entryTitle is what a logged call was: "Missed voice call", "Video call",
// "Group voice call".
func entryTitle(missed, group, video bool) string {
	switch {
	case missed && !group && !video:
		return i18n.T("Missed voice call")
	case missed && !group:
		return i18n.T("Missed video call")
	case missed && !video:
		return i18n.T("Missed group voice call")
	case missed:
		return i18n.T("Missed group video call")
	case !group && !video:
		return i18n.T("Voice call")
	case !group:
		return i18n.T("Video call")
	case !video:
		return i18n.T("Group voice call")
	}
	return i18n.T("Group video call")
}

// appendSummary appends what a logged call was and how it went on one line:
// "Video call · 12 min 34 s".
func appendSummary(dst []byte, e *entry) []byte {
	dst = append(dst, entryTitle(e.missed(), e.group, e.video)...)
	var d string
	switch e.status {
	case abi.CallStatusAnswered:
		if e.duration > 0 {
			dst = append(dst, " · "...)
			return appendLength(dst, e.duration)
		}
		return dst
	case abi.CallStatusMissed:
		return dst
	case abi.CallStatusAnsweredElsewhere:
		d = i18n.T("Answered on another device")
	case abi.CallStatusDeclined:
		d = i18n.T("Declined")
	case abi.CallStatusDeclinedElsewhere:
		d = i18n.T("Declined on another device")
	case abi.CallStatusBusy:
		d = i18n.T("Busy")
	case abi.CallStatusFailed:
		d = i18n.T("Could not connect")
	case abi.CallStatusNoAnswer:
		d = i18n.T("No answer")
	case abi.CallStatusConnectionLost:
		d = i18n.T("Connection lost")
	}
	dst = append(dst, " · "...)
	return append(dst, d...)
}

// appendStamp appends when a call was, as the chat list stamps a chat
// (format.AppendChatStamp): the time today, "Yesterday", the weekday within
// the week, else the date.
func appendStamp(dst []byte, unix int64, now time.Time) []byte {
	if unix <= 0 {
		return dst
	}
	return format.AppendChatStamp(dst, i18n.Current(), time.Unix(unix, 0), now)
}

// appendPhone appends a phone number's digits with a plus sign and its
// country's grouping (format.AppendPhone).
func appendPhone(dst, digits []byte) []byte {
	return format.AppendPhone(dst, model.S(digits))
}

// appendKey appends s folded for search: lower case, and Latin letters
// without their accents (upstream's search_key, for the common cases).
func appendKey(dst, s []byte) []byte {
	for len(s) > 0 {
		r, w := utf8.DecodeRune(s)
		s = s[w:]
		if r < utf8.RuneSelf {
			if r >= 'A' && r <= 'Z' {
				r += 'a' - 'A'
			}
			dst = append(dst, byte(r))
			continue
		}
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		dst = utf8.AppendRune(dst, unicode.ToLower(unaccent(r)))
	}
	return dst
}

// unaccent takes the accent off the precomposed Latin letters upstream's NFD
// strips.
func unaccent(r rune) rune {
	const from, to = "ÀÁÂÃÄÅàáâãäåÇçÈÉÊËèéêëÌÍÎÏìíîïÑñÒÓÔÕÖòóôõöÙÚÛÜùúûüÝýÿ", "AAAAAAaaaaaaCcEEEEeeeeIIIIiiiiNnOOOOOoooooUUUUuuuuYyy"
	i := 0
	for _, f := range from {
		if f == r {
			return rune(to[i])
		}
		i++
	}
	return r
}

func appendInt(dst []byte, v int64) []byte {
	if v < 0 {
		dst = append(dst, '-')
		v = -v
	}
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
		if v == 0 {
			break
		}
	}
	return append(dst, b[i:]...)
}

func index(s, sub string) int {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}
