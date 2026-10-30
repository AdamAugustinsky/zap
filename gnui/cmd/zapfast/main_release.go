//go:build gnui_release

package main

import (
	"flag"
	"fmt"
	"os"

	"gonativeui/app"
	"gonativeui/app/secret"
	"gonativeui/app/selfupdate"

	"zapfast/core"
	"zapfast/internal/buildinfo"
	"zapfast/state"
	"zapfast/sys"
	"zapfast/ui/shell"
)

type options struct {
	args []string
}

func parseOptions(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("zapfast", flag.ContinueOnError)
	fs.Bool("start-hidden", false, "start without a window (the login item)")
	err := fs.Parse(args)
	o.args = fs.Args()
	return o, err
}

func main() {
	// Keep the updater helper ahead of flags, logging and the instance lock.
	selfupdate.Configure(selfupdate.Options{})
	selfupdate.RunHelperIfAsked()
	o, err := parseOptions(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	if code, done := sys.Launch(sys.LaunchOptions{AppID: AppID, Args: o.args}); done {
		os.Exit(code)
	}
	var lg *sys.Log
	var loading settingsReader
	if p, err := state.AppPaths(AppID); err == nil {
		if err := sys.Restrict(p); err != nil {
			fmt.Fprintln(os.Stderr, "zapfast:", err)
			os.Exit(1)
		}
		lg, err = sys.StartLog(p.Data)
		if err != nil {
			fmt.Fprintln(os.Stderr, "zapfast: the log:", err)
		}
		defer lg.Close()
		buildinfo.Log(lg.File(), state.Version) // the revisions this run was built from
		loading = state.LoadSettings(p)
	}
	s := startState(loading, false, showSettingsStartup)
	if s == nil {
		return
	}
	sh, err := shell.New(s, screens(false))
	if err != nil {
		fmt.Fprintln(os.Stderr, "zapfast:", err)
		os.Exit(1)
	}
	sh.Slides = true
	h := shell.NewApp(sh)
	s.Wake = h.Wake
	s.Shell = opener{}
	h.Terminate = terminate(s, lg)
	cores := &coreOpener{wake: h.Wake}
	s.OpenCore = cores.open
	s.ArchiveKeys = secret.Open(core.KeyringService) // a removed account's key goes with its archive
	roster := s.Roster()
	for i, id := range roster.Order {
		pending, conn := s.OpenCore(id)
		s.AddAccount(id, pending, conn)
		if id == roster.Active {
			s.Active = i
		}
	}
	defer closeCores(s)
	defer s.CloseSettings()
	if err := app.Main(sys.Config(AppID, s.Settings.KeepRunning), h); err != nil {
		fmt.Fprintln(os.Stderr, "zapfast:", err)
		closeCores(s)
		os.Exit(1)
	}
}
