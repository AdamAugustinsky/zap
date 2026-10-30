// Ported from whatsapp-rust 5e72baf wacore/src/voip_control/group.rs (its
// unit tests), MIT.

package groupcall

import (
	"fmt"
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/types"

	"zapfast/gocore/voip/kdf"
)

func lid(user string) types.JID { return types.JID{User: user, Server: types.HiddenUserServer} }
func pnJID(user string) types.JID {
	return types.JID{User: user, Server: types.DefaultUserServer}
}
func withDevice(j types.JID, d uint16) types.JID { j.Device = d; return j }

func creator() types.JID { return withDevice(lid("100001"), 1) }

func participant(user, state string, pid uint32) Participant {
	return Participant{
		JID:   lid(user),
		State: state,
		Devices: []Device{{
			JID: withDevice(lid(user), 1), Platform: "web", PID: pid, HasPID: true,
		}},
	}
}

func update(tx uint32, users ...Participant) *Update {
	return &Update{
		CallID: "CALL", Creator: creator(), TransactionID: tx, Media: "video",
		ConnectedLimit: 32, Joinable: true, AVUpgradable: true, Participants: users,
	}
}

func waitingRoom(tx uint32, has bool) *WaitingRoom {
	return &WaitingRoom{
		CallID: "CALL", Creator: creator(), LinkToken: "TEST-CALL-LINK", Media: LinkAudio,
		Enabled: true, TransactionID: tx, HasTransactionID: has,
	}
}

func relayTx(tx uint32) *Relay {
	return &Relay{
		TransactionID: tx, HasTransactionID: true, SelfPID: 1, HasSelfPID: true,
		UUID: "RELAY-UUID", ParticipantUUID: "PARTICIPANT-UUID",
		WarpMITagLen: 16, HasWarpMITagLen: true,
	}
}

func withoutPID(p Participant) Participant {
	p.Devices = append([]Device(nil), p.Devices...)
	for i := range p.Devices {
		p.Devices[i].PID, p.Devices[i].HasPID = 0, false
	}
	return p
}

func expect(t *testing.T, got, want Apply, why string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", why, got, want)
	}
}

func pidOf(d Device) string {
	if !d.HasPID {
		return "none"
	}
	return fmt.Sprint(d.PID)
}

func TestSnapshotsAreTransactionOrderedAndClearDepartedControls(t *testing.T) {
	s := NewState("CALL", creator())
	alice := lid("200002")
	expect(t, s.ApplyUpdate(update(3, participant("100001", "connected", 1), participant("200002", "connected", 2))), Applied, "first")
	s.SetRaisedHand(alice, true)
	s.SetScreenShare(alice, ScreenShare{State: ScreenShareStarted, Version: 2, ID: 7, HasID: true})

	expect(t, s.ApplyUpdate(update(3, participant("100001", "connected", 1))), Stale, "same transaction")
	if !s.RaisedHands()[alice] {
		t.Fatal("a stale roster cleared a hand")
	}
	expect(t, s.ApplyUpdate(update(4, participant("100001", "connected", 1))), Applied, "newer")
	if len(s.RaisedHands()) != 0 || len(s.ScreenShares()) != 0 {
		t.Fatal("departed participant's controls survived")
	}
}

func TestAudioDowngradeClearsScreenShareState(t *testing.T) {
	s := NewState("CALL", creator())
	alice := lid("200002")
	expect(t, s.ApplyUpdate(update(1, participant("100001", "connected", 1), participant("200002", "connected", 2))), Applied, "first")
	s.SetScreenShare(alice, ScreenShare{State: ScreenShareStarted, Version: 2, ID: 7, HasID: true})
	audio := update(2, participant("100001", "connected", 1), participant("200002", "connected", 2))
	audio.Media = "audio"
	expect(t, s.ApplyUpdate(audio), Applied, "audio")
	if len(s.ScreenShares()) != 0 {
		t.Fatal("audio snapshots cannot retain a call-wide screen-share claim")
	}
}

