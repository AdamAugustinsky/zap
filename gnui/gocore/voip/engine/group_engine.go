// Ported from whatsapp-rust 5e72baf wacore/src/voip/engine.rs (the group
// path: CallConfig::for_group, configure_group, apply_group_update,
// apply_group_raw_epoch, commit_group_allocate, sync_group_epoch,
// on_group_rtp and the group RTCP and sender reports), MIT. RTC app-data
// reactions are received and dropped: Zap has no reaction UI.

package engine

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"

	"go.mau.fi/whatsmeow/types"

	"zapfast/gocore/voip/groupcall"
	"zapfast/gocore/voip/h264"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/mlow"
	"zapfast/gocore/voip/rtcp"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/stun"
)

// NewGroupConfig is a group call's engine config, before its shared
// keygen-v2 epoch arrives: the callKey is a zeroed bootstrap that media
// gating keeps off the wire until ApplyGroupEpoch installs a real one.
func NewGroupConfig(direction Direction, callID, selfLID, creator string, r *groupcall.Relay) (Config, error) {
	ep, ok := r.MediaEndpoint()
	if !ok {
		return Config{}, &SetupError{"the group relay has no usable endpoint"}
	}
	if len(r.Key) == 0 {
		return Config{}, &SetupError{"the group relay has no <key> (STUN integrity key)"}
	}
	tag := r.TagLen()
	if tag < 1 || tag > 20 {
		return Config{}, &SetupError{fmt.Sprintf("the relay advertised an unsupported WARP MI tag length: %d", tag)}
	}
	var auth []byte
	if int(ep.AuthTokenID) < len(r.AuthTokens) {
		auth = r.AuthTokens[ep.AuthTokenID]
	}
	return Config{
		CallID: callID, Direction: direction, SelfLID: selfLID, PeerLID: creator,
		CallKey: make([]byte, 32),
		SSRC:    kdf.ParticipantSSRC(callID, kdf.FormatParticipantID(selfLID), 0),
		Audio:   MLow16k60ms, RelayToken: r.Tokens[ep.TokenID], AuthToken: auth,
		RelayIP: ep.IPv4, RelayPort: ep.Port, IntegrityKey: append([]byte(nil), r.Key...), WarpMITagLen: tag,
	}, nil
}

// GroupConfig layers participant-indexed media onto an engine.
type GroupConfig struct {
	Creator types.JID
	// Self is our device JID, as the roster names it.
	Self    types.JID
	Initial *groupcall.Update
}

type directPeer struct {
	user, device types.JID
	callKey      []byte
}

type groupState struct {
	registry    *GroupMediaRegistry
	localDevice types.JID
	// hasRelay: an allocation is held; relayHasTx: it named a transaction.
	hasRelay, relayHasTx bool
	relayTx              uint32
	localEpochTx         uint32
	hasLocalEpoch        bool
	requiredEpochTx      uint32
	hasRequired          bool
	directFallback       bool
	streamSSRCs          [9]uint32
	appDataSSRC          uint32
	hbhFECSSRCs          [2]uint32
	appData              *mediaPipeline
	mixer                *ParticipantAudioMixer
	videoOrientations    map[types.JID]uint8
	audioReception       map[string]*rtcp.ReceptionStats
	videoReception       map[string]*rtcp.ReceptionStats
	decoders             map[string]*mlow.Decoder
	foreignDecoders      map[string]ForeignCodec
	probes               map[string]*codecProbe
	foreignParticipants  map[string]bool
}

func (g *groupState) forget(id string) {
	g.mixer.Reset(id)
	delete(g.decoders, id)
	delete(g.foreignDecoders, id)
	delete(g.probes, id)
	delete(g.foreignParticipants, id)
	delete(g.audioReception, id)
	delete(g.videoReception, id)
}

func (g *groupState) retain(active []string) {
	keep := func(id string) bool { return slices.Contains(active, id) }
	g.mixer.Retain(active)
	for _, m := range []map[string]*rtcp.ReceptionStats{g.audioReception, g.videoReception} {
		for id := range m {
			if !keep(id) {
				delete(m, id)
			}
		}
	}
	for id := range g.decoders {
		if !keep(id) {
			delete(g.decoders, id)
		}
	}
	for id := range g.foreignDecoders {
		if !keep(id) {
			delete(g.foreignDecoders, id)
		}
	}
	for id := range g.probes {
		if !keep(id) {
			delete(g.probes, id)
		}
	}
	for id := range g.foreignParticipants {
		if !keep(id) {
			delete(g.foreignParticipants, id)
		}
	}
}

