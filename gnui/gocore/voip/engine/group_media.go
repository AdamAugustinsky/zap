// Ported from whatsapp-rust 5e72baf wacore/src/voip/group_media.rs, MIT.

package engine

import (
	"crypto/subtle"
	"errors"
	"maps"
	"slices"

	"go.mau.fi/whatsmeow/types"

	"zapfast/gocore/voip/groupcall"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/rtcp"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/srtp"
)

// The group media registry routes each packet by its deterministic SSRC
// before authentication, then authenticates it with the shared keygen-v2
// epoch derived for that sender's device id: keys are never tried across
// participants.

const (
	maxBufferedEpochs    = 8
	relayStreamSlotCount = 9
	// appDataTimestampStride is the RTC app-data stream's RTP timestamp step
	// (wacore app_data.rs).
	appDataTimestampStride = 960
)

// GroupRosterApply is what applying a roster to the media did.
type GroupRosterApply int

const (
	RosterApplied GroupRosterApply = iota
	RosterStale
)

// GroupEpochApply is what applying an epoch did.
type GroupEpochApply int

const (
	EpochInstalled GroupEpochApply = iota
	EpochBuffered
	EpochStale
	EpochDuplicate
)

func (a GroupEpochApply) String() string {
	return [...]string{"installed", "buffered", "stale", "duplicate"}[a]
}

// Group media errors.
var (
	ErrGroupIdentityMismatch   = errors.New("group media call identity mismatch")
	ErrGroupInvalidSnapshot    = errors.New("group media snapshot is invalid")
	ErrGroupInvalidEpoch       = errors.New("group media epoch must contain exactly 32 bytes")
	ErrGroupConflictingEpoch   = errors.New("group media epoch conflicts with an existing transaction")
	ErrLocalParticipantRemoved = errors.New("the local participant is no longer connected to the group call")
	ErrGroupPipeline           = errors.New("group media pipeline could not be constructed")
)

// groupAudioReject is why an inbound group audio packet produced nothing:
// unroutable (no roster SSRC: never reached SRTP, the ordinary tail of a
// removed participant) or unprotect (routed, and the tag failed).
type groupAudioReject int

const (
	rejectNone groupAudioReject = iota
	rejectUnroutable
	rejectUnprotect
)

// ParticipantMedia is one authenticated packet of one participant.
type ParticipantMedia struct {
	ParticipantID string
	User, Device  types.JID
	PID           uint32
	HasPID        bool
	Header        rtp.Header
	Payload       []byte
}

// participantVideo is one authenticated video packet's completed access
// units.
type participantVideo struct {
	ParticipantMedia
	units []completedAU
}

type participantReceiver struct {
	participantID string
	user, device  types.JID
	pid           uint32
	hasPID        bool
	audioSSRC     uint32
	videoSSRC     uint32
	appDataSSRC   uint32
	videoEnabled  bool
	audio         *mediaPipeline
	appData       *mediaPipeline
	video         *videoPipeline
}

func (r *participantReceiver) media(h rtp.Header, payload []byte) ParticipantMedia {
	return ParticipantMedia{ParticipantID: r.participantID, User: r.user, Device: r.device, PID: r.pid, HasPID: r.hasPID,
		Header: h, Payload: payload}
}

type groupMediaStream int

const (
	streamNone groupMediaStream = iota
	streamAudio
	streamVideo
)

// GroupMediaRegistry is one call's participant receivers and its
// transaction-ordered shared epochs.
type GroupMediaRegistry struct {
	callID           string
	creator          types.JID
	selfID           string
	selfDevice       types.JID
	samplesPerPacket uint32
	tagLen           int
	videoStride      uint32
	rosterTx         uint32
	hasRoster        bool
	receivers        map[string]*participantReceiver
	audioRoutes      map[uint32]string
	videoRoutes      map[uint32]string
	appDataRoutes    map[uint32]string
	rtcpRoutes       map[uint32]string
	epochTx          uint32
	epoch            []byte // nil: none installed
	pending          map[uint32][]byte
}

