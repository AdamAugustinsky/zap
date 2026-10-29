package theme

import (
	"gonativeui/rexui"
	gtheme "gonativeui/theme"
)

// Zap's motion rules. Motion says what changed and where it went; it is
// never ornament. Every animation in the screens takes its timing from here
// (the durations are tunables: T.Dur*, tune.go), and the rules below are what
// a new one is held to.
//
//  1. Five durations, by how far a thing travels: Instant (a press's
//     highlight, a selection), DurMicro (hover, a value's tint), DurShort
//     (fades, swaps of an icon or a line, a badge), DurStandard (panes,
//     layout that moves, a menu or a popover), DurLong (a sheet, a big
//     reveal). Nothing longer: a wait the user watches is a bug.
//  2. Arriving decelerates, leaving is quicker: an exit takes ExitScale of
//     its entrance (Exit), and a tooltip or a menu that goes leaves at once
//     when another takes its place.
//  3. Things come from where they live and go back there: the list from the
//     left, the side pane from the right edge, a menu and a popover from
//     their anchor (origin-aware), a toast from the bottom, the tabs' pill
//     to the tab chosen. No motion from nowhere.
//  4. Move, fade, scale a little (0.96 to 1), blur a little (up to 4 pt, for
//     a swap of text or an icon only): never animate what reflows text. A
//     pane that resizes keeps its content at its full width and sweeps over
//     it (shell's curtain).
//  5. One idea per change: a swap is a crossfade with a little blur, an
//     arrival a rise and a fade; never fade, scale, blur and slide together
//     on something small.
//  6. Interruptible and never in the way: a motion retargets from where it
//     is (AnimateValue), and input goes to what is drawn while it moves; only
//     a view sliding over another (a narrow window) holds the pointer.
//  7. Springs only for what a hand moves or a confirmation the user made
//     (a reaction picked, a drag let go): one overshoot at most, settled
//     within DurLong. Not for panes, menus or text.
//  8. Stagger only for a few things arriving together (at most five, 30 ms
//     apart, the whole within DurStandard); never for history loading.
//  9. Loops only while they mean something and are seen: typing, sending,
//     loading; they stop off screen (rexui's loops) and under Reduce Motion.
// 10. Reduce Motion: every travel, scale and spring is at once; a crossfade
//     of DurShort or less stays, as macOS keeps it. Enter and Exit say so.

// Enter is the rate (ui.Context.AnimateValue's) of something arriving over d
// seconds; at once under Reduce Motion, which stills the theme's durations.
func Enter(r *gtheme.Resolved, d float32) float32 {
	if r.Motion.Standard <= 0 || d <= 0 {
		return gtheme.Instant
	}
	return rexui.RateFor(d)
}

// Exit is Enter for something leaving: ExitScale of the time (rule 2).
func Exit(r *gtheme.Resolved, d float32) float32 { return Enter(r, d*T.ExitScale) }

// Toward is Enter while on (arriving) and Exit while not (leaving): the rate
// of a thing that comes and goes, as a pane does.
func Toward(r *gtheme.Resolved, d float32, on bool) float32 {
	if on {
		return Enter(r, d)
	}
	return Exit(r, d)
}

// Dur is d seconds as a tween's length (a motion of fixed length, as a
// narrow window's slide), 0 under Reduce Motion.
func Dur(r *gtheme.Resolved, d float32) float32 {
	if r.Motion.Standard <= 0 {
		return 0
	}
	return d
}

// EaseOut is t, 0 to 1, decelerated (cubic): a tween's arrival (rule 2).
func EaseOut(t float32) float32 {
	t = 1 - min(max(t, 0), 1)
	return 1 - t*t*t
}

// tuneMotion puts the durations into th's rates, which Rex's components
// follow (hover, fades, the panes' focus dimming): one timing for the app's
// own motion and the framework's.
func tuneMotion(th *gtheme.Theme) {
	m := &th.Motion
	m.Hot = rexui.RateFor(T.DurMicro)
	m.Fade = rexui.RateFor(T.DurShort)
	m.Dim = rexui.RateFor(T.DurShort)
	m.Standard, m.Dismiss, m.Sheet = T.DurStandard, T.DurStandard*T.ExitScale, T.DurLong
}
