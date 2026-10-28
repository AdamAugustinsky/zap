package core

import "gonativeui/app/paths"

// AppID is the app's identity (D51): its data and cache directories are
// named after it, never upstream ZapFast's, so this app cannot open the
// archive of a ZapFast that may be running beside it.
const AppID = "ai.a3lab.zap"

// KeyringService is the OS keyring service the app keeps its archive keys
// under: its own, not upstream's "rocks.zapfast.ZapFast", which the core
// refuses.
const KeyringService = AppID

// DeviceName is the app's name in the linked-device entry the phone shows
// for this computer ("Zap (computer name)"), set when the computer links.
const DeviceName = "Zap"

// Dirs returns the app's state and cache roots (each account's folders go
// under accounts/<id> in both). It touches no disk.
func Dirs() (state, cache string, err error) {
	if state, err = paths.Data(AppID); err != nil {
		return "", "", err
	}
	if cache, err = paths.Cache(AppID); err != nil {
		return "", "", err
	}
	return state, cache, nil
}
