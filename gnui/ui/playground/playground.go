// Package playground is the design playground: a window of sliders over the
// tunables (zapfast/theme, tune.go) that the screens read each frame, so a
// change shows in the main window at once. Spacing and radii move between
// the grid's steps; the rest by their own steps. The values are kept in a
// JSON file, read when the window opens and written as they change, and
// Copy as code puts the ones moved from the code's defaults on the
// clipboard as Go, for tune.go.
//
// It is a dev build's (cmd/zapfast -playground FILE), never a release's.
package playground

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gonativeui/app"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// Screen is the playground's window.
type Screen struct {
	path    string
	knobs   []ztheme.Knob
	pos     []float32 // each knob's slider: its value, or its step's index
	scroll  base.ScrollState
	saved   uint32 // the tunables' Gen last written to path
	dirtyAt time.Time
	note    string // what the last action did, under the buttons
	err     string
}

// New is the playground keeping its values in path ("" keeps them in memory).
func New(path string) *Screen { return &Screen{path: path} }

// Init reads the values kept in the file, if there is one.
func (s *Screen) Init(e *screen.Env) error {
	if s.knobs != nil {
		return nil
	}
	s.knobs = ztheme.Knobs()
	s.pos = make([]float32, len(s.knobs))
	s.load()
	for i := range s.knobs {
		s.pos[i] = position(&s.knobs[i])
	}
	s.saved = ztheme.Gen
	return nil
}

// Config is the window: narrow and tall, beside the main one.
func (s *Screen) Config(st *state.State) app.WindowConfig {
	cfg := app.WindowConfig{Title: "Design playground", Width: 400, Height: 820, MinWidth: 340, MinHeight: 400, Materials: true}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(toolbarStyle)
	return cfg
}

const toolbarStyle = theme.ToolbarRex

// Build builds the window.
func (s *Screen) Build(e *screen.Env) {
	c := e.C()
	w, h := c.Size()
	if w <= 0 || h <= 0 {
		return
	}
	W, H := float32(w), float32(h)
	k := e.K.Kit
	r := k.Theme()
	e.Begin(screen.Rect{W: W, H: H}, "Playground")
	c.NextBG(r.Colors[theme.ControlBackground])
	theme.At(c, 0, 0, W, H)
	c.Push(c.Box(base.Floating|ui.DrawBackground, "window"))
	tbH := theme.Snap(r.Window.Toolbars[toolbarStyle].Height)

	k.NextSize(ui.Px(W), ui.Px(H))
	k.NextScrollInset(tbH)
	v := k.BeginScroll("Knobs", &s.scroll, base.ScrollVertical)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextPadding(e.Pt(ztheme.Space4), e.Pt(ztheme.Space3), e.Pt(ztheme.Space4), e.Pt(ztheme.Space6))
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(ztheme.Space2))
	c.Push(c.Box(0, "knobs"))
	s.actions(e)
	group := ""
	for i := range s.knobs {
		kn := &s.knobs[i]
		if kn.Group != group {
			group = kn.Group
			c.NextSize(ui.Px(1), ui.Px(e.Pt(ztheme.Space2)))
			c.Box(0, c.Fmt().Str("before ").Str(group).String())
			rexui.Label(k, group, rexui.LabelOpts{Style: rexui.TextHeadline})
		}
		s.knob(e, i)
	}
	c.Pop()
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k, &v)
	k.CloseScroll(&v)

	tb := rexui.BeginToolbar(k, "Toolbar", rexui.ToolbarOpts{Style: toolbarStyle, Title: "Design playground", ContentEdge: true,
		BandFill: r.Colors[theme.ControlBackground]})
	tb.Title()
	tb.Flexible()
	tb.End()
	c.Pop()
	e.End()
	s.save()
}

// actions are the buttons over the knobs, and what the last one did.
func (s *Screen) actions(e *screen.Env) {
	k := e.K
	k.NextGap(ztheme.Space2)
	k.NextWidth(ui.Pct(1))
	k.Row("actions")
	if rexui.Button(k.Kit, "Copy as code", rexui.ButtonOpts{Variant: rexui.ButtonPrimary}).Clicked {
		if set := e.C().ClipboardSet; set != nil {
			set([]byte(s.code()))
			s.note = "Copied the moved values as Go for theme/tune.go."
		}
	}
	if rexui.Button(k.Kit, "Reset all", rexui.ButtonOpts{}).Clicked {
		ztheme.T = ztheme.Defaults
		for i := range s.knobs {
			s.pos[i] = position(&s.knobs[i])
		}
		s.changed(e)
		s.note = "Back to the code's values."
	}
	k.EndRow()
	msg := s.note
	if s.err != "" {
		msg = s.err
	} else if msg == "" && s.path != "" {
		msg = "Kept in " + s.path
	}
	if msg != "" {
		rexui.Label(k.Kit, msg, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2), Wrap: true})
	}
}