func reception(m map[string]*rtcp.ReceptionStats, id string) *rtcp.ReceptionStats {
	r := m[id]
	if r == nil {
		r = &rtcp.ReceptionStats{}
		m[id] = r
	}
	return r
}

// WithForeignCodecFactory supplies one standard-Opus decoder per group
// participant: the codec carries inter-frame state, so one instance cannot
// serve several speakers.
func (e *Engine) WithForeignCodecFactory(f func() ForeignCodec) *Engine {
	e.foreignFactory = f
	return e
}

// IsGroup reports participant-indexed group media.
func (e *Engine) IsGroup() bool { return e.group != nil }

// GroupEpochTransaction is the epoch our send side uses.
func (e *Engine) GroupEpochTransaction() (uint32, bool) {
	if e.group == nil {
		return 0, false
	}
	return e.group.localEpochTx, e.group.hasLocalEpoch
}

// GroupParticipants are the participant ids with keys, sorted.
func (e *Engine) GroupParticipants() []string {
	if e.group == nil {
		return nil
	}
	return e.group.registry.ActiveParticipantIDs()
}

// ConfigureGroup enables participant-indexed media; before Start.
func (e *Engine) ConfigureGroup(cfg GroupConfig) error {
	if e.started {
		return ErrGroupPipeline
	}
	return e.configureGroupAt(0, cfg, nil)
}

func (e *Engine) configureGroupAt(now Millis, cfg GroupConfig, direct *directPeer) error {
	local := groupcall.LocalDevice(cfg.Initial, cfg.Self)
	if local == nil {
		return ErrLocalParticipantRemoved
	}
	localSender := local.JID.String()
	// Relay material is validated before any group state exists, so a
	// failed promotion stays retryable with the same roster transaction.
	refresh, err := prepareGroupRelayRefresh(cfg.Initial)
	if err != nil {
		return err
	}
	m := e.media
	registry, err := NewGroupMediaRegistry(e.callID, cfg.Creator, cfg.Self, m.audio.TimestampStep, m.tagLen, rtp.VideoTSStride15FPS)
	if err != nil {
		return err
	}
	if direct != nil {
		if err := registry.seedDirectPeer(direct.callKey, direct.user, direct.device, m.video != nil); err != nil {
			return err
		}
	}
	if _, err := registry.ApplyGroupUpdate(cfg.Initial); err != nil {
		return err
	}
	live := e.started || e.allocated || e.allocatePending
	if live {
		localSender = m.selfLID
	}
	localID := kdf.FormatParticipantID(localSender)
	derived := kdf.RelayStreamSSRCs(e.callID, localID)
	if live {
		// A live promotion adds the group planes but cannot rotate an
		// allocated sender identity under in-flight media.
		if e.selfPID != localID || m.pipe.sendSSRC() != derived[0] || (m.video != nil && m.video.pipe.sendSSRC() != derived[3]) {
			return ErrGroupPipeline
		}
	}
	appDataSSRC := kdf.ParticipantSSRC(e.callID, localID, kdf.AppDataSSRCSlotWord)
	streams, err := e.prepareGroupStreamSSRCs(localID, appDataSSRC)
	if err != nil {
		return err
	}
	appData, ok := newMediaPipeline(m.callKey, localSender, m.recvPeerLID, appDataSSRC, appDataTimestampStride, m.tagLen,
		rtcp.WhatsAppCName(e.tx.NextTxID()))
	if !ok || !appData.rtp.SetPayloadType(rtp.PayloadTypeAppData) {
		return ErrGroupPipeline
	}
	appData.rtp.SetMLowProfile(false)
	e.selfPID = localID
	m.selfLID = localSender
	m.pipe.setSendSSRC(streams[0])
	if m.video != nil {
		m.video.pipe.setSendSSRC(streams[3])
	}
	g := &groupState{
		registry: registry, localDevice: cfg.Self, directFallback: direct != nil,
		streamSSRCs: streams, appDataSSRC: appDataSSRC, appData: appData,
		hbhFECSSRCs: [2]uint32{
			kdf.ParticipantSSRC(e.callID, localID, kdf.HBHFECTxSSRCSlotWord),
			kdf.ParticipantSSRC(e.callID, localID, kdf.HBHFECRxSSRCSlotWord),
		},
		mixer: NewParticipantAudioMixer(), videoOrientations: map[types.JID]uint8{},
		audioReception: map[string]*rtcp.ReceptionStats{}, videoReception: map[string]*rtcp.ReceptionStats{},
		decoders: map[string]*mlow.Decoder{}, foreignDecoders: map[string]ForeignCodec{},
		probes: map[string]*codecProbe{}, foreignParticipants: map[string]bool{},
	}
	if r := cfg.Initial.Relay; r != nil {
		g.hasRelay, g.relayHasTx, g.relayTx = true, r.HasTransactionID, r.TransactionID
	}
	if cfg.Initial.RekeyRequested {
		g.requiredEpochTx, g.hasRequired = cfg.Initial.TransactionID, true
	}
	g.mixer.Retain(registry.ActiveParticipantIDs())
	e.group = g
	e.commitGroupAllocate(now, refresh, true)
	return e.syncGroupEpoch()
}

