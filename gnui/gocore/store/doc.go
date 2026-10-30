// Package store is the Go backend's storage: per account, the message
// archive (Archive, from zapfast-go's, extended to the Rust core's tables)
// and whatsmeow's device and Signal store (sqlstore), each in its own
// SQLCipher file under its own Keychain key (OpenAccount). SQLCipher is
// libsqlcipher.dylib through package sqlite, loaded at run time with no
// cgo. Wrong or missing keys fail closed. The study behind it is
// docs/go-backend/storage.md.
package store
