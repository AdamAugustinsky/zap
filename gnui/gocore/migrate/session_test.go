package migrate

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/libsignal/groups"
	"go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/session"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"zapfast/gocore/store"
)

var serializer = wastore.SignalProtobufSerializer

// converted converts the fixture session store fixture into a whatsmeow
// store and opens it as the Go backend does.
func converted(t *testing.T, fixture string) (*wastore.Device, SessionReport) {
	t.Helper()
	needSQLCipher(t)
	dir := t.TempDir()
	source := fixtureCopy(t, fixture, dir, "session.db")
	before := tree(t, dir)
	key := bytes.Repeat([]byte{0x6b}, 32)
	target := filepath.Join(dir, "whatsmeow.db")
	report, err := ConvertSession(context.Background(), source, target, key)
	if err != nil {
		t.Fatalf("%s: %v", fixture, err)
	}
	after := tree(t, dir)
	delete(after, "whatsmeow.db")
	if !sameTree(before, after) {
		t.Fatalf("converting %s changed or left files: %v", fixture, after)
	}
	devices, err := store.OpenDevices(context.Background(), target, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { devices.Close() })
	device, err := devices.GetFirstDevice(context.Background())
	if err != nil || device.ID == nil {
		t.Fatalf("no converted device: %v", err)
	}
	return device, report
}

func TestConvertSessionCounts(t *testing.T) {
	f := testFixtures(t)
	device, r := converted(t, "session.db")
	if device.ID.String() != f.Accounts.Ours.PN || device.LID.String() != f.Accounts.Ours.LID || device.PushName != "Synthetic Us" || device.LIDMigrationTimestamp == 0 {
		t.Fatalf("device %v %v %q", device.ID, device.LID, device.PushName)
	}
	if device.SignedPreKey.KeyID != 2 {
		t.Fatalf("signed prekey %d, want the rotated-in 2", device.SignedPreKey.KeyID)
	}
	want := SessionReport{
		Devices: 1, Identities: 2, Sessions: 2, PreKeys: 12, UploadedPreKeys: 9, SenderKeys: 2,
		AppStateKeys: 2, AppStateVersions: 3, AppStateReset: 2, AppStateMACs: 9,
		LIDMappings: 3, HistoricalLIDAliases: 1, MessageSecrets: 4, PrivacyTokens: 2,
		RotatedSignedPreKeys: 1, PendingInbound: 1,
		SessionCountersAdvanced: r.SessionCountersAdvanced, SenderKeyCountersAdvanced: r.SenderKeyCountersAdvanced,
		PreservedRows: r.PreservedRows,
	}
	if r != want {
		t.Fatalf("report\n got %+v\nwant %+v", r, want)
	}
	// Rust leases sender counters ahead of what it sent; every lease is
	// burnt so the Go side never reuses a message key.
	if r.SessionCountersAdvanced == 0 || r.SenderKeyCountersAdvanced == 0 {
		t.Fatalf("no counters advanced: %+v", r)
	}
	// 1 device, 2 identities, 2 sessions, 12 prekeys, 1 signed prekey, 2
	// sender keys, 2+5+15 app state, 4 mappings, 4 secrets, 2 tokens, 1
	// base key, 2 sender-key devices, 1 sent, 1 pending, 1 group.
	if r.PreservedRows != 58 {
		t.Fatalf("preserved %d rows", r.PreservedRows)
	}
	ctx := context.Background()
	salt, err := device.NCTSalt.GetNCTSalt(ctx)
	if err != nil || !bytes.Equal(salt, bytes.Repeat([]byte{0x4e}, 32)) {
		t.Fatalf("nct salt %x: %v", salt, err)
	}
	pn, err := device.LIDs.GetPNForLID(ctx, types.NewJID("100000000003003", types.HiddenUserServer))
	if err != nil || pn.User != "15550003003" {
		t.Fatalf("lid map: %v %v", pn, err)
	}
	if key, err := device.AppStateKeys.GetAppStateSyncKey(ctx, []byte{1, 0, 0, 1}); err != nil || key == nil || !bytes.Equal(key.Data, bytes.Repeat([]byte{11}, 32)) {
		t.Fatalf("app-state key: %v %v", key, err)
	}
	if version, _, err := device.AppState.GetAppStateVersion(ctx, "regular"); err != nil || version != 41 {
		t.Fatalf("regular at %d: %v", version, err)
	}
	if version, hash, err := device.AppState.GetAppStateVersion(ctx, "regular_low"); err != nil || version != 0 || hash != [128]byte{} {
		t.Fatalf("regular_low not reset: %d %v", version, err)
	}
	secret, _, err := device.MsgSecrets.GetMessageSecret(ctx, types.NewJID("15550002002", types.DefaultUserServer), types.NewJID("15550001001", types.DefaultUserServer), "3EB0SYNTHSECRET0")
	if err != nil || !bytes.Equal(secret, bytes.Repeat([]byte{0x30}, 32)) {
		t.Fatalf("message secret %x: %v", secret, err)
	}
	token, err := device.PrivacyTokens.GetPrivacyToken(ctx, types.NewJID("100000000002002", types.HiddenUserServer))
	if err != nil || token == nil || !bytes.Equal(token.Token, bytes.Repeat([]byte{0x71}, 24)) {
		t.Fatalf("privacy token %v: %v", token, err)
	}
	// whatsmeow numbers its next prekey above the highest Rust handed out.
	next, err := device.PreKeys.GenOnePreKey(ctx)
	if err != nil || next.KeyID != 13 {
		t.Fatalf("next prekey %v: %v", next, err)
	}
}

