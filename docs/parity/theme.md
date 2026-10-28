# Parity: theme (TH)

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

Package `gnui/theme`. Pass 2, 2026-10-21. Rows owned by `theme` in `docs/PARITY.md`; `docs/PARITY.md` itself is untouched.

| id | status | evidence | note |
|---|---|---|---|
| TH-001 | done | `TestChoiceSetsTheModeAndThePalette` (dark, light, system, Messages); capture `docs/parity/theme/light.png` | Built-in Dark/Light keep Rex's own colours and the system accent (decided "full Rex"); upstream's bubble, link, read and wallpaper colours for them come through `theme.Bubble`, `OnBubble`, `Link`, `Chat`, `WallpaperOf`. |
| TH-002 | done | `TestFollowsTheSystemLive`, `TestTheFirstFrameIsInTheSystemsMode`; hand: run a build with Follow system, flip System Settings → Appearance Light/Dark, the window follows within a third of a second (done pass 1, system restored) | The framework's Follower does the following; `Apply` sets the preference. Dark/Light override the system (same test). |
| TH-003 | done | theme: `TestChoiceSetsTheModeAndThePalette`, `TestOnBubble`, `TestFrost`, `TestWallpaper`; conversation: `TestMessagesBubbleColours`, `TestMessagesCurlOnlyEndsTheRun`, `TestMessagesTranscriptUnderTheHeader`; eight revision captures listed in conversation.md | Messages colours/materials and upstream shapes are wired through Rex. The coordinator corrected the prior no-curl instruction: Rex supports BubbleOpts.Curl and a 15 pt BubbleRecipe, now restored exactly as at 79cd936. OnBubble text/secondary colour improvements remain. Translucent sidebar remains the shell's/chatlist's (`rexui.GlassSidebar`), not in this package. |
| TH-004 | done | `TestAThemeFileMapsOntoRexRoles`, `TestParsePalette`, `TestParseColor`, `TestFolderLimits`, `TestDisplayName` | Folder is `core.Dirs()/themes` or `$ZAPFAST_THEMES_DIR`; 64 KiB, 512 entries, 128 themes as upstream. |
| TH-005 | done | `TestAThemeFileMapsOntoRexRoles` (21 palette colours onto roles, light base over a dark system), `TestSixteenColourPalettesDeriveTheChatColours` | `accent_hover` has no Rex role (Rex derives the pressed accent); `chat`, `bubble_*`, `read` have none either and go through the API. |
| TH-006 | done | `TestSharedPalettesAreInstalledOnce` | Eight presets written once, an edited one kept, a deleted one stays deleted. |
| TH-008 | done | `TestABrokenFileIsSkippedAndTheLastPaletteStays` | The reason is in `theme.LoadErrors()` and the log; `theme.Status()` says "The selected theme is unavailable…". |

TH-007 (`zapfast reload-themes`) is owned by `sys`: it calls `theme.Reload()` (`sys/cli.go`); the reload itself is covered by `TestABrokenFileIsSkippedAndTheLastPaletteStays` and `TestSharedPalettesAreInstalledOnce`.

Counts: done 7, partial 0, todo 0, waived 0, hand 0 (TH-002's hand check is its second evidence).

## Messages in the conversation

Rechecked on 2026-10-21 against `79cd936`, then closed out in `z7-conv`. The cf08cb8 missing-call inventory was stale:

1. **Background:** already complete (CV-035), `WallpaperOf`, image cover fitting, doodles outside Messages.
2. **Bubble fills:** Messages already uses `theme.Bubble(mine)` and view-relative `BubbleGradient`. Messages body, secondary and rich-text colours now use `OnBubble`; read ticks use its Read colour with CV-022 privacy masking. Non-Messages bubbles retain the existing Rex palette mapping.
3. **Shape correction:** full Rex includes its own `BubbleOpts.Curl` and `BubbleRecipe`. The coordinator withdrew the mistaken no-curl instruction. Messages uses the original 15 pt corners and curled last-run tail; other styles retain their existing Rex shapes without curls.
4. **Frosted header:** already complete in `look.go`: `Frost` → `NextBackdrop`, `HeaderVeil`, smooth ease-off, plain veil when frost is unavailable; header name glass and day chips are already wired. Composer/sidebar belong to their respective packages.
5. **Group names, avatars and photos:** already wired to `Sender`, `AvatarColor` and `kinds.NoBubble` for bare Messages photos. Their code is unchanged.
6. **Settings:** `ui/settings` keeps its own `wallpapers` table; `theme.WallpaperChoices(dark)`, `WallpaperName`, `WallpaperRGB` hold all 51 colours (settings lists the first 14). It also has no theme-file picker yet: `theme.Entries()`, `theme.Status()`, `theme.LoadErrors()`, `theme.EnsureDir()` (Open themes folder), `Settings.CustomTheme` = the file name.
