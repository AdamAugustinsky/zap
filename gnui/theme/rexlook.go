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
	t.Capsule = [2]rexui.Pair{rexui.Solid(0xD6E6DC, 0x27342F), rexui.Solid(0xD2E0D7, 0x24302C)}
	t.CapsuleBackdrop = [2]rexui.Pair{rexui.PairA(0xD6E6DC, 0x27342F, 0.5, 0.55), rexui.PairA(0xD2E0D7, 0x24302C, 0.5, 0.55)}
	t.Pill = [2]rexui.Pair{rexui.Solid(0xFBFEFC, 0x35443E), rexui.Solid(0xF7FCF9, 0x313F39)}
	t.PillRim = rexui.Solid(0xB4C5BB, 0x607269)
	t.PillShadow = [2]rexui.Pair{rexui.PairA(0x1E3A2E, 0x000000, 0.13, 0.40), rexui.PairA(0x1E3A2E, 0x000000, 0.14, 0.45)}
	t.Sep = rexui.PairA(0x7F9188, 0xB2C2BA, 1, 0.6)
	t.Press = rexui.PairA(0x1E3A2E, 0xFFFFFF, 0.09, 0.08)
	t.Name = [2]rexui.Pair{rexui.Solid(0x55605A, 0xA9B3AE), rexui.Solid(0x1B2420, 0xEEF2F0)}
	t.Detail = [2]rexui.Pair{rexui.Solid(0x8A958F, 0x8B9690), rexui.Solid(0x1B2420, 0xEEF2F0)}
	t.CloseFill = rexui.PairA(0x1E3A2E, 0xFFFFFF, 0.08, 0.08)

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
	ownTint, ownTintDark = 0.16, 0.30
	rowTint, rowTintDark = 0.14, 0.24
)

// Rex is ZapFast's stock theme: Rex in WhatsApp's colours. Its field is a
// mint that settles to WhatsApp's chat beige in light, a deep green ink in
// dark, and the accent is WhatsApp's green, as a Mac whose accent were set
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

// The fields' base colours (the Field role: rings and backdrops drawn on
// it), and the dark panes'.
const (
	lightField = 0xE0EBE4FF
	darkField  = 0x19211EFF
	darkPane   = 0x222A27FF
	darkRaised = 0x2B3430FF
)

// field is the window background: in light a mint, greener toward the left
// edge, that turns to WhatsApp's chat beige (#EFEAE2) toward the bottom; in
// dark a green ink, lifted toward the teal at the top, and the glass of the
// unfocused panes the same ink, lighter.
var field = rex.FieldColors{
	Light: []rex.FieldStop{
		{Y: 0, Mid: 0xF7FBF8, Left: 0xF7FBF8, Right: 0xF7FBF8},
		{Y: 6, Mid: 0xEAF4EE, Left: 0xE4F2EA, Right: 0xECF3EC},
		{Y: 40, Mid: 0xE3EFE8, Left: 0xDCEDE3, Right: 0xE6EEE6},
		{Y: 160, Mid: 0xDFEBE4, Left: 0xD6E9DE, Right: 0xE3EBE2},
		{Y: 520, Mid: 0xE0E8E2, Left: 0xD8E6DD, Right: 0xE5E8DF},
		{Y: 900, Mid: 0xE4E6DF, Left: 0xDCE5DB, Right: 0xEAE6DC},
		{Y: 1278, Mid: 0xEAE6DD, Left: 0xE1E6DA, Right: 0xEEE6DA},
	},
	Dark: []rex.FieldStop{
		{Y: 0, Mid: 0x22302C, Left: 0x21302B, Right: 0x232F2B},
		{Y: 6, Mid: 0x1F2C28, Left: 0x1D2C27, Right: 0x202B27},
		{Y: 40, Mid: 0x1C2824, Left: 0x1A2823, Right: 0x1D2723},
		{Y: 160, Mid: 0x1A2421, Left: 0x182520, Right: 0x1B231F},
		{Y: 520, Mid: 0x171F1C, Left: 0x16211C, Right: 0x191F1B},
		{Y: 900, Mid: 0x161C1A, Left: 0x151E1A, Right: 0x181C19},
		{Y: 1278, Mid: 0x171B19, Left: 0x161D19, Right: 0x191B18},
	},
	DarkGlass: 0x2A3430,
	// over the glass (Settings.Transparency): the mint and the green ink
	Scrim: [2]uint32{0xE6F3EB, 0x17201D},
}

// The dark ink's green: a grey's red, green and blue as the dark panes have
// them (#262A28 for #282828).
const inkR, inkG, inkB = 0.95, 1.04, 1.0

func scale(v uint32, k float32) uint32 { return uint32(min(float32(v)*k+0.5, 255)) }

// Meta is the face Rex sets its metadata in (times, numbers, sizes, hints):
// the UI face with tabular figures at sizePt points in k's scale, a step
// under the text it sits beside (rexui.MetaFont). Asking for it each frame
// is a lookup.
func Meta(k *widget.Kit, sizePt float32) text.FontKey {
	return k.TextSystem().Font(text.FontDesc{SizePt: sizePt, Tabular: true, Scale: k.Scale()})
}

// MetaPt is the metadata's size beside body text: times in the chat list,
// the call log and the bubbles, the composer's hints.
const MetaPt = 11
