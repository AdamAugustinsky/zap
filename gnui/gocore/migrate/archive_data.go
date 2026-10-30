// From zapfast-go 3104c46 (internal/migration/data.go and polls.go), MIT:
// the table-by-table import. Changed for Zap: group receipts map the Rust
// core's delivery ranks (3 delivered, 4 read, 5 played) to the Go archive's
// receipt levels; outgoing direct messages keep their delivered and read
// times; stars go through the archive's star log; and stars, pins and local
// labels are copied as rows (copyTables).

package migrate

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"zapfast/gocore/store"
)

// handled are the Rust tables imported here; every other table is kept as
// preserved rows only.
var handled = map[string]bool{"chats": true, "messages": true, "chat_removals": true, "message_removals": true, "pending_message_removals": true, "contacts": true, "drafts": true, "local_labels": true, "local_chat_labels": true, "favorites": true, "favorite_changes": true, "poll_votes": true, "polls": true, "poll_history": true, "group_receipts": true, "stars": true, "message_pins": true}

func (imp *importer) run() error {
	rows, err := imp.source.QueryContext(imp.ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return fmt.Errorf("migrate: the Rust archive's schema: %w", err)
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		imp.tables[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !imp.tables["chats"] || !imp.tables["messages"] {
		return errors.New("migrate: the source is not a Rust archive")
	}
	if err = imp.rows("chat_removals", func(row map[string]any) error {
		chat := stringValue(row, "chat")
		through := intValue(row, "through")
		if chat == "" {
			return errors.New("migrate: an invalid chat removal")
		}
		imp.barriers[chat] = through
		imp.report.Barriers++
		if err := imp.destination.ClearChat(chat, through); err != nil {
			return err
		}
		return imp.preserve("chat_removals", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	markRemoved := func(row map[string]any) error {
		chat, id := stringValue(row, "chat"), stringValue(row, "id")
		if chat == "" || id == "" {
			return errors.New("migrate: an invalid message removal")
		}
		key := rowKey(chat, id)
		if !imp.removed[key] {
			imp.report.Tombstones++
			imp.removed[key] = true
		}
		return imp.destination.DeleteLocal(chat, id)
	}
	if err = imp.rows("message_removals", func(row map[string]any) error {
		if err := markRemoved(row); err != nil {
			return err
		}
		return imp.preserve("message_removals", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	if err = imp.rows("pending_message_removals", func(row map[string]any) error {
		if boolValue(row, "confirmed") {
			if err := markRemoved(row); err != nil {
				return err
			}
		}
		imp.report.QuarantinedPending++
		data, _ := json.Marshal(row)
		if _, err := imp.destination.Enqueue("rust-import/message-removal", data); err != nil {
			return err
		}
		return imp.preserve("pending_message_removals", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	if err = imp.rows("chats", func(row map[string]any) error {
		chat := imp.chat(row)
		if chat.ID == "" {
			return errors.New("migrate: a chat without an id")
		}
		if err := imp.destination.UpsertChat(chat); err != nil {
			return err
		}
		imp.chatIDs[chat.ID] = true
		if chat.Kind == "group" {
			imp.groups[chat.ID] = true
		}
		imp.report.Chats++
		return imp.preserve("chats", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	if err = imp.importMessages(); err != nil {
		return err
	}
	for _, step := range []func() error{imp.importContacts, imp.importDrafts, imp.importLabels, imp.importFavorites, imp.importStars, imp.importPollIdentities, imp.importVotes, imp.importReceipts} {
		if err = step(); err != nil {
			return err
		}
	}
	for table := range imp.tables {
		if handled[table] {
			continue
		}
		if err = imp.rows(table, func(row map[string]any) error { return imp.preserve(table, intValue(row, "__rowid"), row) }); err != nil {
			return err
		}
	}
	return metadataJSON(imp.destination, "rust-import/report", imp.report)
}

// direct is an outgoing direct message's receipt times, kept in its row.
type direct struct {
	chat, id            string
	delivered, read     int64
	played              bool
	deliveredOK, readOK bool
}

func (imp *importer) importMessages() error {
	latest := map[string]store.Message{}
	var receipts []direct
	batch := make([]store.Message, 0, 256)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := imp.destination.SaveMessages(batch)
		batch = batch[:0]
		return err
	}
	if err := imp.rows("messages", func(row map[string]any) error {
		imp.report.SourceMessages++
		m, unsupported := imp.message(row)
		if m.ID == "" || m.ChatID == "" {
			return errors.New("migrate: a message without an id")
		}
		blocked := imp.removed[rowKey(m.ChatID, m.ID)]
		if through, ok := imp.barriers[m.ChatID]; ok && m.Timestamp <= through {
			blocked = true
		}
		if blocked {
			imp.report.BlockedMessages++
			return imp.preserve("messages", intValue(row, "__rowid"), row)
		}
		if err := imp.ensureChat(m.ChatID); err != nil {
			return err
		}
		if unsupported {
			imp.report.UnsupportedMessages++
		}
		imp.report.Messages++
		batch = append(batch, m)
		if previous, ok := latest[m.ChatID]; !ok || m.Timestamp > previous.Timestamp || m.Timestamp == previous.Timestamp && m.Order > previous.Order {
			latest[m.ChatID] = m
		}
		if m.FromMe && !imp.groups[m.ChatID] {
			r := direct{chat: m.ChatID, id: m.ID, played: m.Status == "played"}
			r.delivered, r.deliveredOK = intValue(row, "delivered_at"), row["delivered_at"] != nil
			r.read, r.readOK = intValue(row, "read_at"), row["read_at"] != nil
			if r.deliveredOK || r.readOK {
				receipts = append(receipts, r)
			}
		}
		// The protobuf is in the messages table; the rest of the row stays
		// verbatim in the encrypted metadata.
		delete(row, "raw")
		row["raw_in_messages"] = len(m.Raw) > 0
		if err := imp.preserve("messages", intValue(row, "__rowid"), row); err != nil {
			return err
		}
		if len(batch) == cap(batch) {
			return flush()
		}
		return nil
	}); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	for chat, m := range latest {
		if err := imp.destination.UpdateChat(chat, func(c *store.Chat) {
			c.Preview = m.Text
			if m.Timestamp > c.LastActivity {
				c.LastActivity = m.Timestamp
			}
		}); err != nil {
			return err
		}
	}
	// An outgoing direct message's first delivered and read (or played)
	// times, as the peer's receipts.
	for _, r := range receipts {
		read := 2
		if r.played {
			read = 3
		}
		for _, stage := range []struct {
			ok     bool
			status int
			at     int64
		}{{r.deliveredOK, 1, r.delivered}, {r.readOK, read, r.read}} {
			if !stage.ok || stage.at < 0 {
				continue
			}
			if _, _, err := imp.destination.ReceiptAt(r.chat, r.id, r.chat, stage.status, stage.at, false); err != nil {
				return err
			}
		}
		imp.report.DirectReceipts++
	}
	return nil
}

func (imp *importer) importContacts() error {
	type contact struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	contacts := []contact{}
	if err := imp.rows("contacts", func(row map[string]any) error {
		name := stringValue(row, "full_name")
		if name == "" {
			if name = stringValue(row, "push_name"); name != "" {
				name = "~" + name
			}
		}
		contacts = append(contacts, contact{ID: stringValue(row, "id"), Name: name})
		imp.report.Contacts++
		return imp.preserve("contacts", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	return metadataJSON(imp.destination, "imported-contacts", contacts)
}

func (imp *importer) importDrafts() error {
	return imp.rows("drafts", func(row map[string]any) error {
		chat, text := stringValue(row, "chat"), stringValue(row, "text")
		if err := imp.ensureChat(chat); err != nil {
			return err
		}
		if err := imp.destination.SaveDraftMentions(chat, text, nil); err != nil {
			return err
		}
		imp.report.Drafts++
		return imp.preserve("drafts", intValue(row, "__rowid"), row)
	})
}

// importLabels preserves the label rows; copyTables copies them.
func (imp *importer) importLabels() error {
	if err := imp.rows("local_labels", func(row map[string]any) error {
		imp.report.Labels++
		return imp.preserve("local_labels", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	return imp.rows("local_chat_labels", func(row map[string]any) error {
		if err := imp.ensureChat(stringValue(row, "chat")); err != nil {
			return err
		}
		imp.report.ChatLabels++
		return imp.preserve("local_chat_labels", intValue(row, "__rowid"), row)
	})
}

// importStars replays the star log: the archive keeps each row with its
// time and marks the message.
func (imp *importer) importStars() error {
	if err := imp.rows("stars", func(row map[string]any) error {
		chat, id := stringValue(row, "chat"), stringValue(row, "id")
		if chat == "" || id == "" {
			return errors.New("migrate: an invalid star")
		}
		if _, err := imp.destination.SetStar(chat, id, boolValue(row, "starred"), intValue(row, "starred_at"), false); err != nil {
			return err
		}
		imp.report.Stars++
		return imp.preserve("stars", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	return imp.rows("message_pins", func(row map[string]any) error {
		imp.report.Pins++
		return imp.preserve("message_pins", intValue(row, "__rowid"), row)
	})
}

func (imp *importer) importFavorites() error {
	type favorite struct {
		Chat     string
		Position int
	}
	favorites := []favorite{}
	if err := imp.rows("favorites", func(row map[string]any) error {
		chat := stringValue(row, "chat")
		if err := imp.ensureChat(chat); err != nil {
			return err
		}
		position := int(intValue(row, "position"))
		favorites = append(favorites, favorite{chat, position})
		if err := imp.destination.UpdateChat(chat, func(c *store.Chat) { c.Favorite = true; c.FavoritePosition = position }); err != nil {
			return err
		}
		imp.report.Favorites++
		return imp.preserve("favorites", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	// The order is kept as a local view; the phone's list is fetched again
	// before any favorites change is sent.
	sort.SliceStable(favorites, func(i, j int) bool { return favorites[i].Position < favorites[j].Position })
	list := make([]string, 0, len(favorites))
	for _, f := range favorites {
		list = append(list, f.Chat)
	}
	if err := metadataJSON(imp.destination, "favorites", struct {
		List      []string
		Timestamp int64
		Known     bool
	}{List: list}); err != nil {
		return err
	}
	return imp.rows("favorite_changes", func(row map[string]any) error {
		imp.report.QuarantinedPending++
		data, _ := json.Marshal(row)
		if _, err := imp.destination.Enqueue("rust-import/favorite-change", data); err != nil {
			return err
		}
		return imp.preserve("favorite_changes", intValue(row, "__rowid"), row)
	})
}

func (imp *importer) importPollIdentities() error {
	return imp.rows("polls", func(row map[string]any) error {
		chat, id, creator := stringValue(row, "chat"), stringValue(row, "id"), stringValue(row, "creator")
		digest := sha256.Sum256([]byte(rowKey(chat, id)))
		key := hex.EncodeToString(digest[:])
		if creator != "" {
			if err := metadataJSON(imp.destination, "poll-identity/"+key, struct{ Chat, Sender string }{chat, creator}); err != nil {
				return err
			}
		}
		// Rust keeps a poll's secret apart; Go reads it from the protobuf's
		// MessageContextInfo. The original protobuf is kept before the
		// secret is added to a copy.
		secret := bytesValue(row, "secret")
		m, err := imp.destination.Message(chat, id)
		if err == nil && m.Kind == "poll" && len(secret) == 32 && len(m.Raw) > 0 {
			p := &waE2E.Message{}
			if proto.Unmarshal(m.Raw, p) == nil && (p.GetPollCreationMessage() != nil || p.GetPollCreationMessageV2() != nil || p.GetPollCreationMessageV3() != nil) && len(p.GetMessageContextInfo().GetMessageSecret()) != 32 {
				if err := imp.destination.SetMetadata("rust-import/original-protobuf/"+key, m.Raw); err != nil {
					return err
				}
				if p.MessageContextInfo == nil {
					p.MessageContextInfo = &waE2E.MessageContextInfo{}
				}
				p.MessageContextInfo.MessageSecret = append([]byte(nil), secret...)
				updated, err := proto.Marshal(p)
				if err != nil {
					return err
				}
				if err = imp.destination.UpdateMessage(chat, id, func(m *store.Message) { m.Raw = updated }); err != nil {
					return err
				}
			}
		}
		return imp.preserve("polls", intValue(row, "__rowid"), row)
	})
}

func (imp *importer) importVotes() error {
	if err := imp.rows("poll_history", func(row map[string]any) error {
		chat, id := stringValue(row, "chat"), stringValue(row, "id")
		if _, err := imp.destination.Message(chat, id); err == nil {
			if err = imp.destination.UpdateMessage(chat, id, func(m *store.Message) { m.PollHistoryComplete = true }); err != nil {
				return err
			}
		}
		return imp.preserve("poll_history", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	if err := imp.rows("poll_votes", func(row map[string]any) error {
		chat, parent := stringValue(row, "chat"), stringValue(row, "poll")
		names := imp.polls[rowKey(chat, parent)]
		quarantine := func(kind string) error {
			imp.report.QuarantinedPending++
			data, _ := json.Marshal(row)
			_, err := imp.destination.Enqueue(kind, data)
			return err
		}
		if anchor, err := imp.destination.Message(chat, parent); err != nil || anchor.Kind != "poll" {
			if err := quarantine("rust-import/orphan-poll-vote"); err != nil {
				return err
			}
			return imp.preserve("poll_votes", intValue(row, "__rowid"), row)
		}
		var choices []int
		if text := stringValue(row, "choices"); text != "" && json.Unmarshal([]byte(text), &choices) == nil && len(names) > 0 {
			hashes := make([][]byte, 0, len(choices))
			for _, choice := range choices {
				if choice < 0 || choice >= len(names) {
					return errors.New("migrate: a poll vote's choice is out of range")
				}
				hash := sha256.Sum256([]byte(names[choice]))
				hashes = append(hashes, hash[:])
			}
			voter := stringValue(row, "voter")
			if voter == "" {
				voter = stringValue(row, "sender")
			}
			if err := imp.destination.SaveVote(chat, parent, store.Vote{Sender: voter, Timestamp: intValue(row, "at"), ID: stringValue(row, "update_id"), Options: hashes}); err != nil {
				return err
			}
			imp.report.Votes++
		} else if len(bytesValue(row, "encrypted")) > 0 {
			if err := quarantine("rust-import/encrypted-poll-vote"); err != nil {
				return err
			}
		}
		return imp.preserve("poll_votes", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	for key, names := range imp.polls {
		chat, parent, ok := strings.Cut(key, "\x00")
		if !ok {
			continue
		}
		votes, err := imp.destination.Votes(chat, parent)
		if err != nil {
			return err
		}
		if len(votes) == 0 {
			continue
		}
		if err = imp.destination.UpdateMessage(chat, parent, func(m *store.Message) {
			counts := make([]int, len(names))
			for _, vote := range votes {
				for _, choice := range vote.Options {
					for i, name := range names {
						if hash := sha256.Sum256([]byte(name)); string(hash[:]) == string(choice) && i < len(m.Poll) {
							counts[i]++
							m.Poll[i].Voters = append(m.Poll[i].Voters, vote.Sender)
							m.Poll[i].VoterTimes = append(m.Poll[i].VoterTimes, vote.Timestamp/1000)
						}
					}
				}
			}
			for i, count := range counts {
				if i < len(m.Poll) {
					m.Poll[i].Votes = max(m.Poll[i].Votes, count)
				}
			}
		}); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

// receiptLevel is the Go archive's receipt level (1 delivered, 2 read, 3
// played) of a Rust delivery rank (src/archive.rs status_rank).
func receiptLevel(rank int64) int {
	switch rank {
	case 3:
		return 1
	case 4:
		return 2
	case 5:
		return 3
	}
	return 0
}

func (imp *importer) importReceipts() error {
	type receipt struct {
		Chat, ID, Recipient string
		Level               int
		Expected            bool
		Times               store.MessageReceipt
	}
	byMessage := map[string][]receipt{}
	var order []string
	if err := imp.rows("group_receipts", func(row map[string]any) error {
		r := receipt{Chat: stringValue(row, "chat"), ID: stringValue(row, "id"), Recipient: stringValue(row, "recipient"), Level: receiptLevel(intValue(row, "status")), Expected: boolValue(row, "expected")}
		r.Times = store.MessageReceipt{Sender: r.Recipient, DeliveredAt: intValue(row, "delivered_at"), ReadAt: intValue(row, "read_at"), PlayedAt: intValue(row, "played_at")}
		key := rowKey(r.Chat, r.ID)
		if _, seen := byMessage[key]; !seen {
			order = append(order, key)
		}
		byMessage[key] = append(byMessage[key], r)
		return imp.preserve("group_receipts", intValue(row, "__rowid"), row)
	}); err != nil {
		return err
	}
	for _, key := range order {
		rows := byMessage[key]
		var audience []string
		for _, r := range rows {
			if r.Expected {
				audience = append(audience, r.Recipient)
			}
		}
		if _, err := imp.destination.Message(rows[0].Chat, rows[0].ID); err != nil || len(audience) == 0 {
			// A receipt for a message gone, or with no snapshot of who
			// should answer, stays preserved only.
			continue
		}
		if err := imp.destination.SetAudience(rows[0].Chat, rows[0].ID, audience); err != nil {
			return err
		}
		for _, r := range rows {
			if r.Level > 0 {
				if _, _, err := imp.destination.Receipt(r.Chat, r.ID, r.Recipient, r.Level); err != nil {
					return err
				}
			}
			if err := imp.destination.RestoreReceiptTimes(r.Chat, r.ID, r.Times); err != nil {
				return err
			}
			if r.Expected {
				imp.report.GroupReceipts++
			}
		}
	}
	return nil
}

// ensureChat keeps a chat that a row refers to but the chats table lacks.
func (imp *importer) ensureChat(chat string) error {
	if chat == "" {
		return errors.New("migrate: a row refers to no chat")
	}
	if imp.chatIDs[chat] {
		return nil
	}
	kind := "direct"
	if strings.HasSuffix(chat, "@g.us") {
		kind = "group"
	}
	if strings.HasSuffix(chat, "@newsletter") {
		kind = "channel"
	}
	if err := imp.destination.UpsertChat(store.Chat{ID: chat, Name: chat, Kind: kind}); err != nil {
		return err
	}
	imp.chatIDs[chat] = true
	imp.report.Chats++
	return nil
}

// copyTables copies the Rust archive's message pins and local labels into
// the Go archive's tables of the same shape, and puts each chat's labels in
// its payload, as the archive's own label calls do.
func (imp *importer) copyTables(staged string, key []byte) error {
	db, err := store.OpenDatabase(staged, key)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(imp.ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	copies := []struct{ table, columns string }{
		{"local_labels", "id,name,color,created_at"},
		{"local_chat_labels", "chat,label"},
		{"message_pins", "chat,id,pinned,pinned_at,expires_at,by_me,sender"},
	}
	for _, c := range copies {
		names := strings.Split(c.columns, ",")
		insert := "INSERT INTO " + c.table + "(" + c.columns + ") VALUES(?" + strings.Repeat(",?", len(names)-1) + ")"
		if err = imp.rows(c.table, func(row map[string]any) error {
			values := make([]any, len(names))
			for i, name := range names {
				v, ok := row[name]
				if !ok {
					return fmt.Errorf("migrate: the Rust archive's %s has no %s", c.table, name)
				}
				values[i] = v
			}
			_, err := tx.ExecContext(imp.ctx, insert, values...)
			return err
		}); err != nil {
			return err
		}
	}
	chats, err := txStrings(tx, "SELECT DISTINCT chat FROM local_chat_labels")
	if err != nil {
		return err
	}
	for _, chat := range chats {
		var data []byte
		if err = tx.QueryRowContext(imp.ctx, "SELECT payload FROM chats WHERE id=?", chat).Scan(&data); err != nil {
			return err
		}
		var c store.Chat
		if err = json.Unmarshal(data, &c); err != nil {
			return err
		}
		if c.Labels, err = txStrings(tx, "SELECT c.label FROM local_chat_labels c JOIN local_labels l ON l.id=c.label WHERE c.chat=? ORDER BY l.created_at,l.rowid", chat); err != nil {
			return err
		}
		if data, err = json.Marshal(c); err != nil {
			return err
		}
		if _, err = tx.ExecContext(imp.ctx, "UPDATE chats SET payload=? WHERE id=?", data, chat); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	_, err = db.ExecContext(imp.ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

func txStrings(tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(query, args...)
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
