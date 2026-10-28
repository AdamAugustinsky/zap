//! Golden media-plane vectors for the Go VoIP port (gnui/gocore/voip), written
//! by whatsapp-rust 5e72baf's own code (wacore/src/voip): SRTP/SRTCP packets
//! out of the real MediaPipeline and VideoPipeline, the E2E and hop-by-hop key
//! schedules, SFrame frames, WARP MI tags, STUN allocate requests and pings,
//! WARP RTP headers, RTCP reports, H.264 RTP payloads and Opus packet shapes.
//! The Go tests rebuild each from the same inputs and compare byte for byte.
//!
//! Every key, id and payload is synthetic.
//!
//! `ZF_WRITE_VOIP_GOLDEN=1 cargo test -p zfcalls --test voip_golden` writes
//! gnui/gocore/voip/testdata/golden.json; without it the test checks the
//! committed file still matches.

use std::path::PathBuf;

use serde_json::{Value, json};
use wacore::voip::e2e_srtp::{
    E2eSrtpKeys, crypt_payload, derive_e2e_keys, derive_srtcp_keys, protect_srtcp,
};
use wacore::voip::h264::{PacketizedAu, packetize_au};
use wacore::voip::hbh_srtp::{
    HBH_SRTCP_MODES, HbhSrtcpMode, crypt_rtp_payload, derive_hbh_srtcp_key_downlink,
    derive_hbh_srtcp_key_shared, derive_hbh_srtcp_key_uplink, derive_hbh_srtcp_keys_downlink_for_mode,
    derive_hbh_srtcp_keys_uplink_for_mode, derive_hbh_srtp_key_shared, expand_libsrtp_session_keys,
    keying_from_hbh_srtp_key_shared,
};
use wacore::voip::opus_packet::opus_packet_shape;
use wacore::voip::rtcp::{
    RtcpSenderStats, build_compact_rtcp_208, build_compact_rtcp_209, build_empty_source_description,
    build_sender_report, build_sender_report_with_sdes, build_source_description,
    build_whatsapp_rtcp_cname,
};
use wacore::voip::rtp::{RtpStream, VideoRtpStream, encode_rtp_header};
use wacore::voip::session::{
    MediaPipeline, MediaPipelineParams, VideoPipeline, VideoPipelineParams,
};
use wacore::voip::sframe::{SframeSession, derive_e2e_sframe_key_for_participant};
use wacore::voip::ssrc::derive_wasm_participant_ssrc;
use wacore::voip::stun::{
    MSG_ALLOCATE_REQUEST, MSG_BINDING_REQUEST, WasmGroupStunAllocateRequest,
    build_android_stun_allocate_request, build_wasm_group_stun_allocate_request,
    build_wasm_stun_allocate_request, build_whatsapp_ping, encode_stun_request,
    encode_xor_relay_endpoint,
};
use wacore::voip::warp::compute_warp_mi_tag;

const SELF_LID: &str = "100000000001001:7@lid";
const PEER_LID: &str = "100000000002002@lid";
const CALL_ID: &str = "A1B2C3D4E5F60718293A4B5C6D7E8F90";

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

/// Deterministic filler: an LCG, so the vectors never change between runs.
fn bytes(seed: u32, n: usize) -> Vec<u8> {
    let mut s = seed.wrapping_mul(2_654_435_761).wrapping_add(1);
    (0..n)
        .map(|_| {
            s = s.wrapping_mul(1_103_515_245).wrapping_add(12_345);
            (s >> 16) as u8
        })
        .collect()
}

fn call_keys() -> [Vec<u8>; 2] {
    [(0u8..32).collect(), bytes(1, 32)]
}

fn keys_json(k: &E2eSrtpKeys) -> Value {
    json!({ "cipher": hex(&k.cipher_key), "salt": hex(&k.salt), "auth": hex(&k.auth_key) })
}

