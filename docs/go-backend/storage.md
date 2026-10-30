# Zap Go backend: storage and migration (read-only investigation, 2026-10-28)

Repos read: ~/Projects/zap (HEAD 4f36cc4, upstream merged to a95d8f0), ~/Projects/go-native-ui (a0c74d2),
~/Documents/projects/a3-lab/zapfast-go (HEAD 3104c46 plus someone's uncommitted edits, read only),
whatsapp-rust 5e72baf (~/.cargo/git/checkouts/whatsapp-rust-6e26428647c827f3/5e72baf), whatsmeow c386243a72ba.
No user data, Keychain items or app-support folders were opened.

## 1. What the Rust core stores today

Roots. `gnui/core/identity.go`: AppID = KeyringService = `ai.a3lab.zap`; `Dirs()` uses go-native-ui
`app/paths` → state `~/Library/Application Support/ai.a3lab.zap`, cache `~/Library/Caches/ai.a3lab.zap`.
`zfcore/src/cores.rs:129-175` refuses an empty or upstream keyring service (`rocks.zapfast.ZapFast`), then builds
upstream `AppDirs{config=state=state_dir, cache}` and `.account(id)` (`src/paths.rs:96-104`).

Per account `<id>` (ids "1", "2", …; roster `accounts.json` and app `settings.json` in state root, written by
Go `gnui/state/persist.go:41-44`):

| File | Path | Format | Key |
|---|---|---|---|
| Message archive | state/accounts/<id>/archive.db (+ -wal/-shm, WAL mode) | SQLCipher 4 (rusqlite 0.40, libsqlite3-sys 0.38.2 = SQLCipher 4.14.0 / SQLite 3.51.3, `Cargo.toml:85` bundled-sqlcipher-vendored-openssl), default cipher settings (4096 page, HMAC-SHA512) | raw 32-byte key, `PRAGMA key = "x'<64 hex>'"` (no KDF), `src/archive/encryption.rs:261-299` |
| Device + Signal store | state/accounts/<id>/session.db (+ -wal/-shm) | **plaintext** SQLite, whatsapp-rust `sqlite-storage` (diesel) schema, 33 migrations through 2026-09-29 | none: `src/backend/worker/device_store.rs:13-21` opens `SqliteStore::open(path)` with no key |
| Account settings | state/accounts/<id>/settings.json | JSON | — |
| Saved stickers, wallpaper | state/accounts/<id>/stickers/, wallpaper.{jpg,png,webp,gif} | files | — |
| Media / avatar / sticker caches | cache/accounts/<id>/{media,avatars,stickers} | plain files; archive `content` JSON keeps local paths (`src/model.rs:1311 keep_local_paths`) | — |
| Logs | state/…/zapfast.log, panic.log (`src/paths.rs:230-236`) | text | — |

Archive key in Keychain (`src/archive/encryption.rs:27-36,128-139,169-182`): macOS legacy (file) keychain
generic password via apple-native-keyring-store 1.0.2 `keychain::Store` (`kSecAttrService` = `ai.a3lab.zap`,
`kSecAttrAccount` = `archive-<hex SHA-256 of canonicalized parent dir>`), value = the 32 raw key bytes. The parent
dir is `…/accounts/<id>`, so the account name depends on the canonical absolute path (moving the folder needs
`copy_archive_key`, `:38-109`). Missing key + encrypted file fails closed (`:203-208`); a plaintext archive is
migrated to SQLCipher via `sqlcipher_export` into `archive.db.encrypting`, integrity-checked, then renamed (`:314-370`).

Archive schema (`src/archive.rs:75-200` + `src/archive/*.rs`): chats, messages (content = serde JSON of Rust
`Content`, raw protobuf BLOB, reactions JSON), motion_clips, contacts, meta (me_pn, PREPARED schema digest),
chat_removals, message_removals, pending_message_removals, lids, stickers, group_receipts, drafts, favorites,
favorite_changes, favorite_stickers, local_labels, local_chat_labels, message_pins, polls, poll_votes,
poll_history, stars, removed_recent_stickers; columns added by an ALTER list (`MIGRATIONS`, `:178ff`).

Device/Signal store is beside, not inside, the archive. Tables (whatsapp-rust `storages/sqlite-storage/migrations`):
device (identity, noise, signed prekey, ADV secret, account blob, next_pre_key_id, first_unupload_pre_key_id,
nct_salt, lid_migrated, read_receipts_disabled, last_signed_pre_key_rotation_ms, …), identities, sessions,
prekeys, signed_prekeys (rotated, retained old ones), sender_keys, sender_key_devices, skdm_recipients,
app_state_keys/versions/mutation_macs, lid_pn_mapping, msg_secrets, tc_tokens, base_keys, device_registry,
sent_messages, pending_inbound_messages, group_metadata. Zap re-adds `msg_secrets.created_at` after
whatsapp-rust drops it (device_store.rs:23-41), a Zap-specific deviation a converter must tolerate.

## 2. Storage options under no-cgo

Facts:
- whatsmeow `sqlstore.NewWithDB(db, "sqlite3", log)` takes any `*sql.DB`; dialect is parsed by prefix only
  (`go.mau.fi/util@v0.10.1/dbutil/database.go:39-49`). Any SQLite driver works; needs foreign_keys ON per connection.
- go-native-ui has no SQLite code. It already has a cgo-free Keychain API: `app/secret` (SecItem generic
  password, legacy keychain, service+account) — same item shape as apple-native-keyring-store, so
  `secret.Open("ai.a3lab.zap").Get("archive-<sha>")` reads the existing key. Its own research said a pure-Go
  port "cannot open ZapFast's SQLCipher archives without cgo" (`docs/research/zapfast.md:227-233`).
- Zap already bundles a non-system dylib loaded with purego (`gnui/core/lib.go:18,75`,
  Contents/Frameworks/libzapfast_core.dylib) under the app-level exception in `DECISIONS.md:709-727`.
- zapfast-go uses `mutecomm/go-sqlcipher/v4` (cgo, SQLCipher 4.4.2) in `internal/archive/store.go:24,39-66` and
  `internal/zzbase/store.go:23`. Not usable in Zap's app binary.

(a) SQLCipher via purego (bundled libsqlcipher.dylib). Reads the existing archive in place, same key, same
format. Existing purego SQLite bindings are all young: `bnema/purego-sqlite` (has a database/sql driver, Linux
loader only, "early stage"), `pjstein/purego-sqlite` v0.1.0 (darwin/arm64 tested, custom-compiled SQLite,
`SetLibraryPath`, no database/sql driver yet). None targets SQLCipher, but SQLCipher is ABI-compatible SQLite
plus `sqlite3_key`/`PRAGMA key`, so a binding pointed at a SQLCipher dylib works. Build SQLCipher 4.14 with
`SQLITE_HAS_CODEC`, `SQLCIPHER_CRYPTO_CC` (CommonCrypto, links only system frameworks, no OpenSSL),
`SQLITE_THREADSAFE=2`, `SQLITE_TEMP_STORE=3`, JSON built in (archive uses json_extract in indexes), FTS5 if needed.
Driver must be a `driver.Connector` that runs `PRAGMA key` as the first statement of every pooled connection,
then `cipher_version` and a `sqlite_master` read to verify (mirror `encryption.rs:283-299`). Use `busy_timeout`,
not Go callbacks (purego callbacks are limited and the ABI rules avoid them).

(b) ncruces/go-sqlite3 (wasm2go, pure Go, MIT). `vfs/adiantum` and `vfs/xts` encrypt at rest but are
deterministic, have no page MACs and are not SQLCipher-compatible; its author closed SQLCipher/sqlite3mc
support (issue #55). Usable for a new Go-only store, cannot read the existing archive, higher memory.
(c) modernc.org/sqlite / glebarez: pure Go, no encryption at all. Only with app-level AEAD or FileVault-only,
which breaks DECISIONS.md:1002 ("names and previews never leave SQLCipher").
(d) Hybrid: pure-Go read-only SQLCipher-4 page decryptor (AES-256-CBC + HMAC-SHA512, raw-key path) feeding
ncruces memdb for a one-time import, then ncruces+adiantum. Avoids a dylib, but we would own crypto code.

Recommendation: (a). One small bundled dylib (SQLCipher, CommonCrypto) replaces the far larger Rust core
exception, keeps the SQLCipher-at-rest guarantee with page MACs, opens the Rust archive with its existing Keychain
key, and serves both the Go archive and whatsmeow's sqlstore. Write our own ~400-line database/sql driver
(or vendor pjstein's binding layer and add a driver) rather than depend on a 0.x package; record the dylib in
DECISIONS. Encrypt the whatsmeow store too (today's session.db is plaintext): separate file
`accounts/<id>/whatsmeow.db`, own Keychain account (e.g. `session-<sha>`), separate lifecycle (unlink deletes
session, keeps archive), no WAL contention with the archive.

Archive schema: keep vs migrate. Keeping upstream's schema in place means the Go backend must read/write
serde-shaped `content` JSON exactly as Rust does and follow upstream's ad-hoc ALTER migrations forever, and an
in-place Go write makes rollback to the Rust core unsafe. Recommend: new Go-owned archive file
(`archive-go.db`, SQLCipher, same raw-key scheme, versioned migrations), populated by a one-time importer;
the original `archive.db` stays untouched as backup/rollback. Start from zapfast-go's `internal/archive` +
`internal/migration/import.go/transform.go` (already validated on Adam's real ZapFast archive: 38,937 messages,
519 chats), but its importer is pinned to upstream df01459 (`docs/IMPORT.md:3`); Zap is at a95d8f0 with more
columns (group_receipts, motion_clips, group_description, info_locked, notification_sound, …). Port it
behind the purego driver and extend it to a95d8f0's schema; keep its "preserve every original row encrypted
in rust-import metadata" rule so nothing unmapped is lost.

## 3. Migrating Adam's MacBook install (linked, installed 2026-10-28)

Session takeover without re-linking is feasible. zapfast-go `internal/migration/session.go` +
`session_signal.go` (`ImportRustSession`, commit d16e899) does it offline: snapshot source + WAL with hash
checks (source never opened in place), read-only/query_only; require exactly one device; verify identity/noise
key pairs, signed-prekey signature and ADV signed device identity (`session.go:172-217`); convert identities,
sessions (Rust protobuf → libsignal-go records, advancing Rust's reserved sender counters so no message key is
reused), prekeys with uploaded flags, sender keys (iterations advanced), app-state keys/versions/MACs (a
"fatal" collection is reset for a fresh snapshot), LID↔PN current mappings, msg_secrets, tc_tokens, nct_salt;
preserves all source rows of 18 tables as JSON in `zapfast_rust_session_preserved`; staged encrypted DB,
integrity + count + SDK read-back, exclusive rename publish (`renameatx_np RENAME_EXCL`). Real-data result
(`docs/IMPORT.md` "Saved session conversion"): 198 sessions, 601 prekeys, 104 sender keys, 94 app-state keys,
812 LID mappings, 15,221 secrets; verified only by an auth-only server handshake (`--probe-session`), not by
live sends/decrypts.

What can break / gaps for Zap:
- Schema drift: zapfast-go's synthetic fixture is hand-written (`session_import_test.go:23-47`), and its real run
  used an older whatsapp-rust. Zap's 5e72baf store has later migrations (signed-prekey rotation, `@c.us` →
  `@s.whatsapp.net` rewrite of tc_tokens/device_registry/sender_key_devices/sent_messages, msg_secrets
  created_at drop-then-Zap-re-add, status privacy, client expiration). Explicit-column SELECTs tolerate extra
  columns, but address parsing of rewritten JIDs must be re-tested.
- Prekeys: import fails closed if `highest+1 < next_pre_key_id` (`session.go:341`); a store whose top prekeys
  were consumed and deleted would be refused → fall back to re-link (or design placeholder rows).
- Signed prekeys: whatsmeow keeps one signed prekey and never rotates (`store/signal.go:139-147`);
  rotated-out ones in `signed_prekeys` are only preserved, so in-flight pkmsgs to an old id fail until the
  peer's retry. Rotation stops after migration.
- Not converted (preserved only): pending_inbound_messages (buffered undecrypted messages lost), sent_messages
  (retry payloads for pre-cutover sends), device_registry/sender_key_devices/skdm_recipients (whatsmeow
  refetches device lists and resends SKDM to all group devices, so harmless), base_keys.
- App-state: a reset collection triggers a full resnapshot; mutes/pins/archives re-sync from the phone.
- Ratchet forking: Rust and Go must never both run on the same identity after cutover. Once Go sends or
  decrypts, the Rust copy is stale; resuming it means counter reuse/desync → re-link.
- Keychain ACL: the legacy-keychain item trusts the creating app (the Zap main executable, signed with Developer
  ID on the MacBook). A Go build with the same bundle id and signing identity reads it without a prompt; an
  ad-hoc or differently signed build gets a prompt or ErrDenied.

Safe procedure (in-app, one-time, per account, before the backend connects):
1. Preconditions: single-instance lock held, no Rust core in process, Keychain readable; else stop and report.
2. Backup, never delete: copy (APFS clone where possible) the whole `state/accounts/<id>` into
   `state/accounts/<id>/rust-backup-<UTC timestamp>/` with 0700, fsync, and a SHA-256 manifest. Caches are not
   copied (they are reproducible) but are adopted in place. The Keychain item is only read.
3. Session: `ImportRustSession`-style conversion from the snapshot into staged `whatsmeow.db` encrypted with a
   new Keychain key (write, read back, compare before use); exclusive publish.
4. Archive: open the snapshot of `archive.db` read-only with the existing key, import into staged
   `archive-go.db`, integrity_check + counts + read-back, exclusive publish.
5. Write `migration.json` atomically (source hashes, counts, versions, state=converted). Any failure before this
   point deletes only staging files, leaves originals untouched, shows "not migrated, nothing changed" and
   offers retry or re-link (re-link creates a fresh Go session and still imports the archive).
6. Activation: rename (not delete) `session.db*` into the backup folder so an older Rust build can never reuse
   the stale ratchet, set state=active, then connect (optionally the passive auth-only probe first).
7. Rollback before step 6: reinstall the Rust build, nothing changed. After step 6: restore the backup and
   re-link; document that the old session cannot be resumed.

## 4. Risks and plan for the storage/migration work package

Risks: purego SQLCipher driver bugs (pooling without keying, text/blob/NULL/int64 conversions, busy handling)
→ silent data loss; dylib exception and signing in `gnui package`; GPL-3.0 libsignal via whatsmeow
(`docs/research/zapfast.md:245-251`) changes Zap's distribution licence; session conversion schema drift;
Keychain prompt with a mis-signed build; archive importer gaps vs a95d8f0 schema; account-ban risk is unchanged.

Plan (each step independently testable, CGO_ENABLED=0):
- S1 libsqlcipher build script (pinned SQLCipher 4.14.0 source + checksum, CommonCrypto, universal) and
  packaging into Contents/Frameworks; DECISIONS entry.
- S2 `gnui/internal/sqlc` purego binding + database/sql driver + Connector(key). Tests: open/keyed pool of N
  conns, wrong key fails before any write, WAL, int64/blob/NULL/text round-trips, ctx cancel via
  sqlite3_interrupt, concurrent readers + writer, whatsmeow `sqlstore.Upgrade` + `PutDevice` + Signal store
  round-trip on it.
- S3 Keychain key helper on `app/secret` reproducing `identity_for` (canonical path SHA-256) bit-for-bit;
  fake store in tests; plus a fixture test that the Go identity string equals the Rust one for the same path.
- S4 Archive: Go schema + migrations; importer from a95d8f0 archive (ported from zapfast-go), preserving
  unmapped rows.
- S5 Session converter ported from zapfast-go, extended for 5e72baf schema and Zap's created_at column.
- S6 Orchestrator (procedure above) with marker file and crash-point injection.

Definition of done without live data:
- Fixtures generated by a Rust test in zfcore (already has rusqlite + bundled SQLCipher and whatsapp-rust):
  (1) a SQLCipher archive written by upstream `Archive` code at a95d8f0 with a fixed test key and synthetic
  chats/messages covering every table and content kind; (2) a session.db produced by the real 5e72baf diesel
  migrations + `device_store::open`, populated with synthetic keys (generated identity/noise/signed prekey,
  ADV-signed identity, sessions from a libsignal handshake between two synthetic devices, sender keys,
  app-state keys, @c.us/@s.whatsapp.net/LID addresses, rotated signed prekeys, consumed prekey gaps).
  Committed small, with their keys, labelled synthetic.
- Go tests pass: archive opened in place read-only via purego with the fixture key; import counts and per-row
  equality; whatsmeow read-back of converted sessions, and a two-party test that the converted device decrypts
  a message encrypted by the synthetic peer's Rust-side session and encrypts one the peer accepts (libsignal-go
  on both sides); source file hashes unchanged; wrong key / missing Keychain item / prekey-gap / unknown schema
  all fail closed with originals intact; kill at each step leaves either the old state or the new state.
- `go vet` + `CGO_ENABLED=0 go build` of gnui; `otool -L` of the bundle shows only system libs plus
  libsqlcipher.dylib.
- Live cutover on the MacBook is a separate, explicitly authorized step after this DoD.

## Summary
1. Rust core per account: SQLCipher-4 archive.db (raw 32-byte Keychain key, service ai.a3lab.zap, account archive-<sha256(path)>) plus a plaintext whatsapp-rust session.db beside it, settings/stickers in state, media in Caches.
2. No pure-Go option reads SQLCipher; ncruces adiantum/xts and modernc are incompatible or unencrypted.
3. Recommend a bundled CommonCrypto libsqlcipher.dylib via purego with our own database/sql driver, used for a new Go archive and an encrypted whatsmeow store; Keychain via go-native-ui app/secret.
4. Session takeover is feasible by porting zapfast-go's ImportRustSession (validated auth-only), with known gaps (rotated signed prekeys, pending inbound, prekey gap refusal, schema drift to 5e72baf).
5. Migrate copy-first: backup, convert into new files, verify, marker, rename (never delete) old session.db, re-link as the fail-closed fallback; prove it on Rust-generated synthetic fixtures before touching the MacBook.
