//go:build !gnui_release

package settings

// releaseFeed is empty outside release builds: a development build, and its
// tests, check no feed unless ZAPFAST_UPDATE_FEED names one.
const releaseFeed = ""
