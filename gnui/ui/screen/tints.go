package screen

import "zapfast/i18n"

// TintLabel is a colour's name (a state.SpaceTints name, "" none) as the
// person reads it: a space's, a chat boost's.
func TintLabel(name string) string {
	switch name {
	case "blue":
		return i18n.T("Blue")
	case "purple":
		return i18n.T("Purple")
	case "pink":
		return i18n.T("Pink")
	case "red":
		return i18n.T("Red")
	case "orange":
		return i18n.T("Orange")
	case "yellow":
		return i18n.T("Yellow")
	case "green":
		return i18n.T("Green")
	case "teal":
		return i18n.T("Teal")
	case "gray":
		return i18n.T("Gray")
	}
	return i18n.T("No colour")
}
