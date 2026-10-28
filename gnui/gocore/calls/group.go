// Ported from whatsapp-rust 5e72baf: the group-call signaling of
// src/voip/facade.rs (AcceptCall's group answer, OutgoingGroupCall,
// GroupBoundCall, CallLinkCall, fanout_group_epoch), src/client/voip.rs
// (join_call_link, the waiting-room heartbeat, normalize_call_link_token)
// and src/handlers/call.rs (apply_group_control, decrypt_group_epoch, the
// creator-only group terminate), MIT. What the window sees of a group call
// (the roster, who rang, whose pictures arrive, where it is filed) follows
// Zap 0.19.0's GroupRoster (src/calls.rs, src/backend/worker/calls.rs).

package calls

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow/types"

	"zapfast/abi"
	"zapfast/gocore/voip/groupcall"
)

// LinkChat is the chat a call joined through a link is filed under: it
// belongs to no chat, so only the Calls page shows it (0.19.0's
// CALL_LINK_CHAT).
const LinkChat = "zapfast-call-link"

const (
	// heartbeatInterval and maxHeartbeatFailures are whatsapp-rust's
	// WAITING_ROOM_HEARTBEAT_INTERVAL and _MAX_CONSECUTIVE_FAILURES.
	heartbeatInterval    = 10 * time.Second
	maxHeartbeatFailures = 3
	// maxPendingEpochs bounds the epochs kept for media still opening.
	maxPendingEpochs = 8
	// maxEarlyControls bounds a link's controls that overtake its join's
	// answer.
	maxEarlyControls = 16
)

// group is a group call's own state: the authoritative roster, who rang,
// whose pictures arrive, a link's waiting room, and the epochs waiting for
// the media.
type group struct {
	state  *groupcall.State
	caller types.JID // who rang us (non-AD); empty for our own call
	// joined: we are in the call (answered, placed, or admitted).
	joined bool

	link         bool
	token        string
	media        groupcall.CallLinkMedia
	waiting      bool // in the link's waiting room
	rekeyOnAdmit bool // the join's answer asked for an epoch
	beats        int  // consecutive heartbeat failures
	early        []Signal

	video  map[types.JID]bool // the roster JIDs (non-AD) whose pictures arrive
	names  map[types.JID]string
	ids    atomic.Pointer[map[string]string] // sender user@server → tile id
	epochs []Epoch                           // before the media opens
}

func newGroup(st *groupcall.State) *group {
	return &group{state: st, video: map[types.JID]bool{}, names: map[types.JID]string{}}
}

func userKey(j types.JID) string { return j.User + "@" + j.Server }

// memberID is the tile a media sender's pictures go on, from any goroutine.
func (g *group) memberID(sender types.JID) (string, bool) {
	m := g.ids.Load()
	if m == nil {
		return "", false
	}
	id, ok := (*m)[userKey(sender)]
	return id, ok
}

// member is the roster entry sender belongs to: the same user, whatever
// the device, by LID or phone number (GroupRoster::member).
func (g *group) member(sender types.JID) *groupcall.Participant {
	snap := g.state.Snapshot()
	if snap == nil {
		return nil
	}
	same := func(j types.JID) bool { return j.User == sender.User && j.Server == sender.Server }
	for i := range snap.Participants {
		p := &snap.Participants[i]
		if same(p.JID) || (!p.PN.IsEmpty() && same(p.PN)) {
			return p
		}
	}
	return nil
}

func sameUser(a, b types.JID) bool { return a.User == b.User && a.Server == b.Server }

// own reports whether j is this account, by its LID or phone number.
func (c *Calls) own(j types.JID) bool {
	if c.cli == nil {
		return false
	}
	pn, lid := c.cli.Own()
	return (!lid.IsEmpty() && sameUser(j, lid)) || (!pn.IsEmpty() && sameUser(j, pn))
}

