# Hand test (macOS, Apple silicon)

The checklist for testing ZapFast's Go UI by hand, once, on the MacBook. Read it top to bottom and tick each box. Everything the automated tests can see is not here: these are the checks that only a real screen, speaker, microphone, Keychain, Dock, menu bar or phone can show.

Each check reads `[id] steps. Expect: result`. The id is a row of `docs/PARITY.md` where one exists, or an `HT-nn` number of this file. Groups 1 to 9 use the demo core and need no phone. Groups 10 to 17 use dedicated test accounts, never Adam's personal account. Group 18 closes with translations and the final numbers. Group 19 repeats a few checks on the release build.

There are 19 numbered groups and 144 checkboxes (143 in the main checklist, including setup, plus one optional advanced-engineering check in the appendix). The engineering appendix is not part of the ordinary hand-test flow. This checklist is still unexecuted; adding or refining a check is not a pass.

## Before you start

- [ ] **Machine.** Run on the MacBook (Apple silicon, macOS 14 or later), not on the Mac mini.
- [ ] **Install.** Build and install the hand-test bundle as described in [HANDTEST-BUILD.md](HANDTEST-BUILD.md). It is a development build: it has the demo and measurement flags, and a Debug menu that the checks ignore. It installs as `/Applications/ZapFast Go.app` when upstream ZapFast is already in Applications. This file uses `APP` for the path you chose:

  ```sh
  APP="/Applications/ZapFast Go.app"
  ```

- [ ] **Demo mode** (groups 1 to 9). `open -n "$APP" --args -demo` opens a synthetic account. It has no network, saves nothing, plays no sound, uses no Keychain and has no menu-bar item. Quit with ⌘Q between pages. A page is a fixed state: `open -n "$APP" --args -demo-page NAME` (for example `delete-chat`, `call-group`, `offline`).
- [ ] **Real account** (groups 10 to 17 and 19). `open "$APP"` and link a test number with its QR code. You need:
  - **Phone A:** the phone that holds the test number (it links ZapFast as a device).
  - **Phone B:** a second phone or WhatsApp account that can message and call the test number.
  - **A second dedicated test number** for AC-008 and HT-42 (account removal/Keychain deletion). Skip AC-008 and record HT-42 blocked if you have none; never substitute Adam's personal account.
- [ ] **Permissions.** macOS asks at the first use, so keep the prompts for the checks that name them:
  - Notifications: first notification (group 14).
  - Microphone: first voice note (group 13). Camera: first video call (group 15).
  - Login Items: Start at login (group 16).
  - To start the prompts over: `tccutil reset All ai.a3lab.zap`.
- [ ] **Clean up at the end.** Turn Start at login off, and remove the build as in HANDTEST-BUILD, section 5, if you do not keep it.

## 1. Install and first launch (demo)

- [ ] **[HT-01]** `codesign --verify --deep --strict --verbose=2 "$APP"`. Expect: "valid on disk" and "satisfies its Designated Requirement".
- [ ] **[HT-02]** `spctl -a -vv "$APP"`. Expect: `rejected`, `origin=gnui-dev` (the build is not notarised). Write down the exact output.
- [ ] **[HT-03]** Gatekeeper, only if the app arrived by AirDrop or a download (quarantined): open it once. Note exactly what Gatekeeper says, then use a way past it from HANDTEST-BUILD, section 3. Signing with a Developer ID and notarisation are untested: do not try them, only record what you see.
- [ ] **[HT-04]** `open -n "$APP" --args -demo`. Expect: a window with sample chats opens, the menu bar title is "ZapFast", and the Dock icon shows.
- [ ] **[DM-001]** With upstream ZapFast running, start the demo too. Expect: both run side by side, the demo has no status item and does not touch the real app's data.

## 2. Menu bar, windows and keyboard (demo)

