package i18n

import "strings"

// Locale is an interface language. The zero value is English, the source
// language.
type Locale uint8

// The languages ZapFast ships, in the order of the picker.
const (
	English Locale = iota
	PortugueseBrazil
	German
	Spanish
	Italian
	French
	Russian
	ChineseSimplified
	ChineseTraditional
	Turkish
	Indonesian
	numLocales
)

// All is every locale in the language picker, in a stable order.
var All = [numLocales]Locale{
	English, PortugueseBrazil, German, Spanish, Italian, French, Russian,
	ChineseSimplified, ChineseTraditional, Turkish, Indonesian,
}

var tags = [numLocales]string{
	"en", "pt-BR", "de", "es", "it", "fr", "ru", "zh-Hans", "zh-Hant", "tr", "id",
}

var labels = [numLocales]string{
	"English", "Português (Brasil)", "Deutsch", "Español", "Italiano", "Français",
	"Русский", "简体中文", "繁體中文", "Türkçe", "Bahasa Indonesia",
}

// Tag is the name the locale is stored under (upstream's serde name), which
// is also the name of its .po file.
func (l Locale) Tag() string {
	if l >= numLocales {
		return tags[English]
	}
	return tags[l]
}

// Label is the language's own name, for the picker.
func (l Locale) Label() string {
	if l >= numLocales {
		return labels[English]
	}
	return labels[l]
}

// String is the tag.
func (l Locale) String() string { return l.Tag() }

// ParseTag is the inverse of Tag: the locale stored under tag.
func ParseTag(tag string) (Locale, bool) {
	for i, t := range tags {
		if t == tag {
			return Locale(i), true
		}
	}
	return English, false
}

// MarshalText and UnmarshalText store a Locale as its tag in JSON and the
// like, as upstream does.
func (l Locale) MarshalText() ([]byte, error) { return []byte(l.Tag()), nil }

// UnmarshalText reads a tag; an unknown one leaves English.
func (l *Locale) UnmarshalText(b []byte) error {
	*l, _ = ParseTag(string(b))
	return nil
}

// languageTag is upstream's LanguageTag: lowercase subtags, the first
// four-letter script and the first two- or three-character region.
type languageTag struct{ language, script, region string }

func parseLanguageTag(tag string) (t languageTag, ok bool) {
	tag = strings.ToLower(tag)
	// A POSIX name can carry an encoding and a modifier.
	if i := strings.IndexAny(tag, ".@"); i >= 0 {
		tag = tag[:i]
	}
	for _, sub := range strings.FieldsFunc(tag, func(r rune) bool { return r == '-' || r == '_' }) {
		if !ok {
			t.language, ok = sub, true
			continue
		}
		switch len(sub) {
		case 4:
			if t.script == "" {
				t.script = sub
			}
		case 2, 3:
			if t.region == "" {
				t.region = sub
			}
		}
	}
	return t, ok
}

// FromSystem maps a system language tag (BCP 47 or POSIX) to a supported
// locale by its language subtag, so pt-PT and pt_BR both resolve to
// Portuguese. Chinese is the exception: its script decides, so a reader who
// asked for Traditional does not get Simplified characters and the other way
// round.
func FromSystem(identifier string) (Locale, bool) {
	t, ok := parseLanguageTag(identifier)
	if !ok {
		return English, false
	}
	switch t.language {
	case "en":
		return English, true
	case "pt":
		return PortugueseBrazil, true
	case "de":
		return German, true
	case "es":
		return Spanish, true
	case "it":
		return Italian, true
	case "fr":
		return French, true
	case "ru":
		return Russian, true
	case "tr":
		return Turkish, true
	case "id":
		return Indonesian, true
	case "zh":
		// An explicit script decides on its own: zh-Hans-HK is Simplified.
		// The region only speaks for the tags that name no script.
		switch t.script {
		case "hans":
			return ChineseSimplified, true
		case "hant":
			return ChineseTraditional, true
		}
		switch t.region {
		case "tw", "hk", "mo", "cht": // Windows writes the legacy zh-CHT
			return ChineseTraditional, true
		}
		return ChineseSimplified, true
	}
	return English, false
}