// groupView is the roster as the window draws it: the others under the
// app's chat ids (the phone number when the roster has it), who rang, and
// whose pictures arrive (GroupRoster::view and the worker's canonical_call).
// It also publishes the media's sender → tile map.
func (c *Calls) groupView(k *call) abi.GroupCall {
	g := k.g
	v := abi.GroupCall{Link: g.link}
	if !g.caller.IsEmpty() {
		v.Caller, v.HasCaller = []byte(c.name(g, g.caller)), true
	}
	ids := map[string]string{}
	if snap := g.state.Snapshot(); snap != nil {
		for i := range snap.Participants {
			p := &snap.Participants[i]
			if c.own(p.JID) || (!p.PN.IsEmpty() && c.own(p.PN)) {
				continue
			}
			who := p.JID
			if !p.PN.IsEmpty() {
				who = p.PN
			}
			id := c.name(g, who)
			ids[userKey(p.JID)] = id
			if !p.PN.IsEmpty() {
				ids[userKey(p.PN)] = id
			}
			v.Participants = append(v.Participants, abi.Participant{ID: []byte(id), Joined: p.Connected(), Video: g.video[p.JID.ToNonAD()]})
		}
	}
	g.ids.Store(&ids)
	return v
}

// name is j's chat id, asked of the host once per person.
func (c *Calls) name(g *group, j types.JID) string {
	j = j.ToNonAD()
	if id, ok := g.names[j]; ok {
		return id
	}
	id := c.h.Chat(j)
	g.names[j] = id
	return id
}

func isGroupChat(chat string) (types.JID, bool) {
	j, err := types.ParseJID(chat)
	if err != nil || j.User == "" || j.Server != types.GroupServer {
		return types.EmptyJID, false
	}
	return j, true
}

// isCallService: sender is the call's own address, call-id@call.
func isCallService(sender types.JID, callID string) bool {
	return sender.Server == CallServer && sender.Device == 0 && sender.User == callID
}

// creatorSent: s is from the call's creator, about this call.
func creatorSent(k *call, s Signal) bool {
	return sameUser(s.Sender(), k.creator) && s.Creator == k.creator
}

// senderAuthorized: sender may hand us an epoch: the creator, or a
// connected participant (group_sender_authorized_if_current).
func senderAuthorized(k *call, sender types.JID) bool {
	if sameUser(sender, k.creator) {
		return true
	}
	snap := k.g.state.Snapshot()
	if snap == nil {
		return false
	}
	for i := range snap.Participants {
		p := &snap.Participants[i]
		if !p.Connected() {
			continue
		}
		if sender.Device == 0 {
			if sameUser(p.JID, sender) || (!p.PN.IsEmpty() && sameUser(p.PN, sender)) {
				return true
			}
			continue
		}
		for _, d := range p.Devices {
			if sameDevice(d.JID, sender) {
				return true
			}
		}
	}
	return false
}

// offerCaller is who rang with s: the member the call service names, else
// the device that sent it, else the creator (0.19.0's offer_caller).
func offerCaller(s Signal) types.JID {
	switch {
	case s.From.Server != CallServer:
		return s.From.ToNonAD()
	case !s.Participant.IsEmpty():
		return s.Participant.ToNonAD()
	}
	return s.Creator.ToNonAD()
}

// --- incoming --------------------------------------------------------------------

// groupOffered rings a group call's offer in its group, or, for a call
// among several people with no group behind it, in its creator's chat.
func (c *Calls) groupOffered(s Signal) {
	refuse := func(why string) {
		slog.Info("zapfast calls: refusing a group call's offer", "why", why)
		c.send(rejectWith(s, c.cli.NewID(), ""))
	}
	st := groupcall.NewState(s.CallID, s.Creator)
	snap, err := ParseGroupInviteSnapshot(s.Action)
	if err != nil {
		refuse(err.Error())
		return
	}
	if snap != nil && st.ApplyUpdate(snap) != groupcall.Applied {
		refuse("its roster is not valid")
		return
	}
	gid := s.Group
	if gid.IsEmpty() && snap != nil {
		gid = snap.Group
	}
	var chat string
	if !gid.IsEmpty() {
		chat = c.h.Chat(gid)
		if _, ok := isGroupChat(chat); !ok {
			refuse("it names no group")
			return
		}
	} else {
		chat = c.h.Chat(s.Creator.ToNonAD())
		if _, ok := callable(chat); !ok {
			refuse("its creator cannot be called")
			return
		}
	}
	video := s.Video()
	k := &call{
		u: abi.CallUpdate{Generation: NextGeneration(), Chat: []byte(chat), Direction: abi.CallDirectionIncoming,
			Video: video, Phase: abi.CallPhaseIncoming},
		id: s.CallID, creator: s.Creator, peer: gid, offer: &s, answering: s.From,
		began: s.Time.Unix(), file: true, carried: video, g: newGroup(st),
	}
	k.g.caller = offerCaller(s)
	if s.Time.IsZero() {
		k.began = time.Now().Unix()
	}
	c.call = k
	k.ringTimer = c.after(c.timing.Ring, k.u.Generation, func(k *call) {
		if k.u.Phase == abi.CallPhaseIncoming {
			c.end(k, abi.CallPhaseEnded, abi.CallStatusMissed)
		}
	})
	c.refreshDevices()
	c.emit(k)
}

