//go:build darwin

package shell_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/chatlist"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

const startupPoll = 0x7a66_5354

type startupView struct {
	rows             int
	skeleton, status bool
}

type startupProbe struct {
	*shell.App
	f                       *fake.Core
	first, last             startupView
	delayed, startedAtFirst bool
	deadline                time.Time
	stop, stopped           chan struct{}
}

func (p *startupProbe) view() (v startupView) {
	var tree ui.SemTree
	p.Shell.Context().Semantics(&tree)
	for _, n := range tree.Nodes {
		name := string(tree.Text(n.Name))
		if n.Role == ui.RoleListItem && name == "Ada Lovelace" {
			v.rows++
		}
		v.skeleton = v.skeleton || n.Role == ui.RoleGroup && name == "Loading your chats"
		v.status = v.status || strings.Contains(name, "Connecting to WhatsApp") || strings.Contains(name, "Loading chat history")
	}
	return v
}

func cachedStartupChats() *abi.Event {
	return &abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true,
		Chats: []abi.Chat{{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada Lovelace"), LastActivity: 1}}}}
}

func (p *startupProbe) Start(a *app.App) error {
	if err := p.App.Start(a); err != nil {
		return err
	}
	// OpenWindow has committed its initial two builds. No settling frames,
	// pre-pump or measurement warm-up may replace this first-frame tree.
	p.first = p.view()
	p.startedAtFirst = p.S.Acct().Started
	p.deadline = time.Now().Add(3 * time.Second)
	go func() {
		defer close(p.stopped)
		if p.delayed {
			time.Sleep(200 * time.Millisecond)
			p.f.Emit(cachedStartupChats(), &abi.Event{Tag: abi.EventTagLink,
				Link: abi.EventLink{Status: abi.LinkStatus{Tag: abi.LinkStatusTagConnected}}})
		}
		for time.Now().Before(p.deadline) {
			select {
			case <-p.stop:
				return
			default:
			}
			app.Post(app.Event{ID: startupPoll}, nil)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return nil
}

func (p *startupProbe) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost && e.ID == startupPoll {
		p.last = p.view()
		if p.last.rows == 1 && p.S.Acct().Started || time.Now().After(p.deadline.Add(-50*time.Millisecond)) {
			a.Quit()
			return
		}
	}
	p.App.Event(a, e)
}

// LG-014: verifier ec66cd7's first-frame probe, with the known-failing
// connected+delayed subcase now always enabled for normal live checks.
// This proves UI routing, not disk/Keychain timing or a real schema migration.
func TestLiveStartupArchiveFirstFrame(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	for _, tc := range []struct {
		name    string
		delayed bool
		link    abi.LinkStatusTag
	}{
		{"cached", false, abi.LinkStatusTagConnected},
		{"slow archive while connecting", true, abi.LinkStatusTagConnecting},
		{"slow archive already connected", true, abi.LinkStatusTagConnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := state.New()
			s.Demo = true
			f := fake.New(nil)
			f.Emit(&abi.Event{Tag: abi.EventTagLink, Link: abi.EventLink{Status: abi.LinkStatus{Tag: tc.link}}})
			if !tc.delayed {
				f.Emit(cachedStartupChats())
			}
			sh, err := shell.New(s, &screen.Set{ChatList: chatlist.New()})
			if err != nil {
				t.Fatal(err)
			}
			s.AddAccount("1", nil, f)
			p := &startupProbe{App: shell.NewApp(sh), f: f, delayed: tc.delayed, stop: make(chan struct{}), stopped: make(chan struct{})}
			widgettest.OnMain(func() { err = app.Main(app.AppConfig{ID: "ai.a3lab.zap.startup-parity"}, p) })
			close(p.stop)
			if err == nil {
				<-p.stopped
			}
			if err != nil {
				t.Fatal(err)
			}
			// The status banner waits out its grace (bannerGrace): the
			// skeleton alone says the archive is coming.
			if p.first.skeleton != tc.delayed || p.first.status || p.first.rows != map[bool]int{false: 1, true: 0}[tc.delayed] {
				t.Errorf("first frame: %+v; want delayed=%v (skeleton, no status, no rows), cached otherwise", p.first, tc.delayed)
			}
			if p.startedAtFirst {
				t.Error("core startup permit preceded the first frame")
			}
			if p.last.rows != 1 || p.last.skeleton || p.last.status {
				t.Errorf("after archive: %+v", p.last)
			}
		})
	}
}
