// Ported from whatsapp-rust 5e72baf wacore/src/stanza/group_call.rs (the
// group-call, call-link, waiting-room and participant-control stanzas), MIT.
// group_stanza_test.go checks the builders against golden nodes written by
// whatsapp-rust's own builders (testdata/group_golden.json) and ports its
// unit tests.

package calls

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"

	"zapfast/gocore/voip/groupcall"
)

const (
	maxRelayTokens = 64
	// A call link may queue more users for approval than the 32 endpoints
	// a call connects at once.
	maxWaitingRoomUsers         = 128
	maxWaitingRoomRetainedBytes = 1024 * 1024
)

// Group-call action tags (wacore CallActionTag).
const (
	TagGroupUpdate       = "group_update"
	TagEncRekey          = "enc_rekey"
	TagWaitingRoomUpdate = "waiting_room_update"
	TagRaiseHand         = "user_action"
	TagScreenShare       = "screen_share"
	TagLinkJoin          = "link_join"
)

// CallServer is the call service's JID server.
const CallServer = "call"

// CallService is the call service's address: the call-link requests go to
// it.
var CallService = types.JID{Server: CallServer}

// CallJID is a group call's own address, {callID}@call: its answers,
// terminate and waiting-room requests go to it.
func CallJID(callID string) types.JID { return types.JID{User: callID, Server: CallServer} }

// videoCapability is the creator's capability in a video group offer: the
// video blob of its capability family.
func videoCapability(c []byte) []byte {
	switch {
	case bytesEqual(c, CapabilityOffer):
		return CapabilityVideoOffer
	case bytesEqual(c, CapabilityStandardOpusOffer):
		return CapabilityStandardOpusVideoOffer
	}
	return c
}

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

// InitialGroupOfferParams are build_initial_group_offer's: an ad-hoc or
// group-bound call's first offer.
type InitialGroupOfferParams struct {
	CallID       string
	ID           string
	Creator      types.JID
	Group        types.JID // empty for an ad-hoc call
	Participants []groupcall.Participant
	AudioRate    uint32
	Video        bool
}

// GroupInviteOfferParams are build_group_invite_offer's: the invitation of
// one new or disconnected participant into an active call.
type GroupInviteOfferParams struct {
	CallID        string
	ID            string
	To            types.JID
	Creator       types.JID
	TargetDevices []types.JID
	Participants  []groupcall.Participant
	Video         bool
	// DeviceOrientation is this side's rotation in quarter turns, 0..3.
	DeviceOrientation uint8
}

// GroupEncRekeyParams are build_group_enc_rekey's: the direct delivery of
// one shared keygen-v2 epoch.
type GroupEncRekeyParams struct {
	CallID         string
	ID             string
	To             types.JID
	Creator        types.JID
	TransactionID  uint32
	Key            DeviceKey
	DeviceIdentity []byte
}

// BuildActiveGroupPreaccept is the eager preparation answer to an
// invitation into an active group call, routed to the call.
func BuildActiveGroupPreaccept(callID string, creator types.JID, requestID string, video bool) (waBinary.Node, error) {
	if err := validateCallIdentity(callID, requestID, creator); err != nil {
		return waBinary.Node{}, err
	}
	return BuildPreaccept(callID, CallJID(callID), creator, requestID, AudioRates16k, video), nil
}

// BuildActiveGroupAccept is the immediate acceptance of an invitation into
// an active group call, routed to the call.
func BuildActiveGroupAccept(callID string, creator types.JID, requestID string, video bool) (waBinary.Node, error) {
	if err := validateCallIdentity(callID, requestID, creator); err != nil {
		return waBinary.Node{}, err
	}
	return BuildAccept(AcceptParams{CallID: callID, To: CallJID(callID), ID: requestID, Creator: creator,
		AudioRates: AudioRates16k, Video: video}), nil
}

// BuildInitialGroupOffer is the first offer of a group call, children in
// the order the server enforces: audio, [video], net, group_info.
func BuildInitialGroupOffer(p InitialGroupOfferParams) (waBinary.Node, error) {
	if err := validateCallIdentity(p.CallID, p.ID, p.Creator); err != nil {
		return waBinary.Node{}, err
	}
	if len(p.Participants) < 3 {
		return waBinary.Node{}, errors.New("group offer requires self and at least two remote participants")
	}
	if len(p.Participants) > groupcall.MaxParticipants {
		return waBinary.Node{}, fmt.Errorf("group offer has %d participants, maximum is %d", len(p.Participants), groupcall.MaxParticipants)
	}
	if p.AudioRate == 0 {
		return waBinary.Node{}, errors.New("group offer audio rate must be non-zero")
	}
	creator := p.Creator
	users, err := buildGroupUsers(p.Participants, &creator, p.Video)
	if err != nil {
		return waBinary.Node{}, err
	}
	children := []waBinary.Node{audioOpus(strconv.FormatUint(uint64(p.AudioRate), 10))}
	if p.Video {
		// A call still being offered has no handle, so no rotation yet.
		children = append(children, videoOfferNode(0))
	}
	children = append(children, node("net", waBinary.Attrs{"medium": "3"}, nil), node("group_info", nil, users))
	act := action("offer", p.CallID, p.Creator, children)
	if !p.Group.IsEmpty() {
		if p.Group.Server != types.GroupServer {
			return waBinary.Node{}, errors.New("group offer group_jid must use @g.us")
		}
		act.Attrs["group-jid"] = p.Group
	}
	return callWrap(CallJID(p.CallID), p.ID, act), nil
}