// ApplyGroupUpdate applies an authoritative roster. On a 1:1 call it
// promotes the call in place, keeping the peer's receiver until the
// roster's own keys arrive. ErrLocalParticipantRemoved leaves the engine
// terminated: the roster no longer has us.
func (e *Engine) ApplyGroupUpdate(now Millis, u *groupcall.Update) (GroupRosterApply, error) {
	m := e.media
	if e.group == nil {
		self, err1 := types.ParseJID(m.selfLID)
		peer, err2 := types.ParseJID(m.recvPeerLID)
		if err1 != nil || err2 != nil {
			return 0, ErrGroupPipeline
		}
		if err := e.configureGroupAt(now, GroupConfig{Creator: u.Creator, Self: self, Initial: u},
			&directPeer{user: peer.ToNonAD(), device: peer, callKey: slices.Clone(m.callKey)}); err != nil {
			return 0, err
		}
		return RosterApplied, nil
	}
	g := e.group
	advances := u.Relay != nil && (!g.hasRelay || (u.Relay.HasTransactionID && (!g.relayHasTx || u.Relay.TransactionID > g.relayTx)))
	// Fallible relay material is validated before the roster commits, so a
	// malformed relay cannot make a corrected resend look stale.
	refresh, err := prepareGroupRelayRefresh(u)
	if err != nil {
		return 0, err
	}
	if !advances {
		refresh = nil
	}
	if refresh != nil && refresh.tagLen != m.tagLen {
		// Every pipeline was built for the established tag length.
		return 0, ErrGroupPipeline
	}
	prevPIDs := g.registry.activePIDs()
	prevDevices := g.registry.activeDeviceIDs()
	changed := g.registry.participantsWithPIDChanges(u)
	result, err := g.registry.ApplyGroupUpdate(u)
	if err != nil {
		return 0, err
	}
	if result == RosterApplied {
		if groupcall.LocalDevice(u, g.localDevice) == nil {
			// A roster without us is authoritative teardown: inert first, so
			// nothing captures or protects media after the error.
			e.terminated = true
			return 0, ErrLocalParticipantRemoved
		}
		if u.RekeyRequested && (!g.hasRequired || u.TransactionID > g.requiredEpochTx) {
			g.requiredEpochTx, g.hasRequired = u.TransactionID, true
		}
		// A new PID is a new media session: every piece of per-participant
		// decode and reception state belongs to the retired one.
		for _, id := range changed {
			g.forget(id)
		}
		active := g.registry.ActiveParticipantIDs()
		g.retain(active)
		for jid := range g.videoOrientations {
			if !rosterNames(u, jid) {
				delete(g.videoOrientations, jid)
			}
		}
		subsChanged := !slices.Equal(g.registry.activePIDs(), prevPIDs)
		added := false
		if u.Media == "video" {
			for _, d := range g.registry.activeDeviceIDs() {
				added = added || !slices.Contains(prevDevices, d)
			}
		}
		if u.Media == "audio" {
			e.DisableVideo()
			e.purgeQueuedVideoOutputs()
		} else if added {
			// A new receiver has no reference frame: start it on an IDR.
			e.RequireVideoKeyframe()
		}
		e.commitGroupAllocate(now, refresh, subsChanged)
		if err := e.syncGroupEpoch(); err != nil {
			return 0, err
		}
	} else if advances {
		// A relay refresh on an already-processed roster: reallocate only.
		e.commitGroupAllocate(now, refresh, false)
	}
	if advances {
		g.hasRelay, g.relayHasTx, g.relayTx = true, u.Relay.HasTransactionID, u.Relay.TransactionID
	}
	return result, nil
}

