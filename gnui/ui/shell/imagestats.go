package shell

import "gonativeui/img"

// ImageStats is the main window's image cache's counters (zero before the
// window has a cache): the budgets' bench reports them.
func (sh *Shell) ImageStats() img.Stats {
	if sh.imgs == nil {
		return img.Stats{}
	}
	return sh.imgs.Stats()
}