// NewGroupMediaRegistry is an empty registry for one call.
func NewGroupMediaRegistry(callID string, creator, self types.JID, samplesPerPacket uint32, tagLen int, videoStride uint32) (*GroupMediaRegistry, error) {
	if samplesPerPacket == 0 || videoStride == 0 || tagLen < 1 || tagLen > 20 {
		return nil, ErrGroupPipeline
	}
	return &GroupMediaRegistry{
		callID: callID, creator: creator, selfID: kdf.FormatParticipantID(self.String()), selfDevice: self,
		samplesPerPacket: samplesPerPacket, tagLen: tagLen, videoStride: videoStride,
		receivers: map[string]*participantReceiver{}, pending: map[uint32][]byte{},
	}, nil
}

// seedDirectPeer keeps a promoted 1:1 call's authenticated peer until a
// PID-bearing roster is ready.
func (g *GroupMediaRegistry) seedDirectPeer(callKey []byte, user, device types.JID, video bool) error {
	r, err := g.buildReceiver(callKey, user, device, 0, false, video)
	if err != nil {
		return err
	}
	g.receivers[r.participantID] = r
	return g.rebuildRoutes()
}

// InstalledEpochTransaction is the installed epoch's transaction.
func (g *GroupMediaRegistry) InstalledEpochTransaction() (uint32, bool) {
	return g.epochTx, g.epoch != nil
}

// ActiveParticipantIDs are the participants that have keys, sorted.
func (g *GroupMediaRegistry) ActiveParticipantIDs() []string {
	var ids []string
	for _, r := range g.receivers {
		if r.audio != nil {
			ids = append(ids, r.participantID)
		}
	}
	slices.Sort(ids)
	return ids
}

func (g *GroupMediaRegistry) activePIDs() []uint32 {
	var pids []uint32
	for _, r := range g.receivers {
		if r.hasPID {
			pids = append(pids, r.pid)
		}
	}
	slices.Sort(pids)
	return slices.Compact(pids)
}

func (g *GroupMediaRegistry) activeDeviceIDs() []string {
	return slices.Sorted(maps.Keys(g.receivers))
}

func pidMigrated(existing uint32, hasExisting bool, incoming uint32, hasIncoming bool) bool {
	return hasExisting && (!hasIncoming || existing != incoming)
}

func (g *GroupMediaRegistry) participantsWithPIDChanges(u *groupcall.Update) []string {
	var out []string
	for _, a := range activeDevices(u, g.selfDevice) {
		id := groupcall.ParticipantID(a.device.JID)
		if r, ok := g.receivers[id]; ok && pidMigrated(r.pid, r.hasPID, a.device.PID, a.device.HasPID) {
			out = append(out, id)
		}
	}
	return out
}

func (g *GroupMediaRegistry) senderReportStream(participantID string, sender uint32) groupMediaStream {
	r, ok := g.receivers[participantID]
	switch {
	case !ok:
		return streamNone
	case r.audio != nil && r.audioSSRC == sender:
		return streamAudio
	case r.videoEnabled && r.video != nil && r.videoSSRC == sender:
		return streamVideo
	}
	return streamNone
}

