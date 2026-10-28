package conversation

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
)

// all are the nodes with the role and the name, in build order.
func (r *rig) all(role ui.Role, name string) []ui.SemNode {
	t := r.Tree()
	var out []ui.SemNode
	for _, n := range t.Nodes {
		if n.Role == role && string(t.Text(n.Name)) == name {
			out = append(out, n)
		}
	}
	return out
}

func (r *rig) node(role ui.Role, name string) ui.SemNode {
	r.t.Helper()
	n, ok := r.Find(role, name)
	if !ok {
		r.t.Fatalf("no %v %q in\n%s", role, name, widgettest.Dump(r.Tree()))
	}
	return n
}

func inside(in, out [4]float32) bool {
	const eps = 0.5
	return in[0] >= out[0]-eps && in[1] >= out[1]-eps && in[2] <= out[2]+eps && in[3] <= out[3]+eps
}

// filled reports whether the last frame fills a shape exactly over b: a
// bubble's background.
func (r *rig) filled(b [4]float32) bool {
	for _, q := range r.Quads() {
		if q.Mode != gpu.ModeShape || q.Soft > 0 || q.Color[0]&0xFF == 0 {
			continue
		}
		d := float32(0)
		for i := range 4 {
			d = max(d, abs32(q.Rect[i]-b[i]))
		}
		if d < 1 {
			return true
		}
	}
	return false
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func picture(id string, ts int64, fromMe bool, caption string) abi.Message {
	m := msg(ada, id, ts, fromMe, "")
	m.Content = abi.Content{Tag: abi.ContentTagImage}
	m.Content.Image.Media = abi.Media{Mime: []byte("image/jpeg"), Size: 4000, Width: 800, HasWidth: true, Height: 600, HasHeight: true}
	if caption != "" {
		m.Content.Image.Caption, m.Content.Image.HasCaption = []byte(caption), true
	}
	return m
}

// Kinds' blocks sit inside their bubbles, the text where kinds says: a
// caption under its photo, a poll's question over its answers, a place's
// name only in its card. A sticker has no bubble behind it; a photo with no
// caption carries its time over its corner.
func TestBlocksAndTheirText(t *testing.T) {
	r := newRig(t)
	poll := msg(ada, "poll", base0+120, false, "")
	poll.Content = abi.Content{Tag: abi.ContentTagPoll, Poll: abi.ContentPoll{Question: []byte("Lunch?"),
		Options: [][]byte{[]byte("Pasta"), []byte("Pizza")}, State: abi.PollState{Selectable: 1, CanVote: true, HistoryComplete: true}}}
	loc := msg(ada, "loc", base0+180, false, "")
	loc.Content = abi.Content{Tag: abi.ContentTagLocation, Location: abi.ContentLocation{Latitude: 48.8584, Longitude: 2.2945,
		Name: []byte("Eiffel Tower"), HasName: true}}
	sticker := msg(ada, "st", base0+240, false, "")
	sticker.Content = abi.Content{Tag: abi.ContentTagSticker, Sticker: abi.ContentSticker{Media: abi.Media{Mime: []byte("image/webp"), Width: 512, HasWidth: true, Height: 512, HasHeight: true}}}
	r.open(ada,
		picture("p1", base0, false, "Sunset"),
		picture("p2", base0+60, true, ""),
		poll, loc, sticker,
		msg(ada, "t", base0+300, false, "plain words"),
	)
	r.Frames(3)

	// a photo with no caption: the footer over its corner, Message info on ours
	photo := r.node(ui.RoleButton, "Download photo").Bounds
	info := r.all(ui.RoleButton, "Message info")
	if len(info) != 1 || !inside(info[0].Bounds, photo) || !inside(photo, r.node(ui.RoleGroup, bubble("You", base0+60)).Bounds) {
		t.Fatalf("Message info %v not over the photo %v in its bubble", info, photo)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Message info"), widgettest.Idle(2))
	if r.s.Panes.MessageInfo.Len() != 1 || string(r.s.Panes.MessageInfo.At(0)) != "p2" {
		t.Errorf("Message info on %v", r.s.Panes.MessageInfo)
	}

	// the poll's question over its answers
	q := r.node(ui.RoleLabel, "Lunch?").Bounds
	if a := r.node(ui.RoleRadio, "Pasta").Bounds; q[3] > a[1] {
		t.Errorf("question %v not over the answers %v", q, a)
	}

	// the place's name once: its card draws it, the transcript does not
	if n := len(r.all(ui.RoleLabel, "Eiffel Tower")); n != 1 {
		t.Errorf("the place's name %d times", n)
	}

	// a sticker has no bubble behind it; a text has
	if !r.filled(r.node(ui.RoleGroup, bubble("Ada Lovelace", base0+300)).Bounds) {
		t.Fatal("no bubble behind the text: the check is blind")
	}
	if r.filled(r.node(ui.RoleGroup, bubble("Ada Lovelace", base0+240)).Bounds) {
		t.Error("a bubble behind the sticker")
	}

	// the first photo's block is in its bubble, its caption under it
	r.Play(widgettest.Key(ui.KeyHome, 0), widgettest.Idle(4))
	b1 := r.node(ui.RoleGroup, bubble("Ada Lovelace", base0)).Bounds
	photo = r.node(ui.RoleButton, "Download photo").Bounds
	if !inside(photo, b1) {
		t.Errorf("photo %v outside its bubble %v", photo, b1)
	}
	if cap := r.node(ui.RoleLabel, "Sunset").Bounds; cap[1] < photo[3] || !inside(cap, b1) {
		t.Errorf("caption %v not under the photo %v in the bubble", cap, photo)
	}
}

// A video not downloaded yet is package av's file row inside its bubble,
// with the caption under it; the transcript adds no line of its own.
func TestVideoBlockAndCaption(t *testing.T) {
	r := newRig(t)
	v := msg(ada, "v", base0, false, "")
	v.Content = abi.Content{Tag: abi.ContentTagVideo, Video: abi.ContentVideo{Caption: []byte("the match"), HasCaption: true,
		Media: abi.Media{Mime: []byte("video/mp4"), Size: 2_000_000}}}
	r.open(ada, v)
	b := r.node(ui.RoleGroup, bubble("Ada Lovelace", base0)).Bounds
	if d := r.node(ui.RoleLabel, "Video").Bounds; !inside(d, b) {
		t.Errorf("the video's block %v is not in its bubble %v", d, b)
	}
	if n := len(r.all(ui.RoleLabel, "Video")); n != 1 {
		t.Errorf("%d Video labels, want av's one", n)
	}
	r.must(ui.RoleLabel, "the match")
}
