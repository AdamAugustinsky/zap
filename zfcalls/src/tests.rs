//! The C ABI end to end, offline: no device is opened (`silent`), and the
//! relay is a local UDP port nothing answers on.

use std::time::{Duration, Instant};

use base64::Engine as _;
use whatsapp_rust::wacore_binary::builder::NodeBuilder;
use whatsapp_rust::wacore_binary::marshal::marshal;

use super::*;

/// A `<relay>` whose one endpoint is 127.0.0.1:`port`, packed as whatsmeow's
/// `binary.Marshal` packs it.
fn relay(port: u16) -> Vec<u8> {
    let mut address = vec![127, 0, 0, 1];
    address.extend_from_slice(&port.to_be_bytes());
    let node = NodeBuilder::new("relay")
        .attr("uuid", "test")
        .children([
            NodeBuilder::new("key").bytes(b"cmVsYXkta2V5LXRlc3Q=".to_vec()).build(),
            NodeBuilder::new("token").attr("id", "0").bytes(vec![0xaa; 16]).build(),
            NodeBuilder::new("auth_token").attr("id", "0").bytes(vec![0xbb; 8]).build(),
            NodeBuilder::new("te2")
                .attr("relay_id", "0")
                .attr("relay_name", "test")
                .attr("token_id", "0")
                .attr("auth_token_id", "0")
                .bytes(address)
                .build(),
        ])
        .build();
    marshal(&node).unwrap()
}

fn params(generation: u64, relay: &[u8]) -> Vec<u8> {
    let b64 = |b: &[u8]| base64::engine::general_purpose::STANDARD.encode(b);
    serde_json::to_vec(&serde_json::json!({
        "call_id": "00112233445566778899aabbccddeeff",
        "generation": generation,
        "outgoing": true,
        "self_lid": "100000000001001:7@lid",
        "peer_lid": "100000000002002@lid",
        "call_key": b64(&[7u8; 32]),
        "relay": b64(relay),
        "mlow": true,
        "video": false,
        "silent": true,
    }))
    .unwrap()
}

fn err() -> zc_err {
    zc_err {
        code: 0,
        len: 0,
        msg: [0; 256],
    }
}

fn message(e: &zc_err) -> String {
    String::from_utf8_lossy(&e.msg[..e.len as usize]).into_owned()
}

/// Events until `last` (inclusive) or the deadline.
unsafe fn events_until(ctx: *mut zc_ctx, last: &str, within: Duration) -> Vec<serde_json::Value> {
    let deadline = Instant::now() + within;
    let mut seen = Vec::new();
    let mut buf = vec![0u8; 4096];
    while Instant::now() < deadline {
        let mut e = err();
        // SAFETY: a live context and a buffer of its length.
        let n = unsafe { zc_next_event(ctx, 200, buf.as_mut_ptr(), buf.len(), &mut e) };
        assert!(n >= 0, "zc_next_event: {}", message(&e));
        if n == 0 {
            continue;
        }
        let event: serde_json::Value = serde_json::from_slice(&buf[..n as usize]).unwrap();
        let done = event["kind"] == last;
        seen.push(event);
        if done {
            break;
        }
    }
    seen
}

#[test]
fn a_call_whose_relay_never_answers_fails_and_releases_everything() {
    // A port that was free a moment ago: nothing answers the DTLS hello.
    let port = std::net::UdpSocket::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap()
        .port();
    let mut e = err();
    unsafe {
        let ctx = zc_new(&mut e);
        assert!(!ctx.is_null(), "zc_new: {}", message(&e));

        let p = params(5, &relay(port));
        assert_eq!(zc_open(ctx, p.as_ptr(), p.len(), &mut e), ZC_OK, "{}", message(&e));
        // One call at a time.
        assert_eq!(zc_open(ctx, p.as_ptr(), p.len(), &mut e), ZC_E_STATE);
        let mute = br#"{"op":"mute","on":true}"#;
        assert_eq!(zc_command(ctx, 5, mute.as_ptr(), mute.len(), &mut e), ZC_OK);
        assert_eq!(zc_command(ctx, 6, mute.as_ptr(), mute.len(), &mut e), ZC_E_NOT_FOUND);
        let bad = br#"{"op":"sing"}"#;
        assert_eq!(zc_command(ctx, 5, bad.as_ptr(), bad.len(), &mut e), ZC_E_MALFORMED);

        let seen = events_until(ctx, "ended", Duration::from_secs(30));
        let kinds: Vec<_> = seen.iter().map(|v| v["kind"].as_str().unwrap().to_owned()).collect();
        assert_eq!(kinds, ["failed", "ended"], "{seen:?}");
        assert!(seen.iter().all(|v| v["generation"] == 5));
        // A call whose media ended on its own leaves room for the next.
        let p = params(6, &relay(port));
        assert_eq!(zc_open(ctx, p.as_ptr(), p.len(), &mut e), ZC_OK, "{}", message(&e));
        assert_eq!(
            zc_frame_acquire(ctx, frames::REMOTE, std::ptr::null_mut(), &mut e),
            ZC_E_ARG
        );
        zc_free(ctx);
    }
}

#[test]
fn a_closed_call_ends_and_another_may_open() {
    let mut e = err();
    unsafe {
        let ctx = zc_new(&mut e);
        // A port nothing listens on, held open so the dial waits.
        let sock = std::net::UdpSocket::bind("127.0.0.1:0").unwrap();
        let port = sock.local_addr().unwrap().port();
        let p = params(1, &relay(port));
        assert_eq!(zc_open(ctx, p.as_ptr(), p.len(), &mut e), ZC_OK, "{}", message(&e));
        assert_eq!(zc_close(ctx, 1, &mut e), ZC_OK);
        let seen = events_until(ctx, "ended", Duration::from_secs(5));
        assert_eq!(seen.last().map(|v| v["kind"].clone()), Some("ended".into()), "{seen:?}");
        let p = params(2, &relay(port));
        assert_eq!(zc_open(ctx, p.as_ptr(), p.len(), &mut e), ZC_OK, "{}", message(&e));
        zc_free(ctx);
    }
}

#[test]
fn bad_parameters_are_refused_before_anything_opens() {
    let mut e = err();
    unsafe {
        let ctx = zc_new(&mut e);
        let junk = b"{";
        assert_eq!(zc_open(ctx, junk.as_ptr(), junk.len(), &mut e), ZC_E_MALFORMED);
        // No relay in the node.
        let node = marshal(&NodeBuilder::new("ack").build()).unwrap();
        let p = params(1, &node);
        assert_eq!(zc_open(ctx, p.as_ptr(), p.len(), &mut e), ZC_E_REFUSED);
        assert!(message(&e).contains("relay"), "{}", message(&e));
        // A short key.
        let mut v: serde_json::Value = serde_json::from_slice(&params(1, &relay(9))).unwrap();
        v["call_key"] = "AAAA".into();
        let p = serde_json::to_vec(&v).unwrap();
        assert_eq!(zc_open(ctx, p.as_ptr(), p.len(), &mut e), ZC_E_REFUSED);
        let mut f = std::mem::MaybeUninit::<zc_frame>::uninit();
        assert_eq!(zc_frame_acquire(ctx, 0, f.as_mut_ptr(), &mut e), ZC_E_NOT_FOUND);
        zc_free(ctx);
    }
    assert_eq!(zc_abi_version(), ZC_ABI);
}
