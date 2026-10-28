//go:build darwin

package av

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The call's Opus: one AudioConverter per direction that lives as long as
// the call, so the codec's state carries from packet to packet. The input
// procs hand over what one Encode or Decode has and then answer
// errNoMore, a private status: AudioConverterFillComplexBuffer returns it
// with what it made so far, and the converter is not put at end of stream
// (which a zero-packet noErr would do), so the next call carries on.

// errNoMore is the input procs' "nothing more for now" ('nmor').
var errNoMore = int32(fourCC("nmor"))

// kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked
var intPacked uint32 = 4 | 8

var stream struct {
	once                     sync.Once
	err                      error
	encodeInput, decodeInput uintptr
}

func loadStream() error {
	stream.once.Do(func() {
		if stream.err = loadAudioToolbox(); stream.err != nil {
			return
		}
		stream.encodeInput = purego.NewCallback(streamEncodeInput)
		stream.decodeInput = purego.NewCallback(streamDecodeInput)
	})
	return stream.err
}

func int16Format(rate int) streamFormat {
	return streamFormat{
		SampleRate: float64(rate), FormatID: formatLPCM, FormatFlags: intPacked,
		BytesPerPacket: 2, FramesPerPacket: 1, BytesPerFrame: 2, Channels: 1, Bits: 16,
	}
}

// streamJob is one Encode's or Decode's input: the bytes (PCM or one
// packet), handed over once.
type streamJob struct {
	data   unsafe.Pointer
	size   int // bytes
	frames int // PCM frames (encoder) or 1 (decoder)
	given  bool
	desc   packetDesc
}

func streamEncodeInput(_ uintptr, packets *uint32, data *bufferList, _ **packetDesc, id uintptr) int32 {
	v, ok := jobs.Load(id)
	if !ok {
		*packets = 0
		return -1
	}
	j := v.(*streamJob)
	data.N, data.Channels = 1, 1
	if j.given {
		*packets, data.Size, data.Data = 0, 0, nil
		return errNoMore
	}
	j.given = true
	data.Data, data.Size = j.data, uint32(j.size)
	*packets = uint32(j.frames)
	return 0
}

func streamDecodeInput(_ uintptr, packets *uint32, data *bufferList, desc **packetDesc, id uintptr) int32 {
	v, ok := jobs.Load(id)
	if !ok {
		*packets = 0
		return -1
	}
	j := v.(*streamJob)
	data.N, data.Channels = 1, 1
	if j.given {
		*packets, data.Size, data.Data = 0, 0, nil
		return errNoMore
	}
	j.given = true
	data.Data, data.Size = j.data, uint32(j.size)
	j.desc = packetDesc{Size: uint32(j.size)}
	if desc != nil {
		*desc = &j.desc
	}
	*packets = 1
	return 0
}

// OpusStreamEncoder encodes a call's microphone: mono int16 PCM at its rate
// (16 kHz for a call), one Opus packet for each frame of frameSamples
// (960: 60 ms), on AudioToolbox's Opus encoder. Its methods are safe from
// any goroutine, one at a time.
//
// It runs natively at 16 kHz with 60 ms packets: each Encode gives that
// frame's packet at once, delayed only by the codec's 6.5 ms lookahead.
// Of whatsapp-rust's WaOpusEncoder settings (VoIP application, 24 kbit/s,
// complexity 5, DTX) AudioToolbox takes the bit rate (variable rate, its
// default); it exposes no application, complexity or DTX property, so
// those are its own.
type OpusStreamEncoder struct {
	mu     sync.Mutex
	conv   uintptr
	frame  int
	max    int
	buf    []byte
	queued [][]byte // packets the converter made beyond the one asked for
}

// NewOpusStreamEncoder is an encoder for frames of frameSamples at rate and
// bitrate bits a second. Off macOS it fails with ErrUnavailable.
func NewOpusStreamEncoder(rate, frameSamples, bitrate int) (*OpusStreamEncoder, error) {
	if rate <= 0 || frameSamples <= 0 {
		return nil, fmt.Errorf("av: an Opus stream of %d samples at %d Hz", frameSamples, rate)
	}
	if err := loadStream(); err != nil {
		return nil, err
	}
	in := int16Format(rate)
	out := streamFormat{SampleRate: float64(rate), FormatID: formatOpus, FramesPerPacket: uint32(frameSamples), Channels: 1}
	var conv uintptr
	if st := at.newConverter(&in, &out, &conv); st != 0 {
		return nil, &UnavailableError{What: "Opus", Err: fmt.Errorf("the stream encoder (%d Hz, %d samples): %w", rate, frameSamples, osStatus(st))}
	}
	e := &OpusStreamEncoder{conv: conv, frame: frameSamples}
	if bitrate > 0 {
		r := uint32(bitrate)
		if st := at.setProperty(conv, propBitRate, 4, unsafe.Pointer(&r)); st != 0 {
			e.Close()
			return nil, fmt.Errorf("av: setting the Opus bit rate: %w", osStatus(st))
		}
	}
	var maxPacket uint32
	size := uint32(4)
	if st := at.getProperty(conv, propMaxPacket, &size, unsafe.Pointer(&maxPacket)); st != 0 || maxPacket == 0 {
		maxPacket = 1275
	}
	e.max = int(maxPacket)
	e.buf = make([]byte, 4*e.max)
	return e, nil
}