- [ ] **[PR-004]** On the main window and on Settings, walk with Tab and Shift+Tab; zoom with ⌘= and ⌘-; drag over message text. Expect: focus visits every control in turn, the interface zooms, the dragged text is selected.
- [ ] **[SH-006]** Press ⌘B. Expect: the chat list gives way to a rail of avatars with unread badges; names show on hover; a click opens the chat; ⌘B again restores the list.
- [ ] **[SH-007]** Make the window narrow (about 500 pt) and click a chat. Expect: the chat slides in from the right over the list; "Back to chats" slides it out again.
- [ ] **[SH-012]** If your mouse has side buttons: open two chats in turn, then press Back and Forward. Expect: the chats are stepped through in order; in a narrow window Back from a chat returns to the list. Skip without such a mouse.
- [ ] **[SH-016]** Click a chat, then Tab through the window and into the message field and out again. Expect: focus moves in the order the controls appear on screen, never to a hidden control.
- [ ] **[SH-019] [MB-021]** View > Enter Full Screen (⌃⌘F), then Exit Full Screen. Repeat using the native green traffic-light button after the window is visible and active. Expect: each entry reaches a full-screen Space, the menu changes to "Exit Full Screen", each exit returns to the normal window, and input/redrawing still work. Record a stuck transition or a green button that never enables; do not count merely starting the animation as success. Also perform the existing viewer and call checks (VW-007, CA-009), not duplicate copies of this check.
- [ ] **[KB-028] [MB-009]** Press ⌘W on the main window, on Settings and on the photo viewer; repeat with File > Close Window. Expect: each closes only the key window and the app stays running in the Dock (Keep running is on by default; the demo has no menu-bar item).
- [ ] **[MB-024]** After closing the main window, choose Window > Show ZapFast. Expect: the main window comes back.
- [ ] **[KB-029] [MB-007]** Quit with ⌘Q, then again from the ZapFast menu's "Quit ZapFast". Expect: both quit the app. (That the draft and settings survive a relaunch is HT-29, group 16.)
- [ ] **[MB-003] [MB-004] [MB-005] [MB-006]** In the ZapFast menu use Services, Hide ZapFast (⌘H), Hide Others (⌥⌘H) and Show All. Expect: each does what it says, with titles in the interface language.
- [ ] **[MB-010] [MB-011] [MB-012] [MB-015]** Type in the message field, then use Edit > Undo, Redo, Cut and Select All. Expect: each acts on the field's text.
- [ ] **[MB-022] [MB-023] [MB-025]** Window > Minimize (⌘M), Window > Zoom, and with Settings open check the Window menu's list. Expect: the window minimises and zooms, and the menu lists the open windows.
- [ ] **[MB-028]** Open the Help menu. Expect: the system's search field is at the top, then "Keyboard Shortcuts" and "ZapFast Help".
- [ ] **[MB-029]** Close the main window (⌘W), then choose Edit > Select All. Expect: no edit is queued and the window stays closed. Choose File > New Chat…. Expect: the window returns with the dialog. Repeat from the closed window for About, Keyboard Shortcuts and Edit > Find; each should show the window before its UI, with Find focusing Search. Also hide the open window with ⌘H, choose an edit command, then show it: the field must not have changed.
- [ ] **[KB-022]** Press ⌘, to open Settings, then ⌘, again. Expect: the second press closes Settings.
- [ ] **[DL-017]** Open ZapFast > About ZapFast, then press ⌘F, ⌘N and ↑. Expect: nothing under the dialog reacts.

## 3. Dialogs and native alerts (demo)

- [ ] **[HT-05]** For each of `delete-chat`, `unlink`, `clear-media`, `clear-chat` and `leave-group`, run `open -n "$APP" --args -demo-page NAME`. Expect: a native sheet drops from the window title bar; the title is a question (for example "Delete chat?"), the default button is Cancel and the destructive one is red. Cancel or Escape closes it and nothing is deleted. (Leave group also offers "Leave group and archive".)
- [ ] **[HT-06]** Answering a native alert: in a normal demo window right-click a chat, choose "Lock chat", and press the default "Lock chat" button. Expect: the sheet closes and the "Set up locked chats" dialog opens. Repeat and choose Cancel: nothing changes.
- [ ] **[HT-07]** Right-click a chat, choose "Delete chat", and press the red "Delete" button. Expect: the sheet closes and the chat leaves the list. If the demo core refuses, a toast says so: write down which happened.

## 4. Chat list and transcript (demo)

- [ ] **[CL-034]** Narrow the list until its chips overflow. Scroll over the filter and label rows with a plain wheel, Shift+wheel and a touchpad swipe; each row scrolls sideways independently. Hover a row and drag its floating scrollbar; move away and it hides without taking space. Chip clicks and Tab still work. In flat light/dark themes the right edge fades; Messages omits the filter fade but keeps the label fade.
- [ ] **[CV-009]** Open a long chat, scroll up, and send a message. Expect: the view stays where you scrolled.
- [ ] **[HT-08]** While scrolled up, wait for the demo's automatic reply to your message. Expect: a round arrow button appears at the bottom right with an unread count; clicking it scrolls to the newest message and the count goes.
- [ ] **[HT-41] [CS-002]** Open a chat with a long history (in a group, to see names), press ⌘F and search for a word from the middle of a line. Expect: each result shows the time it was said, the line with some text before the match, the match in bold and, in a group, the sender's name.
- [ ] **[CV-020]** Star and pin a message, and edit one of yours. Expect: the footer shows star, "edited", pin, time and ticks, beside the last line of text when it fits and on its own line when it does not.
- [ ] **[HT-09]** Click a quoted reply in a bubble. Expect: the chat scrolls to the original message and it flashes.
- [ ] **[HT-10]** Right-click a message and choose Select, tick two more, then press Escape. Expect: a selection bar with "Delete…" takes the composer's place; Escape ends the selection and the composer returns.
- [ ] **[HT-11]** Hover a message and click the round React button. Expect: the reaction picker opens beside the bubble; choosing an emoji adds a pill, clicking your pill removes it.
- [ ] **[MM-014]** In Margaret's chat right-click the location and the shared contact and choose "Copy text", then paste into the message field. Expect: readable text for each.
- [ ] **[MM-021]** Right-click a message. Expect: "Reload earlier messages" is enabled. Then run `open -n "$APP" --args -demo-page offline` and right-click again. Expect: it is disabled.