func TestRosterOnlyUpdatesRetainTheLastUsableRelay(t *testing.T) {
	s := NewState("CALL", creator())
	relay := relayTx(1)
	relay.SelfPID = 7
	admitted := update(1, participant("100001", "connected", 1))
	admitted.Relay = relay
	expect(t, s.ApplyUpdate(admitted), Applied, "admitted")
	expect(t, s.ApplyUpdate(update(2, participant("100001", "connected", 1))), Applied, "roster only")
	if !reflect.DeepEqual(s.Snapshot().Relay, relay) {
		t.Fatal("an omitted relay is a roster-only update, not allocation revocation")
	}
}

func TestRelayTransactionIsIndependentOfTheRosterTransaction(t *testing.T) {
	s := NewState("CALL", creator())
	snap := update(13, participant("100001", "connected", 1))
	snap.Relay = relayTx(1)
	expect(t, s.ApplyUpdate(snap), Applied, "snapshot")
	if r := s.Snapshot().Relay; r == nil || r.TransactionID != 1 {
		t.Fatalf("relay = %v", r)
	}
}

func TestALateRelayOnAnOlderTransactionIsAdoptedWithoutRegressingTheRoster(t *testing.T) {
	s := NewState("CALL", creator())
	expect(t, s.ApplyUpdate(update(15, participant("100001", "connected", 1), participant("200002", "connected", 2))), Applied, "newer")
	older := update(13, participant("100001", "connected", 1))
	older.Relay = relayTx(1)
	expect(t, s.ApplyUpdate(older), Applied, "late relay")
	snap := s.Snapshot()
	if snap.TransactionID != 15 || len(snap.Participants) != 2 || snap.Relay == nil {
		t.Fatalf("the newer roster stays and the late relay is adopted: %+v", snap)
	}
	stale := update(14, participant("100001", "connected", 1))
	stale.Relay = relayTx(0)
	expect(t, s.ApplyUpdate(stale), Stale, "an older relay never replaces one already held")
}

func TestNewerRelayAllocationDoesNotDependOnRosterFreshness(t *testing.T) {
	s := NewState("CALL", creator())
	initial := update(15, participant("100001", "connected", 0))
	initial.Relay = relayTx(1)
	expect(t, s.ApplyUpdate(initial), Applied, "initial")
	olderRoster := update(13, participant("100001", "invited", 0))
	olderRoster.Relay = relayTx(2)
	expect(t, s.ApplyUpdate(olderRoster), Applied, "older roster, newer relay")
	cur := s.Snapshot()
	if cur.TransactionID != 15 || !reflect.DeepEqual(cur.Participants, initial.Participants) || !reflect.DeepEqual(cur.Relay, relayTx(2)) {
		t.Fatalf("current = %+v", cur)
	}
	newerRoster := update(16, initial.Clone().Participants...)
	newerRoster.Relay = relayTx(1)
	expect(t, s.ApplyUpdate(newerRoster), Applied, "newer roster")
	cur = s.Snapshot()
	if cur.TransactionID != 16 || !reflect.DeepEqual(cur.Relay, relayTx(2)) {
		t.Fatal("roster progress cannot roll relay allocation backwards")
	}
}

func TestLateRelayCannotBypassAnEstablishedGroupIdentity(t *testing.T) {
	for _, tx := range []uint32{13, 15, 16} {
		s := NewState("CALL", creator())
		current := update(15, participant("100001", "connected", 0))
		current.Group = types.JID{User: "100001-200002", Server: types.GroupServer}
		expect(t, s.ApplyUpdate(current), Applied, "current")
		conflicting := update(tx, current.Clone().Participants...)
		conflicting.Group = types.JID{User: "300003-400004", Server: types.GroupServer}
		conflicting.Relay = relayTx(1)
		expect(t, s.ApplyUpdate(conflicting), IdentityMismatch, "conflicting group")
		if !reflect.DeepEqual(s.Snapshot(), current) {
			t.Fatal("rejection is mutation-free")
		}
		matching := update(tx, current.Clone().Participants...)
		matching.Relay = relayTx(1)
		expect(t, s.ApplyUpdate(matching), Applied, "matching")
		c := s.Snapshot()
		if c.TransactionID != max(tx, 15) || c.Group != current.Group ||
			!reflect.DeepEqual(c.Participants, current.Participants) || !reflect.DeepEqual(c.Relay, relayTx(1)) {
			t.Fatalf("tx %d: committed = %+v", tx, c)
		}
	}
}

