package dialogs

import (
	"gonativeui/icon"
	"strconv"
	"strings"
	"unicode/utf8"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/state"
)

// Label limits (zf:src/archive/labels.rs).
const (
	LabelLimit   = 20
	LabelNameMax = 32
	DefaultColor = "#3b82f6"
)

// PresetColors are offered when a label is made or edited.
var PresetColors = [10]string{
	"#3b82f6", "#22c55e", "#eab308", "#f97316", "#ef4444", "#ec4899", "#a855f7", "#14b8a6", "#64748b", "#0ea5e9",
}

func colorName(hex string) string {
	names := [...]string{"Blue", "Green", "Yellow", "Orange", "Red", "Pink", "Purple", "Teal", "Grey", "Sky blue"}
	for i, p := range PresetColors {
		if strings.EqualFold(p, hex) {
			return i18n.P("label colour", names[i])
		}
	}
	return i18n.P("label colour", "Colour")
}

// labelRec is one of the account's labels as the core listed them.
type labelRec struct{ id, name, color string }

// labelSet is an account's labels, assembled from EventLabels batches.
type labelSet struct {
	items    []labelRec
	batch    uint32
	assembly bool
}

type labels struct {
	sets                map[uint64]*labelSet // by state.Account.Serial
	name                ui.TextBuffer
	color               int
	editing             string // the id of the label being renamed
	rename              ui.TextBuffer
	editHex             string
	iconEdit, iconTrash icon.ID
}

func (l *labels) reset() {
	l.name.Reset()
	l.color = 0
	l.editing = ""
	l.rename.Reset()
}

// of is account a's labels, by its Serial: an index moves when an earlier
// account goes, a Serial never names another account.
func (l *labels) of(a *state.Account) *labelSet {
	if l.sets == nil {
		l.sets = map[uint64]*labelSet{}
	}
	if l.sets[a.Serial] == nil {
		l.sets[a.Serial] = &labelSet{}
	}
	return l.sets[a.Serial]
}

// gone drops the labels of an account that is gone.
func (l *labels) gone(_ *state.State, serial uint64) { delete(l.sets, serial) }

// event applies an EventLabels batch: a new batch starts the list afresh.
func (l *labels) event(a *state.Account, e *abi.EventLabels) {
	ls := l.of(a)
	if !ls.assembly || ls.batch != e.Batch {
		ls.items, ls.batch, ls.assembly = ls.items[:0], e.Batch, true
	}
	for i := range e.Labels {
		x := &e.Labels[i]
		ls.items = append(ls.items, labelRec{id: string(x.ID), name: string(x.Name), color: string(x.ColorHex)})
	}
	if e.Last {
		ls.assembly = false
	}
}

// LabelNames lists the names of the account's labels (a convenience for
// screens that offer them).
func (s *Screen) labelsOf(a *state.Account) []labelRec { return s.lab.of(a).items }

func (s *Screen) labelManager(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c, l := e.K, e.C(), &s.lab
	items := l.of(a).items
	para(k, i18n.T("Labels stay on this computer. They do not reach your phone or anyone you chat with."), rexui.TextCallout, secondary)

	if len(items) >= LabelLimit {
		para(k, i18n.Format(i18n.T("You have {limit} labels, the most Zap keeps."), "{limit}", strconv.Itoa(LabelLimit)), rexui.TextCallout, rexui.Of(theme.Orange))
	} else {
		k.NextGap(8)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "new", ui.Pct(1), ui.Children(1), 0)
		k.NextWidth(widget.Fill)
		f := rexui.TextField(k.Kit, i18n.T("New label"), &l.name, i18n.T("New label"), rexui.FieldOpts{})
		create := f.Submitted
		k.EndContainer()
		k.NextGap(4)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "swatches", ui.Pct(1), ui.Children(1), 0)
		for i, hex := range PresetColors {
			if s.swatch(e, hex, i == l.color, "swatch"+strconv.Itoa(i)) {
				l.color = i
			}
		}
		k.Spacer()
		k.BeginDisabled(strings.TrimSpace(l.name.String()) == "")
		if rexui.Button(k.Kit, i18n.T("Create"), rexui.ButtonOpts{}).Clicked {
			create = true
		}
		k.EndDisabled()
		k.EndContainer()
		if create {
			s.createLabel(e, a)
		}
	}
	rexui.Separator(k.Kit, rexui.SeparatorOpts{})
	if len(items) == 0 {
		para(k, i18n.T("No labels yet. Create one above, then add chats to it from their right-click menu."), rexui.TextBody, secondary)
	} else {
		_, h := winPt(e)
		k.NextHeight(k.Fixed(min(max(h-380, 90), float32(len(items))*36+4)))
		v := k.BeginScroll("label-list", &s.scroll, widget.ScrollVertical)
		var save *labelRec
		var drop string
		for i := range items {
			it := &items[i]
			count := 0
			for _, ref := range a.Model.Chats.Order() {
				if a.Model.Chats.At(ref).HasLabel([]byte(it.id)) {
					count++
				}
			}
			k.NextGap(8)
			k.BeginContainer(ui.AxisX, ui.CrossCenter, c.Fmt().Str("label").Int(i).String(), ui.Pct(1), ui.Px(k.Pt(34)), 0)
			if l.editing == it.id {
				k.NextWidth(widget.Fill)
				f := rexui.TextField(k.Kit, i18n.T("Rename or recolour")+"###rename", &l.rename, it.name, rexui.FieldOpts{})
				if s.swatchNamed(e, l.editHex, i18n.T("Next colour"), "next") {
					// A new colour keeps the row open, with what is typed so far.
					l.editHex = nextColor(l.editHex)
					cp := *it
					cp.name, cp.color = clip(nameOr(l.rename.String(), it.name)), l.editHex
					save = &cp
				} else if f.Submitted {
					cp := *it
					cp.name, cp.color = clip(nameOr(l.rename.String(), it.name)), l.editHex
					save = &cp
					l.editing = ""
				} else if f.Cancelled {
					l.editing = ""
				}
			} else {
				s.swatch(e, it.color, false, "dot"+strconv.Itoa(i))
				k.NextWidth(widget.Fill)
				rexui.Label(k.Kit, it.name, rexui.LabelOpts{Style: rexui.TextSemibold})
				rexui.Label(k.Kit, i18n.Format(i18n.N("{} chat", "{} chats", count), "{}", strconv.Itoa(count)), rexui.LabelOpts{Style: rexui.TextCallout, Color: secondary})
				if rexui.IconButton(k.Kit, l.iconEdit, c.Fmt().Str(i18n.T("Rename or recolour")).Str("###edit").Int(i).String(), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
					l.editing, l.editHex = it.id, it.color
					l.rename.SetString(it.name)
				}
				if rexui.IconButton(k.Kit, l.iconTrash, c.Fmt().Str(i18n.T("Delete this label. Its chats keep their messages.")).Str("###delete").Int(i).String(), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
					drop = it.id
				}
			}
			k.EndContainer()
		}
		k.EndScroll(v)
		if save != nil {
			cmd := a.Cmd(abi.CommandTagUpdateLabel)
			cmd.UpdateLabel.ID, cmd.UpdateLabel.Name, cmd.UpdateLabel.ColorHex = []byte(save.id), []byte(save.name), []byte(save.color)
			a.Send(cmd)
		}
		if drop != "" {
			cmd := a.Cmd(abi.CommandTagDeleteLabel)
			cmd.DeleteLabel.ID = []byte(drop)
			a.Send(cmd)
		}
	}
	beginActions(e)
	dl.Action(i18n.T("Close"), rexui.ButtonPrimary)
	endActions(e)
}

