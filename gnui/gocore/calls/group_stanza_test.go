// Ported from whatsapp-rust 5e72baf wacore/src/stanza/group_call.rs (its
// unit tests), MIT.

package calls

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"

	"zapfast/gocore/voip/groupcall"
)

// testdata/group_golden.json was written by a throwaway harness (outside
// the repository, deleted after) against whatsapp-rust 5e72baf's
// wacore/src/stanza/group_call.rs builders and wacore-binary's marshal. The
// "in_*" entries are whatsapp-rust's own test fixtures (group_call.rs
// tests) packed as wire bytes; "parsed_*" are its parsers' serde reading of
// them. Every id, key and number is synthetic.

func lidDev(user string, d uint16) types.JID {
	return types.JID{User: user, Server: types.HiddenUserServer, Device: d}
}

func lidUser(user string) types.JID { return types.JID{User: user, Server: types.HiddenUserServer} }

func groupParticipant(user string, d uint16, capability []byte, state string) groupcall.Participant {
	return groupcall.Participant{JID: lidUser(user), State: state, Devices: []groupcall.Device{{
		JID: lidDev(user, d), CapabilityVersion: 1, HasCapabilityVersion: true, Capability: capability,
	}}}
}

func must(t *testing.T, n waBinary.Node, err error) waBinary.Node {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func groupGoldenStanzas(t *testing.T) map[string]waBinary.Node {
	me := lidDev("100000000001001", 7)
	group := types.JID{User: "120363000000000001", Server: types.GroupServer}
	roster := []groupcall.Participant{
		groupParticipant("100000000001001", 7, CapabilityOffer, ""),
		groupParticipant("100000000002002", 3, CapabilityOffer, ""),
		groupParticipant("100000000003003", 0, CapabilityStandardOpusOffer, ""),
	}
	std := []groupcall.Participant{
		groupParticipant("100000000001001", 7, CapabilityStandardOpusOffer, ""),
		groupParticipant("100000000002002", 3, CapabilityOffer, "connected"),
		groupParticipant("100000000003003", 0, CapabilityOffer, "invited"),
	}
	creator := lidDev("100000000002002", 3)
	target := lidUser("100000000004004")
	targets := []types.JID{lidDev("100000000004004", 0), lidDev("100000000004004", 2)}
	ct := func(b byte) []byte { return []byte(strings.Repeat(string(rune(b)), 24)) }
	msgKey := DeviceKey{Device: lidDev("100000000003003", 0), Ciphertext: ct(0x31), Type: "msg"}
	pkKey := DeviceKey{Device: lidDev("100000000003003", 0), Ciphertext: ct(0x32), Type: "pkmsg"}
	user := lidUser("100000000005005")
	call := CallJID(goldenCallID)

	out := map[string]waBinary.Node{}
	add := func(name string, n waBinary.Node, err error) { out[name] = must(t, n, err) }
	n, err := BuildInitialGroupOffer(InitialGroupOfferParams{CallID: goldenCallID, ID: "GOFFER-1", Creator: me, Group: group,
		Participants: roster, AudioRate: 16000, Video: true})
	add("group_offer_initial_video", n, err)
	n, err = BuildInitialGroupOffer(InitialGroupOfferParams{CallID: goldenCallID, ID: "GOFFER-2", Creator: me,
		Participants: std, AudioRate: 16000})
	add("group_offer_initial_audio_adhoc", n, err)
	n, err = BuildInitialGroupOffer(InitialGroupOfferParams{CallID: goldenCallID, ID: "GOFFER-3", Creator: me,
		Participants: std, AudioRate: 16000, Video: true})
	add("group_offer_initial_video_standard_opus", n, err)
	n, err = BuildGroupInviteOffer(GroupInviteOfferParams{CallID: goldenCallID, ID: "GINVITE-1", To: target, Creator: creator,
		TargetDevices: targets, Participants: std, Video: true, DeviceOrientation: 2})
	add("group_invite_offer_video", n, err)
	n, err = BuildActiveGroupPreaccept(goldenCallID, creator, "GPRE-1", true)
	add("group_preaccept_active_video", n, err)
	n, err = BuildActiveGroupPreaccept(goldenCallID, creator, "GPRE-2", false)
	add("group_preaccept_active_audio", n, err)
	n, err = BuildActiveGroupAccept(goldenCallID, creator, "GACC-1", false)
	add("group_accept_active_audio", n, err)
	n, err = BuildActiveGroupAccept(goldenCallID, creator, "GACC-2", true)
	add("group_accept_active_video", n, err)
	n, err = BuildGroupEncRekey(GroupEncRekeyParams{CallID: goldenCallID, ID: "REKEY-1", To: msgKey.Device, Creator: creator,
		TransactionID: 9, Key: msgKey})
	add("group_enc_rekey_msg", n, err)
	n, err = BuildGroupEncRekey(GroupEncRekeyParams{CallID: goldenCallID, ID: "REKEY-2", To: pkKey.Device, Creator: creator,
		TransactionID: 10, Key: pkKey, DeviceIdentity: []byte("device-identity")})
	add("group_enc_rekey_pkmsg", n, err)
	n, err = BuildCallLinkCreate(groupcall.LinkVideo, "LINK-1")
	add("call_link_create_video", n, err)
	n, err = BuildCallLinkQuery("TESTLINKTOKEN", groupcall.LinkAudio, "LINK-2")
	add("call_link_query_audio", n, err)
	n, err = BuildCallLinkJoin("TESTLINKTOKEN", groupcall.LinkVideo, "LINK-3")
	add("call_link_join_video", n, err)
	n, err = BuildCallLinkJoinWithCapability("TESTLINKTOKEN", groupcall.LinkAudio, "LINK-4", CapabilityStandardOpusOffer)
	add("call_link_join_audio_standard_opus", n, err)
	n, err = BuildWaitingRoomToggle(goldenCallID, creator, true, "WR-1")
	add("waiting_room_toggle_on", n, err)
	n, err = BuildWaitingRoomHeartbeat(goldenCallID, creator, "WR-2")
	add("waiting_room_heartbeat", n, err)
	n, err = BuildWaitingRoomAdmit(goldenCallID, creator, user, "WR-3")
	add("waiting_room_admit", n, err)
	n, err = BuildWaitingRoomDeny(goldenCallID, creator, user, "WR-4")
	add("waiting_room_deny", n, err)
	n, err = BuildRaiseHand(goldenCallID, call, creator, "HAND-1", true)
	add("raise_hand_up", n, err)
	n, err = BuildRaiseHand(goldenCallID, call, creator, "HAND-2", false)
	add("raise_hand_down", n, err)
	n, err = BuildScreenShare(goldenCallID, call, creator, "SHARE-1", groupcall.ScreenShareStarted, 7, true)
	add("screen_share_started", n, err)
	n, err = BuildScreenShare(goldenCallID, call, creator, "SHARE-2", groupcall.ScreenShareStopped, 0, false)
	add("screen_share_stopped", n, err)
	original := waBinary.Node{Tag: "call", Attrs: waBinary.Attrs{"id": "CTRL-1", "from": call, "participant": creator, "recipient": me}}
	ack, ok := BuildCallControlAck(&original, "screen_share")
	if !ok {
		t.Fatal("no control ack")
	}
	out["call_control_ack_screen_share"] = ack
	return out
}

func readGroupGolden(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("testdata/group_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]json.RawMessage
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

func goldenNode(t *testing.T, golden map[string]json.RawMessage, name string) *waBinary.Node {
	t.Helper()
	var h string
	if err := json.Unmarshal(golden[name], &h); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	return decode(t, b)
}

func TestGroupStanzasMatchWhatsappRustBuilders(t *testing.T) {
	golden := readGroupGolden(t)
	ours := groupGoldenStanzas(t)
	var names []string
	for name := range golden {
		if !strings.HasPrefix(name, "in_") && !strings.HasPrefix(name, "parsed_") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) != len(ours) {
		t.Errorf("golden has %d stanzas, the Go builders %d", len(names), len(ours))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			n, ok := ours[name]
			if !ok {
				t.Fatalf("no Go stanza for %s", name)
			}
			packed, err := waBinary.Marshal(n)
			if err != nil {
				t.Fatal(err)
			}
			want, got := goldenNode(t, golden, name), decode(t, packed)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("the Go stanza differs from whatsapp-rust's:\n rust %s\n go   %s", want.String(), got.String())
			}
		})
	}
}

