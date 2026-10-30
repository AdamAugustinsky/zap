// Ported from whatsapp-rust 5e72baf wacore/src/voip/group_media.rs (its
// unit tests), MIT.

package engine

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"go.mau.fi/whatsmeow/types"

	"zapfast/gocore/voip/groupcall"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/rtcp"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/warp"
)

func lidDevice(user string, d uint16) types.JID {
	return types.JID{User: user, Server: types.HiddenUserServer, Device: d}
}

func rosterDevice(user string, d uint16, pid uint32) groupcall.Participant {
	return groupcall.Participant{
		JID: types.JID{User: user, Server: types.HiddenUserServer}, State: "connected",
		Devices: []groupcall.Device{{JID: lidDevice(user, d), PID: pid, HasPID: true}},
	}
}

func rosterUpdate(tx uint32, ps ...groupcall.Participant) *groupcall.Update {
	return &groupcall.Update{
		CallID: "CALL", Creator: lidDevice("100001", 1), TransactionID: tx, Media: "video",
		ConnectedLimit: 32, Joinable: true, AVUpgradable: true, Participants: ps,
	}
}

func testRegistry(t *testing.T) *GroupMediaRegistry {
	t.Helper()
	r, err := NewGroupMediaRegistry("CALL", lidDevice("100001", 1), lidDevice("100001", 1), 960, warp.MITagLen, rtp.VideoTSStride15FPS)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func epochOf(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// peerSender is a participant's own audio send side.
func peerSender(t *testing.T, callID string, key []byte, peer types.JID) *mediaPipeline {
	t.Helper()
	id := kdf.FormatParticipantID(peer.String())
	var cname [rtcp.WhatsAppCNameLen]byte
	p, ok := newMediaPipeline(key, id, "100001:1@lid", kdf.ParticipantSSRC(callID, id, 0), 960, warp.MITagLen, cname)
	if !ok {
		t.Fatal("sender")
	}
	return p
}

func peerVideoSender(t *testing.T, key []byte, peer types.JID) *videoPipeline {
	t.Helper()
	id := kdf.FormatParticipantID(peer.String())
	var cname [rtcp.WhatsAppCNameLen]byte
	v, ok := newVideoPipeline(key, peer.String(), "100001:1@lid", kdf.VideoParticipantSSRC("CALL", id), rtp.VideoTSStride15FPS, warp.MITagLen, cname)
	if !ok {
		t.Fatal("video sender")
	}
	return v
}

func mustApply(t *testing.T, r *GroupMediaRegistry, u *groupcall.Update) {
	t.Helper()
	if _, err := r.ApplyGroupUpdate(u); err != nil {
		t.Fatal(err)
	}
}

func TestCreatorReceiverPIDZeroSurvivesAPIDlessRosterAndDecryptsMedia(t *testing.T) {
	epoch := epochOf(0x42)
	local, creator := lidDevice("100001", 1), lidDevice("200002", 2)
	first := rosterUpdate(7, rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 0))
	first.Creator = creator
	state := groupcall.NewState("CALL", creator)
	if state.ApplyUpdate(first) != groupcall.Applied {
		t.Fatal("state")
	}
	media, _ := NewGroupMediaRegistry("CALL", creator, local, 960, warp.MITagLen, rtp.VideoTSStride15FPS)
	if r, _ := media.ApplyRawEpoch(7, epoch); r != EpochBuffered {
		t.Fatalf("epoch %v", r)
	}
	mustApply(t, media, state.Snapshot())
	if !slices.Equal(media.activePIDs(), []uint32{0}) {
		t.Fatalf("the creator is a genuine remote receiver: %v", media.activePIDs())
	}
	payload := []byte{0x50, 1, 2, 3, 4, 5, 6, 7}
	sender := peerSender(t, "CALL", epoch, creator)
	if pm, rej := media.unprotectAudio(sender.protectAudio(payload)); rej != rejectNone || !bytes.Equal(pm.Payload, payload) {
		t.Fatalf("reject %v", rej)
	}
	next := first.Clone()
	next.TransactionID = 8
	for i := range next.Participants {
		for j := range next.Participants[i].Devices {
			next.Participants[i].Devices[j].HasPID = false
		}
	}
	if state.ApplyUpdate(next) != groupcall.Applied {
		t.Fatal("state next")
	}
	mustApply(t, media, state.Snapshot())
	if tx, ok := media.InstalledEpochTransaction(); !ok || tx != 7 || !slices.Equal(media.activePIDs(), []uint32{0}) {
		t.Fatal("epoch or pid lost")
	}
	pm, rej := media.unprotectAudio(sender.protectAudio(payload))
	if rej != rejectNone || pm.Device != creator || !bytes.Equal(pm.Payload, payload) {
		t.Fatalf("crypto keeps the authoritative LID device namespace: %v %v", rej, pm.Device)
	}
}

func TestFutureEpochActivatesWhenRosterArrivesAndRoutesBySSRC(t *testing.T) {
	epoch := epochOf(0x42)
	r := testRegistry(t)
	if a, _ := r.ApplyRawEpoch(7, epoch); a != EpochBuffered {
		t.Fatal("not buffered")
	}
	mustApply(t, r, rosterUpdate(7, rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 2), rosterDevice("300003", 3, 3)))
	if tx, ok := r.InstalledEpochTransaction(); !ok || tx != 7 {
		t.Fatal("epoch not installed")
	}
	payload := []byte{0x50, 1, 2, 3, 4, 5, 6, 7}
	for _, peer := range []types.JID{lidDevice("200002", 2), lidDevice("300003", 3)} {
		pm, rej := r.unprotectAudio(peerSender(t, "CALL", epoch, peer).protectAudio(payload))
		if rej != rejectNone || pm.Device != peer || !bytes.Equal(pm.Payload, payload) {
			t.Fatalf("%v: %v", peer, rej)
		}
	}
	if _, rej := r.unprotectAudio(peerSender(t, "CALL", epoch, lidDevice("400004", 4)).protectAudio(payload)); rej != rejectUnroutable {
		t.Fatalf("an unknown participant must be unroutable: %v", rej)
	}
}

