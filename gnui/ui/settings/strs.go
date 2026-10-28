package settings

import (
	"strings"

	"zapfast/i18n"
	"zapfast/state"
)

// strs are the translated strings the controls show, built once per
// language.
type strs struct {
	versionLine, checkNow, openFolder, open, change, useDefault, clear string
	smaller, larger, play, cancel, save                                string
	storageUsed, clearMedia, images, videos, stickersName, other       string
	openThemes, howTheme, tagline, shortcuts, about, sourceCode        string
	creditBefore, creditAfter                                          string
	updateTitle, releaseNotes, checkingDownload, downloading, progress string
	of, readyToInstall, finishFirst, restart, preparing, fromGitHub    string
	retry, download                                                    string
	checking, upToDate, noFeed, notChecked, failed, failedFor          string
	updToFor, updToStr                                                 string
	privFailed, privOffline, privLoading                               string
	privExcept                                                         [7]string
	lockTitle, lockConfirm                                             [3]string
	lockErr                                                            [4]string
	currentPassword, newPassword, againPassword, working               string
	setPassword, changePassword, turnOffEllipsis                       string
	changePicture, yourName, aboutLabel, editProfile                   string
	addAccount, removeAccount, unlink                                  string
	linkedDevice                                                       []byte
}

func (t *strs) build() {
	T, P := i18n.T, i18n.P
	t.versionLine = "Zap " + state.Version
	t.checkNow, t.openFolder, t.open, t.change = T("Check now"), T("Open folder"), T("Open"), T("Change…")
	t.useDefault, t.clear = T("Use default"), T("Clear")
	t.smaller, t.larger, t.play, t.cancel, t.save = T("Smaller"), T("Larger"), T("Play"), T("Cancel"), T("Save")
	t.storageUsed, t.clearMedia = T("Storage used"), T("Clear…")
	t.images, t.videos, t.stickersName, t.other = T("Images"), T("Videos and GIFs"), T("Stickers"), T("Other")
	t.openThemes, t.howTheme = T("Open themes folder"), T("How to make a theme")
	t.tagline = T("A native WhatsApp client built with Go and gonativeui.")
	t.shortcuts, t.about, t.sourceCode = T("Keyboard shortcuts"), T("About"), T("Source code")
	credit := T("Built with love by {name}")
	t.creditBefore, t.creditAfter, _ = strings.Cut(credit, "{name}")
	t.updateTitle, t.releaseNotes = T("Update Zap"), T("Release notes")
	t.checkingDownload, t.downloading, t.progress = T("Checking download…"), T("Downloading update…"), T("Download progress")
	t.of = T("of")
	t.readyToInstall = T("Ready to install")
	t.finishFirst = T("Finish any unsent messages or recordings before restarting. Zap will briefly disconnect, then reconnect automatically.")
	t.restart, t.preparing, t.fromGitHub = T("Restart to update"), T("Preparing to restart…"), T("Download from GitHub")
	t.retry, t.download = T("Retry download"), T("Download update")
	t.checking, t.upToDate = T("Checking for updates…"), T("Zap is up to date")
	t.noFeed, t.notChecked = T("No update feed is configured."), T("Not checked yet")
	t.failedFor, t.updToFor = "", ""
	t.privFailed = T("Could not load your account privacy. Trying again when Zap reconnects.")
	t.privOffline = T("Connect to WhatsApp to change your account privacy.")
	t.privLoading = T("Loading your account privacy…")
	note := T("Change who is excluded on your phone.")
	for i := range t.privExcept {
		if hint := privHint(i); hint.id != "" {
			t.privExcept[i] = hint.shown() + " " + note
		} else {
			t.privExcept[i] = note
		}
	}
	t.lockTitle = [3]string{T("Set an app lock password"), T("Change the app lock password"), T("Turn off the app lock")}
	t.lockConfirm = [3]string{T("Turn on"), T("Change password"), T("Turn off")}
	count := "6"
	t.lockErr = [4]string{"", i18n.Format(T("The password needs at least {count} characters."), "{count}", count),
		T("The two new passwords are different."), T("Wrong password. Try again.")}
	t.currentPassword = T("Current password")
	t.newPassword = i18n.Format(T("New password, at least {count} characters"), "{count}", count)
	t.againPassword, t.working = T("Type the new password again"), T("Working…")
	t.setPassword, t.changePassword, t.turnOffEllipsis = T("Set password…"), T("Change password…"), T("Turn off…")
	t.changePicture, t.yourName, t.aboutLabel = T("Change profile picture"), T("Your name"), P("profile", "About")
	t.editProfile = T("Edit your name and About")
	t.addAccount, t.removeAccount, t.unlink = T("Add account"), T("Remove this account"), T("Unlink this computer")
	t.linkedDevice = []byte(T("Linked device"))
}

// updateTo is "Update to {version}", built once per version.
func (t *strs) updateTo(version string) string {
	if t.updToFor != version {
		t.updToFor = version
		t.updToStr = i18n.Format(i18n.T("Update to {version}"), "{version}", version)
	}
	return t.updToStr
}
