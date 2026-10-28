// Package viewer is the media viewer window (state.WinViewer).
//
// It ports zf:src/ui/image_preview.rs and zf:src/image_preview.rs: one
// picture or clip of the open chat's album at a time, in a window of its own
// (it can go full screen), with the album along the bottom and the actions of
// its message in the toolbar.
//
// Entry point (the shell's slot, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.WindowScreen: Set.Viewer
//
// # Asking for it
//
// A screen sets state.State.Win.ViewerChat (the chat) and ViewerMsg (the
// message id, and for a picture of an interactive message's carousel the
// card's number in decimal as a second id), then State.OpenWindow(WinViewer).
// The viewer asks the core for the album (Command::LoadChatMedia, around the
// message) and shows what arrives. Asking again while it is open moves it;
// stepping through the album writes the item on screen back to ViewerMsg.
//
// # What it does
//
//   - Photos open fitted to the stage, never enlarged past their own pixels,
//     and zoom from 25 % to 400 % (1 is one picture pixel per window pixel):
//     the wheel (a notch is a step), ⌘ or Ctrl with a trackpad's two fingers,
//     a pinch (Signal.Magnify), the header's Zoom in, Fit or the scale, Zoom
//     out, and the keys + − 0; a double click or smart zoom (Signal.SmartMagnify)
//     switches between fit and the original size around the pointer; dragging
//     or a trackpad's two fingers pan. Images decode on workers (package img)
//     at the size shown, from the file the core keeps in its cache; a pinch asks
//     for a few decodes, not one a frame, and the picture on screen stays until
//     the sharper one is ready.
//   - ← and →, the arrows at the picture's sides on hover, and the strip along
//     the bottom step through the album; the current item is ringed.
//   - The header replies, reacts (the full picker, opened in the main window beside the
//     message's bubble), forwards (the forward dialog, through the state), saves
//     (the core's save panel) or downloads, shows the message in its chat,
//     copies a photo, opens the file in another app, goes full screen and
//     closes. Escape, a click on the empty field, the account switching, the
//     chat's removal, or its locking outside the open folder close it.
//   - An item not downloaded shows its own thumbnail and a Download button; a
//     picture that cannot be decoded says so, with "Open externally".
//
// Video playback waits for media.Video (video.go, TODO S8): a clip shows its
// poster and Play hands it to the system's player.
package viewer