func TestFirstEmptyGroupRosterRemovesTheSeededDirectPeer(t *testing.T) {
	epoch, peer := epochOf(0x42), lidDevice("200002", 2)
	sender := peerSender(t, "CALL", epoch, peer)
	r := testRegistry(t)
	if err := r.seedDirectPeer(epoch, peer.ToNonAD(), peer, true); err != nil {
		t.Fatal(err)
	}
	if _, rej := r.unprotectAudio(sender.protectAudio(bytes.Repeat([]byte{0x50}, 20))); rej != rejectNone {
		t.Fatal("the direct fallback is active before the first roster")
	}
	mustApply(t, r, rosterUpdate(1, rosterDevice("100001", 1, 1)))
	if len(r.ActiveParticipantIDs()) != 0 {
		t.Fatal("receivers left")
	}
	if _, rej := r.unprotectAudio(sender.protectAudio(bytes.Repeat([]byte{0x51}, 20))); rej == rejectNone {
		t.Fatal("the first roster must revoke a departed direct peer")
	}
}

func TestSeededDirectPeerKeepsKeysWhenAdoptingItsFirstPID(t *testing.T) {
	epoch, peer := epochOf(0x43), lidDevice("200002", 2)
	sender := peerSender(t, "CALL", epoch, peer)
	r := testRegistry(t)
	if err := r.seedDirectPeer(epoch, peer.ToNonAD(), peer, true); err != nil {
		t.Fatal(err)
	}
	if _, rej := r.unprotectAudio(sender.protectAudio(bytes.Repeat([]byte{0x50}, 20))); rej != rejectNone {
		t.Fatal("seeded peer")
	}
	mustApply(t, r, rosterUpdate(1, rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 2)))
	if _, rej := r.unprotectAudio(sender.protectAudio(bytes.Repeat([]byte{0x51}, 20))); rej != rejectNone {
		t.Fatal("None-to-Some PID adoption must keep the authenticated receiver")
	}
}

func TestCollidingRoutesDoNotAdvanceOrReplaceTheRoster(t *testing.T) {
	r := testRegistry(t)
	mustApply(t, r, rosterUpdate(1, rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 2)))
	if _, err := r.ApplyRawEpoch(1, epochOf(0x42)); err != nil {
		t.Fatal(err)
	}
	previous := r.ActiveParticipantIDs()
	for _, c := range []struct {
		route         string
		slot          uint32
		first, second string
	}{
		{"audio", 0, "37774", "53838"},
		{"video", kdf.VideoSSRCSlotWord, "52371", "59782"},
		{"app-data", kdf.AppDataSSRCSlotWord, "19671", "43135"},
		{"RTCP", 1, "25851", "41568"},
	} {
		a := kdf.FormatParticipantID(lidDevice(c.first, 1).String())
		b := kdf.FormatParticipantID(lidDevice(c.second, 1).String())
		if kdf.ParticipantSSRC("CALL", a, c.slot) != kdf.ParticipantSSRC("CALL", b, c.slot) {
			t.Fatalf("%s fixture no longer collides", c.route)
		}
		_, err := r.ApplyGroupUpdate(rosterUpdate(2, rosterDevice("100001", 1, 1), rosterDevice(c.first, 1, 2), rosterDevice(c.second, 1, 3)))
		if !errors.Is(err, ErrGroupInvalidSnapshot) {
			t.Fatalf("%s collision accepted: %v", c.route, err)
		}
		if r.rosterTx != 1 || !slices.Equal(r.ActiveParticipantIDs(), previous) {
			t.Fatalf("%s collision changed the roster", c.route)
		}
	}
}

