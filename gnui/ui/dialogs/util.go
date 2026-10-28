package dialogs

import (
	"strings"
)

// InternationalPhone normalizes a typed number without changing an
// international recipient: 7 to 15 ASCII digits, an optional leading plus,
// spaces, hyphens and balanced brackets (zf:src/util.rs:10). ok is false for
// anything else.
func InternationalPhone(input string) (digits string, ok bool) {
	input = strings.TrimSpace(input)
	input = strings.TrimPrefix(input, "+")
	var b strings.Builder
	bracket := false
	for _, r := range input {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
		case r == '(' && !bracket:
			bracket = true
		case r == ')' && bracket:
			bracket = false
		default:
			return "", false
		}
	}
	digits = b.String()
	if bracket || len(digits) < 7 || len(digits) > 15 || digits[0] == '0' {
		return "", false
	}
	return digits, true
}

// ComposeName is upstream's compose_name: the full name, and the first name
// when there is one; both empty when no name was typed.
func ComposeName(first, last string) (full, short string, named bool) {
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)
	switch {
	case first == "" && last == "":
		return "", "", false
	case last == "":
		full = first
	case first == "":
		full = last
	default:
		full = first + " " + last
	}
	return full, first, true
}
