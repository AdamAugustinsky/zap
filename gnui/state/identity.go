package state

import (
	"path/filepath"

	"zapfast/core"
)

// Version is Zap's version (gnui.json's version; it began as upstream
// ZapFast's, whose numbering it continues), which About, Settings → Updates
// and the updater show and compare.
const Version = "0.20.1"

// AppID is the app's identity (core.AppID): its data, cache and Keychain
// items are its own, never upstream ZapFast's.
const AppID = core.AppID

// AccountDirs are account id's folders: its state (archive, settings) and
// its cache (media), each accounts/<id> under the app's roots. It touches no
// disk.
func AccountDirs(id string) (data, cache string, err error) {
	d, c, err := core.Dirs()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(d, "accounts", id), filepath.Join(c, "accounts", id), nil
}