// answerGroup takes a group call rung in: the preaccept and accept go to
// the call's own address, and the media opens on the roster's relay, now
// or when a group_update brings it (AcceptCall::start's group path).
func (c *Calls) answerGroup(k *call) {
	k.stopTimers()
	video := k.u.Video
	if snap := k.g.state.Snapshot(); snap != nil && snap.Media != "" && (snap.Media == "video") != video {
		c.h.Error("The group call could not be answered: its offer and its roster disagree on video")
		c.send(BuildReject(k.id, k.answering, k.creator, c.cli.NewID()))
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	}
	// The group media sends MLow (NewGroupConfig), so that is what we
	// announce.
	k.mlow = true
	to := CallJID(k.id)
	c.send(BuildPreacceptWithCapability(k.id, to, k.creator, c.cli.NewID(), AudioRates16k,
		answerPreacceptCapability(video, false), video))
	p := AcceptParams{CallID: k.id, To: to, ID: c.cli.NewID(), Creator: k.creator, AudioRates: AudioRates16k, Video: video}
	if video {
		k.offer.Metadata(&p)
	} else {
		p.Capability = CapabilityOffer
	}
	c.send(BuildAccept(p))
	k.answering, k.answered, k.g.joined = to, true, true
	k.u.Phase = abi.CallPhaseAccepted
	k.u.CameraOn = video
	c.groupMedia(k)
	if live(k.u.Phase) {
		c.emit(k)
	}
}

// groupMedia opens the media once we are in the call and the roster has a
// relay; until the relay comes, a timer gives up after the ack's patience
// (OFFER_ACK_RELAY_TIMEOUT).
func (c *Calls) groupMedia(k *call) {
	g := k.g
	if k.open || !g.joined || g.waiting {
		return
	}
	snap := g.state.Snapshot()
	if snap == nil || snap.Relay == nil {
		if k.relayTimer == nil {
			k.relayTimer = c.after(c.timing.Ack, k.u.Generation, func(k *call) {
				if !k.open {
					c.h.Error("The group call could not be connected: WhatsApp gave it no relay")
					c.terminate(k, CallJID(k.id), "")
					c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
				}
			})
		}
		return
	}
	if k.relayTimer != nil {
		k.relayTimer.Stop()
		k.relayTimer = nil
	}
	own := c.ownLID()
	c.openMedia(k, nil, nil, k.creator, &GroupOpen{Creator: k.creator, Self: own, Initial: snap.Clone(), MemberID: g.memberID})
	if !k.open {
		return
	}
	for i := range g.epochs {
		c.command(k, groupCommand(nil, &g.epochs[i]))
	}
	g.epochs = nil
}

// --- the group's signaling -------------------------------------------------------

// groupSignal applies a stanza of a group call up.
func (c *Calls) groupSignal(k *call, s Signal) {
	switch s.Tag {
	case TagGroupUpdate:
		c.groupUpdate(k, s)
	case TagEncRekey:
		c.encRekey(k, s)
	case TagWaitingRoomUpdate:
		if !creatorSent(k, s) && !isCallService(s.Sender(), k.id) {
			slog.Warn("zapfast calls: a waiting-room snapshot from someone other than the creator")
			return
		}
		if room, err := ParseWaitingRoomUpdate(s.Action); err == nil {
			k.g.state.ApplyWaitingRoom(&room)
		}
	case "terminate":
		// Only the creator, or the call service, ends a group call; a
		// member who leaves leaves the roster.
		if !creatorSent(k, s) && !isCallService(s.Sender(), k.id) {
			slog.Warn("zapfast calls: a group terminate from someone other than the creator")
			return
		}
		c.terminated(k, s.Reason())
	case "video":
		c.memberVideoState(k, s)
	}
	// preaccept, accept and reject speak for one member alone, as do
	// user_action and screen_share: the roster says who is on the call.
}

