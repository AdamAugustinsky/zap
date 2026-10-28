package main

import (
	"errors"

	"gonativeui/app/selfupdate"

	"zapfast/ui/screen"
	"zapfast/ui/settings"
)

// inertSelf stands in for app/selfupdate in the tests, so that none of them
// asks the real one anything.
type inertSelf struct{}

func (inertSelf) Supported() error { return errors.ErrUnsupported }
func (inertSelf) Prepare(string) (settings.StagedUpdate, error) {
	return nil, errors.New("the tests install nothing")
}
func (inertSelf) Started()                      {}
func (inertSelf) LastResult() selfupdate.Result { return selfupdate.Result{} }

// testScreens is screens with the updater on inertSelf.
func testScreens(demo bool) *screen.Set {
	set := screens(demo)
	set.Updates.(updates).u.SetSystem(inertSelf{})
	return set
}
