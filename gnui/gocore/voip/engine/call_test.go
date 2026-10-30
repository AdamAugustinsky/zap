package engine

// Full 1:1 calls between two Go engines over the in-memory relay: MLow and
// Opus audio (a tone in, its SNR out), H.264 video (a pattern in, its PSNR
// out), mute and camera toggles, teardown and the failure paths.

import (
	"errors"
	"testing"
	"time"

	"zapfast/gocore/av"
	"zapfast/gocore/voip/video"
)

func needVideo(t *testing.T) {
	t.Helper()
	enc, err := video.NewEncoder(video.EncoderConfig{Width: 320, Height: 240})
	if errors.Is(err, video.ErrUnavailable) {
		t.Skip("no H.264 codec here")
	}
	if err != nil {
		t.Fatal(err)
	}
	enc.Close()
}

// opusCodec is gocore/av's streaming Opus as the engine's ForeignCodec.
type opusCodec struct {
	enc *av.OpusStreamEncoder
	dec *av.OpusStreamDecoder
}

func newOpus() (ForeignCodec, error) {
	enc, err := av.NewOpusStreamEncoder(16000, MicFrameSamples, 24000)
	if err != nil {
		return nil, err
	}
	dec, err := av.NewOpusStreamDecoder(16000)
	if err != nil {
		enc.Close()
		return nil, err
	}
	return &opusCodec{enc, dec}, nil
}

func (o *opusCodec) Decode(dst []int16, p []byte) ([]int16, error)  { return o.dec.Decode(dst, p) }
func (o *opusCodec) Encode(dst []byte, pcm []int16) ([]byte, error) { return o.enc.Encode(dst, pcm) }
func (o *opusCodec) Conceal(dst []int16, n int) []int16 {
	return append(dst, make([]int16, n)...)
}

func opusRef(t *testing.T, freqs []float64) float64 {
	t.Helper()
	c, err := newOpus()
	if err != nil {
		t.Fatal(err)
	}
	return codecSNR(t, freqs, c)
}

func needOpus(t *testing.T) {
	t.Helper()
	c, err := newOpus()
	if errors.Is(err, av.ErrUnavailable) {
		t.Skip("no Opus codec here")
	}
	if err != nil {
		t.Fatal(err)
	}
	c.(*opusCodec).enc.Close()
	c.(*opusCodec).dec.Close()
}

// pair opens both ends: a calls b; b answers (the caller's rekey names b).
func pair(t *testing.T, r *memRelay, a, b sideOpts) (*side, *side) {
	t.Helper()
	a.outgoing, a.self, a.peer = true, lidA, lidB
	b.outgoing, b.self, b.peer = false, lidB, lidA
	if a.freqs == nil {
		a.freqs = toneFreqs
	}
	if b.freqs == nil {
		b.freqs = []float64{450, 900}
	}
	sa := openSide(t, r, a)
	sb := openSide(t, r, b)
	sa.ev.wait(t, CallConnected, 5*time.Second)
	sb.ev.wait(t, CallConnected, 5*time.Second)
	return sa, sb
}

// checkAudio holds what s heard to the codec's own SNR on the same tone
// (ref): the relay, SRTP and the jitter buffer may cost at most 1.5 dB.
func checkAudio(t *testing.T, who string, s *side, freqs []float64, ref float64) float64 {
	t.Helper()
	snr := toneSNR(s.audio.playedCopy(), freqs)
	st := s.call.Stats()
	t.Logf("%s heard the tone at %.1f dB SNR, the codec alone %.1f dB (rtp %d, decoded %d, foreign %d, auth failures %d, trimmed %d)",
		who, snr, ref, st.RTPReceived, st.AudioFramesDecoded, st.ForeignFramesDecoded, st.SRTPUnprotectFailed, st.PlayoutTrimmedSamples)
	if snr < ref-1.5 || snr < 6 {
		t.Errorf("%s: SNR %.1f dB, the codec alone gives %.1f", who, snr, ref)
	}
	if st.SRTPUnprotectFailed != 0 {
		t.Errorf("%s: %d SRTP failures", who, st.SRTPUnprotectFailed)
	}
	return snr
}

func TestACallCarriesMLowAudioBothWays(t *testing.T) {
	r := &memRelay{}
	a, b := pair(t, r, sideOpts{}, sideOpts{})
	a.call.Command(Command{Op: OpRekey, LID: lidB})
	time.Sleep(4 * time.Second)
	checkAudio(t, "the callee", b, toneFreqs, codecSNR(t, toneFreqs, nil))
	checkAudio(t, "the caller", a, []float64{450, 900}, codecSNR(t, []float64{450, 900}, nil))
	if b.call.Stats().AudioFramesDecoded == 0 || a.call.Stats().AudioFramesDecoded == 0 {
		t.Error("MLow decoded nothing")
	}
	a.end(t)
	b.end(t)
}