func jid(t *testing.T, s string) types.JID {
	t.Helper()
	j, err := types.ParseJID(s)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The converted device and the converted peer talk through whatsmeow's
// stores with libsignal: the peer's Rust-encrypted messages we never read
// decrypt from the converted skipped keys, and new messages each way and
// in the group decrypt on the other side, past the burnt counter leases.
func TestConvertedDevicesTalk(t *testing.T) {
	f := testFixtures(t)
	ctx := context.Background()
	ours, _ := converted(t, "session.db")
	peer, _ := converted(t, "peer-session.db")
	ourLID, peerLID := jid(t, f.Accounts.Ours.LID), jid(t, f.Accounts.Peer.LID)
	toPeer := session.NewCipher(session.NewBuilderFromSignal(ours, peerLID.SignalAddress(), serializer), peerLID.SignalAddress())
	toUs := session.NewCipher(session.NewBuilderFromSignal(peer, ourLID.SignalAddress(), serializer), ourLID.SignalAddress())

	for _, m := range f.Messages.DMSkipped {
		msg, err := protocol.NewSignalMessageFromBytes(unhex(t, m[0]), serializer.SignalMessage)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := toPeer.Decrypt(ctx, msg)
		if err != nil || string(plain) != m[1] {
			t.Fatalf("Rust message %q: %q %v", m[1], plain, err)
		}
	}
	exchange := func(from, to *session.Cipher, text string) {
		t.Helper()
		sent, err := from.Encrypt(ctx, []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		var plain []byte
		switch sent.Type() {
		case protocol.WHISPER_TYPE:
			msg, err := protocol.NewSignalMessageFromBytes(sent.Serialize(), serializer.SignalMessage)
			if err != nil {
				t.Fatal(err)
			}
			plain, err = to.Decrypt(ctx, msg)
		default:
			msg, err := protocol.NewPreKeySignalMessageFromBytes(sent.Serialize(), serializer.PreKeySignalMessage, serializer.SignalMessage)
			if err != nil {
				t.Fatal(err)
			}
			plain, err = to.DecryptMessage(ctx, msg)
		}
		if string(plain) != text {
			t.Fatalf("%q arrived as %q", text, plain)
		}
	}
	for i := range 3 {
		exchange(toPeer, toUs, "go to peer "+string(rune('a'+i)))
		exchange(toUs, toPeer, "go to us "+string(rune('a'+i)))
	}

	// The session with the third device was never answered: it still
	// sends prekey messages.
	third := jid(t, f.Accounts.Third.PN)
	sent, err := session.NewCipher(session.NewBuilderFromSignal(ours, third.SignalAddress(), serializer), third.SignalAddress()).Encrypt(ctx, []byte("again"))
	if err != nil || sent.Type() != protocol.PREKEY_TYPE {
		t.Fatalf("third: %v %v", sent, err)
	}

	group := f.Accounts.Group
	peerName := protocol.NewSenderKeyName(group, peerLID.SignalAddress())
	ourName := protocol.NewSenderKeyName(group, ourLID.SignalAddress())
	fromPeerAtUs := groups.NewGroupCipher(groups.NewGroupSessionBuilder(ours, serializer), peerName, ours)
	msg, err := protocol.NewSenderKeyMessageFromBytes(unhex(t, f.Messages.GroupSkipped[0]), serializer.SenderKeyMessage)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := fromPeerAtUs.Decrypt(ctx, msg); err != nil || string(plain) != f.Messages.GroupSkipped[1] {
		t.Fatalf("Rust group message: %q %v", plain, err)
	}
	groupExchange := func(from, to *wastore.Device, name *protocol.SenderKeyName, text string) {
		t.Helper()
		out, err := groups.NewGroupCipher(groups.NewGroupSessionBuilder(from, serializer), name, from).Encrypt(ctx, []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		msg, err := protocol.NewSenderKeyMessageFromBytes(out.SignedSerialize(), serializer.SenderKeyMessage)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := groups.NewGroupCipher(groups.NewGroupSessionBuilder(to, serializer), name, to).Decrypt(ctx, msg)
		if err != nil || string(plain) != text {
			t.Fatalf("group %q: %q %v", text, plain, err)
		}
	}
	groupExchange(ours, peer, ourName, "go group from us")
	groupExchange(peer, ours, peerName, "go group from peer")
	groupExchange(ours, peer, ourName, "go group from us again")
}

func TestConvertSessionFailsClosed(t *testing.T) {
	needSQLCipher(t)
	key := bytes.Repeat([]byte{0x6b}, 32)
	for _, c := range []struct {
		name  string
		alter []string
		want  error
	}{
		{"an unknown migration", []string{"INSERT INTO __diesel_schema_migrations(version) VALUES('20261101000000')"}, ErrUnknownSchema},
		{"a missing migration", []string{"DELETE FROM __diesel_schema_migrations WHERE version='20260929000000'"}, ErrUnknownSchema},
		{"an unknown column", []string{"ALTER TABLE sessions ADD COLUMN flavour TEXT"}, ErrUnknownSchema},
		{"an unknown table", []string{"CREATE TABLE future_keys(id INTEGER)"}, ErrUnknownSchema},
		{"a prekey gap", []string{"DELETE FROM prekeys WHERE id=12"}, ErrPreKeyGap},
		{"a forged signed prekey", []string{"UPDATE device SET signed_pre_key_signature=zeroblob(64)"}, ErrIncompatible},
		{"a second device", []string{"INSERT INTO device(lid,pn,registration_id,noise_key,identity_key,signed_pre_key,signed_pre_key_id,signed_pre_key_signature,adv_secret_key) SELECT lid,pn,registration_id,noise_key,identity_key,signed_pre_key,signed_pre_key_id,signed_pre_key_signature,adv_secret_key FROM device"}, ErrIncompatible},
		{"a mangled session", []string{"UPDATE sessions SET record=x'0a0300'"}, ErrIncompatible},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			source := fixtureCopy(t, "session.db", dir, "session.db")
			alter(t, source, c.alter...)
			before := tree(t, dir)
			_, err := ConvertSession(context.Background(), source, filepath.Join(dir, "whatsmeow.db"), key)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if after := tree(t, dir); !sameTree(before, after) {
				t.Fatalf("files changed: %v", after)
			}
		})
	}
	t.Run("without Zap's created_at column", func(t *testing.T) {
		dir := t.TempDir()
		source := fixtureCopy(t, "session.db", dir, "session.db")
		alter(t, source, "ALTER TABLE msg_secrets DROP COLUMN created_at")
		if _, err := ConvertSession(context.Background(), source, filepath.Join(dir, "whatsmeow.db"), key); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("an existing destination", func(t *testing.T) {
		dir := t.TempDir()
		source := fixtureCopy(t, "session.db", dir, "session.db")
		target := filepath.Join(dir, "whatsmeow.db")
		if err := os.WriteFile(target, []byte("someone else's"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ConvertSession(context.Background(), source, target, key); !errors.Is(err, ErrDestinationExists) {
			t.Fatalf("got %v", err)
		}
		if b, _ := os.ReadFile(target); string(b) != "someone else's" {
			t.Fatal("destination replaced")
		}
	})
}
