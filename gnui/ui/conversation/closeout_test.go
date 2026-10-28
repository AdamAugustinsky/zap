package conversation

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	ztheme "zapfast/theme"
)

func TestReceiptTicksRespectAccountPrivacy(t *testing.T) {
	for _, chat := range []string{ada, group} {
		for _, status := range []abi.Delivery{abi.DeliveryDelivered, abi.DeliveryRead, abi.DeliveryPlayed} {
			r := newRigLook(t, 0, 0, true)
			r.s.Settings.Theme = state.ThemeLight
			m := msg(chat, "receipt", base0, true, "Receipt status")
			m.Status = status
			r.open(chat, m)
			for _, off := range []bool{false, true, false} {
				r.f.Emit(ev(abi.EventTagReceiptsPrivacy, func(e *abi.Event) { e.ReceiptsPrivacy.Disabled = off }))
				r.Frames(30)
				bounds := r.node(ui.RoleButton, "Message info").Bounds
				read := false
				for _, q := range r.Quads() {
					if q.Mode != gpu.ModeShape && inside(q.Rect, bounds) && q.Color[0] == ztheme.OnBubble(true).Read {
						read = true
					}
				}
				want := status == abi.DeliveryPlayed || status == abi.DeliveryRead && !(off && chat == ada)
				if read != want {
					t.Errorf("chat %s status %v receipts off %v: read-coloured ticks %v, want %v", chat, status, off, read, want)
				}
				v := r.a.Model.Conversation([]byte(chat))
				if v.At(0).Status() != status {
					t.Error("display privacy changed the stored receipt")
				}
			}
		}
	}
}

func TestUnavailableCallExplainsOnHover(t *testing.T) {
	r := newRig(t)
	r.Context().SetNativeTooltips(true)
	m := msg(ada, "call-option", base0, false, "")
	m.Content = abi.Content{Tag: abi.ContentTagInteractive}
	m.Content.Interactive.HasCard = true
	m.Content.Interactive.Card = abi.InteractiveCard{Buttons: []abi.InteractiveButton{{
		Label: []byte("Call us"), Action: abi.InteractiveAction{Tag: abi.InteractiveActionTagUnavailable},
	}}}
	r.open(ada, m)
	r.Play(widgettest.Move(ui.RoleButton, "Call us"), widgettest.Idle(2))
	found := false
	for _, reg := range r.Context().TooltipRegions(nil) {
		if string(r.Context().TooltipText(reg.Key, nil)) == "Call us\nOpen this option in WhatsApp Web or on your phone" && reg.Rect == r.node(ui.RoleButton, "Call us").Bounds {
			found = true
		}
	}
	if !found {
		t.Fatal("the unavailable call button has no upstream hover explanation")
	}
	r.Context().SetNativeTooltips(false)
	r.Play(widgettest.Move(ui.RoleButton, "Search messages"), widgettest.Idle(2),
		widgettest.Move(ui.RoleLabel, "Call us"), widgettest.Idle(90))
	if _, ok := r.Find(ui.RoleTooltip, "Call us\nOpen this option in WhatsApp Web or on your phone"); !ok {
		t.Error("no drawn tooltip over the disabled button's nested label")
	}
	bounds := r.node(ui.RoleButton, "Call us").Bounds
	r.Play(widgettest.Move(ui.RoleButton, "Search messages"), widgettest.Idle(2),
		widgettest.MoveTo(bounds[0]+4, (bounds[1]+bounds[3])/2), widgettest.Idle(90))
	r.must(ui.RoleTooltip, "Call us\nOpen this option in WhatsApp Web or on your phone")
	r.Play(widgettest.Click(ui.RoleButton, "Call us"), widgettest.Idle(2))
	if len(r.sent(abi.CommandTagReplyInteractive)) != 0 {
		t.Error("an unavailable option sent a reply")
	}
}

// The bubbles are Rex shapes that follow the appearance, with text that is
// not the bubble's colour.
func TestBubbleColours(t *testing.T) {
	var fills [2][2]uint32 // [dark][own]
	for d, dark := range []bool{false, true} {
		r := newRigLook(t, 0, 0, true)
		r.s.Settings.Theme = state.ThemeSystem
		r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true, Dark: dark})
		r.open(ada, msg(ada, "in", base0, false, "Their text"), msg(ada, "out", base0+60, true, "Our text"))
		r.Frames(30)
		for o, own := range []bool{false, true} {
			name, stamp, body := "Ada Lovelace", base0, "Their text"
			if own {
				name, stamp, body = "You", base0+60, "Our text"
			}
			textBounds := r.node(ui.RoleLabel, body).Bounds
			bounds := r.node(ui.RoleGroup, bubble(name, stamp)).Bounds
			found, text := false, uint32(0)
			for _, q := range r.Quads() {
				if q.Mode != gpu.ModeShape && inside(q.Rect, textBounds) {
					text = q.Color[0]
				}
				if q.Mode != gpu.ModeShape || q.Soft > 0 || q.Rect != bounds {
					continue
				}
				found, fills[d][o] = true, q.Color[0]
			}
			if !found {
				t.Error("no Rex bubble shape at the message's bounds")
			}
			if text == 0 || text == fills[d][o] {
				t.Errorf("dark=%v own=%v text %08x on its bubble %08x", dark, own, text, fills[d][o])
			}
		}
	}
	if fills[0][0] == fills[1][0] {
		t.Error("the incoming bubble does not follow the appearance")
	}
}

func TestDroppedRecordIsVisibleInTranscript(t *testing.T) {
	for _, empty := range []bool{false, true} {
		r := newRigLook(t, 0, 0, true)
		if empty {
			r.open(ada)
		} else {
			r.open(ada, msg(ada, "kept", base0, false, "Still visible"))
		}
		r.mustNot(ui.RoleLabel, "Some records could not be shown")
		drop := func(kind abi.EventTag) {
			r.f.Emit(ev(abi.EventTagEventDropped, func(e *abi.Event) { e.EventDropped.Kind, e.EventDropped.Size = uint16(kind), 1<<20 }))
			r.Frames(3)
		}
		drop(abi.EventTagChats)
		r.mustNot(ui.RoleLabel, "Some records could not be shown")
		drop(abi.EventTagMessages)
		r.must(ui.RoleLabel, "Some records could not be shown")
		if !empty {
			r.must(ui.RoleLabel, "Still visible")
		}
		// it was this chat's or a live one's: another chat opens without it
		r.open(group)
		r.mustNot(ui.RoleLabel, "Some records could not be shown")
		a := r.s.AddAccount("2", nil, fake.New(nil))
		a.Model.Event(ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Last: true, Chats: []abi.Chat{{ID: []byte(ada), Name: []byte("Other account")}}}
		}))
		r.s.SwitchAccount(1)
		a.OpenChat(a.Model.Chats.Find([]byte(ada)))
		r.Frames(3)
		r.mustNot(ui.RoleLabel, "Some records could not be shown")
	}
}