// BuildGroupInviteOffer is the singular offer for a participant joining an
// active call.
func BuildGroupInviteOffer(p GroupInviteOfferParams) (waBinary.Node, error) {
	if err := validateCallIdentity(p.CallID, p.ID, p.Creator); err != nil {
		return waBinary.Node{}, err
	}
	switch {
	case p.To.User == "":
		return waBinary.Node{}, errors.New("group invite target is required")
	case len(p.TargetDevices) == 0:
		return waBinary.Node{}, errors.New("group invite requires at least one target device")
	case len(p.Participants) == 0:
		return waBinary.Node{}, errors.New("group invite requires an active roster")
	case p.DeviceOrientation > 3:
		return waBinary.Node{}, errors.New("group invite device orientation must be quarter turns in 0..=3")
	}
	children := []waBinary.Node{audioOpus("16000")}
	if p.Video {
		children = append(children, videoOfferNode(p.DeviceOrientation))
	}
	tos := make([]waBinary.Node, 0, len(p.TargetDevices))
	for _, d := range p.TargetDevices {
		tos = append(tos, node("to", waBinary.Attrs{"jid": d}, nil))
	}
	// The server's roster goes out verbatim: a video invitation advertises
	// video on its own and must not upgrade others' capability blobs.
	users, err := buildGroupUsers(p.Participants, nil, false)
	if err != nil {
		return waBinary.Node{}, err
	}
	children = append(children, node("net", waBinary.Attrs{"medium": "2"}, nil), node("destination", nil, tos), node("group_info", nil, users))
	return callWrap(p.To, p.ID, action("offer", p.CallID, p.Creator, children)), nil
}

func validateCallIdentity(callID, id string, creator types.JID) error {
	switch {
	case callID == "":
		return errors.New("call id is required")
	case id == "":
		return errors.New("call stanza id is required")
	case creator.User == "":
		return errors.New("call creator is required")
	}
	return nil
}

func buildGroupUsers(participants []groupcall.Participant, videoCreator *types.JID, video bool) ([]waBinary.Node, error) {
	users := make([]waBinary.Node, 0, len(participants))
	for pi, p := range participants {
		if p.JID.User == "" {
			return nil, fmt.Errorf("participant %d has no jid", pi)
		}
		if len(p.Devices) == 0 {
			return nil, fmt.Errorf("participant %d has no devices", pi)
		}
		devices := make([]waBinary.Node, 0, len(p.Devices))
		for di, d := range p.Devices {
			if d.JID.User == "" {
				return nil, fmt.Errorf("participant %d device %d has no jid", pi, di)
			}
			dn := node("device", waBinary.Attrs{"jid": d.JID}, nil)
			if len(d.Capability) > 0 {
				c := d.Capability
				if video && videoCreator != nil && videoCreator.ToNonAD() == d.JID.ToNonAD() {
					c = videoCapability(c)
				}
				attrs := waBinary.Attrs{}
				if d.HasCapabilityVersion {
					attrs["ver"] = strconv.FormatUint(uint64(d.CapabilityVersion), 10)
				}
				dn.Content = []waBinary.Node{node("capability", attrs, append([]byte(nil), c...))}
			}
			devices = append(devices, dn)
		}
		attrs := waBinary.Attrs{"jid": p.JID}
		if p.State != "" {
			attrs["state"] = p.State
		}
		users = append(users, node("user", attrs, devices))
	}
	return users, nil
}

type snapshotEnvelope int

const (
	envelopeOffer snapshotEnvelope = iota
	envelopeUpdate
	envelopeAck
)

// ParseGroupInviteSnapshot is the roster an offer into a group call
// carries; nil when it has none (a 1:1 offer).
func ParseGroupInviteSnapshot(offer *waBinary.Node) (*groupcall.Update, error) {
	return parseGroupSnapshot(offer, envelopeOffer)
}

// ParseGroupUpdate parses an authoritative <group_update>.
func ParseGroupUpdate(n *waBinary.Node) (*groupcall.Update, error) {
	u, err := parseGroupSnapshot(n, envelopeUpdate)
	if err == nil && u == nil {
		err = errors.New("group_update is missing group_info")
	}
	return u, err
}

// ParseInitialGroupCallAck is the roster an initial offer's or a link
// join's <ack> returns; nil when it has none.
func ParseInitialGroupCallAck(n *waBinary.Node) (*groupcall.Update, error) {
	return parseGroupSnapshot(n, envelopeAck)
}

