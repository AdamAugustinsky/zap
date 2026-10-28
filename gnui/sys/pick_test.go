package sys

import (
	"reflect"
	"testing"

	"gonativeui/platform"

	"zapfast/abi"
)

func (r *rig) pickRequest(id uint64, kind abi.PickKind, set func(p *abi.EventPickRequest)) {
	r.t.Helper()
	r.pump(ev(abi.EventTagPickRequest, func(e *abi.Event) {
		e.PickRequest.ID, e.PickRequest.Kind = id, kind
		if set != nil {
			set(&e.PickRequest)
		}
	}))
}

func (r *rig) answers() []abi.CommandPickAnswer {
	var out []abi.CommandPickAnswer
	for _, c := range r.sent(abi.CommandTagPickAnswer) {
		a := c.PickAnswer
		a.Paths = append([][]byte(nil), a.Paths...)
		out = append(out, a)
	}
	return out
}

// CP-013: the core's request for files is a native open panel, and what the
// person chooses goes back to it.
func TestAFilePickIsAnOpenPanelAndTheChoiceGoesBackToTheCore(t *testing.T) {
	r := newRig(t)
	r.pickRequest(5, abi.PickKindFiles, func(p *abi.EventPickRequest) {
		p.Title = []byte("Send files")
		p.Filters = []abi.PickFilter{{Name: []byte("Images"), Extensions: [][]byte{[]byte("png"), []byte("jpg")}}, {Name: []byte("More"), Extensions: [][]byte{[]byte("*.png"), []byte(".gif")}}}
		p.Directory, p.HasDirectory = []byte("/Users/me/Pictures"), true
	})
	want := platform.OpenSpec{Files: true, Multiple: true, Types: []string{".png", ".jpg", ".gif"}, Start: "/Users/me/Pictures", Title: "Send files"}
	if len(r.panels.opens) != 1 || !reflect.DeepEqual(r.panels.opens[0], want) {
		t.Fatalf("%+v", r.panels.opens)
	}
	if len(r.answers()) != 0 {
		t.Fatal("answered before the panel ended")
	}
	if !r.y.AppEvent(nil, r.s, r.panels.ended(1, platform.DialogAnswered, "/a.png", "/b.jpg")) {
		t.Error("the panel's end was not taken")
	}
	got := r.answers()
	if len(got) != 1 || got[0].ID != 5 || len(got[0].Paths) != 2 || string(got[0].Paths[0]) != "/a.png" || string(got[0].Paths[1]) != "/b.jpg" {
		t.Errorf("%+v", got)
	}
}

func TestOneFileAFolderAndASavePlace(t *testing.T) {
	r := newRig(t)
	r.pickRequest(1, abi.PickKindFile, nil)
	r.pickRequest(2, abi.PickKindFolder, nil)
	r.pickRequest(3, abi.PickKindSave, func(p *abi.EventPickRequest) {
		p.FileName, p.HasFileName = []byte("photo.jpg"), true
		p.Filters = []abi.PickFilter{{Extensions: [][]byte{[]byte("jpg")}}}
	})
	if o := r.panels.opens; len(o) != 2 || !o[0].Files || o[0].Multiple || !o[1].Dirs || o[1].Files {
		t.Errorf("%+v", o)
	}
	if s := r.panels.saves; len(s) != 1 || s[0].Name != "photo.jpg" || !reflect.DeepEqual(s[0].Types, []string{".jpg"}) {
		t.Errorf("%+v", s)
	}
	r.y.AppEvent(nil, r.s, r.panels.ended(3, platform.DialogAnswered, "/out/photo.jpg"))
	r.y.AppEvent(nil, r.s, r.panels.ended(1, platform.DialogAnswered, "/x"))
	got := r.answers()
	if len(got) != 2 || got[0].ID != 3 || got[1].ID != 1 {
		t.Errorf("%+v", got)
	}
}

// Every request is answered: a cancelled panel and a panel that could not be
// shown answer with no paths.
func TestACancelledOrUnshownPanelAnswersWithNoPaths(t *testing.T) {
	r := newRig(t)
	r.pickRequest(8, abi.PickKindFile, nil)
	r.y.AppEvent(nil, r.s, r.panels.ended(1, platform.DialogCancelled, "/ignored"))
	r.panels.err = platform.ErrUnsupported
	r.pickRequest(9, abi.PickKindSave, nil)
	got := r.answers()
	if len(got) != 2 || got[0].ID != 8 || got[1].ID != 9 || len(got[0].Paths) != 0 || len(got[1].Paths) != 0 {
		t.Errorf("%+v", got)
	}
}

func TestAPanelNotOursIsLeftAlone(t *testing.T) {
	r := newRig(t)
	e := r.panels.ended(41, platform.DialogAnswered, "/x")
	if r.y.AppEvent(nil, r.s, e) {
		t.Error("took a dialog it did not ask for")
	}
}
