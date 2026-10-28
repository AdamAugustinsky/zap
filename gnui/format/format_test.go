package format

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the zones below, wherever the tests run

	"zapfast/i18n"
)

// clock fixes the 12- or 24-hour clock for the test.
func clock(t *testing.T, twelve bool) {
	old := Hour12
	Hour12 = func() bool { return twelve }
	t.Cleanup(func() { Hour12 = old })
}

func str(b []byte) string { return string(b) }

// ---- phone numbers (zf:src/util.rs phone_numbers_are_grouped) ----

func TestPhoneNumbersAreGrouped(t *testing.T) {
	for in, want := range map[string]string{
		"393331234567":        "+39 333 123 4567",
		"15551234567":         "+1 555 123 4567",
		"79161234567":         "+7 916 123 4567",
		"584241234567":        "+58 424 123 4567",
		"2348031234567":       "+234 803 123 4567",
		"97150123456":         "+971 501 234 56",
		"5511999999999":       "+55 (11) 99999-9999",
		"551140028922":        "+55 (11) 4002-8922",
		"551149508333":        "+55 (11) 4950-8333",
		"+55 (11) 99999-9999": "+55 (11) 99999-9999",
		"":                    "",
		// from ui/accounts' tests of its former copy
		"15550000000":       "+1 555 000 0000",
		"5511912345678":     "+55 (11) 91234-5678",
		"551133334444":      "+55 (11) 3333-4444",
		"447911123456":      "+44 791 112 3456",
		"351912345678":      "+351 912 345 678",
		"+1 (555) 000-0000": "+1 555 000 0000",
		"7":                 "+7",
		// ours: short, odd and longer than the stack buffer
		"5":                                     "+5",
		"55":                                    "+55",
		"5511":                                  "+55 11",
		"4412":                                  "+44 12",
		"1":                                     "+1",
		"abc":                                   "",
		"1234567890123456789012345678901234567": "+1 234 567 890 123 456 789 012 345 678 901 234 567",
	} {
		if got := Phone(in); got != want {
			t.Errorf("Phone(%q) = %q, want %q", in, got, want)
		}
	}
	if got := str(AppendPhone([]byte("tel: "), "15551234567")); got != "tel: +1 555 123 4567" {
		t.Errorf("appended %q", got)
	}
}

// ---- the clock (zf:src/util.rs time_of_day_switches_between_24_and_12_hour) ----

func TestTimeOfDaySwitchesBetween24And12Hour(t *testing.T) {
	for _, c := range []struct {
		h, m   int
		twelve bool
		want   string
	}{
		{0, 5, false, "00:05"},
		{14, 5, false, "14:05"},
		{0, 5, true, "12:05 AM"},
		{9, 5, true, "9:05 AM"},
		{12, 0, true, "12:00 PM"},
		{14, 5, true, "2:05 PM"},
		{23, 59, true, "11:59 PM"},
	} {
		if got := str(AppendTimeOfDay(nil, c.h, c.m, c.twelve)); got != c.want {
			t.Errorf("%d:%d twelve %v = %q, want %q", c.h, c.m, c.twelve, got, c.want)
		}
	}
}

func TestTheClockFollowsHour12(t *testing.T) {
	at := time.Date(2026, 9, 23, 14, 5, 0, 0, time.UTC)
	clock(t, true)
	if got := str(AppendClock(nil, at)); got != "2:05 PM" {
		t.Errorf("12-hour %q", got)
	}
	clock(t, false)
	if got := str(AppendClock(nil, at)); got != "14:05" {
		t.Errorf("24-hour %q", got)
	}
}

// The copied transcript's stamp is the same on every clock
// (zf:src/demo.rs copy_stamp(1_700_000_000)).
func TestTheCopyStampIsWhatsApps(t *testing.T) {
	clock(t, true)
	at := time.Unix(1_700_000_000, 0).In(time.UTC)
	if got := str(AppendCopyStamp(nil, at)); got != "22:13, 11/14/2023" {
		t.Errorf("copy stamp %q", got)
	}
	if got := str(AppendCopyStamp(nil, time.Date(2026, 8, 18, 7, 4, 0, 0, time.UTC))); got != "7:04, 8/18/2026" {
		t.Errorf("copy stamp %q", got)
	}
}

