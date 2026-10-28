// Ported from whatsapp-rust 5e72baf wacore/src/voip/group_audio.rs (its
// unit tests), MIT.

package engine

import (
	"slices"
	"testing"
)

func fill(v int16, n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestMixerPrefillAndSingleParticipantAreBitExact(t *testing.T) {
	m := NewParticipantAudioMixer()
	if !m.Push("alice", fill(1234, 960)) || m.MixChunk() != nil {
		t.Fatal("one 60 ms packet must not play")
	}
	m.Push("alice", fill(1234, 960))
	if got := m.MixChunk(); !slices.Equal(got, fill(1234, GroupMixChunkSamples)) {
		t.Fatalf("%v", got)
	}
}

func TestMixerShortBurstFlushesAfterBoundedPriming(t *testing.T) {
	m := NewParticipantAudioMixer()
	m.Push("alice", fill(1234, GroupMixOutputSamples))
	for range groupMixMaxPrimeChunks - 1 {
		if m.MixChunk() != nil {
			t.Fatal("partial speech plays before the cushion")
		}
	}
	for range GroupMixOutputSamples / GroupMixChunkSamples {
		if got := m.MixChunk(); !slices.Equal(got, fill(1234, GroupMixChunkSamples)) {
			t.Fatalf("burst did not drain: %v", got)
		}
	}
	if m.MixChunk() != nil {
		t.Fatal("drained queue still plays")
	}
}

func TestMixerLongerPacketsStillGetAPacketOfCushion(t *testing.T) {
	m := NewParticipantAudioMixer()
	m.Push("alice", fill(50, 1920))
	if m.MixChunk() != nil {
		t.Fatal("one 120 ms packet is no cushion")
	}
	m.Push("alice", fill(50, 1920))
	if got := m.MixChunk(); !slices.Equal(got, fill(50, GroupMixChunkSamples)) {
		t.Fatalf("%v", got)
	}
}

func TestMixerSimultaneousSpeakersSumAndSaturate(t *testing.T) {
	for _, tc := range []struct{ in, want int16 }{{20000, 32767}, {-20000, -32768}} {
		m := NewParticipantAudioMixer()
		for range 2 {
			m.Push("alice", fill(tc.in, GroupMixOutputSamples))
			m.Push("bob", fill(tc.in, GroupMixOutputSamples))
		}
		if got := m.MixChunk(); !slices.Equal(got, fill(tc.want, GroupMixChunkSamples)) {
			t.Fatalf("%d: %v", tc.in, got[:4])
		}
	}
}

func TestMixerRosterRemovesAndBlocksDepartedParticipants(t *testing.T) {
	m := NewParticipantAudioMixer()
	for range 2 {
		m.Push("alice", fill(100, GroupMixOutputSamples))
		m.Push("bob", fill(200, GroupMixOutputSamples))
	}
	m.Retain([]string{"alice"})
	if m.Push("bob", fill(200, GroupMixChunkSamples)) {
		t.Fatal("departed participant accepted")
	}
	if got := m.MixChunk(); !slices.Equal(got, fill(100, GroupMixChunkSamples)) {
		t.Fatalf("%v", got[:4])
	}
}

func TestMixerOverflowDiscardsOldestAudio(t *testing.T) {
	m := NewParticipantAudioMixer()
	for range GroupMixQueueCapacity / GroupMixOutputSamples {
		m.Push("alice", fill(1, GroupMixOutputSamples))
	}
	m.Push("alice", fill(2, GroupMixChunkSamples))
	if got := m.MixChunk(); !slices.Equal(got, fill(1, GroupMixChunkSamples)) {
		t.Fatalf("%v", got[:4])
	}
	for range GroupMixQueueCapacity/GroupMixChunkSamples - 2 {
		m.MixChunk()
	}
	if got := m.MixChunk(); !slices.Equal(got, fill(2, GroupMixChunkSamples)) {
		t.Fatalf("%v", got[:4])
	}
}

func TestFramerEmitsExactlyOnePublicFrame(t *testing.T) {
	var f ParticipantAudioFramer
	for i := range 5 {
		if f.Push(fill(int16(i), GroupMixChunkSamples)) != nil {
			t.Fatal("early frame")
		}
	}
	frame := f.Push(fill(5, GroupMixChunkSamples))
	if len(frame) != GroupMixOutputSamples || frame[0] != 0 || frame[GroupMixOutputSamples-1] != 5 {
		t.Fatalf("frame %d", len(frame))
	}
	if f.Push(fill(0, GroupMixChunkSamples-1)) != nil {
		t.Fatal("wrong-sized chunk accepted")
	}
}
