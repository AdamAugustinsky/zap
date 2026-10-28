package theme

import (
	"math"

	gtheme "gonativeui/theme"
)

// Palette is upstream's palette (zf:src/theme.rs:61): the 21 colours of a
// theme as 0xRRGGBBAA (not premultiplied). The conversation draws from it;
// the rest of the window takes the same colours through Rex's roles
// (roles.go).
type Palette struct {
	// Dark is the palette's mode: a dark palette is shown in Rex's dark mode.
	Dark bool

	Window, Panel, Surface, SurfaceHover, SurfaceActive uint32
	Outline, Text, Secondary, Dim                       uint32
	Accent, AccentHover, OnAccent                       uint32
	Danger, Warning, Overlay, Shadow                    uint32
	// Chat is the conversation's colour behind the bubbles (upstream's
	// wallpaper "Theme" colour).
	Chat uint32
	// BubbleIn and BubbleOut are others' and your own bubbles; Link marks
	// links and mentions; Read is the read-receipt ticks.
	BubbleIn, BubbleOut, Link, Read uint32
}

const lightShadowAlpha = 50

// DarkPalette is upstream's built-in dark palette (zf:src/theme.rs:107).
func DarkPalette() Palette {
	return Palette{
		Dark: true, Window: 0x0B141AFF, Panel: 0x111B21FF, Surface: 0x202C33FF,
		SurfaceHover: 0x2A3942FF, SurfaceActive: 0x35444DFF, Outline: 0x222D34FF,
		Text: 0xE9EDEFFF, Secondary: 0x97A5AEFF, Dim: 0x8A989FFF,
		Accent: 0x00A884FF, AccentHover: 0x06CF9CFF, OnAccent: 0x0B141AFF,
		Danger: 0xF15C6DFF, Warning: 0xFFD279FF, Overlay: 0x233138FF, Shadow: 0x0000008C,
		Chat: 0x0B141AFF, BubbleIn: 0x202C33FF, BubbleOut: 0x005C4BFF,
		Link: 0x53BDEBFF, Read: 0x53BDEBFF,
	}
}

// LightPalette is upstream's built-in light palette (zf:src/theme.rs:249).
func LightPalette() Palette {
	return Palette{
		Dark: false, Window: 0xF0F2F5FF, Panel: 0xFFFFFFFF, Surface: 0xF0F2F5FF,
		SurfaceHover: 0xE6E9ECFF, SurfaceActive: 0xD9DDE1FF, Outline: 0xE9EDEFFF,
		Text: 0x111B21FF, Secondary: 0x515F67FF, Dim: 0x636B72FF,
		Accent: 0x008069FF, AccentHover: 0x006E5AFF, OnAccent: 0xFFFFFFFF,
		Danger: 0xEA0038FF, Warning: 0xA06B00FF, Overlay: 0xFFFFFFFF, Shadow: 0x00000000 | lightShadowAlpha,
		Chat: 0xEFEAE2FF, BubbleIn: 0xFFFFFFFF, BubbleOut: 0xD9FDD3FF,
		Link: 0x027EB5FF, Read: 0x027EB5FF,
	}
}

// How much a dark and a light palette's rail darkens the chat list, how
// much denser a bubble's shadow is than the palette's, and how far a raised
// surface's top edge goes toward its light (zf:src/theme.rs:567-577).
const (
	railShade     = 0.05
	darkRailShade = 0.22
	shadowDensity = 1.04
	darkEdgeTint  = 0.128
	lightEdgeTint = 0.48
)

// Rail is the navigation rail's background: the chat list's (the panel),
// one shade darker (zf:src/theme.rs:307).
func (p *Palette) Rail() uint32 {
	s := p.Panel
	shade := float32(railShade)
	if p.Dark {
		shade = darkRailShade
	}
	keep := 1 - shade
	ch := func(v uint32) uint32 { return uint32(math.Round(float64(float32(v&0xFF) * keep))) }
	a := float32(s & 0xFF)
	a = float32(math.Round(float64(a + (255-a)*shade)))
	return ch(s>>24)<<24 | ch(s>>16)<<16 | ch(s>>8)<<8 | uint32(a)
}

