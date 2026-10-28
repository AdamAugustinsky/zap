//go:build tools

package gocore

// The backend's dependencies, pinned here until the packages that use them
// land (go mod tidy keeps what a tools-tagged file imports).
import (
	_ "go.mau.fi/libsignal/keys/identity"
	_ "go.mau.fi/whatsmeow"
	_ "go.mau.fi/whatsmeow/store/sqlstore"
)
