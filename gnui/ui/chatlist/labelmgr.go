package chatlist

import (
	"strings"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The label manager (zf:src/ui/labels.rs:217): create, rename, recolour and
// delete labels. The labels live in the archive; nothing here reaches the
// phone.

const (
	labelLimit   = 20
	labelNameMax = 32
	defaultColor = "#3b82f6"
)

// presetColors are the ten colours offered (zf:src/ui/labels.rs:20).
var presetColors = [...]string{"#3b82f6", "#22c55e", "#eab308", "#f97316", "#ef4444", "#ec4899", "#a855f7", "#14b8a6", "#64748b", "#0ea5e9"}

func colorName(hex string) string {
	switch hex {
	case "#3b82f6":
		return i18n.P("label colour", "Blue")
	case "#22c55e":
		return i18n.P("label colour", "Green")
	case "#eab308":
		return i18n.P("label colour", "Yellow")
	case "#f97316":
		return i18n.P("label colour", "Orange")
	case "#ef4444":
		return i18n.P("label colour", "Red")
	case "#ec4899":
		return i18n.P("label colour", "Pink")
	case "#a855f7":
		return i18n.P("label colour", "Purple")
	case "#14b8a6":
		return i18n.P("label colour", "Teal")
	case "#64748b":
		return i18n.P("label colour", "Grey")
	case "#0ea5e9":
		return i18n.P("label colour", "Sky blue")
	}
	return i18n.P("label colour", "Colour")
}

// nextColor is the preset after hex, so a click always changes something.
func nextColor(hex string) string {
	for i, p := range presetColors {
		if strings.EqualFold(p, hex) {
			return presetColors[(i+1)%len(presetColors)]
		}
	}
	return presetColors[0]
}

// labelManager is the manager's state.
type labelManager struct {
	name    ui.TextBuffer
	color   string
	editing string // the id of the label being renamed
	edit    ui.TextBuffer
	focus   bool
	fresh   bool
}

func (lm *labelManager) build(s *Screen, e *screen.Env, a *state.Account) {
	open := e.S.Dialog.Kind == state.DialogLabels
	if !open {
		lm.fresh = false
		return
	}
	k := e.K
	c := k.Context()
	if !lm.fresh {
		lm.fresh, lm.editing, lm.color = true, "", defaultColor
		lm.name.Reset()
	}
	mir := s.mirror(a)
	d := rexui.BeginDialog(k.Kit, "labels", &open, rexui.DialogOpts{Title: i18n.T("Labels"), Width: 460})
	if d.Visible {
		rexui.Label(k.Kit, i18n.T("Labels stay on this computer. They do not reach your phone or anyone you chat with."),
			rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Label2), Wrap: true})
		if len(mir.labels) >= labelLimit {
			rexui.Label(k.Kit, i18n.Format(i18n.T("You have {limit} labels, the most Zap keeps."), "{limit}", itoa(labelLimit)),
				rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Orange), Wrap: true})
		} else {
			lm.createRow(s, e, a)
		}
		rexui.Separator(k.Kit, rexui.SeparatorOpts{})
		if len(mir.labels) == 0 {
			rexui.Label(k.Kit, i18n.T("No labels yet. Create one above, then add chats to it from their right-click menu."),
				rexui.LabelOpts{Color: rexui.Of(theme.Label2), Wrap: true})
		}
		for i := range mir.labels {
			lm.labelRow(s, e, a, &mir.labels[i])
		}
		d.BeginActions()
		d.Action(i18n.T("Done"), rexui.ButtonPrimary)
		d.EndActions()
	}
	rexui.EndDialog(k.Kit, &d)
	if !open && e.S.Dialog.Kind == state.DialogLabels {
		e.S.CloseDialog()
	}
	_ = c
}

// createRow is the name field, the colours and Create.
func (lm *labelManager) createRow(s *Screen, e *screen.Env, a *state.Account) {
	k := e.K
	c := k.Context()
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(theme.Snap(8 * k.Scale()))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.BoxKeyed(0, ui.HashString(c.ParentKey(), "create"), nil))
	k.NextWidth(ui.Px(theme.Snap(150 * k.Scale())))
	f := rexui.TextField(k.Kit, i18n.T("New label")+"###new-label", &lm.name, i18n.T("New label"), rexui.FieldOpts{})
	if lm.name.Len() > labelNameMax {
		lm.name.SetBytes(lm.name.Bytes()[:labelNameMax])
	}
	lm.swatches(k.Kit, &lm.color, nil)
	name := strings.TrimSpace(lm.name.String())
	create := false
	if name != "" {
		create = rexui.Button(k.Kit, i18n.T("Create"), rexui.ButtonOpts{}).Clicked
	}
	create = create || (f.Submitted && name != "")
	c.Pop()
	if create {
		lm.create(s, e, a, name)
	}
}

