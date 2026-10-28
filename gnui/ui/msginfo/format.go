package msginfo

import (
	"strconv"
	"time"

	"zapfast/format"
	"zapfast/i18n"
)

// The time formats of zf:src/util.rs this pane shows, from package format.
// They allocate, so they run when the data changes, never in a frame.

// momentStamp is the message-info timestamp with date and minute: the time
// today, "Yesterday at 14:05", "Monday at 14:05", else "23 Sep 2026 at 14:05".
func momentStamp(now time.Time, unix int64) string {
	return string(format.AppendMomentStamp(nil, i18n.Current(), time.Unix(unix, 0), now))
}

// clock is "14:05" (or "2:05 PM") in now's zone.
func clock(now time.Time, unix int64) string {
	return string(format.AppendClock(nil, time.Unix(unix, 0).In(now.Location())))
}

func duration(seconds uint32) string { return string(format.AppendDuration(nil, seconds)) }

func itoa(n int) string { return strconv.Itoa(n) }