// ---- stamps (zf:src/util.rs stamps_fall_back_to_dates and kin) ----

// tuesday is Tuesday 14 November 2023, 22:13 UTC, and later(n) the same time
// n days on, as upstream's tests count.
var tuesday = time.Unix(1_700_000_000, 0).In(time.UTC)

func later(days int) time.Time { return tuesday.AddDate(0, 0, days) }

func TestStampsFallBackToDates(t *testing.T) {
	clock(t, false)
	stamp := func(now time.Time) string { return str(AppendChatStamp(nil, i18n.English, tuesday, now)) }
	if got := stamp(tuesday); got != "22:13" {
		t.Errorf("today %q", got)
	}
	if got := stamp(later(1)); got != "Yesterday" {
		t.Errorf("yesterday %q", got)
	}
	if got := stamp(later(3)); got != "Tuesday" {
		t.Errorf("this week %q", got)
	}
	if got := stamp(later(30)); got != "14 Nov 2023" {
		t.Errorf("a month on %q", got)
	}
	// A day ahead of the clock is a date, as upstream has it.
	if got := stamp(later(-1)); got != "14 Nov 2023" {
		t.Errorf("tomorrow %q", got)
	}
}

// Last seen reads as on the phone: the time today and yesterday, the weekday
// and time within the week, and the date after that.
func TestLastSeenNamesTheDayAndTheTime(t *testing.T) {
	clock(t, false)
	seen := func(l i18n.Locale, now time.Time) string { return str(AppendLastSeen(nil, l, tuesday, now)) }
	for _, c := range []struct {
		now  time.Time
		want string
	}{
		{tuesday, "last seen today at 22:13"},
		{later(-1), "last seen today at 22:13"}, // a timestamp slightly ahead of our clock is still today
		{later(1), "last seen yesterday at 22:13"},
		{later(3), "last seen Tuesday at 22:13"},
		{later(6), "last seen Tuesday at 22:13"},
		{later(7), "last seen 14 Nov 2023"},
		{later(30), "last seen 14 Nov 2023"},
	} {
		if got := seen(i18n.English, c.now); got != c.want {
			t.Errorf("%v: %q, want %q", c.now, got, c.want)
		}
	}
	// Mid-sentence weekdays keep their capital only where the language
	// writes one.
	if got := seen(i18n.Italian, later(3)); got != "ultimo accesso martedì alle 22:13" {
		t.Errorf("Italian %q", got)
	}
	if got := seen(i18n.German, later(3)); got != "zuletzt online am Dienstag um 22:13" {
		t.Errorf("German %q", got)
	}
	for _, l := range i18n.All {
		for _, days := range []int{0, 1, 3, 30} {
			if line := seen(l, later(days)); strings.Contains(line, "{") {
				t.Errorf("%v %d: %q", l, days, line)
			}
		}
	}
}

func TestShortDatesTakeWholeCharactersInEveryLocale(t *testing.T) {
	for _, l := range i18n.All {
		for m := time.January; m <= time.December; m++ {
			short := str(AppendShortDate(nil, l, time.Date(2024, m, 5, 0, 0, 0, 0, time.UTC)))
			if !strings.HasPrefix(short, "5 ") || !strings.HasSuffix(short, " 2024") || strings.ContainsRune(short, '\uFFFD') {
				t.Errorf("%v: %q", l, short)
			}
		}
	}
	if got := str(AppendShortDate(nil, i18n.German, time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC))); got != "5 Mär 2024" {
		t.Errorf("German %q", got)
	}
}

func TestBrazilianPortugueseStampsAreTranslated(t *testing.T) {
	if got := str(AppendChatStamp(nil, i18n.PortugueseBrazil, tuesday, later(1))); got != "Ontem" {
		t.Errorf("yesterday %q", got)
	}
	if got := str(AppendChatStamp(nil, i18n.PortugueseBrazil, tuesday, later(3))); got != "Terça-feira" {
		t.Errorf("weekday %q", got)
	}
}

