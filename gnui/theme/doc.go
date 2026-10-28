// Package theme maps upstream's themes onto Rex: every window's kit, each frame.
//
// It ports zf:src/theme.rs and zf:src/appearance.rs (themes.md): upstream's
// built-in themes and theme files onto Rex's colour roles; light, dark and
// follow-system. Fonts (Inter) and zoom are the shell's and the framework's:
// Rex draws in the system's font, and the menu's zoom roles scale the window.
// Upstream's Messages theme and chat wallpapers are not ported: the
// conversation sits on Rex's panes.
//
// # Entry points (the shell's slot, wired in cmd/zapfast/screens.go)
//
//	New() *Theme   // screen.Theme: Set.Theme; lists the themes folder on a worker
//
// # What it does each frame
//
// Apply sets the kit's preference from Settings.Theme: Dark and Light force
// that mode, Follow system follows the system's, live and already in the
// first frame (the framework's Follower). A theme file chosen in
// Settings.CustomTheme wins over all of them: Rex recoloured by the file's
// palette, in the file's base mode. The built-in themes keep Rex's own look
// (its colours and the system's accent); upstream's bubble and link colours
// for them come through the API below.
//
// # API for other packages (read-only, main thread, each frame)
//
// The colours the conversation and others need. They are the palette in force
// at the window's current light/dark mix, so they fade with Rex's own fade.
//
//	theme.Bubble(mine bool) uint32                    // a bubble's fill (0xRRGGBBAA)
//	theme.OnBubble(mine bool) theme.Palette           // text, secondary, dim, link, read, accent on a bubble
//	theme.Chat() uint32                               // the palette's colour behind the bubbles
//	theme.Link() / Mention() / Read() uint32          // links, @mentions, read ticks
//	theme.Sender(hue) / AvatarColor(hue) uint32       // group-sender name, avatar background
//	theme.BubbleShadow() / RaisedEdge(fill) uint32    // the lift of bubbles and chips
//	theme.Dark() bool                                 // dark palette
//	theme.Colors() *Palette                           // all 21 palette colours + the helpers
//
// For Settings → Appearance and the commands:
//
//	theme.Entries() []Entry   // theme files for the picker (File, Name, Palette)
//	theme.LoadErrors()        // skipped files and why
//	theme.Status() string     // "Loading local themes…" and the other lines under the setting
//	theme.Reload()            // zapfast reload-themes
//	theme.Dir() / EnsureDir() // the themes folder; Settings creates it before opening it
//	theme.ParsePalette(data)  // a palette file's contents
//
// Settings.CustomTheme holds the chosen file's name ("Nord.json").
//
// # Themes folder
//
// <app data>/themes (core.Dirs), or $ZAPFAST_THEMES_DIR. The shared palettes
// (Catppuccin, Nord, Rosé Pine, Tokyo Night, Ristretto) are written there
// once, never rewritten, and a deleted one stays deleted. Files are read on
// a worker; a broken file is skipped, reported in LoadErrors and the log, and
// the last working palette stays.
package theme
