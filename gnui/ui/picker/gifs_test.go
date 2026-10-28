package picker

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

func gifsEvent(query string, ids ...string) *abi.Event {
	return ev(abi.EventTagGifs, func(e *abi.Event) {
		e.Gifs = abi.EventGifs{Query: []byte(query), Batch: 1, Last: true}
		for _, id := range ids {
			e.Gifs.Gifs = append(e.Gifs.Gifs, abi.Gif{ID: []byte(id), Mp4: []byte("https://media.giphy.com/" + id + ".mp4"), Width: 200, Height: 150})
		}
	})
}

// PK-005: opening the GIF tab asks for the trending GIFs, shows them, and
// sending one says so and closes the picker.
func TestGifsTrendingSearchAndSend(t *testing.T) {
	r := newRig(t)
	r.s.Settings.GiphyKey = []byte("  key123 ")
	r.s.Panes.Reply.Add([]byte(msgID))
	r.open(state.PickerGifs)
	asks := r.sent(abi.CommandTagSearchGifs)
	if len(asks) != 1 || len(asks[0].SearchGifs.Query) != 0 || string(asks[0].SearchGifs.Key) != "key123" {
		t.Fatalf("searches sent: %+v", asks)
	}
	if _, ok := r.Find(ui.RoleLabel, "Searching…"); !ok {
		t.Error("no Searching… while waiting")
	}
	r.emit(gifsEvent("", "g1", "g2", "g3", "g4"))
	if _, ok := r.Find(ui.RoleLabel, "Searching…"); ok {
		t.Error("still searching after the results came")
	}
	for _, id := range []string{"g1", "g2", "g3", "g4"} {
		if _, ok := r.Find(ui.RoleButton, id); !ok {
			t.Errorf("no tile for %s", id)
		}
	}
	// a search: Enter in the field asks, and results of another query are not shown
	r.Type("cats")
	r.Key(ui.KeyEnter, 0)
	r.Frames(2)
	asks = r.sent(abi.CommandTagSearchGifs)
	if len(asks) != 2 || string(asks[1].SearchGifs.Query) != "cats" {
		t.Fatalf("searches sent: %+v", asks)
	}
	r.emit(gifsEvent("", "stale"))
	if _, ok := r.Find(ui.RoleButton, "stale"); ok {
		t.Error("the results of an older search replaced the new one's")
	}
	r.emit(gifsEvent("cats", "c1", "c2"))
	if _, ok := r.Find(ui.RoleButton, "g1"); ok {
		t.Error("the old results stayed")
	}
	r.Click(ui.RoleButton, "c2")
	r.Frames(3)
	sent := r.sent(abi.CommandTagSendGif)
	if len(sent) != 1 || string(sent[0].SendGif.Gif.ID) != "c2" || string(sent[0].SendGif.Chat) != chatID ||
		string(sent[0].SendGif.Gif.Mp4) != "https://media.giphy.com/c2.mp4" || sent[0].SendGif.Gif.Width != 200 {
		t.Fatalf("GIFs sent: %+v", sent)
	}
	if !sent[0].SendGif.HasQuoting || string(sent[0].SendGif.Quoting.ID) != msgID {
		t.Errorf("the reply was not carried: %+v", sent[0].SendGif)
	}
	if r.s.Panes.Reply.Len() != 0 {
		t.Error("the reply stayed after it was sent")
	}
	if len(r.s.Toasts) == 0 || string(r.s.Toasts[len(r.s.Toasts)-1].Text) != "Sending GIF…" {
		t.Errorf("toasts %+v", r.s.Toasts)
	}
	if r.s.Panes.Picker != state.PickerNone || !r.s.Req.JumpToBottom {
		t.Error("the picker stayed, or the transcript did not follow the new message")
	}
}

// PK-005: with no results the tab says what to do; a search that failed says
// why.
func TestGifsEmptyAndFailed(t *testing.T) {
	r := newRig(t)
	r.s.Settings.GiphyKey = []byte("k")
	r.open(state.PickerGifs)
	r.emit(gifsEvent(""))
	if _, ok := r.Find(ui.RoleLabel, "Search for a GIF or browse trending results."); !ok {
		t.Error("no hint for an empty result")
	}
	r.emit(ev(abi.EventTagGifs, func(e *abi.Event) {
		e.Gifs = abi.EventGifs{Query: nil, Batch: 2, Last: true, HasError: true, Error: abi.GifFailure{Message: []byte("GIPHY request failed: offline")}}
	}))
	if _, ok := r.Find(ui.RoleLabel, "GIPHY request failed: offline"); !ok {
		t.Errorf("the failure is not shown:\n%s", widgettest.Dump(r.Tree()))
	}
}

// PK-006: without a key the tab asks for one and saves it to the settings,
// then searches with it.
func TestGifsAskForAKey(t *testing.T) {
	r := newRig(t)
	r.open(state.PickerGifs)
	if len(r.sent(abi.CommandTagSearchGifs)) != 0 {
		t.Error("searched without a key")
	}
	if _, ok := r.Find(ui.RoleLabel, "GIF search needs a GIPHY API key. Create a free key at developers.giphy.com and paste it here. It is saved in your settings."); !ok {
		t.Fatalf("no prompt:\n%s", widgettest.Dump(r.Tree()))
	}
	if _, ok := r.Find(ui.RoleSearchField, "Search GIFs via GIPHY"); ok {
		t.Error("a search field without a key")
	}
	r.Click(ui.RoleLink, "developers.giphy.com")
	r.wantOpened("https://developers.giphy.com/")
	r.Click(ui.RoleTextField, "GIPHY API key")
	r.Type("abc123")
	r.Frames(2)
	if string(r.s.Settings.GiphyKey) != "abc123" {
		t.Fatalf("the key was saved as %q", r.s.Settings.GiphyKey)
	}
	r.Key(ui.KeyEnter, 0)
	r.Frames(2)
	asks := r.sent(abi.CommandTagSearchGifs)
	if len(asks) != 1 || string(asks[0].SearchGifs.Key) != "abc123" {
		t.Fatalf("searches: %+v", asks)
	}
	r.emit(gifsEvent("", "g1"))
	if _, ok := r.Find(ui.RoleSearchField, "Search GIFs via GIPHY"); !ok {
		t.Error("no search once there is a key")
	}
}

// PK-006: a key GIPHY refuses is asked for again, with GIPHY's words.
func TestGifsRejectedKey(t *testing.T) {
	r := newRig(t)
	r.s.Settings.GiphyKey = []byte("wrong")
	r.open(state.PickerGifs)
	r.emit(ev(abi.EventTagGifs, func(e *abi.Event) {
		e.Gifs = abi.EventGifs{Batch: 1, Last: true, HasError: true, Error: abi.GifFailure{Message: []byte("GIPHY rejected the API key (error 403)."), BadKey: true}}
	}))
	for _, name := range []string{"GIPHY rejected the API key (error 403).", "This GIPHY API key was rejected. Create a free key at developers.giphy.com and paste it here. It is saved in your settings."} {
		if _, ok := r.Find(ui.RoleLabel, name); !ok {
			t.Errorf("no %q:\n%s", name, widgettest.Dump(r.Tree()))
		}
	}
}
