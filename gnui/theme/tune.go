package theme

// Tunables are the design values the screens read each frame, rather than
// constants, so that the playground (cmd/zapfast -playground, dev builds)
// can move them while the app runs. T is the live set; Defaults the code's,
// from the tokens in space.go and icons.go and the screens' own choices. A
// change bumps Gen (Changed), which the theme's Apply follows into Rex's
// recipes and metrics; the screens read T directly.
//
// Nothing outside the playground writes T: in a release it stays Defaults.
type Tunables struct {
	// Spacing, in points: steps of the grid (SpaceSteps).
	PaneInset, PaneGutter  float32 // the panes from the window's edges and between them
	BubblePadX, BubblePadY float32 // a bubble's padding round its content
	RowGap                 float32 // between two bubbles of one sender's run
	RunGap                 float32 // above a new sender's run
	NameGap                float32 // a group sender's name to their message
	QuotePad               float32 // round a quote's two lines
	BlockGap               float32 // a nested block (a document, a link, a picture) to its caption
	TranscriptSide         float32 // the transcript's side margins
	HeaderPadX             float32 // the chat header's sides
	ListRowPad, ListRowGap float32 // a chat row's sides, and avatar to text
	DockGap                float32 // the composer's plus, emoji and field apart
	InfoMargin             float32 // the info pane's side margin

	// Radii, in points.
	BubbleRadius float32 // a bubble; what nests in it is this less BubblePadY

	// Type, in points.
	MetaPt       float32 // metadata in the lists and hints: times, counts, numbers
	BubbleTimePt float32 // a bubble's time

	// Hit areas, in points.
	HitTool, HitLarge float32 // a title row's items; the composer's tools

	// Motion (motion.go's rules), seconds: the five durations, and how much
	// of its entrance an exit takes.
	DurMicro, DurShort, DurStandard, DurLong float32
	ExitScale                                float32

	// Feedback.
	RowHover, RowPress float32 // the label colour a chat row lays over the pane
	ToolRest, ToolLift float32 // the composer tools' plates, at rest and lifted

	// The header's soft scroll edge.
	SoftBlur float32 // 1: on, 0: off
	SoftFade float32 // how far below the header it eases off, points

	// Tabs.
	MinTabs float32 // how many open chats bring the titlebar's tabs up
}

// Defaults are the code's values.
var Defaults = Tunables{
	PaneInset: PaneInset, PaneGutter: PaneGutter,
	BubblePadX: BubblePadX, BubblePadY: BubblePadY,
	RowGap: Micro, RunGap: MinGap, NameGap: Space1,
	QuotePad: Space2, BlockGap: MinGap, TranscriptSide: Space4, HeaderPadX: Space2,
	ListRowPad: Space3, ListRowGap: Space3,
	DockGap: Space2, InfoMargin: Space4,

	BubbleRadius: BubbleRadius,

	MetaPt: 11, BubbleTimePt: 10,

	HitTool: HitTool, HitLarge: HitLarge,

	DurMicro: 0.1, DurShort: 0.18, DurStandard: 0.25, DurLong: 0.35, ExitScale: 0.7,
	RowHover: 0.05, RowPress: 0.04, ToolRest: 0.06, ToolLift: 0.06,

	SoftBlur: 1, SoftFade: Space6,

	MinTabs: 2,
}

// T is the live set.
var T = Defaults

// Gen counts the changes to T (Changed).
var Gen uint32

// Changed says T has changed: Apply carries it into Rex at the next frame.
func Changed() { Gen++ }

// Nested is the radius of what sits in a bubble's padding: concentric with
// the bubble, never under InnerRadius.
func Nested() float32 { return max(InnerRadius, T.BubbleRadius-T.BubblePadY) }

// Pane is the panes' radius: concentric with the window's.
func Pane() float32 { return max(InnerRadius, WindowRadius-T.PaneInset) }

// SpaceSteps are the grid's steps a spacing knob moves between.
var SpaceSteps = []float32{0, Micro, Space1, Space2, Space3, Space4, Space5, Space6, Space8, 40, 48}

// RadiusSteps are a radius knob's.
var RadiusSteps = []float32{0, Space1, 6, Space2, 10, Space3, 14, Space4, 18, Space5, Space6}

// Knob is one of T's fields as the playground shows it.
type Knob struct {
	ID, Group, Label, Unit string
	V                      *float32
	Def                    float32
	// Steps, when set, are the values it moves between; otherwise Min to Max
	// by Step.
	Steps          []float32
	Min, Max, Step float32
	// Note says what it drives, under its label.
	Note string
}

