package lock

import (
	"strconv"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/i18n"
	"zapfast/lockhash"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// The lock card in the longest languages, at the default window and at the
// smallest it goes: nothing is clipped or overlaps, and what does not fit
// scrolls. The layout audit judges it.
func TestEveryLockStateFitsInTheLongestLanguages(t *testing.T) {
	langs := []i18n.Locale{i18n.English, i18n.German, i18n.Russian, i18n.French}
	sizes := []struct{ w, h int }{{1100, 720}, {600, 400}}
	variants := []struct {
		name string
		do   func(r *rig, t *testing.T)
	}{
		{"password", func(r *rig, t *testing.T) {}},
		{"typed", func(r *rig, t *testing.T) { r.Play(widgettest.Type("hunter2"), widgettest.Idle(2)) }},
		{"wrong", func(r *rig, t *testing.T) { r.tryPassword(t, "nope") }},
		{"waiting", func(r *rig, t *testing.T) {
			for i := 0; i < 4; i++ {
				r.tryPassword(t, "nope")
			}
		}},
		{"forgot", func(r *rig, t *testing.T) {
			r.Click(ui.RoleLink, i18n.T("Forgot password? Unlink this computer"))
			r.Frames(3)
		}},
	}
	for _, lang := range langs {
		for _, sz := range sizes {
			for _, v := range variants {
				name := lang.Tag() + "/" + v.name
				t.Run(name+"/"+strconv.Itoa(sz.w), func(t *testing.T) {
					old := i18n.Current()
					i18n.SetLocale(lang)
					t.Cleanup(func() { i18n.SetLocale(old) })
					r := newRigAt(t, password, sz.w, sz.h)
					v.do(r, t)
					r.Frames(6)
					r.AuditLayout(name)
				})
			}
		}
	}
}

// newRigAt is newRig with a window of w × h points.
func newRigAt(t *testing.T, pw string, w, h int) *rig {
	t.Helper()
	r := &rig{clock: &clock{now: time.Unix(1_000_000, 0)}}
	r.s = state.New()
	r.s.Now = r.clock.Now
	r.s.Settings.AppLockHash = []byte(lockhash.AppVerifierWithRounds([]byte(pw), 10))
	r.f = fake.New(nil)
	r.f.Emit(link(abi.LinkStatusTagConnected), chats())
	r.s.AddAccount("1", nil, r.f)
	r.ls = New()
	sh, err := shell.New(r.s, &screen.Set{Lock: r.ls})
	if err != nil {
		t.Fatal(err)
	}
	sh.Traffic = true
	cfg := shell.Config(r.s)
	cfg.Width, cfg.Height = float32(w), float32(h)
	r.Rig = widgettest.Open(t, cfg, w, h, 1, &host{Shell: sh, ls: r.ls, s: r.s})
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(4)
	return r
}
