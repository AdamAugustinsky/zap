package main

import (
	"fmt"
	"os"
	"time"

	"zapfast/i18n"
	"zapfast/state"
	"zapfast/sys"
)

// AppID is the app's identity (decided 2026-10-18): its own data and
// Keychain items, never upstream ZapFast's.
const AppID = state.AppID

// startState is the state the app starts on: the settings files' (loading;
// nil for the demo, which keeps its settings in memory) and the interface
// language, resolved before anything translates a string as upstream's
// App::new (zf:src/app.rs:1276, i18n::resolve): the saved choice, else the
// system's first supported language (an empty or unknown choice, "system"
// included, follows the system), else English. The scripted runs (the tours
// and the measurements) find their widgets by their English names and are
// compared across machines, so they stay in English, as upstream's what's-new
// tour (zf:src/demo/tour/whats_new.rs:30).
func startState(loading settingsReader, scripted bool, show func(*settingsStartup) error) *state.State {
	s := state.New()
	if loading != nil {
		l := startupSettings(loading, show)
		if l == nil { // closed while loading or recovering: never open accounts
			return nil
		}
		s.UseSettings(l)
	}
	lang := i18n.Resolve(string(s.Settings.InterfaceLanguage))
	if scripted {
		lang = i18n.English
	}
	i18n.SetLocale(lang)
	return s
}

// terminate is the app's Terminate hook: at quit the screens' hooks have run
// and the settings are written; then the cores close and the run's log ends.
// macOS ends the process after this event, so main's deferred calls never
// run: ending the log here is what removes an empty panic.log (FL-005).
func terminate(s *state.State, lg *sys.Log) func() {
	return func() {
		closeCores(s)
		lg.Close()
	}
}

// closeCores closes every account's core (the loop has ended: blocking here
// holds up no frame). It is safe on every way out, the app started or not:
// a core still opening is waited for, and one that failed to open (a nil
// *Core, which must not become a non-nil Conn) is left alone.
func closeCores(s *state.State) {
	for _, a := range s.Accounts {
		if a.Conn == nil && a.Pending != nil && a.Pending.Wait(5*time.Second) {
			if c, err := a.Pending.Result(); err == nil && c != nil {
				a.Conn = c
			}
			a.Pending = nil
		}
		if a.Conn != nil {
			if err := a.Conn.Close(3 * time.Second); err != nil {
				fmt.Fprintln(os.Stderr, "zapfast:", err)
			}
			a.Conn = nil
		}
	}
}