func parseGroupSnapshot(n *waBinary.Node, env snapshotEnvelope) (*groupcall.Update, error) {
	want := map[snapshotEnvelope]string{envelopeOffer: "offer", envelopeUpdate: TagGroupUpdate, envelopeAck: "ack"}[env]
	if n.Tag != want {
		return nil, errors.New("unexpected group snapshot envelope")
	}
	info, ok := n.GetOptionalChildByTag("group_info")
	if !ok {
		if env == envelopeUpdate {
			return nil, errors.New("group_update is missing group_info")
		}
		return nil, nil
	}
	identity := n
	if env == envelopeAck {
		identity = &info
	}
	callID, err := requiredString(identity, "call-id")
	if err != nil {
		return nil, err
	}
	creator, err := requiredJID(identity, "call-creator")
	if err != nil {
		return nil, err
	}
	u, err := parseGroupInfo(&info, callID, creator)
	if err != nil {
		return nil, err
	}
	switch env {
	case envelopeOffer:
		if g, ok := optionalJID(n, "group-jid"); ok {
			if !u.Group.IsEmpty() && u.Group != g {
				return nil, errors.New("group_info group-jid does not match offer")
			}
			u.Group = g
		}
		if id, ok := nodeAttr(&info, "call-id"); ok && id != u.CallID {
			return nil, errors.New("group_info call-id does not match offer")
		}
		if c, ok := optionalJID(&info, "call-creator"); ok && c != u.Creator {
			return nil, errors.New("group_info call-creator does not match offer")
		}
		if j, ok := nodeAttr(n, "joinable"); ok {
			u.Joinable = j == "1"
		}
	case envelopeUpdate:
		if av, ok := n.GetOptionalChildByTag("av_upgrade"); ok {
			v, _ := nodeAttr(&av, "av-upgradable")
			u.AVUpgradable = v == "1"
		}
		fallthrough
	case envelopeAck:
		if r, ok := n.GetOptionalChildByTag("relay"); ok {
			if u.Relay, err = parseGroupRelay(&r); err != nil {
				return nil, err
			}
		}
	}
	return u, nil
}

func parseGroupInfo(n *waBinary.Node, callID string, creator types.JID) (*groupcall.Update, error) {
	u := &groupcall.Update{CallID: callID, Creator: creator}
	u.Group, _ = optionalJID(n, "group-jid")
	var err error
	if u.Media, err = requiredString(n, "media"); err != nil {
		return nil, err
	}
	if u.TransactionID, err = requiredU32(n, "transaction-id"); err != nil {
		return nil, err
	}
	if u.ConnectedLimit, err = requiredU32(n, "connected-limit"); err != nil {
		return nil, err
	}
	u.Joinable = boolAttr(n, "joinable")
	u.RekeyRequested = boolAttr(n, "rekey")
	for _, c := range n.GetChildren() {
		if c.Tag != "user" {
			continue
		}
		p, err := parseGroupParticipant(&c)
		if err != nil {
			return nil, err
		}
		u.Participants = append(u.Participants, p)
	}
	return u, nil
}

func parseGroupParticipant(n *waBinary.Node) (groupcall.Participant, error) {
	var p groupcall.Participant
	var err error
	if p.JID, err = requiredJID(n, "jid"); err != nil {
		return p, err
	}
	p.PN, _ = optionalJID(n, "user_pn")
	p.State, _ = nodeAttr(n, "state")
	p.Type, _ = nodeAttr(n, "type")
	for _, c := range n.GetChildren() {
		if c.Tag != "device" {
			continue
		}
		d, err := parseGroupDevice(&c)
		if err != nil {
			return p, err
		}
		p.Devices = append(p.Devices, d)
	}
	return p, nil
}

func parseGroupDevice(n *waBinary.Node) (groupcall.Device, error) {
	var d groupcall.Device
	var err error
	if d.JID, err = requiredJID(n, "jid"); err != nil {
		return d, err
	}
	d.Platform, _ = nodeAttr(n, "platform")
	if d.PID, d.HasPID, err = optionalU32(n, "pid"); err != nil {
		return d, err
	}
	if c, ok := n.GetOptionalChildByTag("capability"); ok {
		if d.CapabilityVersion, d.HasCapabilityVersion, err = optionalU32(&c, "ver"); err != nil {
			return d, err
		}
		d.Capability = contentBytes(&c)
	}
	return d, nil
}

func parseGroupRelay(n *waBinary.Node) (*groupcall.Relay, error) {
	r := &groupcall.Relay{}
	var err error
	if r.TransactionID, r.HasTransactionID, err = optionalU32(n, "transaction-id"); err != nil {
		return nil, err
	}
	if r.SelfPID, r.HasSelfPID, err = optionalU32(n, "self_pid"); err != nil {
		return nil, err
	}
	if r.UUID, err = requiredString(n, "uuid"); err != nil {
		return nil, err
	}
	if r.ParticipantUUID, err = requiredString(n, "participant_uuid"); err != nil {
		return nil, err
	}
	r.AttributePadding = boolAttr(n, "attribute_padding")
	if r.WarpMITagLen, r.HasWarpMITagLen, err = optionalU32(n, "warp_mi_tag_len"); err != nil {
		return nil, err
	}
	children := n.GetChildren()
	content := func(tag string) []byte {
		for i := range children {
			if children[i].Tag == tag {
				return contentBytes(&children[i])
			}
		}
		return []byte{}
	}
	r.Key, r.HBHKey = content("key"), content("hbh_key")
	r.Tokens, r.AuthTokens = parseIndexedTokens(children, "token"), parseIndexedTokens(children, "auth_token")
	for i := range children {
		if children[i].Tag != "te2" {
			continue
		}
		e, err := parseGroupRelayEndpoint(&children[i])
		if err != nil {
			return nil, err
		}
		r.Endpoints = append(r.Endpoints, e)
	}
	return r, nil
}

