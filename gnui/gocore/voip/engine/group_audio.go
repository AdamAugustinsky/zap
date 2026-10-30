// Ported from whatsapp-rust 5e72baf wacore/src/voip/group_audio.rs, MIT.

package engine

// The group playout mixer: one bounded PCM queue per participant, mixed
// into 10 ms saturating chunks.
const (
	// GroupMixChunkSamples is 10 ms at the 16 kHz mono playout rate.
	GroupMixChunkSamples = 160
	// GroupMixPrefillSamples is two 60 ms frames before a participant joins
	// playout. A floor: a queue scales it to two of the participant's
	// largest packets, so a 120 ms sender still gets a packet of cushion.
	GroupMixPrefillSamples = 1920
	// groupMixMaxPrimeChunks bounds priming of a partial buffer to about
	// 200 ms, so a short utterance before DTX still plays.
	groupMixMaxPrimeChunks = 20
	// GroupMixQueueCapacity is four 60 ms frames (a floor, scaled like the
	// prefill); overflow drops the oldest samples to stay real time.
	GroupMixQueueCapacity = 3840
	// GroupMixOutputSamples is the framer's frame: 60 ms at 16 kHz.
	GroupMixOutputSamples = 960
)

type participantQueue struct {
	samples       []int16
	primed        bool
	primingChunks uint32
	// packetSamples is the largest packet seen, what the prefill and the
	// capacity are measured in.
	packetSamples int
}

func (q *participantQueue) prefill() int  { return max(q.packetSamples*2, GroupMixPrefillSamples) }
func (q *participantQueue) capacity() int { return max(q.packetSamples*4, GroupMixQueueCapacity) }

// ParticipantAudioMixer keeps independent bounded queues and mixes them
// into fixed 10 ms chunks.
type ParticipantAudioMixer struct {
	allowed map[string]bool // nil: no roster yet, every participant allowed
	queues  map[string]*participantQueue
}

// NewParticipantAudioMixer is an empty mixer.
func NewParticipantAudioMixer() *ParticipantAudioMixer {
	return &ParticipantAudioMixer{queues: map[string]*participantQueue{}}
}

// Retain gates future pushes to the active roster and drops departed
// participants' queues.
func (m *ParticipantAudioMixer) Retain(participants []string) {
	allowed := make(map[string]bool, len(participants))
	for _, p := range participants {
		allowed[p] = true
	}
	for p := range m.queues {
		if !allowed[p] {
			delete(m.queues, p)
		}
	}
	m.allowed = allowed
}

// Reset drops a participant's queued PCM (its relay media session was
// replaced).
func (m *ParticipantAudioMixer) Reset(participant string) { delete(m.queues, participant) }

// Push queues one decoded packet of a participant; false for one outside
// the roster. One packet per call: the queue sizes its cushion to it.
func (m *ParticipantAudioMixer) Push(participant string, pcm []int16) bool {
	if len(pcm) == 0 || (m.allowed != nil && !m.allowed[participant]) {
		return false
	}
	q := m.queues[participant]
	if q == nil {
		q = &participantQueue{samples: make([]int16, 0, GroupMixQueueCapacity)}
		m.queues[participant] = q
	}
	q.packetSamples = max(q.packetSamples, len(pcm))
	q.samples = append(q.samples, pcm...)
	if over := len(q.samples) - q.capacity(); over > 0 {
		q.samples = append(q.samples[:0], q.samples[over:]...)
	}
	if len(q.samples) >= q.prefill() {
		q.primed, q.primingChunks = true, 0
	}
	return true
}

// MixChunk mixes one 10 ms chunk, nil when nobody is ready. One ready
// participant passes bit for bit; simultaneous speakers sum with int16
// saturation.
func (m *ParticipantAudioMixer) MixChunk() []int16 {
	ready := 0
	for _, q := range m.queues {
		if !q.primed {
			if len(q.samples) == 0 {
				q.primingChunks = 0
				continue
			}
			q.primingChunks++
			if q.primingChunks >= groupMixMaxPrimeChunks {
				q.primed, q.primingChunks = true, 0
			}
		}
		if q.primed && len(q.samples) > 0 {
			ready++
		}
	}
	if ready == 0 {
		return nil
	}
	var mixed [GroupMixChunkSamples]int32
	for _, q := range m.queues {
		if !q.primed || len(q.samples) == 0 {
			continue
		}
		n := min(len(q.samples), GroupMixChunkSamples)
		for i := range n {
			mixed[i] += int32(q.samples[i])
		}
		q.samples = append(q.samples[:0], q.samples[n:]...)
		if len(q.samples) == 0 {
			q.primed, q.primingChunks = false, 0
		}
	}
	out := make([]int16, GroupMixChunkSamples)
	for i, s := range mixed {
		out[i] = int16(min(max(s, -32768), 32767))
	}
	return out
}

// ParticipantAudioFramer reframes 10 ms mixer chunks into 60 ms frames.
type ParticipantAudioFramer struct{ samples []int16 }

// Push adds one 10 ms chunk; a full 60 ms frame comes back when one is
// complete. A chunk of the wrong size is dropped.
func (f *ParticipantAudioFramer) Push(chunk []int16) []int16 {
	if len(chunk) != GroupMixChunkSamples {
		return nil
	}
	f.samples = append(f.samples, chunk...)
	if len(f.samples) < GroupMixOutputSamples {
		return nil
	}
	out := f.samples
	f.samples = make([]int16, 0, GroupMixOutputSamples)
	return out
}
