package calls

import (
	"testing"

	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/i18n"
)

// The call window at the smallest size it goes and at its own, in the
// languages with the longest words: no label runs out of its box and nothing
// overlaps, ringing, active, on video and as a group.
func TestTheCallWindowFitsAtItsSmallestSizeInEveryLanguage(t *testing.T) {
	scenes := []struct {
		name string
		set  func(w *world)
	}{
		{"ringing", func(w *world) { w.update(incoming(w.f.CallGeneration(), abi.CallPhaseIncoming)) }},
		{"ringing video", func(w *world) {
			w.update(func(u *abi.CallUpdate) { incoming(w.f.CallGeneration(), abi.CallPhaseIncoming)(u); u.Video = true })
		}},
		{"ringing group", func(w *world) {
			w.update(func(u *abi.CallUpdate) {
				u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup = w.f.CallGeneration(), []byte(group), abi.CallDirectionIncoming, abi.CallPhaseIncoming, true
				u.Group.Participants = []abi.Participant{{ID: []byte(grace), Joined: true}, {ID: []byte(alan), Joined: true}, {ID: []byte(ada), Joined: true}}
			})
		}},
		{"active", func(w *world) {
			w.update(func(u *abi.CallUpdate) { incoming(1, abi.CallPhaseActive)(u); u.ActiveForMS = 5_000 })
		}},
		{"active muted", func(w *world) {
			w.update(func(u *abi.CallUpdate) { incoming(1, abi.CallPhaseActive)(u); u.ActiveForMS, u.Muted = 3_725_000, true })
		}},
		{"video", func(w *world) {
			w.f.SetFrame(core.StreamRemote, fake.TestPattern(64, 36, 1))
			w.f.SetFrame(core.StreamLocal, fake.TestPattern(32, 18, 1))
			w.update(func(u *abi.CallUpdate) {
				incoming(1, abi.CallPhaseActive)(u)
				u.Video, u.CameraOn, u.RemoteVideo, u.ActiveForMS = true, true, true, 5_000
			})
		}},
		{"video camera off", func(w *world) {
			w.update(func(u *abi.CallUpdate) { incoming(1, abi.CallPhaseActive)(u); u.Video, u.ActiveForMS = true, 5_000 })
		}},
		{"group", func(w *world) {
			w.update(func(u *abi.CallUpdate) {
				u.Generation, u.Chat, u.Direction, u.Phase, u.HasGroup, u.Video, u.CameraOn = 1, []byte(group), abi.CallDirectionOutgoing, abi.CallPhaseActive, true, true, true
				u.Group.Participants = []abi.Participant{{ID: []byte(grace), Joined: true}, {ID: []byte(alan), Joined: true}, {ID: []byte(ada)}}
			})
		}},
	}
	sizes := []struct{ w, h int }{{360, 300}, {400, 300}, {720, 520}}
	for _, lang := range []i18n.Locale{i18n.English, i18n.German, i18n.Russian} {
		for _, sc := range scenes {
			for _, sz := range sizes {
				name := lang.Tag() + "/" + sc.name + "/" + itoa(sz.w)
				t.Run(name, func(t *testing.T) {
					old := i18n.Current()
					i18n.SetLocale(lang)
					t.Cleanup(func() { i18n.SetLocale(old) })
					w := newWorld(t)
					sc.set(w)
					r := w.windowSized(nil, sz.w, sz.h)
					r.Play(widgettest.Settle())
					r.Frames(3)
					r.AuditLayout(name)
					if sz.w == 400 && lang == i18n.German && (sc.name == "ringing" || sc.name == "active") {
						shot(t, r.Rig, "min-"+sc.name+"-de")
					}
				})
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for ; n > 0; n /= 10 {
		i--
		b[i] = byte('0' + n%10)
	}
	return string(b[i:])
}
