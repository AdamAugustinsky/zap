//! The media host: whatsapp-rust's engine for one 1:1 call, without its
//! `Client`.
//!
//! Go's signaling (gnui/gocore/calls) hands over what upstream's facade
//! would have learnt from the socket: the plaintext callKey, the `<relay>`
//! node, both LIDs and the codec. From there this is the facade's resident
//! backend (`src/voip_control/wacore_backend.rs` in whatsapp-rust) with the
//! client taken out: the spec is built (`MediaSessionSpec::from_relay`), the
//! engine made (`CallEngine::new`), the relay dialled
//! (`RelayMediaChannelFactory`) and the call driven (`run_call`), while
//! upstream Zap's own call media does the rest: the microphone and speaker
//! (`call_audio`), the camera, the H.264 encoder and the peer's decoder
//! (`calls::VideoPipeline`).
//!
//! One task owns a call: the commands, the engine's events and the video
//! frames all reach it, and it releases every device on the way out.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use whatsapp_rust::voip::transport::{RandTxIds, RelayMediaChannelFactory};
use whatsapp_rust::wacore::voip::driver::{
    CallChannels, VideoControl, VideoControlSender, video_control_channel,
};
use whatsapp_rust::wacore::voip::engine::CallEngine;
use whatsapp_rust::wacore::voip::{ForeignAudioCodec, ForeignCodecError, VideoFrame};
use whatsapp_rust::wacore::voip_control::control::PeerAnswer;
use whatsapp_rust::wacore::voip_control::relay_parse::{find_relay, parse_relay_data};
use whatsapp_rust::wacore::voip_control::transport::{RelayEndpointParams, RelayTransportFactory};
use whatsapp_rust::wacore::voip_control::{
    CallDirection, CallEvent, MediaAudioCodec, MediaAudioFormat, MediaCloseReason,
    MediaKeyframeUrgency, MediaSessionKey, MediaSessionSpec, MediaSilenceReason, VideoInput,
};
use whatsapp_rust::wacore_binary::marshal::unmarshal_packed_ref;
use whatsapp_rust::TokioRuntime;
use zapfast::call_audio::{AudioInput, AudioOutput};
use zapfast::calls::{VideoPipeline, VideoTick};

use crate::frames::{self, Frames};
use crate::wire::{Command, Event, Kind, Open};

/// The engine's 60 ms mono 16 kHz frame; a muted frame is zeroed at this length only.
const FRAME_SAMPLES: usize = 960;
/// How long the relay dial may take, above the native dialer's own 12 s
/// (whatsapp-rust's `RELAY_DIAL_CEILING`).
const DIAL_CEILING: Duration = Duration::from_secs(20);
/// The facade's video queues.
const VIDEO_IN_CAPACITY: usize = 4;
const VIDEO_OUT_CAPACITY: usize = 8;
/// How often the camera's queue and health are checked.
const HEARTBEAT: Duration = Duration::from_secs(1);

/// Everything a call's media needs that can be checked before anything is
/// opened: a bad relay or key fails `zc_open` itself.
pub struct Prepared {
    engine: CallEngine,
    endpoint: RelayEndpointParams,
}