func TestACallCarriesStandardOpusWhenBothNegotiateIt(t *testing.T) {
	needOpus(t)
	r := &memRelay{}
	a, b := pair(t, r, sideOpts{opus: true, standard: true}, sideOpts{opus: true, standard: true})
	a.call.Command(Command{Op: OpRekey, LID: lidB, Codec: "opus"})
	time.Sleep(4 * time.Second)
	checkAudio(t, "the callee", b, toneFreqs, opusRef(t, toneFreqs))
	checkAudio(t, "the caller", a, []float64{450, 900}, opusRef(t, []float64{450, 900}))
	if a.call.Stats().CodecSwitches != 0 || b.call.Stats().CodecSwitches != 0 {
		t.Error("a call negotiated on Opus switched codec")
	}
}

func TestACallerMovesToTheOpusItsCalleeAnswersWith(t *testing.T) {
	needOpus(t)
	r := &memRelay{}
	a := sideOpts{opus: true}
	b := sideOpts{opus: true, standard: true}
	sa, sb := pair(t, r, a, b)
	sa.call.Command(Command{Op: OpRekey, LID: lidB, Codec: "opus"})
	sa.ev.wait(t, CallAudible, 3*time.Second)
	time.Sleep(4 * time.Second)
	checkAudio(t, "the callee", sb, toneFreqs, opusRef(t, toneFreqs))
	checkAudio(t, "the caller", sa, []float64{450, 900}, opusRef(t, []float64{450, 900}))
	if sa.call.Stats().ForeignFramesDecoded == 0 || sb.call.Stats().ForeignFramesDecoded == 0 {
		t.Error("no Opus was decoded")
	}
}

func TestThePeersOpusContentRescuesACallerThatHeardNoCapability(t *testing.T) {
	needOpus(t)
	r := &memRelay{}
	// The callee sends Opus; the caller's accept carried no codec, so only
	// the probe can tell.
	sa, sb := pair(t, r, sideOpts{opus: true}, sideOpts{opus: true, standard: true})
	sa.call.Command(Command{Op: OpRekey, LID: lidB})
	sa.ev.wait(t, CallAudible, 3*time.Second)
	time.Sleep(3 * time.Second)
	checkAudio(t, "the caller", sa, []float64{450, 900}, opusRef(t, []float64{450, 900}))
	if sa.call.Stats().CodecSwitches != 1 {
		t.Errorf("codec switches %d, want 1", sa.call.Stats().CodecSwitches)
	}
	_ = sb
}

func TestAVideoCallCarriesH264BothWays(t *testing.T) {
	needVideo(t)
	r := &memRelay{}
	a, b := pair(t, r, sideOpts{video: true, cam: true}, sideOpts{video: true, cam: true})
	a.call.Command(Command{Op: OpRekey, LID: lidB})
	a.ev.wait(t, CallRemoteVideo, 5*time.Second)
	b.ev.wait(t, CallRemoteVideo, 5*time.Second)
	time.Sleep(2 * time.Second)
	for _, c := range []struct {
		who  string
		recv *side
		send *side
	}{{"the callee", b, a}, {"the caller", a, b}} {
		pics := c.recv.frames.remotes()
		if len(pics) < 20 {
			t.Errorf("%s decoded only %d pictures", c.who, len(pics))
			continue
		}
		var sum float64
		last := pics[len(pics)-10:]
		for _, p := range last {
			sum += c.send.cam.bestPSNR(p)
		}
		avg := sum / float64(len(last))
		st := c.recv.call.Stats()
		t.Logf("%s decoded %d pictures, the last ten at %.1f dB PSNR (video rtp %d, lost %d, PLIs sent %d)",
			c.who, len(pics), avg, st.VideoRTPReceived, st.VideoRTPLost, st.PeerKeyframeRequests)
		if avg < 28 {
			t.Errorf("%s: PSNR %.1f dB, want at least 28", c.who, avg)
		}
		if st.VideoSRTPUnprotectFailed != 0 {
			t.Errorf("%s: %d video SRTP failures", c.who, st.VideoSRTPUnprotectFailed)
		}
	}
	// Audio rode along.
	checkAudio(t, "the callee", b, toneFreqs, codecSNR(t, toneFreqs, nil))
}