fn e2e_keys() -> Value {
    let mut out = Vec::new();
    for call_key in call_keys() {
        for lid in [SELF_LID, PEER_LID, "100000000002002:0@lid", "100000000003003:12@lid"] {
            out.push(json!({
                "call_key": hex(&call_key),
                "lid": lid,
                "srtp": keys_json(&derive_e2e_keys(&call_key, lid).unwrap()),
                "srtcp": keys_json(&derive_srtcp_keys(&call_key, lid).unwrap()),
            }));
        }
    }
    json!(out)
}

/// Audio payloads covering priming, MLOW DTX/speech, Opus DTX/speech and a
/// payload long enough for several AES blocks.
fn audio_payloads() -> Vec<Vec<u8>> {
    vec![
        vec![0x90, 0xb8, 0x14, 0x14, 0xc4],
        vec![0x90],
        vec![0x48; 40],
        bytes(2, 60),
        vec![0x10],
        vec![0x58; 80],
        bytes(3, 200),
        vec![0x48, 0x00],
    ]
}

fn audio_pipeline() -> Value {
    let configs: [(&[u8], &str, &str, u32, u32, usize, Option<u8>, bool); 3] = [
        (&call_keys()[0], SELF_LID, PEER_LID, 0x1234_5678, 960, 4, None, true),
        (&call_keys()[1], PEER_LID, SELF_LID, 0xdead_beef, 320, 4, Some(111), false),
        (&call_keys()[1], SELF_LID, PEER_LID, 7, 960, 10, Some(122), true),
    ];
    let mut out = Vec::new();
    for (call_key, self_lid, peer_lid, ssrc, spp, tag_len, pt, mlow) in configs {
        let mut pipeline = MediaPipeline::new(&MediaPipelineParams {
            call_key,
            self_lid,
            peer_lid,
            ssrc,
            samples_per_packet: spp,
            warp_mi_tag_len: tag_len,
        })
        .unwrap();
        if let Some(pt) = pt {
            assert!(pipeline.set_audio_payload_type(pt));
        }
        pipeline.set_audio_mlow_profile(mlow);
        let payloads = audio_payloads();
        let packets: Vec<String> = payloads.iter().map(|p| hex(&pipeline.protect_audio(p))).collect();
        out.push(json!({
            "call_key": hex(call_key),
            "self_lid": self_lid,
            "peer_lid": peer_lid,
            "ssrc": ssrc,
            "samples_per_packet": spp,
            "tag_len": tag_len,
            "payload_type": pt,
            "mlow": mlow,
            "payloads": payloads.iter().map(|p| hex(p)).collect::<Vec<_>>(),
            "packets": packets,
            "source_description": hex(&pipeline.audio_source_description()),
        }));
    }
    json!(out)
}

fn annexb(nals: &[Vec<u8>]) -> Vec<u8> {
    let mut au = Vec::new();
    for n in nals {
        au.extend_from_slice(&[0, 0, 0, 1]);
        au.extend_from_slice(n);
    }
    au
}

fn nal(t: u8, n: usize, seed: u32) -> Vec<u8> {
    let mut v = vec![0x60 | t];
    v.extend(bytes(seed, n - 1));
    v
}

fn access_units() -> Vec<Vec<u8>> {
    vec![
        annexb(&[nal(9, 2, 1), nal(7, 18, 2), nal(8, 4, 3), nal(5, 3000, 4)]),
        annexb(&[nal(1, 500, 5)]),
        annexb(&[nal(6, 12, 6), nal(1, 801, 7), nal(1, 800, 8)]),
        annexb(&[nal(7, 400, 9), nal(8, 400, 10), nal(5, 60, 11), nal(6, 9, 12)]),
        annexb(&[nal(9, 2, 13)]),
    ]
}

fn h264() -> Value {
    let mut payloads = PacketizedAu::default();
    json!(
        access_units()
            .iter()
            .map(|au| {
                packetize_au(au, &mut payloads);
                json!({ "au": hex(au), "payloads": payloads.iter().map(hex).collect::<Vec<_>>() })
            })
            .collect::<Vec<_>>()
    )
}

