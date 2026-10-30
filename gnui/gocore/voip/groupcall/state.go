// Ported from whatsapp-rust 5e72baf wacore/src/voip_control/group.rs, MIT.

package groupcall

import (
	"net/netip"

	"go.mau.fi/whatsmeow/types"

	"zapfast/gocore/voip/kdf"
)

// Apply is what applying an authoritative update did.
type Apply int

const (
	Applied Apply = iota
	Stale
	UnknownCall
	IdentityMismatch
	InvalidSnapshot
)

func (a Apply) String() string {
	switch a {
	case Applied:
		return "applied"
	case Stale:
		return "stale"
	case UnknownCall:
		return "unknown call"
	case IdentityMismatch:
		return "identity mismatch"
	}
	return "invalid snapshot"
}

// WebClientRelayPort is the relay port WhatsApp Web dials; an endpoint on
// it is preferred (relay_parse.rs).
const WebClientRelayPort = 3480

// relayStreamSlotCount is how many relay stream SSRCs one participant has.
const relayStreamSlotCount = 9

// State is one group call's authoritative state. The server owns the
// roster: updates are transaction ordered and an accepted roster replaces
// the previous one whole. A missing relay means no allocation refresh, so
// the last usable relay is carried across roster-only updates.
type State struct {
	callID      string
	creator     types.JID
	snapshot    *Update
	waitingRoom *WaitingRoom
	roomTx      uint32
	hasRoomTx   bool
	hands       map[types.JID]bool
	shares      map[types.JID]ScreenShare
}

// NewState is a call's state before its first snapshot.
func NewState(callID string, creator types.JID) *State {
	return &State{callID: callID, creator: creator, hands: map[types.JID]bool{}, shares: map[types.JID]ScreenShare{}}
}

// Snapshot is the roster in force, nil before the first; it must not be
// changed.
func (s *State) Snapshot() *Update { return s.snapshot }

// WaitingRoom is the link's waiting room, nil when none was seen.
func (s *State) WaitingRoom() *WaitingRoom { return s.waitingRoom }

// RaisedHands are the participants with a raised hand.
func (s *State) RaisedHands() map[types.JID]bool { return s.hands }

// ScreenShares are the participants sharing a screen.
func (s *State) ScreenShares() map[types.JID]ScreenShare { return s.shares }

func (s *State) matches(callID string, creator types.JID) bool {
	return s.callID == callID && s.creator == creator
}

// advancesRelay: incoming is the first relay, or newer than the one held.
// A relay without a transaction never replaces one held.
func advancesRelay(held, incoming *Relay) bool {
	if incoming == nil {
		return false
	}
	if held == nil {
		return true
	}
	return incoming.HasTransactionID && (!held.HasTransactionID || incoming.TransactionID > held.TransactionID)
}

// ApplyUpdate advances the roster and the relay allocation on their own
// transactions. It works on a copy of update, so a rejection changes
// nothing and the caller keeps its own.
func (s *State) ApplyUpdate(update *Update) Apply {
	update = update.Clone()
	if !s.matches(update.CallID, update.Creator) {
		return IdentityMismatch
	}
	if !validSnapshot(update) {
		return InvalidSnapshot
	}
	if s.snapshot != nil && !s.snapshot.Group.IsEmpty() {
		switch {
		case update.Group.IsEmpty():
			update.Group = s.snapshot.Group
		case update.Group != s.snapshot.Group:
			return IdentityMismatch
		}
	}
	var held *Relay
	if s.snapshot != nil {
		held = s.snapshot.Relay
	}
	advances := advancesRelay(held, update.Relay)
	if s.snapshot != nil && update.TransactionID <= s.snapshot.TransactionID {
		// An older roster may bring the first or a newer allocation, but
		// cannot replace the roster or touch the participant controls.
		if advances {
			s.snapshot.Relay = update.Relay
			return Applied
		}
		return Stale
	}
	// A roster-only update leaves unchanged PIDs out: carry them over,
	// exact device first, the PN/LID alias as a fallback.
	if s.snapshot != nil {
		known := map[types.JID]uint32{}
		for _, p := range s.snapshot.Participants {
			for _, d := range p.Devices {
				if d.HasPID {
					known[d.JID] = d.PID
				}
			}
		}
		for _, p := range s.snapshot.Participants {
			for _, d := range p.Devices {
				if !d.HasPID {
					continue
				}
				if alias, ok := mappedDevice(p.JID, p.PN, d.JID); ok {
					if _, have := known[alias]; !have {
						known[alias] = d.PID
					}
				}
			}
		}
		for i := range update.Participants {
			p := &update.Participants[i]
			if !p.Connected() {
				continue
			}
			for j := range p.Devices {
				d := &p.Devices[j]
				if d.HasPID {
					continue
				}
				if pid, ok := known[d.JID]; ok {
					d.PID, d.HasPID = pid, true
				} else if alias, ok := mappedDevice(p.JID, p.PN, d.JID); ok {
					if pid, ok := known[alias]; ok {
						d.PID, d.HasPID = pid, true
					}
				}
			}
		}
	}
	if !advances {
		// Roster progress never revokes or rolls back a held allocation.
		update.Relay = held
	}
	if !validSnapshot(update) {
		return InvalidSnapshot
	}
	canonical := map[types.JID]types.JID{}
	for _, p := range update.Participants {
		if !p.Connected() {
			continue
		}
		jid := p.JID.ToNonAD()
		canonical[jid] = jid
		if !p.PN.IsEmpty() {
			canonical[p.PN.ToNonAD()] = jid
		}
	}
	hands := map[types.JID]bool{}
	for p := range s.hands {
		if c, ok := canonical[p]; ok {
			hands[c] = true
		}
	}
	s.hands = hands
	shares := map[types.JID]ScreenShare{}
	if update.Media != "audio" {
		for p, sh := range s.shares {
			if c, ok := canonical[p]; ok {
				shares[c] = sh
			}
		}
	}
	s.shares = shares
	s.snapshot = update
	return Applied
}

