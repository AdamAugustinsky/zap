package login

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/i18n"
)

// The linking card in the longest languages, at the default window and at the
// smallest it goes: nothing is clipped or overlaps, and what does not fit
// scrolls (the card is in a scroll view). The layout audit judges it.
func TestEveryLinkingStateFitsInTheLongestLanguages(t *testing.T) {
	langs := []i18n.Locale{i18n.English, i18n.German, i18n.Russian, i18n.French}
	sizes := []struct{ w, h int }{{1100, 720}, {600, 400}}
	phone := func(s *abi.LinkStatus) {
		s.Tag = abi.LinkStatusTagUnlinked
		s.Unlinked.PairingPhone, s.Unlinked.HasPairingPhone = []byte("5511912345678"), true
	}
	variants := []struct {
		name string
		do   func(r *rig, t *testing.T)
	}{
		{"linking", func(r *rig, t *testing.T) { r.emit(t, unlinked("2@abcdef,ghij==")) }},
		{"linking, several accounts", func(r *rig, t *testing.T) {
			r.s.AddAccount("2", nil, fake.New(nil))
			r.emit(t, unlinked("2@abcdef,ghij=="))
		}},
		{"phone number", func(r *rig, t *testing.T) {
			r.emit(t, unlinked("2@abcdef,ghij=="))
			r.Click(ui.RoleLink, i18n.T("Link with phone number instead"))
			r.Frames(3)
		}},
		{"requesting a code", func(r *rig, t *testing.T) { r.emit(t, status(phone)) }},
		{"pairing", func(r *rig, t *testing.T) {
			r.emit(t, status(func(s *abi.LinkStatus) {
				phone(s)
				s.Unlinked.PairCode, s.Unlinked.HasPairCode = []byte("ABCD1234"), true
			}))
		}},
		{"pairing, several accounts", func(r *rig, t *testing.T) {
			r.s.AddAccount("2", nil, fake.New(nil))
			r.emit(t, status(func(s *abi.LinkStatus) {
				phone(s)
				s.Unlinked.PairCode, s.Unlinked.HasPairCode = []byte("ABCD1234"), true
			}))
		}},
		{"unlinked by the phone", func(r *rig, t *testing.T) {
			r.emit(t, status(func(s *abi.LinkStatus) { s.Tag = abi.LinkStatusTagLoggedOut }))
		}},
		{"failed, key lost", func(r *rig, t *testing.T) {
			r.emit(t, status(func(s *abi.LinkStatus) {
				s.Tag = abi.LinkStatusTagFailed
				s.Failed.Error = []byte("The archive is encrypted but its OS keyring key is missing. Restore the original keyring; the archive has not been changed")
			}))
		}},
	}
	for _, lang := range langs {
		for _, sz := range sizes {
			for _, v := range variants {
				name := lang.Tag() + "/" + v.name
				t.Run(name+"/"+itoa(sz.w), func(t *testing.T) {
					old := i18n.Current()
					i18n.SetLocale(lang)
					t.Cleanup(func() { i18n.SetLocale(old) })
					r := newRigAt(t, sz.w, sz.h)
					v.do(r, t)
					r.Frames(6)
					r.AuditLayout(name)
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

// In the smallest window the card scrolls, and the QR code comes whole into
// view at 260 pt, not scaled down.
func TestTheQRCodeScrollsWholeIntoViewInTheSmallestWindow(t *testing.T) {
	r := newRigAt(t, 600, 400)
	r.emit(t, unlinked("2@abcdef,ghij=="))
	qrBox := func() [4]float32 { return r.Must(ui.RoleImage, "Link this computer").Bounds }
	if b := qrBox(); b[3] <= 400 {
		t.Fatalf("the QR code is already whole in view at %v: the window is no longer too small for the card", b)
	}
	whole := false
	for i := 0; i < 30 && !whole; i++ {
		r.Play(widgettest.Wheel(ui.RoleScrollArea, "Login", -10, 1), widgettest.Idle(4))
		b := qrBox()
		whole = b[1] >= 0 && b[3] <= 400 && b[2]-b[0] >= 260 && b[3]-b[1] >= 260
	}
	if !whole {
		t.Errorf("scrolling never brought the whole QR code into view; it is at %v", qrBox())
	}
}
