// Ported from whatsapp-rust 5e72baf wacore/src/types/group_call.rs, MIT.

// Package groupcall holds a WhatsApp group call's authoritative state: the
// roster snapshots the call service numbers by transaction, the relay they
// carry, the shared key epochs (enc_rekey), call links and their waiting
// room, and the state machine that orders them (state.go, from
// wacore/src/voip_control/group.rs). Both the signaling (gocore/calls) and
// the media engine (gocore/voip/engine) read it, so it depends on neither.
package groupcall

import (
	"fmt"
	"slices"

	"go.mau.fi/whatsmeow/types"
)

// MaxParticipants is the most endpoints a group call holds, ourselves
// included; MaxRemoteParticipants leaves our own slot out.
const (
	MaxParticipants       = 32
	MaxRemoteParticipants = MaxParticipants - 1
)

// CallLinkMedia is a call link's mode: "audio" or "video".
type CallLinkMedia string

const (
	LinkAudio CallLinkMedia = "audio"
	LinkVideo CallLinkMedia = "video"
)

// ParseLinkMedia reads a <link_*> media attribute.
func ParseLinkMedia(s string) (CallLinkMedia, bool) {
	switch CallLinkMedia(s) {
	case LinkAudio, LinkVideo:
		return CallLinkMedia(s), true
	}
	return "", false
}

// Device is one device of a roster participant.
type Device struct {
	JID      types.JID
	Platform string
	// PID is the relay's participant id of the device's media session; a
	// roster-only update leaves an unchanged one out (HasPID false). PID 0
	// is real: the call creator has it.
	PID    uint32
	HasPID bool
	// CapabilityVersion and Capability are the device's <capability>.
	CapabilityVersion    uint32
	HasCapabilityVersion bool
	Capability           []byte
}

// Participant is one user of a roster.
type Participant struct {
	JID types.JID
	// PN is the phone-number alias the roster names, kept for routing.
	PN    types.JID
	State string // "connected", "invited", ...; "" when absent
	Type  string
	// Devices are the user's devices in the call.
	Devices []Device
}

// Connected: the call service counts the participant as in the call.
func (p *Participant) Connected() bool { return p.State == "connected" }

// RelayEndpoint is one <te2> of a group relay.
type RelayEndpoint struct {
	RelayID     uint32
	TokenID     uint32
	AuthTokenID uint32
	Name        string
	Domain      string
	RTTms       uint32
	HasRTT      bool
	FNA         bool
	Address     []byte
	// IPv4 and Port are Address when it is six bytes.
	IPv4 string
	Port uint16
}

// Relay is a group call's <relay>: its allocation, numbered by its own
// transaction (not the roster's).
type Relay struct {
	TransactionID    uint32
	HasTransactionID bool
	SelfPID          uint32
	HasSelfPID       bool
	UUID             string
	ParticipantUUID  string
	AttributePadding bool
	WarpMITagLen     uint32
	HasWarpMITagLen  bool
	Key              []byte // the STUN integrity key, as it arrived
	HBHKey           []byte
	Tokens           [][]byte
	AuthTokens       [][]byte
	Endpoints        []RelayEndpoint
}

// String keeps the keys and tokens out of logs.
func (r *Relay) String() string {
	return fmt.Sprintf("groupcall.Relay{tx: %d/%v, %d endpoints, keys: [redacted]}", r.TransactionID, r.HasTransactionID, len(r.Endpoints))
}

// Update is one authoritative group snapshot (<group_info>, from an offer,
// a <group_update> or an ack), ordered by TransactionID.
type Update struct {
	CallID         string
	Creator        types.JID
	Group          types.JID // the group-jid; empty for an ad-hoc call
	TransactionID  uint32
	Media          string // "audio" or "video"
	ConnectedLimit uint32
	Joinable       bool
	AVUpgradable   bool
	// RekeyRequested: the receiver must make and fan out a new epoch.
	RekeyRequested bool
	Participants   []Participant
	Relay          *Relay
}

// Clone is a deep copy: an update crosses goroutines (the signaling to the
// media).
func (u *Update) Clone() *Update {
	if u == nil {
		return nil
	}
	c := *u
	c.Participants = make([]Participant, len(u.Participants))
	for i, p := range u.Participants {
		p.Devices = slices.Clone(p.Devices)
		for j := range p.Devices {
			p.Devices[j].Capability = slices.Clone(p.Devices[j].Capability)
		}
		c.Participants[i] = p
	}
	if u.Relay != nil {
		r := *u.Relay
		r.Key, r.HBHKey = slices.Clone(r.Key), slices.Clone(r.HBHKey)
		r.Tokens, r.AuthTokens = cloneTokens(r.Tokens), cloneTokens(r.AuthTokens)
		r.Endpoints = slices.Clone(r.Endpoints)
		for i := range r.Endpoints {
			r.Endpoints[i].Address = slices.Clone(r.Endpoints[i].Address)
		}
		c.Relay = &r
	}
	return &c
}

func cloneTokens(t [][]byte) [][]byte {
	if t == nil {
		return nil
	}
	out := make([][]byte, len(t))
	for i, v := range t {
		out[i] = slices.Clone(v)
	}
	return out
}

// EncRekey is one keygen-v2 epoch delivery: a Signal message holding the
// 32-byte epoch for TransactionID.
type EncRekey struct {
	CallID            string
	Creator           types.JID
	TransactionID     uint32
	KeyGeneration     uint32
	EncryptionType    string // "msg" or "pkmsg"
	EncryptionVersion uint32
	Ciphertext        []byte
}

// CallLink is a created link.
type CallLink struct {
	Token string
	Media CallLinkMedia
}

// URL is the link as WhatsApp shares it.
func (l CallLink) URL() string { return "https://call.whatsapp.com/" + string(l.Media) + "/" + l.Token }

// CallLinkPreview is a link's metadata, without joining.
type CallLinkPreview struct {
	Token              string
	Media              CallLinkMedia
	Creator            types.JID
	CreatorPN          types.JID
	WaitingRoomEnabled bool
	IsAdmin            bool
}

// WaitingRoomUser is one user of a waiting-room snapshot.
type WaitingRoomUser struct {
	JID   types.JID
	PN    types.JID
	State string
}

// WaitingRoom is a call link's waiting room.
type WaitingRoom struct {
	CallID           string
	Creator          types.JID
	LinkToken        string
	Media            CallLinkMedia
	Enabled          bool
	IsAdmin          bool
	TransactionID    uint32
	HasTransactionID bool
	Users            []WaitingRoomUser
}

// CallLinkJoin is a link join's answer: admitted (Group) or waiting.
type CallLinkJoin struct {
	Token              string
	Media              CallLinkMedia
	CallID             string
	Creator            types.JID
	WaitingRoomEnabled bool
	InWaitingRoom      bool
	IsAdmin            bool
	WaitingRoom        *WaitingRoom
	Group              *Update
}

// ScreenShareState is a <screen_share>'s screenshare_state.
type ScreenShareState int

const (
	ScreenShareStarted ScreenShareState = 1
	ScreenShareStopped ScreenShareState = 2
)

// ScreenShare is one participant's screen-share state.
type ScreenShare struct {
	State   ScreenShareState
	Version uint32
	ID      uint32
	HasID   bool
}
