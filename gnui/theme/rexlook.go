package theme

import (
	"gonativeui/rexui"
	"gonativeui/text"
	gtheme "gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/widget"
)

// ZapFast is Rex only (Superlogical's look) in WhatsApp's green: where
// upstream and Messages fill with the accent, your bubbles and the open
// chat's row, Rex lays the accent lightly over the pane, glass included, and
// the text keeps its own colours. The recipes are rexui's, set once for every window before the
// first frame.
func init() {
	tuneRecipes()
	rexui.BubbleLook.Outgoing = rexui.OfAlpha(gtheme.Accent, ownTint, ownTintDark)
	rexui.BubbleLook.TextOut = rexui.Of(gtheme.Label)
	rexui.ChatRowLook.Selected = rexui.OfAlpha(gtheme.Accent, rowTint, rowTintDark)
	rexui.ChatRowLook.TitleSelected = rexui.Of(gtheme.Label)
	rexui.ChatRowLook.PreviewSelected = rexui.Of(gtheme.Label2)

	// the titlebar's tabs: Rex's capsule and pill in the field's greens
	// rather than its lilacs
	t := &rexui.TabsRecipes[rexui.TabsRex]
	t.Capsule = [2]rexui.Pair{rexui.Solid(0xDEE4E0, 0x282D2B), rexui.Solid(0xDAE1DC, 0x252A28)}
	t.CapsuleBackdrop = [2]rexui.Pair{rexui.PairA(0xDEE4E0, 0x282D2B, 0.5, 0.55), rexui.PairA(0xDAE1DC, 0x252A28, 0.5, 0.55)}
	t.Pill = [2]rexui.Pair{rexui.Solid(0xFCFDFC, 0x343A37), rexui.Solid(0xF8FAF9, 0x303633)}
	// a quiet hairline all round the chosen tab: Rex's rim on the end caps
	// alone read as a border that broke off along the top and bottom
	t.PillRim, t.PillRimAround = rexui.PairA(0x26302B, 0xFFFFFF, 0.12, 0.07), true
	t.PillShadow = [2]rexui.Pair{rexui.PairA(0x26302B, 0x000000, 0.13, 0.40), rexui.PairA(0x26302B, 0x000000, 0.14, 0.45)}
	t.Sep = rexui.PairA(0x848E89, 0xB3BCB7, 1, 0.6)
	t.Press = rexui.PairA(0x26302B, 0xFFFFFF, 0.09, 0.08)
	t.Name = [2]rexui.Pair{rexui.Solid(0x575F5B, 0xA9B0AC), rexui.Solid(0x1C211F, 0xEEF1EF)}
	t.Detail = [2]rexui.Pair{rexui.Solid(0x8B9490, 0x8C9591), rexui.Solid(0x1C211F, 0xEEF1EF)}
	t.CloseFill = rexui.PairA(0x26302B, 0xFFFFFF, 0.08, 0.08)

}

// tuneRecipes puts T's values into Rex's recipes: the bubbles and the chat
// rows on Zap's grid, their corners the radius their contents nest in
// (space.go, tune.go).
func tuneRecipes() {
	b := &rexui.BubbleLook
	b.PadX, b.PadY, b.RowGap = T.BubblePadX, T.BubblePadY, T.RowGap
	b.Radius, b.CodeRadius = T.BubbleRadius, Nested()
	rexui.ChatRowLook.Pad, rexui.ChatRowLook.Gap = T.ListRowPad, T.ListRowGap
}

// tuneMetrics puts T's into th: the panes on Zap's grid, concentric with
// the window.
func tuneMetrics(th *gtheme.Theme) {
	th.Metrics.Inset, th.Metrics.Gutter, th.Metrics.RadiusLarge = T.PaneInset, T.PaneGutter, Pane()
	tuneMotion(th)
}

// How much of the accent tints your bubbles and the open chat's row, in light
// and in dark (where the same share reads fainter over the darker panes);
// rexui's chosen toggles and segments take the row's.
const (
	ownTint, ownTintDark = 0.11, 0.22
	rowTint, rowTintDark = 0.10, 0.18
)

