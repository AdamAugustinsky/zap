//go:build darwin

package av

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Opus through AudioToolbox: an AudioConverter between float32 PCM and
// kAudioFormatOpus, which macOS has had since 10.13. AudioToolbox is a
// system framework, so nothing is bundled. The converter pulls its input
// through a callback; the two callbacks are made once, and find their call's
// state by an id, so no Go pointer is handed to C as user data.

const audioToolbox = "/System/Library/Frameworks/AudioToolbox.framework/AudioToolbox"

// at is AudioToolbox, loaded on first use.
var at struct {
	once sync.Once
	err  error

	newConverter func(in, out *streamFormat, conv *uintptr) int32
	fill         func(conv, proc, user uintptr, packets *uint32, out *bufferList, desc *packetDesc) int32
	getProperty  func(conv uintptr, id uint32, size *uint32, data unsafe.Pointer) int32
	setProperty  func(conv uintptr, id uint32, size uint32, data unsafe.Pointer) int32
	dispose      func(conv uintptr) int32

	encodeInput, decodeInput uintptr // the input callbacks
}

// AudioStreamBasicDescription.
type streamFormat struct {
	SampleRate      float64
	FormatID        uint32
	FormatFlags     uint32
	BytesPerPacket  uint32
	FramesPerPacket uint32
	BytesPerFrame   uint32
	Channels        uint32
	Bits            uint32
	_               uint32
}

// AudioBufferList with one AudioBuffer.
type bufferList struct {
	N        uint32
	_        uint32
	Channels uint32
	Size     uint32
	Data     unsafe.Pointer
}

// AudioStreamPacketDescription.
type packetDesc struct {
	Start  int64
	Frames uint32
	Size   uint32
}

func fourCC(s string) uint32 {
	return uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3])
}

var (
	formatLPCM = fourCC("lpcm")
	formatOpus = fourCC("opus")
	// kAudioFormatFlagIsFloat | kAudioFormatFlagIsPacked
	floatPacked uint32 = 1 | 8

	propBitRate   = fourCC("brat") // kAudioConverterEncodeBitRate
	propPrimeInfo = fourCC("prim") // kAudioConverterPrimeInfo
	propMaxPacket = fourCC("xops") // kAudioConverterPropertyMaximumOutputPacketSize
)

// atDecoderLead is how many leading samples AudioToolbox's Opus decoder
// drops by itself (2.5 ms). Measured against libopus (ffmpeg's decoder, which
// drops exactly the pre-skip): the decoder's output starts this much later
// on every stream (TestDecoderMatchesLibopus).
const atDecoderLead = 120

func loadAudioToolbox() error {
	at.once.Do(func() {
		lib, err := purego.Dlopen(audioToolbox, purego.RTLD_LAZY|purego.RTLD_LOCAL)
		if err != nil {
			at.err = &UnavailableError{What: "Opus", Path: audioToolbox, Err: err}
			return
		}
		for _, f := range []struct {
			fn   any
			name string
		}{
			{&at.newConverter, "AudioConverterNew"},
			{&at.fill, "AudioConverterFillComplexBuffer"},
			{&at.getProperty, "AudioConverterGetProperty"},
			{&at.setProperty, "AudioConverterSetProperty"},
			{&at.dispose, "AudioConverterDispose"},
		} {
			sym, err := purego.Dlsym(lib, f.name)
			if err != nil {
				at.err = &UnavailableError{What: "Opus", Path: audioToolbox, Err: err}
				return
			}
			purego.RegisterFunc(f.fn, sym)
		}
		at.encodeInput = purego.NewCallback(encodeInput)
		at.decodeInput = purego.NewCallback(decodeInput)
	})
	return at.err
}

// jobs are the conversions in progress, by the id their callbacks get.
var (
	jobs   sync.Map // uintptr → *encodeJob or *decodeJob
	nextID atomic.Uintptr
)

// osStatus is an AudioToolbox error, its four characters when it has them.
type osStatus int32

