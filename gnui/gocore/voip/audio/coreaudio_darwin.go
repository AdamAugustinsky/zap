//go:build darwin

package audio

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// CoreAudio through purego: the HAL's device list (AudioObject), and an
// AudioUnit a stream: AUHAL ('ahal') for one device, the voice-processing
// I/O unit ('vpio') for both with echo cancellation. Each stream asks for
// interleaved float32 at the device's own rate and channels (16 kHz mono
// from the voice-processing unit, which converts itself), and the rest is
// convert.go's. The three callbacks are made once and find their stream
// by an id, so no Go pointer is handed to C as user data.

const (
	pathAudioToolbox = "/System/Library/Frameworks/AudioToolbox.framework/AudioToolbox"
	pathCoreAudio    = "/System/Library/Frameworks/CoreAudio.framework/CoreAudio"
	pathCF           = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
)

func fourCC(s string) uint32 {
	return uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3])
}

// AudioObjectPropertyAddress.
type propAddr struct {
	Selector, Scope, Element uint32
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

// AudioComponentDescription.
type componentDesc struct {
	Type, SubType, Manufacturer, Flags, FlagsMask uint32
}

// AURenderCallbackStruct.
type renderCallback struct {
	proc, refcon uintptr
}

const systemObject = 1 // kAudioObjectSystemObject

var (
	selDevices       = fourCC("dev#")
	selDefaultInput  = fourCC("dIn ")
	selDefaultOutput = fourCC("dOut")
	selStreams       = fourCC("stm#")
	selUID           = fourCC("uid ")
	selName          = fourCC("lnam")
	scopeGlobal      = fourCC("glob")
	scopeInput       = fourCC("inpt")
	scopeOutput      = fourCC("outp")

	unitOutput = fourCC("auou")
	unitHAL    = fourCC("ahal")
	unitVPIO   = fourCC("vpio")
	unitApple  = fourCC("appl")
	formatLPCM = fourCC("lpcm")
)

// AudioUnit properties and scopes.
const (
	propStreamFormat  = 8
	propMaxFrames     = 14
	propRenderCB      = 23
	propCurrentDevice = 2000
	propEnableIO      = 2003
	propInputCB       = 2005

	auGlobal = 0
	auInput  = 1
	auOutput = 2

	// kAudioFormatFlagIsFloat | kAudioFormatFlagIsPacked
	floatPacked = 1 | 8
	utf8        = 0x08000100 // kCFStringEncodingUTF8
)

var ca struct {
	once sync.Once
	err  error

	getSize   func(obj uint32, addr *propAddr, qsize uint32, q unsafe.Pointer, size *uint32) int32
	getData   func(obj uint32, addr *propAddr, qsize uint32, q unsafe.Pointer, size *uint32, data unsafe.Pointer) int32
	addListen func(obj uint32, addr *propAddr, proc, data uintptr) int32
	rmListen  func(obj uint32, addr *propAddr, proc, data uintptr) int32

	find       func(after uintptr, desc *componentDesc) uintptr
	newUnit    func(comp uintptr, out *uintptr) int32
	dispose    func(unit uintptr) int32
	setProp    func(unit uintptr, id, scope, element uint32, data unsafe.Pointer, size uint32) int32
	getProp    func(unit uintptr, id, scope, element uint32, data unsafe.Pointer, size *uint32) int32
	initialize func(unit uintptr) int32
	uninit     func(unit uintptr) int32
	startUnit  func(unit uintptr) int32
	stopUnit   func(unit uintptr) int32
	renderUnit func(unit, flags, ts uintptr, bus, frames uint32, data *bufferList) int32

	cfRelease   func(p uintptr)
	cfLength    func(s uintptr) int
	cfMaxSize   func(n int, enc uint32) int
	cfGetString func(s uintptr, buf unsafe.Pointer, size int, enc uint32) bool

	inputProc, renderProc, listenProc uintptr
}

func loadCoreAudio() error {
	ca.once.Do(func() { ca.err = loadCA() })
	return ca.err
}

func loadCA() error {
	bind := func(path string, fns []struct {
		fn   any
		name string
	}) error {
		lib, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_LOCAL)
		if err != nil {
			return err
		}
		for _, f := range fns {
			sym, err := purego.Dlsym(lib, f.name)
			if err != nil {
				return fmt.Errorf("%s: %w", f.name, err)
			}
			purego.RegisterFunc(f.fn, sym)
		}
		return nil
	}
	type fn = struct {
		fn   any
		name string
	}
	if err := bind(pathCoreAudio, []fn{
		{&ca.getSize, "AudioObjectGetPropertyDataSize"},
		{&ca.getData, "AudioObjectGetPropertyData"},
		{&ca.addListen, "AudioObjectAddPropertyListener"},
		{&ca.rmListen, "AudioObjectRemovePropertyListener"},
	}); err != nil {
		return &UnavailableError{What: "CoreAudio", Err: err}
	}
	if err := bind(pathAudioToolbox, []fn{
		{&ca.find, "AudioComponentFindNext"},
		{&ca.newUnit, "AudioComponentInstanceNew"},
		{&ca.dispose, "AudioComponentInstanceDispose"},
		{&ca.setProp, "AudioUnitSetProperty"},
		{&ca.getProp, "AudioUnitGetProperty"},
		{&ca.initialize, "AudioUnitInitialize"},
		{&ca.uninit, "AudioUnitUninitialize"},
		{&ca.startUnit, "AudioOutputUnitStart"},
		{&ca.stopUnit, "AudioOutputUnitStop"},
		{&ca.renderUnit, "AudioUnitRender"},
	}); err != nil {
		return &UnavailableError{What: "AudioToolbox", Err: err}
	}
	if err := bind(pathCF, []fn{
		{&ca.cfRelease, "CFRelease"},
		{&ca.cfLength, "CFStringGetLength"},
		{&ca.cfMaxSize, "CFStringGetMaximumSizeForEncoding"},
		{&ca.cfGetString, "CFStringGetCString"},
	}); err != nil {
		return &UnavailableError{What: "CoreFoundation", Err: err}
	}
	ca.inputProc = purego.NewCallback(inputProc)
	ca.renderProc = purego.NewCallback(renderProc)
	ca.listenProc = purego.NewCallback(listenProc)
	return nil
}