func TestSameDeviceReceiverSurvivesRosterUpdateAndDepartureRemovesRoute(t *testing.T) {
	epoch := epochOf(0x24)
	sender := peerSender(t, "CALL", epoch, lidDevice("200002", 2))
	r := testRegistry(t)
	mustApply(t, r, rosterUpdate(2, rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 2)))
	if _, err := r.ApplyRawEpoch(2, epoch); err != nil {
		t.Fatal(err)
	}
	if _, rej := r.unprotectAudio(sender.protectAudio(bytes.Repeat([]byte{0x50}, 20))); rej != rejectNone {
		t.Fatal("first")
	}
	mustApply(t, r, rosterUpdate(3, rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 2), rosterDevice("300003", 3, 3)))
	if _, rej := r.unprotectAudio(sender.protectAudio(bytes.Repeat([]byte{0x51}, 20))); rej != rejectNone {
		t.Fatal("receiver lost on a roster update")
	}
	mustApply(t, r, rosterUpdate(4, rosterDevice("100001", 1, 1)))
	if _, rej := r.unprotectAudio(sender.protectAudio(bytes.Repeat([]byte{0x52}, 20))); rej == rejectNone {
		t.Fatal("a departed participant still routes")
	}
}

func TestDevicePIDChangeRebuildsItsReceiveSession(t *testing.T) {
	epoch, alice := epochOf(0x25), lidDevice("200002", 2)
	ps := []groupcall.Participant{rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 2)}
	r := testRegistry(t)
	mustApply(t, r, rosterUpdate(1, ps...))
	if _, err := r.ApplyRawEpoch(1, epoch); err != nil {
		t.Fatal(err)
	}
	first := peerSender(t, "CALL", epoch, alice)
	for range 3 {
		if _, rej := r.unprotectAudio(first.protectAudio(bytes.Repeat([]byte{0x50}, 20))); rej != rejectNone {
			t.Fatal("first session")
		}
	}
	migrated := rosterUpdate(2, rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 9))
	mustApply(t, r, migrated)
	if _, rej := r.unprotectAudio(peerSender(t, "CALL", epoch, alice).protectAudio(bytes.Repeat([]byte{0x51}, 20))); rej != rejectNone {
		t.Fatal("a new PID must start with fresh ROC and replay state")
	}
}

func anyVideo(r *GroupMediaRegistry, packets [][]byte, want func(participantVideo) bool) bool {
	for _, p := range packets {
		if v, ok := r.unprotectVideo(p); ok && want(v) {
			return true
		}
	}
	return false
}

func TestAudioOnlyRosterUpdateDisablesRetainedVideoReceiver(t *testing.T) {
	epoch, alice := epochOf(0x35), lidDevice("200002", 2)
	ps := []groupcall.Participant{rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 2)}
	r := testRegistry(t)
	mustApply(t, r, rosterUpdate(1, ps...))
	if _, err := r.ApplyRawEpoch(1, epoch); err != nil {
		t.Fatal(err)
	}
	sender := peerVideoSender(t, epoch, alice)
	au := []byte{0, 0, 0, 1, 0x65, 0x88, 0x84, 0x21}
	all := func(participantVideo) bool { return true }
	if !anyVideo(r, sender.protectVideo(au), all) {
		t.Fatal("initial video")
	}
	audio := rosterUpdate(2, ps...)
	audio.Media = "audio"
	mustApply(t, r, audio)
	if anyVideo(r, sender.protectVideo(au), all) {
		t.Fatal("video must be dropped while the roster's media is audio")
	}
	mustApply(t, r, rosterUpdate(3, ps...))
	if !anyVideo(r, sender.protectVideo(au), all) {
		t.Fatal("a later video roster may reactivate the retained receiver")
	}
}

