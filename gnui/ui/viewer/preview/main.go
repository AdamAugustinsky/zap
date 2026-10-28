// Command preview opens the media viewer window over a fake core, with a few
// generated pictures, for looking at it (and for GnuiShot).
//
//	go run ./ui/viewer/preview [-open m2] [-seconds 30] [-o out.png]
//
// m1..m3 are photos, m4 a clip with a thumbnail, m5 a photo not downloaded.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"time"

	"gonativeui/app"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
	"zapfast/ui/viewer"
)

const chat = "1@s.whatsapp.net"

func photo(dir, name string, w, h int, hue float64) string {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			r := 0.5 + 0.5*math.Sin(6*fx+hue)
			g := 0.5 + 0.5*math.Sin(5*fy+hue*1.7)
			b := 0.5 + 0.5*math.Sin(4*(fx+fy)+hue*2.3)
			m.Set(x, y, color.NRGBA{uint8(r * 255), uint8(g * 255), uint8(b * 255), 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, m)
	p := filepath.Join(dir, name)
	os.WriteFile(p, b.Bytes(), 0o644)
	return p
}

func thumb(w, h int, hue float64) []byte {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, color.NRGBA{uint8(40 + x*3%200), uint8(90 + y*2%120), uint8(120 + hue*20), 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, m)
	return b.Bytes()
}

func main() {
	open := flag.String("open", "m1", "the message to open on")
	secs := flag.Int("seconds", 30, "how long to stay")
	clip := flag.String("clip", "", "a video file for m4 (default: a stand-in that cannot play)")
	play := flag.Bool("play", false, "play the clip as soon as it shows")
	flag.Parse()
	dir, _ := os.MkdirTemp("", "viewer-preview")
	defer os.RemoveAll(dir)

	st := state.New()
	f := fake.New(nil)
	a := st.AddAccount("1", nil, f)
	items := []abi.MediaItem{
		{ID: []byte("m1"), Path: []byte(photo(dir, "a.png", 3000, 2000, 0.3)), HasPath: true},
		{ID: []byte("m2"), Path: []byte(photo(dir, "b.png", 1200, 1800, 1.1)), HasPath: true},
		{ID: []byte("m3"), Path: []byte(photo(dir, "c.png", 800, 600, 2.4)), HasPath: true},
		{ID: []byte("m4"), Video: true, Path: []byte(filepath.Join(dir, "d.mp4")), HasPath: true},
		{ID: []byte("m5")},
	}
	if *clip != "" {
		items[3].Path = []byte(*clip)
	} else {
		os.WriteFile(filepath.Join(dir, "d.mp4"), []byte("x"), 0o644)
	}
	for i := range items {
		if i == 3 || i == 0 || i == 4 {
			ref, _ := f.PutBlob(thumb(224, 168, float64(i)))
			items[i].Thumbnail, items[i].HasThumbnail = ref, true
		}
	}
	f.Emit(&abi.Event{Tag: abi.EventTagLink, Link: abi.EventLink{Status: abi.LinkStatus{Tag: abi.LinkStatusTagConnected}}},
		&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(chat), Name: []byte("Ada Lovelace"), LastActivity: 20}}}})
	st.Pump(0)
	ref := a.Model.Chats.Find([]byte(chat))
	a.OpenChat(ref)
	f.Reply(abi.CommandTagLoadChatMedia, &abi.Event{Tag: abi.EventTagChatMedia, ChatMedia: abi.EventChatMedia{Chat: []byte(chat), Batch: 1, Last: true, Items: items}})

	sc := viewer.New()
	sh, err := shell.New(st, &screen.Set{Viewer: sc})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	h := shell.NewApp(sh)
	st.Wake = h.Wake
	st.Win.ViewerChat = ref
	if *play {
		st.OpenViewerPlaying(ref, []byte(*open))
	} else {
		st.Win.ViewerMsg.Add([]byte(*open))
		st.OpenWindow(state.WinViewer)
	}
	go func() {
		time.Sleep(time.Duration(*secs) * time.Second)
		app.Post(app.Event{ID: 99}, nil)
	}()
	if err := app.Main(app.AppConfig{ID: "ai.a3lab.zap.viewer-preview"}, quitter{h}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// quitter ends the run when the timer posts.
type quitter struct{ *shell.App }

func (q quitter) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost && e.ID == 99 {
		a.Quit()
		return
	}
	q.App.Event(a, e)
}
