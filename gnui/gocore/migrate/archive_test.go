package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"zapfast/gocore/store"
	"zapfast/gocore/store/sqlite"
)

const (
	bea   = "15550002002@s.whatsapp.net"
	cy    = "100000000003003@lid"
	me    = "15550001001@s.whatsapp.net"
	group = "120363000000000001@g.us"
)

// sourceRows reads a table of the Rust fixture archive as strings, for
// row-for-row comparison.
func sourceRows(t *testing.T, db *sql.DB, query string) [][]string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	var out [][]string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(values))
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[i] = fmt.Sprint(v)
		}
		out = append(out, row)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func imported(t *testing.T) (*store.Archive, ArchiveReport, *sql.DB, *sql.DB) {
	t.Helper()
	needSQLCipher(t)
	f := testFixtures(t)
	key := f.archiveKey(t)
	dir := t.TempDir()
	source := fixtureCopy(t, "archive.db", dir, "archive.db")
	before := tree(t, dir)
	target := filepath.Join(dir, store.ArchiveFile)
	report, err := ImportArchive(context.Background(), source, key, key, target)
	if err != nil {
		t.Fatal(err)
	}
	after := tree(t, dir)
	delete(after, store.ArchiveFile)
	if !sameTree(before, after) {
		t.Fatalf("the import changed or left files: %v", after)
	}
	rust, err := sqlite.Open(sqlite.Config{Path: source, Key: key, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rust.Close() })
	raw, err := sqlite.Open(sqlite.Config{Path: target, Key: key, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	a, err := store.OpenArchive(target, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, report, rust, raw
}

func TestImportArchiveRowForRow(t *testing.T) {
	a, r, rust, goDB := imported(t)

	// Every Rust message is in the Go archive with its identity, time,
	// side, raw protobuf and a kind for its content.
	kinds := map[string]string{
		"3EB0TEXT01": "text", "3EB0TEXT02": "text", "3EB0IMAGE1": "image", "3EB0VIDEO1": "ptv",
		"3EB0GIF001": "video", "3EB0VOICE1": "voice", "3EB0AUDIO1": "audio", "3EB0DOC001": "document",
		"3EB0STICK1": "sticker", "3EB0PACK01": "unsupported", "3EB0LOC001": "location",
		"3EB0LIVE01": "live-location", "3EB0VCARD1": "unsupported", "3EB0REVOK1": "text",
		"3EB0UNSUP1": "unsupported", "3EB0ONCE01": "view-once", "zapfast-call-0001": "call",
		"3EB0CALL02": "call", "3EB0INTER1": "text", "3EB0GROUP1": "text", "3EB0POLL01": "poll",
		"3EB0GROUP2": "text", "3EB0NEWS01": "text", "3EB0LEFT01": "text", "3EB0GONE02": "text",
		"3EB0DELME2": "text",
	}
	src := sourceRows(t, rust, "SELECT chat,id,sender,from_me,timestamp,hex(coalesce(raw,'')) FROM messages ORDER BY chat,id")
	if int64(len(src)) != r.SourceMessages || r.Messages != r.SourceMessages-r.BlockedMessages || len(src) != len(kinds) {
		t.Fatalf("%d source messages, report %+v", len(src), r)
	}
	for _, row := range src {
		m, err := a.Message(row[0], row[1])
		if err != nil {
			t.Fatalf("%s/%s: %v", row[0], row[1], err)
		}
		if m.Sender != row[2] || fmt.Sprint(map[bool]int{false: 0, true: 1}[m.FromMe]) != row[3] || fmt.Sprint(m.Timestamp) != row[4] || fmt.Sprintf("%X", m.Raw) != row[5] && m.Kind != "poll" {
			t.Fatalf("%s: %+v vs %v", row[1], m, row)
		}
		if kinds[m.ID] != m.Kind {
			t.Fatalf("%s is %q, want %q", m.ID, m.Kind, kinds[m.ID])
		}
	}
	if r.Chats != 6 || r.Labels != 2 || r.ChatLabels != 3 || r.Stars != 3 || r.Pins != 3 || r.Drafts != 2 || r.Favorites != 3 || r.Votes != 2 || r.GroupReceipts != 2 || r.Contacts != 2 || r.Barriers != 1 || r.Tombstones != 1 {
		t.Fatalf("report %+v", r)
	}
	// The tables the Go archive shares with the Rust one, row for row.
	for _, q := range []string{
		"SELECT chat,id,starred,starred_at FROM stars ORDER BY chat,id",
		"SELECT chat,id,pinned,pinned_at,expires_at,by_me,sender FROM message_pins ORDER BY chat,id",
		"SELECT id,name,color,created_at FROM local_labels ORDER BY id",
		"SELECT chat,label FROM local_chat_labels ORDER BY chat,label",
	} {
		if s, g := sourceRows(t, rust, q), sourceRows(t, goDB, q); !slices.EqualFunc(s, g, slices.Equal) || len(s) == 0 {
			t.Fatalf("%s\nrust %v\n  go %v", q, s, g)
		}
	}

	// Chats.
	chats, err := a.Chats()
	if err != nil || len(chats) != 6 {
		t.Fatalf("%d chats: %v", len(chats), err)
	}
	byID := map[string]store.Chat{}
	for _, c := range chats {
		byID[c.ID] = c
	}
	f := testFixtures(t)
	if c := byID[bea]; !c.Pinned || c.MutedUntil != math.MaxInt64 || !c.Favorite || !slices.Equal(c.Labels, f.Archive.Labels) {
		t.Fatalf("bea %+v", c)
	}
	if c := byID[cy]; !c.Archived || !c.Locked || !c.MarkedUnread || !c.Favorite {
		t.Fatalf("cy %+v", c)
	}
	if c := byID[group]; c.Kind != "group" || !c.InfoLocked || !c.Admin || c.EphemeralSeconds != 86400 || c.Unread != 2 || !c.HistoryStart || len(c.Participants) != 3 || !slices.Equal(c.Labels, f.Archive.Labels[:1]) {
		t.Fatalf("group %+v", c)
	}
	if c := byID["120363000000000099@newsletter"]; c.Kind != "channel" || !c.ReadOnly {
		t.Fatalf("channel %+v", c)
	}
	if c := byID["120363000000000002@g.us"]; !c.Left || !c.InfoLocked {
		t.Fatalf("left group %+v", c)
	}

	// Message details.
	text, _ := a.Message(bea, "3EB0TEXT01")
	if text.LinkPreview == nil || text.LinkPreview.Title != "Example page" || len(text.Mentions) != 1 || !text.Starred || text.Order != 10 {
		t.Fatalf("text %+v", text)
	}
	reply, _ := a.Message(bea, "3EB0TEXT02")
	if reply.Quote == nil || reply.Quote.ID != "3EB0TEXT01" || len(reply.Reactions) != 1 || !reply.Forwarded || !reply.Edited {
		t.Fatalf("reply %+v", reply)
	}
	voice, _ := a.Message(bea, "3EB0VOICE1")
	if voice.Media == nil || !voice.Media.PTT || len(voice.Media.Waveform) != 64 || voice.Media.Seconds != 4 {
		t.Fatalf("voice %+v", voice.Media)
	}
	image, _ := a.Message(bea, "3EB0IMAGE1")
	if image.Media == nil || image.Text != "Synthetic photo" || image.Media.Width != 640 || len(image.Media.Thumbnail) == 0 {
		t.Fatalf("image %+v", image)
	}
	live, _ := a.Message(bea, "3EB0LIVE01")
	if live.LiveLocation == nil || live.LiveLocation.Sequence != 3 || *live.LiveLocation.HeadingDeg != 90 {
		t.Fatalf("live %+v", live.LiveLocation)
	}
	inter, _ := a.Message(cy, "3EB0INTER1")
	if len(inter.Actions) != 2 || inter.Actions[0].Kind != "url" || inter.Actions[1].Kind != "copy" || inter.Actions[1].Value != "SYN123" {
		t.Fatalf("interactive %+v", inter.Actions)
	}

	// The call log, the shared links, mentions, drafts, receipts, polls.
	calls, err := a.CallLog(0)
	if err != nil || len(calls) != 2 || calls[0].Call == nil || calls[0].Call.Seconds != 95 || calls[1].Call.Status != "missed" || !calls[1].Call.Video {
		t.Fatalf("calls %+v: %v", calls, err)
	}
	// The photo; the round video note (ptv) and the GIF are not shared media.
	shared, err := a.Shared(bea, 50)
	if err != nil || len(shared.Links) != 2 || len(shared.Docs) != 1 || len(shared.Media) != 1 {
		t.Fatalf("shared links %d docs %d media %d: %v", len(shared.Links), len(shared.Docs), len(shared.Media), err)
	}
	if id, err := a.UnreadMention(group, 2, []string{me}); err != nil || id != "" {
		// The group's two newest are a poll and a reply, neither naming us.
		t.Fatalf("unread mention %q: %v", id, err)
	}
	var mentions int
	if err = goDB.QueryRow("SELECT count(*) FROM message_mentions").Scan(&mentions); err != nil || mentions != 3 {
		t.Fatalf("%d mentions: %v", mentions, err)
	}
	if d, err := a.Draft(bea); err != nil || d != "Synthetic draft to Bea" {
		t.Fatalf("draft %q: %v", d, err)
	}
	details, err := a.ReceiptDetails(group, "3EB0GROUP1")
	if err != nil || len(details) != 2 {
		t.Fatalf("receipts %+v: %v", details, err)
	}
	for _, d := range details {
		if d.Sender == bea && d.ReadAt != 1_760_000_045 || d.Sender == cy && (d.DeliveredAt != 1_760_000_046 || d.ReadAt != 0) {
			t.Fatalf("receipt %+v", d)
		}
	}
	direct, err := a.ReceiptDetails(bea, "3EB0TEXT01")
	if err != nil || len(direct) != 1 || direct[0].DeliveredAt != 1_760_000_002 || direct[0].ReadAt != 1_760_000_003 {
		t.Fatalf("direct receipts %+v: %v", direct, err)
	}
	poll, _ := a.Message(group, "3EB0POLL01")
	var p waE2E.Message
	if err = proto.Unmarshal(poll.Raw, &p); err != nil || !bytes.Equal(p.GetMessageContextInfo().GetMessageSecret(), bytes.Repeat([]byte{0x50}, 32)) || !poll.PollHistoryComplete || !poll.Starred {
		t.Fatalf("poll %+v: %v", poll, err)
	}
	if votes, err := a.Votes(group, "3EB0POLL01"); err != nil || len(votes) != 2 {
		t.Fatalf("votes %v: %v", votes, err)
	}
	// Removals: the cleared chat keeps only what came after its barrier,
	// and the waiting removal is queued.
	if _, err := a.Message("15550004004@s.whatsapp.net", "3EB0GONE02"); err != nil {
		t.Fatal(err)
	}
	if pending, err := a.PendingKind("rust-import/message-removal", 10); err != nil || len(pending) != 1 {
		t.Fatalf("pending removals %v: %v", pending, err)
	}
	// Every source row is preserved, encrypted.
	var total int64
	for _, row := range sourceRows(t, rust, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'") {
		var n int64
		if err := rust.QueryRow(`SELECT count(*) FROM "` + row[0] + `"`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		total += n
	}
	if r.PreservedRows != total {
		t.Fatalf("preserved %d of %d rows", r.PreservedRows, total)
	}
}

// Rust's PhoneOnly told view-once media and live locations apart by flag;
// the Go engine has a kind for each (zf:src/model.rs:746).
func TestPhoneOnlyKeepsWhatItWas(t *testing.T) {
	imp := &importer{polls: map[string][]string{}}
	for _, c := range []struct {
		content, kind, text string
		live                bool
	}{
		{`{"kind":"phoneonly","view_once":true,"once":"voice"}`, "view-once", "voice", false},
		{`{"kind":"phoneonly","view_once":true}`, "view-once", "", false},
		{`{"kind":"phoneonly","view_once":false,"live_location":true}`, "live-location", "", true},
		{`{"kind":"phoneonly","view_once":false}`, "phone-only", "Open this message on your phone", false},
	} {
		m, _ := imp.message(map[string]any{"id": "m", "chat": bea, "timestamp": int64(100), "content": c.content})
		if m.Kind != c.kind || m.Text != c.text || (m.LiveLocation != nil) != c.live {
			t.Fatalf("%s: %+v", c.content, m)
		}
		if c.live && (m.LiveLocation.Available || m.LiveLocation.UpdatedAt != 100) {
			t.Fatalf("live location %+v", m.LiveLocation)
		}
	}
}

func TestImportArchiveFailsClosed(t *testing.T) {
	needSQLCipher(t)
	f := testFixtures(t)
	key := f.archiveKey(t)
	t.Run("a wrong key", func(t *testing.T) {
		dir := t.TempDir()
		source := fixtureCopy(t, "archive.db", dir, "archive.db")
		before := tree(t, dir)
		wrong := bytes.Repeat([]byte{0x42}, 32)
		_, err := ImportArchive(context.Background(), source, wrong, key, filepath.Join(dir, store.ArchiveFile))
		if !errors.Is(err, sqlite.ErrWrongKey) {
			t.Fatalf("got %v", err)
		}
		if after := tree(t, dir); !sameTree(before, after) {
			t.Fatalf("files changed: %v", after)
		}
	})
	t.Run("not an archive", func(t *testing.T) {
		dir := t.TempDir()
		source := fixtureCopy(t, "session.db", dir, "archive.db")
		before := tree(t, dir)
		if _, err := ImportArchive(context.Background(), source, key, key, filepath.Join(dir, store.ArchiveFile)); !errors.Is(err, sqlite.ErrPlaintext) {
			t.Fatalf("got %v", err)
		}
		if after := tree(t, dir); !sameTree(before, after) {
			t.Fatalf("files changed: %v", after)
		}
	})
}
