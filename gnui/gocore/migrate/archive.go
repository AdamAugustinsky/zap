// From zapfast-go 3104c46 (internal/migration/import.go), MIT: its archive
// Import, ported to the purego SQLCipher driver and Zap's Go archive
// (gocore/store). Changed for Zap: media is adopted where the Rust core
// keeps it (the account's cache) rather than copied; the tables the Go
// archive added (stars, message pins, local labels) are copied row for row;
// and the verification counts them too.

package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"zapfast/gocore/store"
	"zapfast/gocore/store/sqlite"
)

const maxRowBytes = 64 << 20

// ArchiveReport holds counts only.
type ArchiveReport struct {
	SourceMessages      int64 `json:"source_messages"`
	Messages            int64 `json:"messages"`
	Chats               int64 `json:"chats"`
	Contacts            int64 `json:"contacts"`
	Drafts              int64 `json:"drafts"`
	Labels              int64 `json:"labels"`
	ChatLabels          int64 `json:"chat_labels"`
	Stars               int64 `json:"stars"`
	Pins                int64 `json:"pins"`
	Favorites           int64 `json:"favorites"`
	Votes               int64 `json:"votes"`
	GroupReceipts       int64 `json:"group_receipts"`
	DirectReceipts      int64 `json:"direct_receipts"`
	Tombstones          int64 `json:"tombstones"`
	Barriers            int64 `json:"barriers"`
	BlockedMessages     int64 `json:"blocked_messages"`
	UnsupportedMessages int64 `json:"unsupported_messages"`
	PreservedRows       int64 `json:"preserved_rows"`
	QuarantinedPending  int64 `json:"quarantined_pending"`
	// MediaPaths are attachments whose downloaded file is adopted where it
	// is; MissingMedia ones whose file is gone (they download again).
	MediaPaths   int64 `json:"media_paths"`
	MissingMedia int64 `json:"missing_media"`
}

type importer struct {
	ctx         context.Context
	source      *sql.Tx
	destination *store.Archive
	report      ArchiveReport
	tables      map[string]bool
	barriers    map[string]int64
	removed     map[string]bool
	polls       map[string][]string
	chatIDs     map[string]bool
	// groups are the chats the Rust archive calls groups.
	groups map[string]bool
}

// ImportArchive imports the Rust core's archive at source, opened (from a
// snapshot, read-only) with sourceKey, into a new Go archive at target
// under targetKey. It stages beside target, verifies the staged archive
// (integrity, counts, every payload read back) and publishes it without
// replacing anything. The source is never written. A wrong key fails with
// sqlite.ErrWrongKey.
func ImportArchive(ctx context.Context, source string, sourceKey, targetKey []byte, target string) (report ArchiveReport, err error) {
	if len(sourceKey) != 32 || len(targetKey) != 32 {
		return report, store.ErrKey
	}
	if err = ctx.Err(); err != nil {
		return report, err
	}
	if source, err = filepath.Abs(source); err != nil {
		return report, err
	}
	if target, err = filepath.Abs(target); err != nil || target == source {
		return report, errors.New("migrate: invalid archive destination")
	}
	if found, err := exists(target); err != nil {
		return report, err
	} else if found {
		return report, ErrDestinationExists
	}
	if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return report, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".zap-archive-import-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(stage)
	staged := filepath.Join(stage, store.ArchiveFile)
	if report, err = importArchiveInto(ctx, source, sourceKey, targetKey, staged); err != nil {
		return report, err
	}
	return report, publish(staged, target)
}

// importArchiveInto imports source into a new archive at staged and
// verifies it there.
func importArchiveInto(ctx context.Context, source string, sourceKey, targetKey []byte, staged string) (report ArchiveReport, err error) {
	snapshot := staged + ".rust-source"
	if err = copySourceSnapshot(ctx, source, snapshot); err != nil {
		return report, err
	}
	defer removeDB(snapshot)
	src, err := openSource(snapshot, sourceKey)
	if err != nil {
		return report, err
	}
	defer src.Close()
	read, err := src.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return report, err
	}
	defer read.Rollback()
	destination, err := store.OpenArchive(staged, targetKey)
	if err != nil {
		return report, fmt.Errorf("migrate: creating the Go archive: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			destination.Close()
		}
	}()
	imp := &importer{ctx: ctx, source: read, destination: destination, tables: map[string]bool{}, barriers: map[string]int64{}, removed: map[string]bool{}, polls: map[string][]string{}, chatIDs: map[string]bool{}, groups: map[string]bool{}}
	if err = imp.run(); err != nil {
		return imp.report, err
	}
	if err = destination.Checkpoint(); err != nil {
		return imp.report, err
	}
	closed = true
	if err = destination.Close(); err != nil {
		return imp.report, err
	}
	// The Go archive's own copies of the Rust tables it shares, row for
	// row, written beside the archive's API.
	if err = imp.copyTables(staged, targetKey); err != nil {
		return imp.report, err
	}
	if err = verifyArchive(ctx, staged, targetKey, imp.report); err != nil {
		return imp.report, err
	}
	return imp.report, ctx.Err()
}

