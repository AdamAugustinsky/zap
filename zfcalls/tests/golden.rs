//! Golden call stanzas for the Go signaling (gnui/gocore/calls), written by
//! whatsapp-rust 5e72baf's own builders (wacore/src/stanza/call.rs) and
//! packed by its binary marshal. gnui/gocore/calls/stanza_test.go builds the
//! same stanzas with the Go builders and compares them node for node.
//!
//! Every id, key and number is synthetic.
//!
//! `ZF_WRITE_CALL_GOLDEN=1 cargo test -p zfcalls --test golden` writes
//! gnui/gocore/calls/testdata/golden.json; without it the test checks the
//! committed file still matches these builders.

use std::collections::BTreeMap;
use std::path::PathBuf;

use whatsapp_rust::wacore::stanza::call::{
    AcceptParams, CAPABILITY_OFFER, CAPABILITY_STANDARD_OPUS_OFFER, CAPABILITY_VIDEO_OFFER,
    OfferDeviceKey, OfferParams, RelayLatencyParams, TerminateParams, TransportParams,
    VideoStateParams, build_accept, build_heartbeat, build_mute_v2, build_offer,
    build_offer_ack_receipt, build_preaccept, build_preaccept_with_capability, build_reject,
    build_relay_latency, build_terminate, build_transport, build_video_state, parse_call_stanza,
    standard_opus_voip_settings, CAPABILITY_STANDARD_OPUS_PREACCEPT,
};
use whatsapp_rust::wacore::types::call::VideoState;
use whatsapp_rust::wacore_binary::builder::NodeBuilder;
use whatsapp_rust::wacore_binary::marshal::marshal;
use whatsapp_rust::wacore_binary::Node;
use whatsapp_rust::{Jid, Server};

const CALL_ID: &str = "A1B2C3D4E5F60718293A4B5C6D7E8F90";

fn me() -> Jid {
    Jid::new("100000000001001", Server::Lid).with_device(7)
}

fn peer() -> Jid {
    Jid::new("100000000002002", Server::Lid)
}

fn peer_device(device: u16) -> Jid {
    peer().with_device(device)
}

fn key(device: u16, enc_type: &str) -> OfferDeviceKey {
    OfferDeviceKey {
        device_jid: peer_device(device),
        ciphertext: vec![0x30 + device as u8; 24],
        enc_type: enc_type.to_owned(),
    }
}