func parseGroupRelayEndpoint(n *waBinary.Node) (groupcall.RelayEndpoint, error) {
	var e groupcall.RelayEndpoint
	var err error
	if e.RelayID, err = requiredU32(n, "relay_id"); err != nil {
		return e, err
	}
	if e.TokenID, err = requiredU32(n, "token_id"); err != nil {
		return e, err
	}
	if e.AuthTokenID, err = requiredU32(n, "auth_token_id"); err != nil {
		return e, err
	}
	if e.Name, err = requiredString(n, "relay_name"); err != nil {
		return e, err
	}
	e.Domain, _ = nodeAttr(n, "domain_name")
	if e.RTTms, e.HasRTT, err = optionalU32(n, "c2r_rtt"); err != nil {
		return e, err
	}
	e.FNA = boolAttr(n, "is_fna")
	e.Address = contentBytes(n)
	if a := e.Address; len(a) == 6 {
		e.IPv4 = fmt.Sprintf("%d.%d.%d.%d", a[0], a[1], a[2], a[3])
		e.Port = uint16(a[4])<<8 | uint16(a[5])
	}
	return e, nil
}

// parseIndexedTokens places each <tag id=N> at index N (its position when
// id is absent), dropping indexes past maxRelayTokens.
func parseIndexedTokens(children []waBinary.Node, tag string) [][]byte {
	values := [][]byte{}
	for i := range children {
		c := &children[i]
		if c.Tag != tag {
			continue
		}
		value, ok := c.Content.([]byte)
		if !ok {
			continue
		}
		index := len(values)
		if raw, ok := nodeAttr(c, "id"); ok {
			if v, err := strconv.ParseUint(raw, 10, 64); err == nil {
				index = int(min(v, maxRelayTokens))
			}
		}
		if index >= maxRelayTokens {
			continue
		}
		for len(values) <= index {
			values = append(values, []byte{})
		}
		values[index] = append([]byte(nil), value...)
	}
	return values
}

// BuildGroupEncRekey is one keygen-v2 group epoch delivery.
func BuildGroupEncRekey(p GroupEncRekeyParams) (waBinary.Node, error) {
	if err := validateCallIdentity(p.CallID, p.ID, p.Creator); err != nil {
		return waBinary.Node{}, err
	}
	switch {
	case p.TransactionID == 0:
		return waBinary.Node{}, errors.New("group epoch transaction id must be non-zero")
	case p.Key.Device != p.To:
		return waBinary.Node{}, errors.New("group epoch ciphertext target does not match stanza target")
	case len(p.Key.Ciphertext) == 0:
		return waBinary.Node{}, errors.New("group epoch ciphertext is empty")
	case p.Key.Type != "msg" && p.Key.Type != "pkmsg":
		return waBinary.Node{}, errors.New("unsupported group epoch encryption type")
	case p.Key.Type == "pkmsg" && p.DeviceIdentity == nil:
		return waBinary.Node{}, errors.New("pre-key group epoch requires device identity")
	}
	children := []waBinary.Node{encopt(), encNode(p.Key)}
	if p.DeviceIdentity != nil {
		children = append(children, node("device-identity", nil, append([]byte(nil), p.DeviceIdentity...)))
	}
	act := node(TagEncRekey, waBinary.Attrs{
		"call-id": p.CallID, "call-creator": p.Creator,
		"transaction-id": strconv.FormatUint(uint64(p.TransactionID), 10),
	}, children)
	return callWrap(p.To, p.ID, act), nil
}

// ParseGroupEncRekey parses one keygen-v2 group epoch delivery.
func ParseGroupEncRekey(n *waBinary.Node) (groupcall.EncRekey, error) {
	var r groupcall.EncRekey
	if n.Tag != TagEncRekey {
		return r, errors.New("expected an enc_rekey stanza")
	}
	var err error
	if r.CallID, err = requiredString(n, "call-id"); err != nil {
		return r, err
	}
	if r.Creator, err = requiredJID(n, "call-creator"); err != nil {
		return r, err
	}
	if r.TransactionID, err = requiredU32(n, "transaction-id"); err != nil {
		return r, err
	}
	eo, ok := n.GetOptionalChildByTag("encopt")
	if !ok {
		return r, errors.New("enc_rekey is missing encopt")
	}
	enc, ok := n.GetOptionalChildByTag("enc")
	if !ok {
		return r, errors.New("enc_rekey is missing enc")
	}
	if r.KeyGeneration, err = requiredU32(&eo, "keygen"); err != nil {
		return r, err
	}
	if r.EncryptionType, err = requiredString(&enc, "type"); err != nil {
		return r, err
	}
	if r.EncryptionVersion, err = requiredU32(&enc, "v"); err != nil {
		return r, err
	}
	if r.KeyGeneration != 2 || r.EncryptionVersion != 2 || (r.EncryptionType != "msg" && r.EncryptionType != "pkmsg") {
		return r, errors.New("unsupported group epoch encryption")
	}
	if r.Ciphertext = contentBytes(&enc); len(r.Ciphertext) == 0 {
		return r, errors.New("enc_rekey has no ciphertext")
	}
	return r, nil
}