fn video_pipeline() -> Value {
    let mut out = Vec::new();
    for (call_key, ssrc, stride, tag_len) in [
        (&call_keys()[0], 0x49c5_fb8cu32, 6000u32, 4usize),
        (&call_keys()[1], 3, 4500, 4),
    ] {
        let mut pipeline = VideoPipeline::new(&VideoPipelineParams {
            call_key,
            self_lid: SELF_LID,
            peer_lid: PEER_LID,
            ssrc,
            ts_stride: stride,
            warp_mi_tag_len: tag_len,
        })
        .unwrap();
        let aus = access_units();
        let packets: Vec<Vec<String>> = aus
            .iter()
            .map(|au| pipeline.protect_video(au).iter().map(|p| hex(p)).collect())
            .collect();
        out.push(json!({
            "call_key": hex(call_key),
            "self_lid": SELF_LID,
            "peer_lid": PEER_LID,
            "ssrc": ssrc,
            "ts_stride": stride,
            "tag_len": tag_len,
            "aus": aus.iter().map(|a| hex(a)).collect::<Vec<_>>(),
            "packets": packets,
        }));
    }
    json!(out)
}

/// The cipher at ROCs and sequence numbers a short pipeline run never reaches.
fn e2e_crypt() -> Value {
    let keys = derive_e2e_keys(&call_keys()[1], PEER_LID).unwrap();
    let payload = bytes(20, 70);
    json!(
        [(0u16, 0u32), (0xffff, 0), (0, 1), (0x8000, 0xffff), (0x1234, 0x0001_0000), (7, u32::MAX)]
            .iter()
            .map(|&(seq, roc)| json!({
                "lid": PEER_LID,
                "call_key": hex(&call_keys()[1]),
                "ssrc": 0x5741_0001u32,
                "seq": seq,
                "roc": roc,
                "payload": hex(&payload),
                "out": hex(&crypt_payload(&keys, 0x5741_0001, seq, roc, &payload)),
            }))
            .collect::<Vec<_>>()
    )
}

fn warp_tags() -> Value {
    json!(
        [(4u32, 16usize, 0u32), (5, 33, 1), (6, 200, 0xffff_ffff)]
            .iter()
            .map(|&(seed, n, roc)| {
                let key = bytes(seed, 20);
                let packet = bytes(seed + 100, n);
                json!({
                    "auth_key": hex(&key),
                    "packet": hex(&packet),
                    "roc": roc,
                    "tag": hex(&compute_warp_mi_tag(&key, &packet, roc)),
                })
            })
            .collect::<Vec<_>>()
    )
}

fn sample_rtcp() -> Vec<Vec<u8>> {
    let cname = build_whatsapp_rtcp_cname(&[1, 2, 3, 4, 5, 6, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc]);
    let stats = RtcpSenderStats { packets_sent: 42, octets_sent: 4200, rtp_timestamp: 96_000 };
    vec![
        build_compact_rtcp_208(0x1234_5678, 0x9abc_def0).to_vec(),
        build_compact_rtcp_209(0x1234_5678).to_vec(),
        build_sender_report_with_sdes(0x1234_5678, &stats, 1_761_600_000_123, &cname),
        vec![0x80, 200, 0, 1, 1, 2, 3],
    ]
}

fn srtcp() -> Value {
    let mut out = Vec::new();
    for (i, rtcp) in sample_rtcp().iter().enumerate() {
        let keys = derive_srtcp_keys(&call_keys()[i % 2], SELF_LID).unwrap();
        for index in [1u32, 2, 0x7fff_ffff, 0x8000_0001] {
            out.push(json!({
                "call_key": hex(&call_keys()[i % 2]),
                "lid": SELF_LID,
                "ssrc": 0x1234_5678u32,
                "index": index,
                "rtcp": hex(rtcp),
                "out": hex(&protect_srtcp(&keys, 0x1234_5678, index, rtcp)),
            }));
        }
    }
    json!(out)
}