// coreAudio is the system's backend.
type coreAudio struct{}

func system() (backend, error) {
	if err := loadCoreAudio(); err != nil {
		return nil, err
	}
	return coreAudio{}, nil
}

// goString copies a CFString and releases it.
func goString(s uintptr) string {
	if s == 0 {
		return ""
	}
	defer ca.cfRelease(s)
	n := ca.cfMaxSize(ca.cfLength(s), utf8) + 1
	buf := make([]byte, n)
	if !ca.cfGetString(s, unsafe.Pointer(&buf[0]), n, utf8) {
		return ""
	}
	if i := strings.IndexByte(string(buf), 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf)
}

// cfProp reads a CFString property of an object.
func cfProp(obj uint32, sel, scope uint32) string {
	var s uintptr
	size := uint32(unsafe.Sizeof(s))
	addr := propAddr{sel, scope, 0}
	if ca.getData(obj, &addr, 0, nil, &size, unsafe.Pointer(&s)) != 0 {
		return ""
	}
	return goString(s)
}

// deviceIDs are the HAL's devices.
func deviceIDs() ([]uint32, error) {
	addr := propAddr{selDevices, scopeGlobal, 0}
	var size uint32
	if st := ca.getSize(systemObject, &addr, 0, nil, &size); st != 0 {
		return nil, &StatusError{Op: "AudioObjectGetPropertyDataSize(devices)", Status: st}
	}
	ids := make([]uint32, size/4)
	if len(ids) == 0 {
		return nil, nil
	}
	if st := ca.getData(systemObject, &addr, 0, nil, &size, unsafe.Pointer(&ids[0])); st != 0 {
		return nil, &StatusError{Op: "AudioObjectGetPropertyData(devices)", Status: st}
	}
	return ids[:size/4], nil
}

// hasStreams says the device has streams in scope (it is an input, or an
// output).
func hasStreams(dev uint32, scope uint32) bool {
	addr := propAddr{selStreams, scope, 0}
	var size uint32
	return ca.getSize(dev, &addr, 0, nil, &size) == 0 && size > 0
}

func scopeOf(input bool) uint32 {
	if input {
		return scopeInput
	}
	return scopeOutput
}

// hidden is the aggregate the voice-processing unit makes of the devices
// it runs on: not a device to pick.
func hidden(uid string) bool { return strings.HasPrefix(uid, "CADefaultDeviceAggregate") }