/// Builds the engine from Go's parameters, as the facade's `build_spec` and
/// the resident backend's `build_engine` do.
pub fn prepare(p: &Open) -> Result<Prepared, String> {
    let node = unmarshal_packed_ref(&p.relay).map_err(|e| format!("the relay node: {e}"))?;
    let relay = find_relay(&node)
        .and_then(parse_relay_data)
        .ok_or("the node carries no <relay>")?;
    let key = MediaSessionKey::builder()
        .call_id(p.call_id.clone())
        .generation(p.generation)
        .build();
    let direction = if p.outgoing {
        CallDirection::Outgoing
    } else {
        CallDirection::Incoming
    };
    let mut spec = MediaSessionSpec::from_relay(
        direction,
        key,
        &p.self_lid,
        &p.peer_lid,
        p.call_key.clone(),
        &relay,
    )
    .map_err(|e| format!("the call's media: {e}"))?;
    spec.audio.format = if p.mlow {
        MediaAudioFormat::MLOW_16KHZ_60MS
    } else {
        MediaAudioFormat::OPUS_16KHZ_60MS
    };
    spec.enable_video = p.video;
    let endpoint = RelayEndpointParams::from_spec(&spec).ok_or("the relay's address")?;
    let parts = whatsapp_rust::wacore::voip_control::engine_bridge::into_engine_parts(spec)
        .map_err(|e| format!("the call's media: {e}"))?;
    let engine = CallEngine::new(parts.config, Box::new(RandTxIds))
        .map_err(|e| format!("the media engine: {e}"))?;
    Ok(Prepared {
        engine: with_opus(engine),
        endpoint,
    })
}

/// Gives the engine standard Opus (the facade's `with_platform_audio_codec`),
/// so a peer that speaks it is heard. whatsapp-rust keeps its own codec
/// crate-private, so this is the same thing over its public encoder and
/// decoder.
fn with_opus(engine: CallEngine) -> CallEngine {
    use whatsapp_rust::voip::audio::{WaOpusDecoder, WaOpusEncoder};
    struct Opus {
        encoder: WaOpusEncoder,
        decoder: WaOpusDecoder,
    }
    impl ForeignAudioCodec for Opus {
        fn decode(&mut self, payload: &[u8], out: &mut Vec<i16>) -> Result<(), ForeignCodecError> {
            let pcm = self
                .decoder
                .decode(payload)
                .map_err(|_| ForeignCodecError::InvalidPayload)?;
            out.extend_from_slice(pcm);
            Ok(())
        }
        fn conceal(&mut self, samples: usize, out: &mut Vec<i16>) {
            out.resize(out.len() + samples, 0);
        }
        fn encode(&mut self, pcm: &[i16], out: &mut Vec<u8>) -> Result<(), ForeignCodecError> {
            let payload = self
                .encoder
                .encode(pcm)
                .map_err(|_| ForeignCodecError::BadFrameSize)?;
            out.extend_from_slice(&payload);
            Ok(())
        }
    }
    match (WaOpusEncoder::new(), WaOpusDecoder::new()) {
        (Ok(encoder), Ok(decoder)) => {
            engine.with_foreign_audio_codec(Box::new(Opus { encoder, decoder }))
        }
        _ => {
            log::warn!("zfcalls: libopus is unavailable; a peer on Opus will not be heard");
            engine
        }
    }
}

/// The one call a context has at a time.
pub struct Live {
    pub generation: u64,
    commands: async_channel::Sender<Option<Command>>,
    /// Set once the task said Ended: the call is over and a new one may open.
    done: Arc<AtomicBool>,
}

impl Live {
    /// Spawns the call's task on `rt`.
    pub fn start(
        rt: &tokio::runtime::Runtime,
        p: Open,
        prepared: Prepared,
        events: async_channel::Sender<Event>,
        frames: Arc<Frames>,
    ) -> Self {
        let generation = p.generation;
        let (commands, inbox) = async_channel::unbounded();
        let done = Arc::new(AtomicBool::new(false));
        let finished = Arc::clone(&done);
        frames.begin(generation);
        rt.spawn(async move {
            use whatsapp_rust::futures::FutureExt as _;
            let out = Out { generation, events };
            let run = std::panic::AssertUnwindSafe(run(p, prepared, inbox, out.clone(), frames.clone()));
            if run.catch_unwind().await.is_err() {
                out.emit(Kind::Failed {
                    reason: "the call's media stopped unexpectedly".into(),
                });
            }
            frames.end(generation);
            // Before Ended, so a caller that saw it may open the next call.
            finished.store(true, Ordering::Release);
            out.emit(Kind::Ended);
        });
        Self {
            generation,
            commands,
            done,
        }
    }