fn hbh() -> Value {
    let mut out = Vec::new();
    for seed in [30u32, 31] {
        let hbh_key = bytes(seed, 30);
        let keying = keying_from_hbh_srtp_key_shared(&hbh_key).unwrap();
        let session = expand_libsrtp_session_keys(&keying);
        let payload = bytes(seed + 1, 1100);
        let modes: Vec<Value> = HBH_SRTCP_MODES
            .iter()
            .map(|&mode| {
                let up = derive_hbh_srtcp_keys_uplink_for_mode(&hbh_key, mode).unwrap();
                let down = derive_hbh_srtcp_keys_downlink_for_mode(&hbh_key, mode).unwrap();
                let sr = &sample_rtcp()[2];
                json!({
                    "mode": if mode == HbhSrtcpMode::Directional { "directional" } else { "shared" },
                    "uplink": keys_json(&up),
                    "downlink": keys_json(&down),
                    "protected_uplink_sr": hex(&protect_srtcp(&up, 0x1234_5678, 3, sr)),
                })
            })
            .collect();
        let rtp: Vec<Value> = [(0x1111_2222u32, 5u64), (0xffff_0000, 0x0001_ffff)]
            .iter()
            .map(|&(ssrc, index)| json!({
                "ssrc": ssrc,
                "index": index,
                "payload": hex(&payload),
                "out": hex(&crypt_rtp_payload(&session, ssrc, index, &payload)),
            }))
            .collect();
        out.push(json!({
            "hbh_key": hex(&hbh_key),
            "uplink30": hex(&derive_hbh_srtcp_key_uplink(&hbh_key).unwrap()),
            "downlink30": hex(&derive_hbh_srtcp_key_downlink(&hbh_key).unwrap()),
            "shared_srtp30": hex(&derive_hbh_srtp_key_shared(&hbh_key).unwrap()),
            "shared_srtcp30": hex(&derive_hbh_srtcp_key_shared(&hbh_key).unwrap()),
            "session_key": hex(&session.session_key),
            "session_salt": hex(&session.session_salt),
            "session_auth": hex(&session.auth_key),
            "modes": modes,
            "rtp": rtp,
        }));
    }
    json!(out)
}

fn sframe() -> Value {
    let mut out = Vec::new();
    for (call_key, self_lid, peer_lid, start) in [
        (&call_keys()[0], SELF_LID, PEER_LID, 0u64),
        (&call_keys()[1], PEER_LID, SELF_LID, 126),
        (&call_keys()[1], SELF_LID, PEER_LID, 16_383),
    ] {
        let mut session = SframeSession::new(call_key, self_lid, peer_lid).unwrap();
        // tx_counter is private: advance it by encrypting throwaway frames, so
        // the counters cross the one- and two-byte varint boundaries.
        let skip = start;
        for _ in 0..skip {
            session.encrypt(b"");
        }
        let frames: Vec<Vec<u8>> = vec![bytes(40, 1), bytes(41, 20), bytes(42, 300), vec![0x48; 64]];
        let outs: Vec<String> = frames.iter().map(|f| hex(&session.encrypt(f))).collect();
        out.push(json!({
            "call_key": hex(call_key),
            "self_lid": self_lid,
            "peer_lid": peer_lid,
            "counter_start": skip,
            "peer_key": hex(&derive_e2e_sframe_key_for_participant(call_key, &session.peer_participant_id).unwrap()),
            "frames": frames.iter().map(|f| hex(f)).collect::<Vec<_>>(),
            "out": outs,
        }));
    }
    json!(out)
}