// BuildCallLinkCreate is a call-link creation request.
func BuildCallLinkCreate(media groupcall.CallLinkMedia, requestID string) (waBinary.Node, error) {
	return buildCallServiceRequest(requestID, node("link_create", waBinary.Attrs{"media": string(media)}, nil))
}

// BuildCallLinkQuery is a call-link preview request.
func BuildCallLinkQuery(token string, media groupcall.CallLinkMedia, requestID string) (waBinary.Node, error) {
	if strings.TrimSpace(token) == "" {
		return waBinary.Node{}, errors.New("call-link token is required")
	}
	return buildCallServiceRequest(requestID, node("link_query", waBinary.Attrs{"token": token, "media": string(media)}, nil))
}

// BuildCallLinkJoin is a call-link join request with the default
// capability of the link's media.
func BuildCallLinkJoin(token string, media groupcall.CallLinkMedia, requestID string) (waBinary.Node, error) {
	c := CapabilityOffer
	if media == groupcall.LinkVideo {
		c = CapabilityVideoOffer
	}
	return BuildCallLinkJoinWithCapability(token, media, requestID, c)
}

// BuildCallLinkJoinWithCapability is a call-link join request with an
// explicit version-1 capability.
func BuildCallLinkJoinWithCapability(token string, media groupcall.CallLinkMedia, requestID string, capability []byte) (waBinary.Node, error) {
	if strings.TrimSpace(token) == "" {
		return waBinary.Node{}, errors.New("call-link token is required")
	}
	if len(capability) == 0 {
		return waBinary.Node{}, errors.New("call-link capability is required")
	}
	children := []waBinary.Node{audioOpus("16000")}
	if media == groupcall.LinkVideo {
		// The call being joined has no handle yet.
		children = append(children, node("video", waBinary.Attrs{"dec": "H264", "device_orientation": "0"}, nil))
	}
	children = append(children, node("net", waBinary.Attrs{"medium": "2"}, nil), capabilityNode(capability))
	return buildCallServiceRequest(requestID, node(TagLinkJoin, waBinary.Attrs{"token": token, "media": string(media)}, children))
}

func buildCallServiceRequest(requestID string, act waBinary.Node) (waBinary.Node, error) {
	if requestID == "" {
		return waBinary.Node{}, errors.New("call request id is required")
	}
	return callWrap(CallService, requestID, act), nil
}

// ParseCallLinkCreateAck parses a link-creation answer.
func ParseCallLinkCreateAck(n *waBinary.Node) (groupcall.CallLink, error) {
	var l groupcall.CallLink
	c, err := validatedCallAckChild(n, "link_create")
	if err != nil {
		return l, err
	}
	if l.Token, err = requiredString(c, "token"); err != nil {
		return l, err
	}
	l.Media, err = requiredLinkMedia(c)
	return l, err
}

// ParseCallLinkQueryAck parses a link-preview answer.
func ParseCallLinkQueryAck(n *waBinary.Node) (groupcall.CallLinkPreview, error) {
	var p groupcall.CallLinkPreview
	c, err := validatedCallAckChild(n, "link_query")
	if err != nil {
		return p, err
	}
	if p.Token, err = requiredString(c, "token"); err != nil {
		return p, err
	}
	if p.Media, err = requiredLinkMedia(c); err != nil {
		return p, err
	}
	if p.Creator, err = requiredJID(c, "link_creator"); err != nil {
		return p, err
	}
	p.CreatorPN, _ = optionalJID(c, "link_creator_pn")
	if w, ok := c.GetOptionalChildByTag("waiting_room"); ok {
		p.WaitingRoomEnabled, p.IsAdmin = boolAttr(&w, "enabled"), boolAttr(&w, "is_admin")
	}
	return p, nil
}

// ParseCallLinkJoinAck parses a link join that admitted this endpoint or
// put it in a waiting room, bound to the token requested.
func ParseCallLinkJoinAck(n *waBinary.Node, requestedToken string) (groupcall.CallLinkJoin, error) {
	var j groupcall.CallLinkJoin
	if requestedToken == "" {
		return j, errors.New("requested call-link token is empty")
	}
	if err := validateCallAck(n, TagLinkJoin); err != nil {
		return j, err
	}
	var waiting *groupcall.WaitingRoom
	if w, ok := n.GetOptionalChildByTag("waiting_room"); ok {
		room, err := parseWaitingRoom(&w)
		if err != nil {
			return j, err
		}
		waiting = &room
	}
	group, err := ParseInitialGroupCallAck(n)
	if err != nil {
		return j, err
	}
	switch {
	case waiting != nil:
		j.Media, j.CallID, j.Creator, j.WaitingRoomEnabled, j.IsAdmin = waiting.Media, waiting.CallID, waiting.Creator, waiting.Enabled, waiting.IsAdmin
	case group != nil:
		m, ok := groupcall.ParseLinkMedia(group.Media)
		if !ok {
			return j, errors.New("unsupported call-link media")
		}
		j.Media, j.CallID, j.Creator = m, group.CallID, group.Creator
	default:
		return j, errors.New("<link_join> ack has neither waiting_room nor group_info")
	}
	if waiting != nil && waiting.LinkToken != requestedToken {
		return j, errors.New("waiting-room token differs from the requested call link")
	}
	if group == nil && !j.WaitingRoomEnabled {
		return j, errors.New("<link_join> waiting-room-only ack is not enabled")
	}
	if group != nil && (group.CallID != j.CallID || group.Creator != j.Creator) {
		return j, errors.New("waiting-room and admitted group identities differ")
	}
	if group != nil && group.Media != string(j.Media) {
		return j, errors.New("waiting-room and admitted group media modes differ")
	}
	j.Token = requestedToken
	j.InWaitingRoom = waiting != nil && j.WaitingRoomEnabled && group == nil
	j.WaitingRoom, j.Group = waiting, group
	return j, nil
}