    /// Whether the call's media is over (it failed or closed on its own).
    pub fn finished(&self) -> bool {
        self.done.load(Ordering::Acquire)
    }

    pub fn command(&self, command: Command) {
        let _ = self.commands.try_send(Some(command));
    }

    /// Ends the call's media; its task releases every device and says Ended.
    pub fn close(&self) {
        let _ = self.commands.try_send(None);
        self.commands.close();
    }
}

#[derive(Clone)]
struct Out {
    generation: u64,
    events: async_channel::Sender<Event>,
}

impl Out {
    fn emit(&self, kind: Kind) {
        let _ = self.events.try_send(Event {
            generation: self.generation,
            kind,
        });
    }
}

/// The microphone and the speaker, or silence for a run without devices.
struct Audio {
    mic: Option<AudioInput>,
    speaker: Option<AudioOutput>,
    muted: Arc<AtomicBool>,
}

impl Audio {
    fn open(p: &Open) -> (Self, async_channel::Receiver<Vec<i16>>, async_channel::Sender<Vec<i16>>) {
        let muted = Arc::new(AtomicBool::new(false));
        let (source, speaker_tx, mic, speaker) = if p.silent {
            let (tx, rx) = async_channel::bounded::<Vec<i16>>(4);
            tokio::spawn(async move {
                let mut tick = tokio::time::interval(Duration::from_millis(60));
                loop {
                    tick.tick().await;
                    if tx.send(vec![0; FRAME_SAMPLES]).await.is_err() {
                        return;
                    }
                }
            });
            let (spk, drain) = async_channel::bounded::<Vec<i16>>(5);
            tokio::spawn(async move { while drain.recv().await.is_ok() {} });
            (rx, spk, None, None)
        } else {
            let (mic, rx) = AudioInput::spawn(p.microphone.clone());
            let (speaker, tx) = AudioOutput::spawn(p.speaker.clone());
            (rx, tx, Some(mic), Some(speaker))
        };
        // The mute feed (the backend's MuteFeed): exact-zero frames keep the stream fed, which
        // the engine turns into comfort noise, so the relay never sees a gap.
        let (to_engine, engine_mic) = async_channel::bounded::<Vec<i16>>(3);
        let flag = Arc::clone(&muted);
        tokio::spawn(async move {
            while let Ok(mut frame) = source.recv().await {
                if flag.load(Ordering::Relaxed) && frame.len() == FRAME_SAMPLES {
                    frame.fill(0);
                }
                if to_engine.send(frame).await.is_err() {
                    break;
                }
            }
        });
        (
            Self {
                mic,
                speaker,
                muted,
            },
            engine_mic,
            speaker_tx,
        )
    }

    fn stop(&mut self) {
        if let Some(mic) = self.mic.as_mut() {
            mic.stop();
        }
        if let Some(speaker) = self.speaker.as_mut() {
            speaker.stop();
        }
    }
}

/// The video direction: the facade's `VideoShared` plus upstream Zap's
/// pipeline (camera, encoder, decoder).
struct Video {
    pipe: Option<VideoPipeline>,
    ticks: Option<async_channel::Receiver<VideoTick>>,
    /// The task feeding the camera's access units to the engine.
    feed: Option<tokio::task::JoinHandle<()>>,
    input_generation: u64,
    ctl: VideoControlSender,
    timed: async_channel::Sender<VideoInput>,
    /// Where the engine's reassembled peer access units go: the decoder.
    sink: Arc<Mutex<Option<async_channel::Sender<VideoFrame>>>>,
    device: Option<String>,
    camera_wanted: bool,
}