// openSource opens a SQLCipher snapshot for reading only, with one
// connection.
func openSource(path string, key []byte) (*sql.DB, error) {
	db, err := sqlite.Open(sqlite.Config{Path: path, Key: key, ReadOnly: true, Pragmas: []string{"PRAGMA query_only = ON"}})
	if err != nil {
		return nil, fmt.Errorf("migrate: opening the Rust archive: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// verifyArchive reopens the staged archive read-only with its key, runs an
// integrity check, checks the counts and reads every payload back.
func verifyArchive(ctx context.Context, path string, key []byte, want ArchiveReport) error {
	db, err := sqlite.Open(sqlite.Config{Path: path, Key: key, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("migrate: reopening the Go archive: %w", err)
	}
	defer db.Close()
	var integrity string
	if err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return errors.Join(errors.New("migrate: the Go archive failed its integrity check"), err)
	}
	for table, n := range map[string]int64{"messages": want.Messages, "chats": want.Chats, "stars": want.Stars, "message_pins": want.Pins, "local_labels": want.Labels, "local_chat_labels": want.ChatLabels} {
		var count int64
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != n {
			return errors.Join(fmt.Errorf("migrate: the Go archive's %s holds %d rows, not %d", table, count, n), err)
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT chat,id,payload FROM messages ORDER BY chat,id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var chat, id string
		var payload []byte
		if err = rows.Scan(&chat, &id, &payload); err != nil {
			return err
		}
		var m store.Message
		if json.Unmarshal(payload, &m) != nil || m.ChatID != chat || m.ID != id {
			return errors.New("migrate: a Go archive message does not read back")
		}
	}
	return rows.Err()
}

func rowKey(chat, id string) string { return chat + "\x00" + id }

func stringValue(row map[string]any, key string) string {
	switch value := row[key].(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return ""
	}
}

func intValue(row map[string]any, key string) int64 {
	switch value := row[key].(type) {
	case int64:
		return value
	case float64:
		return int64(value)
	case int:
		return int64(value)
	default:
		return 0
	}
}

func bytesValue(row map[string]any, key string) []byte {
	switch value := row[key].(type) {
	case []byte:
		return value
	case string:
		return []byte(value)
	default:
		return nil
	}
}

func boolValue(row map[string]any, key string) bool { return intValue(row, key) != 0 }

func metadataJSON(s *store.Archive, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.SetMetadata(key, data)
}

// preserve keeps a source row verbatim, encrypted, in the Go archive's
// metadata, so nothing the import leaves out is lost.
func (imp *importer) preserve(table string, index int64, row map[string]any) error {
	imp.report.PreservedRows++
	return metadataJSON(imp.destination, fmt.Sprintf("rust-import/%s/%d", table, index), row)
}

// rows calls fn with every row of a source table, in rowid order.
func (imp *importer) rows(table string, fn func(map[string]any) error) error {
	if !imp.tables[table] {
		return nil
	}
	rows, err := imp.source.QueryContext(imp.ctx, `SELECT rowid AS __rowid,* FROM "`+strings.ReplaceAll(table, `"`, `""`)+`" ORDER BY rowid`)
	if err != nil {
		return fmt.Errorf("migrate: reading the Rust archive's %s: %w", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		if err = imp.ctx.Err(); err != nil {
			return err
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			return err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			switch v := values[i].(type) {
			case []byte:
				if len(v) > maxRowBytes {
					return errors.New("migrate: a Rust archive row exceeds the import limit")
				}
				values[i] = append([]byte(nil), v...)
			case string:
				if len(v) > maxRowBytes {
					return errors.New("migrate: a Rust archive row exceeds the import limit")
				}
			}
			row[column] = values[i]
		}
		if err = fn(row); err != nil {
			return err
		}
	}
	return rows.Err()
}

// adoptMedia keeps an attachment's downloaded file where the Rust core
// left it (the account's cache, which the Go backend uses too).
func (imp *importer) adoptMedia(path string) string {
	if path == "" {
		return ""
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() || !filepath.IsAbs(path) {
		imp.report.MissingMedia++
		return ""
	}
	imp.report.MediaPaths++
	return path
}
