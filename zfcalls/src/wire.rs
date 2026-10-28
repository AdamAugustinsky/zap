//! What crosses the C ABI besides frames: the open parameters, the commands
//! and the events, as small JSON documents (`include/zfcalls.h`). A call
//! makes a handful of each, so their cost does not matter; their shape is
//! shared with gnui/gocore/calls (media.go), which encodes and decodes the
//! same names.

use base64::Engine as _;
use serde::{Deserialize, Deserializer, Serialize};

/// `zc_open`'s parameters: one 1:1 call's media, as Go's signaling found it.
#[derive(Deserialize)]
pub struct Open {
    pub call_id: String,
    /// The UI's number for the call; every command and event carries it.
    pub generation: u64,
    /// We placed the call (the caller's keys are rekeyed on the peer's accept).
    pub outgoing: bool,
    /// Our device's LID (`user:device@lid`), the send-side SRTP identity.
    pub self_lid: String,
    /// The peer's LID: the offer's call-creator on an incoming call, the
    /// callee on an outgoing one until `rekey` names the device that answered.
    pub peer_lid: String,
    /// The 32-byte callKey, decrypted (incoming) or generated (outgoing) by Go.
    #[serde(deserialize_with = "base64")]
    pub call_key: Vec<u8>,
    /// whatsmeow's `binary.Marshal` of the `<relay>` node, or of a node that
    /// holds one (the offer, the offer's ack): the format byte, then the node.
    #[serde(deserialize_with = "base64")]
    pub relay: Vec<u8>,
    /// MLow, the default; false is the standard Opus the peer's capability
    /// asked for (gocore/calls decides, as the facade's negotiated plan does).
    pub mlow: bool,
    /// A video call: the camera is captured and the peer's picture decoded.
    pub video: bool,
    /// Our camera starts on (a video call answered or placed with it on).
    #[serde(default)]
    pub camera_on: bool,
    #[serde(default)]
    pub microphone: Option<String>,
    #[serde(default)]
    pub speaker: Option<String>,
    #[serde(default)]
    pub camera: Option<String>,
    /// No devices at all: the microphone is silence and the speaker discards.
    /// For tests and headless runs; the app never sets it.
    #[serde(default)]
    pub silent: bool,
}

/// One command to the live call (`zc_command`).
#[derive(Debug, Deserialize, PartialEq)]
#[serde(tag = "op", rename_all = "snake_case")]
pub enum Command {
    /// Mutes the microphone: the engine is fed exact-zero frames.
    Mute { on: bool },
    /// Caller side: the device that answered, and the codec its capability
    /// selects (`"mlow"`, `"opus"`, or none: the negotiated one stands).
    Rekey {
        answering_lid: String,
        #[serde(default)]
        codec: Option<String>,
    },
    /// Starts our video: the camera, the encoder and the peer's decoder.
    /// `await_accept` holds outbound video off the wire until `video_enable`
    /// (an upgrade we asked for).
    VideoStart {
        #[serde(default)]
        device: Option<String>,
        #[serde(default)]
        await_accept: bool,
    },
    /// Lets outbound video onto the wire (the peer accepted, or announced
    /// its own video).
    VideoEnable,
    /// Our camera back on, within a call that has video.
    CameraOn {
        #[serde(default)]
        device: Option<String>,
    },
    /// Our camera off; the peer's picture keeps decoding.
    CameraOff,
    /// The video plane down: camera, encoder and decoder (a refused or
    /// cancelled upgrade, a downgrade).
    VideoStop,
    /// The peer's camera, as its `<video>` state says.
    PeerVideo { on: bool },
    /// The peer's camera rotation, in quarter turns.
    Orientation { value: u8 },
    /// Asks the peer for a keyframe.
    Keyframe,
    Microphone {
        #[serde(default)]
        device: Option<String>,
    },
    Speaker {
        #[serde(default)]
        device: Option<String>,
    },
    CameraDevice {
        #[serde(default)]
        device: Option<String>,
    },
}

/// One event (`zc_next_event`).
#[derive(Debug, Serialize, PartialEq)]
pub struct Event {
    pub generation: u64,
    #[serde(flatten)]
    pub kind: Kind,
}

#[derive(Debug, Serialize, PartialEq)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum Kind {
    /// The relay allocated: media flows.
    Connected,
    /// The media never came up, or the relay was lost while it was coming up.
    Failed { reason: String },
    /// The media ended after it was up (the relay dropped, a send failed).
    Closed { reason: String },
    /// The peer's audio is arriving but not becoming sound.
    Silent { reason: String },
    /// No audio is arriving from the peer.
    Stalled,
    /// The peer's audio decodes again (the engine switched codec).
    Audible,
    /// The first decoded frame of the peer's picture.
    RemoteVideo,
    /// Our camera stopped by itself (unplugged, taken by another app).
    CameraStopped,
    /// The call's media is gone: every device released. The last event of a
    /// generation.
    Ended,
}

/// The devices the call screen offers (`zc_devices`).
#[derive(Serialize)]
pub struct Devices {
    pub microphones: Vec<Device>,
    pub speakers: Vec<Device>,
    pub cameras: Vec<Device>,
}

#[derive(Serialize)]
pub struct Device {
    pub id: String,
    pub label: String,
}

fn base64<'de, D: Deserializer<'de>>(d: D) -> Result<Vec<u8>, D::Error> {
    let s = String::deserialize(d)?;
    base64::engine::general_purpose::STANDARD
        .decode(s)
        .map_err(serde::de::Error::custom)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn commands_and_events_have_the_shapes_go_writes_and_reads() {
        let c: Command =
            serde_json::from_str(r#"{"op":"rekey","answering_lid":"1:2@lid","codec":"opus"}"#)
                .unwrap();
        assert_eq!(
            c,
            Command::Rekey {
                answering_lid: "1:2@lid".into(),
                codec: Some("opus".into())
            }
        );
        let c: Command = serde_json::from_str(r#"{"op":"video_start","await_accept":true}"#).unwrap();
        assert_eq!(
            c,
            Command::VideoStart {
                device: None,
                await_accept: true
            }
        );
        let e = Event {
            generation: 7,
            kind: Kind::Failed {
                reason: "x".into(),
            },
        };
        assert_eq!(
            serde_json::to_string(&e).unwrap(),
            r#"{"generation":7,"kind":"failed","reason":"x"}"#
        );
        let e = Event {
            generation: 7,
            kind: Kind::RemoteVideo,
        };
        assert_eq!(
            serde_json::to_string(&e).unwrap(),
            r#"{"generation":7,"kind":"remote_video"}"#
        );
    }
}