// rustShape is u in the serde shape of wacore's GroupCallUpdate (None
// fields skipped, secrets not serialized).
func rustShape(u *groupcall.Update) map[string]any {
	jid := func(j types.JID) map[string]any {
		return map[string]any{"agent": j.RawAgent, "device": j.Device, "integrator": j.Integrator, "server": j.Server, "user": j.User}
	}
	var ps []any
	for _, p := range u.Participants {
		var ds []any
		for _, d := range p.Devices {
			m := map[string]any{"jid": jid(d.JID)}
			if d.HasPID {
				m["pid"] = d.PID
			}
			if d.Platform != "" {
				m["platform"] = d.Platform
			}
			ds = append(ds, m)
		}
		m := map[string]any{"jid": jid(p.JID), "devices": ds}
		if p.State != "" {
			m["state"] = p.State
		}
		ps = append(ps, m)
	}
	out := map[string]any{
		"av_upgradable": u.AVUpgradable, "call_creator": jid(u.Creator), "call_id": u.CallID,
		"connected_limit": u.ConnectedLimit, "joinable": u.Joinable, "media": u.Media,
		"participants": ps, "rekey_requested": u.RekeyRequested, "transaction_id": u.TransactionID,
	}
	if r := u.Relay; r != nil {
		var es []any
		for _, e := range r.Endpoints {
			es = append(es, map[string]any{"auth_token_id": e.AuthTokenID, "ipv4": e.IPv4, "is_fna": e.FNA, "port": e.Port,
				"relay_id": e.RelayID, "relay_name": e.Name, "rtt_ms": e.RTTms, "token_id": e.TokenID})
		}
		out["relay"] = map[string]any{"attribute_padding": r.AttributePadding, "endpoints": es,
			"participant_uuid": r.ParticipantUUID, "self_pid": r.SelfPID, "transaction_id": r.TransactionID,
			"uuid": r.UUID, "warp_mi_tag_len": r.WarpMITagLen}
	}
	return out
}

