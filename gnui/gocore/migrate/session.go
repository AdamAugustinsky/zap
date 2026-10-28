// From zapfast-go 3104c46 (internal/migration/session.go), MIT: its
// ImportRustSession, ported to the purego SQLCipher driver and extended for
// whatsapp-rust 5e72baf's store as Zap opens it: the schema is checked
// against the 33 migrations and the column sets this converter knows (Zap's
// re-added msg_secrets.created_at included) before any row is read, so a
// newer or older store fails closed.

package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"go.mau.fi/libsignal/ecc"
	groupRecord "go.mau.fi/libsignal/groups/state/record"
	"go.mau.fi/libsignal/serialize"
	"go.mau.fi/libsignal/state/record"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waE2E"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/util/keys"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"zapfast/gocore/store"
	"zapfast/gocore/store/sqlite"
)

var (
	// ErrUnknownSchema means the Rust store was written by a whatsapp-rust
	// this converter does not know: a migration, table or column it has
	// not been checked against. Nothing is converted.
	ErrUnknownSchema = errors.New("migrate: the Rust session store's schema is not one this build knows")
	// ErrIncompatible means a Rust row is invalid or cannot be represented
	// in whatsmeow's store. Nothing is converted.
	ErrIncompatible = errors.New("migrate: the Rust session store holds a record this build cannot convert")
	// ErrPreKeyGap means Rust handed out prekey ids above the highest it
	// still holds. whatsmeow numbers new prekeys from the highest it holds,
	// so it would reuse ids the server already has: re-link instead.
	ErrPreKeyGap = errors.New("migrate: the Rust store's prekey ids have a gap whatsmeow would reuse; re-link the account")
)

// rustMigrations are whatsapp-rust 5e72baf's diesel migrations
// (storages/sqlite-storage/migrations), all of which a store must have run
// and none other.
var rustMigrations = []string{
	"20250814035031", "202509230322320000", "20251204000000", "20251211000000",
	"20251224000000", "20260205000000", "20260212000000", "20260312000000",
	"20260315000000", "20260323000000", "20260326000000", "20260331000000",
	"20260406171140", "20260426000000", "20260513000000", "20260528000000",
	"20260531000000", "20260601000000", "20260610000000", "20260626000000",
	"20260702000000", "20260703000000", "20260703000001", "20260816000000",
	"20260831000000", "20260903000000", "20260903000001", "20260904000000",
	"20260904000001", "20260904000002", "20260916000000", "20260916000001",
	"20260929000000",
}

// rustTables are 5e72baf's tables and columns (its src/schema.rs).
var rustTables = map[string][]string{
	"app_state_keys":           {"key_id", "key_data", "device_id"},
	"app_state_mutation_macs":  {"name", "version", "index_mac", "value_mac", "device_id"},
	"app_state_versions":       {"name", "state_data", "device_id"},
	"base_keys":                {"address", "message_id", "base_key", "device_id", "created_at"},
	"device_registry":          {"user_id", "devices_json", "timestamp", "phash", "device_id", "updated_at", "raw_id"},
	"group_metadata":           {"group_jid", "info", "device_id", "updated_at"},
	"identities":               {"address", "key", "device_id"},
	"lid_pn_mapping":           {"lid", "phone_number", "created_at", "learning_source", "updated_at", "device_id"},
	"msg_secrets":              {"chat", "sender", "msg_id", "secret", "device_id", "expires_at", "message_ts"},
	"pending_inbound_messages": {"chat", "sender", "id", "message", "device_id", "inserted_at"},
	"prekeys":                  {"id", "key", "uploaded", "device_id"},
	"sender_key_devices":       {"group_jid", "device_jid", "has_key", "device_id", "updated_at"},
	"sender_keys":              {"address", "record", "device_id"},
	"sent_messages":            {"chat_jid", "message_id", "payload", "device_id", "created_at"},
	"sessions":                 {"address", "record", "device_id"},
	"signed_prekeys":           {"id", "record", "device_id"},
	"tc_tokens":                {"jid", "token", "token_timestamp", "sender_timestamp", "device_id", "updated_at"},
	"device": {"id", "lid", "pn", "registration_id", "noise_key", "identity_key", "signed_pre_key",
		"signed_pre_key_id", "signed_pre_key_signature", "adv_secret_key", "account", "push_name",
		"app_version_primary", "app_version_secondary", "app_version_tertiary",
		"app_version_last_fetched_ms", "edge_routing_info", "props_hash", "next_pre_key_id", "nct_salt",
		"server_has_prekeys", "server_cert_chain", "login_counter", "first_unupload_pre_key_id",
		"lid_migrated", "last_signed_pre_key_rotation_ms", "read_receipts_disabled",
		"server_client_expiration", "status_privacy"},
}