// Encode appends to dst the one packet for pcm, which must be exactly one
// frame.
func (e *OpusStreamEncoder) Encode(dst []byte, pcm []int16) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conv == 0 {
		return dst, errors.New("av: the Opus encoder is closed")
	}
	if len(pcm) != e.frame {
		return dst, fmt.Errorf("av: an Opus frame of %d samples, not %d", len(pcm), e.frame)
	}
	j := &streamJob{data: unsafe.Pointer(&pcm[0]), size: 2 * len(pcm), frames: len(pcm)}
	var pin runtime.Pinner
	defer pin.Unpin()
	pin.Pin(&pcm[0])
	pin.Pin(&e.buf[0])
	descs := make([]packetDesc, 4)
	pin.Pin(&descs[0])
	id := nextID.Add(1)
	jobs.Store(id, j)
	defer jobs.Delete(id)
	out := bufferList{N: 1, Channels: 1, Size: uint32(len(e.buf)), Data: unsafe.Pointer(&e.buf[0])}
	n := uint32(len(descs))
	st := at.fill(e.conv, stream.encodeInput, id, &n, &out, &descs[0])
	if st != 0 && st != errNoMore {
		return dst, fmt.Errorf("av: Opus encoding: %w", osStatus(st))
	}
	for _, d := range descs[:n] {
		end := d.Start + int64(d.Size)
		if d.Start < 0 || end > int64(out.Size) {
			return dst, errors.New("av: Opus encoding: a packet out of its buffer")
		}
		e.queued = append(e.queued, append([]byte(nil), e.buf[d.Start:end]...))
	}
	if len(e.queued) == 0 {
		return dst, errors.New("av: the Opus encoder made no packet for a frame")
	}
	p := e.queued[0]
	e.queued = e.queued[1:]
	return append(dst, p...), nil
}

// Close releases the encoder.
func (e *OpusStreamEncoder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conv != 0 {
		at.dispose(e.conv)
		e.conv = 0
	}
}

// OpusStreamDecoder decodes a call's peer: one Opus packet at a time (any
// valid packet, from any encoder) to mono int16 at its rate, on
// AudioToolbox's Opus decoder. Its methods are safe from any goroutine, one
// at a time.
type OpusStreamDecoder struct {
	mu   sync.Mutex
	conv uintptr
	rate int
	buf  []int16
}

// NewOpusStreamDecoder is a decoder to rate. Off macOS it fails with
// ErrUnavailable.
func NewOpusStreamDecoder(rate int) (*OpusStreamDecoder, error) {
	if rate <= 0 {
		return nil, fmt.Errorf("av: an Opus stream at %d Hz", rate)
	}
	if err := loadStream(); err != nil {
		return nil, err
	}
	in := streamFormat{SampleRate: float64(rate), FormatID: formatOpus, Channels: 1}
	out := int16Format(rate)
	var conv uintptr
	if st := at.newConverter(&in, &out, &conv); st != 0 {
		return nil, &UnavailableError{What: "Opus", Err: fmt.Errorf("the stream decoder (%d Hz): %w", rate, osStatus(st))}
	}
	// 120 ms, the longest packet Opus has.
	return &OpusStreamDecoder{conv: conv, rate: rate, buf: make([]int16, rate*120/1000)}, nil
}

// Decode appends packet's samples to dst.
func (d *OpusStreamDecoder) Decode(dst []int16, packet []byte) ([]int16, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conv == 0 {
		return dst, errors.New("av: the Opus decoder is closed")
	}
	if len(packet) == 0 {
		return dst, malformed("an empty Opus packet")
	}
	data := append([]byte(nil), packet...)
	j := &streamJob{data: unsafe.Pointer(&data[0]), size: len(data), frames: 1}
	var pin runtime.Pinner
	defer pin.Unpin()
	pin.Pin(&data[0])
	pin.Pin(j) // its packet description is read by C between callbacks
	pin.Pin(&d.buf[0])
	id := nextID.Add(1)
	jobs.Store(id, j)
	defer jobs.Delete(id)
	start := len(dst)
	for {
		out := bufferList{N: 1, Channels: 1, Size: uint32(2 * len(d.buf)), Data: unsafe.Pointer(&d.buf[0])}
		n := uint32(len(d.buf))
		st := at.fill(d.conv, stream.decodeInput, id, &n, &out, nil)
		dst = append(dst, d.buf[:n]...)
		if st == errNoMore || (st == 0 && n == 0) {
			break
		}
		if st != 0 {
			return dst[:start], malformed(fmt.Sprintf("a damaged Opus packet (%v)", osStatus(st)))
		}
	}
	// AudioToolbox drops the codec's 2.5 ms of lead from the first packet
	// (atDecoderLead). A call's playout counts on every packet giving its
	// whole duration, so the shortfall is made up with leading silence.
	if want, ok := packetSamples(packet); ok {
		want = want * d.rate / 48000
		if got := len(dst) - start; got < want {
			dst = append(dst, make([]int16, want-got)...)
			copy(dst[start+want-got:], dst[start:start+got])
			clear(dst[start : start+want-got])
		}
	}
	return dst, nil
}

// Close releases the decoder.
func (d *OpusStreamDecoder) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conv != 0 {
		at.dispose(d.conv)
		d.conv = 0
	}
}