func TestMuteAndCameraToggles(t *testing.T) {
	needVideo(t)
	r := &memRelay{}
	a, b := pair(t, r, sideOpts{video: true, cam: true}, sideOpts{video: true, cam: true})
	a.call.Command(Command{Op: OpRekey, LID: lidB})
	b.ev.wait(t, CallRemoteVideo, 5*time.Second)
	time.Sleep(time.Second)

	// Mute: the callee hears comfort noise (SIDs), not the tone, and the
	// call is not reported silent.
	sidBefore := b.call.Stats().MLowInactiveOrSID
	a.call.Command(Command{Op: OpMute, On: true})
	time.Sleep(2 * time.Second)
	if got := b.call.Stats().MLowInactiveOrSID - sidBefore; got < 10 {
		t.Errorf("a muted caller sent %d comfort-noise frames, want a stream of them", got)
	}
	if snr := toneSNR(b.audio.playedCopy(), toneFreqs); snr > 0 {
		t.Errorf("the tone still came through a mute (%.1f dB)", snr)
	}
	a.call.Command(Command{Op: OpMute, On: false})
	time.Sleep(2 * time.Second)
	checkAudio(t, "the callee after unmute", b, toneFreqs, codecSNR(t, toneFreqs, nil))
	if b.ev.has(CallSilent) {
		t.Error("a mute was reported as a silent call")
	}

	// Camera off: the callee's pictures stop; on: they come back.
	a.call.Command(Command{Op: OpCameraOff})
	time.Sleep(500 * time.Millisecond)
	n := len(b.frames.remotes())
	time.Sleep(time.Second)
	if got := len(b.frames.remotes()) - n; got > 2 {
		t.Errorf("%d pictures arrived with the camera off", got)
	}
	a.call.Command(Command{Op: OpCameraOn})
	time.Sleep(2 * time.Second)
	if got := len(b.frames.remotes()) - n; got < 10 {
		t.Errorf("only %d pictures after the camera came back", got)
	}
	a.end(t)
	b.end(t)
}

func TestTeardownReleasesEveryDevice(t *testing.T) {
	needVideo(t)
	r := &memRelay{}
	a, b := pair(t, r, sideOpts{video: true, cam: true}, sideOpts{})
	a.end(t)
	select {
	case <-a.audio.stop:
	default:
		t.Error("the microphone was not closed")
	}
	select {
	case <-a.cam.done:
	default:
		t.Error("the camera was not closed")
	}
	if a.ev.has(CallFailed) || a.ev.has(CallClosed) {
		t.Errorf("a hangup reported a failure: %v", a.ev.snapshot())
	}
	b.end(t)
	// Commands after the end are ignored.
	a.call.Command(Command{Op: OpMute, On: true})
	a.call.Close()
}

func TestAnUnreachableRelayFailsTheCall(t *testing.T) {
	r := &memRelay{dialErr: errors.New("connection refused")}
	s := openSide(t, r, sideOpts{outgoing: true, self: lidA, peer: lidB})
	ev := s.ev.wait(t, CallFailed, 2*time.Second)
	if ev.Reason != "The relay could not be reached: connection refused" {
		t.Errorf("reason %q", ev.Reason)
	}
	s.ev.wait(t, CallEnded, 2*time.Second)
}

func TestARelayThatRefusesTheAllocateFailsTheCall(t *testing.T) {
	r := &memRelay{refuse: 486}
	s := openSide(t, r, sideOpts{outgoing: true, self: lidA, peer: lidB})
	ev := s.ev.wait(t, CallFailed, 2*time.Second)
	if ev.Reason != "The relay refused the call (486)" {
		t.Errorf("reason %q", ev.Reason)
	}
	s.ev.wait(t, CallEnded, 2*time.Second)
	if s.ev.has(CallConnected) {
		t.Error("a refused call connected")
	}
}

func TestBadKeysAreCountedAndReportedAsAuthenticationFailing(t *testing.T) {
	r := &memRelay{}
	wrong := append([]byte(nil), testCallKey...)
	wrong[0] ^= 0xff
	a, b := pair(t, r, sideOpts{}, sideOpts{key: wrong})
	a.call.Command(Command{Op: OpRekey, LID: lidB})
	ev := b.ev.wait(t, CallSilent, 6*time.Second)
	if ev.Reason != "authentication_failing" {
		t.Errorf("silent because %q", ev.Reason)
	}
	if n := b.call.Stats().SRTPUnprotectFailed; n < 12 {
		t.Errorf("%d authentication failures counted", n)
	}
	if n := b.call.Stats().AudioFramesDecoded; n != 0 {
		t.Errorf("%d frames decoded under the wrong key", n)
	}
	_ = a
}

func TestAPeerThatGoesAwayIsReportedStalled(t *testing.T) {
	r := &memRelay{}
	a, b := pair(t, r, sideOpts{}, sideOpts{})
	a.call.Command(Command{Op: OpRekey, LID: lidB})
	time.Sleep(time.Second)
	b.end(t)
	a.ev.wait(t, CallStalled, 5*time.Second)
	if a.ev.has(CallFailed) {
		t.Error("a peer that left failed the call; the signaling ends it")
	}
}

func TestADroppedRelayClosesAConnectedCall(t *testing.T) {
	r := &memRelay{}
	a, _ := pair(t, r, sideOpts{}, sideOpts{})
	// The sides dial at once, so which end is a's is a race: the relay
	// drops them all.
	r.mu.Lock()
	ends := append([]*memTransport(nil), r.ends...)
	r.mu.Unlock()
	for _, end := range ends {
		end.Close()
	}
	ev := a.ev.wait(t, CallClosed, 3*time.Second)
	if ev.Reason != "The relay connection dropped" {
		t.Errorf("reason %q", ev.Reason)
	}
	a.ev.wait(t, CallEnded, 3*time.Second)
}