// zapExtraColumns are columns Zap adds to the library's tables: its device
// store re-adds msg_secrets.created_at that 5e72baf drops
// (src/backend/worker/device_store.rs), so a store may have it or not.
var zapExtraColumns = map[string][]string{"msg_secrets": {"created_at"}}

// preservedTable keeps every source row, as JSON, in the encrypted
// whatsmeow.db, so nothing the conversion leaves out is lost.
const preservedTable = "zap_rust_session_preserved"

// SessionReport holds counts only: no id, address or key leaves the
// converter. AppStateReset collections ask for a fresh snapshot.
type SessionReport struct {
	Devices                   int64 `json:"devices"`
	Identities                int64 `json:"identities"`
	Sessions                  int64 `json:"sessions"`
	SessionCountersAdvanced   int64 `json:"session_counters_advanced"`
	PreKeys                   int64 `json:"prekeys"`
	UploadedPreKeys           int64 `json:"uploaded_prekeys"`
	SenderKeys                int64 `json:"sender_keys"`
	SenderKeyCountersAdvanced int64 `json:"sender_key_counters_advanced"`
	AppStateKeys              int64 `json:"app_state_keys"`
	AppStateVersions          int64 `json:"app_state_versions"`
	AppStateMACs              int64 `json:"app_state_macs"`
	AppStateReset             int64 `json:"app_state_reset"`
	LIDMappings               int64 `json:"lid_mappings"`
	HistoricalLIDAliases      int64 `json:"historical_lid_aliases"`
	MessageSecrets            int64 `json:"message_secrets"`
	PrivacyTokens             int64 `json:"privacy_tokens"`
	AmbiguousPrivacyTokens    int64 `json:"ambiguous_privacy_tokens"`
	// RotatedSignedPreKeys are kept only as preserved rows: whatsmeow has
	// one signed prekey, so a message to an older one needs the peer's
	// retry.
	RotatedSignedPreKeys int64 `json:"rotated_signed_prekeys"`
	// PendingInbound are messages Rust decrypted but had not handed on:
	// preserved, not replayed.
	PendingInbound       int64 `json:"pending_inbound"`
	PreservedRows        int64 `json:"preserved_rows"`
	ReadReceiptsDisabled bool  `json:"read_receipts_disabled"`
}

// ConvertSession converts the Rust core's plaintext session store at
// source into a new whatsmeow store at target, encrypted with key. It reads
// a snapshot of source (never source itself), checks the snapshot's schema
// first, converts in a private staging folder beside target, verifies the
// result through whatsmeow and libsignal, and publishes it without
// replacing anything at target. It never connects to WhatsApp. The caller
// keeps the Rust core and any other user of the same linked device away
// from source for good once the result is used: their ratchets fork.
func ConvertSession(ctx context.Context, source, target string, key []byte) (report SessionReport, err error) {
	if len(key) != 32 {
		return report, store.ErrKey
	}
	if err = ctx.Err(); err != nil {
		return report, err
	}
	if source, err = filepath.Abs(source); err != nil {
		return report, err
	}
	if target, err = filepath.Abs(target); err != nil || target == source {
		return report, errors.New("migrate: invalid session destination")
	}
	if found, err := exists(target); err != nil || found {
		return report, errors.Join(ErrDestinationExists, err)
	}
	if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return report, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".zap-session-convert-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(stage)
	staged := filepath.Join(stage, "whatsmeow.db")
	if report, err = convertSessionInto(ctx, source, staged, key); err != nil {
		return report, err
	}
	if err = publish(staged, target); err != nil {
		return report, err
	}
	return report, nil
}

