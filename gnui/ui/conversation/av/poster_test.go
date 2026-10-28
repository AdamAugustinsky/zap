package av_test

import (
	"bytes"
	"image"
	"image/png"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/ui/conversation/av"
	"zapfast/ui/screen"
)

// Account removal shifts the next account into the same slot. Both cores
// start their blob IDs at one, but their posters must remain independent.
func TestPosterAfterAccountSlotReuse(t *testing.T) {
	for _, freshImages := range []bool{false, true} {
		name := "resident image"
		if freshImages {
			name = "fresh image cache"
		}
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			old := video("vid", 10, "/m/old.mp4", false, false)
			r.poster(&old)
			r.load(old)
			r.until("old green poster", func() bool {
				c := r.at("vid", 20, 20)
				return int(c.G) > int(c.R)+60 && int(c.G) > int(c.B)+60
			})

			f := fake.New(nil)
			a := r.s.AddAccount("2", nil, f)
			m := video("vid", 10, "/m/new.mp4", false, false)
			im := image.NewRGBA(image.Rect(0, 0, 32, 18))
			for i := 0; i < len(im.Pix); i += 4 {
				im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = 200, 30, 200, 255
			}
			var buf bytes.Buffer
			if err := png.Encode(&buf, im); err != nil {
				t.Fatal(err)
			}
			ref, err := f.PutBlob(buf.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if ref.ID != old.Thumbnail.ID {
				t.Fatal("fixture blob IDs must collide")
			}
			m.Thumbnail, m.HasThumbnail = ref, true
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
			}))
			r.load(m)
			a.OpenChat(a.Model.Chats.Find([]byte(chatID)))
			r.until("replacement magenta poster", func() bool {
				c := r.at("vid", 20, 20)
				return int(c.R) > int(c.G)+80 && int(c.B) > int(c.G)+80
			})
			if len(r.videos) != 0 {
				t.Fatal("a thumbnail opened a video player")
			}
			r.p.quiet = true
			r.Frames(10)
			if n := testing.AllocsPerRun(30, func() { r.Frames(1) }); n != 0 {
				t.Errorf("steady poster frame allocates %v times", n)
			}
		})
	}
}

func TestPosterDecodedCacheSurvivesRawEviction(t *testing.T) {
	r := newRig(t)
	m := video("vid", 10, "/m/clip.mp4", false, false)
	r.poster(&m)
	r.load(m)
	r.until("green poster", func() bool { return green(r.at("vid", 20, 20)) })
	e := &r.p.env
	before := e.K.Images().Stats().Decodes
	hd := av.PosterHandle(e, r.view("vid"), 320, 180)
	if evicted, err := av.ChurnRawThumbnails(e, m.Thumbnail); err != nil || !evicted {
		t.Fatalf("raw entry eviction: %v, %v", evicted, err)
	}
	// Reload just the raw entry, without drawing or requesting another image.
	for deadline := time.Now().Add(2 * time.Second); !av.RawThumbnailReady(e, m.Thumbnail); {
		if time.Now().After(deadline) {
			t.Fatal("raw thumbnail did not reload")
		}
		time.Sleep(time.Millisecond)
	}
	if got := av.PosterHandle(e, r.view("vid"), 320, 180); got != hd {
		t.Errorf("raw eviction changed decoded image handle: %v -> %v", hd, got)
	}
	r.until("poster after raw reload", func() bool { return green(r.at("vid", 20, 20)) })
	if n := e.K.Images().Stats().Decodes; n != before {
		t.Errorf("raw eviction decoded again: %d -> %d", before, n)
	}
}

func TestPosterDecodedHitSkipsRawCache(t *testing.T) {
	r := newRig(t)
	m := video("vid", 10, "/m/clip.mp4", false, false)
	r.poster(&m)
	r.load(m)
	r.until("green poster", func() bool { return green(r.at("vid", 20, 20)) })
	if n := av.CountRawLookups(func() { r.Frames(1) }, 5); n != 0 {
		t.Errorf("five decoded-cache hit frames made %d raw lookups", n)
	}
	r.p.quiet = true
	r.Frames(10)
	if n := testing.AllocsPerRun(30, func() { r.Frames(1) }); n != 0 {
		t.Errorf("steady poster frame allocates %v times", n)
	}
}
