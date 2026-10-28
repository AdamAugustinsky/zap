// Package audio is a call's microphone and speaker: what upstream Zap's
// call_audio.rs does on fastframe-audio, on CoreAudio.
//
// The engine runs a 16 kHz mono clock in 960-sample (60 ms) int16 frames
// ([Rate], [FrameSamples]). [Open] opens the picked devices (or the
// system's defaults) at whatever rate and channels each wants and converts
// both ways ([Resampler], a windowed sinc that neither drifts nor aliases).
// The engine reads [Audio.Microphone] and writes [Audio.Play]; the channel
// and the [Playout] buffer behind them are made once, so a device picked
// ([Audio.SetMicrophone], [Audio.SetSpeaker]), unplugged, or restarted
// because it stopped moving changes nothing the engine sees. A picked
// device that is gone, will not open, or never moves is given up for the
// default, and [Audio.FellBack] says so. With [Config.EchoCancel] both run
// through macOS's voice-processing I/O unit (echo cancellation, noise
// suppression, gain control) when it opens.
//
// [Microphones], [Speakers] and [WatchDevices] are the UI's CallDevices
// list. Everything is reached at run time with purego: nothing needs cgo.
// Off macOS, Open and the lists fail with [ErrUnavailable]; a CoreAudio
// call that fails is a [*StatusError].
package audio
