// Package calls is the call window (state.WinCall), the Calls page (state.PageCalls) in the list column, its content pane, and the bar of a call put aside.
//
// It ports zf:src/ui/call.rs, zf:src/ui/calls.rs and zf:src/app/calls.rs (the call window: frames from the core, mute, camera, devices, Dock attention; the Calls page's history and new-call list).
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	NewWindow() *Window   // screen.WindowScreen: Set.Call
//	NewPage() *Page       // screen.Pane: Set.CallsPage
//	NewContent() *Content // screen.Pane: Set.CallsContent ("Start call")
//	NewBar() *Bar         // screen.Band: Set.CallBar
//
// and for other packages Place, LiveHere (the conversation header), JoinLink
// and SetDock (sys's hooks).
//
// They share one hub per *state.State (hub.go): it keeps the call's state
// from the core's Call, CallDevices and Incoming events, the per-account call
// logs, and opens and closes WinCall. Calls placed here are numbered by the
// core's counter (core.CallNumbers); video frames come from the call's core
// (core.Frames, video.go) and are not drawn until the framework's external
// textures land.
package calls