func normalize(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGroupParsersMatchWhatsappRustOnWireBytes(t *testing.T) {
	golden := readGroupGolden(t)
	u, err := ParseGroupUpdate(goldenNode(t, golden, "in_group_update"))
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal(golden["parsed_in_group_update"], &want); err != nil {
		t.Fatal(err)
	}
	if got := normalize(t, rustShape(u)); !reflect.DeepEqual(got, want) {
		t.Errorf("group_update parse differs:\n rust %v\n go   %v", want, got)
	}
	if string(u.Relay.Key) != "relay-key" || string(u.Relay.HBHKey) != "hbh-key" ||
		!reflect.DeepEqual(u.Relay.Tokens, [][]byte{[]byte("token")}) || !reflect.DeepEqual(u.Relay.AuthTokens, [][]byte{[]byte("auth")}) {
		t.Errorf("relay secrets: %+v", u.Relay)
	}

	rekeyNode := goldenNode(t, golden, "group_enc_rekey_msg").GetChildren()[0]
	r, err := ParseGroupEncRekey(&rekeyNode)
	if err != nil {
		t.Fatal(err)
	}
	var wantRekey map[string]any
	if err := json.Unmarshal(golden["parsed_group_enc_rekey_msg"], &wantRekey); err != nil {
		t.Fatal(err)
	}
	gotRekey := normalize(t, map[string]any{
		"call_creator": map[string]any{"agent": r.Creator.RawAgent, "device": r.Creator.Device, "integrator": r.Creator.Integrator, "server": r.Creator.Server, "user": r.Creator.User},
		"call_id":      r.CallID, "encryption_type": r.EncryptionType, "encryption_version": r.EncryptionVersion,
		"key_generation": r.KeyGeneration, "transaction_id": r.TransactionID,
	})
	if !reflect.DeepEqual(gotRekey, normalize(t, wantRekey)) || string(r.Ciphertext) != strings.Repeat("1", 24) {
		t.Errorf("enc_rekey parse differs:\n rust %v\n go   %v", wantRekey, gotRekey)
	}

	waiting, err := ParseCallLinkJoinAck(goldenNode(t, golden, "in_link_join_waiting"), "TOKEN")
	if err != nil || !waiting.InWaitingRoom || waiting.Group != nil || waiting.Token != "TOKEN" {
		t.Fatalf("waiting join: %+v %v", waiting, err)
	}
	if _, err := ParseCallLinkJoinAck(goldenNode(t, golden, "in_link_join_waiting"), "DIFFERENT-TOKEN"); err == nil {
		t.Fatal("the admission state must remain bound to the requested call link")
	}
	admitted, err := ParseCallLinkJoinAck(goldenNode(t, golden, "in_link_join_admitted"), "TOKEN")
	if err != nil || admitted.InWaitingRoom || admitted.Group == nil || admitted.Group.TransactionID != 5 {
		t.Fatalf("admitted join: %+v %v", admitted, err)
	}
	if id, err := ParseCallLinkJoinCallID(goldenNode(t, golden, "in_link_join_admitted")); err != nil || id != "CID" {
		t.Fatalf("join call id %q %v", id, err)
	}
}

func childTags(n waBinary.Node) []string {
	var tags []string
	for _, c := range n.GetChildren() {
		tags = append(tags, c.Tag)
	}
	return tags
}

func callAction(n waBinary.Node) waBinary.Node { return n.GetChildren()[0] }

func mustNode(n waBinary.Node, err error) waBinary.Node {
	if err != nil {
		panic(err)
	}
	return n
}

func threeParticipants(first []byte) []groupcall.Participant {
	return []groupcall.Participant{
		groupParticipant("100001", 1, first, ""),
		groupParticipant("200002", 2, CapabilityOffer, ""),
		groupParticipant("300003", 3, CapabilityOffer, ""),
	}
}

func TestInitialGroupOfferHasExactRouteOrderAndVideoCapability(t *testing.T) {
	creator := lidDev("100001", 1)
	group := types.JID{User: "1234567890-1111111111", Server: types.GroupServer}
	n, err := BuildInitialGroupOffer(InitialGroupOfferParams{CallID: "00aabbccddeeff001122334455667788", ID: "REQ-1",
		Creator: creator, Group: group, Participants: threeParticipants(CapabilityOffer), AudioRate: 16000, Video: true})
	if err != nil {
		t.Fatal(err)
	}
	if n.Attrs["to"] != CallJID("00aabbccddeeff001122334455667788") {
		t.Fatalf("to %v", n.Attrs["to"])
	}
	act := callAction(n)
	if got := childTags(act); !slices.Equal(got, []string{"audio", "video", "net", "group_info"}) {
		t.Fatalf("children %v", got)
	}
	if act.Attrs["group-jid"] != group {
		t.Fatal("group-jid missing")
	}
	// Add the server's group_info attributes, then parse it back.
	parseable := act
	parseable.Content = slices.Clone(act.GetChildren())
	info := &parseable.Content.([]waBinary.Node)[3]
	info.Attrs = waBinary.Attrs{"transaction-id": "1", "connected-limit": "32", "media": "video"}
	u, err := ParseGroupInviteSnapshot(&parseable)
	if err != nil || u.Group != group {
		t.Fatalf("the parser must keep the offer's group identity: %+v %v", u, err)
	}
	users := act.GetChildren()[3].GetChildren()
	if _, ok := users[0].Attrs["state"]; ok {
		t.Fatal("outbound offer participants must omit an absent state")
	}
	capNode := users[0].GetChildren()[0].GetChildren()[0]
	if !bytesEqual(capNode.Content.([]byte), CapabilityVideoOffer) {
		t.Fatal("the creator's capability must be the video offer's")
	}

	// A group-jid on group_info that contradicts the offer's is refused.
	info.Attrs["group-jid"] = types.JID{User: "9876543210-2222222222", Server: types.GroupServer}
	if _, err := ParseGroupInviteSnapshot(&parseable); err == nil || !strings.Contains(err.Error(), "group_info group-jid does not match offer") {
		t.Fatalf("mismatched group identities must be rejected: %v", err)
	}
}

func TestInitialGroupVideoOfferPreservesStandardOpusCapabilityFamily(t *testing.T) {
	n, err := BuildInitialGroupOffer(InitialGroupOfferParams{CallID: "CID", ID: "REQ-1", Creator: lidDev("100001", 0),
		Participants: threeParticipants(CapabilityStandardOpusOffer), AudioRate: 16000, Video: true})
	if err != nil {
		t.Fatal(err)
	}
	act := callAction(n)
	users := act.GetChildren()[3].GetChildren()
	devices := users[0].GetChildren()
	c := devices[0].GetChildren()[0]
	if !bytesEqual(c.Content.([]byte), CapabilityStandardOpusVideoOffer) {
		t.Fatal("standard-Opus creator capability not kept in its family")
	}
}

func TestInitialGroupOfferValidatesMembershipAndGroupJID(t *testing.T) {
	creator := lidDev("100001", 1)
	if _, err := BuildInitialGroupOffer(InitialGroupOfferParams{CallID: "CID", ID: "REQ", Creator: creator,
		Participants: threeParticipants(CapabilityOffer)[:2], AudioRate: 16000}); err == nil {
		t.Fatal("two participants accepted")
	}
	if _, err := BuildInitialGroupOffer(InitialGroupOfferParams{CallID: "CID", ID: "REQ", Creator: creator,
		Group: lidUser("not-a-group"), Participants: threeParticipants(CapabilityOffer), AudioRate: 16000}); err == nil {
		t.Fatal("a non-group group-jid accepted")
	}
}

func TestParticipantInviteKeepsDestinationBeforeRoster(t *testing.T) {
	targets := []types.JID{lidDev("400004", 4), lidDev("400004", 5)}
	n, err := BuildGroupInviteOffer(GroupInviteOfferParams{CallID: "CID", ID: "REQ", To: lidUser("400004"), Creator: lidDev("100001", 1),
		TargetDevices: targets, Participants: threeParticipants(CapabilityOffer)[:2], Video: true, DeviceOrientation: 2})
	if err != nil {
		t.Fatal(err)
	}
	act := callAction(n)
	if got := childTags(act); !slices.Equal(got, []string{"audio", "video", "net", "destination", "group_info"}) {
		t.Fatalf("children %v", got)
	}
	if act.GetChildren()[1].Attrs["device_orientation"] != "2" {
		t.Fatal("an invitation from a rotated camera must describe it")
	}
	var addressed []types.JID
	for _, to := range act.GetChildren()[3].GetChildren() {
		addressed = append(addressed, to.Attrs["jid"].(types.JID))
	}
	if !slices.Equal(addressed, targets) {
		t.Fatalf("addressed %v", addressed)
	}
}

func TestActiveGroupPreacceptAndAcceptUseCallScopedRouting(t *testing.T) {
	creator := lidDev("111111111111111", 0)
	pre := mustNode(BuildActiveGroupPreaccept("ACTIVE-CALL", creator, "PRE-ID", true))
	if pre.Attrs["to"] != CallJID("ACTIVE-CALL") || !slices.Equal(childTags(callAction(pre)), []string{"audio", "video", "encopt", "capability"}) {
		t.Fatalf("preaccept %s", pre.String())
	}
	acc := mustNode(BuildActiveGroupAccept("ACTIVE-CALL", creator, "ACCEPT-ID", true))
	if acc.Attrs["to"] != CallJID("ACTIVE-CALL") || !slices.Equal(childTags(callAction(acc)), []string{"audio", "video", "net", "encopt"}) {
		t.Fatalf("accept %s", acc.String())
	}
}

func groupUpdateNode(info waBinary.Node) *waBinary.Node {
	n := node(TagGroupUpdate, waBinary.Attrs{"call-id": "CID", "call-creator": lidDev("100001", 1)}, []waBinary.Node{info})
	return &n
}

func TestGroupOfferPreservesNestedJoinableWhenWrapperIsSilent(t *testing.T) {
	offer := node("offer", waBinary.Attrs{"call-id": "CID", "call-creator": lidDev("100001", 1)}, []waBinary.Node{
		node("group_info", waBinary.Attrs{"transaction-id": "7", "media": "audio", "connected-limit": "8", "joinable": "1"}, nil)})
	u, err := ParseGroupInviteSnapshot(&offer)
	if err != nil || u == nil || !u.Joinable {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestGroupParticipantStateIsOptionalAndEmptyIsAbsent(t *testing.T) {
	for _, state := range []*string{nil, new(string)} {
		attrs := waBinary.Attrs{"jid": lidUser("200002")}
		if state != nil {
			attrs["state"] = *state
		}
		user := node("user", attrs, []waBinary.Node{node("device", waBinary.Attrs{"jid": lidDev("200002", 2)}, nil)})
		u, err := ParseGroupUpdate(groupUpdateNode(node("group_info",
			waBinary.Attrs{"transaction-id": "7", "media": "audio", "connected-limit": "8"}, []waBinary.Node{user})))
		if err != nil || u.Participants[0].State != "" {
			t.Fatalf("%+v %v", u, err)
		}
	}
}

func TestGroupUpdateRejectsMissingAndInvalidTransactionFields(t *testing.T) {
	for _, tx := range []string{"", "not-a-number"} {
		attrs := waBinary.Attrs{"media": "audio", "connected-limit": "8"}
		if tx != "" {
			attrs["transaction-id"] = tx
		}
		if _, err := ParseGroupUpdate(groupUpdateNode(node("group_info", attrs, nil))); err == nil {
			t.Fatalf("transaction %q accepted", tx)
		}
	}
	if _, err := ParseGroupUpdate(&waBinary.Node{Tag: TagGroupUpdate, Attrs: waBinary.Attrs{"call-id": "CID", "call-creator": lidDev("1", 1)}}); err == nil {
		t.Fatal("a group_update without group_info accepted")
	}
}

func TestGroupEpochRoundTripsAndRejectsWrongTarget(t *testing.T) {
	creator, target := lidDev("100001", 1), lidDev("200002", 2)
	key := DeviceKey{Device: target, Ciphertext: []byte{1, 2, 3}, Type: "msg"}
	n := mustNode(BuildGroupEncRekey(GroupEncRekeyParams{CallID: "CID", ID: "REQ", To: target, Creator: creator, TransactionID: 9, Key: key}))
	act := callAction(n)
	r, err := ParseGroupEncRekey(&act)
	if err != nil || r.TransactionID != 9 || r.KeyGeneration != 2 || r.EncryptionType != "msg" || !bytesEqual(r.Ciphertext, []byte{1, 2, 3}) {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := BuildGroupEncRekey(GroupEncRekeyParams{CallID: "CID", ID: "REQ", To: lidDev("300003", 3), Creator: creator, TransactionID: 9, Key: key}); err == nil {
		t.Fatal("a mismatched target accepted")
	}
	if _, err := BuildGroupEncRekey(GroupEncRekeyParams{CallID: "CID", ID: "REQ", To: target, Creator: creator, Key: key}); err == nil {
		t.Fatal("transaction 0 accepted")
	}
}

func TestPrekeyGroupEpochIncludesDeviceIdentity(t *testing.T) {
	creator, target := lidDev("100001", 1), lidDev("200002", 2)
	key := DeviceKey{Device: target, Ciphertext: []byte{4, 5, 6}, Type: "pkmsg"}
	if _, err := BuildGroupEncRekey(GroupEncRekeyParams{CallID: "CID", ID: "REQ", To: target, Creator: creator, TransactionID: 10, Key: key}); err == nil {
		t.Fatal("a pre-key envelope is unusable without the sender's ADV identity")
	}
	n := mustNode(BuildGroupEncRekey(GroupEncRekeyParams{CallID: "CID", ID: "REQ", To: target, Creator: creator, TransactionID: 10, Key: key,
		DeviceIdentity: []byte{7, 8, 9}}))
	act := callAction(n)
	if !slices.Equal(childTags(act), []string{"encopt", "enc", "device-identity"}) || !bytesEqual(act.GetChildren()[2].Content.([]byte), []byte{7, 8, 9}) {
		t.Fatalf("%s", act.String())
	}
}

func TestCallLinkBuildersMatchServiceAndCallScopedRoutes(t *testing.T) {
	create := mustNode(BuildCallLinkCreate(groupcall.LinkVideo, "REQ-1"))
	if create.Attrs["to"] != CallService || callAction(create).Attrs["media"] != "video" {
		t.Fatalf("%s", create.String())
	}
	join := mustNode(BuildCallLinkJoin("TOKEN", groupcall.LinkVideo, "REQ-2"))
	joinAct := callAction(join)
	if !slices.Equal(childTags(joinAct), []string{"audio", "video", "net", "capability"}) ||
		!bytesEqual(joinAct.GetChildren()[3].Content.([]byte), CapabilityVideoOffer) {
		t.Fatalf("%s", join.String())
	}
	if _, err := BuildCallLinkJoin(" ", groupcall.LinkAudio, "REQ"); err == nil {
		t.Fatal("a blank token accepted")
	}
	creator, user := lidDev("100001", 1), lidUser("200002")
	for _, build := range []func() (waBinary.Node, error){
		func() (waBinary.Node, error) { return BuildWaitingRoomToggle("CID", creator, true, "REQ-3") },
		func() (waBinary.Node, error) { return BuildWaitingRoomAdmit("CID", creator, user, "REQ-4") },
		func() (waBinary.Node, error) { return BuildWaitingRoomDeny("CID", creator, user, "REQ-5") },
	} {
		if n := mustNode(build()); n.Attrs["to"] != CallJID("CID") {
			t.Fatalf("%s", n.String())
		}
	}
	if _, err := BuildWaitingRoomHeartbeat("", creator, "REQ-6"); err == nil {
		t.Fatal("a heartbeat without a call id accepted")
	}
	if _, err := BuildRaiseHand("CID", types.JID{Server: CallServer}, creator, "REQ-7", true); err == nil {
		t.Fatal("a raise-hand without a target accepted")
	}
	if _, err := BuildScreenShare("CID", CallJID("CID"), creator, "", groupcall.ScreenShareStarted, 0, false); err == nil {
		t.Fatal("a screen share without an id accepted")
	}
}

func TestCallLinkQueryAckAllowsMissingWaitingRoom(t *testing.T) {
	ack := node("ack", waBinary.Attrs{"class": "call", "type": "link_query"}, []waBinary.Node{
		node("link_query", waBinary.Attrs{"token": "TOKEN", "media": "audio", "link_creator": lidDev("100001", 1)}, nil)})
	p, err := ParseCallLinkQueryAck(&ack)
	if err != nil || p.WaitingRoomEnabled || p.IsAdmin {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestRelayTokensPreserveOutOfOrderEntries(t *testing.T) {
	got := parseIndexedTokens([]waBinary.Node{
		node("token", waBinary.Attrs{"id": "2"}, []byte("two")),
		node("token", waBinary.Attrs{"id": "0"}, []byte("zero")),
	}, "token")
	if want := [][]byte{[]byte("zero"), {}, []byte("two")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestRelayEndpointRejectsMissingCredentialBindings(t *testing.T) {
	for _, missing := range []string{"relay_id", "token_id", "auth_token_id"} {
		attrs := waBinary.Attrs{"relay_name": "gru1c02"}
		for _, kv := range [][2]string{{"relay_id", "3"}, {"token_id", "0"}, {"auth_token_id", "0"}} {
			if kv[0] != missing {
				attrs[kv[0]] = kv[1]
			}
		}
		e := node("te2", attrs, []byte{1, 2, 3, 4, 0x1f, 0x90})
		if _, err := parseGroupRelayEndpoint(&e); err == nil {
			t.Fatalf("an endpoint without %s must not get credential zero", missing)
		}
	}
}

func TestCallLinkJoinDistinguishesWaitingAndAdmitted(t *testing.T) {
	creator := lidDev("100001", 1)
	room := func(media, enabled string, tx bool) waBinary.Node {
		attrs := waBinary.Attrs{"call-id": "CID", "call-creator": creator, "link-token": "TOKEN", "media": media, "enabled": enabled, "is_admin": "0"}
		if tx {
			attrs["transaction-id"] = "4"
		}
		return node("waiting_room", attrs, nil)
	}
	info := node("group_info", waBinary.Attrs{"call-id": "CID", "call-creator": creator, "transaction-id": "5", "media": "video", "connected-limit": "8"}, nil)
	ack := func(children ...waBinary.Node) *waBinary.Node {
		n := node("ack", waBinary.Attrs{"class": "call", "type": "link_join"}, children)
		return &n
	}
	if _, err := ParseCallLinkJoinAck(ack(room("audio", "1", true), info), "TOKEN"); err == nil {
		t.Fatal("one admitted ack cannot describe two media modes")
	}
	if _, err := ParseCallLinkJoinAck(ack(room("video", "0", false)), "TOKEN"); err == nil {
		t.Fatal("a disabled waiting-room-only ack accepted")
	}
	j, err := ParseCallLinkJoinAck(ack(room("video", "1", true), info), "TOKEN")
	if err != nil || j.InWaitingRoom || j.Group == nil {
		t.Fatalf("admitted with a room: %+v %v", j, err)
	}
	refused := node("ack", waBinary.Attrs{"class": "call", "type": "link_join", "error": "404"}, nil)
	var ae *AckError
	if _, err := ParseCallLinkJoinAck(&refused, "TOKEN"); err == nil || !errors.As(err, &ae) || ae.Code != "404" {
		t.Fatalf("a refused join: %v", err)
	}
}

func TestWaitingRoomSnapshotsAreBoundedBeforeRetention(t *testing.T) {
	creator := lidDev("100001", 1)
	user := func(i int, state string) waBinary.Node {
		return node("user", waBinary.Attrs{"jid": lidUser(fmt.Sprintf("200%03d", i)), "state": state}, nil)
	}
	update := func(users []waBinary.Node) *waBinary.Node {
		n := node(TagWaitingRoomUpdate, waBinary.Attrs{"call-id": "CID", "call-creator": creator}, []waBinary.Node{
			node("waiting_room", waBinary.Attrs{"call-id": "CID", "call-creator": creator, "link-token": "TOKEN", "media": "audio"}, users)})
		return &n
	}
	var many []waBinary.Node
	for i := 0; i <= maxWaitingRoomUsers; i++ {
		many = append(many, user(i, "pending"))
	}
	if _, err := ParseWaitingRoomUpdate(update(many)); err == nil {
		t.Fatal("more users than the cap accepted")
	}
	if _, err := ParseWaitingRoomUpdate(update(many[:maxWaitingRoomUsers])); err != nil {
		t.Fatalf("the cap itself refused: %v", err)
	}
	if _, err := ParseWaitingRoomUpdate(update([]waBinary.Node{user(1, strings.Repeat("x", maxWaitingRoomRetainedBytes+1))})); err == nil {
		t.Fatal("an oversized snapshot accepted")
	}
}

func TestWaitingRoomControlAcksRejectWrongTypeAndServerErrors(t *testing.T) {
	ok := node("ack", waBinary.Attrs{"class": "call", "type": "waiting_room_toggle"}, nil)
	if ParseWaitingRoomToggleAck(&ok) != nil || ParseWaitingRoomAdmitAck(&ok) == nil {
		t.Fatal("ack types not told apart")
	}
	rejected := node("ack", waBinary.Attrs{"class": "call", "type": "waiting_room_deny", "error": "403"}, nil)
	if ParseWaitingRoomDenyAck(&rejected) == nil {
		t.Fatal("a server error accepted")
	}
}

func TestParticipantControlsRoundTripAndAckPreservesRouting(t *testing.T) {
	creator, target := lidDev("100001", 1), CallJID("CID")
	raised := callAction(mustNode(BuildRaiseHand("CID", target, creator, "REQ-1", true)))
	if up, err := ParseRaiseHand(&raised); err != nil || !up {
		t.Fatalf("%v %v", up, err)
	}
	share := callAction(mustNode(BuildScreenShare("CID", target, creator, "REQ-2", groupcall.ScreenShareStarted, 7, true)))
	s, err := ParseScreenShare(&share)
	if err != nil || s.State != groupcall.ScreenShareStarted || s.Version != 2 || !s.HasID || s.ID != 7 {
		t.Fatalf("%+v %v", s, err)
	}
	overflow := node(TagScreenShare, waBinary.Attrs{"call-id": "CID", "call-creator": creator, "screenshare_state": "4294967295", "version": "2"}, nil)
	if _, err := ParseScreenShare(&overflow); err == nil {
		t.Fatal("an unknown screen-share state accepted")
	}
	from, participant, recipient := lidDev("100001", 1), lidDev("100001", 2), lidDev("200002", 3)
	original := waBinary.Node{Tag: "call", Attrs: waBinary.Attrs{"id": "REQ", "from": from, "participant": participant, "recipient": recipient}}
	ack, ok := BuildCallControlAck(&original, "screen_share")
	if !ok || ack.Attrs["to"] != from || ack.Attrs["participant"] != participant || ack.Attrs["recipient"] != recipient {
		t.Fatalf("%s", ack.String())
	}
}
