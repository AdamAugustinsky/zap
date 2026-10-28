package settings

import (
	"os/exec"
	"runtime"

	"zapfast/state"
)

// previewSound plays a notification sound (the Play button, SE-032). The
// Pidgin sounds are not shipped (GPL-2.0): Receive and Alert play macOS's own.
// TODO(sys): package sys plays the notification sounds; share its player.
func previewSound(sc state.SoundChoice) {
	if runtime.GOOS != "darwin" {
		return
	}
	file := ""
	switch sc.Sound {
	case state.SoundReceive:
		file = "/System/Library/Sounds/Glass.aiff"
	case state.SoundAlert:
		file = "/System/Library/Sounds/Ping.aiff"
	case state.SoundCustom:
		file = string(sc.Path)
	}
	if file == "" {
		return
	}
	go func() { _ = exec.Command("/usr/bin/afplay", file).Run() }()
}
