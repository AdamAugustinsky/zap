package format

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gonativeui/app/locale"

	"zapfast/i18n"
	"zapfast/internal/testbin"
)

// Hour12 reports whether times read on a 12-hour clock ("2:05 PM") rather
// than a 24-hour one ("14:05"): the system's setting, as upstream's
// twelve_hour_clock reads it (zf:src/util.rs:63), which app/locale reads
// afresh when the system says it changed. In a test binary it is the
// 24-hour clock, whatever the machine's setting, unless the test sets Hour12
// (upstream's tests fix the clock the same way).
var Hour12 = systemHour12

func systemHour12() bool { return !testbin.Is() && localeHour12() }

// localeHour12 is the system's clock: settings that could not be read at
// all (no language, no region) leave the 24-hour clock, as upstream's
// readers do.
func localeHour12() bool {
	info := locale.Current()
	return (len(info.Languages) > 0 || info.Region != "") && !info.Hour24
}

// AppendTimeOfDay appends an hour (0-23) and minute as "14:05", or as
// "2:05 PM" on a 12-hour clock (zf:src/util.rs time_of_day).
func AppendTimeOfDay(dst []byte, hour, minute int, twelve bool) []byte {
	if !twelve {
		return append2(append(append2(dst, hour), ':'), minute)
	}
	meridiem := " AM"
	switch {
	case hour == 0:
		hour = 12
	case hour == 12:
		meridiem = " PM"
	case hour > 12:
		hour, meridiem = hour-12, " PM"
	}
	dst = append(appendInt(dst, hour), ':')
	return append(append2(dst, minute), meridiem...)
}

// AppendClock appends t's time of day on the clock Hour12 says
// (zf:src/util.rs clock): "14:05" or "2:05 PM".
func AppendClock(dst []byte, t time.Time) []byte {
	return AppendTimeOfDay(dst, t.Hour(), t.Minute(), Hour12())
}

// AppendCopyStamp appends WhatsApp's transcript stamp, always on a 24-hour
// clock with the date month first: "22:41, 8/18/2026" (zf:src/util.rs
// copy_stamp).
func AppendCopyStamp(dst []byte, t time.Time) []byte {
	y, m, d := t.Date()
	dst = append(appendInt(dst, t.Hour()), ':')
	dst = append(append2(dst, t.Minute()), ", "...)
	dst = append(appendInt(dst, int(m)), '/')
	dst = append(appendInt(dst, d), '/')
	return appendInt(dst, y)
}

// DaysSince is how many calendar days t's date lies before now's, both in
// now's time zone: 0 today, 1 yesterday, negative for a later day.
func DaysSince(t, now time.Time) int {
	return dayNumber(now) - dayNumber(t.In(now.Location()))
}