impl Video {
    /// Starts the pipeline: the decoder, and the camera unless `camera` is off.
    async fn start(&mut self, device: Option<String>, camera: bool) -> Result<(), String> {
        self.device = device.clone();
        if self.pipe.is_none() {
            let (pipe, ticks) = VideoPipeline::start(device)
                .await
                .map_err(|e| format!("the video could not start: {e}"))?;
            *self.sink.lock().unwrap_or_else(|e| e.into_inner()) = Some({
                use whatsapp_rust::voip::VideoSink as _;
                pipe.sink().playout()
            });
            self.pipe = Some(pipe);
            self.ticks = Some(ticks);
        } else if camera {
            let pipe = self.pipe.as_mut().expect("checked");
            pipe.resume_camera(self.device.clone())
                .await
                .map_err(|e| format!("the camera could not start: {e}"))?;
        }
        if camera {
            self.attach();
            self.camera_wanted = self.camera_running();
        } else {
            self.camera_off();
        }
        Ok(())
    }

    /// Feeds the camera's access units to the engine under a fresh input
    /// generation (the facade's `attach_endpoints`).
    fn attach(&mut self) {
        use whatsapp_rust::voip::VideoSource as _;
        let Some(pipe) = &self.pipe else { return };
        if let Some(old) = self.feed.take() {
            old.abort();
        }
        let source = pipe.source();
        self.input_generation += 1;
        let generation = self.input_generation;
        self.ctl.send(VideoControl::SetInputGeneration(generation));
        self.ctl
            .send(VideoControl::SetTimestampStride(source.rtp_timestamp_stride()));
        let Some(frames) = source.timed_frames() else {
            return;
        };
        let out = self.timed.clone();
        self.feed = Some(tokio::spawn(async move {
            while let Ok(frame) = frames.recv().await {
                let input = VideoInput::builder()
                    .data(frame.data)
                    .timestamp(frame.timestamp)
                    .generation(generation)
                    .build();
                if out.send(input).await.is_err() {
                    return;
                }
            }
        }));
    }

    fn camera_running(&self) -> bool {
        self.pipe.as_ref().is_some_and(VideoPipeline::camera_running)
    }

    /// Our camera off: released, and outbound video gated off the wire.
    fn camera_off(&mut self) {
        if let Some(feed) = self.feed.take() {
            feed.abort();
        }
        if let Some(pipe) = self.pipe.as_mut() {
            pipe.pause_camera();
        }
        self.camera_wanted = false;
        self.ctl.send(VideoControl::DisableOutbound);
    }

    async fn camera_on(&mut self, device: Option<String>) -> Result<(), String> {
        if device.is_some() {
            self.device = device;
        }
        let Some(pipe) = self.pipe.as_mut() else {
            return Err("this call has no video".into());
        };
        pipe.resume_camera(self.device.clone())
            .await
            .map_err(|e| format!("the camera could not start: {e}"))?;
        self.attach();
        self.camera_wanted = true;
        self.ctl.send(VideoControl::Enable);
        Ok(())
    }

    /// Switches the camera; a running one is restarted on the new device.
    async fn switch(&mut self, device: Option<String>) -> Result<(), String> {
        self.device = device.clone();
        if self.camera_wanted {
            self.camera_on(device).await?;
        }
        Ok(())
    }

    /// The whole video plane down.
    fn stop(&mut self) {
        if let Some(feed) = self.feed.take() {
            feed.abort();
        }
        if let Some(mut pipe) = self.pipe.take() {
            pipe.shutdown();
        }
        self.ticks = None;
        self.camera_wanted = false;
        *self.sink.lock().unwrap_or_else(|e| e.into_inner()) = None;
        self.ctl.send(VideoControl::Disable);
    }
}

async fn next_tick(ticks: &Option<async_channel::Receiver<VideoTick>>) -> Option<VideoTick> {
    match ticks {
        Some(ticks) => ticks.recv().await.ok(),
        None => std::future::pending().await,
    }
}

