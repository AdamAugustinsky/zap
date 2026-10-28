package settings

import (
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/i18n"
	"zapfast/state"
)

// The longest languages (and Indonesian, for its long words) at the default
// window width and at the narrowest the window goes: no control runs past its
// card, no label is clipped, nothing overlaps. The window is tall enough to
// lay out the whole page at once.
func TestEverySectionFitsInTheLongestLanguages(t *testing.T) {
	langs := []i18n.Locale{i18n.English, i18n.German, i18n.Russian, i18n.French, i18n.Indonesian}
	sizes := []struct{ w, h int }{{760, 4200}, {480, 4600}}
	for _, lang := range langs {
		for _, sz := range sizes {
			for _, variant := range []string{"linked", "several accounts", "app lock form and profile editor"} {
				name := lang.Tag() + "/" + variant
				t.Run(name+"/"+itoa(sz.w), func(t *testing.T) {
					old := i18n.Current()
					i18n.SetLocale(lang)
					t.Cleanup(func() { i18n.SetLocale(old) })
					r := newRig(t, sz.w, sz.h, func(s *state.State, f *fake.Core, scr *Screen) {
						if variant == "several accounts" {
							s.AddAccount("2", nil, fake.New(nil))
						}
					})
					connect(r)
					r.emit(ev(abi.EventTagAccountPrivacy, func(e *abi.Event) {
						e.AccountPrivacy.Values = []abi.PrivacyValue{{Kind: abi.PrivacyKindLastSeen, Choice: abi.PrivacyChoiceExcept}, {Kind: abi.PrivacyKindGroupAdd, Choice: abi.PrivacyChoiceContacts}}
					}), ev(abi.EventTagStorageStats, func(e *abi.Event) {
						e.StorageStats.HasStats = true
						e.StorageStats.Stats = abi.StorageStats{Images: 3, ImageBytes: 3 << 20, Videos: 1, VideoBytes: 2048, Other: 2, OtherBytes: 10}
					}))
					switch variant {
					case "app lock form and profile editor":
						r.s.Settings.AppLockHash = nil
						r.click(ui.RoleButton, i18n.T("Set password…"))
						r.click(ui.RoleButton, i18n.T("Edit your name and About"))
					}
					r.Frames(4)
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
