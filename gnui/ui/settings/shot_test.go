package settings

import (
	"fmt"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
	"image"
	"image/png"
	"os"
	"testing"
	"zapfast/abi"
	"zapfast/i18n"
)

// TestShots writes screenshots when ZF_SHOTS names a directory (a look, not a
// gate).
func TestShots(t *testing.T) {
	dir := os.Getenv("ZF_SHOTS")
	if dir == "" {
		t.Skip("ZF_SHOTS not set")
	}
	save := func(r *rig, name string) {
		f, err := os.Create(dir + "/" + name + ".png")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, r.Image()); err != nil {
			t.Fatal(err)
		}
	}
	r := newRig(t, 760, 900, nil)
	connect(r)
	save(r, "top")
	for i, dy := range []float32{-120, -120, -120, -120} {
		r.Play(widgettest.Wheel(ui.RoleScrollArea, "Settings", dy, 3))
		r.Frames(3)
		save(r, "scroll"+string(rune('1'+i)))
	}
	n := newRig(t, 480, 900, nil)
	connect(n)
	save(n, "narrow")
}

// TestLanguageShots writes the whole page in the longest languages, in
// slices of 900 pixels, when ZF_SHOTS names a directory (a look, not a gate;
// TestEverySectionFitsInTheLongestLanguages is the gate).
func TestLanguageShots(t *testing.T) {
	dir := os.Getenv("ZF_SHOTS")
	if dir == "" {
		t.Skip("ZF_SHOTS not set")
	}
	for _, lang := range []i18n.Locale{i18n.German, i18n.Russian, i18n.French, i18n.Indonesian} {
		for _, w := range []int{760, 480} {
			i18n.SetLocale(lang)
			r := newRig(t, w, 4600, nil)
			connect(r)
			r.emit(ev(abi.EventTagAccountPrivacy, func(e *abi.Event) {
				e.AccountPrivacy.Values = []abi.PrivacyValue{{Kind: abi.PrivacyKindLastSeen, Choice: abi.PrivacyChoiceExcept}, {Kind: abi.PrivacyKindGroupAdd, Choice: abi.PrivacyChoiceContacts}}
			}), ev(abi.EventTagStorageStats, func(e *abi.Event) {
				e.StorageStats.HasStats = true
				e.StorageStats.Stats = abi.StorageStats{Images: 3, ImageBytes: 3 << 20, Videos: 1, VideoBytes: 2048, Other: 2, OtherBytes: 10}
			}))
			r.Frames(4)
			img := r.Image()
			b := img.Bounds()
			for i, y := 0, 0; y < b.Dy(); i, y = i+1, y+900 {
				f, err := os.Create(fmt.Sprintf("%s/%s-%d-%d.png", dir, lang.Tag(), w, i))
				if err != nil {
					t.Fatal(err)
				}
				sub := img.SubImage(image.Rect(b.Min.X, b.Min.Y+y, b.Max.X, min(b.Min.Y+y+900, b.Max.Y)))
				if err := png.Encode(f, sub); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
			i18n.SetLocale(i18n.English)
		}
	}
}
