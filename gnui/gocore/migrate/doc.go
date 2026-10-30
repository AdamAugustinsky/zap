// Package migrate takes over an account the Rust core kept: its
// whatsapp-rust session store (session.db) becomes whatsmeow's encrypted
// store (whatsmeow.db, ConvertSession), and its archive (archive.db) is
// imported into the Go archive (archive-go.db, ImportArchive). Account runs
// both as one crash-safe, idempotent step: back up, convert into staging,
// verify, publish without overwriting, mark, and retire the old session
// store by renaming it into the backup. The Rust archive is only ever read,
// from copies; nothing of the Rust core's is deleted.
//
// The study behind it is docs/go-backend/storage.md (§3). The fixtures in
// testdata were written by the Rust core's own code
// (zfcore/tests/migration_fixtures.rs, removed in WP9); they are synthetic,
// and can no longer be regenerated here. testdata/oracle holds what that
// Rust core reported for a seeded synthetic archive and for the fixture's,
// which differential_test.go holds the import to; its generator is a patch
// on zfcore at 5a57d37 (testdata/oracle/migration_oracle.patch).
package migrate