func TestTurkishStampsAreTranslated(t *testing.T) {
	if got := str(AppendChatStamp(nil, i18n.Turkish, tuesday, later(1))); got != "Dün" {
		t.Errorf("yesterday %q", got)
	}
	if got := str(AppendChatStamp(nil, i18n.Turkish, tuesday, later(3))); got != "Salı" {
		t.Errorf("weekday %q", got)
	}
}

func TestMomentStampsNameTheDayAndTheMinute(t *testing.T) {
	clock(t, true)
	for _, c := range []struct {
		now  time.Time
		want string
	}{
		{tuesday, "10:13 PM"},
		{later(1), "Yesterday at 10:13 PM"},
		{later(4), "Tuesday at 10:13 PM"},
		{later(7), "14 Nov 2023 at 10:13 PM"},
		{later(-2), "14 Nov 2023 at 10:13 PM"},
	} {
		if got := str(AppendMomentStamp(nil, i18n.English, tuesday, c.now)); got != c.want {
			t.Errorf("%v: %q, want %q", c.now, got, c.want)
		}
	}
	for _, l := range i18n.All {
		if line := str(AppendMomentStamp(nil, l, tuesday, later(3))); strings.Contains(line, "{") {
			t.Errorf("%v: %q", l, line)
		}
	}
}

func TestDayLabelsNameTheDay(t *testing.T) {
	for _, c := range []struct {
		now  time.Time
		want string
	}{
		{tuesday, "Today"},
		{later(1), "Yesterday"},
		{later(6), "Tuesday"},
		{later(7), "Tuesday, 14 November 2023"},
	} {
		if got := str(AppendDayLabel(nil, i18n.English, tuesday, c.now)); got != c.want {
			t.Errorf("%v: %q, want %q", c.now, got, c.want)
		}
	}
}

// The day is the one on the calendar of now's zone, not 24-hour spans.
func TestStampsCountCalendarDaysInNowsZone(t *testing.T) {
	clock(t, false)
	sp, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	// 01:30 UTC is still the evening before in São Paulo.
	at := time.Date(2026, 9, 23, 1, 30, 0, 0, time.UTC)
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, sp)
	if got := str(AppendChatStamp(nil, i18n.English, at, now)); got != "Yesterday" {
		t.Errorf("%q", got)
	}
	if got := DaysSince(at, now.In(time.UTC)); got != 0 {
		t.Errorf("in UTC, %d days", got)
	}
}

func TestMonthHeadingsAndWeekdayHeadings(t *testing.T) {
	if got := str(AppendMonthHeading(nil, i18n.English, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))); got != "September 2026" {
		t.Errorf("heading %q", got)
	}
	if got := WeekdayHeadings(i18n.English); got != [7]string{"Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"} {
		t.Errorf("headings %q", got)
	}
	for _, l := range i18n.All {
		for _, h := range WeekdayHeadings(l) {
			if n := len([]rune(h)); n == 0 || n > 2 || strings.ContainsRune(h, '\uFFFD') {
				t.Errorf("%v: %q", l, h)
			}
		}
	}
}

// zf:src/util.rs a_day_filter_covers_the_local_day_across_clock_changes
func TestADayFilterCoversTheLocalDayAcrossClockChanges(t *testing.T) {
	second := func(s string) int64 {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v.Unix()
	}
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	if from, until := DayBounds(2026, 9, 23, berlin); from != second("2026-09-22T22:00:00Z") || until != second("2026-09-23T22:00:00Z") {
		t.Errorf("an ordinary day runs from local midnight to local midnight: %d %d", from, until)
	}
	if from, until := DayBounds(2026, 3, 29, berlin); until-from != 23*3600 {
		t.Errorf("spring forward loses an hour: %d", until-from)
	}
	if from, until := DayBounds(2026, 10, 25, berlin); until-from != 25*3600 {
		t.Errorf("fall back gains one: %d", until-from)
	}
	// Brazil once sprang forward at midnight: that day began at 01:00.
	brasilia, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	if from, until := DayBounds(2018, 11, 4, brasilia); from != second("2018-11-04T03:00:00Z") || until != second("2018-11-05T02:00:00Z") {
		t.Errorf("Brazil's day: %d %d", from, until)
	}
	// The day before ends where that one begins.
	if _, until := DayBounds(2018, 11, 3, brasilia); until != second("2018-11-04T03:00:00Z") {
		t.Errorf("the day before ends at %d", until)
	}
	// The last day of a month runs into the next month's first.
	if from, until := DayBounds(2026, 1, 31, time.UTC); until-from != 86400 {
		t.Errorf("31 January: %d", until-from)
	}
}