// OnBubble is the palette for a bubble's contents (zf:src/theme.rs:365):
// secondary and dim text and the read ticks move toward the text colour just
// far enough to stay readable on the bubble.
func (p *Palette) OnBubble(own bool) Palette {
	fill := p.BubbleIn
	if own {
		fill = p.BubbleOut
	}
	q := *p
	q.Secondary = readableOn(fill, p.Secondary, p.Text, 4.5)
	q.Dim = readableOn(fill, p.Dim, p.Text, 4.5)
	q.Read = readableOn(fill, p.Read, p.Text, 3.0)
	return q
}

// BubbleShadow is the soft shadow that lifts bubbles and date chips off the
// conversation: 2 points down with a 6-point blur, the palette's shadow
// colour a little denser but no heavier than the light theme's.
func (p *Palette) BubbleShadow() uint32 {
	c := p.LiftShadow()
	a := math.Round(float64(c&0xFF) * shadowDensity)
	return c&^0xFF | uint32(min(max(a, 0), 255))
}

// LiftShadow is the palette's shadow colour, no heavier than the light
// theme's.
func (p *Palette) LiftShadow() uint32 {
	return p.Shadow&^0xFF | min(p.Shadow&0xFF, lightShadowAlpha)
}

// RaisedEdge is the faint light along the top edge of a raised surface of
// colour fill: toward the text colour in a dark palette, toward white in a
// light one.
func (p *Palette) RaisedEdge(fill uint32) uint32 {
	if p.Dark {
		return lerpGamma(fill, p.Text, darkEdgeTint)
	}
	return lerpGamma(fill, 0xFFFFFFFF, lightEdgeTint)
}

// Sender is the group-sender colour derived from the avatar hue (degrees).
func (p *Palette) Sender(hue float32) uint32 {
	if p.Dark {
		return hsl(hue, 0.6, 0.68)
	}
	return hsl(hue, 0.65, 0.38)
}

// Avatar is the avatar background derived from the chat hue (degrees).
func (p *Palette) Avatar(hue float32) uint32 {
	if p.Dark {
		return hsl(hue, 0.38, 0.42)
	}
	return hsl(hue, 0.45, 0.62)
}

// mixPalette is the palette between a (t = 0) and b (t = 1): the colours
// fade, the mode is the target's.
func mixPalette(a, b *Palette, t float32) Palette {
	switch {
	case t <= 0:
		return *a
	case t >= 1:
		return *b
	}
	p := *b
	if t < 0.5 {
		p.Dark = a.Dark
	}
	m := func(x, y uint32) uint32 { return gtheme.MixRGBA(x, y, t) }
	p.Window, p.Panel, p.Surface = m(a.Window, b.Window), m(a.Panel, b.Panel), m(a.Surface, b.Surface)
	p.SurfaceHover, p.SurfaceActive = m(a.SurfaceHover, b.SurfaceHover), m(a.SurfaceActive, b.SurfaceActive)
	p.Outline, p.Text, p.Secondary, p.Dim = m(a.Outline, b.Outline), m(a.Text, b.Text), m(a.Secondary, b.Secondary), m(a.Dim, b.Dim)
	p.Accent, p.AccentHover, p.OnAccent = m(a.Accent, b.Accent), m(a.AccentHover, b.AccentHover), m(a.OnAccent, b.OnAccent)
	p.Danger, p.Warning, p.Overlay, p.Shadow = m(a.Danger, b.Danger), m(a.Warning, b.Warning), m(a.Overlay, b.Overlay), m(a.Shadow, b.Shadow)
	p.Chat, p.BubbleIn, p.BubbleOut = m(a.Chat, b.Chat), m(a.BubbleIn, b.BubbleIn), m(a.BubbleOut, b.BubbleOut)
	p.Link, p.Read = m(a.Link, b.Link), m(a.Read, b.Read)
	return p
}

// set sets the colour called name (the names of a palette file); false for a
// name the palette does not have.
func (p *Palette) set(name string, c uint32) bool {
	f := p.field(name)
	if f == nil {
		return false
	}
	*f = c
	return true
}

