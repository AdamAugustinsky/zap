package chatlist

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// CL-034: the label row routes a line wheel sideways without moving the
// filters (which fold rather than scroll). The existing swipe and Tab tests
// cover those input paths.
func TestPlainWheelScrollsTheLabelRow(t *testing.T) {
	r := newRig(t)
	r.s.Settings.SidebarWidth = 240
	r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
		for i := 0; i < 8; i++ {
			e.Labels.Labels = append(e.Labels.Labels, abi.Label{ID: []byte{byte('a' + i)}, Name: []byte("Long label " + string(rune('A'+i)))})
		}
		e.Labels.Batch, e.Labels.Last = 1, true
	}))
	r.chats(chat("1", "Ada", 1))
	start := r.Must(ui.RoleCheckbox, "Long label H").Bounds[0]
	allStart := r.Must(ui.RoleCheckbox, "All").Bounds[0]
	r.Play(widgettest.WheelNotch(ui.RoleScrollArea, "label-chips", -2, 1), widgettest.Idle(30))
	if end := r.Must(ui.RoleCheckbox, "Long label H").Bounds[0]; end >= start {
		t.Errorf("plain wheel did not move the labels: %v -> %v", start, end)
	}
	if end := r.Must(ui.RoleCheckbox, "All").Bounds[0]; end != allStart {
		t.Error("wheel on the labels also moved the filters")
	}
	r.sh.Set.Dialogs = modalSheet{}
	r.Frames(2)
	start = r.Must(ui.RoleCheckbox, "Long label H").Bounds[0]
	r.Play(widgettest.WheelNotch(ui.RoleScrollArea, "label-chips", 2, 1), widgettest.Idle(30))
	if r.Must(ui.RoleCheckbox, "Long label H").Bounds[0] != start {
		t.Error("wheel scrolled chips behind a dialog")
	}
}

// modalSheet is a modal over the whole window, as an open dialog is.
type modalSheet struct{}

func (modalSheet) Init(*screen.Env) error { return nil }
func (modalSheet) Build(e *screen.Env) {
	c := e.C()
	w, h := c.Size()
	c.BeginOverlay(ui.HashString(0, "sheet"), ui.OverlayModal, [4]float32{0, 0, float32(w), float32(h)}, ui.PlaceCenter)
	c.EndOverlay()
}

// CL-034: the label row's floating bar takes no room in either system
// scroller style, appears on hover, and dragging it reveals the hidden chips.
func TestChipScrollbarAppearsOnHoverAndDrags(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		r := newRig(t)
		r.s.Settings.SidebarWidth = 240
		r.Context().SetSystemValues(2, &platform.SystemValues{Known: true, LegacyScrollers: legacy})
		r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
			for i := 0; i < 8; i++ {
				e.Labels.Labels = append(e.Labels.Labels, abi.Label{ID: []byte{byte('a' + i)}, Name: []byte("Long label " + string(rune('A'+i)))})
			}
			e.Labels.Batch, e.Labels.Last = 1, true
		}))
		r.chats(chat("1", "Ada", 1))
		area := r.Must(ui.RoleScrollArea, "label-chips")
		if r.has(ui.RoleScrollBar, "Horizontal scroll bar") {
			t.Fatal("bar visible at rest")
		}
		r.Play(widgettest.Move(ui.RoleCheckbox, "Long label A"), widgettest.Idle(5))
		r.Play(widgettest.Click(ui.RoleCheckbox, "Long label A"), widgettest.Idle(3))
		if r.acct().Filter != state.FilterLabel {
			t.Fatal("hover scrollbar covered the chip's click target")
		}
		bar := r.Must(ui.RoleScrollBar, "Horizontal scroll bar")
		if n := testing.AllocsPerRun(20, func() { r.Frame() }); n != 0 {
			t.Errorf("steady hovered chips allocated %v times per frame", n)
		}
		if now := r.Must(ui.RoleScrollArea, "label-chips"); now.Bounds != area.Bounds {
			t.Fatal("hover bar took room from the row")
		}
		start := r.Must(ui.RoleCheckbox, "Long label H").Bounds[0]
		r.Play(widgettest.Down(ui.RoleScrollBar, "Horizontal scroll bar"),
			widgettest.MoveTo(bar.Bounds[2]-2, (bar.Bounds[1]+bar.Bounds[3])/2), widgettest.Idle(5), widgettest.Up(), widgettest.Idle(5))
		if end := r.Must(ui.RoleCheckbox, "Long label H").Bounds[0]; end >= start {
			t.Errorf("legacy=%v: dragging the bar did not scroll: %v -> %v", legacy, start, end)
		}
		r.Play(widgettest.MoveTo(800, 600), widgettest.Idle(5))
		if r.has(ui.RoleScrollBar, "Horizontal scroll bar") {
			t.Error("bar stays after leaving the row")
		}
	}
}

// The fade ends at the panel colour, not a hard cut through a chip.
func TestChipEdgeFades(t *testing.T) {
	defer ztheme.NewIn("", false)
	for _, tc := range []struct {
		name   string
		choice state.Theme
		dark   bool
	}{
		{"light", state.ThemeLight, false}, {"dark", state.ThemeDark, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.sh.Set.Theme = ztheme.NewIn("", false)
			r.s.Settings.Theme, r.s.Settings.SidebarWidth = tc.choice, 240
			r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true, Dark: tc.dark})
			r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
				e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{{ID: []byte("a"), Name: []byte("A very long label that overflows the whole row"), ColorHex: []byte("#3b82f6")}}}
			}))
			r.chats(chat("1", "Ada", 1))
			r.Play(widgettest.Pause(1500), widgettest.Idle(5))
			for _, row := range []string{"chip-filters", "label-chips"} {
				a := r.Must(ui.RoleScrollArea, row).Bounds
				x, y := int(a[2])-1, int(a[1]+(a[3]-a[1])/3)
				panel := r.Image().RGBAAt(x, int(a[1])-1)
				edge := r.Image().RGBAAt(x, y)
				// the fade's last pixel is a pixel's width short of nothing
				near := func(a, b uint8) bool { return a-b < 4 || b-a < 4 }
				panelled := near(edge.R, panel.R) && near(edge.G, panel.G) && near(edge.B, panel.B)
				if !panelled {
					t.Errorf("%s edge=%v panel=%v, want the fade", row, edge, panel)
				}
			}
			if dir := os.Getenv("ZAPFAST_CAPTURE"); dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				f, err := os.Create(filepath.Join(dir, "chip-parity-"+tc.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, r.Image().SubImage(image.Rect(45, 45, 320, 160))); err != nil {
					t.Fatal(err)
				}
				r.Play(widgettest.Move(ui.RoleCheckbox, "Unread"), widgettest.Idle(5))
				f, err = os.Create(filepath.Join(dir, "chip-parity-"+tc.name+"-hover.png"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, r.Image().SubImage(image.Rect(45, 45, 320, 160))); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