// Rex is ZapFast's stock theme: Rex in WhatsApp's colours. Its field is a
// quiet grey with a trace of green in light, a near-neutral ink in dark
// (flat across, so with transparency off the window is solid), and the accent is WhatsApp's green, as a Mac whose accent were set
// to it: the default buttons, the badges, the focus ring, the selection and
// the tints of your bubbles and of the open chat. The macOS roles' neutral
// greys (fields, the window, menus, the selection without focus) would read
// cold on the dark field, so in dark every grey that is opaque or nearly
// takes its green, and the System ones among them keep it over the system's
// colours (Theme.Own); labels, fills and lines stay the system's, and light
// keeps macOS's own.
func Rex() *gtheme.Theme {
	th := rex.ThemeWithField(field)
	tuneMetrics(th)
	d := &th.Roles[1]
	for i, v := range d {
		r, g, b, a := v>>24, v>>16&0xFF, v>>8&0xFF, v&0xFF
		if a < 0xE6 || r != g || g != b {
			continue // a translucent fill takes the green of what is under it
		}
		d[i] = scale(r, inkR)<<24 | scale(g, inkG)<<16 | scale(b, inkB)<<8 | a
		th.Own[i] = true
	}
	th.Dark.Background, th.Dark.Surface, th.Dark.SurfaceRaised = darkField, darkPane, darkRaised
	th.Light.Background = lightField
	th.Roles[0][gtheme.Field], d[gtheme.Field] = lightField, darkField
	// a pane and what is raised over it are the dark field's surfaces
	d[gtheme.Pane], d[gtheme.Raised] = darkPane, darkRaised

	// WhatsApp's green is the accent whatever the system's is, its deeper
	// one in light under white text and its own under dark text in dark, as
	// WhatsApp pairs them; links are green too, deep enough to read on the
	// bubbles (4.5:1 on both, measured by Zap's QA)
	th.SetAccent(accentLight, accentDark)
	th.Roles[0][gtheme.Link], d[gtheme.Link] = 0x00705BFF, 0x88E6C3FF
	return th
}

// The accent is WhatsApp's green: its web app's #008069 in light (white
// reads on it at 4.9:1, on #00A884 at 3:1) and #00A884 in dark.
const accentLight, accentDark = 0x008069FF, 0x00A884FF

// Accent is Zap's own accent, in dark or in light: what a space without a
// colour of its own shows (the spaces' crossfade goes through it).
func Accent(dark bool) uint32 {
	if dark {
		return accentDark
	}
	return accentLight
}

// The fields' base colours (the Field role: rings and backdrops drawn on
// it), and the dark panes'.
const (
	lightField = 0xE6E9E7FF
	darkField  = 0x181B1AFF
	darkPane   = 0x212524FF
	darkRaised = 0x2A2F2DFF
)

// field is the window background: in light a mint, greener toward the left
// edge, that turns to WhatsApp's chat beige (#EFEAE2) toward the bottom; in
// dark a green ink, lifted toward the teal at the top, and the glass of the
// unfocused panes the same ink, lighter.
var field = rex.FieldColors{
	// Flat across, a few shades down: a solid window, not a wash.
	Light: []rex.FieldStop{
		{Y: 0, Mid: 0xF4F6F5, Left: 0xF4F6F5, Right: 0xF4F6F5},
		{Y: 6, Mid: 0xEBEEEC, Left: 0xEBEEEC, Right: 0xEBEEEC},
		{Y: 40, Mid: 0xE8EBE9, Left: 0xE8EBE9, Right: 0xE8EBE9},
		{Y: 160, Mid: 0xE6E9E7, Left: 0xE6E9E7, Right: 0xE6E9E7},
		{Y: 520, Mid: 0xE6E8E6, Left: 0xE6E8E6, Right: 0xE6E8E6},
		{Y: 900, Mid: 0xE7E8E5, Left: 0xE7E8E5, Right: 0xE7E8E5},
		{Y: 1278, Mid: 0xE9E9E6, Left: 0xE9E9E6, Right: 0xE9E9E6},
	},
	Dark: []rex.FieldStop{
		{Y: 0, Mid: 0x242827, Left: 0x242827, Right: 0x242827},
		{Y: 6, Mid: 0x202423, Left: 0x202423, Right: 0x202423},
		{Y: 40, Mid: 0x1D2120, Left: 0x1D2120, Right: 0x1D2120},
		{Y: 160, Mid: 0x1B1F1E, Left: 0x1B1F1E, Right: 0x1B1F1E},
		{Y: 520, Mid: 0x191C1B, Left: 0x191C1B, Right: 0x191C1B},
		{Y: 900, Mid: 0x181B1A, Left: 0x181B1A, Right: 0x181B1A},
		{Y: 1278, Mid: 0x181B1A, Left: 0x181B1A, Right: 0x181B1A},
	},
	DarkGlass: 0x2B302E,
	// over the glass (Settings.Transparency): a quiet grey-green
	Scrim: [2]uint32{0xEAEEEB, 0x181B1A},
}

// The dark ink's green: a grey's red, green and blue as the dark panes have
// them (#262A28 for #282828).
const inkR, inkG, inkB = 0.98, 1.02, 1.0

func scale(v uint32, k float32) uint32 { return uint32(min(float32(v)*k+0.5, 255)) }

// Meta is the face Rex sets its metadata in (times, numbers, sizes, hints):
// the UI face with tabular figures at sizePt points in k's scale, a step
// under the text it sits beside (rexui.MetaFont). Asking for it each frame
// is a lookup.
func Meta(k *widget.Kit, sizePt float32) text.FontKey {
	return k.TextSystem().Font(text.FontDesc{SizePt: sizePt, TabularDigits: true, Scale: k.Scale()})
}

// MetaPt is the metadata's size beside body text: times in the chat list,
// the call log and the bubbles, the composer's hints.
const MetaPt = 11