func TestRestoredPIDCollisionsDoNotConsumeTransactionsOrControls(t *testing.T) {
	for _, pid := range []uint32{0, 1} {
		s := NewState("CALL", creator())
		current := update(1, participant("100001", "connected", pid))
		expect(t, s.ApplyUpdate(current), Applied, "current")
		hand := current.Participants[0].JID
		s.SetRaisedHand(hand, true)
		share := ScreenShare{State: ScreenShareStarted, Version: 2, ID: 7, HasID: true}
		s.SetScreenShare(hand, share)
		collision := update(2, withoutPID(participant("100001", "connected", pid)), participant("200002", "connected", pid))
		collision.Media = "audio"
		collision.Relay = relayTx(9)
		expect(t, s.ApplyUpdate(collision), InvalidSnapshot, "collision")
		if !reflect.DeepEqual(s.Snapshot(), current) || !s.RaisedHands()[hand] || s.ScreenShares()[hand] != share {
			t.Fatal("a rejected snapshot changed the state")
		}
		corrected := update(2, withoutPID(participant("100001", "connected", pid)), participant("200002", "connected", pid+2))
		expect(t, s.ApplyUpdate(corrected), Applied, "corrected")
		if d := s.Snapshot().Participants[0].Devices[0]; !d.HasPID || d.PID != pid {
			t.Fatalf("pid = %s, want %d", pidOf(d), pid)
		}
		if !ValidateForMedia(s.Snapshot()) {
			t.Fatal("committed snapshot fails the media check")
		}
	}
}

func TestPIDRestorationUsesOnlyExplicitPNLIDDeviceMapping(t *testing.T) {
	l, pn := lid("200002"), pnJID("15550002002")
	for _, pair := range [][2]types.JID{{l, pn}, {pn, l}} {
		oldID, newID := pair[0], pair[1]
		s := NewState("CALL", creator())
		old := participant("200002", "connected", 0)
		old.PN = pn
		old.Devices[0].JID = withDevice(oldID, 1)
		expect(t, s.ApplyUpdate(update(1, old)), Applied, "old")
		next := withoutPID(participant("200002", "connected", 0))
		next.PN = pn
		next.Devices[0].JID = withDevice(newID, 1)
		expect(t, s.ApplyUpdate(update(2, next)), Applied, "aliased")
		d := s.Snapshot().Participants[0].Devices[0]
		if !d.HasPID || d.PID != 0 {
			t.Fatalf("pid = %s, want 0", pidOf(d))
		}
		if d.JID != next.Devices[0].JID {
			t.Fatal("PID continuity must not rewrite the media crypto namespace")
		}
		next = withoutPID(next)
		next.Devices[0].JID = withDevice(newID, 2)
		expect(t, s.ApplyUpdate(update(3, next)), Applied, "sibling")
		if d := s.Snapshot().Participants[0].Devices[0]; d.HasPID {
			t.Fatal("a sibling device cannot inherit the other device's PID")
		}
	}
	s := NewState("CALL", creator())
	expect(t, s.ApplyUpdate(update(1, participant("200002", "connected", 0))), Applied, "first")
	unrelated := withoutPID(participant("200002", "connected", 0))
	unrelated.JID = pnJID("200002")
	unrelated.Devices[0].JID = withDevice(unrelated.JID, 1)
	expect(t, s.ApplyUpdate(update(2, unrelated)), Applied, "unrelated")
	if s.Snapshot().Participants[0].Devices[0].HasPID {
		t.Fatal("equal user text in different namespaces is not an identity mapping")
	}
}

func TestPIDRestorationPrefersExplicitNamespacePIDsOverAliases(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		owner := participant("100001", "connected", 0)
		owner.PN = pnJID("200002")
		lidDevice := owner.Devices[0].JID
		pnDevice := owner.Devices[0]
		pnDevice.JID = withDevice(pnJID("200002"), lidDevice.Device)
		pnDevice.PID = 1
		owner.Devices = append(owner.Devices, pnDevice)
		if reverse {
			owner.Devices[0], owner.Devices[1] = owner.Devices[1], owner.Devices[0]
		}
		s := NewState("CALL", creator())
		expect(t, s.ApplyUpdate(update(1, owner)), Applied, "first")
		expect(t, s.ApplyUpdate(update(2, withoutPID(owner))), Applied, "roster only")
		for _, d := range s.Snapshot().Participants[0].Devices {
			want := uint32(0)
			if d.JID == pnDevice.JID {
				want = 1
			}
			if !d.HasPID || d.PID != want {
				t.Fatalf("reverse=%v %v: pid %s, want %d", reverse, d.JID, pidOf(d), want)
			}
		}
	}
}

