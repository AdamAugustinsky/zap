package ui_test

import (
	"testing"

	"gonativeui/widget/widgettest"
)

// TestSpacingGrid holds the screens to the framework's 4-point grid
// (go-native-ui's theme/grid.go): every literal a gap, a padding, an inset or
// a margin is built from, a literal times the display scale included, is a
// step. A line that must stay off the grid says so with "grid:ok" and why.
func TestSpacingGrid(t *testing.T) { widgettest.LintSpacingGrid(t, widgettest.GridOpts{}) }
