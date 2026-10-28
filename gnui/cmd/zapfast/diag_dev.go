//go:build !gnui_release

// Command zapfast is ZapFast's Go UI: app.Main over the shell (ui/shell)
// and the screens (screens.go), one account's core opened off the main
// thread before the window exists.
//
//	zapfast -demo            the demo core: a synthetic account, no network, no disk
//	zapfast -demo -o out.png render the main window offscreen once the chat list is in, and quit
//
// Without -demo it opens the real account in the app's own data folder
// (ai.a3lab.zap). Tests and screenshots use -demo only.
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"
	"time"

	"gonativeui/app"
	"gonativeui/app/secret"
	"gonativeui/app/selfupdate"
	"gonativeui/ui"

	"zapfast/core"
	"zapfast/state"
	"zapfast/sys"
	"zapfast/ui/shell"
)

// options are the command line's flags.
type options struct {
	demo bool
	out  string
	dark bool
	// page is upstream's --demo-page list (demopage.go).
	page string
	tour tourOptions // -demo-tour (tour.go)
	args []string    // the positional arguments: a number or link to open, or quit, reload-themes

	budget budgetOptions // the measurement modes (budgets.go)
}

// parseOptions parses args (without the program name) with package flag's
// syntax: -o PATH, -o=PATH, --o PATH and --o=PATH alike.
func parseOptions(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("zapfast", flag.ContinueOnError)
	fs.BoolVar(&o.demo, "demo", false, "open the demo core: a synthetic account, no network, no disk")
	fs.StringVar(&o.out, "o", "", "render the main window offscreen into this PNG once the chat list is in, and quit")
	fs.BoolVar(&o.dark, "dark", false, "with -o: render in dark mode")
	fs.StringVar(&o.page, "demo-page", "", "with -demo: open in one of upstream's demo states (NAME[,NAME…], DEMO.md)")
	shot := fs.String("demo-shot", "", "the demo's -o: render the demo page offscreen into this PNG, and quit")
	o.tour.register(fs)
	fs.Bool("start-hidden", false, "start without a window (the login item)")
	o.budget.register(fs)
	err := fs.Parse(args)
	o.args = fs.Args()
	if *shot != "" {
		o.demo, o.out = true, *shot
	}
	if o.page != "" || o.tour.on || o.tour.frames != "" {
		o.demo = true
		o.tour.on = o.tour.on || o.tour.frames != ""
	}
	return o, err
}

