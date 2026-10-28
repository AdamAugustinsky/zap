package demo

import (
	"os"

	"zapfast/core"
)

// EnvEngine picks the demo core that OpenChosen opens: "go" for this
// package's; anything else, or unset (the default for now), zfcore's
// through the library.
const EnvEngine = "ZAPFAST_ENGINE"

// Chosen reports whether $ZAPFAST_ENGINE picks the Go demo.
func Chosen() bool { return os.Getenv(EnvEngine) == "go" }

// Ready is nil when OpenChosen can open a demo core: always for the Go
// demo; for zfcore's, when its library resolves (core.ResolveLibrary of
// library, "" for $ZAPFAST_CORE or a cargo build).
func Ready(library string) error {
	if Chosen() {
		return nil
	}
	_, err := core.ResolveLibrary(library)
	return err
}

// OpenChosen opens the demo core $ZAPFAST_ENGINE picks, with cfg (Demo is
// set): the Go demo (Open), or zfcore's (core.Open).
func OpenChosen(cfg core.Config) (core.Conn, error) {
	cfg.Demo = true
	if Chosen() {
		c, err := Open(cfg)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	c, err := core.Open(cfg)
	if err != nil {
		return nil, err
	}
	return c, nil
}