// ParseCallLinkJoinCallID is a valid link-join ack's call id, before the
// full validation of ParseCallLinkJoinAck.
func ParseCallLinkJoinCallID(n *waBinary.Node) (string, error) {
	if err := validateCallAck(n, TagLinkJoin); err != nil {
		return "", err
	}
	var waiting, group string
	if w, ok := n.GetOptionalChildByTag("waiting_room"); ok {
		id, err := requiredString(&w, "call-id")
		if err != nil {
			return "", err
		}
		waiting = id
	}
	if g, ok := n.GetOptionalChildByTag("group_info"); ok {
		id, err := requiredString(&g, "call-id")
		if err != nil {
			return "", err
		}
		group = id
	}
	switch {
	case waiting != "" && group != "" && waiting != group:
		return "", errors.New("waiting-room and admitted group identities differ")
	case waiting != "":
		return waiting, nil
	case group != "":
		return group, nil
	}
	return "", errors.New("<link_join> ack has neither waiting_room nor group_info")
}

// ParseWaitingRoomUpdate parses one authoritative <waiting_room_update>.
func ParseWaitingRoomUpdate(n *waBinary.Node) (groupcall.WaitingRoom, error) {
	if n.Tag != TagWaitingRoomUpdate {
		return groupcall.WaitingRoom{}, errors.New("expected a waiting_room_update stanza")
	}
	callID, err := requiredString(n, "call-id")
	if err != nil {
		return groupcall.WaitingRoom{}, err
	}
	creator, err := requiredJID(n, "call-creator")
	if err != nil {
		return groupcall.WaitingRoom{}, err
	}
	w, ok := n.GetOptionalChildByTag("waiting_room")
	if !ok {
		return groupcall.WaitingRoom{}, errors.New("waiting_room_update is missing waiting_room")
	}
	room, err := parseWaitingRoom(&w)
	if err != nil {
		return room, err
	}
	if room.CallID != callID || room.Creator != creator {
		return groupcall.WaitingRoom{}, errors.New("waiting-room update identity mismatch")
	}
	return room, nil
}

// waitingRoomUserSize approximates WaitingRoomUser's retained size: the
// struct and its strings, as wacore's HeapSize counts them.
const waitingRoomUserSize = 3*48 + 16

func parseWaitingRoom(n *waBinary.Node) (groupcall.WaitingRoom, error) {
	var r groupcall.WaitingRoom
	if n.Tag != "waiting_room" {
		return r, errors.New("expected a waiting_room stanza")
	}
	var err error
	if r.CallID, err = requiredString(n, "call-id"); err != nil {
		return r, err
	}
	if r.Creator, err = requiredJID(n, "call-creator"); err != nil {
		return r, err
	}
	if r.LinkToken, err = requiredString(n, "link-token"); err != nil {
		return r, err
	}
	if r.Media, err = requiredLinkMedia(n); err != nil {
		return r, err
	}
	r.Enabled, r.IsAdmin = boolAttr(n, "enabled"), boolAttr(n, "is_admin")
	if r.TransactionID, r.HasTransactionID, err = optionalU32(n, "transaction-id"); err != nil {
		return r, err
	}
	retained := 0
	for _, c := range n.GetChildren() {
		if c.Tag != "user" {
			continue
		}
		if len(r.Users) >= maxWaitingRoomUsers {
			return r, errors.New("waiting-room snapshot exceeds the user limit")
		}
		var u groupcall.WaitingRoomUser
		if u.JID, err = requiredJID(&c, "jid"); err != nil {
			return r, err
		}
		u.PN, _ = optionalJID(&c, "user_pn")
		if u.State, err = requiredString(&c, "state"); err != nil {
			return r, err
		}
		retained += waitingRoomUserSize + len(u.JID.User) + len(u.PN.User) + len(u.State)
		if retained > maxWaitingRoomRetainedBytes {
			return r, errors.New("waiting-room snapshot exceeds the retained-byte limit")
		}
		r.Users = append(r.Users, u)
	}
	return r, nil
}

// BuildWaitingRoomToggle turns a live link call's approval on or off.
func BuildWaitingRoomToggle(callID string, creator types.JID, enabled bool, requestID string) (waBinary.Node, error) {
	if err := validateCallIdentity(callID, requestID, creator); err != nil {
		return waBinary.Node{}, err
	}
	v := "0"
	if enabled {
		v = "1"
	}
	return buildWaitingRoomRequest("waiting_room_toggle", callID, creator, requestID, waBinary.Attrs{"enabled": v}, nil), nil
}