// ---- durations (zf:src/util.rs a_call_length_reads_the_way_the_phone_shows_it and kin) ----

func TestACallLengthReadsTheWayThePhoneShowsIt(t *testing.T) {
	for secs, want := range map[uint64]string{0: "0:00", 65: "1:05", 3600 + 2*60 + 3: "1:02:03"} {
		if got := str(AppendCallDuration(nil, secs)); got != want {
			t.Errorf("%d: %q, want %q", secs, got, want)
		}
	}
}

func TestAFinishedCallLengthReadsInWords(t *testing.T) {
	for secs, want := range map[uint64]string{45: "45 s", 754: "12 min 34 s", 720: "12 min", 3725: "1 h 2 min", 3600: "1 h"} {
		if got := str(AppendCallLength(nil, i18n.English, secs)); got != want {
			t.Errorf("%d: %q, want %q", secs, got, want)
		}
	}
	if got := str(AppendCallLength(nil, i18n.German, 754)); got != "12 Min. 34 Sek." {
		t.Errorf("German %q", got)
	}
}

func TestDurationsReadNaturally(t *testing.T) {
	if got := str(AppendDuration(nil, 75)); got != "1:15" {
		t.Errorf("%q", got)
	}
	if got := str(AppendDuration(nil, 5)); got != "0:05" {
		t.Errorf("%q", got)
	}
}

// ---- allocations ----

// Every Append function appends into a buffer that has room without
// allocating, so a frame can call them.
func TestAppendingAllocatesNothing(t *testing.T) {
	clock(t, true)
	buf := make([]byte, 0, 256)
	l := i18n.PortugueseBrazil
	now := later(3)
	for name, f := range map[string]func(){
		"phone":         func() { buf = AppendPhone(buf[:0], "5511999999999") },
		"phone grouped": func() { buf = AppendPhone(buf[:0], "+39 333 123 4567") },
		"clock":         func() { buf = AppendClock(buf[:0], tuesday) },
		"copy stamp":    func() { buf = AppendCopyStamp(buf[:0], tuesday) },
		"chat stamp":    func() { buf = AppendChatStamp(buf[:0], l, tuesday, later(30)) },
		"moment":        func() { buf = AppendMomentStamp(buf[:0], l, tuesday, now) },
		"last seen":     func() { buf = AppendLastSeen(buf[:0], i18n.Italian, tuesday, now) },
		"day label":     func() { buf = AppendDayLabel(buf[:0], l, tuesday, later(9)) },
		"call length":   func() { buf = AppendCallLength(buf[:0], l, 754) },
		"call clock":    func() { buf = AppendCallDuration(buf[:0], 3725) },
		"duration":      func() { buf = AppendDuration(buf[:0], 75) },
		"local zone":    func() { buf = AppendChatStamp(buf[:0], l, tuesday, time.Now()) },
	} {
		if n := testing.AllocsPerRun(50, f); n != 0 {
			t.Errorf("%s: %v allocations", name, n)
		}
	}
	localeHour12() // the system's setting costs a read once, then nothing
	if n := testing.AllocsPerRun(50, func() { localeHour12() }); n != 0 {
		t.Errorf("the system clock setting: %v allocations", n)
	}
	if systemHour12() {
		t.Error("a test binary reads the 12-hour clock")
	}
}