## 5. Composer (demo)

- [ ] **[HT-12]** Click the plus button (named "Attach"). Expect: the menu offers "Send files", "Send as document", "Send location" and "Create poll"; the last two open their dialogs.
- [ ] **[CP-014]** In Finder copy a file (⌘C), click the message field and press ⌘V. Expect: the file is staged as a chip above the field and no text is inserted.
- [ ] **[CP-009]** Turn on a Japanese (Romaji) input source. In the group chat type `@` then `ni`. Expect: the suggestions close while the text is underlined. Press Enter: the text is committed and nothing is sent. Press Enter again: the message is sent. Repeat with Dictation (Fn Fn) and with Writing Tools on a selected sentence. Expect: neither sends.
- [ ] **[DM-007]** Click "Record a voice message" (the microphone in the empty field). Expect: no permission prompt in demo; a bar shows Discard, a red light, a counting time and Send; Escape discards.

## 6. Media, viewer, cropper and sticker maker (demo)

- [ ] **[VW-001] [VW-009] [VW-010] [MK-018]** Open Ada's video in the viewer and press Play. Expect: it plays with sound; Space pauses, M mutes, S changes the speed, ← and → jump five seconds, dragging the seek bar seeks. A motion photo's badge plays its clip and returns to the still.
- [ ] **[MK-014] [MK-016]** Play a clip in its chat bubble, then double-click it. Expect: it plays in the bubble; the double click opens the viewer at the same position, already playing.
- [ ] **[VW-007]** In the viewer click "Enter Full Screen". Expect: the window fills the screen and the button reads "Exit Full Screen"; Escape closes the viewer from either state.
- [ ] **[VW-002]** On a trackpad, over a zoomed photo: pinch, two-finger double tap, ⌘ with two-finger scroll, and drag. With a mouse: a wheel notch and a double click. Expect: each zooms or pans as expected, and the open hand shows over a draggable photo and the closed hand while dragging.
- [ ] **[VW-003]** With the viewer in front press ⌘+, ⌘- and ⌘0. Expect: only the picture zooms, not the interface.
- [ ] **[CR-001] [CR-002]** Stage a picture (paste or drop it) and open it from its chip. Expect: the outside of the crop is dimmed and handles show. The pointer is a resize arrow on edges, diagonal on corners, an open hand inside and a closed hand while dragging.
- [ ] **[CR-003]** In the cropper click "Turn left" and "Turn right" on a PNG, a HEIC, a TIFF and a BMP. Expect: the picture turns for each format.
- [ ] **[SM-002]** Open the emoji, GIF and sticker picker, go to Stickers, open the add page (+) and choose "Make a sticker from a picture…" with a wide picture. Expect: the square stays inside the picture, can be dragged, and the Size slider resizes it.
- [ ] **[IN-007]** Open Info for a group you can edit (Rust Berlin) and click its large picture. Expect: a menu with "Change photo" (and "Remove photo" if it has one); Change photo opens the file picker.

## 7. Calls, screens only (demo)

- [ ] **[CA-001]** Press ⌘⇧2 (Calls), choose New call and press a contact's video button. Expect: the call window says "Connecting…", "Calling…", "Ringing…", then shows a running timer after about 2.4 s. The demo makes no sound. Hang up.
- [ ] **[CA-003] [CA-004]** `open -n "$APP" --args -demo-page call-video-incoming`. Expect: only the call window appears, with the caller's picture, "Incoming video call" and solid Accept and Decline buttons; no main window. Accept starts the call.
- [ ] **[CA-009]** In a demo call click the corner "Full screen" button. Expect: the window fills a full-screen space and the button reads "Exit full screen". The first Escape leaves full screen and the call goes on; the second Escape puts the call aside to a bar showing "Return to the call" and Hang up.
- [ ] **[CA-014]** In a demo video call click Devices. Expect: Microphone, Speaker and Camera lists open and show the Mac's devices (the demo lists two cameras).
- [ ] **[CA-017]** `open -n "$APP" --args -demo-page call-group`. Expect: a tile and name plate for everyone, "You" with a red microphone while muted, and a person who is only invited dimmed with "Invited". Joins and leaves are checked with a real call in group 15.
- [ ] **[HT-13]** In a demo video call look at both pictures. Expect: the other side fills the window (letterboxed, not cropped, not mirrored), your own camera sits in a corner, and both keep moving.

