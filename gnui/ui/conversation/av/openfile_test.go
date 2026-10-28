package av_test

import (
	"os"
	"path/filepath"
	"testing"

	"gonativeui/ui"
)

// mediaFile is a regular file named name in a temporary folder: what
// state.OpenFile hands to the system player (the fake opener here).
func mediaFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("not really media"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A "video" the system cannot play goes to the system player only if it is
// a regular file of a known format: a command posing as a clip, or an .mp4
// that is a symbolic link to one, is never launched (state.OpenFile); the
// note says to open it from its folder.
func TestAVideoThatIsNotOneIsNeverLaunched(t *testing.T) {
	command := mediaFile(t, "clip.command")
	link := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.Symlink(command, link); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"a command": command, "a symlink named mp4": link} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.badVideos = map[string]bool{path: true}
			m := video("odd", 10, path, false, false)
			r.poster(&m)
			r.load(m)
			r.Click(ui.RoleButton, "Video")
			r.until("the safety note", func() bool {
				for _, ts := range r.s.Toasts {
					if string(ts.Text) == "For safety, open this file yourself from its folder" {
						return true
					}
				}
				return false
			})
			if got := r.sh.opened(); len(got) != 0 {
				t.Errorf("launched %q", got)
			}
		})
	}
}

// A motion photo's clip the system cannot play goes to its player only if it
// is a regular file of a known format: a command posing as the clip, or a
// .mov that is a symbolic link to one, is never launched (MK-018 through
// state.OpenFile); the note says to open it from its folder.
func TestAMotionClipThatIsNotOneIsNeverLaunched(t *testing.T) {
	command := mediaFile(t, "motion.command")
	link := filepath.Join(t.TempDir(), "motion.mov")
	if err := os.Symlink(command, link); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"a command": command, "a symlink named mov": link} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.badVideos = map[string]bool{path: true}
			r.load(r.photo("mp", 10, path))
			r.Play(clickMotion(0))
			r.until("the safety note", func() bool {
				for _, ts := range r.s.Toasts {
					if string(ts.Text) == "For safety, open this file yourself from its folder" {
						return true
					}
				}
				return false
			})
			if got := r.sh.opened(); len(got) != 0 || r.openVideos() != 0 {
				t.Errorf("launched %q; %d players kept", got, r.openVideos())
			}
		})
	}
}
