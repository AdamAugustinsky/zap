package kinds_test

import (
	"image/color"
	"os"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/ui/conversation/kinds"
	"zapfast/ui/screen"
)

func TestThumbnailAfterAccountSlotReuse(t *testing.T) {
	for _, freshImages := range []bool{false, true} {
		name := "resident image"
		if freshImages {
			name = "fresh image cache"
		}
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			put := func(f *fake.Core, c color.RGBA) abi.Message {
				t.Helper()
				data, err := os.ReadFile(solid(t, "thumb.png", 40, 30, c))
				if err != nil {
					t.Fatal(err)
				}
				ref, err := f.PutBlob(data)
				if err != nil {
					t.Fatal(err)
				}
				m := photo("p", false, "", 800, 600, abi.MediaStateTagIdle)
				m.Thumbnail, m.HasThumbnail = ref, true
				return m
			}
			old := put(r.f, color.RGBA{30, 160, 60, 255})
			r.update(old)
			pixel := func() color.RGBA {
				x, y := r.centre("p")
				return r.pixel(x-60, y-60)
			}
			r.until("old green thumbnail", func() bool {
				c := pixel()
				return int(c.G) > int(c.R)+60 && int(c.G) > int(c.B)+60
			})
			f := fake.New(nil)
			a := r.s.AddAccount("2", nil, f)
			a.Settings.AutoDownload = false
			m := put(f, color.RGBA{200, 30, 200, 255})
			if m.Thumbnail.ID != old.Thumbnail.ID {
				t.Fatal("fixture blob IDs must collide")
			}
			r.f.Emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
			r.Frames(3)
			if a.Index != 0 || r.s.Acct() != a {
				t.Fatal("replacement account did not take slot zero")
			}
			r.a, r.f = a, f
			if freshImages {
				r.p.env.K.Images().Close(time.Second)
				ca := screen.NewImages(r.p.env.A, r.p.env.K, nil)
				t.Cleanup(func() { ca.Close(time.Second) })
			}
			f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
				e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(chatID), Name: []byte("Ada"), LastActivity: 1}}}
			}), ev(abi.EventTagMessages, func(e *abi.Event) {
				e.Messages = abi.EventMessages{Chat: []byte(chatID), Batch: 1, Last: true, Messages: []abi.Message{m}}
			}))
			r.Frames(3)
			a.OpenChat(a.Model.Chats.Find([]byte(chatID)))
			if _, ok := r.view("p").Thumbnail(); !ok {
				t.Fatal("replacement message has no thumbnail")
			}
			r.until("replacement magenta thumbnail", func() bool {
				c := pixel()
				return int(c.R) > int(c.G)+80 && int(c.B) > int(c.G)+80
			})
			r.p.quiet = true
			r.Frames(10)
			if n := testing.AllocsPerRun(30, func() { r.Frames(1) }); n != 0 {
				t.Errorf("steady thumbnail frame allocates %v times", n)
			}
		})
	}
}

func thumbnailRig(t *testing.T) (*rig, abi.BlobRef) {
	t.Helper()
	r := newRig(t)
	data, err := os.ReadFile(solid(t, "thumb.png", 40, 30, color.RGBA{30, 160, 60, 255}))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r.f.PutBlob(data)
	if err != nil {
		t.Fatal(err)
	}
	m := photo("p", false, "", 800, 600, abi.MediaStateTagIdle)
	m.Thumbnail, m.HasThumbnail = ref, true
	r.update(m)
	r.until("green thumbnail", func() bool { return greenThumbnail(r) })
	return r, ref
}

func greenThumbnail(r *rig) bool {
	x, y := r.centre("p")
	c := r.pixel(x-60, y-60)
	return int(c.G) > int(c.R)+60 && int(c.G) > int(c.B)+60
}

func TestThumbnailDecodedCacheSurvivesRawEviction(t *testing.T) {
	r, ref := thumbnailRig(t)
	e := &r.p.env
	before := e.K.Images().Stats().Decodes
	hd := kinds.ThumbnailHandle(e, ref, 320, 240)
	if evicted, err := kinds.ChurnRawThumbnails(e, ref); err != nil || !evicted {
		t.Fatalf("raw entry eviction: %v, %v", evicted, err)
	}
	for deadline := time.Now().Add(2 * time.Second); !kinds.RawThumbnailReady(e, ref); {
		if time.Now().After(deadline) {
			t.Fatal("raw thumbnail did not reload")
		}
		time.Sleep(time.Millisecond)
	}
	if got := kinds.ThumbnailHandle(e, ref, 320, 240); got != hd {
		t.Errorf("raw eviction changed decoded image handle: %v -> %v", hd, got)
	}
	r.until("thumbnail after raw reload", func() bool { return greenThumbnail(r) })
	if n := e.K.Images().Stats().Decodes; n != before {
		t.Errorf("raw eviction decoded again: %d -> %d", before, n)
	}
}

func TestThumbnailDecodedHitSkipsRawCache(t *testing.T) {
	r, _ := thumbnailRig(t)
	if n := kinds.CountRawLookups(func() { r.Frames(1) }, 5); n != 0 {
		t.Errorf("five decoded-cache hit frames made %d raw lookups", n)
	}
	r.p.quiet = true
	r.Frames(10)
	if n := testing.AllocsPerRun(30, func() { r.Frames(1) }); n != 0 {
		t.Errorf("steady thumbnail frame allocates %v times", n)
	}
}