func (coreAudio) devices(input bool) ([]Device, error) {
	ids, err := deviceIDs()
	if err != nil {
		return nil, err
	}
	var out []Device
	for _, id := range ids {
		if !hasStreams(id, scopeOf(input)) {
			continue
		}
		uid := cfProp(id, selUID, scopeGlobal)
		if uid == "" || hidden(uid) {
			continue
		}
		name := strings.TrimSpace(cfProp(id, selName, scopeGlobal))
		if name == "" {
			name = map[bool]string{true: "Microphone", false: "Speaker"}[input]
		}
		out = append(out, Device{ID: uid, Label: name})
	}
	return out, nil
}

// defaultID is the default input's or output's AudioDeviceID.
func defaultID(input bool) (uint32, error) {
	sel := selDefaultOutput
	if input {
		sel = selDefaultInput
	}
	addr := propAddr{sel, scopeGlobal, 0}
	var dev uint32
	size := uint32(4)
	if st := ca.getData(systemObject, &addr, 0, nil, &size, unsafe.Pointer(&dev)); st != 0 {
		return 0, &StatusError{Op: "AudioObjectGetPropertyData(default device)", Status: st}
	}
	if dev == 0 {
		return 0, ErrNoDevice
	}
	return dev, nil
}

func (coreAudio) defaultDevice(input bool) (string, error) {
	dev, err := defaultID(input)
	if err != nil {
		return "", err
	}
	return cfProp(dev, selUID, scopeGlobal), nil
}

// resolve is the AudioDeviceID and UID of device uid, the default's when
// uid is empty.
func resolve(uid string, input bool) (uint32, string, error) {
	if uid == "" {
		dev, err := defaultID(input)
		if err != nil {
			return 0, "", err
		}
		return dev, cfProp(dev, selUID, scopeGlobal), nil
	}
	ids, err := deviceIDs()
	if err != nil {
		return 0, "", err
	}
	for _, id := range ids {
		if hasStreams(id, scopeOf(input)) && cfProp(id, selUID, scopeGlobal) == uid {
			return id, uid, nil
		}
	}
	return 0, "", fmt.Errorf("%w: %q", ErrNoDevice, uid)
}

// streams are the open units whose callbacks may run, by the id they get.
var (
	streams sync.Map // uintptr → *auStream
	nextID  atomic.Uintptr
)

// auStream is one open AudioUnit.
type auStream struct {
	id   uintptr
	unit uintptr
	ids  [2]string

	mu      sync.Mutex // held by a callback while it runs, and by close
	closed  bool
	in, out Format
	deliver func([]float32, Format)
	fill    func([]float32, Format)

	buf  []float32 // the microphone's render buffer, pinned
	list bufferList
	pin  runtime.Pinner
}

func (s *auStream) devices() [2]string { return s.ids }

func (s *auStream) close() {
	ca.stopUnit(s.unit)
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	streams.Delete(s.id)
	ca.uninit(s.unit)
	ca.dispose(s.unit)
	s.pin.Unpin()
}

func floatFormat(f Format) streamFormat {
	bpf := uint32(4 * f.Channels)
	return streamFormat{
		SampleRate: float64(f.Rate), FormatID: formatLPCM, FormatFlags: floatPacked,
		BytesPerPacket: bpf, FramesPerPacket: 1, BytesPerFrame: bpf, Channels: uint32(f.Channels), Bits: 32,
	}
}

func newUnit(sub uint32) (uintptr, error) {
	desc := componentDesc{Type: unitOutput, SubType: sub, Manufacturer: unitApple}
	comp := ca.find(0, &desc)
	if comp == 0 {
		return 0, &UnavailableError{What: fmt.Sprintf("the %q audio unit", []byte{byte(sub >> 24), byte(sub >> 16), byte(sub >> 8), byte(sub)})}
	}
	var unit uintptr
	if st := ca.newUnit(comp, &unit); st != 0 {
		return 0, &StatusError{Op: "AudioComponentInstanceNew", Status: st}
	}
	return unit, nil
}

// unitSetup is a unit being made: the first failure is kept, and the unit
// disposed of when it is not handed on.
type unitSetup struct {
	unit uintptr
	err  error
}

func (u *unitSetup) set(op string, id, scope, element uint32, data unsafe.Pointer, size uintptr) {
	if u.err != nil {
		return
	}
	if st := ca.setProp(u.unit, id, scope, element, data, uint32(size)); st != 0 {
		u.err = &StatusError{Op: op, Status: st}
	}
}