// groupUpdate applies an authoritative roster from the creator or the
// call service: the media follows it, a link's waiting room admits us, an
// outgoing call is answered once someone joins, and a requested epoch is
// made and shared.
func (c *Calls) groupUpdate(k *call, s Signal) {
	g := k.g
	if !creatorSent(k, s) && !isCallService(s.Sender(), k.id) {
		slog.Warn("zapfast calls: a group snapshot from someone other than the creator")
		return
	}
	u, err := ParseGroupUpdate(s.Action)
	if err != nil {
		slog.Warn("zapfast calls: an unreadable group snapshot", "err", err)
		return
	}
	var held uint32
	if prev := g.state.Snapshot(); prev != nil {
		held = prev.TransactionID
	}
	switch a := g.state.ApplyUpdate(u); a {
	case groupcall.Applied:
	case groupcall.Stale, groupcall.UnknownCall:
		return
	default:
		slog.Warn("zapfast calls: a group snapshot was refused", "why", a)
		return
	}
	snap := g.state.Snapshot()
	// The committed snapshot, which keeps the relay and pids a roster-only
	// update leaves out.
	c.command(k, groupCommand(snap.Clone(), nil))
	// A member who left sends no picture; one who comes back says so with
	// a new first frame.
	for j := range g.video {
		if p := g.member(j); p == nil || !p.Connected() {
			delete(g.video, j)
		}
	}
	own := c.ownLID()
	switch {
	case g.waiting && groupcall.LocalDevice(snap, own) != nil:
		c.admit(k)
	case u.RekeyRequested && g.joined && u.TransactionID > held:
		// A late relay on an older roster is no new epoch: only a fresh
		// roster fans one out.
		c.fanout(k, snap)
	}
	if k.outgoing && !g.link && !k.answered && c.someoneElseJoined(snap) {
		k.answered = true
		if k.ringTimer != nil {
			k.ringTimer.Stop()
			k.ringTimer = nil
		}
		k.u.Phase = abi.CallPhaseConnecting
		c.active(k)
	}
	c.groupMedia(k)
	if live(k.u.Phase) {
		c.emit(k)
	}
}

func (c *Calls) someoneElseJoined(snap *groupcall.Update) bool {
	for i := range snap.Participants {
		p := &snap.Participants[i]
		if p.Connected() && !c.own(p.JID) && (p.PN.IsEmpty() || !c.own(p.PN)) {
			return true
		}
	}
	return false
}

// encRekey decrypts an epoch a member (or the creator) sent us and hands it
// to the media; it must be exactly 32 bytes.
func (c *Calls) encRekey(k *call, s Signal) {
	sender := s.Sender()
	r, err := ParseGroupEncRekey(s.Action)
	if err != nil {
		slog.Warn("zapfast calls: an unreadable group epoch", "err", err)
		return
	}
	if r.CallID != k.id || r.Creator != k.creator || !senderAuthorized(k, sender) {
		slog.Warn("zapfast calls: a group epoch from someone not on the call")
		return
	}
	enc, _ := s.Action.GetOptionalChildByTag("enc")
	cli, at := c.cli, s.Time
	c.h.Go("calls-epoch", func(ctx context.Context) {
		key, err := cli.DecryptKey(ctx, &enc, sender, at)
		if err == nil && len(key) != 32 {
			err = errors.New("call key must contain exactly 32 bytes")
		}
		c.h.Post(func() {
			if c.call != k || !live(k.u.Phase) {
				return
			}
			if err != nil {
				slog.Warn("zapfast calls: a group epoch could not be decrypted", "err", err)
				return
			}
			if !senderAuthorized(k, sender) {
				return
			}
			c.installEpoch(k, Epoch{Transaction: r.TransactionID, Key: key})
		})
	})
}