// knob builds knob i: its label and value, its note, its slider.
func (s *Screen) knob(e *screen.Env, i int) {
	k := e.K
	c := e.C()
	kn := &s.knobs[i]
	moved := *kn.V != kn.Def
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(ztheme.Space1))
	c.Push(c.Box(0, kn.ID))

	k.NextGap(ztheme.Space2)
	k.NextWidth(ui.Pct(1))
	k.NextCross(ui.CrossCenter)
	k.Row("head")
	rexui.Label(k.Kit, kn.Label, rexui.LabelOpts{})
	k.Spacer()
	col := rexui.Of(theme.Label2)
	if moved {
		col = rexui.Of(theme.Accent)
		if rexui.Button(k.Kit, "Reset###reset "+kn.ID, rexui.ButtonOpts{Variant: rexui.ButtonPlain, Size: theme.SizeSmall}).Clicked {
			*kn.V = kn.Def
			s.pos[i] = position(kn)
			s.changed(e)
		}
	}
	rexui.Label(k.Kit, value(kn), rexui.LabelOpts{Color: col, MetaPt: 12})
	k.EndRow()

	lo, hi, step := kn.Min, kn.Max, kn.Step
	if len(kn.Steps) > 0 {
		lo, hi, step = 0, float32(len(kn.Steps)-1), 1
	}
	if rexui.Slider(k.Kit, kn.Label+"###slider "+kn.ID, &s.pos[i], lo, hi, step, rexui.SliderOpts{}).Changed {
		v := s.pos[i]
		if len(kn.Steps) > 0 {
			v = kn.Steps[min(max(int(v+0.5), 0), len(kn.Steps)-1)]
		}
		if v != *kn.V {
			*kn.V = v
			s.changed(e)
		}
	}
	if kn.Note != "" {
		rexui.Label(k.Kit, kn.Note, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2), Wrap: true})
	}
	c.Pop()
}

// changed tells the app the tunables moved: the theme carries them into Rex
// and every window draws again.
func (s *Screen) changed(e *screen.Env) {
	ztheme.Changed()
	s.note = ""
	s.dirtyAt = time.Now()
	e.S.Touch()
	e.S.Wake()
}

// position is knob kn's slider position for its value: the value itself, or
// the index of the step nearest it.
func position(kn *ztheme.Knob) float32 {
	if len(kn.Steps) == 0 {
		return *kn.V
	}
	best := 0
	for i, st := range kn.Steps {
		if abs(st-*kn.V) < abs(kn.Steps[best]-*kn.V) {
			best = i
		}
	}
	return float32(best)
}

func abs(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// value is kn's value as it shows: "12 pt", "0.25 s", "0.05".
func value(kn *ztheme.Knob) string {
	v := strconv.FormatFloat(float64(*kn.V), 'f', -1, 32)
	if kn.Unit != "" {
		v += " " + kn.Unit
	}
	return v
}

// code is the values moved from the code's defaults, as Go for tune.go's
// Defaults, one field a line.
func (s *Screen) code() string {
	var b strings.Builder
	b.WriteString("// Design playground: the values moved from the code's defaults (theme/tune.go, Defaults)\n")
	n := 0
	for i := range s.knobs {
		kn := &s.knobs[i]
		if *kn.V == kn.Def {
			continue
		}
		n++
		b.WriteString(field(kn.ID))
		b.WriteString(": ")
		b.WriteString(strconv.FormatFloat(float64(*kn.V), 'f', -1, 32))
		b.WriteString(", // was ")
		b.WriteString(strconv.FormatFloat(float64(kn.Def), 'f', -1, 32))
		b.WriteString("\n")
	}
	if n == 0 {
		b.WriteString("// (nothing moved)\n")
	}
	return b.String()
}

// field is the Tunables field of knob id: its id with a capital.
func field(id string) string { return strings.ToUpper(id[:1]) + id[1:] }

// load reads the values kept in the file into T.
func (s *Screen) load() {
	if s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return // none yet: the code's values
	}
	var m map[string]float32
	if err := json.Unmarshal(b, &m); err != nil {
		s.err = "Could not read " + s.path + ": " + err.Error()
		return
	}
	for i := range s.knobs {
		if v, ok := m[s.knobs[i].ID]; ok {
			*s.knobs[i].V = v
		}
	}
	ztheme.Changed()
}

// save writes the values to the file once they have stopped moving for a
// moment (a drag writes once, when it rests).
func (s *Screen) save() {
	if s.path == "" || s.saved == ztheme.Gen || time.Since(s.dirtyAt) < 300*time.Millisecond {
		return
	}
	s.saved = ztheme.Gen
	m := make(map[string]float32, len(s.knobs))
	ids := make([]string, 0, len(s.knobs))
	for i := range s.knobs {
		m[s.knobs[i].ID] = *s.knobs[i].V
		ids = append(ids, s.knobs[i].ID)
	}
	sort.Strings(ids)
	b, err := json.MarshalIndent(m, "", "  ")
	if err == nil {
		err = os.WriteFile(s.path, append(b, '\n'), 0o644)
	}
	if err != nil {
		s.err = "Could not save " + s.path + ": " + err.Error()
	} else {
		s.err = ""
	}
}
