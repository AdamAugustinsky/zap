package calls

import (
	"strconv"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Signal is one incoming <call> stanza, as whatsmeow delivers it: its
// wrapper's meta and its one action node. It follows whatsapp-rust's
// parse_call_stanza (wacore/src/stanza/call.rs) for what the 1:1 signaling
// reads.
type Signal struct {
	// Tag is the action's: offer, preaccept, accept, reject, terminate,
	// video, mute_v2, relaylatency, transport, offer_notice, ...
	Tag string
	// From is the device that sent it.
	From     types.JID
	StanzaID string
	Time     time.Time
	CallID   string
	Creator  types.JID
	// Group is the group-jid of a group call's stanza.
	Group  types.JID
	Action *waBinary.Node
}

// FromEvent reads a whatsmeow call event; ok is false for anything else.
func FromEvent(evt any) (s Signal, ok bool) {
	var meta types.BasicCallMeta
	var action *waBinary.Node
	tag := ""
	switch v := evt.(type) {
	case *events.CallOffer:
		meta, action, tag = v.BasicCallMeta, v.Data, "offer"
	case *events.CallPreAccept:
		meta, action, tag = v.BasicCallMeta, v.Data, "preaccept"
	case *events.CallAccept:
		meta, action, tag = v.BasicCallMeta, v.Data, "accept"
	case *events.CallReject:
		meta, action, tag = v.BasicCallMeta, v.Data, "reject"
	case *events.CallTerminate:
		meta, action, tag = v.BasicCallMeta, v.Data, "terminate"
	case *events.CallRelayLatency:
		meta, action, tag = v.BasicCallMeta, v.Data, "relaylatency"
	case *events.CallTransport:
		meta, action, tag = v.BasicCallMeta, v.Data, "transport"
	case *events.CallOfferNotice:
		meta, action, tag = v.BasicCallMeta, v.Data, "offer_notice"
	case *events.UnknownCallEvent:
		// <video>, <mute_v2>, <enc_rekey> and the rest whatsmeow has no
		// type for, with the whole <call>.
		return fromNode(v.Node)
	default:
		return Signal{}, false
	}
	if action == nil {
		return Signal{}, false
	}
	return Signal{Tag: tag, From: meta.From, StanzaID: meta.StanzaID, Time: meta.Timestamp,
		CallID: meta.CallID, Creator: meta.CallCreator, Group: meta.GroupJID, Action: action}, true
}

func fromNode(n *waBinary.Node) (Signal, bool) {
	if n == nil || n.Tag != "call" {
		return Signal{}, false
	}
	children := n.GetChildren()
	if len(children) != 1 {
		return Signal{}, false
	}
	child := &children[0]
	ag, cag := n.AttrGetter(), child.AttrGetter()
	s := Signal{
		Tag:      child.Tag,
		From:     ag.JID("from"),
		StanzaID: ag.OptionalString("id"),
		Time:     ag.UnixTime("t"),
		CallID:   cag.OptionalString("call-id"),
		Creator:  cag.OptionalJIDOrEmpty("call-creator"),
		Group:    cag.OptionalJIDOrEmpty("group-jid"),
		Action:   child,
	}
	return s, s.CallID != ""
}

func (s Signal) attr(name string) string {
	v, _ := s.Action.Attrs[name].(string)
	return v
}

// Reason is a reject's or a terminate's reason attribute.
func (s Signal) Reason() string { return s.attr("reason") }

// child is the action's first child named tag.
func (s Signal) child(tag string) (*waBinary.Node, bool) {
	for _, c := range s.Action.GetChildren() {
		if c.Tag == tag {
			return &c, true
		}
	}
	return nil, false
}

// IsGroup: a group call's offer (a group-jid, or a <group_info>), which
// the 1:1 bridge refuses.
func (s Signal) IsGroup() bool {
	if !s.Group.IsEmpty() {
		return true
	}
	_, ok := s.child("group_info")
	return ok
}

// Video: an offer (or accept) that carries a <video>.
func (s Signal) Video() bool {
	_, ok := s.child("video")
	return ok
}

// sameDevice compares a user, server and device, not the agent the wire
// may add.
func sameDevice(a, b types.JID) bool {
	return a.User == b.User && a.Server == b.Server && a.Device == b.Device
}

// OfferKey is the <enc> an offer addresses to own (MediaOffer::enc_for):
// the one under <destination><to jid=own>, else the one bare <enc> of a
// single-device offer.
func (s Signal) OfferKey(own types.JID) (*waBinary.Node, bool) {
	if enc, ok := s.child("enc"); ok {
		return enc, true
	}
	dest, ok := s.child("destination")
	if !ok {
		return nil, false
	}
	for _, to := range dest.GetChildren() {
		if to.Tag != "to" {
			continue
		}
		jid, ok := to.Attrs["jid"].(types.JID)
		if !ok || !sameDevice(jid, own) {
			continue
		}
		for _, enc := range to.GetChildren() {
			if enc.Tag == "enc" {
				return &enc, true
			}
		}
	}
	return nil, false
}

// MLow reads the action's <capability> at the MLow index: unknown when it
// carries none.
func (s Signal) MLow() CapabilityBit {
	c, ok := s.child("capability")
	if !ok {
		return CapabilityUnknown
	}
	blob, _ := c.Content.([]byte)
	v, err := strconv.ParseUint(c.AttrGetter().OptionalString("ver"), 10, 32)
	return ReadCapability(uint32(v), err == nil, blob, CapabilityIndexMLowV1)
}

// Metadata is an offer's <metadata> experiment buckets, which a video
// accept mirrors.
func (s Signal) Metadata(p *AcceptParams) {
	m, ok := s.child("metadata")
	if !ok {
		return
	}
	if v, ok := m.Attrs["peer_abtest_bucket"].(string); ok {
		p.AbTestBucket, p.HasAbTestBucket = v, true
	}
	if v, ok := m.Attrs["peer_abtest_bucket_id_list"].(string); ok {
		p.AbTestBucketIDList, p.HasAbTestBucketIDList = v, true
	}
}

// VideoState is a <video>'s state.
func (s Signal) VideoState() (VideoState, bool) {
	n, err := strconv.Atoi(s.attr("state"))
	return VideoState(n), err == nil
}

// Orientation is a <video>'s device_orientation, 0 to 3.
func (s Signal) Orientation() (uint8, bool) {
	n, err := strconv.ParseUint(s.attr("device_orientation"), 10, 8)
	return uint8(n), err == nil && n <= 3
}