## 8. Settings, theme and language (demo)

- [ ] **[TH-002]** Settings > Appearance > Theme: "Follow system". With Settings open, switch System Settings > Appearance between Light and Dark. Expect: the main window and Settings both follow within about a third of a second.
- [ ] **[HT-14]** Choose Light, Dark and Messages in the Theme list. Expect: every window changes at once and the choice is kept for the session.
- [ ] **[HT-15]** First frame: set Follow system, quit, set the system to Dark, and start `open -n "$APP" --args -demo`. Expect: the first frame is already dark, with no flash of light. Repeat from Dark to Light.
- [ ] **[CV-035]** Settings > Appearance > Wallpaper > "Choose image…" and pick a photo. Expect: the preview and the chat background show it filling the pane, cover-fitted; "Remove image" brings the colour back.
- [ ] **[SE-032]** Settings > Notifications. Set "Message sound" to "Pidgin alert" and press Play; then choose "Choose a file…", pick an `.mp3` and press Play. Expect: you hear a built-in sound, then your file.
- [ ] **[HT-16]** Language: Settings > Appearance > Language > "Deutsch". Expect: the menu bar titles switch to German at once ("Ablage", "Bearbeiten", "Darstellung", "Fenster", "Hilfe"; the ZapFast menu shows "Über „ZapFast“", "Einstellungen …", "Dienste", "„ZapFast“ ausblenden", "Andere ausblenden", "Alle einblenden" and "„ZapFast“ beenden"), and Settings shows German section names ("Darstellung", "Benachrichtigungen", "Datenschutz"). Switch back to "English": the menu bar and Settings return to English; "Auto" follows the system language.

## 9. Accessibility (demo)

- [ ] **[AX-001] [AX-003]** Turn VoiceOver on (⌘F5). Walk the transcript and wait for the demo's automatic reply. Expect: VoiceOver reads the messages and announces the new one. Run `open -n "$APP" --args -demo-page offline` and `-demo-page toasts`. Expect: the connection change and each toast are announced.
- [ ] **[SL-012]** In the demo window choose Select on a message and move VoiceOver to its check box. Expect: its name is "Select message from {sender}, {time}: {summary}".

## 10. Linking and the Keychain (real)