// Knobs are T's fields in the playground's order.
func Knobs() []Knob {
	d := &Defaults
	sp := func(id, group, label string, v *float32, def float32, note string) Knob {
		return Knob{ID: id, Group: group, Label: label, Unit: "pt", V: v, Def: def, Steps: SpaceSteps, Note: note}
	}
	return []Knob{
		sp("paneInset", "Panes", "Inset", &T.PaneInset, d.PaneInset, "panes from the window's edges; pane radius = 16 − inset"),
		sp("paneGutter", "Panes", "Gutter", &T.PaneGutter, d.PaneGutter, "between list, chat and side pane"),

		sp("bubblePadX", "Bubbles", "Padding X", &T.BubblePadX, d.BubblePadX, "a bubble's sides"),
		sp("bubblePadY", "Bubbles", "Padding Y", &T.BubblePadY, d.BubblePadY, "top and bottom; nested radius = radius − this"),
		{ID: "bubbleRadius", Group: "Bubbles", Label: "Radius", Unit: "pt", V: &T.BubbleRadius, Def: d.BubbleRadius, Steps: RadiusSteps, Note: "bubbles and flush photos"},
		sp("rowGap", "Bubbles", "Gap in a run", &T.RowGap, d.RowGap, "between one sender's bubbles"),
		sp("runGap", "Bubbles", "Gap between senders", &T.RunGap, d.RunGap, "above a new sender's run"),
		sp("nameGap", "Bubbles", "Name to message", &T.NameGap, d.NameGap, "a group sender's name"),
		sp("quotePad", "Bubbles", "Quote padding", &T.QuotePad, d.QuotePad, "round a reply's quoted lines"),
		sp("blockGap", "Bubbles", "Block to caption", &T.BlockGap, d.BlockGap, "a document, link or picture to the text with it"),

		sp("transcriptSide", "Transcript", "Side margin", &T.TranscriptSide, d.TranscriptSide, "bubbles from the pane's sides"),
		sp("headerPadX", "Transcript", "Header sides", &T.HeaderPadX, d.HeaderPadX, "the chat header's avatar and name inset"),
		sp("softFade", "Transcript", "Blur fade", &T.SoftFade, d.SoftFade, "how far below the header the blur eases off"),
		{ID: "softBlur", Group: "Transcript", Label: "Header blur", V: &T.SoftBlur, Def: d.SoftBlur, Min: 0, Max: 1, Step: 1, Note: "0 off, 1 on"},

		sp("listRowPad", "Chat list", "Row padding", &T.ListRowPad, d.ListRowPad, "a row's sides (Rex's was 10)"),
		sp("listRowGap", "Chat list", "Avatar to text", &T.ListRowGap, d.ListRowGap, "inside a row (Rex's was 10)"),
		{ID: "rowHover", Group: "Chat list", Label: "Hover fill", V: &T.RowHover, Def: d.RowHover, Min: 0, Max: 0.2, Step: 0.01, Note: "label colour under the pointer"},
		{ID: "rowPress", Group: "Chat list", Label: "Press fill", V: &T.RowPress, Def: d.RowPress, Min: 0, Max: 0.2, Step: 0.01},

		sp("dockGap", "Composer", "Dock gap", &T.DockGap, d.DockGap, "plus, emoji and field apart"),
		{ID: "hitLarge", Group: "Composer", Label: "Tool plate", Unit: "pt", V: &T.HitLarge, Def: d.HitLarge, Steps: []float32{24, 28, 32, 36, 40, 44}, Note: "plus and emoji plates"},
		{ID: "toolRest", Group: "Composer", Label: "Plate at rest", V: &T.ToolRest, Def: d.ToolRest, Min: 0, Max: 0.2, Step: 0.01},
		{ID: "toolLift", Group: "Composer", Label: "Plate lift", V: &T.ToolLift, Def: d.ToolLift, Min: 0, Max: 0.2, Step: 0.01, Note: "added under the pointer"},

		sp("infoMargin", "Info pane", "Side margin", &T.InfoMargin, d.InfoMargin, "the contact and group info"),
		{ID: "hitTool", Group: "Info pane", Label: "Title row hit", Unit: "pt", V: &T.HitTool, Def: d.HitTool, Steps: []float32{20, 24, 28, 32, 36}, Note: "Close and Back in pane headers"},

		{ID: "metaPt", Group: "Type", Label: "Metadata", Unit: "pt", V: &T.MetaPt, Def: d.MetaPt, Min: 9, Max: 13, Step: 0.5, Note: "list times, hints, numbers"},
		{ID: "bubbleTimePt", Group: "Type", Label: "Bubble time", Unit: "pt", V: &T.BubbleTimePt, Def: d.BubbleTimePt, Min: 8, Max: 13, Step: 0.5},

		{ID: "durMicro", Group: "Motion", Label: "Micro", Unit: "s", V: &T.DurMicro, Def: d.DurMicro, Min: 0, Max: 0.3, Step: 0.02, Note: "hover, a value's tint"},
		{ID: "durShort", Group: "Motion", Label: "Short", Unit: "s", V: &T.DurShort, Def: d.DurShort, Min: 0, Max: 0.4, Step: 0.02, Note: "fades, swaps, a pane's focus dimming"},
		{ID: "durStandard", Group: "Motion", Label: "Standard", Unit: "s", V: &T.DurStandard, Def: d.DurStandard, Min: 0, Max: 0.6, Step: 0.05, Note: "panes (⌘B, the info pane, a narrow window's slide), menus"},
		{ID: "durLong", Group: "Motion", Label: "Long", Unit: "s", V: &T.DurLong, Def: d.DurLong, Min: 0, Max: 0.8, Step: 0.05, Note: "sheets, big reveals"},
		{ID: "exitScale", Group: "Motion", Label: "Exit / entrance", Unit: "×", V: &T.ExitScale, Def: d.ExitScale, Min: 0.4, Max: 1, Step: 0.05, Note: "leaving is quicker than arriving"},
		{ID: "minTabs", Group: "Tabs", Label: "Tabs from", Unit: "chats", V: &T.MinTabs, Def: d.MinTabs, Min: 1, Max: 4, Step: 1},
	}
}