fn stanzas() -> BTreeMap<&'static str, Node> {
    let me = me();
    let peer = peer();
    let one = [key(0, "msg")];
    let two = [key(0, "pkmsg"), key(3, "msg")];
    let mut out = BTreeMap::new();

    out.insert(
        "offer_audio_single",
        build_offer(&OfferParams {
            call_id: CALL_ID,
            to: &peer,
            call_creator: &me,
            device_keys: &one,
            privacy_token: Some(b"tc-token"),
            capability: Some(&CAPABILITY_STANDARD_OPUS_OFFER),
            device_identity: None,
            id: Some("OFFER-ID-1"),
            multi_device: false,
            video: false,
            audio_rates: &["16000"],
        }),
    );
    out.insert(
        "offer_audio_multi_device_one_key",
        build_offer(&OfferParams {
            call_id: CALL_ID,
            to: &peer,
            call_creator: &me,
            device_keys: &one,
            privacy_token: None,
            capability: Some(&CAPABILITY_OFFER),
            device_identity: None,
            id: Some("OFFER-ID-2"),
            multi_device: true,
            video: false,
            audio_rates: &["8000", "16000"],
        }),
    );
    out.insert(
        "offer_video_two_keys",
        build_offer(&OfferParams {
            call_id: CALL_ID,
            to: &peer,
            call_creator: &me,
            device_keys: &two,
            privacy_token: Some(b"tc-token"),
            capability: Some(&CAPABILITY_VIDEO_OFFER),
            device_identity: Some(b"device-identity"),
            id: Some("OFFER-ID-3"),
            multi_device: true,
            video: true,
            audio_rates: &["16000"],
        }),
    );

    out.insert(
        "accept_audio_standard_opus",
        build_accept(&AcceptParams {
            call_id: CALL_ID,
            to: &peer_device(3),
            id: "ACCEPT-ID-1",
            call_creator: &peer,
            audio_rates: &["16000"],
            relay_te: None,
            rte: None,
            voip_settings: Some(standard_opus_voip_settings(false)),
            capability: Some(&CAPABILITY_STANDARD_OPUS_OFFER),
            video: false,
            peer_abtest_bucket: None,
            peer_abtest_bucket_id_list: None,
        }),
    );
    out.insert(
        "accept_video_full",
        build_accept(&AcceptParams {
            call_id: CALL_ID,
            to: &peer_device(3),
            id: "ACCEPT-ID-2",
            call_creator: &peer,
            audio_rates: &["16000"],
            relay_te: Some(&[1, 2, 3, 4, 0x0d, 0x96]),
            rte: Some(&[9, 8, 7]),
            voip_settings: None,
            capability: None,
            video: true,
            peer_abtest_bucket: Some("bucket"),
            peer_abtest_bucket_id_list: Some("1,2"),
        }),
    );

    out.insert(
        "preaccept_audio",
        build_preaccept(CALL_ID, &peer_device(3), &peer, "PRE-ID-1", &["16000"], false),
    );
    out.insert(
        "preaccept_video",
        build_preaccept(CALL_ID, &peer_device(3), &peer, "PRE-ID-2", &["16000"], true),
    );
    out.insert(
        "preaccept_standard_opus",
        build_preaccept_with_capability(
            CALL_ID,
            &peer_device(3),
            &peer,
            "PRE-ID-3",
            &["16000"],
            &CAPABILITY_STANDARD_OPUS_PREACCEPT,
            false,
        ),
    );

    out.insert(
        "transport_te",
        build_transport(&TransportParams {
            call_id: CALL_ID,
            to: &peer_device(3),
            call_creator: &me,
            p2p_cand_round: Some("1"),
            transport_message_type: Some("2"),
            relay_te: Some(&[1, 2, 3, 4, 0x0d, 0x96]),
        }),
    );
    out.insert(
        "transport_type_9",
        build_transport(&TransportParams {
            call_id: CALL_ID,
            to: &peer_device(3),
            call_creator: &me,
            p2p_cand_round: None,
            transport_message_type: Some("9"),
            relay_te: None,
        }),
    );

    let devices = [peer_device(0), peer_device(3)];
    out.insert(
        "relaylatency_devices",
        build_relay_latency(&RelayLatencyParams {
            call_id: CALL_ID,
            to: &peer,
            call_creator: &me,
            latency_ms: 42,
            relay_name: "gru1c01",
            address_bytes: &[10, 0, 0, 1, 0x0d, 0x96],
            devices: &devices,
        }),
    );
    out.insert(
        "relaylatency_plain",
        build_relay_latency(&RelayLatencyParams {
            call_id: CALL_ID,
            to: &peer_device(3),
            call_creator: &me,
            latency_ms: 0,
            relay_name: "gru1c01",
            address_bytes: &[10, 0, 0, 1, 0x0d, 0x96],
            devices: &[],
        }),
    );

    out.insert(
        "terminate_reason",
        build_terminate(&TerminateParams {
            call_id: CALL_ID,
            to: &peer_device(3),
            id: Some("TERM-ID-1"),
            call_creator: &me,
            reason: Some("accepted_elsewhere"),
        }),
    );
    out.insert(
        "terminate_plain",
        build_terminate(&TerminateParams {
            call_id: CALL_ID,
            to: &peer,
            id: None,
            call_creator: &me,
            reason: None,
        }),
    );

    out.insert(
        "video_upgrade_request",
        build_video_state(&VideoStateParams {
            call_id: CALL_ID,
            to: &peer_device(3),
            id: "VIDEO-ID-1",
            call_creator: &me,
            state: VideoState::UpgradeRequestV2,
            dec: Some("H264"),
            device_orientation: None,
        }),
    );
    out.insert(
        "video_enabled_orientation",
        build_video_state(&VideoStateParams {
            call_id: CALL_ID,
            to: &peer_device(3),
            id: "VIDEO-ID-2",
            call_creator: &me,
            state: VideoState::Enabled,
            dec: Some("H264"),
            device_orientation: Some(0),
        }),
    );
    out.insert(
        "video_stopped",
        build_video_state(&VideoStateParams {
            call_id: CALL_ID,
            to: &peer_device(3),
            id: "VIDEO-ID-3",
            call_creator: &me,
            state: VideoState::Stopped,
            dec: None,
            device_orientation: None,
        }),
    );

    out.insert("mute_on", build_mute_v2(CALL_ID, &peer_device(3), &me, "MUTE-ID-1", true));
    out.insert("mute_off", build_mute_v2(CALL_ID, &peer_device(3), &me, "MUTE-ID-2", false));
    out.insert("reject", build_reject(CALL_ID, &peer_device(3), &peer, "REJECT-ID-1"));
    out.insert("heartbeat", build_heartbeat(CALL_ID, &me, "BEAT-ID-1"));

    // The receipt answering an incoming offer, parsed from the offer.
    let offer = NodeBuilder::new("call")
        .attr("from", peer_device(3))
        .attr("id", "STANZA-ID-1")
        .attr("t", "1766847151")
        .attr("e", "0")
        .children([NodeBuilder::new("offer")
            .attr("call-creator", peer.clone())
            .attr("call-id", CALL_ID)
            .build()])
        .build();
    let call = parse_call_stanza(&offer.as_node_ref())
        .expect("the offer parses")
        .expect("an offer is a call");
    out.insert(
        "offer_ack_receipt",
        build_offer_ack_receipt(&call, Some(&me)).expect("an offer has a receipt"),
    );
    out
}

fn golden_path() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../gnui/gocore/calls/testdata/golden.json")
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

#[test]
fn call_stanzas_match_the_go_golden_file() {
    let golden: BTreeMap<&str, String> = stanzas()
        .into_iter()
        .map(|(name, node)| (name, hex(&marshal(&node).expect("the stanza marshals"))))
        .collect();
    let json = serde_json::to_string_pretty(&golden).unwrap() + "\n";
    let path = golden_path();
    if std::env::var_os("ZF_WRITE_CALL_GOLDEN").is_some() {
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(&path, json).unwrap();
        return;
    }
    let committed = std::fs::read_to_string(&path)
        .unwrap_or_else(|e| panic!("{}: {e}; write it with ZF_WRITE_CALL_GOLDEN=1", path.display()));
    assert_eq!(committed, json, "the golden stanzas changed: rewrite them with ZF_WRITE_CALL_GOLDEN=1");
}
