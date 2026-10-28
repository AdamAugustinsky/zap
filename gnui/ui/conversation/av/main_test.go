package av_test

import (
	"testing"

	"gonativeui/widget/widgettest"
)

// RunMain serves the main thread for the live video test
// (live_darwin_test.go): AVPlayer plays there.
func TestMain(m *testing.M) { widgettest.RunMain(m) }