// BuildWaitingRoomHeartbeat keeps a pending call-link join alive.
func BuildWaitingRoomHeartbeat(callID string, creator types.JID, requestID string) (waBinary.Node, error) {
	if err := validateCallIdentity(callID, requestID, creator); err != nil {
		return waBinary.Node{}, err
	}
	return buildWaitingRoomRequest("heartbeat", callID, creator, requestID, waBinary.Attrs{"type": "waiting_room"}, nil), nil
}

// BuildWaitingRoomAdmit admits one waiting user.
func BuildWaitingRoomAdmit(callID string, creator, user types.JID, requestID string) (waBinary.Node, error) {
	if err := validateCallIdentity(callID, requestID, creator); err != nil {
		return waBinary.Node{}, err
	}
	return buildWaitingRoomUserAction("waiting_room_admit", callID, creator, user, requestID)
}

// BuildWaitingRoomDeny denies one waiting user.
func BuildWaitingRoomDeny(callID string, creator, user types.JID, requestID string) (waBinary.Node, error) {
	if err := validateCallIdentity(callID, requestID, creator); err != nil {
		return waBinary.Node{}, err
	}
	return buildWaitingRoomUserAction("waiting_room_deny", callID, creator, user, requestID)
}

// ParseWaitingRoomToggleAck, ParseWaitingRoomAdmitAck and
// ParseWaitingRoomDenyAck validate the service's answers.
func ParseWaitingRoomToggleAck(n *waBinary.Node) error {
	return validateCallAck(n, "waiting_room_toggle")
}
func ParseWaitingRoomAdmitAck(n *waBinary.Node) error {
	return validateCallAck(n, "waiting_room_admit")
}
func ParseWaitingRoomDenyAck(n *waBinary.Node) error { return validateCallAck(n, "waiting_room_deny") }

func buildWaitingRoomUserAction(tag, callID string, creator, user types.JID, requestID string) (waBinary.Node, error) {
	if user.User == "" {
		return waBinary.Node{}, errors.New("waiting-room user is required")
	}
	return buildWaitingRoomRequest(tag, callID, creator, requestID, nil,
		[]waBinary.Node{node("user", waBinary.Attrs{"jid": user}, nil)}), nil
}

func buildWaitingRoomRequest(tag, callID string, creator types.JID, requestID string, extra waBinary.Attrs, children []waBinary.Node) waBinary.Node {
	attrs := waBinary.Attrs{"call-id": callID, "call-creator": creator}
	for k, v := range extra {
		attrs[k] = v
	}
	var content any
	if len(children) > 0 {
		content = children
	}
	return callWrap(CallJID(callID), requestID, node(tag, attrs, content))
}

// BuildRaiseHand is a persistent raise or lower of the hand.
func BuildRaiseHand(callID string, to, creator types.JID, requestID string, raised bool) (waBinary.Node, error) {
	if err := validateCallIdentity(callID, requestID, creator); err != nil {
		return waBinary.Node{}, err
	}
	if to.User == "" {
		return waBinary.Node{}, errors.New("raise-hand target is required")
	}
	state := "0"
	if raised {
		state = "1"
	}
	return callWrap(to, requestID, node(TagRaiseHand,
		waBinary.Attrs{"call-id": callID, "call-creator": creator, "action": "raise_hand"},
		[]waBinary.Node{node("raise_hand", waBinary.Attrs{"raise-hand-state": state}, nil)})), nil
}

// ParseRaiseHand parses a raise or lower of the hand.
func ParseRaiseHand(n *waBinary.Node) (bool, error) {
	if n.Tag != TagRaiseHand {
		return false, errors.New("expected a user_action stanza")
	}
	act, err := requiredString(n, "action")
	if err != nil {
		return false, err
	}
	if _, err := requiredString(n, "call-id"); err != nil {
		return false, err
	}
	if _, err := requiredJID(n, "call-creator"); err != nil {
		return false, err
	}
	if act != "raise_hand" {
		return false, errors.New("unsupported group user action")
	}
	rh, ok := n.GetOptionalChildByTag("raise_hand")
	if !ok {
		return false, errors.New("user_action is missing raise_hand")
	}
	switch v, _ := nodeAttr(&rh, "raise-hand-state"); v {
	case "1":
		return true, nil
	case "0":
		return false, nil
	}
	return false, errors.New("invalid raise-hand-state")
}

// BuildScreenShare is one version-2 screen-share transition.
func BuildScreenShare(callID string, to, creator types.JID, requestID string, state groupcall.ScreenShareState, id uint32, hasID bool) (waBinary.Node, error) {
	if err := validateCallIdentity(callID, requestID, creator); err != nil {
		return waBinary.Node{}, err
	}
	if to.User == "" {
		return waBinary.Node{}, errors.New("screen-share target is required")
	}
	attrs := waBinary.Attrs{"call-id": callID, "call-creator": creator,
		"screenshare_state": strconv.Itoa(int(state)), "version": "2"}
	if hasID {
		attrs["screen_share_id"] = strconv.FormatUint(uint64(id), 10)
	}
	return callWrap(to, requestID, node(TagScreenShare, attrs, nil)), nil
}