func (u *unitSetup) u32(op string, id, scope, element, v uint32) {
	u.set(op, id, scope, element, unsafe.Pointer(&v), 4)
}

// hwFormat is the device side's format of element in scope.
func (u *unitSetup) hwFormat(scope, element uint32) Format {
	if u.err != nil {
		return Format{}
	}
	var f streamFormat
	size := uint32(unsafe.Sizeof(f))
	if st := ca.getProp(u.unit, propStreamFormat, scope, element, unsafe.Pointer(&f), &size); st != 0 {
		u.err = &StatusError{Op: "AudioUnitGetProperty(StreamFormat)", Status: st}
		return Format{}
	}
	if f.SampleRate <= 0 || f.Channels == 0 {
		u.err = &StatusError{Op: "AudioUnitGetProperty(StreamFormat): no format", Status: -1}
		return Format{}
	}
	return Format{Rate: int(f.SampleRate + 0.5), Channels: int(f.Channels)}
}

func (u *unitSetup) clientFormat(scope, element uint32, f Format) {
	sf := floatFormat(f)
	u.set("AudioUnitSetProperty(StreamFormat)", propStreamFormat, scope, element, unsafe.Pointer(&sf), unsafe.Sizeof(sf))
}

func (u *unitSetup) callback(id, scope, element uint32, proc, refcon uintptr) {
	cb := renderCallback{proc: proc, refcon: refcon}
	u.set("AudioUnitSetProperty(callback)", id, scope, element, unsafe.Pointer(&cb), unsafe.Sizeof(cb))
}

// maxFrames is the most frames a callback is handed.
func (u *unitSetup) maxFrames() int {
	var n uint32
	size := uint32(4)
	if ca.getProp(u.unit, propMaxFrames, auGlobal, 0, unsafe.Pointer(&n), &size) != 0 || n == 0 {
		n = 4096
	}
	return int(n)
}

// start initialises and starts the unit and registers s; on failure the
// unit is disposed of.
func (u *unitSetup) start(s *auStream) (stream, error) {
	if u.err == nil {
		if st := ca.initialize(u.unit); st != 0 {
			u.err = &StatusError{Op: "AudioUnitInitialize", Status: st}
		}
	}
	if u.err == nil {
		streams.Store(s.id, s)
		if st := ca.startUnit(u.unit); st != 0 {
			streams.Delete(s.id)
			ca.uninit(u.unit)
			u.err = &StatusError{Op: "AudioOutputUnitStart", Status: st}
		}
	}
	if u.err != nil {
		ca.dispose(u.unit)
		s.pin.Unpin()
		return nil, u.err
	}
	return s, nil
}

// micBuffer sizes and pins s's render buffer for frames of f.
func (s *auStream) micBuffer(frames int, f Format) {
	s.buf = make([]float32, frames*max(f.Channels, 1))
	s.pin.Pin(&s.buf[0])
}

func (coreAudio) openInput(uid string, deliver func([]float32, Format)) (stream, error) {
	dev, resolved, err := resolve(uid, true)
	if err != nil {
		return nil, err
	}
	unit, err := newUnit(unitHAL)
	if err != nil {
		return nil, err
	}
	s := &auStream{id: nextID.Add(1), unit: unit, deliver: deliver}
	s.ids[Microphone] = resolved
	u := &unitSetup{unit: unit}
	u.u32("EnableIO(input)", propEnableIO, auInput, 1, 1)
	u.u32("EnableIO(output)", propEnableIO, auOutput, 0, 0)
	u.u32("CurrentDevice", propCurrentDevice, auGlobal, 0, dev)
	s.in = u.hwFormat(auInput, 1)
	u.clientFormat(auOutput, 1, s.in)
	u.callback(propInputCB, auGlobal, 0, ca.inputProc, s.id)
	if u.err == nil {
		s.micBuffer(u.maxFrames(), s.in)
	}
	return u.start(s)
}

func (coreAudio) openOutput(uid string, fill func([]float32, Format)) (stream, error) {
	dev, resolved, err := resolve(uid, false)
	if err != nil {
		return nil, err
	}
	unit, err := newUnit(unitHAL)
	if err != nil {
		return nil, err
	}
	s := &auStream{id: nextID.Add(1), unit: unit, fill: fill}
	s.ids[Speaker] = resolved
	u := &unitSetup{unit: unit}
	u.u32("CurrentDevice", propCurrentDevice, auGlobal, 0, dev)
	s.out = u.hwFormat(auOutput, 0)
	u.clientFormat(auInput, 0, s.out)
	u.callback(propRenderCB, auInput, 0, ca.renderProc, s.id)
	return u.start(s)
}