func (s osStatus) Error() string {
	u := uint32(s)
	b := []byte{byte(u >> 24), byte(u >> 16), byte(u >> 8), byte(u)}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return fmt.Sprintf("OSStatus %d", int32(s))
		}
	}
	return fmt.Sprintf("OSStatus '%s'", b)
}

func pcmFormat(channels int) streamFormat {
	return streamFormat{
		SampleRate: Rate, FormatID: formatLPCM, FormatFlags: floatPacked,
		BytesPerPacket: uint32(4 * channels), FramesPerPacket: 1, BytesPerFrame: uint32(4 * channels),
		Channels: uint32(channels), Bits: 32,
	}
}

// atEncoder is a mono Opus encoder, 20 ms packets.
type atEncoder struct {
	conv uintptr
	pre  int
	max  int
}

func newOpusEncoder() (opusEncoder, error) {
	if err := loadAudioToolbox(); err != nil {
		return nil, err
	}
	in := pcmFormat(1)
	out := streamFormat{SampleRate: Rate, FormatID: formatOpus, FramesPerPacket: frame, Channels: 1}
	var conv uintptr
	if st := at.newConverter(&in, &out, &conv); st != 0 {
		return nil, &UnavailableError{What: "Opus", Err: fmt.Errorf("the encoder: %w", osStatus(st))}
	}
	e := &atEncoder{conv: conv}
	rate := uint32(bitrate)
	if st := at.setProperty(conv, propBitRate, 4, unsafe.Pointer(&rate)); st != 0 {
		e.close()
		return nil, fmt.Errorf("av: setting the Opus bit rate: %w", osStatus(st))
	}
	var prime struct{ leading, trailing uint32 }
	size := uint32(unsafe.Sizeof(prime))
	if st := at.getProperty(conv, propPrimeInfo, &size, unsafe.Pointer(&prime)); st != 0 {
		e.close()
		return nil, fmt.Errorf("av: reading the Opus lookahead: %w", osStatus(st))
	}
	e.pre = int(prime.leading)
	var maxPacket uint32
	size = 4
	if st := at.getProperty(conv, propMaxPacket, &size, unsafe.Pointer(&maxPacket)); st != 0 || maxPacket == 0 {
		maxPacket = 1500
	}
	e.max = int(maxPacket)
	return e, nil
}

func (e *atEncoder) preSkip() int { return e.pre }

func (e *atEncoder) close() {
	if e.conv != 0 {
		at.dispose(e.conv)
		e.conv = 0
	}
}

// encodeJob is one encode's input: the samples, and how far the converter
// has read them.
type encodeJob struct {
	samples []float32
	at      int
}

// encodeInput is the encoder's AudioConverterComplexInputDataProc: the next
// samples, as many as asked for; none at the end.
func encodeInput(_ uintptr, packets *uint32, data *bufferList, _ **packetDesc, id uintptr) int32 {
	v, ok := jobs.Load(id)
	if !ok {
		*packets = 0
		return -1
	}
	j := v.(*encodeJob)
	n := min(int(*packets), len(j.samples)-j.at)
	data.N, data.Channels = 1, 1
	if n <= 0 {
		*packets, data.Size, data.Data = 0, 0, nil
		return 0
	}
	data.Data = unsafe.Pointer(&j.samples[j.at])
	data.Size = uint32(4 * n)
	*packets = uint32(n)
	j.at += n
	return 0
}

