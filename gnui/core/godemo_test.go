package core_test

import (
	"zapfast/core"
	"zapfast/gocore/demo"
)

// The Go demo for the demo tests, with ZAPFAST_ENGINE=go (openDemo).
func init() {
	core.GoDemo = func(cfg core.Config) (core.DemoConn, error) {
		c, err := demo.Open(cfg)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
}