func TestPIDZeroIsAValidParticipantID(t *testing.T) {
	s := NewState("CALL", creator())
	expect(t, s.ApplyUpdate(update(1, participant("100001", "connected", 0), participant("200002", "connected", 1))), Applied,
		"the call creator is pid 0 on live calls")
	expect(t, s.ApplyUpdate(update(2, participant("100001", "connected", 0), participant("200002", "connected", 0))), InvalidSnapshot,
		"pids stay unique")
}

func TestRosterUpdatesWithoutPIDsKeepTheKnownPIDs(t *testing.T) {
	s := NewState("CALL", creator())
	expect(t, s.ApplyUpdate(update(1, participant("100001", "connected", 0), participant("200002", "connected", 1))), Applied, "first")
	expect(t, s.ApplyUpdate(update(2,
		withoutPID(participant("100001", "connected", 0)),
		withoutPID(participant("200002", "connected", 1)),
		withoutPID(participant("300003", "invited", 9)))), Applied, "roster only")
	var got []string
	for _, p := range s.Snapshot().Participants {
		got = append(got, pidOf(p.Devices[0]))
	}
	if want := []string{"0", "1", "none"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("absent pids mean unchanged: got %v, want %v", got, want)
	}
}

func TestRosterOnlyUpdatesRetainGroupIdentityAndRejectRetagging(t *testing.T) {
	s := NewState("CALL", creator())
	group := types.JID{User: "1234567890-1111111111", Server: types.GroupServer}
	admitted := update(1, participant("100001", "connected", 1))
	admitted.Group = group
	expect(t, s.ApplyUpdate(admitted), Applied, "admitted")
	expect(t, s.ApplyUpdate(update(2, participant("100001", "connected", 1))), Applied, "roster only")
	if s.Snapshot().Group != group {
		t.Fatal("an omitted group_jid is a roster-only update, not an identity change")
	}
	conflicting := update(3, participant("100001", "connected", 1))
	conflicting.Group = types.JID{User: "9876543210-2222222222", Server: types.GroupServer}
	expect(t, s.ApplyUpdate(conflicting), IdentityMismatch, "retag")
	if s.Snapshot().TransactionID != 2 || s.Snapshot().Group != group {
		t.Fatal("a conflicting group_jid must not consume the transaction or retag the call")
	}
}

func TestInvalidIdentityDuplicatePIDDeviceAndOversizedLimitAreRejected(t *testing.T) {
	s := NewState("CALL", creator())
	wrong := update(1, participant("100001", "connected", 1))
	wrong.CallID = "OTHER"
	expect(t, s.ApplyUpdate(wrong), IdentityMismatch, "wrong call")

	expect(t, s.ApplyUpdate(update(1, participant("100001", "connected", 1), participant("200002", "connected", 1))), InvalidSnapshot, "duplicate pid")

	second := participant("200002", "connected", 2)
	second.Devices[0].JID = withDevice(lid("100001"), 1)
	expect(t, s.ApplyUpdate(update(1, participant("100001", "connected", 1), second)), InvalidSnapshot,
		"one device identity cannot belong to two roster participants")

	shared := pnJID("12025550111")
	first := participant("100001", "connected", 1)
	first.PN = shared
	second = participant("200002", "connected", 2)
	second.PN = shared
	expect(t, s.ApplyUpdate(update(1, first, second)), InvalidSnapshot, "one PN alias cannot resolve to two participants")

	canonicalPN := pnJID("12025550112")
	aliased := participant("100001", "connected", 1)
	aliased.PN = canonicalPN
	canonical := participant("200002", "connected", 2)
	canonical.JID = canonicalPN
	expect(t, s.ApplyUpdate(update(1, aliased, canonical)), InvalidSnapshot,
		"a PN alias cannot collide with another participant's canonical identity")

	oversized := update(1)
	oversized.ConnectedLimit = 33
	expect(t, s.ApplyUpdate(oversized), InvalidSnapshot, "oversized limit")

	over := update(1, participant("100001", "connected", 1), participant("200002", "connected", 2))
	over.ConnectedLimit = 1
	expect(t, s.ApplyUpdate(over), InvalidSnapshot, "the roster cannot exceed its connected limit")

	fanout := participant("100001", "connected", 1)
	fanout.Devices = nil
	for i := 1; i <= MaxParticipants+1; i++ {
		fanout.Devices = append(fanout.Devices, Device{
			JID: withDevice(lid(fmt.Sprintf("200%03d", i)), 1), Platform: "web", PID: uint32(i), HasPID: true,
		})
	}
	expect(t, s.ApplyUpdate(update(1, fanout)), InvalidSnapshot, "one user cannot expand the media registry past the limit")
}