// convertSessionInto converts source into a new encrypted store at staged
// (which must not exist) and verifies it there.
func convertSessionInto(ctx context.Context, source, staged string, key []byte) (report SessionReport, err error) {
	snapshot := staged + ".rust-source"
	if err = copySourceSnapshot(ctx, source, snapshot); err != nil {
		return report, err
	}
	defer removeDB(snapshot)
	src, err := sqlite.Open(sqlite.Config{Path: snapshot, Plaintext: true, ReadOnly: true})
	if err != nil {
		return report, fmt.Errorf("migrate: opening the Rust session snapshot: %w", err)
	}
	defer src.Close()
	src.SetMaxOpenConns(1)
	read, err := src.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return report, err
	}
	defer read.Rollback()
	if err = checkRustSchema(ctx, read); err != nil {
		return report, err
	}
	target, err := store.OpenDatabase(staged, key)
	if err != nil {
		return report, fmt.Errorf("migrate: creating the encrypted session store: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			target.Close()
		}
	}()
	container := sqlstore.NewWithDB(target, "sqlite3", waLog.Noop)
	if err = container.Upgrade(ctx); err != nil {
		return report, fmt.Errorf("migrate: whatsmeow's schema: %w", err)
	}
	device, rust, err := readRustDevice(ctx, read)
	if err != nil {
		return report, err
	}
	if err = container.PutDevice(ctx, device); err != nil {
		return report, fmt.Errorf("migrate: storing the converted device: %w", err)
	}
	report.Devices, report.ReadReceiptsDisabled = 1, rust.receiptsOff
	write, err := target.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer write.Rollback()
	converter := sessionImporter{
		ctx: ctx, source: read, target: write, deviceID: rust.id, jid: device.ID.String(),
		identityPublic: append([]byte{ecc.DjbType}, device.IdentityKey.Pub[:]...),
		registrationID: device.RegistrationID, report: &report, resetCollections: map[string]bool{},
	}
	if err = converter.convert(rust.next, rust.first, rust.salt); err != nil {
		return report, err
	}
	if err = write.Commit(); err != nil {
		return report, err
	}
	var integrity string
	if err = target.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return report, errors.Join(errors.New("migrate: the converted session store failed its integrity check"), err)
	}
	if _, err = target.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return report, err
	}
	closed = true
	if err = target.Close(); err != nil {
		return report, err
	}
	if err = verifySession(ctx, staged, key, report, device); err != nil {
		return report, err
	}
	return report, ctx.Err()
}

// removeDB removes a scratch database and its sidecars.
func removeDB(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		os.Remove(path + suffix)
	}
}

