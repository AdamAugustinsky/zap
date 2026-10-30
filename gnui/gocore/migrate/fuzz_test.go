package migrate

import (
	"bytes"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.mau.fi/libsignal/groups/state/record"
	sessionRecord "go.mau.fi/libsignal/state/record"
	wastore "go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/encoding/protowire"

	"zapfast/gocore/store"
	"zapfast/gocore/store/sqlite"
)

// importedKinds are the kinds imp.message gives a Rust message.
var importedKinds = []string{
	"text", "image", "video", "ptv", "audio", "voice", "document", "sticker", "location", "live-location",
	store.KindCall, "poll", "view-once", "phone-only", "waiting", "unsupported",
}

// FuzzImportMessage reads a Rust archive's message row as the import does:
// its content, quote, reactions and mentions are JSON the Rust core wrote.
func FuzzImportMessage(f *testing.F) {
	for _, content := range []string{
		`{"kind":"text","text":"Synthetic hello","preview":{"url":"https://example.com","title":"Example"}}`,
		`{"kind":"image","caption":"Synthetic","media":{"mime":"image/jpeg","size":10,"path":"/nowhere/a.jpg"},"motion":null}`,
		`{"kind":"audio","media":{"mime":"audio/ogg","size":5},"seconds":4,"voice_note":true,"waveform":[1,2,3]}`,
		`{"kind":"video","media":{"mime":"video/mp4","size":5},"note":true,"gif":false}`,
		`{"kind":"livelocation","latitude":1.5,"longitude":2,"sequence":3,"updated":0,"heading_deg":90}`,
		`{"kind":"poll","question":"Q?","options":["a","b"],"state":{"selectable":0,"counts":[1],"selected":[1,7,-1]}}`,
		`{"kind":"interactive","text":"menu","card":{"body":"b","buttons":[{"label":"x","action":{"Copy":"C"}}],"image":{"mime":"image/png","size":1},"carousel":[{"body":"c","image":null}]}}`,
		`{"kind":"call","video":true,"status":"missed","duration":18446744073709551615}`,
		`{"kind":"phoneonly","view_once":true,"once":"voice"}`,
		`{"kind":"unsupported","what":""}`,
		`{"kind":"revoked"}`, `{"kind":"contact","display_name":""}`, `{"kind":"sticker_pack","name":"p"}`,
		`{"kind":"future"}`, `not json`, ``,
	} {
		f.Add(content, `{"id":"Q","sender":"s","summary":"q"}`, `[{"sender":"s","emoji":"👍"}]`, `[{"user":"1","id":"1@s.whatsapp.net"}]`, int64(5), true, int64(1))
	}
	f.Add(`{"kind":"text","text":"x"}`, `null`, `{}`, `[1]`, int64(math.MaxInt64), false, int64(math.MaxInt64))
	f.Fuzz(func(t *testing.T, content, quoted, reactions, mentions string, order int64, hasOrder bool, rowid int64) {
		row := map[string]any{"id": "3EB0FUZZ", "chat": bea, "sender": bea, "timestamp": int64(1_760_000_000), "content": content,
			"quoted": quoted, "reactions": reactions, "mentions": mentions, "__rowid": rowid, "history_order": nil}
		if hasOrder {
			row["history_order"] = order
		}
		imp := &importer{polls: map[string][]string{}}
		m, _ := imp.message(row)
		if m.ID != "3EB0FUZZ" || m.ChatID != bea || m.Timestamp != 1_760_000_000 {
			t.Fatalf("identity lost: %+v", m)
		}
		if !slices.Contains(importedKinds, m.Kind) {
			t.Fatalf("kind %q", m.Kind)
		}
		// A media path is kept only when its file is there, absolute.
		paths := []*store.Media{m.Media}
		for _, c := range m.Cards {
			paths = append(paths, c.Media)
		}
		for _, media := range paths {
			if media != nil && media.Path != "" {
				t.Fatalf("a missing file kept as %q", media.Path)
			}
		}
		if m.Kind == "poll" {
			if m.PollSelectable < 1 {
				t.Fatalf("poll selectable %d", m.PollSelectable)
			}
			if len(m.Poll) != len(imp.polls[rowKey(bea, "3EB0FUZZ")]) {
				t.Fatalf("poll options %d, remembered %d", len(m.Poll), len(imp.polls[rowKey(bea, "3EB0FUZZ")]))
			}
		}
		// Ordering: Rust put a row without history order after every row
		// with one in the same second (COALESCE(history_order, MAX)); the
		// phone's orders are non-negative (u64 msg_order_id).
		if hasOrder && order >= 0 && rowid >= 0 {
			plain := map[string]any{"id": "3EB0LIVE", "chat": bea, "timestamp": int64(1_760_000_000), "content": content, "__rowid": rowid, "history_order": nil}
			live, _ := imp.message(plain)
			if live.Order <= m.Order {
				t.Fatalf("history order %d sorts at %d, after a live row's %d (rowid %d)", order, m.Order, live.Order, rowid)
			}
		}
	})
}