func dayNumber(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// AppendChatStamp appends a chat row's stamp (zf:src/util.rs chat_stamp):
// the time today, "Yesterday", the weekday within the week, else the date.
func AppendChatStamp(dst []byte, l i18n.Locale, t, now time.Time) []byte {
	t = t.In(now.Location())
	switch days := DaysSince(t, now); {
	case days == 0:
		return AppendClock(dst, t)
	case days == 1:
		return append(dst, i18n.TIn(l, "Yesterday")...)
	case days >= 2 && days <= 6:
		return append(dst, WeekdayName(l, t.Weekday())...)
	}
	return AppendShortDate(dst, l, t)
}

// AppendMomentStamp appends a stamp with the day and the minute, for message
// info and poll votes (zf:src/util.rs moment_stamp): "14:05" today,
// "Yesterday at 14:05", "Monday at 14:05", else "23 Sep 2026 at 14:05".
func AppendMomentStamp(dst []byte, l i18n.Locale, t, now time.Time) []byte {
	t = t.In(now.Location())
	switch days := DaysSince(t, now); {
	case days == 0:
		return AppendClock(dst, t)
	case days == 1:
		return fill(dst, i18n.TIn(l, "Yesterday at {time}"), l, t, false)
	case days >= 2 && days <= 6:
		return fill(dst, i18n.TIn(l, "{weekday} at {time}"), l, t, false)
	}
	return fill(dst, i18n.TIn(l, "{date} at {time}"), l, t, false)
}

// AppendLastSeen appends a contact's last-seen line as WhatsApp words it
// (zf:src/util.rs last_seen): "last seen today at 14:05", "last seen
// yesterday at 14:05", the weekday within the week (lower case mid-sentence,
// except in English and German), else only the date.
func AppendLastSeen(dst []byte, l i18n.Locale, t, now time.Time) []byte {
	t = t.In(now.Location())
	switch days := DaysSince(t, now); {
	case days <= 0: // a clock slightly ahead of ours still means today
		return fill(dst, i18n.TIn(l, "last seen today at {time}"), l, t, false)
	case days == 1:
		return fill(dst, i18n.TIn(l, "last seen yesterday at {time}"), l, t, false)
	case days <= 6:
		return fill(dst, i18n.TIn(l, "last seen {weekday} at {time}"), l, t, l != i18n.English && l != i18n.German)
	}
	return fill(dst, i18n.TIn(l, "last seen {date}"), l, t, false)
}

// AppendDayLabel appends a conversation's day separator (zf:src/util.rs
// day_label): "Today", "Yesterday", the weekday within the week, else the
// long date.
func AppendDayLabel(dst []byte, l i18n.Locale, t, now time.Time) []byte {
	t = t.In(now.Location())
	switch days := DaysSince(t, now); {
	case days == 0:
		return append(dst, i18n.TIn(l, "Today")...)
	case days == 1:
		return append(dst, i18n.TIn(l, "Yesterday")...)
	case days >= 2 && days <= 6:
		return append(dst, WeekdayName(l, t.Weekday())...)
	}
	return AppendLongDate(dst, l, t)
}

// AppendShortDate appends a date as "23 Sep 2026": the month's first three
// characters (zf:src/util.rs short_date).
func AppendShortDate(dst []byte, l i18n.Locale, t time.Time) []byte {
	y, m, d := t.Date()
	dst = append(appendInt(dst, d), ' ')
	dst = append(appendChars(dst, MonthName(l, m), 3), ' ')
	return appendInt(dst, y)
}

// AppendLongDate appends a date with its weekday, as "Wednesday, 23
// September 2026" (zf:src/util.rs long_date).
func AppendLongDate(dst []byte, l i18n.Locale, t time.Time) []byte {
	y, m, d := t.Date()
	dst = append(append(dst, WeekdayName(l, t.Weekday())...), ", "...)
	dst = append(appendInt(dst, d), ' ')
	dst = append(append(dst, MonthName(l, m)...), ' ')
	return appendInt(dst, y)
}

// AppendMonthHeading appends a month's name and year, for the day filter's
// header: "September 2026" (zf:src/util.rs month_heading).
func AppendMonthHeading(dst []byte, l i18n.Locale, t time.Time) []byte {
	return appendInt(append(append(dst, MonthName(l, t.Month())...), ' '), t.Year())
}

// WeekdayHeadings are the two-letter weekday headings of the day filter's
// grid, Monday first, taken by character (zf:src/util.rs weekday_headings).
func WeekdayHeadings(l i18n.Locale) [7]string {
	var out [7]string
	for i := range out {
		out[i] = string(appendChars(nil, WeekdayName(l, time.Weekday((i+1)%7)), 2))
	}
	return out
}

// DayBounds is the half-open range of Unix seconds a local calendar day
// covers in loc (zf:src/util.rs day_bounds). A day starts at its first
// instant, so one whose midnight a clock change skips starts when the clock
// resumes, and a day is 23 or 25 hours long around a change.
func DayBounds(year int, month time.Month, day int, loc *time.Location) (from, until int64) {
	return startOfDay(year, month, day, loc), startOfDay(year, month, day+1, loc)
}

func startOfDay(year int, month time.Month, day int, loc *time.Location) int64 {
	t := time.Date(year, month, day, 0, 0, 0, 0, loc)
	y, m, d := t.Date()
	want := time.Date(year, month, day, 0, 0, 0, 0, time.UTC) // normalised: day+1 may be the 1st
	switch {
	case y != want.Year() || m != want.Month() || d != want.Day():
		// Midnight does not exist and the zone took the day before: the day
		// starts where that zone ends.
		_, end := t.ZoneBounds()
		return end.Unix()
	case t.Hour() != 0 || t.Minute() != 0:
		// Midnight does not exist and the zone took the clock after it: the
		// day starts where this zone begins.
		start, _ := t.ZoneBounds()
		return start.Unix()
	}
	return t.Unix()
}

// WeekdayName is the weekday's name in l.
func WeekdayName(l i18n.Locale, w time.Weekday) string {
	// Each literal sits in its own call, as upstream has them for xgettext.
	switch w {
	case time.Monday:
		return i18n.TIn(l, "Monday")
	case time.Tuesday:
		return i18n.TIn(l, "Tuesday")
	case time.Wednesday:
		return i18n.TIn(l, "Wednesday")
	case time.Thursday:
		return i18n.TIn(l, "Thursday")
	case time.Friday:
		return i18n.TIn(l, "Friday")
	case time.Saturday:
		return i18n.TIn(l, "Saturday")
	}
	return i18n.TIn(l, "Sunday")
}

// MonthName is the month's name in l.
func MonthName(l i18n.Locale, m time.Month) string {
	switch m {
	case time.January:
		return i18n.TIn(l, "January")
	case time.February:
		return i18n.TIn(l, "February")
	case time.March:
		return i18n.TIn(l, "March")
	case time.April:
		return i18n.TIn(l, "April")
	case time.May:
		return i18n.TIn(l, "May")
	case time.June:
		return i18n.TIn(l, "June")
	case time.July:
		return i18n.TIn(l, "July")
	case time.August:
		return i18n.TIn(l, "August")
	case time.September:
		return i18n.TIn(l, "September")
	case time.October:
		return i18n.TIn(l, "October")
	case time.November:
		return i18n.TIn(l, "November")
	}
	return i18n.TIn(l, "December")
}

// AppendDuration appends a media length as "0:12" (zf:src/util.rs
// duration).
func AppendDuration(dst []byte, seconds uint32) []byte {
	return append2(append(appendInt(dst, int(seconds/60)), ':'), int(seconds%60))
}

// AppendCallDuration appends a running call's clock: "m:ss", or "h:mm:ss"
// past an hour (zf:src/util.rs call_duration).
func AppendCallDuration(dst []byte, seconds uint64) []byte {
	h, m, s := int(seconds/3600), int(seconds%3600/60), int(seconds%60)
	if h > 0 {
		dst = append(append2(append(appendInt(dst, h), ':'), m), ':')
	} else {
		dst = append(appendInt(dst, m), ':')
	}
	return append2(dst, s)
}

// AppendCallLength appends a finished call's length in words, as WhatsApp's
// call info gives it (zf:src/util.rs call_length): "45 s" under a minute,
// "12 min 34 s" under an hour, "1 h 2 min" past one, leaving out a part that
// is zero. Each form is a message of its own, so a language orders and
// abbreviates the units its own way.
func AppendCallLength(dst []byte, l i18n.Locale, seconds uint64) []byte {
	h, m, s := int(seconds/3600), int(seconds%3600/60), int(seconds%60)
	var tmpl string
	switch {
	case h == 0 && m == 0:
		tmpl = i18n.TIn(l, "{seconds} s")
	case h == 0 && s == 0:
		tmpl = i18n.TIn(l, "{minutes} min")
	case h == 0:
		tmpl = i18n.TIn(l, "{minutes} min {seconds} s")
	case m == 0:
		tmpl = i18n.TIn(l, "{hours} h")
	default:
		tmpl = i18n.TIn(l, "{hours} h {minutes} min")
	}
	for {
		i := strings.IndexByte(tmpl, '{')
		if i < 0 {
			return append(dst, tmpl...)
		}
		dst, tmpl = append(dst, tmpl[:i]...), tmpl[i:]
		switch {
		case strings.HasPrefix(tmpl, "{hours}"):
			dst, tmpl = appendInt(dst, h), tmpl[len("{hours}"):]
		case strings.HasPrefix(tmpl, "{minutes}"):
			dst, tmpl = appendInt(dst, m), tmpl[len("{minutes}"):]
		case strings.HasPrefix(tmpl, "{seconds}"):
			dst, tmpl = appendInt(dst, s), tmpl[len("{seconds}"):]
		default:
			dst, tmpl = append(dst, '{'), tmpl[1:]
		}
	}
}

// fill appends a translated template with its {time}, {weekday} and {date}
// for t (upstream's chained str::replace); lower puts the weekday in lower
// case.
func fill(dst []byte, tmpl string, l i18n.Locale, t time.Time, lower bool) []byte {
	for {
		i := strings.IndexByte(tmpl, '{')
		if i < 0 {
			return append(dst, tmpl...)
		}
		dst, tmpl = append(dst, tmpl[:i]...), tmpl[i:]
		switch {
		case strings.HasPrefix(tmpl, "{time}"):
			dst, tmpl = AppendClock(dst, t), tmpl[len("{time}"):]
		case strings.HasPrefix(tmpl, "{weekday}"):
			w := WeekdayName(l, t.Weekday())
			if lower {
				dst = appendLower(dst, w)
			} else {
				dst = append(dst, w...)
			}
			tmpl = tmpl[len("{weekday}"):]
		case strings.HasPrefix(tmpl, "{date}"):
			dst, tmpl = AppendShortDate(dst, l, t), tmpl[len("{date}"):]
		default:
			dst, tmpl = append(dst, '{'), tmpl[1:]
		}
	}
}

// appendChars appends s's first n characters.
func appendChars(dst []byte, s string, n int) []byte {
	for i := range s {
		if n == 0 {
			return append(dst, s[:i]...)
		}
		n--
	}
	return append(dst, s...)
}

func appendLower(dst []byte, s string) []byte {
	for _, r := range s {
		dst = utf8.AppendRune(dst, unicode.ToLower(r))
	}
	return dst
}

// append2 appends n (0-99) in two digits.
func append2(dst []byte, n int) []byte {
	return append(dst, byte('0'+n/10%10), byte('0'+n%10))
}

func appendInt(dst []byte, n int) []byte {
	if n < 0 {
		dst = append(dst, '-')
		n = -n
	}
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return append(dst, b[i:]...)
}