func (c *Calls) installEpoch(k *call, e Epoch) {
	if k.open {
		c.command(k, groupCommand(nil, &e))
		return
	}
	g := k.g
	if len(g.epochs) == maxPendingEpochs {
		g.epochs = g.epochs[1:]
	}
	g.epochs = append(g.epochs, e)
}

// fanout makes a new epoch for snap's transaction, sends it to every
// connected device of the roster with a pid but ours, and installs it
// here once all of them have it (fanout_group_epoch). A fan-out that fails
// ends the call, as whatsapp-rust's GroupRekeyTeardown does.
func (c *Calls) fanout(k *call, snap *groupcall.Update) {
	pn, lid := c.cli.Own()
	seen := map[types.JID]bool{}
	var to []types.JID
	for i := range snap.Participants {
		p := &snap.Participants[i]
		if !p.Connected() {
			continue
		}
		for _, d := range p.Devices {
			if !d.HasPID || sameDevice(d.JID, lid) || (!pn.IsEmpty() && sameDevice(d.JID, pn)) || seen[d.JID] {
				continue
			}
			seen[d.JID] = true
			to = append(to, d.JID)
		}
	}
	epoch := make([]byte, 32)
	_, _ = rand.Read(epoch)
	cli, callID, creator, tx := c.cli, k.id, k.creator, snap.TransactionID
	c.h.Go("calls-rekey", func(ctx context.Context) {
		err := sendEpoch(ctx, cli, callID, creator, tx, to, epoch)
		c.h.Post(func() {
			if c.call != k || !live(k.u.Phase) {
				return
			}
			if err != nil {
				slog.Error("zapfast calls: the group call's key could not be shared", "err", err)
				c.h.Error("The group call ended: its key could not be shared: " + err.Error())
				c.terminate(k, CallJID(k.id), "")
				c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
				return
			}
			c.installEpoch(k, Epoch{Transaction: tx, Key: epoch})
		})
	})
}

// sendEpoch encrypts epoch for each device and sends each its
// <enc_rekey>; an error when one device could not have it.
func sendEpoch(ctx context.Context, cli Client, callID string, creator types.JID, tx uint32, to []types.JID, epoch []byte) error {
	if len(to) == 0 {
		return nil
	}
	keys, identity, err := cli.EncryptKey(ctx, to, epoch, cli.NewID())
	if err != nil {
		return err
	}
	for _, key := range keys {
		n, err := BuildGroupEncRekey(GroupEncRekeyParams{CallID: callID, ID: cli.NewID(), To: key.Device, Creator: creator,
			TransactionID: tx, Key: key, DeviceIdentity: identity})
		if err != nil {
			return err
		}
		if err := cli.Send(ctx, n); err != nil {
			return err
		}
	}
	if len(keys) != len(to) {
		return errors.New("the epoch could not be encrypted for every member's device")
	}
	return nil
}

// memberVideoState notes a member's <video> stopping their picture.
func (c *Calls) memberVideoState(k *call, s Signal) {
	state, ok := s.VideoState()
	if !ok || !(state == VideoStopped || state == VideoPaused || state.Inactive()) {
		return
	}
	p := k.g.member(s.Sender())
	if p == nil || !k.g.video[p.JID.ToNonAD()] {
		return
	}
	delete(k.g.video, p.JID.ToNonAD())
	c.emit(k)
}

// memberVideo notes a member's first picture.
func (c *Calls) memberVideo(k *call, sender types.JID) {
	if k.g == nil {
		return
	}
	p := k.g.member(sender)
	if p == nil || k.g.video[p.JID.ToNonAD()] {
		return
	}
	k.g.video[p.JID.ToNonAD()] = true
	c.emit(k)
}

// --- outgoing --------------------------------------------------------------------

// errGroupRefused is WhatsApp's 427 to a group offer: the server does not
// let this client start group calls.
var errGroupRefused = errors.New("WhatsApp refused to start the group call (427): it does not let this app start group calls")