// ApplyGroupUpdate replaces the connected, PID-bearing roster. A device's
// receiver is reused so its ROC and reassembly survive an update.
func (g *GroupMediaRegistry) ApplyGroupUpdate(u *groupcall.Update) (GroupRosterApply, error) {
	if err := g.validateUpdate(u); err != nil {
		return 0, err
	}
	if g.hasRoster && u.TransactionID <= g.rosterTx {
		return RosterStale, nil
	}
	active := activeDevices(u, g.selfDevice)
	video := u.Media == "video"
	if len(active) == 0 {
		// Even the first roster supersedes the direct-call fallback.
		g.receivers = map[string]*participantReceiver{}
		if err := g.rebuildRoutes(); err != nil {
			return 0, err
		}
		g.rosterTx, g.hasRoster = u.TransactionID, true
		return RosterApplied, g.installBestPendingEpoch()
	}
	// Derive every route before moving a receiver or advancing the roster,
	// so a corrected resend stays admissible.
	previous := g.receivers
	next := make(map[string]*participantReceiver, len(active))
	for _, a := range active {
		id := groupcall.ParticipantID(a.device.JID)
		var r *participantReceiver
		var err error
		existing, had := previous[id]
		switch {
		case had && pidMigrated(existing.pid, existing.hasPID, a.device.PID, a.device.HasPID):
			// A PID is one relay media session: its ROC, replay and
			// reassembly must not carry into the next one.
			r, err = g.receiverForEpoch(a.participant.JID, a.device, video)
		case had && video && existing.video == nil && g.epoch != nil:
			r, err = g.buildReceiver(g.epoch, a.participant.JID, a.device.JID, a.device.PID, a.device.HasPID, true)
			if err == nil {
				r.audio, r.appData = existing.audio, existing.appData
			}
		case had:
			r = existing
		default:
			r, err = g.receiverForEpoch(a.participant.JID, a.device, video)
		}
		if err != nil {
			return 0, err
		}
		r.user, r.device, r.pid, r.hasPID = a.participant.JID, a.device.JID, a.device.PID, a.device.HasPID
		if !video && r.videoEnabled && r.video != nil {
			// Keep the SRTP replay state for a later reactivation, but no
			// pre-downgrade access unit may surface under the new roster.
			r.video.resetDepacketizer()
		}
		r.videoEnabled = video
		next[id] = r
	}
	g.receivers = next
	g.rosterTx, g.hasRoster = u.TransactionID, true
	if err := g.installBestPendingEpoch(); err != nil {
		return 0, err
	}
	return RosterApplied, g.rebuildRoutes()
}

func (g *GroupMediaRegistry) receiverForEpoch(user types.JID, d *groupcall.Device, video bool) (*participantReceiver, error) {
	if g.epoch != nil {
		return g.buildReceiver(g.epoch, user, d.JID, d.PID, d.HasPID, video)
	}
	return g.receiverWithoutKeys(user, d.JID, d.PID, d.HasPID), nil
}

// ApplyRawEpoch installs an epoch once its roster is known, and buffers a
// bounded few for rosters still to come.
func (g *GroupMediaRegistry) ApplyRawEpoch(tx uint32, epoch []byte) (GroupEpochApply, error) {
	if tx == 0 || len(epoch) != 32 {
		return 0, ErrGroupInvalidEpoch
	}
	if g.epoch != nil {
		if tx < g.epochTx {
			return EpochStale, nil
		}
		if tx == g.epochTx {
			if subtle.ConstantTimeCompare(g.epoch, epoch) == 1 {
				return EpochDuplicate, nil
			}
			return 0, ErrGroupConflictingEpoch
		}
	}
	if p, ok := g.pending[tx]; ok {
		if subtle.ConstantTimeCompare(p, epoch) == 1 {
			return EpochDuplicate, nil
		}
		return 0, ErrGroupConflictingEpoch
	}
	if !g.hasRoster || tx > g.rosterTx {
		g.pending[tx] = slices.Clone(epoch)
		if len(g.pending) > maxBufferedEpochs {
			farthest := slices.Max(slices.Collect(maps.Keys(g.pending)))
			clear(g.pending[farthest])
			delete(g.pending, farthest)
		}
		return EpochBuffered, nil
	}
	if err := g.installEpoch(tx, epoch); err != nil {
		return 0, err
	}
	return EpochInstalled, nil
}

func (g *GroupMediaRegistry) unprotectAudio(p []byte) (ParticipantMedia, groupAudioReject) {
	h, ok := rtp.ParseHeader(p)
	if !ok {
		return ParticipantMedia{}, rejectUnroutable
	}
	r := g.receivers[g.audioRoutes[h.SSRC]]
	if _, routed := g.audioRoutes[h.SSRC]; !routed || r == nil || r.audio == nil {
		return ParticipantMedia{}, rejectUnroutable
	}
	hdr, payload, err := r.audio.unprotectAudio(p)
	if err != nil {
		return ParticipantMedia{}, rejectUnprotect
	}
	return r.media(hdr, payload), rejectNone
}

