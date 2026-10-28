package calls

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

// The golden stanzas are whatsapp-rust's own builders' (whatsapp-rust
// 5e72baf through zapfast_core's zfcalls/tests/golden.rs, which wrote
// testdata/golden.json). They are committed data: zfcalls went with the
// Rust tree in WP9, so they can no longer be regenerated here. whatsmeow's encoder writes a
// node's attributes in map order, so the comparison is of the decoded
// nodes: same tags, attributes, children (in order) and bytes.

const goldenCallID = "A1B2C3D4E5F60718293A4B5C6D7E8F90"

var (
	goldenMe   = types.JID{User: "100000000001001", Server: types.HiddenUserServer, Device: 7}
	goldenPeer = types.JID{User: "100000000002002", Server: types.HiddenUserServer}
)

func goldenPeerDevice(d uint16) types.JID {
	j := goldenPeer
	j.Device = d
	return j
}

func goldenKey(d uint16, typ string) DeviceKey {
	ct := make([]byte, 24)
	for i := range ct {
		ct[i] = 0x30 + byte(d)
	}
	return DeviceKey{Device: goldenPeerDevice(d), Ciphertext: ct, Type: typ}
}

func goldenStanzas() map[string]waBinary.Node {
	me, peer, dev3 := goldenMe, goldenPeer, goldenPeerDevice(3)
	one := []DeviceKey{goldenKey(0, "msg")}
	two := []DeviceKey{goldenKey(0, "pkmsg"), goldenKey(3, "msg")}
	te := []byte{1, 2, 3, 4, 0x0d, 0x96}
	return map[string]waBinary.Node{
		"offer_audio_single": BuildOffer(OfferParams{CallID: goldenCallID, To: peer, Creator: me, Keys: one,
			PrivacyToken: []byte("tc-token"), Capability: CapabilityStandardOpusOffer, ID: "OFFER-ID-1", AudioRates: AudioRates16k}),
		"offer_audio_multi_device_one_key": BuildOffer(OfferParams{CallID: goldenCallID, To: peer, Creator: me, Keys: one,
			Capability: CapabilityOffer, ID: "OFFER-ID-2", MultiDevice: true, AudioRates: DefaultAudioRates}),
		"offer_video_two_keys": BuildOffer(OfferParams{CallID: goldenCallID, To: peer, Creator: me, Keys: two,
			PrivacyToken: []byte("tc-token"), Capability: CapabilityVideoOffer, DeviceIdentity: []byte("device-identity"),
			ID: "OFFER-ID-3", MultiDevice: true, Video: true, AudioRates: AudioRates16k}),

		"accept_audio_standard_opus": BuildAccept(AcceptParams{CallID: goldenCallID, To: dev3, ID: "ACCEPT-ID-1", Creator: peer,
			AudioRates: AudioRates16k, VoipSettings: StandardOpusVoipSettings(false), Capability: CapabilityStandardOpusOffer}),
		"accept_video_full": BuildAccept(AcceptParams{CallID: goldenCallID, To: dev3, ID: "ACCEPT-ID-2", Creator: peer,
			AudioRates: AudioRates16k, RelayTE: te, RTE: []byte{9, 8, 7}, Video: true,
			AbTestBucket: "bucket", HasAbTestBucket: true, AbTestBucketIDList: "1,2", HasAbTestBucketIDList: true}),

		"preaccept_audio":         BuildPreaccept(goldenCallID, dev3, peer, "PRE-ID-1", AudioRates16k, false),
		"preaccept_video":         BuildPreaccept(goldenCallID, dev3, peer, "PRE-ID-2", AudioRates16k, true),
		"preaccept_standard_opus": BuildPreacceptWithCapability(goldenCallID, dev3, peer, "PRE-ID-3", AudioRates16k, CapabilityStandardOpusPreaccept, false),

		"transport_te": BuildTransport(TransportParams{CallID: goldenCallID, To: dev3, Creator: me,
			P2PCandRound: "1", TransportMessageType: "2", RelayTE: te}),
		"transport_type_9": BuildTransport(TransportParams{CallID: goldenCallID, To: dev3, Creator: me, TransportMessageType: "9"}),

		"relaylatency_devices": BuildRelayLatency(RelayLatencyParams{CallID: goldenCallID, To: peer, Creator: me, LatencyMS: 42,
			RelayName: "gru1c01", Address: []byte{10, 0, 0, 1, 0x0d, 0x96}, Devices: []types.JID{goldenPeerDevice(0), dev3}}),
		"relaylatency_plain": BuildRelayLatency(RelayLatencyParams{CallID: goldenCallID, To: dev3, Creator: me,
			RelayName: "gru1c01", Address: []byte{10, 0, 0, 1, 0x0d, 0x96}}),

		"terminate_reason": BuildTerminate(TerminateParams{CallID: goldenCallID, To: dev3, ID: "TERM-ID-1", Creator: me, Reason: TerminateAcceptedElsewhere}),
		"terminate_plain":  BuildTerminate(TerminateParams{CallID: goldenCallID, To: peer, Creator: me}),

		"video_upgrade_request": BuildVideoState(VideoStateParams{CallID: goldenCallID, To: dev3, ID: "VIDEO-ID-1", Creator: me,
			State: VideoUpgradeRequestV2, Dec: "H264"}),
		"video_enabled_orientation": BuildVideoState(VideoStateParams{CallID: goldenCallID, To: dev3, ID: "VIDEO-ID-2", Creator: me,
			State: VideoEnabled, Dec: "H264", HasOrientation: true}),
		"video_stopped": BuildVideoState(VideoStateParams{CallID: goldenCallID, To: dev3, ID: "VIDEO-ID-3", Creator: me, State: VideoStopped}),

		"mute_on":   BuildMuteV2(goldenCallID, dev3, me, "MUTE-ID-1", true),
		"mute_off":  BuildMuteV2(goldenCallID, dev3, me, "MUTE-ID-2", false),
		"reject":    BuildReject(goldenCallID, dev3, peer, "REJECT-ID-1"),
		"heartbeat": BuildHeartbeat(goldenCallID, me, "BEAT-ID-1"),

		"offer_ack_receipt": BuildOfferAckReceipt(dev3, "STANZA-ID-1", goldenCallID, peer, me),
	}
}

