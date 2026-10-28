module zapfast

go 1.27.1

require gonativeui v0.0.0-00010101000000-000000000000

require (
	github.com/ebitengine/purego v0.12.0-alpha.1.0.20261015130536-d456b1bb95e8 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.23.0 // indirect
)

// gonativeui's module path is a placeholder (D1), so it is never fetched: this
// points at the framework checkout beside this repository's main checkout
// (~/Projects/go-native-ui next to ~/Projects/zapfast-gnui). A worktree
// anywhere else uses an uncommitted go.work instead (README.md).
replace gonativeui => ../../go-native-ui

// The framework's patched typesetting (its D23). A replace in a dependency's
// go.mod does not apply here, so it is repeated.
replace github.com/go-text/typesetting => ../../go-native-ui/third_party/typesetting