// startGroup places a call to a group's members (GroupBoundCall).
func (c *Calls) startGroup(k *call, cli Client, gid types.JID) {
	k.g = newGroup(groupcall.NewState(k.id, k.creator))
	k.u.HasGroup = true
	c.call = k
	c.refreshDevices()
	c.h.Go("calls-group-offer", func(ctx context.Context) {
		snap, err := c.offerGroup(ctx, cli, k, gid)
		c.h.Post(func() { c.placedGroup(k, snap, err) })
	})
}

// offerGroup resolves the group's members to their LIDs and devices, and
// sends one call-scoped offer with us first (OutgoingGroupCall::start). It
// reads nothing of k but what start set.
func (c *Calls) offerGroup(ctx context.Context, cli Client, k *call, gid types.JID) (*groupcall.Update, error) {
	members, err := cli.GroupMembers(ctx, gid)
	if err != nil {
		return nil, err
	}
	pn, lid := cli.Own()
	own := func(j types.JID) bool { return sameUser(j, lid) || (!pn.IsEmpty() && sameUser(j, pn)) }
	seen := map[types.JID]bool{}
	var targets []types.JID
	for _, m := range members {
		m = m.ToNonAD()
		if own(m) {
			continue
		}
		l, err := cli.PeerLID(ctx, m)
		if err != nil {
			return nil, err
		}
		if own(l) || seen[l] {
			continue
		}
		seen[l] = true
		targets = append(targets, l)
	}
	switch {
	case len(targets) < 2:
		return nil, errors.New("a group call needs at least two other people in the group")
	case len(targets) > groupcall.MaxRemoteParticipants:
		return nil, errors.New("a group call takes at most 32 people")
	}
	devices, err := cli.Devices(ctx, targets...)
	if err != nil {
		return nil, err
	}
	video := k.u.Video
	parts := []groupcall.Participant{{JID: lid.ToNonAD(), Devices: []groupcall.Device{{JID: lid,
		CapabilityVersion: 1, HasCapabilityVersion: true, Capability: offerCapability(video, false)}}}}
	for _, t := range targets {
		p := groupcall.Participant{JID: t}
		for _, d := range devices {
			if sameUser(d, t) {
				p.Devices = append(p.Devices, groupcall.Device{JID: d})
			}
		}
		if len(p.Devices) == 0 {
			return nil, errors.New("WhatsApp lists no device for someone in the group")
		}
		parts = append(parts, p)
	}
	offer, err := BuildInitialGroupOffer(InitialGroupOfferParams{CallID: k.id, ID: cli.NewID(), Creator: k.creator,
		Group: gid, Participants: parts, AudioRate: 16000, Video: video})
	if err != nil {
		return nil, err
	}
	if k.cancel.cancelled.Load() {
		return nil, errCancelled
	}
	ack, err := cli.SendAwait(ctx, offer, c.timing.Ack)
	if err != nil {
		return nil, err
	}
	if code, ok := ack.Attrs["error"]; ok {
		if attrString(code) == "427" {
			return nil, errGroupRefused
		}
		return nil, errors.New("WhatsApp refused the group call (" + attrString(code) + ")")
	}
	snap, err := ParseInitialGroupCallAck(ack)
	switch {
	case err != nil:
		return nil, err
	case snap == nil:
		return nil, errors.New("WhatsApp's answer to the group call has no roster")
	case snap.CallID != k.id || snap.Creator != k.creator:
		return nil, errors.New("WhatsApp answered the group call for another call")
	case (snap.Media == "video") != video:
		return nil, errors.New("WhatsApp changed the group call's media")
	}
	return snap, nil
}

// placedGroup is the group offer's answer, on the engine's goroutine.
func (c *Calls) placedGroup(k *call, snap *groupcall.Update, err error) {
	if c.call != k {
		return
	}
	k.placing = false
	switch {
	case errors.Is(err, errCancelled):
		k.file = false
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	case err != nil:
		slog.Error("zapfast calls: the group call could not be started", "err", err)
		c.h.Error("The group call could not be started: " + err.Error())
		k.file = false
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	}
	k.answering = CallJID(k.id)
	if a := k.g.state.ApplyUpdate(snap); a != groupcall.Applied && a != groupcall.Stale {
		c.h.Error("The group call could not be started: WhatsApp's roster for it is not valid")
		c.terminate(k, k.answering, "")
		k.file = false
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	}
	if k.cancel.cancelled.Load() {
		c.hangup()
		return
	}
	k.g.joined = true
	k.u.Phase = abi.CallPhaseRinging
	cur := k.g.state.Snapshot()
	if snap.RekeyRequested {
		c.fanout(k, cur)
	}
	if c.someoneElseJoined(cur) {
		k.answered = true
		k.u.Phase = abi.CallPhaseConnecting
	}
	c.groupMedia(k)
	if !live(k.u.Phase) {
		return
	}
	if !k.answered {
		k.ringTimer = c.after(c.timing.Ring, k.u.Generation, func(k *call) {
			if !k.answered {
				c.hangup()
			}
		})
	}
	c.emit(k)
}