func rosterNames(u *groupcall.Update, jid types.JID) bool {
	for _, p := range u.Participants {
		if !p.Connected() {
			continue
		}
		if p.JID == jid {
			return true
		}
		for _, d := range p.Devices {
			if d.JID == jid {
				return true
			}
		}
	}
	return false
}

// ApplyGroupEpoch installs (or buffers) one decrypted keygen-v2 epoch.
func (e *Engine) ApplyGroupEpoch(tx uint32, epoch []byte) (GroupEpochApply, error) {
	if e.group == nil {
		return 0, ErrGroupPipeline
	}
	r, err := e.group.registry.ApplyRawEpoch(tx, epoch)
	if err != nil {
		return 0, err
	}
	return r, e.syncGroupEpoch()
}

// RelayReconnected starts the allocate's response budget once the
// replacement relay transport is up.
func (e *Engine) RelayReconnected(now Millis) {
	if e.started && e.allocatePending && e.allocateDeadline == Never {
		e.allocateDeadline = now + allocateTimeoutMS
	}
}

// SetParticipantVideoOrientation is one participant's (or device's)
// rotation from its <video> stanza.
func (e *Engine) SetParticipantVideoOrientation(jid types.JID, o uint8) {
	if e.group != nil {
		e.group.videoOrientations[jid] = o & 3
	}
}

// prepareGroupStreamSSRCs is the relay stream grid: derived, with the
// hop-by-hop FEC and app-data slots (6..8) replaced by random values that
// collide with nothing.
func (e *Engine) prepareGroupStreamSSRCs(localID string, appDataSSRC uint32) ([9]uint32, error) {
	s := kdf.RelayStreamSSRCs(e.callID, localID)
	used := map[uint32]bool{appDataSSRC: true}
	for _, v := range s[:6] {
		used[v] = true
	}
	for i := 6; i < 9; i++ {
		picked := false
		for range 64 {
			r := e.tx.NextTxID()
			c := uint32(r[4])<<24 | uint32(r[5])<<16 | uint32(r[6])<<8 | uint32(r[7])
			if c != 0 && !used[c] {
				used[c], s[i], picked = true, c, true
				break
			}
		}
		if !picked {
			return s, ErrGroupPipeline
		}
	}
	return s, nil
}

type groupRelayRefresh struct {
	addr         netip.AddrPort
	token        []byte
	endpointXOR  [6]byte
	integrityKey []byte
	tagLen       int
}

func prepareGroupRelayRefresh(u *groupcall.Update) (*groupRelayRefresh, error) {
	r := u.Relay
	if r == nil {
		return nil, nil
	}
	tag := r.TagLen()
	if tag < 1 || tag > 20 {
		return nil, ErrGroupPipeline
	}
	if len(r.Key) == 0 {
		return nil, ErrGroupInvalidSnapshot
	}
	ep, ok := r.MediaEndpoint()
	if !ok {
		return nil, ErrGroupInvalidSnapshot
	}
	xor, ok := stun.XORRelayEndpoint(ep.IPv4, ep.Port)
	ip, err := netip.ParseAddr(ep.IPv4)
	if !ok || err != nil || !ip.Is4() {
		return nil, ErrGroupInvalidSnapshot
	}
	return &groupRelayRefresh{
		addr: netip.AddrPortFrom(ip, ep.Port), token: slices.Clone(r.Tokens[ep.TokenID]),
		endpointXOR: xor, integrityKey: slices.Clone(r.Key), tagLen: tag,
	}, nil
}

