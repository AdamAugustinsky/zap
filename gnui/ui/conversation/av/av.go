// Package av draws voice notes, audio files, inline videos and round video
// notes inside the transcript's bubbles, and plays them, on the framework's
// media.Player and media.Video.
//
// It ports those parts of zf:src/ui/conversation.rs (voice_player,
// speed_pill, video, video_controls, video_note), zf:src/audio.rs (the
// player's states and speeds), zf:src/video.rs (the video player's session,
// speed and sound) and the voice and video actions of zf:src/app.rs
// (play_voice, SeekVoice, SetVoiceSpeed, continue_voice, play_video,
// PlayVideoWhenDownloaded, SeekVideo, ToggleVideoSound, CycleVideoSpeed,
// ExpandVideo). The transcript (package conversation) owns the bubble and
// asks this package for the block's size and has it build the block,
// exactly as it does with package kinds.
//
// # Contract with the transcript
//
//   - Has(m) says whether the message has a block here: every audio message
//     (voice notes and audio files) and every video (inline, GIF, round).
//   - Size(e, m, maxW) is cheap and allocates nothing.
//   - Build(e, m, r) builds the block in r, in the coordinates of the box
//     being built (ui.Context.NextPos), and handles its input. It asks the
//     core for downloads that upstream starts when a block comes into view,
//     so call it only for messages that are on screen.
//   - NoBubble(m) tells the transcript to draw the message without a bubble
//     round it: a round video note (upstream's bubbleless).
//   - FooterLine(e, m, r) is where a voice note's time and ticks go.
//   - Frame(e), once per frame of the main window (a screen.Ticker), lets
//     playback end, the next voice note start, a clip that does not show
//     pause and stop, and a clip clicked before its file was here play
//     once it is, while no block is on screen; Size and Build run it too,
//     once per frame.
//   - Event(s, ev) takes the framework's media events (a clip that played
//     to its end, a file that opened); without it Frame notices the same by
//     polling.
//
// Package kinds draws photos; a motion photo's clip is this package's: kinds
// calls Motion(e, m, r, radius) after the photo, which draws the clip over
// it while it plays and says what the badge shows, and ToggleMotion(e, m)
// when the badge is clicked.
//
// # Voice notes and audio files
//
// One clip is loaded at a time (upstream's audio::Player): starting another
// closes the one before. The core decodes the Ogg/Opus file to PCM on a
// worker (core.VoiceDecoder), where its blob is read and freed; an audio
// file it cannot decode (MP3, AAC, M4A) plays from its file
// (media.OpenPlayer, opened off the main thread), and one the system cannot
// decode either goes to the system's player. The Player is made, played,
// paused, sought and sped up on the main thread, and read every frame
// without allocating. The speed is the app's (Settings.VoiceSpeed: 1x, 1.5x
// and 2x from the chip; Speeds, SpeedLabel and SetSpeed for the message
// menu's row), with the pitch kept. Playing sends the played receipt
// (logic.PlayVoice); a note that ends carries on into the next unheard one
// (logic.VoiceEnded, State.Voice.Next). Locking the app, or switching
// account, stops playback.
//
// # Videos
//
// A click plays a downloaded clip in its bubble with sound (one not
// downloaded yet downloads and plays once it is here): its frames are
// media.Video's, drawn with Kit.ExternalImage while the box shows (an
// inline clip fitted in its frame, a round note's centred square in its
// circle). An inline clip has upstream's controls while paused or under the
// pointer (play or pause, the time, the seek bar, the speed chip, the sound
// switch, "Fill the window", which opens the viewer window); a round note
// has its progress ring. One sound at a time: a clip stops the voice note
// and its run, and a voice note stops the clip. A clip that does not show
// pauses, and after 1.5 s unseen it stops and lets its player go; played to
// its end it goes back to its poster; locking, switching account, leaving
// the chat or opening the viewer stops it. The video speed and the sound
// switch carry over to the next clip until quitting. A clip the system
// cannot play opens in the system's player. A downloaded GIF video loops
// muted under the pointer and opens in the system's player when clicked. A
// double click or "Fill the window" plays the clip in the viewer window,
// from where it stood (State.OpenViewerPlayingAt). A motion photo's clip
// loops muted over the photo from a click on its badge until a second one,
// with the clips' rules for what does not show.
//
// Playing reports a clip playing with its sound, for pausing other apps'
// media.
package av

import (
	"zapfast/abi"
	"zapfast/model"
	"zapfast/ui/screen"
)

// Has reports whether m has a block drawn by this package.
func Has(m model.Message) bool {
	switch m.Kind() {
	case abi.ContentTagAudio, abi.ContentTagVideo:
		_, ok := m.Media()
		return ok
	}
	return false
}

// Size is the block's size in pixels for a bubble whose content is at most
// maxW pixels wide. It must not allocate once warm.
func Size(e *screen.Env, m model.Message, maxW float32) (w, h float32) {
	if !Has(m) || maxW < 1 {
		return 0, 0
	}
	Frame(e)
	b := use(e, m)
	defer b.done()
	if m.Kind() == abi.ContentTagAudio {
		return b.voiceSize(maxW)
	}
	return b.videoSize(maxW)
}

// Build builds the block at r (in the coordinates of the box being built,
// see the package's doc) and handles its input.
func Build(e *screen.Env, m model.Message, r screen.Rect) {
	if !Has(m) || r.W < 1 || r.H < 1 {
		return
	}
	Frame(e)
	b := use(e, m)
	defer b.done()
	b.begin(r)
	if m.Kind() == abi.ContentTagAudio {
		b.voiceBuild(r)
	} else {
		b.videoBuild(r)
	}
	b.end()
}

// NoBubble reports whether m is drawn without a bubble round it: a round
// video note, which is its own circle (zf:src/ui/conversation.rs
// bubbleless).
func NoBubble(m model.Message) bool {
	if m.Kind() != abi.ContentTagVideo {
		return false
	}
	md, ok := m.Media()
	return ok && md.VideoNote
}
