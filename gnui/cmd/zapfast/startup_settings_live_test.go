//go:build darwin

package main

import (
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/shell"
)

func TestMain(m *testing.M) { widgettest.RunMain(m) }

type loadingLiveProbe struct {
	*settingsStartup
	f      *fakeSettingsReader
	frames int
	first  ui.SemTree
}

func (p *loadingLiveProbe) Build(c *ui.Context) {
	p.frames++
	p.settingsStartup.Build(c)
	if p.frames == 3 {
		c.Semantics(&p.first) // the placeholder, still before releasing settings
		close(p.f.ready)
	}
	c.RequestFrame()
}

type lockedLiveProbe struct {
	*shell.App
	first ui.SemTree
}

func (p *lockedLiveProbe) Start(a *app.App) error {
	if err := p.App.Start(a); err != nil {
		return err
	}
	p.Shell.Context().Semantics(&p.first)
	a.Quit()
	return nil
}

// The temporary startup loop must return to the real application loop, not
// terminate the process or leave an unguarded first frame during the handoff.
func TestLiveSettingsStartupTransitionsToLockedWindow(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens windows: set ZAPFAST_LIVE=1")
	}
	f := &fakeSettingsReader{ready: make(chan struct{}), result: startupFixture(t, true)}
	var s *state.State
	var first ui.SemTree
	widgettest.OnMain(func() {
		s = startState(f, true, func(v *settingsStartup) error {
			p := &loadingLiveProbe{settingsStartup: v, f: f}
			err := app.Run(app.Config{Title: "Zap startup test", Width: 480, Height: 260}, p)
			first = p.first
			return err
		})
	})
	if s == nil {
		t.Fatal("startup did not hand over loaded settings")
	}
	loading := false
	for _, n := range first.Nodes {
		loading = loading || string(first.Text(n.Name)) == "Loading…"
		if n.Role == ui.RoleListItem || n.Role == ui.RoleTextField {
			t.Fatal("private controls appeared while settings were pending")
		}
	}
	if !loading {
		t.Fatal("no committed startup placeholder")
	}
	s.Demo = true
	c := fake.New(nil)
	linked(c)
	s.AddAccount("1", nil, c)
	sh, err := shell.New(s, testScreens(true))
	if err != nil {
		t.Fatal(err)
	}
	p := &lockedLiveProbe{App: shell.NewApp(sh)}
	widgettest.OnMain(func() {
		err = app.Main(app.AppConfig{ID: "ai.a3lab.zap.settings-test"}, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	password := false
	for _, n := range p.first.Nodes {
		password = password || n.Role == ui.RoleTextField && string(p.first.Text(n.Name)) == "Password"
		if n.Role == ui.RoleListItem {
			t.Fatal("private chat list appeared during the startup handoff")
		}
	}
	if !password || !s.Lock.Locked {
		t.Fatal("the first application frame was not locked")
	}
}
