package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gonativeui/app"
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget"

	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

type settingsReader interface {
	Wait(time.Duration) (*state.Loaded, bool)
	Done() <-chan struct{}
	Retry()
}

// Keep the normal startup path unchanged. A slow or failed read gets only
// this placeholder, before any shell, account, notification or preview exists.
// Upstream also waits for settings before constructing App (src/main.rs:333).
func startupSettings(loading settingsReader, show func(*settingsStartup) error) *state.Loaded {
	l, ok := loading.Wait(2 * time.Second)
	if !ok || l.ReadErr != nil {
		v := &settingsStartup{loading: loading}
		if err := show(v); err != nil {
			fmt.Fprintln(os.Stderr, "zapfast:", err)
			return nil
		}
		l = v.loaded
	}
	if l != nil {
		for _, err := range l.Errs {
			fmt.Fprintln(os.Stderr, "zapfast:", err)
		}
	}
	return l
}

func showSettingsStartup(v *settingsStartup) error {
	return app.Run(app.Config{Title: "Zap", Width: 480, Height: 260}, v)
}

type settingsStartup struct {
	loading settingsReader
	loaded  *state.Loaded
	k       *widget.Kit
	a       *app.App
	waiting <-chan struct{} // the read whose end wakes the window (await)
}

func (v *settingsStartup) Init(a *app.App) error {
	v.a, v.k = a, screen.NewKit(a, nil)
	i18n.SetLocale(i18n.Resolve(""))
	return nil
}

func (v *settingsStartup) Build(c *ui.Context) {
	l, ok := v.loading.Wait(0)
	if ok && l.ReadErr == nil {
		v.loaded = l
		v.a.Quit() // return to startup; apply settings before opening accounts
		return
	}
	k := v.k
	k.Begin(c)
	k.NextPad(24)
	k.NextGap(12)
	k.BeginContainer(ui.AxisY, ui.CrossCenter, "startup", ui.Pct(1), ui.Pct(1), 0)
	rexui.Label(k.Kit, "Zap", rexui.LabelOpts{Style: rexui.TextTitle1})
	if ok {
		message := i18n.T("Could not read settings. Try again.")
		if errors.Is(l.ReadErr, state.ErrRosterRead) {
			message = i18n.T("Could not read the account list. Try again.")
		}
		rexui.Label(k.Kit, message, rexui.LabelOpts{Wrap: true})
		if rexui.Button(k.Kit, i18n.T("Retry"), rexui.ButtonOpts{Variant: rexui.ButtonPrimary}).Clicked {
			v.loading.Retry()
			c.RequestFrame()
		}
	} else {
		rexui.Label(k.Kit, i18n.T("Loading…"), rexui.LabelOpts{})
		v.await()
	}
	k.EndContainer()
	k.End()
}

// await has the window woken when the read under way ends, once a read:
// the placeholder then takes the settings or offers Retry.
func (v *settingsStartup) await() {
	done := v.loading.Done()
	if v.waiting == done {
		return
	}
	v.waiting = done
	if w := v.a.Window(); w != nil {
		go func() {
			<-done
			w.Wake()
		}()
	}
}