func TestRosterDevicesMustBelongToTheirEnclosingParticipant(t *testing.T) {
	s := NewState("CALL", creator())
	mismatched := participant("100001", "connected", 1)
	mismatched.Devices[0].JID = withDevice(lid("200002"), 1)
	expect(t, s.ApplyUpdate(update(1, mismatched)), InvalidSnapshot, "unrelated device")

	aliased := participant("100001", "connected", 1)
	aliased.PN = pnJID("12025550113")
	aliased.Devices[0].JID = withDevice(aliased.PN, 2)
	expect(t, s.ApplyUpdate(update(1, aliased)), Applied, "a device on the participant's PN alias is valid")
}

func TestRouteCollisionsDoNotConsumeTheRosterTransaction(t *testing.T) {
	s := NewState("CALL", creator())
	first, second := "37774", "53838"
	a := ParticipantID(withDevice(lid(first), 1))
	b := ParticipantID(withDevice(lid(second), 1))
	if kdf.ParticipantSSRC("CALL", a, 0) != kdf.ParticipantSSRC("CALL", b, 0) {
		t.Fatal("fixture must collide in the audio route")
	}
	expect(t, s.ApplyUpdate(update(1, participant(first, "connected", 1), participant(second, "connected", 2))), InvalidSnapshot, "collision")
	expect(t, s.ApplyUpdate(update(1, participant(first, "connected", 1))), Applied,
		"a corrected redelivery must retain the rejected transaction ID")
}

func TestStoppedScreenShareAndLoweredHandRemoveState(t *testing.T) {
	s := NewState("CALL", creator())
	alice := withDevice(lid("200002"), 3)
	s.SetRaisedHand(alice, true)
	s.SetScreenShare(alice, ScreenShare{State: ScreenShareStarted, Version: 2, ID: 9, HasID: true})
	if len(s.RaisedHands()) != 1 || len(s.ScreenShares()) != 1 {
		t.Fatal("controls not recorded")
	}
	s.SetRaisedHand(alice, false)
	s.SetScreenShare(alice, ScreenShare{State: ScreenShareStopped, Version: 2})
	if len(s.RaisedHands()) != 0 || len(s.ScreenShares()) != 0 {
		t.Fatal("controls not removed")
	}
}

func TestWaitingRoomIdentityAndTransactionOrderAreEnforced(t *testing.T) {
	s := NewState("CALL", creator())
	expect(t, s.ApplyWaitingRoom(waitingRoom(4, true)), Applied, "4")
	expect(t, s.ApplyWaitingRoom(waitingRoom(3, true)), Stale, "3")
	expect(t, s.ApplyWaitingRoom(waitingRoom(0, false)), Applied, "transaction-less service updates always apply")
	expect(t, s.ApplyWaitingRoom(waitingRoom(3, true)), Stale, "transaction-less updates must not erase the watermark")

	wrongCall := waitingRoom(5, true)
	wrongCall.CallID = "OTHER"
	expect(t, s.ApplyWaitingRoom(wrongCall), IdentityMismatch, "call")
	wrongCreator := waitingRoom(5, true)
	wrongCreator.Creator = lid("999999")
	expect(t, s.ApplyWaitingRoom(wrongCreator), IdentityMismatch, "creator")
	wrongLink := waitingRoom(5, true)
	wrongLink.LinkToken = "OTHER-CALL-LINK"
	expect(t, s.ApplyWaitingRoom(wrongLink), IdentityMismatch, "link")
	wrongMedia := waitingRoom(5, true)
	wrongMedia.Media = LinkVideo
	expect(t, s.ApplyWaitingRoom(wrongMedia), IdentityMismatch, "media")
	expect(t, s.ApplyWaitingRoom(waitingRoom(5, true)), Applied, "conflicts must not consume the watermark")
	if !s.SetWaitingRoomEnabled(false) || s.WaitingRoom().Enabled {
		t.Fatal("waiting room not disabled")
	}
}

