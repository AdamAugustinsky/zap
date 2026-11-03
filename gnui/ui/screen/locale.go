package screen

import (
	"gonativeui/rexui"

	"zapfast/i18n"
)

// The framework's components and its format package speak Zap's language
// where the framework has it (English and Brazilian Portuguese), and write
// a date day first, as Zap always has, in the others.
func init() {
	i18n.Observe(func(l i18n.Locale) { rexui.SetStrings(frameworkStrings(l)) })
	rexui.SetStrings(frameworkStrings(i18n.Current()))
}

// frameworkStrings are rexui's words and formats for l.
func frameworkStrings(l i18n.Locale) rexui.Strings {
	if l == i18n.PortugueseBrazil {
		return rexui.StringsPtBR
	}
	s := rexui.StringsEnglish
	s.Format.Date, s.Format.DateTime = "02/01/06", "02/01/2006 15:04"
	return s
}