// --- call links ------------------------------------------------------------------

// normalizeCallLinkToken is a link's token, from the token or the link
// itself (normalize_call_link_token).
func normalizeCallLinkToken(v string, media groupcall.CallLinkMedia) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", errors.New("call-link token is required")
	}
	const prefix = "https://call.whatsapp.com/"
	if path, ok := strings.CutPrefix(v, prefix); ok {
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[1] == "" || parts[0] != string(media) {
			return "", errors.New("invalid call-link URL or media mode")
		}
		return parts[1], nil
	}
	if strings.Contains(v, "://") || strings.Contains(v, "/") {
		return "", errors.New("invalid call-link token")
	}
	return v, nil
}

// joinLink joins a call link the window shows as placing under
// generation; the call service may hold us in its waiting room.
func (c *Calls) joinLink(token string, video bool, generation uint64, cancel *cancelFlag) {
	placing := abi.CallUpdate{Generation: generation, Direction: abi.CallDirectionOutgoing, Video: video,
		Phase: abi.CallPhasePlacing, HasGroup: true, Group: abi.GroupCall{Link: true}}
	refuse := func(text string) {
		if text != "" {
			c.h.Error(text)
		}
		c.flags.release(cancel)
		c.emitUpdate(notPlaced(placing))
	}
	if c.call != nil {
		slog.Warn("zapfast calls: refusing a call link: a call is already up")
		refuse("")
		return
	}
	cli := c.cli
	if cli == nil {
		refuse("Zap is not connected to WhatsApp")
		return
	}
	if _, lid := cli.Own(); lid.IsEmpty() {
		refuse("Zap is not connected to WhatsApp")
		return
	}
	media := groupcall.LinkAudio
	if video {
		media = groupcall.LinkVideo
	}
	tok, err := normalizeCallLinkToken(token, media)
	if err != nil {
		refuse("The call link could not be joined: " + err.Error())
		return
	}
	if cancel == nil {
		cancel = &cancelFlag{}
	}
	placing.CameraOn = video
	placing.Phase = abi.CallPhaseConnecting
	k := &call{u: placing, outgoing: true, cancel: cancel, began: time.Now().Unix(), file: true, mlow: true,
		placing: true, carried: video, g: newGroup(groupcall.NewState("", types.EmptyJID))}
	k.g.link, k.g.token, k.g.media = true, tok, media
	c.call = k
	c.refreshDevices()
	c.emit(k)
	c.h.Go("calls-link-join", func(ctx context.Context) {
		n, err := BuildCallLinkJoinWithCapability(tok, media, cli.NewID(), offerCapability(video, false))
		var join groupcall.CallLinkJoin
		if err == nil {
			ack, aerr := cli.SendAwait(ctx, n, c.timing.Ack)
			if err = aerr; err == nil {
				join, err = ParseCallLinkJoinAck(ack, tok)
			}
		}
		if err == nil && join.Media != media {
			err = errors.New("the call service changed the link's media")
		}
		if err == nil && join.CallID == "" {
			err = errors.New("the call service named no call")
		}
		c.h.Post(func() { c.linkJoined(k, join, err) })
	})
}

