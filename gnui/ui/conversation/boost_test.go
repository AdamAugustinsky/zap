package conversation

import (
	"testing"

	"zapfast/state"
)

// A chat's boost changes how its conversation reads: the body in its face
// and size, your bubbles in its colour; none puts Zap's back.
func TestABoostedChat(t *testing.T) {
	r := newRig(t)
	r.open(ada, msg(ada, "in", base0, false, "hello"), msg(ada, "out", base0+60, true, "hi"))
	r.Frames(3)
	tall := func() float32 { // the tallest box named hello: its text
		tr, h := r.Tree(), float32(0)
		for _, n := range tr.Nodes {
			if string(tr.Text(n.Name)) == "hello" {
				h = max(h, n.Bounds[3]-n.Bounds[1])
			}
		}
		return h
	}
	plain, plainH := r.scr.look.body, tall()
	if plainH == 0 {
		t.Fatal("no text named hello")
	}
	if r.scr.look.own != 0 {
		t.Fatalf("an unboosted chat's bubbles have a fill of their own: %08X", r.scr.look.own)
	}
	r.a.SetBoost([]byte(ada), state.ChatBoost{Font: "serif", Size: 1.5, Tint: "teal"})
	r.Frames(3)
	lk := &r.scr.look
	if lk.body == plain || tall() < plainH*1.3 {
		t.Errorf("at 150%% the text is %v high, the usual %v", tall(), plainH)
	}
	if lk.own == 0 || lk.own&0xFF == 0xFF {
		t.Errorf("a teal boost gave your bubbles %08X, not a light teal", lk.own)
	}
	r.a.SetBoost([]byte(ada), state.ChatBoost{})
	r.Frames(3)
	if r.scr.look.body != plain || r.scr.look.own != 0 {
		t.Error("taking the boost away did not put Zap's look back")
	}
}