func TestAudioDowngradeDiscardsPartialVideoAccessUnits(t *testing.T) {
	epoch, alice := epochOf(0x35), lidDevice("200002", 2)
	ps := []groupcall.Participant{rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 2)}
	r := testRegistry(t)
	mustApply(t, r, rosterUpdate(1, ps...))
	if _, err := r.ApplyRawEpoch(1, epoch); err != nil {
		t.Fatal(err)
	}
	sender := peerVideoSender(t, epoch, alice)
	big := append([]byte{0, 0, 0, 1, 0x65}, bytes.Repeat([]byte{0x88}, 3000)...)
	packets := sender.protectVideo(big)
	if len(packets) < 2 {
		t.Fatal("fixture must fragment")
	}
	for _, p := range packets[:len(packets)-1] {
		if v, ok := r.unprotectVideo(p); !ok || len(v.units) != 0 {
			t.Fatal("fragment")
		}
	}
	audio := rosterUpdate(2, ps...)
	audio.Media = "audio"
	mustApply(t, r, audio)
	mustApply(t, r, rosterUpdate(3, ps...))
	if v, ok := r.unprotectVideo(packets[len(packets)-1]); !ok || len(v.units) != 0 {
		t.Fatal("reactivation completed an access unit from before the downgrade")
	}
	fresh := []byte{0, 0, 0, 1, 0x65, 0x88, 0x84, 0x21}
	if !anyVideo(r, sender.protectVideo(fresh), func(v participantVideo) bool { return len(v.units) > 0 }) {
		t.Fatal("the depacketizer reset must keep the SRTP state")
	}
}

func TestDuplicateAndConflictingEpochsAreDistinguished(t *testing.T) {
	r := testRegistry(t)
	mustApply(t, r, rosterUpdate(5, rosterDevice("200002", 2, 2)))
	for _, c := range []struct {
		tx    uint32
		epoch []byte
		want  GroupEpochApply
		err   error
	}{
		{5, epochOf(0x11), EpochInstalled, nil},
		{5, epochOf(0x11), EpochDuplicate, nil},
		{5, epochOf(0x22), 0, ErrGroupConflictingEpoch},
		{4, epochOf(0x33), EpochStale, nil},
	} {
		got, err := r.ApplyRawEpoch(c.tx, c.epoch)
		if !errors.Is(err, c.err) || (err == nil && got != c.want) {
			t.Fatalf("tx %d: %v %v", c.tx, got, err)
		}
	}
	if _, err := r.ApplyRawEpoch(6, make([]byte, 31)); !errors.Is(err, ErrGroupInvalidEpoch) {
		t.Fatal("a short epoch accepted")
	}
}

func TestFutureEpochBufferPreservesTheNearestUsableTransaction(t *testing.T) {
	r := testRegistry(t)
	for tx := uint32(100); tx < 100+maxBufferedEpochs; tx++ {
		if a, _ := r.ApplyRawEpoch(tx, epochOf(byte(tx))); a != EpochBuffered {
			t.Fatal("not buffered")
		}
	}
	if a, _ := r.ApplyRawEpoch(1, epochOf(0x42)); a != EpochBuffered {
		t.Fatal("next epoch not buffered")
	}
	if _, has := r.pending[1]; len(r.pending) != maxBufferedEpochs || !has {
		t.Fatal("a legitimate next epoch must displace the farthest")
	}
	if _, has := r.pending[100+maxBufferedEpochs-1]; has {
		t.Fatal("the farthest stayed")
	}
	mustApply(t, r, rosterUpdate(1, rosterDevice("200002", 2, 2)))
	if tx, ok := r.InstalledEpochTransaction(); !ok || tx != 1 {
		t.Fatal("far-future epochs must not suppress the next key")
	}
}

func TestVideoRoutesAreParticipantSpecificForMultipleDevices(t *testing.T) {
	epoch := epochOf(0x57)
	r := testRegistry(t)
	mustApply(t, r, rosterUpdate(1, rosterDevice("100001", 1, 1), rosterDevice("200002", 2, 2), rosterDevice("300003", 3, 3)))
	if _, err := r.ApplyRawEpoch(1, epoch); err != nil {
		t.Fatal(err)
	}
	au := []byte{0, 0, 0, 1, 0x65, 0x88, 0x84, 0x21}
	for _, peer := range []types.JID{lidDevice("200002", 2), lidDevice("300003", 3)} {
		found := false
		for _, p := range peerVideoSender(t, epoch, peer).protectVideo(au) {
			v, ok := r.unprotectVideo(p)
			if !ok {
				continue
			}
			found = true
			if v.Device != peer || len(v.units) != 1 || v.units[0].timestamp != v.Header.Timestamp || !bytes.Equal(v.units[0].au, au) {
				t.Fatalf("%v: %+v", peer, v)
			}
		}
		if !found {
			t.Fatalf("%v: no video", peer)
		}
	}
}

func TestPIDMigrationRule(t *testing.T) {
	for _, c := range []struct {
		e     uint32
		he    bool
		i     uint32
		hi    bool
		moved bool
	}{{0, false, 0, false, false}, {0, false, 7, true, false}, {7, true, 7, true, false}, {7, true, 8, true, true}, {7, true, 0, false, true}} {
		if pidMigrated(c.e, c.he, c.i, c.hi) != c.moved {
			t.Fatalf("%+v", c)
		}
	}
}
