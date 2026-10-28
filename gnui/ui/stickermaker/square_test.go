package stickermaker

import "testing"

func TestSquareStaysSquareAndInsideThePicture(t *testing.T) {
	sq := Centered(800, 600)
	if sq != (Square{100, 0, 600}) {
		t.Fatalf("Centered = %+v", sq)
	}
	// Dragging past an edge stops at it.
	if got := sq.Moved(-500, 40, 800, 600); got != (Square{0, 0, 600}) {
		t.Errorf("moved = %+v", got)
	}
	// Shrinking keeps the centre; growing past the picture stops at it.
	small := sq.Resized(200, 800, 600)
	if small != (Square{300, 200, 200}) {
		t.Errorf("resized = %+v", small)
	}
	if got := small.Moved(1000, 1000, 800, 600); got != (Square{600, 400, 200}) {
		t.Errorf("pushed = %+v", got)
	}
	if got := small.Resized(5000, 800, 600).Side; got != 600 {
		t.Errorf("grown past the picture = %d", got)
	}
	if got := Centered(0, 0).Side; got != 1 {
		t.Errorf("empty picture = %d", got)
	}
}

func TestSmallestSide(t *testing.T) {
	for _, c := range []struct{ w, h, want uint32 }{{800, 600, 75}, {4, 4, 1}, {0, 0, 1}} {
		if got := Smallest(c.w, c.h); got != c.want {
			t.Errorf("Smallest(%d, %d) = %d, want %d", c.w, c.h, got, c.want)
		}
	}
}

func TestCleanEmojis(t *testing.T) {
	var got []string
	for _, e := range CleanEmojis([]byte("hi 😂 ❤️ 😂 abc 👍🏽 🇧🇷")) {
		got = append(got, string(e))
	}
	want := []string{"😂", "❤️", "👍🏽", "🇧🇷"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("emoji %d = %q, want %q", i, got[i], want[i])
		}
	}
	if len(CleanEmojis([]byte("just words"))) != 0 {
		t.Error("words are not emojis")
	}
}
