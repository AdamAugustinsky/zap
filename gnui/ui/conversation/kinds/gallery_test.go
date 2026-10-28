package kinds_test

import (
	"testing"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/ui/conversation/kinds"
	"zapfast/ui/screen"
)

// TestGallery draws the kinds side by side for a look, when KINDS_SHOT is set.
func TestGallery(t *testing.T) {
	loc := msg("loc", 10, false, abi.Content{Tag: abi.ContentTagLocation})
	loc.Content.Location = abi.ContentLocation{Latitude: 1.5, Longitude: 2, Name: []byte("Café Central"), HasName: true, Address: []byte("Rua A, 12"), HasAddress: true}
	pack := msg("pack", 11, true, abi.Content{Tag: abi.ContentTagStickerPack})
	pack.Content.StickerPack = abi.ContentStickerPack{Name: []byte("Cats"), Publisher: []byte("Ada"), Count: 3}
	pre := msg("pre", 12, false, abi.Content{Tag: abi.ContentTagText})
	pre.Content.Text = abi.ContentText{Text: []byte("look https://example.com/a"), HasPreview: true,
		Preview: abi.LinkPreview{URL: []byte("https://example.com/a"), Title: []byte("An example"), HasTitle: true, Description: []byte("About it"), HasDescription: true}}
	loc2 := loc
	loc2.ID, loc2.FromMe = []byte("loc2"), true
	r := newRig(t, document("doc", "", abi.MediaStateTagIdle), loc, loc2, contact("con", vcard, "Ada"), pack, pre)
	settle(r)
	r.shot("gallery-cards")

	r = newRig(t,
		poll("poll", 1, []uint64{1}, []uint64{3, 5, 1}, 9, true, true),
		poll("poll2", 0, nil, []uint64{3, 5, 1}, 9, true, true),
		interactive("ia", false, actions()),
		interactive("ca", false, carouselOf(4)),
	)
	settle(r)
	r.shot("gallery-interactive")
}

func TestGalleryOwn(t *testing.T) {
	p := poll("poll", 1, []uint64{1}, []uint64{3, 5, 1}, 9, true, true)
	i := interactive("ia", true, actions())
	c := interactive("ca", true, carouselOf(3))
	p.FromMe = true
	r := newRig(t, p, i, c)
	settle(r)
	r.shot("gallery-own")
}

func TestSizeAndHasAllocateNothingOnceWarm(t *testing.T) {
	loc := msg("loc", 10, false, abi.Content{Tag: abi.ContentTagLocation})
	loc.Content.Location = abi.ContentLocation{Latitude: 1.5, Longitude: 2, Name: []byte("Café Central"), HasName: true}
	r := newRig(t, document("doc", "", abi.MediaStateTagIdle), loc, contact("con", vcard, "Ada"),
		poll("poll", 1, nil, []uint64{1, 2, 3}, 6, true, true), interactive("ia", false, actions()), interactive("ca", false, carouselOf(3)))
	settle(r)
	var warm, allocs float64
	r.p.probe = func(e *screen.Env, v *model.Conversation) {
		size := func() {
			for i := 0; i < v.Len(); i++ {
				if m := v.At(i); kinds.Has(m) {
					kinds.Size(e, m, 330*e.K.Scale())
				}
			}
		}
		size()
		warm = 1
		allocs = testing.AllocsPerRun(20, size)
	}
	r.Frames(2)
	if warm == 0 {
		t.Fatal("the probe did not run")
	}
	if allocs != 0 {
		t.Errorf("Has and Size allocate %v times per pass once warm", allocs)
	}
}

func TestBuildAllocatesNothingOnceWarm(t *testing.T) {
	loc := msg("loc", 10, false, abi.Content{Tag: abi.ContentTagLocation})
	loc.Content.Location = abi.ContentLocation{Latitude: 1.5, Longitude: 2, Name: []byte("Café Central"), HasName: true}
	pre := msg("pre", 12, false, abi.Content{Tag: abi.ContentTagText})
	pre.Content.Text = abi.ContentText{Text: []byte("look https://example.com/a"), HasPreview: true,
		Preview: abi.LinkPreview{URL: []byte("https://example.com/a"), Title: []byte("An example"), HasTitle: true, Description: []byte("About it"), HasDescription: true}}
	r := newRig(t, document("doc", "", abi.MediaStateTagIdle), loc, pre, contact("con", vcard, "Ada"),
		poll("poll", 1, nil, []uint64{1, 2, 3}, 6, true, true), interactive("ia", false, actions()), interactive("ca", false, carouselOf(3)))
	settle(r)
	r.p.quiet = true
	r.Frames(5)
	if n := testing.AllocsPerRun(20, func() { r.Frames(1) }); n != 0 {
		t.Errorf("a steady frame of the kinds allocates %v times", n)
	}
}