func (p *Palette) field(name string) *uint32 {
	switch name {
	case "window":
		return &p.Window
	case "panel":
		return &p.Panel
	case "surface":
		return &p.Surface
	case "surface_hover":
		return &p.SurfaceHover
	case "surface_active":
		return &p.SurfaceActive
	case "outline":
		return &p.Outline
	case "text":
		return &p.Text
	case "secondary":
		return &p.Secondary
	case "dim":
		return &p.Dim
	case "accent":
		return &p.Accent
	case "accent_hover":
		return &p.AccentHover
	case "on_accent":
		return &p.OnAccent
	case "danger":
		return &p.Danger
	case "warning":
		return &p.Warning
	case "overlay":
		return &p.Overlay
	case "shadow":
		return &p.Shadow
	case "chat":
		return &p.Chat
	case "bubble_in":
		return &p.BubbleIn
	case "bubble_out":
		return &p.BubbleOut
	case "link":
		return &p.Link
	case "read":
		return &p.Read
	}
	return nil
}

// derive fills the chat colours a file left out from those it set
// (zf:src/theme.rs:497): Spotifast palettes share the sixteen interface
// colours, so chat follows window, bubbles follow surface and accent, link
// and read follow accent, overlay follows panel.
func (p *Palette) derive(given map[string]bool) {
	if given["window"] && !given["chat"] {
		p.Chat = p.Window
	}
	if given["surface"] && !given["bubble_in"] {
		p.BubbleIn = lerpGamma(p.Surface, p.Text, 0.05)
	}
	if given["accent"] {
		if !given["bubble_out"] {
			p.BubbleOut = lerpGamma(p.Surface, p.Accent, 0.24)
		}
		if !given["link"] {
			p.Link = p.Accent
		}
		if !given["read"] {
			p.Read = p.Accent
		}
	}
	if given["panel"] && !given["overlay"] {
		p.Overlay = p.Panel
	}
}

// lerpGamma is the colour t of the way from a to b, channel by channel in
// sRGB (egui's lerp_to_gamma).
func lerpGamma(a, b uint32, t float32) uint32 {
	var out uint32
	for sh := uint(0); sh < 32; sh += 8 {
		x, y := float32(a>>sh&0xFF), float32(b>>sh&0xFF)
		v := uint32(math.Round(float64(x + (y-x)*t)))
		out |= min(v, 255) << sh
	}
	return out
}

// blend is upstream's theme::blend: a to b, t = 1 returns b.
func blend(a, b uint32, t float32) uint32 { return lerpGamma(a, b, t) }

// contrast is the WCAG contrast ratio of two opaque colours, 1 to 21.
func contrast(a, b uint32) float32 { return float32(gtheme.Contrast(a|0xFF, b|0xFF)) }

// readableOn is c, blended toward toward only as far as needed to reach the
// target contrast on bg (zf:src/theme.rs:1422).
func readableOn(bg, c, toward uint32, target float32) uint32 {
	var step float32
	for {
		cand := blend(c, toward, step)
		if step >= 1 || contrast(cand, bg) >= target {
			return cand
		}
		step = min(step+0.05, 1)
	}
}

// hsl converts hue (degrees), saturation and lightness to an opaque colour.
func hsl(hue, sat, light float32) uint32 {
	chroma := (1 - float32(math.Abs(float64(2*light-1)))) * sat
	sector := hue / 60
	x := chroma * (1 - float32(math.Abs(float64(float32(math.Mod(float64(sector), 2))-1))))
	var r, g, b float32
	switch uint32(sector) {
	case 0:
		r, g, b = chroma, x, 0
	case 1:
		r, g, b = x, chroma, 0
	case 2:
		r, g, b = 0, chroma, x
	case 3:
		r, g, b = 0, x, chroma
	case 4:
		r, g, b = x, 0, chroma
	default:
		r, g, b = chroma, 0, x
	}
	m := light - chroma/2
	ch := func(v float32) uint32 {
		return uint32(min(max(math.Round(float64((v+m)*255)), 0), 255))
	}
	return ch(r)<<24 | ch(g)<<16 | ch(b)<<8 | 0xFF
}
