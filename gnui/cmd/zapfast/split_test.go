package main

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
)

// The split view (ui/shell/split.go): two whole chats side by side, the
// one in use the open chat. ⌃⌥V puts the open chat beside its neighbour
// tab; each composer sends to its own chat; a press in the other pane puts
// it in use; the dock's Expand gives the window to one; a tab dragged onto
// the conversation makes room for itself before the drop, and the open
// chat's own tab goes beside the chat the person was on before.
func TestSplitView(t *testing.T) {
	r, f := keyRig(t)
	a := r.s.Accounts[0]
	r.s.Panes.ListHidden = true // room for two chats
	r.open(adaJID)
	r.open(graceJID)
	r.open(adaJID) // tabs: Ada, Grace
	ada, grace := a.Model.Chats.Find([]byte(adaJID)), a.Model.Chats.Find([]byte(graceJID))

	r.Play(widgettest.Key('V', ui.ModCtrl|ui.ModAlt), widgettest.Idle(4))
	if first, second := a.Panes(); a.Open != grace || first != ada || second != grace {
		t.Fatalf("⌃⌥V: open %q, panes %v %v", r.openName(), first, second)
	}
	fields := allNodes(r, ui.RoleTextField, "Message")
	if len(fields) != 2 {
		t.Fatalf("%d composers, want one per pane", len(fields))
	}

	// the pane in use's composer takes ⌘L, and sends to its chat
	r.Play(widgettest.Key('L', prim), widgettest.Type("to grace"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	if c, ok := lastSent(f, abi.CommandTagSendText); !ok || string(c.SendText.Chat) != graceJID {
		t.Fatalf("the second pane's message: %v", f.SentTags())
	}
	// a press in the first pane puts Ada in use; its composer sends to her
	left := fields[0].Bounds
	r.Play(widgettest.MoveTo((left[0]+left[2])/2, left[1]-120), widgettest.Down(ui.RoleTextField, "Message"), widgettest.Up(), widgettest.Idle(3))
	if a.Open != ada || a.Split != grace {
		t.Fatalf("a press in the first pane: open %q, split %v", r.openName(), a.Split)
	}
	if first, _ := a.Panes(); first != ada {
		t.Error("the panes moved when one was put in use")
	}
	r.Play(widgettest.Key('L', prim), widgettest.Type("to ada"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	if c, ok := lastSent(f, abi.CommandTagSendText); !ok || string(c.SendText.Chat) != adaJID || string(c.SendText.Text) != "to ada" {
		t.Fatalf("the first pane's message went to %q", c.SendText.Chat)
	}

	// the dock under the pointer: Expand gives the window to that chat
	r.Play(widgettest.MoveTo((left[0]+left[2])/2, left[1]-200), widgettest.Idle(8))
	r.Click(ui.RoleButton, "Fill the window with this chat")
	r.Frames(3)
	if a.Split != 0 || a.Open != ada {
		t.Fatalf("expand: open %q, split %v", r.openName(), a.Split)
	}

	// a tab dragged onto the conversation: the chat shown makes room, a
	// pane waits for the tab on the pointer's side, and the drop splits
	field := r.Must(ui.RoleTextField, "Message")
	x := field.Bounds[2] - (field.Bounds[2]-field.Bounds[0])/6
	y := field.Bounds[1] - 200
	r.Play(widgettest.Down(ui.RoleTab, "Grace Hopper"), widgettest.MoveTo(x, y), widgettest.Idle(10))
	r.Must(ui.RoleGroup, "Drop to split")
	if narrow := r.Must(ui.RoleTextField, "Message"); narrow.Bounds[2] >= field.Bounds[2]-10 {
		t.Errorf("the chat shown did not make room: %v, was %v", narrow.Bounds, field.Bounds)
	}
	if a.Open != ada {
		t.Errorf("while the tab is held the chat it goes beside shows: %q", r.openName())
	}
	r.Play(widgettest.Up(), widgettest.Idle(4))
	if first, second := a.Panes(); a.Open != grace || first != ada || second != grace {
		t.Fatalf("drop: open %q, panes %v %v", r.openName(), first, second)
	}

	// the open chat's own tab dragged to the left goes beside the chat the
	// person was on before it
	a.Expand(grace)
	r.Frames(3)
	field = r.Must(ui.RoleTextField, "Message")
	x = field.Bounds[0] + (field.Bounds[2]-field.Bounds[0])/6
	r.Play(widgettest.Down(ui.RoleTab, "Grace Hopper"), widgettest.MoveTo(x, y), widgettest.Idle(10), widgettest.Up(), widgettest.Idle(4))
	if first, second := a.Panes(); a.Open != grace || first != grace || second != ada {
		t.Fatalf("the open tab dropped left: open %q, panes %v %v", r.openName(), first, second)
	}

	// the dock's grip drags a pane onto the other: they trade places
	panes := allNodes(r, ui.RoleTextField, "Message")
	l, rt := panes[0].Bounds, panes[1].Bounds
	r.Play(widgettest.MoveTo((l[0]+l[2])/2, l[1]-200), widgettest.Idle(8),
		widgettest.Down(ui.RoleButton, "Drag to move the pane"), widgettest.MoveTo((rt[0]+rt[2])/2, rt[1]-200), widgettest.Idle(2))
	r.Must(ui.RoleGroup, "Drop to move here")
	r.Play(widgettest.Up(), widgettest.Idle(3))
	if first, second := a.Panes(); first != ada || second != grace || a.Open != grace {
		t.Fatalf("moved: open %q, panes %v %v", r.openName(), first, second)
	}
}

// allNodes are the frame's nodes with role and name, in build order.
func allNodes(r *rig, role ui.Role, name string) []ui.SemNode {
	tree := r.Tree()
	var out []ui.SemNode
	for _, n := range tree.Nodes {
		if n.Role == role && string(tree.Text(n.Name)) == name {
			out = append(out, n)
		}
	}
	return out
}