func TestPNAliasControlsAreCanonicalizedToTheCurrentRosterJID(t *testing.T) {
	s := NewState("CALL", creator())
	alice := participant("200002", "connected", 2)
	aliceJID := alice.JID
	alicePN := pnJID("12025550111")
	alice.PN = alicePN
	initial := alice
	initial.Devices = append([]Device(nil), alice.Devices...)
	initial.JID, initial.PN = alicePN, types.JID{}
	initial.Devices[0].JID = withDevice(alicePN, 2)
	expect(t, s.ApplyUpdate(update(1, participant("100001", "connected", 1), initial)), Applied, "first")
	s.SetRaisedHand(alicePN, true)
	s.SetScreenShare(alicePN, ScreenShare{State: ScreenShareStarted, Version: 2, ID: 7, HasID: true})
	expect(t, s.ApplyUpdate(update(2, participant("100001", "connected", 1), alice)), Applied, "lid roster")
	if !s.RaisedHands()[aliceJID] || s.RaisedHands()[alicePN] {
		t.Fatal("hand not canonicalized to the LID")
	}
	if _, ok := s.ScreenShares()[aliceJID]; !ok {
		t.Fatal("share not canonicalized to the LID")
	}
	if _, ok := s.ScreenShares()[alicePN]; ok {
		t.Fatal("share kept under the PN")
	}
}

func TestDeviceIsLocalAndRelayValidation(t *testing.T) {
	local := withDevice(lid("100001"), 3)
	p := participant("100001", "connected", 1)
	p.PN = pnJID("15550001")
	p.Devices = append(p.Devices, Device{JID: withDevice(p.PN, 3), PID: 2, HasPID: true}, Device{JID: withDevice(lid("100001"), 3), PID: 3, HasPID: true})
	if DeviceIsLocal(&p, &p.Devices[0], local) {
		t.Fatal("another device id is not local")
	}
	if !DeviceIsLocal(&p, &p.Devices[1], local) || !DeviceIsLocal(&p, &p.Devices[2], local) {
		t.Fatal("the local device under its LID or PN alias is local")
	}
	other := participant("200002", "connected", 4)
	other.Devices[0].JID.Device = 3
	if DeviceIsLocal(&other, &other.Devices[0], local) {
		t.Fatal("the same device id on another account is not local")
	}

	u := update(1, participant("100001", "connected", 1))
	if !ValidateRelay(u) {
		t.Fatal("no relay is valid")
	}
	u.Relay = relayTx(1)
	u.Relay.Key = []byte{1}
	u.Relay.Tokens = [][]byte{{9}}
	u.Relay.Endpoints = []RelayEndpoint{
		{TokenID: 0, IPv4: "10.0.0.1", Port: 3478},
		{TokenID: 0, IPv4: "10.0.0.2", Port: WebClientRelayPort},
	}
	if e, ok := u.Relay.MediaEndpoint(); !ok || e.IPv4 != "10.0.0.2" || !ValidateRelay(u) {
		t.Fatal("the web client port is preferred")
	}
	u.Relay.WarpMITagLen = 21
	if ValidateRelay(u) {
		t.Fatal("tag length over 20 accepted")
	}
	u.Relay.WarpMITagLen = 16
	u.Relay.Endpoints = []RelayEndpoint{{TokenID: 1, IPv4: "10.0.0.1", Port: 3478}}
	if ValidateRelay(u) {
		t.Fatal("an endpoint without a token accepted")
	}
}
