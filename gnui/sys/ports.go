package sys

import (
	"os/exec"
	"runtime"

	"gonativeui/app"
	"gonativeui/app/dock"
	"gonativeui/app/login"
	"gonativeui/app/notify"
	"gonativeui/app/tray"
	"gonativeui/menu"
)

// The framework's services behind small interfaces, so the tests can stand
// in for the system (the framework's own fakes are unexported).

// center is the system's notification center (app/notify).
type center interface {
	// Available is whether this process can use it (a bundled app).
	Available() bool
	Post(notify.Note) (notify.PostResult, error)
	Remove(ids ...string) error
	RegisterCategories(...notify.Category) error
	RequestPermission(notify.Permission) (notify.Ticket, error)
	Status() (notify.Ticket, error)
	// Result and Response decode the service's app events.
	Result(*app.Event) (notify.Result, bool)
	Response(*app.Event) (notify.Response, bool)
}

type osCenter struct{}

func (osCenter) Available() bool                               { return notify.Available() }
func (osCenter) Post(n notify.Note) (notify.PostResult, error) { return notify.Post(n) }
func (osCenter) Remove(ids ...string) error                    { return notify.Remove(ids...) }
func (osCenter) RegisterCategories(c ...notify.Category) error {
	return notify.RegisterCategories(c...)
}
func (osCenter) RequestPermission(p notify.Permission) (notify.Ticket, error) {
	return notify.RequestPermission(p)
}
func (osCenter) Status() (notify.Ticket, error)                { return notify.Status() }
func (osCenter) Result(e *app.Event) (notify.Result, bool)     { return notify.ResultOf(e) }
func (osCenter) Response(e *app.Event) (notify.Response, bool) { return notify.ResponseOf(e) }

// dockPort is the Dock tile (app/dock).
type dockPort interface {
	SetBadgeCount(n int) error
	Bounce(critical bool) (dock.Attention, error)
	CancelBounce(id dock.Attention)
}

type osDock struct{}

func (osDock) SetBadgeCount(n int) error                    { return dock.SetBadgeCount(n) }
func (osDock) Bounce(critical bool) (dock.Attention, error) { return dock.Bounce(critical) }
func (osDock) CancelBounce(id dock.Attention)               { dock.CancelBounce(id) }

// statusItem is the menu-bar item (app/tray).
type statusItem interface {
	SetMenu(m *menu.Model, idx int) error
	Remove()
}

func newStatusItem(o tray.Options) (statusItem, error) {
	t, err := tray.New(o)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// loginItem is the login agent (app/login): Status, Enable and Disable.
type loginItem interface {
	Status() (login.State, error)
	Enable() (login.State, error)
	Disable() (login.State, error)
}

// playFile plays a sound file on a goroutine of its own: a notification
// cannot carry a sound outside the bundle, so a custom sound is played here
// (macOS's own player, as Settings' Play button does).
func playFile(path string) {
	if runtime.GOOS != "darwin" || path == "" {
		return
	}
	go func() { _ = exec.Command("/usr/bin/afplay", path).Run() }()
}