// sessionSeeds are the fixture session store's Signal records (synthetic).
func sessionSeeds(f *testing.F, table string) [][]byte {
	if _, err := sqlite.Load(); err != nil {
		return nil
	}
	db, err := sqlite.Open(sqlite.Config{Path: filepath.Join("testdata", SessionFile), Plaintext: true, ReadOnly: true})
	if err != nil {
		f.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT record FROM " + table)
	if err != nil {
		f.Fatal(err)
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			f.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

// FuzzConvertSessionRecord converts a whatsapp-rust session record (with its
// local counter fields) to libsignal's: malformed input is refused with
// errSignalRecord-class errors, and what is accepted opens in libsignal.
func FuzzConvertSessionRecord(f *testing.F) {
	for _, seed := range sessionSeeds(f, "sessions") {
		f.Add(seed)
	}
	f.Add([]byte{0x0a, 0x00})
	f.Add(protowire.AppendVarint(protowire.AppendTag(nil, 100, protowire.VarintType), math.MaxUint32))
	f.Fuzz(func(t *testing.T, raw []byte) {
		out, advanced, err := convertSessionWithCounters(raw)
		if err != nil {
			if !errors.Is(err, ErrIncompatible) && !strings.Contains(err.Error(), "reservation") {
				t.Fatalf("error %v is not the import's", err)
			}
			return
		}
		if advanced < 0 || advanced > 41*2000 {
			t.Fatalf("advanced %d", advanced)
		}
		if _, err := sessionRecord.NewSessionFromBytes(out, wastore.SignalProtobufSerializer.Session, wastore.SignalProtobufSerializer.State); err != nil {
			t.Fatalf("accepted a record libsignal refuses: %v", err)
		}
		again, advancedAgain, err := convertSessionWithCounters(raw)
		if err != nil || !bytes.Equal(out, again) || advanced != advancedAgain {
			t.Fatal("not deterministic")
		}
	})
}

// FuzzConvertSenderKeyRecord is FuzzConvertSessionRecord for sender keys.
func FuzzConvertSenderKeyRecord(f *testing.F) {
	for _, seed := range sessionSeeds(f, "sender_keys") {
		f.Add(seed)
	}
	f.Add([]byte{0x12, 0x00})
	f.Fuzz(func(t *testing.T, raw []byte) {
		out, advanced, err := convertSenderKeyWithCounters(raw)
		if err != nil {
			if !errors.Is(err, ErrIncompatible) && !strings.Contains(err.Error(), "reservation") {
				t.Fatalf("error %v is not the import's", err)
			}
			return
		}
		if advanced < 0 || advanced > 5*2000 {
			t.Fatalf("advanced %d", advanced)
		}
		if _, err := record.NewSenderKeyFromBytes(out, wastore.SignalProtobufSerializer.SenderKeyRecord, wastore.SignalProtobufSerializer.SenderKeyState); err != nil {
			t.Fatalf("accepted a record libsignal refuses: %v", err)
		}
	})
}

// FuzzDecodeHashState reads whatsapp-rust's app-state hash: accepted only
// with a 128-byte hash, and what is read writes back to the same reading.
func FuzzDecodeHashState(f *testing.F) {
	encode := func(version uint64, hash []byte, mismatch, bootstrapped bool) []byte {
		b := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), version)
		b = protowire.AppendBytes(protowire.AppendTag(b, 2, protowire.BytesType), hash)
		flag := func(v bool) uint64 {
			if v {
				return 1
			}
			return 0
		}
		b = protowire.AppendVarint(protowire.AppendTag(b, 4, protowire.VarintType), flag(mismatch))
		return protowire.AppendVarint(protowire.AppendTag(b, 5, protowire.VarintType), flag(bootstrapped))
	}
	f.Add(encode(7, bytes.Repeat([]byte{1}, 128), false, true))
	f.Add(encode(0, bytes.Repeat([]byte{2}, 127), true, false))
	f.Add([]byte{0x1a, 0x00})
	f.Fuzz(func(t *testing.T, raw []byte) {
		version, hash, reset, err := decodeHashState(raw)
		if err != nil {
			if !errors.Is(err, ErrIncompatible) {
				t.Fatalf("error %v", err)
			}
			return
		}
		if len(hash) != 128 || version > math.MaxInt64 {
			t.Fatalf("version %d, %d-byte hash", version, len(hash))
		}
		v2, h2, r2, err := decodeHashState(encode(version, hash, reset, !reset))
		if err != nil || v2 != version || !bytes.Equal(h2, hash) || r2 != reset {
			t.Fatalf("round trip: %d %v %v", v2, r2, err)
		}
	})
}

// FuzzSignalAddress reads the Rust store's Signal addresses: an accepted
// one names a person's device, and its JID reads back as itself.
func FuzzSignalAddress(f *testing.F) {
	for _, s := range []string{
		"15550002002@s.whatsapp.net.0", "15550002002:3@s.whatsapp.net.0", "100000000002002:3@lid.0", "15550002002@c.us.0",
		"120363000000000001@g.us:15550002002@s.whatsapp.net.0", "status@broadcast:100000000002002@lid.0", "@.0", "a b@lid.0",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if jid, err := sessionJID(s); err == nil {
			again, err := sessionJID(jid.String())
			if err != nil || again != jid {
				t.Fatalf("%q reads as %v, then %v (%v)", s, jid, again, err)
			}
		}
		if address, err := signalAddress(s); err == nil {
			if !strings.HasSuffix(s, ".0") || address == "" {
				t.Fatalf("%q gave %q", s, address)
			}
		}
		if chat, address, err := senderAddress(s); err == nil {
			if !strings.HasSuffix(chat, "@g.us") && chat != "status@broadcast" || address == "" {
				t.Fatalf("%q gave %q %q", s, chat, address)
			}
		}
	})
}