// linkJoined applies the join's answer: admitted, or in the waiting room.
func (c *Calls) linkJoined(k *call, join groupcall.CallLinkJoin, err error) {
	if c.call != k {
		return
	}
	k.placing = false
	if err != nil {
		slog.Error("zapfast calls: the call link could not be joined", "err", err)
		c.h.Error("The call link could not be joined: " + err.Error())
		k.file = false
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	}
	g := k.g
	k.id, k.creator = join.CallID, join.Creator
	k.answering, k.answered = CallJID(k.id), true
	g.state = groupcall.NewState(k.id, k.creator)
	if join.Group != nil && g.state.ApplyUpdate(join.Group) != groupcall.Applied {
		c.h.Error("The call link could not be joined: its roster is not valid")
		k.file = false
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	}
	if join.WaitingRoom != nil {
		g.state.ApplyWaitingRoom(join.WaitingRoom)
	}
	g.rekeyOnAdmit = join.Group != nil && join.Group.RekeyRequested
	g.waiting = join.InWaitingRoom
	if k.cancel.cancelled.Load() {
		// Hung up while joining: one admitted is left with a terminate, a
		// waiting one locally.
		c.hangup()
		return
	}
	if g.waiting {
		c.heartbeat(k)
	} else {
		c.admit(k)
	}
	early := g.early
	g.early = nil
	for _, s := range early {
		if !live(k.u.Phase) || c.call != k {
			return
		}
		if s.CallID == k.id {
			c.signal(k, s)
		}
	}
	if live(k.u.Phase) {
		c.emit(k)
	}
}

// admit: the call service let us in. A requested epoch is shared, and the
// media opens on the relay.
func (c *Calls) admit(k *call) {
	g := k.g
	g.waiting, g.joined = false, true
	if k.beatTimer != nil {
		k.beatTimer.Stop()
		k.beatTimer = nil
	}
	k.u.Phase = abi.CallPhaseAccepted
	snap := g.state.Snapshot()
	if snap == nil {
		c.h.Error("The call link could not be joined: the call service sent no roster")
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	}
	if snap.Media != string(g.media) {
		c.h.Error("The call link could not be joined: the call service changed its media")
		c.terminate(k, k.answering, "")
		c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
		return
	}
	if g.rekeyOnAdmit || snap.RekeyRequested {
		c.fanout(k, snap)
	}
	c.groupMedia(k)
}

// heartbeat keeps a waiting-room admission alive, every heartbeatInterval;
// three failures in a row give it up.
func (c *Calls) heartbeat(k *call) {
	if !k.g.waiting {
		return
	}
	n, err := BuildWaitingRoomHeartbeat(k.id, k.creator, c.cli.NewID())
	if err == nil {
		err = c.cli.Send(c.h.Context, n)
	}
	if err != nil {
		k.g.beats++
		slog.Warn("zapfast calls: a waiting-room heartbeat failed", "failures", k.g.beats, "err", err)
		if k.g.beats >= maxHeartbeatFailures {
			c.h.Error("The call link's waiting room could not be kept")
			c.end(k, abi.CallPhaseFailed, abi.CallStatusFailed)
			return
		}
	} else {
		k.g.beats = 0
	}
	every := c.timing.Heartbeat
	if every <= 0 {
		every = heartbeatInterval
	}
	k.beatTimer = c.after(every, k.u.Generation, c.heartbeat)
}

// earlyControl keeps a control that overtook a link join's answer, for
// the call it turns out to be.
func (c *Calls) earlyControl(k *call, s Signal) bool {
	if k.g == nil || !k.g.link || k.id != "" {
		return false
	}
	switch s.Tag {
	case TagGroupUpdate, TagEncRekey, TagWaitingRoomUpdate, "terminate":
	default:
		return false
	}
	if len(k.g.early) < maxEarlyControls {
		k.g.early = append(k.g.early, s)
	}
	return true
}

// groupCamera turns our camera on or off in a group call's media: a group
// call takes video only when it began as a video call, and there is no
// upgrade to ask anyone for. The call itself is told.
func (c *Calls) groupCamera(k *call, on bool) {
	if !k.u.Video {
		return
	}
	cam := c.cameraID(k)
	if on {
		if k.plane {
			c.command(k, cameraOnCommand(cam))
		} else {
			c.command(k, videoStartCommand(cam, false))
			k.plane = true
		}
		c.videoState(k, VideoEnabled, videoDecH264, true)
	} else {
		c.videoState(k, VideoStopped, "", false)
		c.command(k, cameraOffCommand())
	}
	k.u.CameraOn = on
	c.emit(k)
}