// ApplyWaitingRoom replaces the waiting room unless its transaction is
// stale.
func (s *State) ApplyWaitingRoom(room *WaitingRoom) Apply {
	if !s.matches(room.CallID, room.Creator) {
		return IdentityMismatch
	}
	if cur := s.waitingRoom; cur != nil && (cur.LinkToken != room.LinkToken || cur.Media != room.Media) {
		return IdentityMismatch
	}
	if s.hasRoomTx && room.HasTransactionID && room.TransactionID <= s.roomTx {
		return Stale
	}
	if room.HasTransactionID {
		if !s.hasRoomTx || room.TransactionID > s.roomTx {
			s.roomTx = room.TransactionID
		}
		s.hasRoomTx = true
	}
	s.waitingRoom = room
	return Applied
}

// SetWaitingRoomEnabled flips the held waiting room; false when none is held.
func (s *State) SetWaitingRoomEnabled(enabled bool) bool {
	if s.waitingRoom == nil {
		return false
	}
	s.waitingRoom.Enabled = enabled
	return true
}

// SetRaisedHand records a participant's hand.
func (s *State) SetRaisedHand(participant types.JID, raised bool) {
	p := participant.ToNonAD()
	if raised {
		s.hands[p] = true
	} else {
		delete(s.hands, p)
	}
}

// SetScreenShare records a participant's screen share.
func (s *State) SetScreenShare(participant types.JID, sh ScreenShare) {
	p := participant.ToNonAD()
	if sh.State == ScreenShareStopped {
		delete(s.shares, p)
	} else {
		s.shares[p] = sh
	}
}

// mappedDevice is device under the roster's explicit PN/LID alias of its
// owner, for PID continuity only: the stored JID is never rewritten, as
// media and Signal namespaces derive different keys.
func mappedDevice(owner, pn, device types.JID) (types.JID, bool) {
	if pn.IsEmpty() || owner.Server != types.HiddenUserServer || pn.Server != types.DefaultUserServer ||
		owner.Integrator != pn.Integrator {
		return types.JID{}, false
	}
	user := device.ToNonAD()
	var alternate types.JID
	switch {
	case user == owner.ToNonAD():
		alternate = pn
	case user == pn.ToNonAD():
		alternate = owner
	default:
		return types.JID{}, false
	}
	alias := device
	alias.User, alias.Server = alternate.User, alternate.Server
	return alias, true
}

func validSnapshot(u *Update) bool {
	connected, active := 0, 0
	for _, p := range u.Participants {
		if !p.Connected() {
			continue
		}
		connected++
		for _, d := range p.Devices {
			if d.HasPID {
				active++
			}
		}
	}
	if u.TransactionID == 0 || u.ConnectedLimit == 0 || u.ConnectedLimit > MaxParticipants ||
		len(u.Participants) > MaxParticipants || active > MaxParticipants || connected > int(u.ConnectedLimit) ||
		(u.Media != "audio" && u.Media != "video") {
		return false
	}
	// The relay's transaction numbers allocations, not rosters, so the two
	// are not compared.
	users := map[types.JID]bool{}
	pids := map[uint32]bool{}
	devices := map[types.JID]bool{}
	insert := func(m map[types.JID]bool, j types.JID) bool {
		if m[j] {
			return false
		}
		m[j] = true
		return true
	}
	for _, p := range u.Participants {
		if !insert(users, p.JID.ToNonAD()) {
			return false
		}
		if !p.PN.IsEmpty() && !insert(users, p.PN.ToNonAD()) {
			return false
		}
		for _, d := range p.Devices {
			du := d.JID.ToNonAD()
			if du != p.JID.ToNonAD() && (p.PN.IsEmpty() || du != p.PN.ToNonAD()) {
				return false
			}
			if !insert(devices, d.JID) {
				return false
			}
			if d.HasPID {
				if pids[d.PID] {
					return false
				}
				pids[d.PID] = true
			}
		}
	}
	return ValidateForMedia(u)
}

