// Package format is upstream's text formatting helpers (zf:src/util.rs) that
// several screens show: phone numbers, times of day, dates, stamps and
// durations. Every Append function appends to dst and returns it, without
// allocating once dst has room, so a frame can format into a buffer it
// reuses; the string forms are for code that runs when data changes.
//
// Words come from the catalog of the locale passed in (i18n.Current() for
// the interface's language); the clock follows Hour12.
package format

// Phone is AppendPhone as a string.
func Phone(digits string) string { return string(AppendPhone(nil, digits)) }

// AppendPhone appends a phone number with a plus sign and its country's
// grouping (zf:src/util.rs phone): Brazil's "+55 (11) 99999-9999", the rest
// as the country code and the national number in threes, the last four
// together ("+39 333 123 4567"). Anything but ASCII digits in digits is
// ignored; no digits append nothing.
func AppendPhone(dst []byte, digits string) []byte {
	var buf [32]byte
	d := buf[:0]
	for i := 0; i < len(digits); i++ {
		if c := digits[i]; c >= '0' && c <= '9' {
			d = append(d, c)
		}
	}
	if len(d) == 0 {
		return dst
	}
	if len(d) >= 2 && d[0] == '5' && d[1] == '5' && (len(d) == 12 || len(d) == 13) {
		return appendBrazilian(dst, d[2:])
	}
	cc := min(countryCodeLen(d), len(d))
	dst = append(append(dst, '+'), d[:cc]...)
	// Approximate national grouping: threes, with the last four together
	// rather than leaving a lone digit.
	national := d[cc:]
	for start := 0; start < len(national); {
		end := min(start+3, len(national))
		if len(national)-start == 4 {
			end = len(national)
		}
		dst = append(append(dst, ' '), national[start:end]...)
		start = end
	}
	return dst
}

// appendBrazilian appends a +55 number's national part, a two-digit area
// code and an eight-digit fixed line or nine-digit mobile, as
// "+55 (DDD) XXXX-XXXX" or "+55 (DDD) XXXXX-XXXX".
func appendBrazilian(dst, national []byte) []byte {
	area, subscriber := national[:2], national[2:]
	split := len(subscriber) - 4
	dst = append(dst, "+55 ("...)
	dst = append(dst, area...)
	dst = append(dst, ") "...)
	dst = append(dst, subscriber[:split]...)
	dst = append(dst, '-')
	return append(dst, subscriber[split:]...)
}

// twoDigitCodes are the ITU-T E.164 country calling codes of two digits.
var twoDigitCodes = [...]string{
	"20", "27", "30", "31", "32", "33", "34", "36", "39", "40", "41", "43", "44", "45", "46",
	"47", "48", "49", "51", "52", "53", "54", "55", "56", "57", "58", "60", "61", "62", "63",
	"64", "65", "66", "81", "82", "84", "86", "90", "91", "92", "93", "94", "95", "98",
}

// countryCodeLen is the length of the country calling code digits starts
// with. The codes are prefix-free: only 1 and 7 have one digit, the two-digit
// ones are listed, and every other code has three.
func countryCodeLen(d []byte) int {
	if d[0] == '1' || d[0] == '7' {
		return 1
	}
	if len(d) >= 2 {
		for _, c := range twoDigitCodes {
			if d[0] == c[0] && d[1] == c[1] {
				return 2
			}
		}
	}
	return 3
}