func (e *atEncoder) encode(samples []float32, packet func([]byte) error) error {
	if len(samples) == 0 {
		samples = make([]float32, 1) // one packet of silence, as upstream gives
	}
	j := &encodeJob{samples: samples}
	var pin runtime.Pinner
	defer pin.Unpin()
	pin.Pin(&samples[0])
	const batch = 64
	buf := make([]byte, batch*e.max)
	descs := make([]packetDesc, batch)
	pin.Pin(&buf[0])
	pin.Pin(&descs[0])
	id := nextID.Add(1)
	jobs.Store(id, j)
	defer jobs.Delete(id)
	for {
		out := bufferList{N: 1, Channels: 1, Size: uint32(len(buf)), Data: unsafe.Pointer(&buf[0])}
		n := uint32(batch)
		if st := at.fill(e.conv, at.encodeInput, id, &n, &out, &descs[0]); st != 0 {
			return fmt.Errorf("av: Opus encoding: %w", osStatus(st))
		}
		if n == 0 {
			return nil
		}
		for _, d := range descs[:n] {
			end := d.Start + int64(d.Size)
			if d.Start < 0 || end > int64(out.Size) {
				return errors.New("av: Opus encoding: a packet out of its buffer")
			}
			if err := packet(buf[d.Start:end]); err != nil {
				return err
			}
		}
	}
}

// atDecoder decodes mono or stereo Opus to interleaved float32.
type atDecoder struct {
	conv     uintptr
	channels int
}

func newOpusDecoder(channels int) (opusDecoder, error) {
	if err := loadAudioToolbox(); err != nil {
		return nil, err
	}
	in := streamFormat{SampleRate: Rate, FormatID: formatOpus, Channels: uint32(channels)}
	out := pcmFormat(channels)
	var conv uintptr
	if st := at.newConverter(&in, &out, &conv); st != 0 {
		return nil, &UnavailableError{What: "Opus", Err: fmt.Errorf("the decoder: %w", osStatus(st))}
	}
	return &atDecoder{conv: conv, channels: channels}, nil
}

func (d *atDecoder) lead() int { return atDecoderLead }

func (d *atDecoder) close() {
	if d.conv != 0 {
		at.dispose(d.conv)
		d.conv = 0
	}
}

// decodeJob is one decode's input: the packets, laid end to end in arena,
// and the next to hand over.
type decodeJob struct {
	arena []byte
	ends  []int
	next  int
	desc  packetDesc // the description of the packet handed over last
}

// decodeInput is the decoder's input proc: one packet at a time.
func decodeInput(_ uintptr, packets *uint32, data *bufferList, desc **packetDesc, id uintptr) int32 {
	v, ok := jobs.Load(id)
	if !ok {
		*packets = 0
		return -1
	}
	j := v.(*decodeJob)
	data.N, data.Channels = 1, 0
	if j.next >= len(j.ends) {
		*packets, data.Size, data.Data = 0, 0, nil
		return 0
	}
	start := 0
	if j.next > 0 {
		start = j.ends[j.next-1]
	}
	end := j.ends[j.next]
	j.next++
	data.Data = unsafe.Pointer(&j.arena[start])
	data.Size = uint32(end - start)
	j.desc = packetDesc{Size: uint32(end - start)}
	if desc != nil {
		*desc = &j.desc
	}
	*packets = 1
	return 0
}

func (d *atDecoder) decode(dst []float32, packets [][]byte) ([]float32, error) {
	if len(packets) == 0 {
		return dst, nil
	}
	j := &decodeJob{ends: make([]int, len(packets))}
	for i, p := range packets {
		j.arena = append(j.arena, p...)
		j.ends[i] = len(j.arena)
	}
	var pin runtime.Pinner
	defer pin.Unpin()
	pin.Pin(&j.arena[0])
	pin.Pin(j) // the packet description C keeps between callbacks
	buf := make([]float32, Rate/4*d.channels)
	pin.Pin(&buf[0])
	id := nextID.Add(1)
	jobs.Store(id, j)
	defer jobs.Delete(id)
	for {
		out := bufferList{N: 1, Channels: uint32(d.channels), Size: uint32(4 * len(buf)), Data: unsafe.Pointer(&buf[0])}
		n := uint32(len(buf) / d.channels)
		if st := at.fill(d.conv, at.decodeInput, id, &n, &out, nil); st != 0 {
			return nil, malformed(fmt.Sprintf("a damaged Opus packet (%v)", osStatus(st)))
		}
		if n == 0 {
			return dst, nil
		}
		dst = append(dst, buf[:int(n)*d.channels]...)
	}
}