/// The call: dial, drive, and release everything on the way out.
async fn run(
    p: Open,
    prepared: Prepared,
    inbox: async_channel::Receiver<Option<Command>>,
    out: Out,
    frames: Arc<Frames>,
) {
    let generation = p.generation;
    let Prepared { engine, endpoint } = prepared;
    let (mut audio, engine_mic, speaker) = Audio::open(&p);

    let (ctl, ctl_rx) = video_control_channel();
    let (timed, timed_rx) = async_channel::bounded::<VideoInput>(VIDEO_IN_CAPACITY);
    let (video_out, video_out_rx) = async_channel::bounded::<VideoFrame>(VIDEO_OUT_CAPACITY);
    let sink: Arc<Mutex<Option<async_channel::Sender<VideoFrame>>>> = Arc::default();
    let forward = Arc::clone(&sink);
    tokio::spawn(async move {
        while let Ok(frame) = video_out_rx.recv().await {
            let to = forward.lock().unwrap_or_else(|e| e.into_inner()).clone();
            if let Some(to) = to {
                let _ = to.try_send(frame);
            }
        }
    });
    let mut video = Video {
        pipe: None,
        ticks: None,
        feed: None,
        input_generation: 0,
        ctl,
        timed,
        sink,
        device: p.camera.clone(),
        camera_wanted: false,
    };
    // The caller's rekey (the answering device and its codec): delivered once,
    // whether the accept lands before or after the relay.
    let (rekey_tx, rekey_rx) = async_channel::bounded::<PeerAnswer>(1);

    // Commands that arrive while the relay is dialled wait for it.
    let mut waiting = Vec::new();
    let dial = async {
        let factory = RelayMediaChannelFactory::new(endpoint.addr, Arc::new(TokioRuntime));
        tokio::time::timeout(DIAL_CEILING, factory.connect()).await
    };
    tokio::pin!(dial);
    let dialed = loop {
        tokio::select! {
            dialed = &mut dial => break dialed,
            command = inbox.recv() => match command {
                Ok(Some(Command::Mute { on })) => audio.muted.store(on, Ordering::Relaxed),
                Ok(Some(command)) => waiting.push(command),
                _ => {
                    audio.stop();
                    return;
                }
            },
        }
    };
    let (transport, relay_events) = match dialed {
        Ok(Ok(pair)) => pair,
        Ok(Err(error)) => {
            out.emit(Kind::Failed {
                reason: format!("The relay could not be reached: {error}"),
            });
            audio.stop();
            return;
        }
        Err(_) => {
            out.emit(Kind::Failed {
                reason: "The relay did not answer in time".into(),
            });
            audio.stop();
            return;
        }
    };

    if p.video {
        if let Err(error) = video.start(p.camera.clone(), p.camera_on).await {
            log::warn!("zfcalls: the video call goes on without video: {error}");
        }
        video.ctl.send(VideoControl::Enable);
        if !p.camera_on {
            video.ctl.send(VideoControl::DisableOutbound);
        }
    }

    let (events, engine_events) = async_channel::bounded::<CallEvent>(64);
    let (_encoded_tx, encoded_in) = async_channel::bounded(1);
    let (encoded_out, _encoded_rx) = async_channel::bounded(1);
    let (_legacy_tx, legacy_in) = async_channel::bounded::<Vec<u8>>(1);
    let channels = CallChannels {
        mic: engine_mic,
        speaker,
        encoded_audio_in: encoded_in,
        encoded_audio_out: encoded_out,
        events,
        rekey: p.outgoing.then_some(rekey_rx),
        video_in: legacy_in,
        timed_video_in: Some(timed_rx),
        video_out,
        video_ctl: ctl_rx,
        group_ctl: None,
        media_stats: Arc::default(),
    };
    let mut drive = tokio::spawn(whatsapp_rust::wacore::voip::run_call(
        Arc::new(TokioRuntime),
        transport,
        relay_events,
        channels,
        engine,
    ));

    let mut state = State {
        call_id: p.call_id.clone(),
        generation,
        connected: false,
        remote_seen: false,
        frames: Arc::clone(&frames),
        out: out.clone(),
        rekey: rekey_tx,
    };
    for command in waiting {
        state.command(command, &mut audio, &mut video).await;
    }
    let mut heartbeat = tokio::time::interval(HEARTBEAT);
    loop {
        tokio::select! {
            command = inbox.recv() => match command {
                Ok(Some(command)) => state.command(command, &mut audio, &mut video).await,
                _ => break,
            },
            event = engine_events.recv() => match event {
                Ok(event) => state.media(&event, &mut video),
                Err(_) => {}
            },
            tick = next_tick(&video.ticks) => match tick {
                Some(tick) => state.tick(tick, &mut video),
                None => video.ticks = None,
            },
            reason = &mut drive => {
                let reason = match reason {
                    Ok(MediaCloseReason::Local) => None,
                    Ok(MediaCloseReason::RelayDisconnected) => Some("The relay connection dropped".to_owned()),
                    Ok(MediaCloseReason::SendFailed(e)) => Some(format!("The media could not be sent: {e}")),
                    Ok(MediaCloseReason::SetupFailed(e)) => Some(e),
                    Ok(other) => Some(format!("{other:?}")),
                    Err(_) => Some("the media engine stopped".to_owned()),
                };
                if let Some(reason) = reason {
                    if state.connected {
                        out.emit(Kind::Closed { reason });
                    } else {
                        out.emit(Kind::Failed { reason });
                    }
                }
                break;
            }
            _ = heartbeat.tick() => {
                if let Some(pipe) = video.pipe.as_mut() {
                    pipe.check_queue(&state.call_id, state.connected);
                }
                if video.camera_wanted && !video.camera_running() {
                    video.camera_off();
                    frames.clear(generation, frames::LOCAL);
                    out.emit(Kind::CameraStopped);
                }
            }
        }
    }
    drive.abort();
    video.stop();
    audio.stop();
}