// swatches builds the ten colours as radio buttons, round as macOS's colour
// wells; it sets *sel to the one clicked.
func (lm *labelManager) swatches(k *base.Kit, sel *string, changed *bool) {
	c := k.Context()
	sc := k.Scale()
	c.NextAxis(ui.AxisX)
	c.NextGap(theme.Snap(3 * sc))
	c.NextRole(ui.RoleRadioGroup)
	c.NextSemName(i18n.P("label colour", "Colour"))
	c.NextSize(ui.Children(1), ui.Children(1))
	c.Push(c.BoxKeyed(0, ui.HashString(c.ParentKey(), "swatches"), nil))
	side := theme.Snap(18 * sc)
	for _, hex := range presetColors {
		on := strings.EqualFold(*sel, hex)
		p := k.PressState(colorName(hex), base.HoverOn)
		var st ui.SemState
		if on {
			st = ui.SemChecked
		}
		c.NextSemState(st)
		c.NextBG(colorOf(k.Theme(), hex))
		c.NextRadius(side / 2)
		if on {
			c.NextBorder(k.Theme().Colors[theme.Label], theme.Snap(1.5*sc))
		}
		fl := ui.DrawBackground
		if on {
			fl |= ui.DrawBorder
		}
		k.BeginPress(&p, ui.RoleRadio, ui.Px(side), ui.Px(side), fl, nil)
		k.FocusRing(p.ID, p.Visual, side/2)
		if k.EndPress(&p).Clicked {
			*sel = hex
			if changed != nil {
				*changed = true
			}
		}
	}
	c.Pop()
}

// create sends CreateLabel unless the limit or a taken name refuses it.
func (lm *labelManager) create(s *Screen, e *screen.Env, a *state.Account, name string) {
	mir := s.mirror(a)
	if len(mir.labels) >= labelLimit {
		e.S.Toast([]byte(i18n.Format(i18n.T("You have {limit} labels, the most Zap keeps."), "{limit}", itoa(labelLimit))), true)
		return
	}
	if msg := lm.refusal(mir, name, ""); msg != "" {
		e.S.Toast([]byte(msg), true)
		return
	}
	cmd := a.Cmd(abi.CommandTagCreateLabel)
	cmd.CreateLabel.Name, cmd.CreateLabel.ColorHex = []byte(name), []byte(lm.color)
	a.Send(cmd)
	lm.name.Reset()
	lm.color = defaultColor
}

// refusal says why a name cannot be used: another label has it already.
func (lm *labelManager) refusal(mir *mirror, name, except string) string {
	for i := range mir.labels {
		if mir.labels[i].id != except && strings.EqualFold(mir.labels[i].name, name) {
			return i18n.Format(i18n.T("A label named “{name}” already exists."), "{name}", name)
		}
	}
	return ""
}

// labelRow is one label: its colour, name, how many chats wear it, and the
// buttons that edit and delete it; or the fields that rename it.
func (lm *labelManager) labelRow(s *Screen, e *screen.Env, a *state.Account, lb *label) {
	k := e.K
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	mir := s.mirror(a)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(theme.Snap(8 * sc))
	c.NextSize(ui.Pct(1), ui.Px(theme.Snap(34*sc)))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(lb.name)
	c.Push(c.BoxKeyed(0, ui.HashString(c.ParentKey(), "row-"+lb.id), nil))
	defer c.Pop()
	if lm.editing == lb.id {
		k.NextWidth(ui.Px(theme.Snap(180 * sc)))
		f := rexui.TextField(k.Kit, i18n.T("Rename")+"###rename", &lm.edit, lb.name, rexui.FieldOpts{})
		if lm.focus {
			lm.focus = false
			c.SetFocus(f.Key)
		}
		name := strings.TrimSpace(lm.edit.String())
		if name == "" {
			name = lb.name
		}
		sel := lb.color
		var changed bool
		lm.swatches(k.Kit, &sel, &changed)
		switch {
		case changed:
			lm.update(e, a, lb, name, sel)
			lm.editing = ""
		case f.Submitted:
			if msg := lm.refusal(mir, name, lb.id); msg != "" {
				e.S.Toast([]byte(msg), true)
			} else {
				lm.update(e, a, lb, name, lb.color)
				lm.editing = ""
			}
		case f.Cancelled:
			lm.editing = ""
		}
		return
	}
	d := theme.Snap(14 * sc)
	c.NextSize(ui.Px(d), ui.Px(d))
	c.NextBG(colorOf(r, lb.color))
	c.NextRadius(theme.Snap(4 * sc))
	c.BoxKeyed(ui.DrawBackground, ui.HashString(c.ParentKey(), "dot"), nil)
	rexui.Label(k.Kit, lb.name, rexui.LabelOpts{})
	k.Spacer()
	n := 0
	m := a.Model
	for _, ref := range m.Chats.Order() {
		if m.Chats.At(ref).HasLabel([]byte(lb.id)) {
			n++
		}
	}
	rexui.Label(k.Kit, i18n.Format(i18n.N("{} chat", "{} chats", n), "{}", itoa(n)), rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Label2)})
	if rexui.IconButton(k.Kit, s.ic.pencil, i18n.T("Rename or recolour"), rexui.IconButtonOpts{Variant: rexui.IconBorderless, Size: theme.SizeSmall}).Clicked {
		lm.editing, lm.focus = lb.id, true
		lm.edit.SetString(lb.name)
		lm.edit.SelectAll()
	}
	if rexui.IconButton(k.Kit, s.ic.trash, i18n.T("Delete this label. Its chats keep their messages."), rexui.IconButtonOpts{Variant: rexui.IconBorderless, Size: theme.SizeSmall}).Clicked {
		cmd := a.Cmd(abi.CommandTagDeleteLabel)
		cmd.DeleteLabel.ID = []byte(lb.id)
		a.Send(cmd)
		lm.editing = ""
	}
}

// update sends UpdateLabel.
func (lm *labelManager) update(e *screen.Env, a *state.Account, lb *label, name, color string) {
	cmd := a.Cmd(abi.CommandTagUpdateLabel)
	cmd.UpdateLabel.ID, cmd.UpdateLabel.Name, cmd.UpdateLabel.ColorHex = []byte(lb.id), []byte(name), []byte(color)
	a.Send(cmd)
}

var _ icon.ID
