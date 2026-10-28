package conversation

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
)

func voiceMsg(id string, ts int64, fromMe bool, path string) abi.Message {
	m := msg(ada, id, ts, fromMe, "")
	m.Content = abi.Content{Tag: abi.ContentTagAudio}
	au := &m.Content.Audio
	au.VoiceNote, au.Seconds, au.HasSeconds = true, 7, true
	au.Media = abi.Media{Mime: []byte("audio/ogg; codecs=opus"), Size: 12_000, Path: []byte(path), HasPath: path != ""}
	return m
}

// A voice note sits in its bubble with its time and ticks on its duration's
// line, under the waveform; its menu lists every speed (MM-019).
func TestVoiceNoteInTheTranscript(t *testing.T) {
	r := newRig(t)
	r.open(ada, voiceMsg("vn", base0, true, "/v/7.ogg"))
	b := r.node(ui.RoleGroup, bubble("You", base0)).Bounds
	wave := r.node(ui.RoleSlider, "Voice message").Bounds
	info := r.node(ui.RoleButton, "Message info").Bounds
	if !inside(info, b) || info[1] < wave[3]-1 || b[2]-info[2] > 24 {
		t.Errorf("the footer %v is not on the duration line under %v at the right of %v", info, wave, b)
	}
	r.Play(widgettest.RClick(ui.RoleGroup, bubble("You", base0)), widgettest.Idle(2))
	for _, l := range []string{"1x", "1.25x", "1.5x", "1.75x", "2x"} {
		r.must(ui.RoleButton, "Playback speed "+l)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Playback speed 1.75x"), widgettest.Idle(2))
	if r.s.Settings.VoiceSpeed != 1.75 {
		t.Errorf("speed %v", r.s.Settings.VoiceSpeed)
	}
	r.mustNot(ui.RoleButton, "Playback speed 1.25x")
}

// A round video note is drawn without a bubble: the bubble is as wide as
// the circle.
func TestRoundNoteHasNoBubble(t *testing.T) {
	r := newRig(t)
	n := msg(ada, "rn", base0, false, "")
	n.Content = abi.Content{Tag: abi.ContentTagVideo}
	n.Content.Video.Note, n.Content.Video.Seconds, n.Content.Video.HasSeconds = true, 9, true
	n.Content.Video.Media = abi.Media{Mime: []byte("video/mp4"), Size: 900_000}
	r.open(ada, n)
	b := r.node(ui.RoleGroup, bubble("Ada Lovelace", base0)).Bounds
	var circle [4]float32
	for _, nd := range r.Tree().Nodes {
		if nd.Role == ui.RoleButton && inside(nd.Bounds, b) && nd.Bounds[2]-nd.Bounds[0] > circle[2]-circle[0] {
			circle = nd.Bounds
		}
	}
	if w := circle[2] - circle[0]; w < 100 || b[2]-b[0] > w+1 {
		t.Errorf("bubble %v round the note %v", b, circle)
	}
}

// A failed message of ours says "Not sent" in its footer and explains it
// (CV-023); it opens no Message info.
func TestNotSentExplains(t *testing.T) {
	r := newRig(t)
	m := msg(ada, "f1", base0, true, "lost")
	m.Status = abi.DeliveryFailed
	r.open(ada, m)
	r.must(ui.RoleLabel, notSentHint)
	r.mustNot(ui.RoleButton, "Message info")
}
