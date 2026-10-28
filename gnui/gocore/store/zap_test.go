package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func ids(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

func count(t *testing.T, s *Archive, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A zapfast-go archive (schema 4) is brought to 5: kinds, stars, links and
// mentions are rebuilt from the payloads it already has.
func TestMigratesAZapfastArchive(t *testing.T) {
	needSQLCipher(t)
	path := filepath.Join(t.TempDir(), "archive.db")
	key := make([]byte, 32)
	db, err := OpenDatabase(path, key)
	if err != nil {
		t.Fatal(err)
	}
	v4 := strings.Replace(baseSchema, ",kind TEXT NOT NULL DEFAULT ''", "", 1)
	if _, err := db.Exec(v4 + "; PRAGMA user_version=4"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []Message{
		{ID: "photo", ChatID: "c", Timestamp: 10, Kind: KindImage, Starred: true, Media: &Media{MIME: "image/jpeg"}},
		{ID: "link", ChatID: "c", Timestamp: 11, Kind: "text", Text: "see https://example.org/a.", Mentions: []Mention{{User: "1555", ID: "1555@s.whatsapp.net"}}},
	} {
		payload, _ := json.Marshal(m)
		if _, err := db.Exec("INSERT INTO messages(chat,id,ts,ord,payload) VALUES(?,?,?,0,?)", m.ChatID, m.ID, m.Timestamp, payload); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := OpenArchive(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := count(t, s, "PRAGMA user_version"); n != SchemaVersion {
		t.Fatalf("version %d", n)
	}
	sh, err := s.Shared("c", 10)
	if err != nil || !reflect.DeepEqual(ids(sh.Media), []string{"photo"}) || len(sh.Links) != 1 || sh.Links[0].URL != "https://example.org/a" {
		t.Fatalf("shared %+v %v", sh, err)
	}
	starred, err := s.Starred(0)
	if err != nil || len(starred) != 1 || starred[0].StarredAt != 10 {
		t.Fatalf("starred %+v %v", starred, err)
	}
	if hits, err := s.Search("see", "c", 10); err != nil || len(hits) != 1 {
		t.Fatalf("tokens %v %v", hits, err)
	}
	s.Close()
	if _, err := s.db.Exec("PRAGMA user_version=99"); err == nil {
		t.Fatal("closed archive wrote")
	}
	db, _ = OpenDatabase(path, key)
	db.Exec("PRAGMA user_version=99")
	db.Close()
	if _, err := OpenArchive(path, key); !errors.Is(err, ErrNewerArchive) {
		t.Fatalf("newer schema: %v", err)
	}
}

func TestStarsKeepTheirTimeAndOrder(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.UpsertChat(Chat{ID: "hidden", Locked: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMessages([]Message{
		{ID: "a", ChatID: "c", Timestamp: 1, Text: "first"},
		{ID: "b", ChatID: "c", Timestamp: 2, Text: "second"},
		{ID: "gone", ChatID: "c", Timestamp: 3, Text: "revoked"},
		{ID: "x", ChatID: "hidden", Timestamp: 4, Text: "locked"},
	}); err != nil {
		t.Fatal(err)
	}
	star := func(chat, id string, on bool, at int64, replayed bool) bool {
		t.Helper()
		changed, err := s.SetStar(chat, id, on, at, replayed)
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	star("c", "a", true, 100, false)
	star("c", "b", true, 200, false)
	star("c", "gone", true, 300, false)
	star("hidden", "x", true, 400, false)
	if star("c", "a", false, 100, true) {
		t.Fatal("a replay at the same second undid a star")
	}
	if star("c", "a", true, 50, false) {
		t.Fatal("an older event won")
	}
	if err := s.UpdateMessage("c", "gone", func(m *Message) { m.Deleted = true }); err != nil {
		t.Fatal(err)
	}
	list, err := s.Starred(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{}; true {
		for _, st := range list {
			got = append(got, st.Message.ID)
		}
		if !reflect.DeepEqual(got, []string{"b", "a"}) || list[0].StarredAt != 200 {
			t.Fatalf("starred list %v", got)
		}
	}
	// History replays the message unstarred: the star stays.
	if err := s.SaveMessages([]Message{{ID: "a", ChatID: "c", Timestamp: 1, Text: "first"}}); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Message("c", "a"); !m.Starred {
		t.Fatal("a replay dropped the star")
	}
	if !star("c", "a", false, 100, false) {
		t.Fatal("a live unstar at the same second lost")
	}
	if m, _ := s.Message("c", "a"); m.Starred {
		t.Fatal("the message still shows its star")
	}
	// zapfast-go's starred search follows the flag (a revoked message stays
	// in it, as there); the Starred list above leaves it out.
	if hits, _, err := s.SearchRange("", "c", 0, 0, true, 10); err != nil || !reflect.DeepEqual(hitIDs(hits), []string{"gone", "b"}) {
		t.Fatalf("starred search %v %v", hitIDs(hits), err)
	}
	if got, err := s.StarredIDs("c"); err != nil || !reflect.DeepEqual(got, []string{"b", "gone"}) {
		t.Fatalf("ids %v %v", got, err)
	}
	// Starring through UpdateMessage is a live event now.
	if err := s.UpdateMessage("c", "a", func(m *Message) { m.Starred = true }); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.StarredIDs("c"); !slices.Contains(got, "a") {
		t.Fatal("UpdateMessage's star was not recorded")
	}
}

func hitIDs(hits []SearchHit) []string {
	out := []string{}
	for _, h := range hits {
		out = append(out, h.Message.ID)
	}
	return out
}

func TestMessagePins(t *testing.T) {
	s, _, _ := testStore(t)
	for i, id := range []string{"p1", "p2", "p3", "p4", "short"} {
		if err := s.SaveMessages([]Message{{ID: id, ChatID: "c", Timestamp: int64(i + 1), Text: id}}); err != nil {
			t.Fatal(err)
		}
	}
	pin := func(id string, on bool, at, until int64, by Pinner) bool {
		t.Helper()
		changed, err := s.SetPin("c", id, on, at, until, by)
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	me := Pinner{ByMe: true}
	pin("p1", true, 10, 1000, me)
	pin("p2", true, 20, 1000, Pinner{Sender: "ada@s.whatsapp.net"})
	pin("p3", true, 30, 1000, me)
	pin("short", true, 35, 40, me)
	pin("p4", true, 40, 1000, me)
	got, err := s.PinnedIDs("c", 100)
	if err != nil || !reflect.DeepEqual(got, []string{"p4", "p3", "p2"}) {
		t.Fatalf("active pins %v %v", got, err)
	}
	if full, _ := s.PinFull("c", "p1", 100); !full {
		t.Fatal("a fourth pin fits")
	}
	if full, _ := s.PinFull("c", "p3", 100); full {
		t.Fatal("a pinned message counts against itself")
	}
	pins, err := s.ChatPins("c", 100)
	if err != nil || len(pins) != 3 || pins[2].Pinner.Sender != "ada@s.whatsapp.net" || pins[0].Message.Text != "p4" {
		t.Fatalf("pins %+v %v", pins, err)
	}
	if !pin("p4", false, 50, 0, me) || pin("p4", true, 45, 1000, me) {
		t.Fatal("an older pin replay undid an unpin")
	}
	if err := s.DeleteLocal("c", "p3"); err != nil {
		t.Fatal(err)
	}
	if pin("p3", true, 60, 1000, me) {
		t.Fatal("a deleted message took a pin")
	}
	if got, _ := s.PinnedIDs("c", 100); !reflect.DeepEqual(got, []string{"p2", "p1"}) {
		t.Fatalf("after unpin and delete %v", got)
	}
	if err := s.UpdateMessage("c", "p2", func(m *Message) { m.Deleted = true }); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.PinnedIDs("c", 100); !reflect.DeepEqual(got, []string{"p1"}) {
		t.Fatalf("a revoked message kept its pin %v", got)
	}
}

func TestLocalLabels(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.UpsertChat(Chat{ID: "c", Name: "Chat"}); err != nil {
		t.Fatal(err)
	}
	work, ok, err := s.CreateLabel("  Work  ", "F0a", 100)
	if err != nil || !ok || work.ID != "label-100" || work.Name != "Work" || work.Color != "#ff00aa" {
		t.Fatalf("create %+v %v %v", work, ok, err)
	}
	if _, ok, _ := s.CreateLabel("work", "", 101); ok {
		t.Fatal("a taken name was accepted")
	}
	home, ok, _ := s.CreateLabel("Home", "nonsense", 100)
	if !ok || home.ID != "label-100-1" || home.Color != DefaultLabelColor {
		t.Fatalf("second label %+v", home)
	}
	if err := s.SetChatLabels("c", []string{home.ID, "label-gone", work.ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ChatLabels("c"); !reflect.DeepEqual(got, []string{work.ID, home.ID}) {
		t.Fatalf("chat labels %v", got)
	}
	if c, _ := s.Chat("c"); !reflect.DeepEqual(c.Labels, []string{work.ID, home.ID}) {
		t.Fatalf("chat payload labels %v", c.Labels)
	}
	if ok, _ := s.UpdateLabel(home.ID, "WORK", "#000"); ok {
		t.Fatal("rename to a taken name")
	}
	if ok, _ := s.UpdateLabel(home.ID, "Family", "#000"); !ok {
		t.Fatal("rename")
	}
	if ok, _ := s.DeleteLabel(work.ID); !ok {
		t.Fatal("delete")
	}
	if c, _ := s.Chat("c"); !reflect.DeepEqual(c.Labels, []string{home.ID}) {
		t.Fatalf("a deleted label stayed on the chat: %v", c.Labels)
	}
	for i := range LabelLimit {
		s.CreateLabel("L"+string(rune('a'+i)), "", int64(200+i))
	}
	if l, _ := s.Labels(); len(l) != LabelLimit || l[0].Name != "Family" {
		t.Fatalf("labels %d", len(l))
	}
	if _, ok, _ := s.CreateLabel("one too many", "", 999); ok {
		t.Fatal("the limit was passed")
	}
}

func TestCallLog(t *testing.T) {
	s, _, _ := testStore(t)
	call := func(id, chat string, ts int64, fromMe bool) Message {
		return Message{ID: id, ChatID: chat, Timestamp: ts, FromMe: fromMe, Kind: KindCall, Call: &Call{Status: "missed"}}
	}
	if err := s.SaveMessages([]Message{
		call("PHONE", "a", 1000, false),
		call(CallEntryPrefix+"1", "a", 1050, false),
		call(CallEntryPrefix+"2", "a", 1500, false),
		call("OUT", "b", 2000, true),
		{ID: "text", ChatID: "a", Timestamp: 3000, Text: "not a call"},
	}); err != nil {
		t.Fatal(err)
	}
	log, err := s.CallLog(0)
	if err != nil || !reflect.DeepEqual(ids(log), []string{"OUT", CallEntryPrefix + "2", CallEntryPrefix + "1", "PHONE"}) || log[3].Call == nil {
		t.Fatalf("call log %v %v", ids(log), err)
	}
	if near, _ := s.CallEntryNear("a", 1000, false, "PHONE"); !near {
		t.Fatal("the twin within the window was not found")
	}
	if near, _ := s.CallEntryNear("a", 1000, true, "PHONE"); near {
		t.Fatal("the other side's entry matched")
	}
	if twins, _ := s.LocalCallTwins("a", 1000, false); !reflect.DeepEqual(twins, []string{CallEntryPrefix + "1"}) {
		t.Fatalf("twins %v", twins)
	}
}

func TestSharedMediaDocsAndLinks(t *testing.T) {
	s, _, _ := testStore(t)
	img := &Media{MIME: "image/jpeg"}
	if err := s.SaveMessages([]Message{
		{ID: "photo", ChatID: "c", Timestamp: 1, Kind: KindImage, Media: img},
		{ID: "doc", ChatID: "c", Timestamp: 2, Kind: KindDocument, Media: &Media{Name: "a.pdf"}},
		{ID: "gif", ChatID: "c", Timestamp: 3, Kind: KindVideo, Media: &Media{Animated: true}},
		{ID: "clip", ChatID: "c", Timestamp: 4, Kind: KindVideo, Media: &Media{}},
		{ID: "link", ChatID: "c", Timestamp: 5, Kind: "text", Text: "go to www.example.com, or (https://a.example/x_(y)) now"},
		{ID: "bare", ChatID: "c", Timestamp: 6, Kind: "text", Text: "example.com is not a link here"},
		{ID: "card", ChatID: "c", Timestamp: 7, Kind: "text", Text: "https://news.example/1", LinkPreview: &LinkPreview{URL: "https://news.example/1", Title: "News"}},
		{ID: "caption", ChatID: "c", Timestamp: 8, Kind: KindImage, Text: "https://caption.example", Media: img},
		{ID: "other", ChatID: "d", Timestamp: 9, Kind: KindImage, Media: img},
	}); err != nil {
		t.Fatal(err)
	}
	sh, err := s.Shared("c", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(sh.Media), []string{"caption", "clip", "photo"}) || !reflect.DeepEqual(ids(sh.Docs), []string{"doc"}) {
		t.Fatalf("media %v docs %v", ids(sh.Media), ids(sh.Docs))
	}
	var links []string
	for _, l := range sh.Links {
		links = append(links, l.Message+" "+l.URL+" "+l.Title)
	}
	want := []string{"card https://news.example/1 News", "link www.example.com ", "link https://a.example/x_(y) "}
	if !reflect.DeepEqual(links, want) {
		t.Fatalf("links %q", links)
	}
	if sh, _ = s.Shared("c", 1); !sh.MediaTruncated || sh.DocsTruncated || !sh.LinksTruncated || len(sh.Links) != 1 {
		t.Fatalf("truncation %+v", sh)
	}
	page, err := s.ChatMedia("c", "", 2)
	if err != nil || !reflect.DeepEqual(ids(page), []string{"clip", "caption"}) {
		t.Fatalf("newest page %v %v", ids(page), err)
	}
	page, _ = s.ChatMedia("c", "clip", 2)
	if !reflect.DeepEqual(ids(page), []string{"clip", "caption"}) {
		t.Fatalf("around clip %v", ids(page))
	}
	page, _ = s.ChatMedia("c", "photo", 3)
	if !reflect.DeepEqual(ids(page), []string{"photo", "clip"}) {
		t.Fatalf("around photo %v", ids(page))
	}
	// A deleted message leaves every index.
	if err := s.DeleteLocal("c", "card"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, "SELECT count(*) FROM message_links WHERE id='card'"); n != 0 {
		t.Fatal("links outlived their message")
	}
	if err := s.ClearChat("c", 100); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"message_links", "message_mentions", "message_flags", "stars", "message_pins"} {
		if n := count(t, s, "SELECT count(*) FROM "+table+" WHERE chat='c'"); n != 0 {
			t.Fatalf("%s outlived a cleared chat", table)
		}
	}
}

func TestMentionsUnreadAndCanonical(t *testing.T) {
	s, _, _ := testStore(t)
	me := []string{"15550000000@s.whatsapp.net", "99999@lid"}
	chat := "group@g.us"
	if err := s.SaveMessages([]Message{
		{ID: "old", ChatID: chat, Timestamp: 1, Mentions: []Mention{{User: "15550000000", ID: me[0]}}},
		{ID: "lid", ChatID: chat, Timestamp: 2, Mentions: []Mention{{User: "99999", ID: "99999@lid"}}},
		{ID: "all", ChatID: chat, Timestamp: 3, Mentions: []Mention{{User: "all", ID: MentionAll}}},
		{ID: "other", ChatID: chat, Timestamp: 4, Mentions: []Mention{{User: "1777", ID: "1777@s.whatsapp.net"}}},
		{ID: "mine", ChatID: chat, Timestamp: 5, FromMe: true, Mentions: []Mention{{User: "15550000000", ID: me[0]}}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		unread int
		ours   []string
		want   string
	}{{0, me, ""}, {1, me, ""}, {2, me, "all"}, {3, me, "lid"}, {4, me, "old"}, {4, nil, "all"}} {
		if got, err := s.UnreadMention(chat, c.unread, c.ours); err != nil || got != c.want {
			t.Fatalf("unread %d: %q %v", c.unread, got, err)
		}
	}
	if err := s.Canonicalize("99999@lid", me[0]); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Message(chat, "lid")
	if len(m.Mentions) != 1 || m.Mentions[0].ID != me[0] || m.Mentions[0].User != "99999" {
		t.Fatalf("mention after canonicalize %+v", m.Mentions)
	}
	if n := count(t, s, "SELECT count(*) FROM message_mentions WHERE jid='99999@lid'"); n != 0 {
		t.Fatal("the old identity is still indexed")
	}
}

// Canonicalizing a chat moves its stars, pins and labels with it.
func TestCanonicalizeMovesZapRows(t *testing.T) {
	s, _, _ := testStore(t)
	from, to := "old@lid", "phone@s.whatsapp.net"
	if err := s.UpsertChat(Chat{ID: from}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMessages([]Message{{ID: "m", ChatID: from, Timestamp: 5, Text: "x"}}); err != nil {
		t.Fatal(err)
	}
	s.SetStar(from, "m", true, 50, false)
	s.SetPin(from, "m", true, 60, 1000, Pinner{Sender: from})
	l, _, _ := s.CreateLabel("L", "", 1)
	s.SetChatLabels(from, []string{l.ID})
	if err := s.Canonicalize(from, to); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.StarredIDs(to); !reflect.DeepEqual(got, []string{"m"}) {
		t.Fatalf("stars %v", got)
	}
	pins, _ := s.ChatPins(to, 100)
	if len(pins) != 1 || pins[0].Pinner.Sender != to {
		t.Fatalf("pins %+v", pins)
	}
	if got, _ := s.ChatLabels(to); !reflect.DeepEqual(got, []string{l.ID}) {
		t.Fatalf("labels %v", got)
	}
	for _, table := range []string{"stars", "message_pins", "local_chat_labels", "message_flags"} {
		if n := count(t, s, "SELECT count(*) FROM "+table+" WHERE chat=?", from); n != 0 {
			t.Fatalf("%s kept the old chat", table)
		}
	}
}

// Labels from the phone keep their own ids; a chat wears one at a time.
func TestPhoneLabels(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.UpsertChat(Chat{ID: "c", Name: "Chat"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.PutLabel(Label{ID: "wa-3", Name: " New customer ", Color: "#0f0", CreatedAt: 5}); !ok || err != nil {
		t.Fatalf("put %v %v", ok, err)
	}
	if ok, _ := s.PutLabel(Label{ID: "wa-3", Name: "New customer", Color: "#00ff00", CreatedAt: 9}); ok {
		t.Fatal("an unchanged label changed")
	}
	if ok, _ := s.LabelChat("c", "wa-3", true); !ok {
		t.Fatal("label a chat")
	}
	if ok, _ := s.LabelChat("c", "wa-gone", true); ok {
		t.Fatal("a missing label was put on")
	}
	if c, _ := s.Chat("c"); !reflect.DeepEqual(c.Labels, []string{"wa-3"}) {
		t.Fatalf("labels %v", c.Labels)
	}
	if ok, _ := s.LabelChat("c", "wa-3", false); !ok {
		t.Fatal("unlabel")
	}
	if l, _ := s.Labels(); len(l) != 1 || l[0].Name != "New customer" || l[0].CreatedAt != 5 {
		t.Fatalf("labels %+v", l)
	}
}

// Stickers are listed newest first by side; Clear leaves nothing behind.
func TestStickersAndClear(t *testing.T) {
	s, _, _ := testStore(t)
	if err := s.SaveMessages([]Message{
		{ID: "a", ChatID: "c", Timestamp: 1, Kind: "sticker", FromMe: true, Media: &Media{Path: "/a.webp"}},
		{ID: "b", ChatID: "c", Timestamp: 2, Kind: "sticker", FromMe: true},
		{ID: "c", ChatID: "c", Timestamp: 3, Kind: "sticker"},
		{ID: "d", ChatID: "c", Timestamp: 4, Kind: "text", Text: "hi", FromMe: true},
	}); err != nil {
		t.Fatal(err)
	}
	if ours, _ := s.Stickers(true, 10); !reflect.DeepEqual(ids(ours), []string{"b", "a"}) {
		t.Fatalf("ours %v", ids(ours))
	}
	if theirs, _ := s.Stickers(false, 10); !reflect.DeepEqual(ids(theirs), []string{"c"}) {
		t.Fatalf("theirs %v", ids(theirs))
	}
	if _, _, err := s.CreateLabel("Work", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertChat(Chat{ID: "c", Name: "Chat"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMetadata("k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"messages", "chats", "local_labels", "metadata", "message_flags", "tokens"} {
		if n := count(t, s, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("%s keeps %d rows", table, n)
		}
	}
	if n := count(t, s, "PRAGMA user_version"); n != SchemaVersion {
		t.Fatalf("schema %d", n)
	}
}