// commitGroupAllocate re-sends the group allocate when the relay material
// or the subscriptions changed. A changed address asks the driver to redial
// (OutReconnectRelay); the allocate's deadline waits for RelayReconnected.
func (e *Engine) commitGroupAllocate(now Millis, refresh *groupRelayRefresh, subsChanged bool) {
	changed := refresh != nil && (refresh.addr != e.relayAddr || !bytes.Equal(refresh.token, e.relayToken) ||
		refresh.endpointXOR != e.endpointXOR || !bytes.Equal(refresh.integrityKey, e.integrityKey))
	if !changed && !subsChanged {
		return
	}
	reconnect := false
	if refresh != nil {
		if refresh.addr != e.relayAddr {
			e.relayAddr, reconnect = refresh.addr, true
		}
		e.relayToken, e.endpointXOR, e.integrityKey = refresh.token, refresh.endpointXOR, refresh.integrityKey
	}
	if changed {
		// New credentials invalidate the allocation even on the same address.
		e.allocated = false
	}
	e.allocatePending = true
	switch {
	case reconnect:
		e.allocateDeadline = Never
	case e.started:
		e.allocateDeadline = now + allocateTimeoutMS
	}
	g := e.group
	e.allocTx = e.tx.NextTxID()
	e.allocate = stun.BuildWASMGroupAllocate(&stun.WASMGroupAllocate{
		TransactionID: &e.allocTx, RelayToken: e.relayToken, EndpointXOR: &e.endpointXOR, IntegrityKey: e.integrityKey,
		StreamSSRCs: &g.streamSSRCs, AppDataSSRC: g.appDataSSRC, HBHFECSSRCs: &g.hbhFECSSRCs,
		ParticipantPIDs: g.registry.activePIDs(),
	})
	if e.started {
		if reconnect {
			e.push(Output{Kind: OutReconnectRelay, Addr: e.relayAddr})
		}
		e.transmit(e.allocate)
	}
}

func (e *Engine) purgeQueuedVideoOutputs() {
	e.outbox = slices.DeleteFunc(e.outbox, func(o Output) bool {
		if o.Kind != OutTransmit {
			return false
		}
		h, ok := rtp.ParseHeader(o.Packet)
		return ok && rtp.IsVersion2(o.Packet) && h.PayloadType == rtp.PayloadTypeH264
	})
}

// syncGroupEpoch moves the send side onto a newly installed epoch: every
// derivation first, then audio, video and app-data together, preserving
// their counters.
func (e *Engine) syncGroupEpoch() error {
	g := e.group
	tx, ok := g.registry.InstalledEpochTransaction()
	if !ok || (g.hasLocalEpoch && tx <= g.localEpochTx) {
		return nil
	}
	epoch := g.registry.epoch
	m := e.media
	audio, ok1 := prepareSendRekey(epoch, m.selfLID)
	appData, ok2 := prepareSendRekey(epoch, m.selfLID)
	if !ok1 || !ok2 {
		return ErrGroupInvalidEpoch
	}
	m.pipe.commitSendRekey(audio)
	if v := m.video; v != nil {
		v.pipe.commitSendRekey(audio)
		// The epoch may be a newly admitted member's first decryptable
		// media: never start it on a dependent frame.
		v.keyframeRequired = true
	}
	g.appData.commitSendRekey(appData)
	clear(m.callKey)
	m.callKey = slices.Clone(epoch)
	g.localEpochTx, g.hasLocalEpoch, g.directFallback = tx, true, false
	g.mixer.Retain(g.registry.ActiveParticipantIDs())
	e.announceVideoKeyframe()
	if e.allocated {
		e.announceAudioRTCPSession()
	}
	return nil
}

// groupEpochReady: media may flow. With a rekey requested, only an epoch
// at least that new; otherwise any installed epoch, or the promoted 1:1
// call's own key.
func (e *Engine) groupEpochReady() bool {
	g := e.group
	if g == nil {
		return true
	}
	if g.hasRequired {
		return g.hasLocalEpoch && g.localEpochTx >= g.requiredEpochTx
	}
	return g.hasLocalEpoch || g.directFallback
}

func (e *Engine) emitGroupSenderReports(now Millis, wall uint64) {
	g, m := e.group, e.media
	var audio, video []rtcp.ReceptionReport
	for _, r := range g.audioReception {
		if rep, ok := r.Report(now); ok {
			audio = append(audio, rep)
		}
	}
	for _, r := range g.videoReception {
		if rep, ok := r.Report(now); ok {
			video = append(video, rep)
		}
	}
	if len(audio) == 0 {
		e.transmit(m.pipe.audioSenderReport(wall, nil))
	}
	for i := range audio {
		e.transmit(m.pipe.audioSenderReport(wall, &audio[i]))
	}
	if v := m.video; v != nil && v.active && !v.sendGated {
		if len(video) == 0 {
			e.transmit(v.pipe.senderReport(wall, nil))
		}
		for i := range video {
			e.transmit(v.pipe.senderReport(wall, &video[i]))
		}
	}
}

