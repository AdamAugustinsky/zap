package cropper

import (
	"strconv"
	"testing"

	"zapfast/i18n"
	"zapfast/state"
)

// The cropper's controls in the languages with the longest words, at the
// default window and at the narrowest one: nothing runs past the window or
// overlaps (the framework's layout audit).
func TestCropperFitsInTheLongestLanguages(t *testing.T) {
	for _, lang := range []i18n.Locale{i18n.English, i18n.German, i18n.Russian, i18n.French} {
		for _, sz := range []struct{ w, h int }{{760, 700}, {480, 700}} {
			name := lang.Tag() + "/" + strconv.Itoa(sz.w)
			t.Run(name, func(t *testing.T) {
				old := i18n.Current()
				i18n.SetLocale(lang)
				t.Cleanup(func() { i18n.SetLocale(old) })
				r := newRigSize(t, sz.w, sz.h, linkedEvent())
				r.stagePasted(3, 800, 600)
				r.open(state.Dialog{Kind: state.DialogCropper, N: 3})
				r.waitFor(i18n.T("Keep this crop"))
				r.Frames(3)
				r.AuditLayout(name)
			})
		}
	}
}