/// The call's state between events: what the task needs besides its devices.
struct State {
    call_id: String,
    generation: u64,
    connected: bool,
    remote_seen: bool,
    frames: Arc<Frames>,
    out: Out,
    rekey: async_channel::Sender<PeerAnswer>,
}

impl State {
    async fn command(&mut self, command: Command, audio: &mut Audio, video: &mut Video) {
        let result = match command {
            Command::Mute { on } => {
                audio.muted.store(on, Ordering::Relaxed);
                Ok(())
            }
            Command::Rekey {
                answering_lid,
                codec,
            } => {
                let codec = match codec.as_deref() {
                    Some("mlow") => Some(MediaAudioCodec::Mlow),
                    Some("opus") => Some(MediaAudioCodec::Opus),
                    _ => None,
                };
                let answer = PeerAnswer::builder()
                    .answering_lid(answering_lid)
                    .maybe_audio_codec(codec)
                    .build();
                let _ = self.rekey.try_send(answer);
                Ok(())
            }
            Command::VideoStart {
                device,
                await_accept,
            } => match video.start(device, true).await {
                Ok(()) => {
                    video.ctl.send(if await_accept {
                        VideoControl::EnableAwaitingAccept
                    } else {
                        VideoControl::Enable
                    });
                    Ok(())
                }
                Err(e) => Err(e),
            },
            Command::VideoEnable => {
                video.ctl.send(VideoControl::Enable);
                Ok(())
            }
            Command::CameraOn { device } => video.camera_on(device).await,
            Command::CameraOff => {
                video.camera_off();
                self.frames.clear(self.generation, frames::LOCAL);
                Ok(())
            }
            Command::VideoStop => {
                video.stop();
                self.frames.clear(self.generation, frames::LOCAL);
                self.frames.clear(self.generation, frames::REMOTE);
                self.remote_seen = false;
                Ok(())
            }
            Command::PeerVideo { on } => {
                if on {
                    if video.pipe.is_some() {
                        video.ctl.send(VideoControl::Enable);
                        if !video.camera_wanted {
                            video.ctl.send(VideoControl::DisableOutbound);
                        }
                    }
                } else {
                    self.frames.clear(self.generation, frames::REMOTE);
                    self.remote_seen = false;
                }
                Ok(())
            }
            Command::Orientation { value } => {
                video.ctl.send(VideoControl::SetOrientation(value % 4));
                Ok(())
            }
            Command::Keyframe => {
                video
                    .ctl
                    .send(VideoControl::RequestPeerKeyframe(MediaKeyframeUrgency::Immediate));
                Ok(())
            }
            Command::Microphone { device } => {
                if let Some(mic) = &audio.mic {
                    mic.bind(device);
                }
                Ok(())
            }
            Command::Speaker { device } => {
                if let Some(speaker) = &audio.speaker {
                    speaker.bind(device);
                }
                Ok(())
            }
            Command::CameraDevice { device } => video.switch(device).await,
        };
        if let Err(error) = result {
            log::warn!("zfcalls: {error}");
            if !video.camera_running() && video.camera_wanted {
                video.camera_wanted = false;
            }
            self.out.emit(Kind::CameraStopped);
        }
    }

