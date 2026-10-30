package settings

import (
	"zapfast/i18n"
)

// The titlebar's update button (the shell's, screen.UpdatePill), as Ghostty
// and T3 Code put theirs: shown while a newer version is known, it follows
// the download to "Restart to update". Its words are the Updates section's.

// pillText caches the label "Update to {version}" per version and language,
// as Pill runs every frame.
type pillText struct {
	version string
	locale  i18n.Locale
	label   string
}

// Pill is the button's label, "" when there is no newer version to offer.
func (u *Updates) Pill() string {
	sn := u.Snapshot()
	if sn.Release.Version == "" {
		return ""
	}
	switch sn.State {
	case Downloading:
		return i18n.T("Downloading update…")
	case DownloadReady:
		return i18n.T("Restart to update")
	case Installing:
		return i18n.T("Preparing to restart…")
	case DownloadFailed:
		if sn.SupportKnown && sn.Support == "" {
			return i18n.T("Retry download")
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if t := &u.pill; t.version != sn.Release.Version || t.locale != i18n.Current() {
		t.version, t.locale = sn.Release.Version, i18n.Current()
		t.label = i18n.Format(i18n.T("Update to {version}"), "{version}", sn.Release.Version)
	}
	return u.pill.label
}

// PressPill acts on the button: it downloads the release, or restarts into
// the downloaded one. It reports false when the update needs the Updates
// section instead (this copy cannot replace itself, the download is under
// way, or the probe has not answered), which the shell then opens.
func (u *Updates) PressPill() bool {
	sn := u.Snapshot()
	switch {
	case sn.Release.Version == "":
		return false
	case sn.State == DownloadReady:
		u.Install()
		return true
	case (sn.State == DownloadIdle || sn.State == DownloadFailed) && sn.SupportKnown && sn.Support == "":
		u.Download()
		return true
	}
	return false
}
