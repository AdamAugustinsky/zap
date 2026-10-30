//go:build gnui_release

package settings

// releaseFeed is where release builds look for updates: the latest GitHub
// release's assets, latest.json among them (packaging/macos/gnui-release.sh).
const releaseFeed = "https://github.com/AdamAugustinsky/zap/releases/latest/download"
