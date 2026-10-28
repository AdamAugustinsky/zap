// Ported from whatsapp-rust 5e72baf (wacore/src/stanza/call.rs, the pure
// <call> builders, and the capability helpers beside them), MIT. Each builder
// makes the node whatsapp-rust's makes, child for child and attribute for
// attribute; stanza_test.go checks them against golden nodes written by
// whatsapp-rust's own builders (zfcalls/tests/golden.rs). The <offer> child
// order is enforced by the server, which answers a mis-ordered offer with
// error 439.

package calls

import (
	"strconv"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

// Capability blobs (ver=1). CapabilityOffer goes in an <offer>, an <accept>
// and a video <preaccept>; CapabilityPreaccept in an audio-only <preaccept>;
// CapabilityVideoOffer in a video <offer>.
var (
	CapabilityOffer      = []byte{0x01, 0x05, 0xf7, 0x09, 0xe0, 0xbb, 0x13}
	CapabilityPreaccept  = []byte{0x01, 0x05, 0xf7, 0x09, 0xe0, 0xbb, 0x07}
	CapabilityVideoOffer = []byte{0x01, 0x05, 0xf7, 0x09, 0xe0, 0xbb, 0x53}

	// The same blobs selecting WhatsApp's standard Opus instead of MLow:
	// capability 31 (use_mlow_codec_v1) cleared.
	CapabilityStandardOpusOffer      = withoutMLow(CapabilityOffer)
	CapabilityStandardOpusPreaccept  = withoutMLow(CapabilityPreaccept)
	CapabilityStandardOpusVideoOffer = withoutMLow(CapabilityVideoOffer)
)

// CapabilityIndexMLowV1 is the capability bit for use_mlow_codec_v1.
const CapabilityIndexMLowV1 = 31

func withoutMLow(c []byte) []byte {
	out := append([]byte(nil), c...)
	out[5] &= 0x7f
	return out
}

// CapabilityBit is what a peer's <capability> says about one index: set,
// clear (including a blob that cannot be read), or unknown (no blob at all).
type CapabilityBit uint8

const (
	CapabilityUnknown CapabilityBit = iota
	CapabilitySet
	CapabilityClear
)

// ReadCapability reads one index of a <capability> blob, whose ver
// attribute is version (hasVersion false: absent or not a number), as the
// official client reads it: a blob of another version, a truncated one or
// an empty one reads as clear. An absent node is the caller's
// CapabilityUnknown.
func ReadCapability(version uint32, hasVersion bool, blob []byte, index uint32) CapabilityBit {
	if !hasVersion || version < 1 || len(blob) < 2 || blob[0] != 1 {
		return CapabilityClear
	}
	mask := blob[2:]
	n := int(blob[1])
	if n > len(mask) {
		return CapabilityClear
	}
	b := int(index/8) & 31
	if b >= n {
		return CapabilityClear
	}
	if mask[b]&(1<<(index%8)) != 0 {
		return CapabilitySet
	}
	return CapabilityClear
}

// MLowAfterPeer is whether MLow stays on against a peer: only when we asked
// for it and the peer did not clear the bit.
func MLowAfterPeer(local bool, peer CapabilityBit) bool { return local && peer != CapabilityClear }

// Wire tokens.
const (
	TerminateAcceptedElsewhere = "accepted_elsewhere"
	TerminateRejectedElsewhere = "rejected_elsewhere"
	TerminateTimeout           = "timeout"
	TerminateGroupCallEnded    = "group_call_ended"
	RejectBusy                 = "busy"
	RejectEnc                  = "enc"
)

// The audio rates an offer, accept or preaccept advertises: whatsapp-rust
// sends the one rate of its 16 kHz format (DEFAULT_AUDIO_RATES is the
// legacy pair).
var (
	AudioRates16k      = []string{"16000"}
	DefaultAudioRates  = []string{"8000", "16000"}
	videoEncH264       = "h.264"
	videoDecH264       = "H264"
	videoDecAccept     = "H264,AV1"
	initialOrientation = "0"
)

// VideoState is a <video state=N> value.
type VideoState int

const (
	VideoDisabled               VideoState = 0
	VideoEnabled                VideoState = 1
	VideoPaused                 VideoState = 2
	VideoUpgradeRequest         VideoState = 3
	VideoUpgradeAccept          VideoState = 4
	VideoUpgradeReject          VideoState = 5
	VideoStopped                VideoState = 6
	VideoUpgradeRejectByTimeout VideoState = 7
	VideoUpgradeCancel          VideoState = 8
	VideoUpgradeCancelByTimeout VideoState = 9
	VideoUnknownPeer            VideoState = 10
	VideoUpgradeRequestV2       VideoState = 11
	VideoError                  VideoState = 20
)

// Inactive is whatsapp-rust's is_inactive_for_call_mode.
func (s VideoState) Inactive() bool {
	switch s {
	case VideoDisabled, VideoUpgradeReject, VideoStopped, VideoUpgradeRejectByTimeout,
		VideoUpgradeCancel, VideoUpgradeCancelByTimeout, VideoError:
		return true
	}
	return false
}

// UpgradeRequest is whatsapp-rust's is_upgrade_request.
func (s VideoState) UpgradeRequest() bool {
	return s == VideoUpgradeRequest || s == VideoUpgradeRequestV2
}

// EncodeLatency is the relay latency's wire form: 0x2000000 + rtt.
func EncodeLatency(rttMS uint32) string { return strconv.FormatUint(uint64(0x0200_0000+rttMS), 10) }

func node(tag string, attrs waBinary.Attrs, content any) waBinary.Node {
	if attrs == nil {
		attrs = waBinary.Attrs{}
	}
	return waBinary.Node{Tag: tag, Attrs: attrs, Content: content}
}

func callWrap(to types.JID, id string, action waBinary.Node) waBinary.Node {
	attrs := waBinary.Attrs{"to": to}
	if id != "" {
		attrs["id"] = id
	}
	return node("call", attrs, []waBinary.Node{action})
}

func action(tag, callID string, creator types.JID, children []waBinary.Node) waBinary.Node {
	return node(tag, waBinary.Attrs{"call-id": callID, "call-creator": creator}, children)
}

func audioOpus(rate string) waBinary.Node {
	return node("audio", waBinary.Attrs{"enc": "opus", "rate": rate}, nil)
}

func encopt() waBinary.Node { return node("encopt", waBinary.Attrs{"keygen": "2"}, nil) }

func capabilityNode(blob []byte) waBinary.Node {
	return node("capability", waBinary.Attrs{"ver": "1"}, append([]byte(nil), blob...))
}

// DeviceKey is one device's encrypted callKey in an <offer>.
type DeviceKey struct {
	Device     types.JID
	Ciphertext []byte
	// Type is the Signal message type: "pkmsg" or "msg".
	Type string
}

func encNode(k DeviceKey) waBinary.Node {
	return node("enc", waBinary.Attrs{"v": "2", "type": k.Type, "count": "0"}, append([]byte(nil), k.Ciphertext...))
}

// OfferParams are build_offer's.
type OfferParams struct {
	CallID         string
	To             types.JID
	Creator        types.JID
	Keys           []DeviceKey
	PrivacyToken   []byte
	Capability     []byte
	DeviceIdentity []byte
	// ID is the <call> id the server's <ack type=offer> (with our relay)
	// answers.
	ID string
	// MultiDevice keeps the <destination><to jid> shape for a callee with
	// several devices even when one key survived.
	MultiDevice bool
	Video       bool
	AudioRates  []string
}

// BuildOffer is <call to id><offer call-id call-creator> with the order the
// server enforces: privacy, audio..., [video], net, capability,
// destination|enc, encopt, device-identity.
func BuildOffer(p OfferParams) waBinary.Node {
	var children []waBinary.Node
	if p.PrivacyToken != nil {
		children = append(children, node("privacy", nil, append([]byte(nil), p.PrivacyToken...)))
	}
	for _, rate := range p.AudioRates {
		children = append(children, audioOpus(rate))
	}
	if p.Video {
		children = append(children, node("video", waBinary.Attrs{
			"enc": videoEncH264, "dec": videoDecH264,
			"screen_width": "0", "screen_height": "0",
			"device_orientation": initialOrientation,
		}, nil))
	}
	children = append(children, node("net", waBinary.Attrs{"medium": "3"}, nil))
	if p.Capability != nil {
		children = append(children, capabilityNode(p.Capability))
	}
	if len(p.Keys) > 1 || p.MultiDevice {
		tos := make([]waBinary.Node, 0, len(p.Keys))
		for _, k := range p.Keys {
			tos = append(tos, node("to", waBinary.Attrs{"jid": k.Device}, []waBinary.Node{encNode(k)}))
		}
		children = append(children, node("destination", nil, tos))
	} else if len(p.Keys) == 1 {
		children = append(children, encNode(p.Keys[0]))
	}
	children = append(children, encopt())
	if p.DeviceIdentity != nil {
		children = append(children, node("device-identity", nil, append([]byte(nil), p.DeviceIdentity...)))
	}
	return callWrap(p.To, p.ID, action("offer", p.CallID, p.Creator, children))
}

// StandardOpusVoipSettings is the <voip_settings> of a standard-Opus answer.
func StandardOpusVoipSettings(clock48k bool) []byte {
	if clock48k {
		return []byte(`{"encode":{"use_mlow_codec_v1":"false"},"options":{"enable_48khz_rtp_clock":"true"}}`)
	}
	return []byte(`{"encode":{"use_mlow_codec_v1":"false"},"options":{"enable_48khz_rtp_clock":"false"}}`)
}

// AcceptParams are build_accept's.
type AcceptParams struct {
	CallID string
	To     types.JID
	// ID is required: the server drops an idless <accept>.
	ID                    string
	Creator               types.JID
	AudioRates            []string
	RelayTE, RTE          []byte
	VoipSettings          []byte
	Capability            []byte
	Video                 bool
	AbTestBucket          string
	AbTestBucketIDList    string
	HasAbTestBucket       bool
	HasAbTestBucketIDList bool
}

// BuildAccept is <accept>: audio..., [video], [te priority=2], net medium=2,
// encopt, [metadata], [capability], [rte], [voip_settings].
func BuildAccept(p AcceptParams) waBinary.Node {
	var children []waBinary.Node
	for _, rate := range p.AudioRates {
		children = append(children, audioOpus(rate))
	}
	if p.Video {
		children = append(children, node("video", waBinary.Attrs{"dec": videoDecH264, "device_orientation": initialOrientation}, nil))
	}
	if p.RelayTE != nil {
		children = append(children, node("te", waBinary.Attrs{"priority": "2"}, append([]byte(nil), p.RelayTE...)))
	}
	children = append(children, node("net", waBinary.Attrs{"medium": "2"}, nil), encopt())
	if p.HasAbTestBucket || p.HasAbTestBucketIDList {
		attrs := waBinary.Attrs{}
		if p.HasAbTestBucket {
			attrs["peer_abtest_bucket"] = p.AbTestBucket
		}
		if p.HasAbTestBucketIDList {
			attrs["peer_abtest_bucket_id_list"] = p.AbTestBucketIDList
		}
		children = append(children, node("metadata", attrs, nil))
	}
	if p.Capability != nil {
		children = append(children, capabilityNode(p.Capability))
	}
	if p.RTE != nil {
		children = append(children, node("rte", nil, append([]byte(nil), p.RTE...)))
	}
	if p.VoipSettings != nil {
		children = append(children, node("voip_settings", waBinary.Attrs{"uncompressed": "1"}, append([]byte(nil), p.VoipSettings...)))
	}
	return callWrap(p.To, p.ID, action("accept", p.CallID, p.Creator, children))
}

// BuildPreaccept is build_preaccept: the capability is CapabilityOffer for a
// video call, CapabilityPreaccept for a voice call.
func BuildPreaccept(callID string, to, creator types.JID, id string, rates []string, video bool) waBinary.Node {
	c := CapabilityPreaccept
	if video {
		c = CapabilityOffer
	}
	return BuildPreacceptWithCapability(callID, to, creator, id, rates, c, video)
}

// BuildPreacceptWithCapability is <preaccept>: audio..., [video], encopt,
// capability.
func BuildPreacceptWithCapability(callID string, to, creator types.JID, id string, rates []string, capability []byte, video bool) waBinary.Node {
	var children []waBinary.Node
	for _, rate := range rates {
		children = append(children, audioOpus(rate))
	}
	if video {
		children = append(children, node("video", waBinary.Attrs{
			"dec": videoDecH264, "device_orientation": initialOrientation,
			"screen_width": "0", "screen_height": "0",
		}, nil))
	}
	children = append(children, encopt(), capabilityNode(capability))
	return callWrap(to, id, action("preaccept", callID, creator, children))
}

// TransportParams are build_transport's.
type TransportParams struct {
	CallID               string
	To                   types.JID
	Creator              types.JID
	P2PCandRound         string
	TransportMessageType string
	RelayTE              []byte
}

// BuildTransport is <transport>: [te priority=1], net medium=2 [protocol=0].
func BuildTransport(p TransportParams) waBinary.Node {
	attrs := waBinary.Attrs{"call-id": p.CallID, "call-creator": p.Creator}
	if p.P2PCandRound != "" {
		attrs["p2p-cand-round"] = p.P2PCandRound
	}
	if p.TransportMessageType != "" {
		attrs["transport-message-type"] = p.TransportMessageType
	}
	var children []waBinary.Node
	if p.RelayTE != nil {
		children = append(children, node("te", waBinary.Attrs{"priority": "1"}, append([]byte(nil), p.RelayTE...)))
	}
	net := waBinary.Attrs{"medium": "2"}
	if p.TransportMessageType != "9" {
		net["protocol"] = "0"
	}
	children = append(children, node("net", net, nil))
	return callWrap(p.To, "", node("transport", attrs, children))
}

// RelayLatencyParams are build_relay_latency's.
type RelayLatencyParams struct {
	CallID    string
	To        types.JID
	Creator   types.JID
	LatencyMS uint32
	RelayName string
	Address   []byte
	Devices   []types.JID
}

// BuildRelayLatency is <relaylatency> with <te latency relay_name> and an
// optional <destination>.
func BuildRelayLatency(p RelayLatencyParams) waBinary.Node {
	children := []waBinary.Node{node("te", waBinary.Attrs{
		"latency": EncodeLatency(p.LatencyMS), "relay_name": p.RelayName,
	}, append([]byte(nil), p.Address...))}
	if len(p.Devices) > 0 {
		tos := make([]waBinary.Node, 0, len(p.Devices))
		for _, d := range p.Devices {
			tos = append(tos, node("to", waBinary.Attrs{"jid": d}, nil))
		}
		children = append(children, node("destination", nil, tos))
	}
	return callWrap(p.To, "", action("relaylatency", p.CallID, p.Creator, children))
}

// TerminateParams are build_terminate's. To is a device: call signaling is
// routed per device.
type TerminateParams struct {
	CallID  string
	To      types.JID
	ID      string
	Creator types.JID
	Reason  string
}

func BuildTerminate(p TerminateParams) waBinary.Node {
	attrs := waBinary.Attrs{"call-id": p.CallID, "call-creator": p.Creator}
	if p.Reason != "" {
		attrs["reason"] = p.Reason
	}
	return callWrap(p.To, p.ID, node("terminate", attrs, nil))
}

// VideoStateParams are build_video_state's. ID is load-bearing: the peer's
// typed <ack type="video"> answers it.
type VideoStateParams struct {
	CallID         string
	To             types.JID
	ID             string
	Creator        types.JID
	State          VideoState
	Dec            string
	Orientation    uint8
	HasOrientation bool
}

// BuildVideoState is <video call-id call-creator state [dec]
// [voip_settings="video"] [device_orientation]>.
func BuildVideoState(p VideoStateParams) waBinary.Node {
	attrs := waBinary.Attrs{"call-id": p.CallID, "call-creator": p.Creator, "state": strconv.Itoa(int(p.State))}
	if p.Dec != "" {
		attrs["dec"] = p.Dec
	}
	if p.State == VideoUpgradeRequestV2 {
		attrs["voip_settings"] = "video"
	}
	if p.HasOrientation {
		attrs["device_orientation"] = strconv.Itoa(int(p.Orientation))
	}
	return callWrap(p.To, p.ID, node("video", attrs, nil))
}

// BuildMuteV2 is our microphone's state: <mute_v2 mute-state="1|0">.
func BuildMuteV2(callID string, to, creator types.JID, id string, muted bool) waBinary.Node {
	state := "0"
	if muted {
		state = "1"
	}
	return callWrap(to, id, node("mute_v2", waBinary.Attrs{"call-id": callID, "call-creator": creator, "mute-state": state}, nil))
}

// BuildReject is <reject call-id call-creator count="0">.
func BuildReject(callID string, to, creator types.JID, id string) waBinary.Node {
	return callWrap(to, id, node("reject", waBinary.Attrs{"call-id": callID, "call-creator": creator, "count": "0"}, nil))
}

// BuildHeartbeat is <call to={call_id}@call id><heartbeat/>.
func BuildHeartbeat(callID string, creator types.JID, id string) waBinary.Node {
	// A JID, as whatsapp-rust's encoder packs the "{call_id}@call" string.
	to := types.JID{User: callID, Server: "call"}
	return node("call", waBinary.Attrs{"to": to, "id": id},
		[]waBinary.Node{node("heartbeat", waBinary.Attrs{"call-id": callID, "call-creator": creator}, nil)})
}

// BuildOfferAckReceipt is <receipt to id [from]><offer call-id
// call-creator/></receipt>, the answer to an incoming offer's <call id>.
func BuildOfferAckReceipt(to types.JID, stanzaID, callID string, creator, from types.JID) waBinary.Node {
	attrs := waBinary.Attrs{"to": to, "id": stanzaID}
	if !from.IsEmpty() {
		attrs["from"] = from
	}
	return node("receipt", attrs, []waBinary.Node{node("offer", waBinary.Attrs{"call-id": callID, "call-creator": creator}, nil)})
}
