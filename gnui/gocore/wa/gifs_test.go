package wa

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow"

	"zapfast/abi"
)

// giphy is a GIPHY of one GIF, which takes the key "k".
func giphy(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/gifs/search", "/v1/gifs/trending":
			if r.URL.Query().Get("api_key") != "k" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if r.URL.Path == "/v1/gifs/search" && r.URL.Query().Get("q") != "happy cats" {
				t.Errorf("query %q", r.URL.RawQuery)
			}
			fmt.Fprintf(w, `{"meta":{"status":200,"msg":"OK"},"data":[{"id":"g/1","images":{
				"fixed_width":{"mp4":"%[1]s/clip.mp4","width":"200","height":"150"},
				"fixed_width_small_still":{"url":"%[1]s/still.jpg"}}}]}`, srv.URL)
		case "/still.jpg":
			w.Write([]byte("\xff\xd8\xff a still"))
		case "/clip.mp4":
			w.Write([]byte("an mp4"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func gifsOf(t *testing.T, evs []abi.Event) abi.EventGifs {
	t.Helper()
	return one(t, evs, abi.EventTagGifs).Gifs
}

func TestSearchGifs(t *testing.T) {
	h := linked(t)
	srv := giphy(t)
	h.e.opts.Web, h.e.giphy = srv.Client(), srv.URL+"/v1/gifs"

	h.send(abi.Command{Tag: abi.CommandTagSearchGifs, SearchGifs: abi.CommandSearchGifs{Query: []byte("cats")}})
	if g := gifsOf(t, h.take()); !g.HasError || !g.Error.BadKey || string(g.Error.Message) != "GIF search needs a GIPHY API key." || !g.Last {
		t.Fatalf("no key: %+v", g)
	}

	h.send(abi.Command{Tag: abi.CommandTagSearchGifs, SearchGifs: abi.CommandSearchGifs{Query: []byte(" happy cats "), Key: []byte("k")}})
	g := gifsOf(t, h.take())
	if g.HasError || string(g.Query) != " happy cats " || len(g.Gifs) != 1 {
		t.Fatalf("found: %+v", g)
	}
	gif := g.Gifs[0]
	if string(gif.ID) != "g/1" || string(gif.Mp4) != srv.URL+"/clip.mp4" || gif.Width != 200 || gif.Height != 150 || !gif.HasStill || filepath.Base(string(gif.Still)) != "g_1.jpg" {
		t.Fatalf("gif: %+v", gif)
	}
	if _, err := os.Stat(string(gif.Still)); err != nil {
		t.Fatal(err)
	}

	h.send(abi.Command{Tag: abi.CommandTagSearchGifs, SearchGifs: abi.CommandSearchGifs{Key: []byte("bad")}})
	if g := gifsOf(t, h.take()); !g.HasError || !g.Error.BadKey || string(g.Error.Message) != "GIPHY rejected the API key (error 403)." {
		t.Fatalf("bad key: %+v", g)
	}
}

func TestSendGif(t *testing.T) {
	h := linked(t)
	srv := giphy(t)
	h.e.opts.Web = srv.Client()
	gif := abi.Gif{ID: []byte("g1"), Mp4: []byte(srv.URL + "/clip.mp4"), Width: 200, Height: 150}
	h.send(abi.Command{Tag: abi.CommandTagSendGif, SendGif: abi.CommandSendGif{Chat: chatID(peer), Gif: gif}})
	h.take()
	v := h.f.lastSent().msg.GetVideoMessage()
	if !v.GetGifPlayback() || v.GetWidth() != 200 || v.GetHeight() != 150 || v.GetMimetype() != "video/mp4" || h.f.uploads[len(h.f.uploads)-1] != whatsmeow.MediaVideo || string(h.f.uploaded[len(h.f.uploaded)-1]) != "an mp4" {
		t.Fatalf("sent: %v", v)
	}

	h.f.setConnected(false)
	h.send(abi.Command{Tag: abi.CommandTagSendGif, SendGif: abi.CommandSendGif{Chat: chatID(peer), Gif: gif}})
	if r := one(t, h.take(), abi.EventTagSendRefused).SendRefused; r.Reason != abi.RefusalOffline || r.Unsent.Tag != abi.UnsentTagGif {
		t.Fatalf("refused: %+v", r)
	}
}
