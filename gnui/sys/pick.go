package sys

import (
	"strings"

	"gonativeui/app"
	"gonativeui/platform"

	"zapfast/abi"
	"zapfast/state"
)

// The core's file panels (zf:src/pick.rs): the core asks the interface to
// choose files, a folder or a place to save, and every request is answered,
// with no paths when the person cancels or no panel can be shown (state
// would answer the same when nobody listened). The panels are the
// system's sheets on the main window (NSOpenPanel, NSSavePanel).

// panelPort is the app's sheet panels.
type panelPort interface {
	OpenPanel(platform.OpenSpec) (platform.DialogTicket, error)
	SavePanel(platform.SaveSpec) (platform.DialogTicket, error)
	TakeDialog() (platform.DialogResult, bool)
}

// picking is a panel on screen and the request it answers.
type picking struct {
	ticket platform.DialogTicket
	acct   *state.Account
	id     uint64
}

// panels is the sheet panels of the app, nil before it exists.
func (y *System) panels() panelPort {
	if y.panel != nil {
		return y.panel
	}
	if y.app != nil {
		return y.app
	}
	return nil
}

// pick shows the panel the core asked for.
func (y *System) pick(a *state.Account, r *abi.EventPickRequest) {
	p := y.panels()
	if p == nil {
		y.answerPick(a, r.ID, nil)
		return
	}
	types := pickTypes(r.Filters)
	title := string(r.Title)
	var start string
	if r.HasDirectory {
		start = string(r.Directory)
	}
	var t platform.DialogTicket
	var err error
	switch r.Kind {
	case abi.PickKindSave:
		spec := platform.SaveSpec{Types: types, Start: start, Title: title}
		if r.HasFileName {
			spec.Name = string(r.FileName)
		}
		t, err = p.SavePanel(spec)
	case abi.PickKindFolder:
		t, err = p.OpenPanel(platform.OpenSpec{Dirs: true, Start: start, Title: title})
	default:
		t, err = p.OpenPanel(platform.OpenSpec{Files: true, Multiple: r.Kind == abi.PickKindFiles, Types: types, Start: start, Title: title})
	}
	if err != nil {
		logWarn("no file panel", "err", err)
		y.answerPick(a, r.ID, nil)
		return
	}
	y.picks = append(y.picks, picking{ticket: t, acct: a, id: r.ID})
}

// pickTypes are the filters' extensions as the panels take them: one list,
// each with its leading dot.
func pickTypes(fs []abi.PickFilter) []string {
	var out []string
	for i := range fs {
		for _, x := range fs[i].Extensions {
			e := "." + strings.TrimLeft(string(x), "*.")
			if e == "." {
				continue
			}
			dup := false
			for _, o := range out {
				if o == e {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, e)
			}
		}
	}
	return out
}

// pickEnded takes the result of a panel of ours. It reports whether e was
// one's end.
func (y *System) pickEnded(e *app.Event) bool {
	if e.Source != platform.SourceDialog || len(y.picks) == 0 || !y.isPick(platform.DialogTicket(e.ID)) {
		return false
	}
	p := y.panels()
	for p != nil {
		r, ok := p.TakeDialog()
		if !ok {
			break
		}
		for i, k := range y.picks {
			if k.ticket != r.Ticket {
				continue
			}
			y.picks = append(y.picks[:i], y.picks[i+1:]...)
			var paths []string
			if r.Outcome == platform.DialogAnswered {
				paths = r.Paths
			}
			y.answerPick(k.acct, k.id, paths)
			break
		}
	}
	return true
}

func (y *System) isPick(t platform.DialogTicket) bool {
	for _, k := range y.picks {
		if k.ticket == t {
			return true
		}
	}
	return false
}

// answerPick tells the core what was chosen: no paths for a cancelled panel.
func (y *System) answerPick(a *state.Account, id uint64, paths []string) {
	cmd := a.Cmd(abi.CommandTagPickAnswer)
	cmd.PickAnswer.ID = id
	for _, p := range paths {
		cmd.PickAnswer.Paths = append(cmd.PickAnswer.Paths, []byte(p))
	}
	a.Send(cmd)
}