// clip keeps a label's name within the limit.
func clip(name string) string {
	if utf8.RuneCountInString(name) > LabelNameMax {
		return string([]rune(name)[:LabelNameMax])
	}
	return name
}

func nameOr(typed, old string) string {
	if t := strings.TrimSpace(typed); t != "" {
		return t
	}
	return old
}

// nextColor is the preset after this one, so a click always changes
// something.
func nextColor(hex string) string {
	for i, p := range PresetColors {
		if strings.EqualFold(p, hex) {
			return PresetColors[(i+1)%len(PresetColors)]
		}
	}
	return PresetColors[0]
}

// createLabel sends the label the new-label row describes.
func (s *Screen) createLabel(e *screenEnv, a *state.Account) {
	l := &s.lab
	name := strings.TrimSpace(l.name.String())
	if name == "" {
		return
	}
	if utf8.RuneCountInString(name) > LabelNameMax {
		r := []rune(name)
		name = string(r[:LabelNameMax])
	}
	if len(l.of(a).items) >= LabelLimit {
		e.S.Toast([]byte(i18n.Format(i18n.T("You have {limit} labels, the most Zap keeps."), "{limit}", strconv.Itoa(LabelLimit))), true)
		return
	}
	cmd := a.Cmd(abi.CommandTagCreateLabel)
	cmd.CreateLabel.Name, cmd.CreateLabel.ColorHex = []byte(name), []byte(PresetColors[l.color])
	a.Send(cmd)
	l.name.Reset()
	l.color = 0
}

// parseColor reads "#rrggbb" into 0xRRGGBBAA, the accent when it is not one.
func parseColor(k *widget.Kit, hex string) uint32 {
	if len(hex) == 7 && hex[0] == '#' {
		if v, err := strconv.ParseUint(hex[1:], 16, 32); err == nil {
			return uint32(v)<<8 | 0xFF
		}
	}
	return k.Theme().Colors[theme.Accent]
}

// swatch is a small coloured circle (macOS's colour wells), a button when it
// is a choice.
func (s *Screen) swatch(e *screenEnv, hex string, selected bool, id string) bool {
	return s.swatchOf(e, hex, colorName(hex), selected, id)
}

// swatchNamed is a swatch that cycles to the next colour, named for that.
func (s *Screen) swatchNamed(e *screenEnv, hex, name, id string) bool {
	return s.swatchOf(e, hex, name, false, id)
}

func (s *Screen) swatchOf(e *screenEnv, hex, label string, selected bool, id string) bool {
	k, c := e.K, e.C()
	name := c.Fmt().Str(label).Str("###").Str(id).String()
	p := k.PressState(name, base.HoverOn)
	side := k.Pt(18)
	c.NextRadius(side / 2)
	c.NextBG(parseColor(k, hex))
	flags := ui.DrawBackground
	if selected {
		c.NextBorder(k.Theme().Colors[theme.Label], k.Pt(2))
		flags |= ui.DrawBorder
	}
	k.BeginPress(&p, ui.RoleButton, ui.Px(side), ui.Px(side), flags, nil)
	return k.EndPress(&p).Clicked
}