- [ ] **[LG-015]** Start `open "$APP"` with no account. Drag the window by the top strip of the QR screen. Expect: the window moves.
- [ ] **[HT-17]** On Phone A open WhatsApp > Linked devices > Link a device and scan the QR code. Expect: the chat list fills, a banner reads "Loading chat history… N%" and a "History loaded" toast follows. Also try "Link with phone number instead" in a second run: a code of the form XXXX-XXXX appears and links when entered on the phone.
- [ ] **[HT-18]** Keychain prompt: note whether macOS asked for access to a Keychain item when the account linked or at the next launch. Expect: at most one prompt with "Always Allow"; after you allow it, a relaunch does not ask again. Write down the prompt's wording.
- [ ] **[FL-003]** `security find-generic-password -s ai.a3lab.zap`. Expect: it lists the archive key's item (attributes only). Then `sqlite3 ~/Library/Application\ Support/ai.a3lab.zap/accounts/<id>/archive.db '.tables'`. Expect: "file is not a database".
- [ ] **[HT-19]** Keychain ACL: in Terminal run `security find-generic-password -s ai.a3lab.zap -w >/dev/null`. Expect: macOS asks permission for `security` (or refuses outright); click Deny. ZapFast itself still opens its chats. Never click Allow here.
- [ ] **[LG-011]** Quit, put Phone A in flight mode, relaunch. Expect: the link holds and stored chats are readable.
- [ ] **[LG-009]** Quit. In Keychain Access choose File > Lock Keychain "login", then start ZapFast (cancel macOS's own unlock prompt). Expect: an error with a "Try again" button; unlock the keychain and press it: the chats load.
- [ ] **[LG-010]** A missing archive key offers "Start over" with a confirmation. No setup steps are known: leave this unticked and write "not tested" if you cannot arrange it.

## 11. App lock, chat lock and file safety (real)

- [ ] **[AL-001]** Settings > Privacy > App lock > "Set password…". Enter a password of at least 6 characters twice and press "Turn on". Expect: toast "App lock password saved".
- [ ] **[AL-005]** Press ⌘⇧L (Lock ZapFast). Expect: "ZapFast is locked" with the password field focused. In Terminal `ioreg -l -w 0 | grep kCGSSessionSecureInputPID` shows ZapFast's pid (`pgrep -f 'Contents/MacOS/zapfast'`). A wrong password is refused and makes you wait; after unlocking with the right one, the pid is gone.
- [ ] **[AX-001]** With the app locked and VoiceOver on, send yourself a message from Phone B. Expect: VoiceOver says nothing about the message while locked.
- [ ] **[HT-20]** Chat lock: right-click a chat, choose "Lock chat" and confirm. Expect: "Set up locked chats" asks for a "Local code" and "Confirm code"; "Set code and open" opens the Locked tab. A "Locked" button is now in the rail.
- [ ] **[HT-21]** Change the code, then close Settings at once. Leave the Locked tab first. Open Settings > Chats > "Locked chats code", type a new code (several characters, for example `new1234`), and close Settings with ⌘W within a second. Click "Locked" in the rail. Expect: the old code is refused and the new one opens the tab.
- [ ] **[HT-22]** Attachment safety: from Phone B send a `.command` file and a `.terminal` file to the test number. Right-click each in the chat and choose "Open file". Expect: a toast "For safety, open this file yourself from its folder", Finder reveals the file, and nothing runs.
- [ ] **[HT-23]** Turn the app lock off again: Settings > Privacy > App lock > "Turn off…", enter the password. Expect: toast "App lock turned off".

## 12. Messages with a second phone (real)

- [ ] **[CL-033]** Select the Unread chip, quit, relaunch. Expect: Unread is still selected.
- [ ] **[CV-034]** From Phone B send Hebrew, Arabic and mixed left-to-right and right-to-left lines. Expect: they lay out correctly, each line in its own direction.
- [ ] **[MM-016]** Turn off Settings > Chats > "Download files automatically", send Phone B's photo, and right-click the photo. Expect: "Download", or a greyed "Downloading…" while it loads.
- [ ] **[MM-023]** Delete a message for everyone on Phone B and right-click it in ZapFast. Expect: its menu has no Reply, Star or Pin.
- [ ] **[MM-011]** Turn Wi-Fi off, right-click your own message and choose "Delete for me". Expect: it says it needs a connection. Turn Wi-Fi on. Expect: the delete goes through.
- [ ] **[IA-008] [IA-009]** Where you have an interactive message with buttons, answer a button on Phone A. Expect: the answer shows in ZapFast as an ordinary reply with its quote. Select the interactive message's text, search for it and use "Copy text": all three work.
- [ ] **[PL-008]** Create a poll in a chat, quit ZapFast, vote on Phone B, relaunch. Expect: the vote is there.
- [ ] **[CP-013]** In a chat press Attach > "Send files". Expect: a file sheet opens; choose two files and both stage as chips. Then Attach > "Send as document" with a PNG and send. Expect: Phone B receives a document with its own name.
- [ ] **[SE-072]** Settings > Files > Downloads > "Change…", pick a folder, and download the same `report.pdf` twice. Expect: the second is `report (1).pdf`.
- [ ] **[MM-014]** Send an interactive message, a location and a contact from Phone B and use "Copy text" on each. Expect: readable text on the clipboard.
- [ ] **[HT-24]** Scroll up in a chat, put another app in front, and send two messages from Phone B. Come back. Expect: the round arrow at the bottom right shows an unread count of 2; clicking it jumps to the newest message.

## 13. Voice notes and the microphone (real)

- [ ] **[HT-25]** Reset the answer with `tccutil reset Microphone ai.a3lab.zap`, then restart ZapFast. Open a chat and click "Record a voice message". Expect: macOS asks for the microphone; after "Allow", recording starts at once. The bar shows Discard, a pulsing red light, a counting time, the waveform and Send.
- [ ] **[CP-023]** Speak for about 3 seconds and press Send. Expect: the waveform rises with your voice and Phone B receives a voice note about 3 s long. Whisper one more: it plays at normal loudness.
- [ ] **[CP-023]** Choose Reply on a message, record, and press Enter. Expect: the voice note arrives quoting that message.
- [ ] **[CP-023]** Record and press Escape. Expect: nothing is sent. Record, then lock the app (⌘⇧L) and unlock. Expect: nothing was sent and the recording is gone. Record and press Send within half a second. Expect: nothing is sent and no toast appears.
- [ ] **[CP-025]** Turn ZapFast off in System Settings > Privacy & Security > Microphone and click the microphone. Expect: toast "Could not record: microphone access is off for ZapFast (System Settings, Privacy & Security, Microphone)", and System Settings opens on the Microphone page. Turn it back on.
- [ ] **[CP-024]** With Wi-Fi off, record and send. Expect: "Voice message (0:0N) not sent" waits above the field and Send replaces the microphone. Turn Wi-Fi on and press Send: it is sent. Do it again and press Enter instead: it is sent too.

## 14. Notifications, Dock and the menu-bar item (real)

- [ ] **[NT-001]** With ZapFast in the background (another app in front), send a message from Phone B. Expect: the first one asks for notification permission once; the banner then shows the chat name, the sender in groups, the text and the chat's picture. Allow banners in System Settings > Notifications > ZapFast if asked.
- [ ] **[NT-003]** Click the banner. Expect: ZapFast comes forward on that account and chat, scrolls to the message and flashes it. With the app lock on and locked: the click shows the lock, and the chat opens after unlocking. A locked app's banner reads "New message" ([NT-005]).
- [ ] **[NT-015]** From the next banner choose Reply (pull it down or hover), type text and press Send. Expect: the text goes to that chat on Phone B, the chat is marked read in ZapFast and the banner goes.
- [ ] **[NT-004]** Read the chat in ZapFast while a banner is still up. Expect: the banner goes away on its own.
- [ ] **[NT-008]** With two unread chats the Dock icon shows 2. Read one. Expect: 1. Close the window and reopen it from the menu-bar item. Expect: the badge is unchanged.
- [ ] **[NT-009]** Look at the menu bar. Expect: a speech-bubble icon. Its menu reads "Show or hide ZapFast", "Lock ZapFast" (only with an app lock password set) and "Quit". Show or hide toggles the window; Lock locks; no chat name appears in the menu.
- [ ] **[NT-010]** Settings > System > "Keep running when the window closes" on: close the window. Expect: the menu-bar item and Dock icon stay. Turn it off and close the window again, without relaunching. Expect: the app quits.
- [ ] **[HT-26]** Keep running in the menu bar: turn the setting on, close the main window with ⌘W, then send a message from Phone B. Expect: a banner still appears. Click the menu-bar item's "Show or hide ZapFast". Expect: the message is in the chat.
- [ ] **[AC-008]** With two linked numbers, send a message to the number that is not on screen. Expect: the banner uses that account's contact names, and clicking it switches to that account. Skip without a second number.

## 15. Calls (real)

- [ ] **[CA-001]** From ZapFast call Phone B with a voice call. Expect: the screen says "Connecting…", "Calling…", then "Ringing…". A soft 425 Hz tone plays for one second in every five. It stops at once when Phone B answers or when you hang up.
- [ ] **[HT-27]** Ringback follows the notification sound: Settings > Notifications, set "Message sound" to "None" (or turn "Desktop notifications" off) and call Phone B again. Expect: no ringback tone. Restore the setting.
- [ ] **[CA-005]** With the same setting still "None", call the test number from Phone B. Expect: no ringtone. Restore "Message sound" and call again. Expect: the ringtone plays until you answer, decline or the caller hangs up.
- [ ] **[CA-003]** Call the test number from Phone B. Expect: Accept connects audio both ways; Decline stops the ringing on Phone B.
- [ ] **[CA-004]** Close the main window (⌘W, Keep running on) and call the test number. Expect: a banner "Incoming voice call", the ringtone, and only the call window with the caller's picture and a solid Accept. Accept stops the ringtone. With app lock locked, the banner says "Incoming call" and no window shows who calls until you unlock.
- [ ] **[CA-030]** Put another app in front and let a call ring. Click the banner. Expect: the call's account comes on screen, the call window opens and the main window comes forward.
- [ ] **[CA-029]** With another app in front, let a call ring. Expect: the Dock icon bounces until you answer, decline or the call ends. A plain message does not bounce it.
- [ ] **[HT-28]** Place a video call to Phone B (allow the Camera prompt) and answer it there. Expect: the other side's video fills the window, your own camera shows in a corner, and both move. Turn the camera off with "Turn the camera off": Phone B sees it go.
- [ ] **[CA-014]** In a call click Devices and pick another microphone. Expect: Phone B hears it and System Settings > Sound is unchanged. Unplug a chosen headset. Expect: the screen says "Microphone “name” is not available; using the system default" and the call goes on.
- [ ] **[CA-017]** Join a group call of three or more with some cameras on. Expect: everyone has a tile; a person with a camera on fills their tile with the name on a plate; "You" shows a red microphone while muted; people asked but not in are dimmed with "Invited"; tiles follow joins and leaves; hanging up leaves the call to the others.
- [ ] **[CA-009]** In a real call use "Full screen". Expect: the first Escape leaves full screen and the second puts the call aside.

## 16. System behaviour (real)

- [ ] **[SH-014]** Move and resize the window and zoom it. Quit and relaunch. Expect: it opens in the same place, size and zoom. Then drag it onto an external display, unplug the display, and relaunch. Expect: it comes back on a screen.
- [ ] **[HT-29]** Type a draft in a chat, change a setting such as Larger zoom, press ⌘Q and relaunch. Expect: the open chat's draft and the setting are kept.
- [ ] **[NT-011]** With the window hidden, run `"$APP/Contents/MacOS/zapfast"` again. Expect: no second process or window appears and the existing window comes forward.
- [ ] **[NT-012]** Quit from the menu-bar item, from the Dock icon's menu, with ⌘Q and with `"$APP/Contents/MacOS/zapfast" quit`. Expect: each ends the app. With nothing running, `"$APP/Contents/MacOS/zapfast" quit; echo $?` prints 0 and starts nothing.
- [ ] **[NT-013] [SE-051]** Settings > System > "Start at login" on. Expect: System Settings > General > Login Items lists ZapFast (approve it if asked). Run `"$APP/Contents/MacOS/zapfast" --start-hidden`. Expect: no window, no Dock bounce, the menu-bar item present, and a message still notifies. Turn the setting off. Expect: the login item is gone.
- [ ] **[NT-014]** Put the Mac to sleep for a minute and wake it. Expect: the link shows reconnecting then connected without a click; `~/Library/Application Support/ai.a3lab.zap/settings.json` holds a setting you changed just before sleeping.
- [ ] **[LN-005]** `open 'whatsapp://send?phone=<a chat's number>'`. Expect: the window comes forward on that chat. With ZapFast quit, the same command starts it and opens the chat once the list is in.
- [ ] **[LN-007]** `"$APP/Contents/MacOS/zapfast" wa.me/<number>` opens the chat; `... reload-themes` refreshes the theme list; `... quit` quits. Expect: each exits 0 at once.
- [ ] **[FL-004]** Turn Wi-Fi off for a minute, then read `~/Library/Application Support/ai.a3lab.zap/zapfast.log`. Expect: WARN or ERROR lines from the app (`level=WARN msg=…`) and from the core (`source=core target=…`). Quit, turn Wi-Fi on, relaunch. Expect: the first run's lines are gone.
- [ ] **[PR-006]** Use chats, GIF search and an update check, then `lsof -nP -i -a -p $(pgrep -f 'Contents/MacOS/zapfast')` several times. Expect: every remote peer is WhatsApp, GIPHY, the Signal sticker CDN or the update feed.
- [ ] **[HT-42] [AC-005] [FL-003] Dedicated TEST account removal and its real Keychain item.** Human-only, destructive to the selected test account's local archive/cache: use two dedicated test numbers, switch to the disposable second one, and use Settings > Account > "Remove this account" and its confirmation. Never use Adam's account, never remove the last account (that path unlinks instead), and never delete a key manually to make this check pass. Before removal, identify the disposable account's exact `accounts/<id>` folder and derive its Keychain account as `archive-` plus the lowercase SHA-256 of that folder's canonical absolute path (no trailing newline, matching `state.ArchiveKeyIdentity`). Record that identifier locally as `TEST_KEY`; confirm the exact item with `security find-generic-password -s ai.a3lab.zap -a "$TEST_KEY"` (attributes only, never `-w` or `-g`). After the app finishes removal, repeat the exact lookup. Expect: the test archive/cache are gone, that item is not found, and the retained test account still opens its chats. If the folder/account mapping or initial item is uncertain, stop and mark blocked. Report only pass/fail and any error, not key material or account identifiers. Automated evidence uses a fake keyring; this real-Keychain check has not been run.

## 17. Updates (real)

- [ ] **[UP-003]** Needs a notarised build in Applications and a feed with a newer signed zip, which do not exist yet. Skip unless you have both, and write "skipped". If you have them: Settings > Updates > "Check now", "Download update", "Restart to update". Expect: the new version opens and Settings shows its version.
- [ ] **[UP-006]** Needs an update to be on offer, so skip it without a feed. Otherwise put a copy of the app (or a symlink to it) under `/opt/homebrew/Caskroom/zapfast-test/` and open it from there. Expect: "Restart to update" is refused with upstream's Homebrew wording and a "Download from GitHub" link.

## 18. Translations and final numbers

- [ ] **[HT-30]** Language "German": lock the app (⌘⇧L; set a password first if none) and open Settings > Updates. Expect: the lock screen and the updates text fit with no cut-off words or English leftovers. Long prose is still awaiting a native review: note anything that reads wrongly.
- [ ] **[HT-31]** Language "简体中文": look at the menu bar, Settings, the lock screen and the updates text. Expect: no truncation, no English leftovers.
- [ ] **[HT-32]** Warm startup: run `"$APP/Contents/MacOS/zapfast" -demo -exit-first` five times. Expect: a JSON report each time; write down the median time to the first frame (target 150 ms or less).
- [ ] **[HT-33]** Idle: run `"$APP/Contents/MacOS/zapfast" -demo -idle 10s`. Expect: a JSON report with no frames built and at most one wakeup a second.
- [ ] **[HT-34]** Memory: open a chat with plenty of messages, scroll for a minute, and run `footprint -p $(pgrep -f 'Contents/MacOS/zapfast')`. Expect: `phys_footprint` of 150 MB or less.
- [ ] **[HT-36]** The 10k-message scroll and the allocation gate (`-bench -gate`) are measured on the Mac mini. Do not run them here: see [the measurements](measurements/zapfast-macmini.md), "Re-running". (The release size, HT-35, is in group 19.)

## 19. Release build (real)

Use the release bundle, built from the same revision and installed as in HANDTEST-BUILD (sections 1 and 2) in place of the hand-test build. These five checks are the short release-only group, not a second full checklist. It has no demo, no measurement flags and no Debug menu; use the retained dedicated test account. The diagnostics-enabled `--dev` bundle cannot substitute for it. A locally signed release build is not evidence of Developer ID signing or notarization. Afterwards, copy the hand-test build back if you test again.

- [ ] **[HT-37]** `open "$APP"`. Expect: it starts like the hand-test build, on your chats (or the sign-in card if no account is linked), and the menu bar has no Debug menu.
- [ ] **[HT-38]** Open a chat, send Phone B a message, then quit with ⌘Q. Expect: Phone B receives it, and ZapFast quits at once (`pgrep -f 'Contents/MacOS/zapfast'` prints nothing).
- [ ] **[HT-39]** Open Settings > About, then ZapFast > About ZapFast. Expect: "ZapFast 0.19.0" and "Version 0.19.0", the version that `defaults read "$APP/Contents/Info.plist" CFBundleShortVersionString` prints.
- [ ] **[HT-35]** Release size: `ls -l "$APP/Contents/MacOS/zapfast"`. Record the exact byte count. Expect: never over about 12.2 MB; 12,029,504 B is a historical reference, not a measurement of the current build.
- [ ] **[HT-40]** Optional: `"$APP/Contents/MacOS/zapfast" -demo; echo $?`. Expect: "flag provided but not defined: -demo", then `2`. Release builds have no demo, as upstream's.

## Appendix: optional advanced engineering checks

These checks need specialist fixtures and equipment. Skip this appendix during ordinary hand testing; an unavailable prerequisite means **blocked**, not passed. Do not use Adam's archive or personal accounts.

- [ ] **[MK-033] Optional, advanced engineering: attachment deadlines and expired-media recovery.** Follow the [detailed procedure and evidence boundary in logic.md](parity/logic.md#advanced-engineering-procedure-mk-033). Prerequisites: dedicated test accounts and a second phone; fresh, uncached attachments (65 MiB and a known size); established expired-media fixtures both with and without the original on the phone; and a controlled network shaper that stalls only an established media transfer without resetting its socket or disrupting the WhatsApp control channel. No TLS interception or private URL/key logging. Expect: manual download bypasses the automatic size limit; a stalled transfer times out by the size-based deadline and can be retried; an expired attachment recovers through one automatic re-upload retry when available, or reaches a bounded failure with a reader-triggered retry, never an endless spinner. Record version, sizes, elapsed times and each observed or blocked subcase. Without a safe shaper or established expiration provenance, record the affected subcase **blocked**; disconnecting Wi-Fi or completing an old download does not prove it. **Current outcome: not executed; end-to-end network/re-upload evidence remains missing and MK-033 stays partial.**

## Report back

For each failed or odd check, write one entry:

- **Check id** (for example `CA-009` or `HT-21`), and which mode you were in (demo, real, or the release build).
- **What happened**, in one or two lines, next to what the check expected.
- **A screenshot** (⌘⇧4, or ⌘⇧5 for a recording). For a prompt or an alert, capture the whole dialog.
- **The log**, when the app misbehaved: attach `~/Library/Application Support/ai.a3lab.zap/zapfast.log` and `panic.log` if it exists.

Checks you skipped or could not arrange get "skipped" and the reason. Send the list to the coordinator when you finish.
