package info

import "zapfast/state"

// PhotoChoice is the choice of a menu item of the group picture's menu, which
// a native menu delivers a frame after it closes (the framework's own tests
// cover that delivery; a headless window has no native menu to choose from).
func (s *Screen) PhotoChoice(a *state.Account, item int) { s.photoChoice(a, item) }

const (
	MenuChangePhoto = menuChangePhoto
	MenuRemovePhoto = menuRemovePhoto
)
