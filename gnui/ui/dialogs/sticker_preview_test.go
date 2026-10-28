package dialogs

import (
	"testing"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// Upstream always toasts preview errors, but only closes the originating
// account's visible pack dialog (src/app.rs:3967-3975).
func TestStickerPreviewErrorsKeepTheirAccount(t *testing.T) {
	for _, tc := range []struct {
		name              string
		offscreen, locked bool
		kind              state.DialogKind
		success           bool
	}{
		{"offscreen pack", true, false, state.DialogStickerPack, false},
		{"offscreen other dialog", true, false, state.DialogAbout, false},
		{"offscreen locked", true, true, state.DialogStickerPack, false},
		{"offscreen success", true, false, state.DialogStickerPack, true},
		{"active pack", false, false, state.DialogStickerPack, false},
		{"active other dialog", false, false, state.DialogAbout, false},
		{"active no dialog", false, false, state.DialogNone, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := state.New()
			f := fake.New(nil)
			if tc.offscreen {
				s.AddAccount("1", nil, fake.New(nil))
			}
			s.AddAccount("2", nil, f)
			scr := New()
			scr.st = s
			s.Listen((*listener)(scr), abi.EventTagStickerPackPreview)
			s.OpenDialog(state.Dialog{Kind: tc.kind})
			s.Lock.Locked = tc.locked
			f.Emit(ev(abi.EventTagMe, func(e *abi.Event) { e.Me.Name, e.Me.HasName = []byte("Studio"), true }),
				ev(abi.EventTagStickerPackPreview, func(e *abi.Event) {
					if !tc.success {
						e.StickerPackPreview.Result.Tag = abi.StickerPackResultTagErr
						e.StickerPackPreview.Result.Err.Error = []byte("Pack unavailable")
					}
				}))
			s.Pump(0)
			if tc.offscreen && scr.pack.ready {
				t.Error("off-screen result populated the on-screen preview")
			}
			wantKind := tc.kind
			if !tc.offscreen && tc.kind == state.DialogStickerPack && !tc.success {
				wantKind = state.DialogNone
			}
			if s.Dialog.Kind != wantKind {
				t.Errorf("dialog %v, want %v", s.Dialog.Kind, wantKind)
			}
			want := "Pack unavailable"
			if tc.success {
				want = ""
				if len(s.Toasts) != 0 {
					t.Fatalf("success toasted: %+v", s.Toasts)
				}
			} else if len(s.Toasts) != 1 || string(s.Toasts[0].Text) != want || !s.Toasts[0].Error {
				t.Fatalf("error toasts: %+v", s.Toasts)
			}
			if tc.offscreen && !tc.success {
				want = "Studio: " + want
			}
			if tc.locked {
				want = ""
			}
			if got, urgent := s.TakeAnnouncement(nil, !s.Lock.Locked); string(got) != want || urgent != (!tc.locked && !tc.success) {
				t.Errorf("said %q, urgent=%v; want %q", got, urgent, want)
			}
		})
	}
}