func (e *Engine) onGroupRTCP(now Millis, p []byte) {
	if !e.groupEpochReady() {
		// A requested epoch retires the old SRTCP keys with SRTP; an old-key
		// index would also advance the replay window past the new epoch's.
		return
	}
	g, m := e.group, e.media
	audioSSRC := m.pipe.sendSSRC()
	pm, ok := g.registry.unprotectRTCP(p)
	if !ok {
		return
	}
	sum, ok := rtcp.Summarize(pm.Payload)
	if !ok {
		return
	}
	var videoSSRC uint32
	if v := m.video; v != nil {
		videoSSRC = v.pipe.sendSSRC()
		if requestsKeyframe(sum.Feedback, videoSSRC) {
			v.keyframeRequired = true
			e.announceVideoKeyframe()
		}
	}
	if sender, sec, frac, ok := rtcp.SenderReportTiming(pm.Payload); ok {
		switch g.registry.senderReportStream(pm.ParticipantID, sender) {
		case streamAudio:
			reception(g.audioReception, pm.ParticipantID).ObserveSenderReport(sender, sec, frac, now)
		case streamVideo:
			reception(g.videoReception, pm.ParticipantID).ObserveSenderReport(sender, sec, frac, now)
		}
	}
	e.event(Event{
		Kind: EventRTCPReceived, ReportsAudio: slices.Contains(sum.ReferencedSSRCs, audioSSRC),
		ReportsVideo: m.video != nil && slices.Contains(sum.ReferencedSSRCs, videoSSRC), RTCP: sum,
	})
}