// ParseScreenShare parses one versioned screen-share transition.
func ParseScreenShare(n *waBinary.Node) (groupcall.ScreenShare, error) {
	var s groupcall.ScreenShare
	if n.Tag != TagScreenShare {
		return s, errors.New("expected a screen_share stanza")
	}
	if _, err := requiredString(n, "call-id"); err != nil {
		return s, err
	}
	if _, err := requiredJID(n, "call-creator"); err != nil {
		return s, err
	}
	state, err := requiredU32(n, "screenshare_state")
	if err != nil {
		return s, err
	}
	if s.State = groupcall.ScreenShareState(state); s.State != groupcall.ScreenShareStarted && s.State != groupcall.ScreenShareStopped {
		return s, errors.New("unsupported screen-share state")
	}
	if s.Version, err = requiredU32(n, "version"); err != nil {
		return s, err
	}
	s.ID, s.HasID, err = optionalU32(n, "screen_share_id")
	return s, err
}

// BuildCallControlAck is the typed <ack> a received group-call control
// (waiting_room_update, user_action, screen_share) needs in place of the
// generic one; false for a stanza that cannot be acknowledged.
func BuildCallControlAck(original *waBinary.Node, actionType string) (waBinary.Node, bool) {
	if original.Tag != "call" || actionType == "" {
		return waBinary.Node{}, false
	}
	id, okID := original.Attrs["id"]
	from, okFrom := original.Attrs["from"]
	if !okID || !okFrom {
		return waBinary.Node{}, false
	}
	attrs := waBinary.Attrs{"class": "call", "id": id, "to": from, "type": actionType}
	if p, ok := original.Attrs["participant"]; ok && fmt.Sprint(p) != fmt.Sprint(from) {
		attrs["participant"] = p
	}
	if r, ok := original.Attrs["recipient"]; ok {
		attrs["recipient"] = r
	}
	return node("ack", attrs, nil), true
}

func validateCallAck(n *waBinary.Node, expected string) error {
	class, _ := nodeAttr(n, "class")
	typ, _ := nodeAttr(n, "type")
	if n.Tag != "ack" || class != "call" || typ != expected {
		return fmt.Errorf("unexpected %s ack envelope", expected)
	}
	if e, ok := nodeAttr(n, "error"); ok && e != "" {
		return &AckError{Type: expected, Code: e}
	}
	return nil
}

// AckError is a call service's <ack error=…> refusal.
type AckError struct {
	Type string
	Code string
}

func (e *AckError) Error() string { return e.Type + " rejected with error " + e.Code }

func validatedCallAckChild(n *waBinary.Node, expected string) (*waBinary.Node, error) {
	if err := validateCallAck(n, expected); err != nil {
		return nil, err
	}
	c, ok := n.GetOptionalChildByTag(expected)
	if !ok {
		return nil, fmt.Errorf("<ack type=%s> missing payload", expected)
	}
	return &c, nil
}

func requiredLinkMedia(n *waBinary.Node) (groupcall.CallLinkMedia, error) {
	raw, err := requiredString(n, "media")
	if err != nil {
		return "", err
	}
	m, ok := groupcall.ParseLinkMedia(raw)
	if !ok {
		return "", errors.New("unsupported call-link media")
	}
	return m, nil
}

func videoOfferNode(orientation uint8) waBinary.Node {
	return node("video", waBinary.Attrs{
		"enc": "h264", "dec": "h264", "orientation": "0",
		"screen_width": "1920", "screen_height": "1080",
		"device_orientation": strconv.Itoa(int(orientation)),
	}, nil)
}

// nodeAttr is an attribute as text: a string as is, a JID in its string
// form (wacore's optional_string reads both).
func nodeAttr(n *waBinary.Node, name string) (string, bool) {
	switch v := n.Attrs[name].(type) {
	case string:
		return v, true
	case types.JID:
		return v.String(), true
	case nil:
		return "", false
	default:
		return fmt.Sprint(v), true
	}
}

func boolAttr(n *waBinary.Node, name string) bool {
	v, _ := nodeAttr(n, name)
	return v == "1"
}

func requiredString(n *waBinary.Node, name string) (string, error) {
	if v, ok := nodeAttr(n, name); ok && v != "" {
		return v, nil
	}
	return "", errors.New("missing group-call string attribute")
}

// optionalJID is a JID attribute; an unparseable one is absent.
func optionalJID(n *waBinary.Node, name string) (types.JID, bool) {
	switch v := n.Attrs[name].(type) {
	case types.JID:
		return v, true
	case string:
		j, err := types.ParseJID(v)
		if err != nil || v == "" {
			return types.JID{}, false
		}
		return j, true
	}
	return types.JID{}, false
}

func requiredJID(n *waBinary.Node, name string) (types.JID, error) {
	if j, ok := optionalJID(n, name); ok {
		return j, nil
	}
	return types.JID{}, errors.New("missing or invalid group-call jid")
}

func optionalU32(n *waBinary.Node, name string) (uint32, bool, error) {
	raw, ok := nodeAttr(n, name)
	if !ok {
		return 0, false, nil
	}
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, false, errors.New("invalid group-call integer attribute")
	}
	return uint32(v), true, nil
}

func requiredU32(n *waBinary.Node, name string) (uint32, error) {
	v, ok, err := optionalU32(n, name)
	if err == nil && !ok {
		err = errors.New("missing group-call integer attribute")
	}
	return v, err
}

func contentBytes(n *waBinary.Node) []byte {
	if b, ok := n.Content.([]byte); ok {
		return append([]byte{}, b...)
	}
	return []byte{}
}