    /// One event of the engine's.
    fn media(&mut self, event: &CallEvent, video: &mut Video) {
        match event {
            CallEvent::RelayAllocated => {
                self.connected = true;
                self.out.emit(Kind::Connected);
                if video.pipe.is_some() {
                    // A decoder cannot start on a delta frame, so ask for an IDR at once.
                    video
                        .ctl
                        .send(VideoControl::RequestPeerKeyframe(MediaKeyframeUrgency::Immediate));
                }
            }
            CallEvent::RelayAllocateFailed(code) => self.out.emit(Kind::Failed {
                reason: format!("The relay refused the call ({code})"),
            }),
            CallEvent::RelayAllocateTimedOut => self.out.emit(Kind::Failed {
                reason: "The relay did not answer".into(),
            }),
            CallEvent::RelayReconnectTimedOut => self.out.emit(Kind::Failed {
                reason: "The relay connection dropped".into(),
            }),
            CallEvent::MediaSetupFailed(reason) => self.out.emit(Kind::Failed {
                reason: reason.clone(),
            }),
            CallEvent::AudioSilent {
                dominant_reason, ..
            } => self.out.emit(Kind::Silent {
                reason: silence(*dominant_reason).into(),
            }),
            CallEvent::AudioReceptionStalled { .. } => self.out.emit(Kind::Stalled),
            CallEvent::AudioCodecSwitched { .. } => self.out.emit(Kind::Audible),
            CallEvent::VideoKeyframeNeeded => {
                if let Some(pipe) = &video.pipe {
                    pipe.force_keyframe();
                }
            }
            _ => {
                if let Some(pipe) = video.pipe.as_mut() {
                    pipe.media(&self.call_id, event);
                }
            }
        }
    }

    /// One frame of the pipeline's.
    fn tick(&mut self, tick: VideoTick, video: &mut Video) {
        match tick {
            VideoTick::Local(picture) => self.frames.put(self.generation, frames::LOCAL, picture),
            VideoTick::Remote(picture) => {
                self.frames.put(self.generation, frames::REMOTE, picture);
                if !self.remote_seen {
                    self.remote_seen = true;
                    self.out.emit(Kind::RemoteVideo);
                }
            }
            VideoTick::Lost => {
                video
                    .ctl
                    .send(VideoControl::RequestPeerKeyframe(MediaKeyframeUrgency::Coalesced));
            }
            // A group member's: group calls are not bridged.
            VideoTick::RemoteFrom { .. } => {}
        }
    }
}

fn silence(reason: MediaSilenceReason) -> &'static str {
    match reason {
        MediaSilenceReason::NoDecoderForNegotiatedCodec => "no_decoder",
        MediaSilenceReason::AuthenticationFailing => "authentication_failing",
        MediaSilenceReason::UnexpectedPayloadType => "unexpected_payload_type",
        MediaSilenceReason::CodecRejectingFrames => "codec_rejecting_frames",
        MediaSilenceReason::CodecFlapping => "codec_flapping",
        _ => "unknown",
    }
}