fn stun() -> Value {
    let tx: [u8; 12] = bytes(50, 12).try_into().unwrap();
    let token = bytes(51, 37);
    let key = b"c2VjcmV0LXJlbGF5LWtleQ==".to_vec();
    let ep = encode_xor_relay_endpoint("10.20.30.40", 3480).unwrap();
    let ssrcs: [u32; 9] = std::array::from_fn(|i| derive_wasm_participant_ssrc(CALL_ID, SELF_LID, i as u32));
    let token_attr = {
        let mut a = vec![0x40, 0x00, 0, token.len() as u8];
        a.extend_from_slice(&token);
        a.resize(a.len() + (4 - token.len() % 4) % 4, 0);
        a
    };
    json!({
        "tx": hex(&tx),
        "token": hex(&token),
        "key": hex(&key),
        "endpoint": { "ip": "10.20.30.40", "port": 3480, "xor": hex(&ep) },
        "call_id": CALL_ID,
        "lid": SELF_LID,
        "stream_ssrcs": ssrcs.to_vec(),
        "wasm_allocate": hex(&build_wasm_stun_allocate_request(&tx, &token, &ep, &key, CALL_ID, SELF_LID)),
        "android_allocate": hex(&build_android_stun_allocate_request(&tx, &token, 0x1234_5678, None, &key, false)),
        "android_allocate_pid_fp": hex(&build_android_stun_allocate_request(&tx, &token, 0x1234_5678, Some(9), &key, true)),
        "group_allocate": hex(&build_wasm_group_stun_allocate_request(&WasmGroupStunAllocateRequest {
            transaction_id: &tx,
            relay_token: &token,
            endpoint_xor: &ep,
            integrity_key: &key,
            stream_ssrcs: &ssrcs,
            app_data_ssrc: 0xb31d_ed3e,
            hbh_fec_ssrcs: &[0xc1a1_7938, 0x1bb2_0c84],
            participant_pids: &[5, 3, 5, 9],
        })),
        "ping": hex(&build_whatsapp_ping(&tx)),
        "binding_mi_fp": hex(&encode_stun_request(MSG_BINDING_REQUEST, &tx, &token_attr, Some(&key), true)),
        "allocate_fp_only": hex(&encode_stun_request(MSG_ALLOCATE_REQUEST, &tx, &token_attr, None, true)),
        "token_attr": hex(&token_attr),
    })
}

/// WARP RTP headers: the audio sequencer with piggyback on (empty extension on
/// packets #1-2, the 0x30010000 word from #3, the DTX word on silence), and
/// the video sequencer's three-word block.
fn rtp_headers() -> Value {
    let mut audio = RtpStream::new(0x0102_0304, 960, true);
    let payloads = [vec![0x48; 30], vec![0x48; 30], vec![0x48; 30], vec![0x90], vec![0x48; 30]];
    let audio_headers: Vec<String> = payloads
        .iter()
        .map(|p| hex(&encode_rtp_header(&audio.next_packet(p, false))))
        .collect();
    let mut video = VideoRtpStream::new(0x0506_0708, 6000).unwrap();
    let video_headers: Vec<String> = [(false, 8u8), (true, 8), (true, 0), (true, 1)]
        .iter()
        .map(|&(last, info)| hex(&encode_rtp_header(&video.next_video_packet(last, info))))
        .collect();
    json!({
        "audio": { "ssrc": 0x0102_0304u32, "samples_per_packet": 960, "payloads": payloads.iter().map(|p| hex(p)).collect::<Vec<_>>(), "headers": audio_headers },
        "video": { "ssrc": 0x0506_0708u32, "ts_stride": 6000, "steps": [[false, 8], [true, 8], [true, 0], [true, 1]], "headers": video_headers },
    })
}

