// From zapfast-go 3104c46 (internal/archive/store_test.go), MIT: the
// archive's tests, run on the purego driver. Zap's own are in zap_test.go.

package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"zapfast/gocore/store/sqlite"
)

// needSQLCipher skips a test when libsqlcipher.dylib is not built.
func needSQLCipher(t testing.TB) {
	t.Helper()
	if _, err := sqlite.Load(); err != nil {
		if errors.Is(err, sqlite.ErrNoLibrary) {
			t.Skipf("%v; build it with packaging/macos/sqlcipher/build.sh", err)
		}
		t.Fatal(err)
	}
}

func testStore(t *testing.T) (*Archive, string, []byte) {
	t.Helper()
	needSQLCipher(t)
	path := filepath.Join(t.TempDir(), "archive.db")
	key := bytes.Repeat([]byte{0x37}, 32)
	s, err := OpenArchive(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path, key
}
func TestEncryptedArchiveWrongKeyAndAccountIsolation(t *testing.T) {
	s, path, key := testStore(t)
	secret := "synthetic sensitive message 90781"
	if err := s.SaveMessages([]Message{{ID: "same-id", ChatID: "synthetic@lid", Timestamp: 1, Text: secret, Raw: []byte("synthetic raw attachment key")}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDraft("synthetic@lid", secret); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		b, err := os.ReadFile(name)
		if err == nil && (bytes.Contains(b, []byte(secret)) || bytes.Contains(b, []byte("synthetic raw attachment key")) || bytes.HasPrefix(b, []byte("SQLite format 3"))) {
			t.Fatalf("plaintext in %s", filepath.Base(name))
		}
	}
	wrong := bytes.Repeat([]byte{0x42}, 32)
	if other, err := OpenArchive(path, wrong); err == nil {
		other.Close()
		t.Fatal("wrong key accepted")
	}
	second, err := OpenArchive(filepath.Join(t.TempDir(), "archive.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	n, err := second.Count()
	if err != nil || n != 0 {
		t.Fatalf("account isolation failed: %d %v", n, err)
	}
	if m, err := s.Message("synthetic@lid", "same-id"); err != nil || m.Text != secret {
		t.Fatalf("wrong-key attempt altered original: %v", err)
	}
	if _, err := OpenArchive(filepath.Join(t.TempDir(), "bad.db"), nil); err == nil {
		t.Fatal("missing key accepted")
	}
}
func TestPagingTieOrderAndReplayPreservesEditsMedia(t *testing.T) {
	s, _, _ := testStore(t)
	chat := "fixture@group"
	if err := s.UpsertChat(Chat{ID: chat, Name: "Fixture"}); err != nil {
		t.Fatal(err)
	}
	msgs := []Message{{ID: "c", ChatID: chat, Timestamp: 50, Order: 3, Text: "Hello world"}, {ID: "a", ChatID: chat, Timestamp: 50, Order: 1, Text: "Hello earlier"}, {ID: "b", ChatID: chat, Timestamp: 50, Order: 2, Text: "Hello middle", Raw: []byte{1, 2}, Media: &Media{Path: "synthetic.png"}}}
	if err := s.SaveMessages(msgs); err != nil {
		t.Fatal(err)
	}
	page, more, err := s.Messages(chat, "", 2)
	if err != nil || !more || len(page) != 2 || page[0].ID != "b" || page[1].ID != "c" {
		t.Fatalf("page: %+v %t %v", page, more, err)
	}
	older, more, err := s.Messages(chat, page[0].ID, 2)
	if err != nil || more || len(older) != 1 || older[0].ID != "a" {
		t.Fatalf("older: %+v %t %v", older, more, err)
	}
	if err = s.UpdateMessage(chat, "b", func(m *Message) { m.Edited = true; m.Text = "edited distinct" }); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveMessages([]Message{{ID: "b", ChatID: chat, Timestamp: 50, Text: "original"}}); err != nil {
		t.Fatal(err)
	}
	m, err := s.Message(chat, "b")
	if err != nil || !m.Edited || m.Text != "edited distinct" || m.Order != 2 || m.Media.Path != "synthetic.png" || !bytes.Equal(m.Raw, []byte{1, 2}) {
		t.Fatalf("replay undo: %+v %v", m, err)
	}
	hits, err := s.Search("EDITED distinct", chat, 10)
	if err != nil || len(hits) != 1 || hits[0].Message.ID != "b" {
		t.Fatalf("search: %+v %v", hits, err)
	}
}
func TestDeletionBarrierVotesAndQueueRecovery(t *testing.T) {
	s, _, _ := testStore(t)
	m := Message{ID: "gone", ChatID: "fixture", Timestamp: 10, Text: "gone secret"}
	if err := s.SaveMessages([]Message{m}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteLocal(m.ChatID, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMessages([]Message{m}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Count()
	if n != 0 {
		t.Fatal("history restored deleted message")
	}
	if err := s.ClearChat("fixture", 100); err != nil {
		t.Fatal(err)
	}
	m.ID = "earlier"
	m.Timestamp = 80
	if err := s.SaveMessages([]Message{m}); err != nil {
		t.Fatal(err)
	}
	n, _ = s.Count()
	if n != 0 {
		t.Fatal("history crossed clear barrier")
	}
	if err := s.SaveVote("fixture", "poll", Vote{Sender: "voter", Timestamp: 20, ID: "new", Options: [][]byte{{2}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveVote("fixture", "poll", Vote{Sender: "voter", Timestamp: 10, ID: "old", Options: [][]byte{{1}}}); err != nil {
		t.Fatal(err)
	}
	v, err := s.Votes("fixture", "poll")
	if err != nil || len(v) != 1 || v[0].ID != "new" {
		t.Fatalf("vote replay: %+v %v", v, err)
	}
	id, err := s.Enqueue("delete", []byte("synthetic operation"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Pending(10)
	if err != nil || len(p) != 1 || p[0].ID != id {
		t.Fatal("queue missing")
	}
	if err = s.ReplacePending(id, []byte("acknowledged")); err != nil {
		t.Fatal(err)
	}
	p, err = s.Pending(10)
	if err != nil || string(p[0].Payload) != "acknowledged" {
		t.Fatal("queue ack lost")
	}
	if err = s.Acknowledge(id); err != nil {
		t.Fatal(err)
	}
}
func TestCanonicalMergeAndGroupReceiptAudience(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.UpsertChat(Chat{ID: "old@lid", Name: "Fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMessages([]Message{{ID: "a", ChatID: "old@lid", Timestamp: 1, Text: "old"}, {ID: "b", ChatID: "phone@s.whatsapp.net", Timestamp: 2, Text: "existing"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Canonicalize("old@lid", "phone@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	msgs, _, err := s.Messages("phone@s.whatsapp.net", "", 10)
	if err != nil || len(msgs) != 2 || msgs[0].ChatID != "phone@s.whatsapp.net" {
		t.Fatalf("canonical merge %+v %v", msgs, err)
	}
	if err = s.SetAudience("group", "out", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	level, known, err := s.Receipt("group", "out", "one", 2)
	if err != nil || !known || level != 0 {
		t.Fatalf("partial audience incorrectly read: %d %t %v", level, known, err)
	}
	level, known, err = s.Receipt("group", "out", "two", 1)
	if err != nil || !known || level != 1 {
		t.Fatal("aggregate delivery failed")
	}
	_, known, err = s.Receipt("group", "out", "new-member", 3)
	if err != nil || known {
		t.Fatal("historical audience expanded")
	}
}

func TestSearchDateStarredAccentsLockedAndTruncation(t *testing.T) {
	s, _, _ := testStore(t)
	for _, c := range []Chat{{ID: "open", Name: "Visible"}, {ID: "hidden", Name: "Locked", Locked: true}} {
		if err := s.UpsertChat(c); err != nil {
			t.Fatal(err)
		}
	}
	msgs := []Message{{ID: "before", ChatID: "open", Timestamp: 9, Text: "Café reunião", Starred: true}, {ID: "in", ChatID: "open", Timestamp: 10, Text: "Café reunião", Starred: true}, {ID: "plain", ChatID: "open", Timestamp: 11, Text: "Cafe reuniao"}, {ID: "end", ChatID: "open", Timestamp: 20, Text: "Cafe reuniao", Starred: true}, {ID: "secret", ChatID: "hidden", Timestamp: 15, Text: "Cafe reuniao", Starred: true}}
	if err := s.SaveMessages(msgs); err != nil {
		t.Fatal(err)
	}
	hits, truncated, err := s.SearchRange("CAFE REUNIAO", "", 10, 20, true, 100)
	if err != nil || truncated || len(hits) != 1 || hits[0].Message.ID != "in" {
		t.Fatalf("date/lock/accent/star filters %+v %t %v", hits, truncated, err)
	}
	hits, truncated, err = s.SearchRange("", "", 10, 20, false, 1)
	if err != nil || !truncated || len(hits) != 1 || hits[0].Message.ID != "plain" {
		t.Fatalf("calendar truncation %+v %t %v", hits, truncated, err)
	}
	hits, _, err = s.SearchRange("cafe", "hidden", 0, 0, false, 100)
	if err != nil || len(hits) != 0 {
		t.Fatal("locked in-chat search exposed content")
	}
	if err = s.UpdateChat("hidden", func(c *Chat) { c.Locked = false }); err != nil {
		t.Fatal(err)
	}
	hits, err = s.Search("cafe", "hidden", 100)
	if err != nil || len(hits) != 1 {
		t.Fatal("visibility index failed to follow unlock")
	}
}
func TestEditsKeepOriginalPlacementEvenWhenEditArrivesFirst(t *testing.T) {
	s, _, _ := testStore(t)
	original := Message{ID: "known", ChatID: "chat", Timestamp: 100, Order: 42, Text: "before"}
	if err := s.SaveMessages([]Message{original, {ID: "known", ChatID: "chat", Timestamp: 300, Order: 99, Text: "after", Edited: true}}); err != nil {
		t.Fatal(err)
	}
	m, err := s.Message("chat", "known")
	if err != nil || m.Timestamp != 100 || m.Order != 42 || m.Text != "after" {
		t.Fatalf("edit moved bubble %+v %v", m, err)
	}
	edit := Message{ID: "late-original", ChatID: "chat", Timestamp: 300, Order: 99, Text: "edited first", Edited: true, Raw: []byte("edited original protobuf")}
	if err = s.SaveMessages([]Message{edit, {ID: "late-original", ChatID: "chat", Timestamp: 90, Order: 40, Text: "stale original"}}); err != nil {
		t.Fatal(err)
	}
	m, err = s.Message("chat", "late-original")
	if err != nil || m.Timestamp != 90 || m.Order != 40 || m.Text != "edited first" || !m.Edited || string(m.Raw) != "edited original protobuf" {
		t.Fatalf("late original moved/undid edit %+v %v", m, err)
	}
}
func TestDraftMentionsCanonicalQueuesAndOriginalAudience(t *testing.T) {
	s, path, key := testStore(t)
	if err := s.SaveDraftMentions("old@lid", "persistent draft", []string{"someone@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMessages([]Message{{ID: "group-message", ChatID: "group@g.us", Sender: "old@lid", Text: "identity"}, {ID: "private-message", ChatID: "old@lid", Sender: "old@lid", Text: "private"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue("action", []byte(`{"Action":{"Kind":"mark-read","ChatID":"old@lid"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveVote("old@lid", "poll", Vote{Sender: "old@lid", Timestamp: 12, ID: "vote"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAudience("old@lid", "out", []string{"one"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Canonicalize("old@lid", "phone@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	m, err := s.Message("group@g.us", "group-message")
	if err != nil || m.Sender != "phone@s.whatsapp.net" {
		t.Fatal("group sender was not canonicalized")
	}
	pending, err := s.Pending(10)
	if err != nil || len(pending) != 1 || !bytes.Contains(pending[0].Payload, []byte("phone@s.whatsapp.net")) {
		t.Fatal("durable queued action did not move")
	}
	votes, err := s.Votes("phone@s.whatsapp.net", "poll")
	if err != nil || len(votes) != 1 || votes[0].Sender != "phone@s.whatsapp.net" {
		t.Fatal("durable votes did not move")
	}
	if err = s.SetAudience("phone@s.whatsapp.net", "out", []string{"one", "later-member"}); err != nil {
		t.Fatal(err)
	}
	_, known, err := s.Receipt("phone@s.whatsapp.net", "out", "later-member", 2)
	if err != nil || known {
		t.Fatal("retry expanded original group audience")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenArchive(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	text, mentions, err := reopened.DraftMentions("phone@s.whatsapp.net")
	if err != nil || text != "persistent draft" || len(mentions) != 1 {
		t.Fatal("draft mentions did not survive migration/restart")
	}
}
func TestInterruptedOutboxRequiresRetryAndUniqueEncryptedQueue(t *testing.T) {
	s, _, _ := testStore(t)
	m := Message{ID: "stable-wire-id", ChatID: "chat", FromMe: true, Status: "pending", Text: "do not lose", Raw: []byte("original encrypted stored protocol")}
	if err := s.SaveMessages([]Message{m}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverOutbox(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Message("chat", m.ID)
	if err != nil || got.Status != "failed" || !bytes.Equal(got.Raw, m.Raw) {
		t.Fatal("interrupted send was lost or auto-sent")
	}
	first, err := s.EnqueueUnique("poll-vote/hashed-wire-id", []byte("synthetic ciphertext"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.EnqueueUnique("poll-vote/hashed-wire-id", []byte("duplicate"))
	if err != nil || first != second {
		t.Fatal("encrypted replay was queued twice")
	}
}

func TestReceiptTimesSurviveReplayCanonicalizationAndDeletion(t *testing.T) {
	s, _, _ := testStore(t)
	chat := "team@g.us"
	if err := s.SaveMessages([]Message{{ChatID: chat, ID: "out", FromMe: true, Timestamp: 10, Status: "sent"}, {ChatID: chat, ID: "in", Timestamp: 10}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAudience(chat, "out", []string{"old@lid", "other@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	for _, observation := range []struct {
		status int
		ts     int64
	}{{3, 300}, {2, 200}, {1, 100}, {2, 190}, {1, 110}} {
		level, known, err := s.ReceiptAt(chat, "out", "old@lid", observation.status, observation.ts, true)
		if err != nil || !known || level != 0 {
			t.Fatalf("original audience aggregate: %d %v %v", level, known, err)
		}
	}
	if _, known, err := s.ReceiptAt(chat, "out", "later@s.whatsapp.net", 3, 400, true); err != nil || known {
		t.Fatal("later group member invented an acknowledgement")
	}
	if _, known, err := s.ReceiptAt(chat, "in", "old@lid", 2, 400, false); err != nil || known {
		t.Fatal("incoming message received outgoing receipt details")
	}
	if err := s.Canonicalize("old@lid", "phone@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	details, err := s.ReceiptDetails(chat, "out")
	if err != nil || len(details) != 2 {
		t.Fatalf("receipt details: %v %v", details, err)
	}
	var observed MessageReceipt
	for _, r := range details {
		if r.Sender == "phone@s.whatsapp.net" {
			observed = r
		}
	}
	if observed.DeliveredAt != 100 || observed.ReadAt != 190 || observed.PlayedAt != 300 {
		t.Fatalf("observations lost or inferred: %+v", observed)
	}
	if err := s.DeleteLocal(chat, "out"); err != nil {
		t.Fatal(err)
	}
	details, err = s.ReceiptDetails(chat, "out")
	if err != nil || len(details) != 0 {
		t.Fatal("deleted receipt details retained")
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM receipt_times").Scan(&count); err != nil || count != 0 {
		t.Fatal("orphaned receipt timestamps")
	}
}
func TestDirectReadReceiptDoesNotInventOtherTimes(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.SaveMessages([]Message{{ChatID: "person", ID: "out", FromMe: true, Timestamp: 10, Status: "sent"}}); err != nil {
		t.Fatal(err)
	}
	if _, known, err := s.ReceiptAt("person", "out", "person", 2, 200, false); err != nil || !known {
		t.Fatal(err)
	}
	details, err := s.ReceiptDetails("person", "out")
	if err != nil || len(details) != 1 || details[0].DeliveredAt != 0 || details[0].ReadAt != 200 || details[0].PlayedAt != 0 {
		t.Fatalf("guessed receipt times: %+v %v", details, err)
	}
}