func (g *GroupMediaRegistry) unprotectVideo(p []byte) (participantVideo, bool) {
	h, ok := rtp.ParseHeader(p)
	if !ok {
		return participantVideo{}, false
	}
	id, routed := g.videoRoutes[h.SSRC]
	r := g.receivers[id]
	if !routed || r == nil || !r.videoEnabled || r.video == nil {
		return participantVideo{}, false
	}
	hdr, units, _, err := r.video.unprotectSplit(p)
	if err != nil {
		return participantVideo{}, false
	}
	return participantVideo{ParticipantMedia: r.media(hdr, nil), units: units}, true
}

func (g *GroupMediaRegistry) unprotectAppData(p []byte) (ParticipantMedia, bool) {
	h, ok := rtp.ParseHeader(p)
	if !ok {
		return ParticipantMedia{}, false
	}
	id, routed := g.appDataRoutes[h.SSRC]
	r := g.receivers[id]
	if !routed || r == nil || r.appData == nil {
		return ParticipantMedia{}, false
	}
	hdr, payload, err := r.appData.unprotectAudio(p)
	if err != nil || hdr.PayloadType != rtp.PayloadTypeAppData {
		return ParticipantMedia{}, false
	}
	return r.media(hdr, payload), true
}

func (g *GroupMediaRegistry) unprotectRTCP(p []byte) (ParticipantMedia, bool) {
	sender, ok := rtcp.SenderSSRC(p)
	if !ok {
		return ParticipantMedia{}, false
	}
	id, routed := g.rtcpRoutes[sender]
	r := g.receivers[id]
	if !routed || r == nil || r.audio == nil {
		return ParticipantMedia{}, false
	}
	plain, ok := r.audio.unprotectRTCP(p)
	if !ok {
		return ParticipantMedia{}, false
	}
	return r.media(rtp.Header{SSRC: sender}, plain), true
}

func (g *GroupMediaRegistry) installBestPendingEpoch() error {
	if !g.hasRoster {
		return nil
	}
	best, found := uint32(0), false
	for tx := range g.pending {
		if tx <= g.rosterTx && (!found || tx > best) {
			best, found = tx, true
		}
	}
	if !found {
		return nil
	}
	epoch := g.pending[best]
	delete(g.pending, best)
	if err := g.installEpoch(best, epoch); err != nil {
		return err
	}
	for tx, e := range g.pending {
		if tx <= best {
			clear(e)
			delete(g.pending, tx)
		}
	}
	return nil
}

func (g *GroupMediaRegistry) installEpoch(tx uint32, epoch []byte) error {
	if len(epoch) != 32 {
		return ErrGroupInvalidEpoch
	}
	epoch = slices.Clone(epoch)
	// Every fallible derivation completes before a working receiver changes.
	for _, r := range g.receivers {
		if _, ok := srtp.DeriveE2EKeysFromRaw(epoch, r.participantID); !ok {
			return ErrGroupInvalidEpoch
		}
		if _, ok := srtp.DeriveSRTCPKeysFromRaw(epoch, r.participantID); !ok {
			return ErrGroupInvalidEpoch
		}
	}
	for id, r := range g.receivers {
		if r.audio == nil {
			rebuilt, err := g.buildReceiver(epoch, r.user, r.device, r.pid, r.hasPID, r.videoEnabled)
			if err != nil {
				return err
			}
			g.receivers[id] = rebuilt
			continue
		}
		if !r.audio.rekeyRecvPreservingROC(epoch, r.participantID) ||
			r.appData == nil || !r.appData.rekeyRecvPreservingROC(epoch, r.participantID) {
			return ErrGroupPipeline
		}
		if r.video != nil && !r.video.rekeyRecvPreservingROC(epoch, r.participantID) {
			return ErrGroupPipeline
		}
	}
	clear(g.epoch)
	g.epochTx, g.epoch = tx, epoch
	return g.rebuildRoutes()
}