fn rtcp() -> Value {
    let entropy = [9u8, 8, 7, 6, 5, 4, 0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54];
    let cname = build_whatsapp_rtcp_cname(&entropy);
    let stats = RtcpSenderStats { packets_sent: 0xfff0_0001, octets_sent: 77, rtp_timestamp: 0xffff_fff0 };
    let reports: Vec<Value> = [0u64, 999, 1_761_600_000_999, 4_294_967_295_500, u64::from(u32::MAX) * 1000 + 1]
        .iter()
        .map(|&now| json!({
            "now_ms": now,
            "sender_report": hex(&build_sender_report(0xa1b2_c3d4, &stats, now)),
            "sender_report_with_sdes": hex(&build_sender_report_with_sdes(0xa1b2_c3d4, &stats, now, &cname)),
        }))
        .collect();
    json!({
        "entropy": hex(&entropy),
        "cname": hex(&cname),
        "ssrc": 0xa1b2_c3d4u32,
        "remote_ssrc": 0x0102_0304u32,
        "stats": { "packets_sent": stats.packets_sent, "octets_sent": stats.octets_sent, "rtp_timestamp": stats.rtp_timestamp },
        "reports": reports,
        "compact208": hex(&build_compact_rtcp_208(0xa1b2_c3d4, 0x0102_0304)),
        "compact209": hex(&build_compact_rtcp_209(0xa1b2_c3d4)),
        "sdes": hex(&build_source_description(0xa1b2_c3d4, &cname)),
        "empty_sdes": hex(&build_empty_source_description(0xa1b2_c3d4)),
    })
}

fn opus_shapes() -> Value {
    let mut payloads: Vec<Vec<u8>> = (0u8..=255).map(|toc| vec![toc, 0x11, 0x22, 0x33]).collect();
    payloads.extend([
        vec![],
        vec![0x03],
        vec![0x03, 0x00],
        vec![0x03, 0x30, 1, 2],
        vec![0x0b, 0x85, 0, 0, 0, 0, 0],
        vec![0xfb, 0xc2, 0x01, 0x05, 1, 2, 3, 4, 5, 6],
        vec![0x02, 0xfc, 0xff, 9],
        vec![0x01, 1, 2, 3],
        vec![0x83, 0x3f],
        vec![0x83, 0x02, 9, 9],
    ]);
    json!(
        payloads
            .iter()
            .map(|p| json!({
                "payload": hex(p),
                "shape": opus_packet_shape(p).map(|s| json!({
                    "mode": format!("{:?}", s.mode),
                    "bandwidth": format!("{:?}", s.bandwidth),
                    "stereo": s.stereo,
                    "frames": s.frames,
                    "frame_duration_us": s.frame_duration_us,
                })),
            }))
            .collect::<Vec<_>>()
    )
}

fn ssrcs() -> Value {
    json!(
        [(CALL_ID, SELF_LID), (CALL_ID, PEER_LID), ("CALL-ID-0001", "12345:0@lid")]
            .iter()
            .flat_map(|&(call, lid)| (0u32..9).map(move |slot| json!({
                "call_id": call,
                "lid": lid,
                "slot": slot,
                "ssrc": derive_wasm_participant_ssrc(call, lid, slot),
            })))
            .collect::<Vec<_>>()
    )
}

fn golden_path() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../gnui/gocore/voip/testdata/golden.json")
}

#[test]
fn voip_vectors_match_the_go_golden_file() {
    let golden = json!({
        "e2e_keys": e2e_keys(),
        "audio_pipeline": audio_pipeline(),
        "video_pipeline": video_pipeline(),
        "e2e_crypt": e2e_crypt(),
        "warp_tags": warp_tags(),
        "srtcp": srtcp(),
        "hbh": hbh(),
        "sframe": sframe(),
        "stun": stun(),
        "rtp_headers": rtp_headers(),
        "rtcp": rtcp(),
        "h264": h264(),
        "opus_shape": opus_shapes(),
        "ssrc": ssrcs(),
    });
    let text = serde_json::to_string_pretty(&golden).unwrap() + "\n";
    let path = golden_path();
    if std::env::var_os("ZF_WRITE_VOIP_GOLDEN").is_some() {
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(&path, text).unwrap();
        return;
    }
    let committed = std::fs::read_to_string(&path)
        .unwrap_or_else(|e| panic!("{}: {e}; write it with ZF_WRITE_VOIP_GOLDEN=1", path.display()));
    assert_eq!(committed, text, "the VoIP vectors changed: rewrite them with ZF_WRITE_VOIP_GOLDEN=1");
}
