package chatlist

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/ui"

	"zapfast/abi"
)

// writeSolid writes a w×w PNG of one colour at path, over what was there.
func writeSolid(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(im.Pix); i += 4 {
		im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, im); err != nil {
		t.Fatal(err)
	}
}

// avatarShows waits for the row's picture to be mostly colour c (red or
// blue), reporting whether it got there.
func (r *rig) avatarShows(row string, red bool) bool {
	for i := 0; i < 200; i++ {
		r.Frames(1)
		if n, ok := r.Find(ui.RoleListItem, row); ok {
			im := r.Image()
			reds, blues := 0, 0
			for y := int(n.Bounds[1]); y < int(n.Bounds[3]); y++ {
				for x := int(n.Bounds[0]); x < int(n.Bounds[0])+70 && x < int(n.Bounds[2]); x++ {
					p := im.RGBAAt(x, y)
					switch {
					case p.R > 200 && p.G < 60 && p.B < 60:
						reds++
					case p.B > 200 && p.R < 60 && p.G < 60:
						blues++
					}
				}
			}
			if red && reds > 300 && blues == 0 || !red && blues > 300 && reds == 0 {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// The core writes a JID's picture to the same file each time
// (zf:src/paths.rs:350): a new picture at the same path, after an ordinary
// Avatar update and after the number is unlinked and linked again, shows
// the new pixels, not the decoded old ones (zf:src/app.rs:3559).
func TestANewPictureAtTheSamePathShows(t *testing.T) {
	red, blue := color.RGBA{230, 20, 20, 255}, color.RGBA{20, 20, 230, 255}
	for _, relink := range []bool{false, true} {
		name := "update"
		if relink {
			name = "relink"
		}
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.chats(chat("1", "Ada", 30))
			path := filepath.Join(t.TempDir(), "ada.png")
			avatar := func() {
				r.f.Emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
					e.Avatar = abi.EventAvatar{ID: jid("1"), Path: []byte(path), HasPath: true}
				}))
			}
			writeSolid(t, path, red)
			avatar()
			if !r.avatarShows("Ada", true) {
				t.Fatal("the first picture never showed")
			}
			if relink {
				r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }))
				r.Frames(3)
				r.f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
				r.chats(chat("1", "Ada", 30))
			}
			writeSolid(t, path, blue)
			avatar()
			if !r.avatarShows("Ada", false) {
				t.Error("the new picture at the same path did not show")
			}
		})
	}
}