// ParticipantID is the device's E2E SRTP participant id, the HKDF input of
// its keys and every SSRC.
func ParticipantID(device types.JID) string { return kdf.FormatParticipantID(device.String()) }

// ValidateForMedia rejects a roster whose connected devices collide on a
// participant id or an SSRC: a duplicate SSRC would land one participant's
// media in another's decoder.
func ValidateForMedia(u *Update) bool {
	pids := map[uint32]bool{}
	ids := map[string]bool{}
	audio, video, appData, rtcp := map[uint32]bool{}, map[uint32]bool{}, map[uint32]bool{}, map[uint32]bool{}
	add := func(m map[uint32]bool, v uint32) bool {
		if m[v] {
			return false
		}
		m[v] = true
		return true
	}
	for _, p := range u.Participants {
		if p.State != "connected" {
			continue
		}
		for _, d := range p.Devices {
			if !d.HasPID {
				continue
			}
			id := ParticipantID(d.JID)
			if pids[d.PID] || ids[id] {
				return false
			}
			pids[d.PID], ids[id] = true, true
			if !add(audio, kdf.ParticipantSSRC(u.CallID, id, 0)) ||
				!add(video, kdf.ParticipantSSRC(u.CallID, id, kdf.VideoSSRCSlotWord)) ||
				!add(appData, kdf.ParticipantSSRC(u.CallID, id, kdf.AppDataSSRCSlotWord)) {
				return false
			}
			for slot := uint32(0); slot < relayStreamSlotCount; slot++ {
				if !add(rtcp, kdf.ParticipantSSRC(u.CallID, id, slot)) {
					return false
				}
			}
		}
	}
	return true
}

// DeviceIsLocal: device of participant is our local device, by the user
// (its LID or PN alias) and the device id together.
func DeviceIsLocal(p *Participant, d *Device, local types.JID) bool {
	ownsUser := p.JID.User == local.User || (!p.PN.IsEmpty() && p.PN.User == local.User)
	return ownsUser && d.JID.Device == local.Device &&
		(d.JID.User == p.JID.User || (!p.PN.IsEmpty() && d.JID.User == p.PN.User))
}

// LocalDevice is our connected device in u, nil when the roster lacks it.
func LocalDevice(u *Update, local types.JID) *Device {
	for i := range u.Participants {
		p := &u.Participants[i]
		if !p.Connected() {
			continue
		}
		for j := range p.Devices {
			if DeviceIsLocal(p, &p.Devices[j], local) {
				return &p.Devices[j]
			}
		}
	}
	return nil
}

// MediaEndpoint is the endpoint the group media dials: a usable one (not
// FNA, an IPv4 address, a port, a token we hold) on the web client's port,
// else the first usable one.
func (r *Relay) MediaEndpoint() (*RelayEndpoint, bool) {
	usable := func(e *RelayEndpoint) bool {
		return !e.FNA && e.IPv4 != "" && e.Port != 0 && int(e.TokenID) < len(r.Tokens) && len(r.Tokens[e.TokenID]) > 0
	}
	for i := range r.Endpoints {
		if e := &r.Endpoints[i]; usable(e) && e.Port == WebClientRelayPort {
			return e, true
		}
	}
	for i := range r.Endpoints {
		if e := &r.Endpoints[i]; usable(e) {
			return e, true
		}
	}
	return nil, false
}

// TagLen is the relay's WARP MI tag length, 4 when it says none.
func (r *Relay) TagLen() int {
	if r.HasWarpMITagLen {
		return int(r.WarpMITagLen)
	}
	return 4
}

// ValidateRelay: u's relay (if any) is usable for media — a tag length in
// 1..20, a key, and an endpoint with an IPv4 address and a token.
func ValidateRelay(u *Update) bool {
	r := u.Relay
	if r == nil {
		return true
	}
	if t := r.TagLen(); t < 1 || t > 20 || len(r.Key) == 0 {
		return false
	}
	e, ok := r.MediaEndpoint()
	if !ok {
		return false
	}
	a, err := netip.ParseAddr(e.IPv4)
	return err == nil && a.Is4()
}
