module zapfast

go 1.27.1

require (
	github.com/ebitengine/purego v0.12.0-alpha.1.0.20261015130536-d456b1bb95e8
	github.com/pion/datachannel v1.6.3
	github.com/pion/dtls/v3 v3.1.15
	github.com/pion/logging v0.2.4
	github.com/pion/rtcp v1.2.20
	github.com/pion/rtp v1.10.6
	github.com/pion/sctp v1.12.0
	go.mau.fi/libsignal v0.2.2
	go.mau.fi/whatsmeow v0.0.0-20261007111105-c386243a72ba
	golang.org/x/image v0.23.0
	golang.org/x/text v0.43.0
	gonativeui v0.0.0-00010101000000-000000000000
	google.golang.org/protobuf v1.36.12
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/beeper/argo-go v1.1.2 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/elliotchance/orderedmap/v3 v3.1.0 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/petermattis/goid v0.0.0-20260820044319-269ab09b5261 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/transport/v5 v5.1.1 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	github.com/srwiley/oksvg v0.0.0-20221011165216-be6e8873101c // indirect
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef // indirect
	github.com/vektah/gqlparser/v2 v2.5.27 // indirect
	go.mau.fi/util v0.10.1 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/exp v0.0.0-20260908205506-85c1c2202aba // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

// gonativeui's module path is a placeholder (D1), so it is never fetched: this
// points at the framework checkout beside this repository's main checkout
// (~/Projects/go-native-ui next to ~/Projects/zap). A worktree
// anywhere else uses an uncommitted go.work instead (README.md).
replace gonativeui => ../../go-native-ui

// The framework's patched typesetting (its D23). A replace in a dependency's
// go.mod does not apply here, so it is repeated.
replace github.com/go-text/typesetting => ../../go-native-ui/third_party/typesetting

// whatsmeow with Zap's call patch: the <call> stanza id and the typed video
// ack the call bridge needs (third_party/whatsmeow/ZAP-PATCH.md).
replace go.mau.fi/whatsmeow => ./third_party/whatsmeow