// checkRustSchema fails closed unless the store ran exactly 5e72baf's
// migrations and has exactly its tables and columns (plus Zap's own).
func checkRustSchema(ctx context.Context, q *sql.Tx) error {
	applied, err := queryStrings(ctx, q, "SELECT version FROM __diesel_schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("%w: no migration record", ErrUnknownSchema)
	}
	want := slices.Clone(rustMigrations)
	sort.Strings(want)
	if !slices.Equal(applied, want) {
		return fmt.Errorf("%w: %d migrations, %d known", ErrUnknownSchema, len(applied), len(want))
	}
	tables, err := queryStrings(ctx, q, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite\\_%' ESCAPE '\\' AND name<>'__diesel_schema_migrations' ORDER BY name")
	if err != nil {
		return err
	}
	if len(tables) != len(rustTables) {
		return fmt.Errorf("%w: %d tables, %d known", ErrUnknownSchema, len(tables), len(rustTables))
	}
	for _, table := range tables {
		known, ok := rustTables[table]
		if !ok {
			return fmt.Errorf("%w: table %q", ErrUnknownSchema, table)
		}
		columns, err := queryStrings(ctx, q, "SELECT name FROM pragma_table_info(?)", table)
		if err != nil {
			return err
		}
		extra := zapExtraColumns[table]
		for _, c := range columns {
			if !slices.Contains(known, c) && !slices.Contains(extra, c) {
				return fmt.Errorf("%w: column %s.%s", ErrUnknownSchema, table, c)
			}
		}
		for _, c := range known {
			if !slices.Contains(columns, c) {
				return fmt.Errorf("%w: no column %s.%s", ErrUnknownSchema, table, c)
			}
		}
	}
	return nil
}

func queryStrings(ctx context.Context, q *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []string
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// rustDevice is what the conversion needs of the Rust device row besides
// the whatsmeow device.
type rustDevice struct {
	id, next, first int64
	receiptsOff     bool
	salt            []byte
}

func readRustDevice(ctx context.Context, source *sql.Tx) (*wastore.Device, rustDevice, error) {
	var rust rustDevice
	var count int64
	if source.QueryRowContext(ctx, "SELECT COUNT(*) FROM device").Scan(&count) != nil || count != 1 {
		return nil, rust, fmt.Errorf("%w: the store must hold exactly one linked device", ErrIncompatible)
	}
	var registration, signedID int64
	var pn, lid, name string
	var noise, identity, signed, signature, adv, accountRaw []byte
	var migrated bool
	err := source.QueryRowContext(ctx, `SELECT id,pn,lid,registration_id,noise_key,identity_key,signed_pre_key,signed_pre_key_id,signed_pre_key_signature,adv_secret_key,account,push_name,next_pre_key_id,first_unupload_pre_key_id,lid_migrated,read_receipts_disabled,nct_salt FROM device`).
		Scan(&rust.id, &pn, &lid, &registration, &noise, &identity, &signed, &signedID, &signature, &adv, &accountRaw, &name, &rust.next, &rust.first, &migrated, &rust.receiptsOff, &rust.salt)
	if err != nil || registration <= 0 || registration > math.MaxUint32 || signedID < 0 || signedID >= 1<<24 || rust.next <= 0 || rust.next >= 1<<24 || rust.first <= 0 || rust.first > rust.next || len(signature) != 64 || len(adv) != 32 || len(rust.salt) > 1024 {
		return nil, rust, fmt.Errorf("%w: device credentials", ErrIncompatible)
	}
	phone, err := sessionJID(pn)
	if err != nil || phone.Server != types.DefaultUserServer || phone.Device == 0 {
		return nil, rust, fmt.Errorf("%w: device id", ErrIncompatible)
	}
	hidden, err := sessionJID(lid)
	if err != nil || hidden.Server != types.HiddenUserServer {
		return nil, rust, fmt.Errorf("%w: device LID", ErrIncompatible)
	}
	noiseKey, noiseErr := keyPair(noise)
	identityKey, identityErr := keyPair(identity)
	signedKey, signedErr := keyPair(signed)
	var account waAdv.ADVSignedDeviceIdentity
	if noiseErr != nil || identityErr != nil || signedErr != nil || strictProto(accountRaw, &account) != nil || len(account.Details) == 0 || len(account.AccountSignature) != 64 || len(account.AccountSignatureKey) != 32 || len(account.DeviceSignature) != 64 {
		return nil, rust, fmt.Errorf("%w: device keys or signed identity", ErrIncompatible)
	}
	public := append([]byte{ecc.DjbType}, signedKey.Pub[:]...)
	if !ecc.VerifySignature(ecc.NewDjbECPublicKey(*identityKey.Pub), public, *(*[64]byte)(signature)) {
		return nil, rust, fmt.Errorf("%w: the signed prekey's signature does not verify", ErrIncompatible)
	}
	if !validSignedDeviceIdentity(&account, identityKey) {
		return nil, rust, fmt.Errorf("%w: the signed device identity does not verify", ErrIncompatible)
	}
	device := &wastore.Device{NoiseKey: noiseKey, IdentityKey: identityKey, SignedPreKey: &keys.PreKey{KeyPair: *signedKey, KeyID: uint32(signedID), Signature: (*[64]byte)(signature)}, RegistrationID: uint32(registration), AdvSecretKey: adv, ID: &phone, LID: hidden, Account: &account, PushName: name}
	if migrated {
		// Rust keeps a flag; a nonzero time keeps whatsmeow's migrated
		// state without inventing a date.
		device.LIDMigrationTimestamp = 1
	}
	return device, rust, nil
}

func validSignedDeviceIdentity(account *waAdv.ADVSignedDeviceIdentity, identity *keys.KeyPair) bool {
	var details waAdv.ADVDeviceIdentity
	if strictProto(account.Details, &details) != nil {
		return false
	}
	accountPrefix := []byte{6, 0}
	if details.GetDeviceType() == waAdv.ADVEncryptionType_HOSTED {
		accountPrefix = []byte{6, 5}
	}
	accountMessage := append(accountPrefix, account.Details...)
	accountMessage = append(accountMessage, identity.Pub[:]...)
	if !ecc.VerifySignature(ecc.NewDjbECPublicKey(*(*[32]byte)(account.AccountSignatureKey)), accountMessage, *(*[64]byte)(account.AccountSignature)) {
		return false
	}
	deviceMessage := append([]byte{6, 1}, account.Details...)
	deviceMessage = append(deviceMessage, identity.Pub[:]...)
	deviceMessage = append(deviceMessage, account.AccountSignatureKey...)
	return ecc.VerifySignature(ecc.NewDjbECPublicKey(*identity.Pub), deviceMessage, *(*[64]byte)(account.DeviceSignature))
}

type sessionImporter struct {
	ctx              context.Context
	source, target   *sql.Tx
	deviceID         int64
	jid              string
	identityPublic   []byte
	registrationID   uint32
	report           *SessionReport
	resetCollections map[string]bool
}

// maxRows bounds every table read.
const maxRows = 1_000_000

func (s *sessionImporter) each(query string, fn func(*sql.Rows) error) error {
	rows, err := s.source.QueryContext(s.ctx, query, s.deviceID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnknownSchema, err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if count++; count > maxRows {
			return fmt.Errorf("%w: a table exceeds the import bounds", ErrIncompatible)
		}
		if err = fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *sessionImporter) insert(query string, values ...any) error {
	if _, err := s.target.ExecContext(s.ctx, query, values...); err != nil {
		return fmt.Errorf("migrate: storing a converted row: %w", err)
	}
	return nil
}

func (s *sessionImporter) count(query string) (int64, error) {
	var n int64
	err := s.source.QueryRowContext(s.ctx, query, s.deviceID).Scan(&n)
	return n, err
}

func (s *sessionImporter) convert(next, first int64, salt []byte) error {
	if err := s.each("SELECT address,key FROM identities WHERE device_id=?", func(row *sql.Rows) error {
		var address string
		var key []byte
		if row.Scan(&address, &key) != nil || len(key) != 32 {
			return errSignalRecord
		}
		converted, err := signalAddress(address)
		if err != nil {
			return err
		}
		if err = s.insert("INSERT INTO whatsmeow_identity_keys(our_jid,their_id,identity) VALUES(?,?,?)", s.jid, converted, key); err == nil {
			s.report.Identities++
		}
		return err
	}); err != nil {
		return err
	}
	if err := s.each("SELECT address,record FROM sessions WHERE device_id=?", func(row *sql.Rows) error {
		var address string
		var raw []byte
		if row.Scan(&address, &raw) != nil {
			return errSignalRecord
		}
		converted, err := signalAddress(address)
		if err != nil {
			return err
		}
		payload, advanced, err := convertSessionWithCounters(raw)
		if err != nil {
			return err
		}
		var session record.SessionStructure
		if json.Unmarshal(payload, &session) != nil || session.SessionState.LocalRegistrationID != s.registrationID || !bytes.Equal(session.SessionState.LocalIdentityPublic, s.identityPublic) {
			return fmt.Errorf("%w: a session does not belong to the linked device", ErrIncompatible)
		}
		if err = s.insert("INSERT INTO whatsmeow_sessions(our_jid,their_id,session) VALUES(?,?,?)", s.jid, converted, payload); err == nil {
			s.report.Sessions++
			s.report.SessionCountersAdvanced += advanced
		}
		return err
	}); err != nil {
		return err
	}
	var highest int64
	if err := s.each("SELECT id,key,uploaded FROM prekeys WHERE device_id=? ORDER BY id", func(row *sql.Rows) error {
		var id int64
		var raw []byte
		var uploaded bool
		var prekey serialize.PreKeyRecordStructure
		if row.Scan(&id, &raw, &uploaded) != nil || id <= 0 || id >= 1<<24 || strictProto(raw, &prekey) != nil || int64(prekey.GetId()) != id || !matchingPreKey(prekey.PrivateKey, prekey.PublicKey) || id < first && !uploaded {
			return errSignalRecord
		}
		highest = max(highest, id)
		if err := s.insert("INSERT INTO whatsmeow_pre_keys(jid,key_id,key,uploaded) VALUES(?,?,?,?)", s.jid, id, prekey.PrivateKey, uploaded); err != nil {
			return err
		}
		s.report.PreKeys++
		if uploaded {
			s.report.UploadedPreKeys++
		}
		return nil
	}); err != nil {
		return err
	}
	if highest+1 < next {
		return fmt.Errorf("%w (highest held %d, next %d)", ErrPreKeyGap, highest, next)
	}
	if err := s.each("SELECT address,record FROM sender_keys WHERE device_id=?", func(row *sql.Rows) error {
		var address string
		var raw []byte
		if row.Scan(&address, &raw) != nil {
			return errSignalRecord
		}
		group, sender, err := senderAddress(address)
		if err != nil {
			return err
		}
		payload, advanced, err := convertSenderKeyWithCounters(raw)
		if err != nil {
			return err
		}
		if err = s.insert("INSERT INTO whatsmeow_sender_keys(our_jid,chat_id,sender_id,sender_key) VALUES(?,?,?,?)", s.jid, group, sender, payload); err == nil {
			s.report.SenderKeys++
			s.report.SenderKeyCountersAdvanced += advanced
		}
		return err
	}); err != nil {
		return err
	}
	if err := s.copyAppState(); err != nil {
		return err
	}
	if err := s.copyAuxiliary(); err != nil {
		return err
	}
	if len(salt) != 0 {
		if err := s.insert("INSERT INTO whatsmeow_nct_salt(our_jid,salt) VALUES(?,?)", s.jid, salt); err != nil {
			return err
		}
	}
	var err error
	if s.report.RotatedSignedPreKeys, err = s.count("SELECT COUNT(*) FROM signed_prekeys WHERE device_id=?"); err != nil {
		return err
	}
	if s.report.PendingInbound, err = s.count("SELECT COUNT(*) FROM pending_inbound_messages WHERE device_id=?"); err != nil {
		return err
	}
	return s.preserveSourceRows()
}

func (s *sessionImporter) copyAppState() error {
	if err := s.each("SELECT key_id,key_data FROM app_state_keys WHERE device_id=?", func(row *sql.Rows) error {
		var id, raw []byte
		var key waE2E.AppStateSyncKeyData
		if row.Scan(&id, &raw) != nil || len(id) == 0 || len(id) > 128 || strictProto(raw, &key) != nil || len(key.KeyData) != 32 || key.Fingerprint == nil || key.GetTimestamp() < 0 {
			return fmt.Errorf("%w: an app-state sync key", ErrIncompatible)
		}
		fingerprint, err := proto.Marshal(key.Fingerprint)
		if err != nil {
			return fmt.Errorf("%w: an app-state key fingerprint", ErrIncompatible)
		}
		if err = s.insert("INSERT INTO whatsmeow_app_state_sync_keys(jid,key_id,key_data,timestamp,fingerprint) VALUES(?,?,?,?,?)", s.jid, id, key.KeyData, key.GetTimestamp(), fingerprint); err == nil {
			s.report.AppStateKeys++
		}
		return err
	}); err != nil {
		return err
	}
	if err := s.each("SELECT name,state_data FROM app_state_versions WHERE device_id=?", func(row *sql.Rows) error {
		var name string
		var raw []byte
		if row.Scan(&name, &raw) != nil || !validCollection(name) {
			return fmt.Errorf("%w: an app-state collection", ErrIncompatible)
		}
		version, hash, reset, err := decodeHashState(raw)
		if err != nil {
			return err
		}
		if reset {
			// No row: whatsmeow reads that as a collection never synced
			// and asks for a full snapshot. A version-0 row (zapfast-go's
			// reset) is one its GetAppStateVersion refuses, so the
			// collection would never sync again.
			s.resetCollections[name] = true
			s.report.AppStateReset++
			return nil
		}
		if err = s.insert("INSERT INTO whatsmeow_app_state_version(jid,name,version,hash) VALUES(?,?,?,?)", s.jid, name, version, hash); err == nil {
			s.report.AppStateVersions++
		}
		return err
	}); err != nil {
		return err
	}
	return s.each("SELECT name,version,index_mac,value_mac FROM app_state_mutation_macs WHERE device_id=?", func(row *sql.Rows) error {
		var name string
		var version int64
		var index, value []byte
		if row.Scan(&name, &version, &index, &value) != nil || !validCollection(name) || version < 0 || len(index) != 32 || len(value) != 32 {
			return fmt.Errorf("%w: an app-state mutation MAC", ErrIncompatible)
		}
		if s.resetCollections[name] {
			return nil
		}
		if err := s.insert("INSERT INTO whatsmeow_app_state_mutation_macs(jid,name,version,index_mac,value_mac) VALUES(?,?,?,?,?)", s.jid, name, version, index, value); err != nil {
			return err
		}
		s.report.AppStateMACs++
		return nil
	})
}

func validCollection(name string) bool {
	switch name {
	case "critical_block", "critical_unblock_low", "regular_low", "regular_high", "regular":
		return true
	}
	return false
}

// decodeHashState reads whatsapp-rust's HashStateWire (wire.proto): 1
// version, 2 hash, 3 index_value_map (unsupported), 4 mac_mismatch_fatal,
// 5 bootstrapped. A collection not bootstrapped, or whose MACs diverged,
// is reset for a fresh snapshot.
func decodeHashState(raw []byte) (version uint64, hash []byte, reset bool, err error) {
	bad := fmt.Errorf("%w: an app-state hash", ErrIncompatible)
	seen := make(map[protowire.Number]bool)
	bootstrapped := false
	for len(raw) != 0 {
		number, kind, count := protowire.ConsumeTag(raw)
		if count < 0 || number < 1 || number > 5 || seen[number] && number != 3 {
			return 0, nil, false, bad
		}
		seen[number] = true
		raw = raw[count:]
		if number == 2 || number == 3 {
			if kind != protowire.BytesType {
				return 0, nil, false, bad
			}
			value, n := protowire.ConsumeBytes(raw)
			if n < 0 || number == 2 && len(value) != 128 {
				return 0, nil, false, bad
			}
			if number == 3 {
				// Rust keeps mutation MACs in their own table; a filled
				// legacy map would need a conversion of its own.
				return 0, nil, false, fmt.Errorf("%w: a legacy embedded app-state MAC map", ErrIncompatible)
			}
			hash = value
			raw = raw[n:]
		} else {
			if kind != protowire.VarintType {
				return 0, nil, false, bad
			}
			value, n := protowire.ConsumeVarint(raw)
			if n < 0 || number == 1 && value > math.MaxInt64 || number != 1 && value > 1 {
				return 0, nil, false, bad
			}
			switch number {
			case 1:
				version = value
			case 4:
				reset = value != 0
			case 5:
				bootstrapped = value != 0
			}
			raw = raw[n:]
		}
	}
	if len(hash) != 128 {
		return 0, nil, false, bad
	}
	return version, hash, reset || !bootstrapped, nil
}

func (s *sessionImporter) copyAuxiliary() error {
	// Rust keeps earlier LIDs of a number; whatsmeow keeps one. The most
	// recently learned wins; the others stay in the preserved rows.
	totalMappings, err := s.count("SELECT COUNT(*) FROM lid_pn_mapping WHERE device_id=?")
	if err != nil {
		return err
	}
	if err := s.each(`SELECT lid,phone_number FROM lid_pn_mapping m WHERE device_id=? AND NOT EXISTS (SELECT 1 FROM lid_pn_mapping newer WHERE newer.device_id=m.device_id AND newer.phone_number=m.phone_number AND (newer.updated_at>m.updated_at OR (newer.updated_at=m.updated_at AND newer.lid>m.lid)))`, func(row *sql.Rows) error {
		var lid, phone string
		if row.Scan(&lid, &phone) != nil || !numericUser(lid) || !numericUser(phone) {
			return fmt.Errorf("%w: a LID mapping", ErrIncompatible)
		}
		if err := s.insert("INSERT INTO whatsmeow_lid_map(lid,pn) VALUES(?,?)", lid, phone); err != nil {
			return err
		}
		s.report.LIDMappings++
		return nil
	}); err != nil {
		return err
	}
	s.report.HistoricalLIDAliases = totalMappings - s.report.LIDMappings
	// Zap's re-added created_at is not read: explicit columns only.
	if err := s.each("SELECT chat,sender,msg_id,secret FROM msg_secrets WHERE device_id=?", func(row *sql.Rows) error {
		var chat, sender, id string
		var secret []byte
		if row.Scan(&chat, &sender, &id, &secret) != nil || len(secret) != 32 || len(id) == 0 || len(id) > 256 {
			return fmt.Errorf("%w: a message secret", ErrIncompatible)
		}
		chatJID, chatErr := sessionJID(chat)
		senderJID, senderErr := sessionJID(sender)
		if chatErr != nil || senderErr != nil {
			return fmt.Errorf("%w: a message secret's address", ErrIncompatible)
		}
		if err := s.insert("INSERT INTO whatsmeow_message_secrets(our_jid,chat_jid,sender_jid,message_id,key) VALUES(?,?,?,?,?)", s.jid, chatJID.ToNonAD().String(), senderJID.ToNonAD().String(), id, secret); err != nil {
			return err
		}
		s.report.MessageSecrets++
		return nil
	}); err != nil {
		return err
	}
	return s.each("SELECT jid,token,token_timestamp,sender_timestamp FROM tc_tokens WHERE device_id=?", func(row *sql.Rows) error {
		var user string
		var token []byte
		var timestamp int64
		var sender sql.NullInt64
		if row.Scan(&user, &token, &timestamp, &sender) != nil || len(token) == 0 || len(token) > 4096 || timestamp < 0 || sender.Valid && sender.Int64 < 0 {
			return fmt.Errorf("%w: a privacy token", ErrIncompatible)
		}
		jid, ambiguous, err := s.privacyAddress(user)
		if err != nil {
			return fmt.Errorf("%w: a privacy token's address", ErrIncompatible)
		}
		if ambiguous {
			// Kept in the preserved rows, never sent to a guessed peer.
			s.report.AmbiguousPrivacyTokens++
			return nil
		}
		if err = s.insert("INSERT INTO whatsmeow_privacy_tokens(our_jid,their_jid,token,timestamp,sender_timestamp) VALUES(?,?,?,?,?)", s.jid, jid.ToNonAD().String(), token, timestamp, sender); err == nil {
			s.report.PrivacyTokens++
		}
		return err
	})
}

// privacyAddress is a token's peer: a full JID as 5e72baf writes it, or a
// bare user that older stores wrote, resolved only when it is a LID or a
// number but not both.
func (s *sessionImporter) privacyAddress(user string) (types.JID, bool, error) {
	if strings.Contains(user, "@") {
		jid, err := sessionJID(user)
		return jid, false, err
	}
	if !numericUser(user) {
		return types.EmptyJID, false, errSignalRecord
	}
	var lid, pn bool
	query := `SELECT EXISTS(SELECT 1 FROM lid_pn_mapping WHERE device_id=? AND lid=?) OR EXISTS(SELECT 1 FROM identities WHERE device_id=? AND (address=? OR address LIKE ?))`
	if s.source.QueryRowContext(s.ctx, query, s.deviceID, user, s.deviceID, user+"@lid.0", user+":%@lid.0").Scan(&lid) != nil {
		return types.EmptyJID, false, errSignalRecord
	}
	query = `SELECT EXISTS(SELECT 1 FROM lid_pn_mapping WHERE device_id=? AND phone_number=?) OR EXISTS(SELECT 1 FROM identities WHERE device_id=? AND (address=? OR address LIKE ?))`
	if s.source.QueryRowContext(s.ctx, query, s.deviceID, user, s.deviceID, user+"@c.us.0", user+":%@c.us.0").Scan(&pn) != nil {
		return types.EmptyJID, false, errSignalRecord
	}
	if lid == pn {
		return types.EmptyJID, true, nil
	}
	server := types.DefaultUserServer
	if lid {
		server = types.HiddenUserServer
	}
	return types.NewJID(user, server), false, nil
}

func numericUser(user string) bool {
	if len(user) == 0 || len(user) > 32 {
		return false
	}
	for _, c := range user {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// preserveSourceRows keeps every row of every Rust table as JSON.
func (s *sessionImporter) preserveSourceRows() error {
	if err := s.insert("CREATE TABLE " + preservedTable + "(table_name TEXT NOT NULL,ordinal INTEGER NOT NULL,payload BLOB NOT NULL,PRIMARY KEY(table_name,ordinal))"); err != nil {
		return err
	}
	tables := make([]string, 0, len(rustTables))
	for table := range rustTables {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		where := " WHERE device_id=?"
		if table == "device" {
			where = " WHERE id=?"
		}
		ordinal := int64(0)
		if err := s.each("SELECT * FROM "+table+where+" ORDER BY rowid", func(row *sql.Rows) error {
			columns, err := row.Columns()
			if err != nil || len(columns) > 64 {
				return fmt.Errorf("%w: preserving %s", ErrIncompatible, table)
			}
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = row.Scan(pointers...); err != nil {
				return err
			}
			payload := make(map[string]any, len(columns))
			for i, name := range columns {
				payload[name] = values[i]
			}
			encoded, err := json.Marshal(payload)
			if err != nil || len(encoded) > 8<<20 {
				return fmt.Errorf("%w: preserving %s", ErrIncompatible, table)
			}
			ordinal++
			if err = s.insert("INSERT INTO "+preservedTable+"(table_name,ordinal,payload) VALUES(?,?,?)", table, ordinal, encoded); err == nil {
				s.report.PreservedRows++
			}
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// verifySession reopens the staged store read-only with its key, checks
// every count, loads the device through whatsmeow and reads every Signal
// record back through libsignal.
func verifySession(ctx context.Context, path string, key []byte, report SessionReport, want *wastore.Device) error {
	db, err := sqlite.Open(sqlite.Config{Path: path, Key: key, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("migrate: reopening the converted session store: %w", err)
	}
	defer db.Close()
	for table, n := range map[string]int64{"whatsmeow_device": report.Devices, "whatsmeow_identity_keys": report.Identities, "whatsmeow_sessions": report.Sessions, "whatsmeow_pre_keys": report.PreKeys, "whatsmeow_sender_keys": report.SenderKeys, "whatsmeow_app_state_sync_keys": report.AppStateKeys, "whatsmeow_app_state_version": report.AppStateVersions, "whatsmeow_app_state_mutation_macs": report.AppStateMACs, "whatsmeow_lid_map": report.LIDMappings, "whatsmeow_message_secrets": report.MessageSecrets, "whatsmeow_privacy_tokens": report.PrivacyTokens, preservedTable: report.PreservedRows} {
		var count int64
		if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != n {
			return errors.Join(fmt.Errorf("migrate: the converted %s holds %d rows, not %d", table, count, n), err)
		}
	}
	container := sqlstore.NewWithDB(db, "sqlite3", waLog.Noop)
	device, err := container.GetFirstDevice(ctx)
	if err != nil || device.ID == nil || *device.ID != *want.ID || device.RegistrationID != want.RegistrationID || *device.IdentityKey.Priv != *want.IdentityKey.Priv || device.SignedPreKey.KeyID != want.SignedPreKey.KeyID {
		return errors.Join(errors.New("migrate: the converted device does not read back"), err)
	}
	for _, q := range []string{"SELECT session FROM whatsmeow_sessions", "SELECT sender_key FROM whatsmeow_sender_keys"} {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				break
			}
			if strings.Contains(q, "sender_key") {
				_, err = groupRecord.NewSenderKeyFromBytes(raw, wastore.SignalProtobufSerializer.SenderKeyRecord, wastore.SignalProtobufSerializer.SenderKeyState)
			} else {
				_, err = record.NewSessionFromBytes(raw, wastore.SignalProtobufSerializer.Session, wastore.SignalProtobufSerializer.State)
			}
			if err != nil {
				break
			}
		}
		err = errors.Join(err, rows.Err())
		rows.Close()
		if err != nil {
			return fmt.Errorf("migrate: a converted Signal record does not read back: %w", err)
		}
	}
	return nil
}