// voiceFormat is what the voice-processing unit is asked for on both
// sides: the engine's own, which it converts to and from itself.
var voiceFormat = Format{Rate: Rate, Channels: 1}

func (coreAudio) openDuplex(inUID, outUID string, deliver, fill func([]float32, Format)) (stream, error) {
	inDev, inID, err := resolve(inUID, true)
	if err != nil {
		return nil, err
	}
	outDev, outID, err := resolve(outUID, false)
	if err != nil {
		return nil, err
	}
	unit, err := newUnit(unitVPIO)
	if err != nil {
		return nil, err
	}
	s := &auStream{id: nextID.Add(1), unit: unit, deliver: deliver, fill: fill,
		in: voiceFormat, out: voiceFormat, ids: [2]string{inID, outID}}
	u := &unitSetup{unit: unit}
	u.u32("EnableIO(input)", propEnableIO, auInput, 1, 1)
	u.u32("EnableIO(output)", propEnableIO, auOutput, 0, 1)
	u.u32("CurrentDevice(input)", propCurrentDevice, auGlobal, 1, inDev)
	u.u32("CurrentDevice(output)", propCurrentDevice, auGlobal, 0, outDev)
	u.clientFormat(auOutput, 1, voiceFormat)
	u.clientFormat(auInput, 0, voiceFormat)
	u.callback(propInputCB, auGlobal, 1, ca.inputProc, s.id)
	u.callback(propRenderCB, auInput, 0, ca.renderProc, s.id)
	if u.err == nil {
		s.micBuffer(u.maxFrames(), voiceFormat)
	}
	return u.start(s)
}

func lookup(id uintptr) *auStream {
	v, ok := streams.Load(id)
	if !ok {
		return nil
	}
	return v.(*auStream)
}

// inputProc is the microphone's AURenderCallback: the unit renders the
// device's samples into the stream's buffer, which goes to deliver.
func inputProc(refcon, flags, ts, bus, frames, _ uintptr) int32 {
	s := lookup(refcon)
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.deliver == nil {
		return 0
	}
	ch := max(s.in.Channels, 1)
	n := int(uint32(frames))
	if n*ch > len(s.buf) {
		n = len(s.buf) / ch
	}
	s.list = bufferList{N: 1, Channels: uint32(ch), Size: uint32(4 * n * ch), Data: unsafe.Pointer(&s.buf[0])}
	if st := ca.renderUnit(s.unit, flags, ts, uint32(bus), uint32(n), &s.list); st != 0 {
		return st
	}
	got := min(int(s.list.Size)/4, n*ch)
	s.deliver(s.buf[:got], s.in)
	return 0
}

// renderProc is the speaker's AURenderCallback: fill writes the buffer
// the unit hands over.
func renderProc(refcon, _, _, _, frames, data uintptr) int32 {
	s := lookup(refcon)
	if s == nil || data == 0 {
		return 0
	}
	list := *(**bufferList)(unsafe.Pointer(&data))
	if list.N == 0 || list.Data == nil {
		return 0
	}
	out := unsafe.Slice((*float32)(list.Data), list.Size/4)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.fill == nil {
		clear(out)
		return 0
	}
	n := min(int(uint32(frames))*max(s.out.Channels, 1), len(out))
	s.fill(out[:n], s.out)
	clear(out[n:])
	return 0
}

// watchers are the device watches, by the id their listener gets.
var watchers sync.Map // uintptr → func()

var watched = []propAddr{
	{selDevices, scopeGlobal, 0},
	{selDefaultInput, scopeGlobal, 0},
	{selDefaultOutput, scopeGlobal, 0},
}

func (coreAudio) watch(changed func()) func() {
	id := nextID.Add(1)
	watchers.Store(id, changed)
	for i := range watched {
		ca.addListen(systemObject, &watched[i], ca.listenProc, id)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for i := range watched {
				ca.rmListen(systemObject, &watched[i], ca.listenProc, id)
			}
			watchers.Delete(id)
		})
	}
}

// listenProc is the AudioObjectPropertyListenerProc.
func listenProc(_, _, _, data uintptr) int32 {
	if v, ok := watchers.Load(data); ok {
		v.(func())()
	}
	return 0
}