// decode reads a marshalled node (its format byte first).
func decode(t *testing.T, packed []byte) *waBinary.Node {
	t.Helper()
	if len(packed) == 0 || packed[0] != 0 {
		t.Fatalf("not a plain packed node: % x", packed[:min(len(packed), 4)])
	}
	n, err := waBinary.Unmarshal(packed[1:])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStanzasMatchWhatsappRustBuilders(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]string
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	ours := goldenStanzas()
	var names []string
	for name := range golden {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) != len(ours) {
		t.Errorf("golden has %d stanzas, the Go builders %d", len(names), len(ours))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			node, ok := ours[name]
			if !ok {
				t.Fatalf("no Go stanza for %s", name)
			}
			rust, err := hex.DecodeString(golden[name])
			if err != nil {
				t.Fatal(err)
			}
			packed, err := waBinary.Marshal(node)
			if err != nil {
				t.Fatal(err)
			}
			want, got := decode(t, rust), decode(t, packed)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("the Go stanza differs from whatsapp-rust's:\n rust %s\n go   %s", want.String(), got.String())
			}
		})
	}
}

func TestCapabilityReadsAsTheOfficialClient(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ver     uint32
		hasVer  bool
		blob    []byte
		want    CapabilityBit
		mlowOff bool
	}{
		{"offer", 1, true, CapabilityOffer, CapabilitySet, false},
		{"standard opus", 1, true, CapabilityStandardOpusOffer, CapabilityClear, true},
		{"no ver", 0, false, CapabilityOffer, CapabilityClear, true},
		{"ver 0", 0, true, CapabilityOffer, CapabilityClear, true},
		{"empty", 1, true, nil, CapabilityClear, true},
		{"embedded version 0", 1, true, []byte{0, 5, 0xf7, 0x09, 0xe0, 0xbb, 0x13}, CapabilityClear, true},
		{"truncated", 1, true, []byte{1, 5, 0xf7, 0x09}, CapabilityClear, true},
	} {
		got := ReadCapability(tc.ver, tc.hasVer, tc.blob, CapabilityIndexMLowV1)
		if got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
		if MLowAfterPeer(true, got) == tc.mlowOff {
			t.Errorf("%s: MLow after the peer is %v", tc.name, !tc.mlowOff)
		}
	}
	if !MLowAfterPeer(true, CapabilityUnknown) || MLowAfterPeer(false, CapabilitySet) {
		t.Error("an absent capability keeps MLow; a local off stays off")
	}
	if EncodeLatency(42) != "33554474" {
		t.Errorf("latency %s", EncodeLatency(42))
	}
}