func (g *GroupMediaRegistry) buildReceiver(key []byte, user, device types.JID, pid uint32, hasPID, video bool) (*participantReceiver, error) {
	r := g.receiverWithoutKeys(user, device, pid, hasPID)
	var cname [rtcp.WhatsAppCNameLen]byte
	audio, ok := newMediaPipeline(key, g.selfID, r.participantID, r.audioSSRC, g.samplesPerPacket, g.tagLen, cname)
	if !ok {
		return nil, ErrGroupPipeline
	}
	appData, ok := newMediaPipeline(key, g.selfID, r.participantID, r.appDataSSRC, appDataTimestampStride, g.tagLen, cname)
	if !ok || !appData.rtp.SetPayloadType(rtp.PayloadTypeAppData) {
		return nil, ErrGroupPipeline
	}
	appData.rtp.SetMLowProfile(false)
	r.audio, r.appData = audio, appData
	if video {
		v, ok := newVideoPipeline(key, g.selfID, r.participantID, r.videoSSRC, g.videoStride, g.tagLen, cname)
		if !ok {
			return nil, ErrGroupPipeline
		}
		r.video, r.videoEnabled = v, true
	}
	return r, nil
}

func (g *GroupMediaRegistry) receiverWithoutKeys(user, device types.JID, pid uint32, hasPID bool) *participantReceiver {
	id := groupcall.ParticipantID(device)
	return &participantReceiver{
		participantID: id, user: user, device: device, pid: pid, hasPID: hasPID,
		audioSSRC:   kdf.ParticipantSSRC(g.callID, id, 0),
		videoSSRC:   kdf.VideoParticipantSSRC(g.callID, id),
		appDataSSRC: kdf.ParticipantSSRC(g.callID, id, kdf.AppDataSSRCSlotWord),
	}
}

func (g *GroupMediaRegistry) rebuildRoutes() error {
	audio, video, appData, rtcpR := map[uint32]string{}, map[uint32]string{}, map[uint32]string{}, map[uint32]string{}
	put := func(m map[uint32]string, ssrc uint32, id string) bool {
		if _, dup := m[ssrc]; dup {
			return false
		}
		m[ssrc] = id
		return true
	}
	for _, r := range g.receivers {
		if r.audio != nil && !put(audio, r.audioSSRC, r.participantID) {
			return ErrGroupInvalidSnapshot
		}
		if r.videoEnabled && r.video != nil && !put(video, r.videoSSRC, r.participantID) {
			return ErrGroupInvalidSnapshot
		}
		if r.appData != nil && !put(appData, r.appDataSSRC, r.participantID) {
			return ErrGroupInvalidSnapshot
		}
		if r.audio != nil {
			for slot := uint32(0); slot < relayStreamSlotCount; slot++ {
				if !put(rtcpR, kdf.ParticipantSSRC(g.callID, r.participantID, slot), r.participantID) {
					return ErrGroupInvalidSnapshot
				}
			}
		}
	}
	g.audioRoutes, g.videoRoutes, g.appDataRoutes, g.rtcpRoutes = audio, video, appData, rtcpR
	return nil
}

func (g *GroupMediaRegistry) validateUpdate(u *groupcall.Update) error {
	if u.CallID != g.callID || u.Creator != g.creator {
		return ErrGroupIdentityMismatch
	}
	if u.TransactionID == 0 || len(u.Participants) > groupcall.MaxParticipants || (u.Media != "audio" && u.Media != "video") {
		return ErrGroupInvalidSnapshot
	}
	if !groupcall.ValidateForMedia(u) {
		return ErrGroupInvalidSnapshot
	}
	return nil
}

type activeDevice struct {
	participant *groupcall.Participant
	device      *groupcall.Device
}

// activeDevices are u's connected, PID-bearing devices other than ours.
func activeDevices(u *groupcall.Update, local types.JID) []activeDevice {
	var out []activeDevice
	for i := range u.Participants {
		p := &u.Participants[i]
		if !p.Connected() {
			continue
		}
		for j := range p.Devices {
			d := &p.Devices[j]
			if d.HasPID && !groupcall.DeviceIsLocal(p, d, local) {
				out = append(out, activeDevice{p, d})
			}
		}
	}
	return out
}