func main() {
	// The updater's helper is this executable run with a private argument: it
	// must run before anything else (the flags, the log, the single-instance
	// lock) and never returns.
	selfupdate.Configure(selfupdate.Options{})
	selfupdate.RunHelperIfAsked()
	o, err := parseOptions(os.Args[1:])
	if err != nil {
		os.Exit(2) // flag has printed why
	}
	if o.out == "" {
		// One linked copy: a second launch hands its arguments to the first and
		// exits. Notifications opt in here, before app.Main (package sys).
		if code, done := sys.Launch(sys.LaunchOptions{AppID: AppID, Args: o.args, Demo: o.demo}); done {
			os.Exit(code)
		}
	}
	// The settings files are read on a goroutine while the process starts;
	// the demo keeps its settings in memory.
	var lg *sys.Log
	var loading settingsReader
	if !o.demo {
		if p, err := state.AppPaths(AppID); err == nil {
			// The folders are the user's alone, or ZapFast does not start;
			// the log of this run replaces the last one's.
			if err := sys.Restrict(p); err != nil {
				fmt.Fprintln(os.Stderr, "zapfast:", err)
				os.Exit(1)
			}
			lg, err = sys.StartLog(p.Data)
			if err != nil {
				fmt.Fprintln(os.Stderr, "zapfast: the log:", err)
			}
			defer lg.Close()
			loading = state.LoadSettings(p)
		}
	}
	s := startState(loading, o.tour.on || o.budget.on(), showSettingsStartup)
	if s == nil {
		return
	}
	s.Demo = o.demo
	set := screens(o.demo)
	page := newDemoPage(o.page)
	if page != nil {
		set.Tickers = append(set.Tickers, page)
		s.Settings.WindowW, s.Settings.WindowH = page.size()
		if page.light() {
			s.Settings.Theme = state.ThemeLight
		}
	}
	if w, h, err := o.tour.windowSize(); err != nil {
		fmt.Fprintln(os.Stderr, "zapfast:", err)
		os.Exit(2)
	} else if w > 0 {
		s.Settings.WindowW, s.Settings.WindowH = w, h
	}
	hx, hy, hover, err := o.tour.hoverAt()
	if err != nil {
		fmt.Fprintln(os.Stderr, "zapfast:", err)
		os.Exit(2)
	}
	sh, err := shell.New(s, set)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zapfast:", err)
		os.Exit(1)
	}
	sh.Slides = true
	h := shell.NewApp(sh)
	s.Wake = h.Wake
	s.Shell = opener{}
	// At quit the screens' hooks have run and the settings are written:
	// then the cores close and the log ends (terminate).
	h.Terminate = terminate(s, lg)
	// Each account's core loads and opens on its own goroutine, so the
	// window never waits for SQLCipher, the Keychain or a migration.
	cores := &coreOpener{demo: o.demo, wake: h.Wake}
	s.OpenCore = cores.open
	if !o.demo {
		s.ArchiveKeys = secret.Open(core.KeyringService) // a removed account's key goes with its archive
	}
	roster := s.Roster()
	if o.demo {
		roster = state.DefaultRoster()
	}
	for i, id := range roster.Order {
		pending, conn := s.OpenCore(id)
		s.AddAccount(id, pending, conn)
		if id == roster.Active {
			s.Active = i
		}
	}
	defer closeCores(s)
	defer s.CloseSettings()

	if o.tour.on {
		if err := runTour(&o.tour, s, sh); err != nil {
			fmt.Fprintln(os.Stderr, "zapfast:", err)
			closeCores(s)
			os.Exit(1)
		}
		return
	}
	var b app.Builder = sh
	if hover {
		b = &hoverer{Shell: sh, x: hx, y: hy}
	}
	if o.out != "" {
		if err := render(o.out, sh, b, o.dark, page); err != nil {
			fmt.Fprintln(os.Stderr, "zapfast:", err)
			closeCores(s)
			os.Exit(1)
		}
		return
	}
	if o.budget.on() {
		os.Exit(runBudgets(&o, s, sh, h))
	}
	if hover {
		if err := runHover(s, b.(*hoverer)); err != nil {
			fmt.Fprintln(os.Stderr, "zapfast:", err)
			closeCores(s)
			os.Exit(1)
		}
		return
	}
	if err := app.Main(sys.Config(AppID, s.Settings.KeepRunning), h); err != nil {
		fmt.Fprintln(os.Stderr, "zapfast:", err)
		closeCores(s)
		os.Exit(1)
	}
}

// render draws the main window offscreen into a PNG once the account's chat
// list is in and the demo page (nil for none) has applied (or after 5 s).
// b builds the window: the shell, or the shell under -demo-hover's pointer.
func render(path string, sh *shell.Shell, b app.Builder, dark bool, page *demoPage) error {
	sh.Traffic = true
	cfg := shell.Config(sh.S)
	off, err := app.NewOffscreen(cfg, int(cfg.Width*2), int(cfg.Height*2), 2, b)
	if err != nil {
		return err
	}
	defer off.Close()
	off.UI.SetSystemAppearance(ui.Appearance{Dark: dark, Known: true})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		off.Frame()
		if a := sh.S.Acct(); a != nil && a.Model.Chats.Loaded() && (page == nil || page.done && !page.late) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The demo page applies once the chats are in; its chat's messages
	// and pictures come a few frames later.
	for range 24 {
		off.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	pic, _ := off.Frame()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, pic)
}
