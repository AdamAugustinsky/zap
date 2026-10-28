package stickermaker

import (
	"strconv"
	"testing"

	"zapfast/i18n"
)

// The sticker maker in the languages with the longest words, at the default
// window and at the narrowest one: no control runs past its dialog, nothing
// overlaps (the framework's layout audit). With a chat open it has three
// buttons, the row that wraps.
func TestStickerMakerFitsInTheLongestLanguages(t *testing.T) {
	for _, lang := range []i18n.Locale{i18n.English, i18n.German, i18n.Russian, i18n.French} {
		for _, sz := range []struct{ w, h int }{{760, 800}, {480, 800}} {
			for _, open := range []bool{false, true} {
				name := lang.Tag() + "/" + strconv.Itoa(sz.w) + "/chat=" + strconv.FormatBool(open)
				t.Run(name, func(t *testing.T) {
					old := i18n.Current()
					i18n.SetLocale(lang)
					t.Cleanup(func() { i18n.SetLocale(old) })
					r := newRigSize(t, sz.w, sz.h, linkedEvent(), chatsEvent(chat("1@s.whatsapp.net", "Ada Lovelace", 1)))
					if open {
						r.a.OpenChat(r.a.Model.Chats.Find([]byte("1@s.whatsapp.net")))
					}
					r.f.Emit(pictureEvent(800, 600, true))
					r.Frames(4)
					r.Frames(3)
					r.AuditLayout(name)
				})
			}
		}
	}
}