func (e *Engine) onGroupRTP(now Millis, p []byte, h rtp.Header) {
	if !e.groupEpochReady() {
		// Until the requested epoch installs, a removed member could forge
		// media with the still-resident receivers.
		return
	}
	g, m := e.group, e.media
	switch h.PayloadType {
	case rtp.PayloadTypeH264:
		v, ok := g.registry.unprotectVideo(p)
		if !ok {
			return
		}
		e.stats.VideoRTPReceived++
		reception(g.videoReception, v.ParticipantID).Observe(v.Header.SSRC, v.Header.SequenceNumber, v.Header.Timestamp, now, rtp.VideoClockRate)
		o, ok := g.videoOrientations[v.Device]
		if !ok {
			o = g.videoOrientations[v.User]
		}
		for _, u := range v.units {
			fo := o
			if u.orientation >= 0 {
				fo = uint8(u.orientation)
			}
			e.push(Output{Kind: OutVideo, Video: VideoFrame{
				Data: u.au, Keyframe: h264.AUIsKeyframe(u.au), Orientation: fo, Timestamp: u.timestamp,
				Sender: v.User, Device: v.Device, PID: v.PID, HasPID: v.HasPID,
			}})
		}
		return
	case rtp.PayloadTypeAppData:
		// Reactions authenticate but have no consumer in Zap.
		g.registry.unprotectAppData(p)
		return
	}
	format := m.audio
	if !format.AcceptsPayloadType(h.PayloadType) {
		e.stats.RTPPayloadTypeUnexpected++
		e.stats.LastUnexpectedPayloadType = h.PayloadType
		return
	}
	pm, reject := g.registry.unprotectAudio(p)
	switch reject {
	case rejectUnprotect:
		e.stats.SRTPUnprotectFailed++
		return
	case rejectUnroutable:
		// The ordinary tail of a participant a roster just removed: no key
		// was tried, so it is not an authentication failure.
		return
	}
	id := pm.ParticipantID
	e.stats.RTPReceived++
	rec := reception(g.audioReception, id)
	if s, ok := rec.SSRC(); ok && s != pm.Header.SSRC {
		if probe := g.probes[id]; probe != nil {
			probe.streamRestarted()
		}
	}
	rec.Observe(pm.Header.SSRC, pm.Header.SequenceNumber, pm.Header.Timestamp, now, format.ClockRate)
	// The latched promotion first: native CELT's TOC looks like an escape.
	promoted := g.foreignParticipants[id]
	codec := Opus
	if !promoted {
		codec = format.inboundCodec(pm.Header.PayloadType, pm.Payload)
	}
	escaped := !promoted && format.payloadIsMLowEscape(pm.Payload)
	if codec == Opus && !escaped && !g.foreignParticipants[id] {
		// Proven native: latch now, and drop what the MLow decoder made of
		// this participant's earlier packets.
		g.foreignParticipants[id] = true
		delete(g.decoders, id)
		g.mixer.Reset(id)
	}
	if codec == Opus {
		dec := g.foreignDecoders[id]
		if dec == nil && e.foreignFactory != nil {
			if dec = e.foreignFactory(); dec != nil {
				g.foreignDecoders[id] = dec
			}
		}
		if dec == nil {
			e.stats.AudioFramesWithoutDecoder++
			e.event(Event{Kind: EventForeignAudio, Payload: pm.Payload})
			return
		}
		encoded := pm.Payload
		if escaped {
			encoded = slices.Clone(encoded)
			if err := depacketizeOpusFromMLow(encoded); err != nil {
				slog.Debug("zapfast voip: a malformed MLow Opus escape from a participant", "err", err)
				e.stats.AudioFramesConcealed++
				return
			}
		}
		pcm, err := dec.Decode(nil, encoded)
		if err == nil {
			e.stats.ForeignFramesDecoded++
		} else {
			slog.Debug("zapfast voip: participant Opus decode failed", "err", err)
			pcm = dec.Conceal(nil, int(format.SamplesPerFrame))
			e.stats.AudioFramesConcealed++
		}
		g.mixer.Push(id, pcm)
		return
	}
	dec := g.decoders[id]
	if dec == nil {
		dec = mlow.NewDecoder()
		g.decoders[id] = dec
	}
	red := int32(0)
	if pm.Header.PayloadType == rtp.PayloadTypeMLowRed {
		red = 1
	}
	dec.SetRedundancy(red)
	floats := dec.AppendDecode(nil, pm.Payload)
	pcm := make([]int16, len(floats))
	for i, s := range floats {
		pcm[i] = floatToInt16(s)
	}
	r := dec.TakeFrameReport()
	e.stats.AudioFramesDecoded += uint32(r.Decoded)
	e.stats.AudioFramesConcealed += uint32(r.Concealed)
	e.stats.MLowOffPointDropped += uint32(r.OffPoint)
	e.stats.MLowInactiveOrSID += uint32(r.InactiveOrSID)
	if r.Decoded == 0 {
		if verdict, ok := e.observeGroupCodec(pm, codec); ok && verdict == Opus {
			// What the decoder built from another codec's bytes, and the
			// concealment it queued, would play in front of the rescue.
			delete(g.decoders, id)
			g.mixer.Reset(id)
			pcm = nil
		}
	}
	g.mixer.Push(id, pcm)
}

// observeGroupCodec asks one participant's probe whether its bytes
// contradict the negotiation, with the packet's own classification; a
// verdict latches the participant.
func (e *Engine) observeGroupCodec(pm ParticipantMedia, classified AudioCodec) (AudioCodec, bool) {
	g, f := e.group, e.media.audio
	var span uint32
	var have bool
	if r := g.audioReception[pm.ParticipantID]; r != nil {
		span, have = r.FrameSpan()
	}
	probe := g.probes[pm.ParticipantID]
	if probe == nil {
		probe = &codecProbe{}
		g.probes[pm.ParticipantID] = probe
	}
	verdict, ok := probe.observe(pm.Payload, classified, span, have, f.ClockRate, f.TimestampStep)
	if ok {
		g.foreignParticipants[pm.ParticipantID] = true
	}
	return verdict, ok
}

// groupPlayout mixes two 10 ms chunks into the 20 ms the speaker takes.
func (e *Engine) groupPlayout() []int16 {
	frame := make([]int16, 0, playoutDrain)
	for range 2 {
		if c := e.group.mixer.MixChunk(); c != nil {
			frame = append(frame, c...)
		} else {
			frame = append(frame, make([]int16, playoutDrain/2)...)
		}
	}
	return frame
}
