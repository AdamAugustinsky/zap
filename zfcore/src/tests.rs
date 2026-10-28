//! The records against their schema: the hash, the fixtures the Go side
//! reads back byte for byte, every variant at its bounds, refusals, and the
//! chunking and dropping the DTO layer does.

use std::path::PathBuf;

use crate::abi_gen::{self as w, samples};
use crate::cores::Shared;
use crate::wire::sample::Seed;

fn fixture(name: &str) -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("abi/fixtures")
        .join(name)
}

fn frames(buf: &[u8]) -> Vec<&[u8]> {
    let mut out = Vec::new();
    let mut rest = buf;
    while !rest.is_empty() {
        let n = u32::from_le_bytes(rest[..4].try_into().unwrap()) as usize;
        out.push(&rest[4..4 + n]);
        rest = &rest[4 + n..];
    }
    out
}

fn frame(out: &mut Vec<u8>, record: &[u8]) {
    out.extend_from_slice(&(record.len() as u32).to_le_bytes());
    out.extend_from_slice(record);
}

/// Every variant over enough rounds that each union variant appears.
fn sample_events() -> Vec<w::Event> {
    let width = samples::events(&mut Seed::new(0, false));
    let mut out = Vec::new();
    for (index, (_, rounds)) in width.iter().enumerate() {
        for round in 0..*rounds {
            out.push(
                samples::events(&mut Seed::new(round, false))
                    .swap_remove(index)
                    .0,
            );
        }
    }
    out
}

fn sample_commands() -> Vec<w::Command> {
    let width = samples::commands(&mut Seed::new(0, false));
    let mut out = Vec::new();
    for (index, (_, rounds)) in width.iter().enumerate() {
        for round in 0..*rounds {
            out.push(
                samples::commands(&mut Seed::new(round, false))
                    .swap_remove(index)
                    .0,
            );
        }
    }
    out
}

fn normalized(src: &str) -> String {
    let mut out = String::new();
    for line in src.lines() {
        let line = line.split('#').next().unwrap_or("").trim();
        if !line.is_empty() {
            out.push_str(line);
            out.push('\n');
        }
    }
    out
}

#[test]
fn the_generated_code_matches_the_schema_file() {
    let mut h: u64 = 0xcbf2_9ce4_8422_2325;
    for b in normalized(include_str!("../abi/zf.schema")).bytes() {
        h ^= u64::from(b);
        h = h.wrapping_mul(0x0100_0000_01b3);
    }
    assert_eq!(
        h,
        w::SCHEMA_HASH,
        "abi_gen.rs is stale: run `go run ./tools/abigen` in gnui/"
    );
}

/// Writes the fixtures with `ZF_WRITE_FIXTURES=1`; otherwise checks them.
/// gnui/abi's golden test decodes and re-encodes them byte for byte.
#[test]
fn fixtures_round_trip_and_are_current() {
    let mut events = Vec::new();
    for event in sample_events() {
        let mut record = Vec::new();
        event.encode(&mut record).unwrap();
        assert_eq!(record.len(), event.wire_len(), "{}", event.name());
        assert_eq!(w::Event::decode(&record).unwrap(), event);
        frame(&mut events, &record);
    }
    let mut commands = Vec::new();
    for command in sample_commands() {
        let mut record = Vec::new();
        command.encode(&mut record).unwrap();
        assert_eq!(record.len(), command.wire_len(), "{}", command.name());
        assert_eq!(w::Command::decode(&record).unwrap(), command);
        frame(&mut commands, &record);
    }
    for (name, bytes) in [("events.bin", events), ("commands.bin", commands)] {
        if std::env::var_os("ZF_WRITE_FIXTURES").is_some() {
            std::fs::create_dir_all(fixture("")).unwrap();
            std::fs::write(fixture(name), &bytes).unwrap();
        } else {
            let have =
                std::fs::read(fixture(name)).expect("fixtures exist: run with ZF_WRITE_FIXTURES=1");
            assert!(
                have == bytes,
                "{name} is stale: run with ZF_WRITE_FIXTURES=1"
            );
        }
    }
}

#[test]
fn every_variant_at_its_bounds_fits_one_record() {
    for (event, _) in samples::events(&mut Seed::new(0, true)) {
        let mut record = Vec::new();
        event
            .encode(&mut record)
            .unwrap_or_else(|e| panic!("{}: {e}", event.name()));
        let &(_, _, max) = w::EVENT_VARIANTS
            .iter()
            .find(|v| v.0 == event.tag())
            .unwrap();
        assert!(record.len() <= w::MAX_RECORD, "{}", event.name());
        assert!(
            record.len() <= max,
            "{}: {} over the generator's {max}",
            event.name(),
            record.len()
        );
        assert_eq!(w::Event::decode(&record).unwrap(), event);
    }
    for (command, _) in samples::commands(&mut Seed::new(0, true)) {
        let mut record = Vec::new();
        command
            .encode(&mut record)
            .unwrap_or_else(|e| panic!("{}: {e}", command.name()));
        let &(_, _, max) = w::COMMAND_VARIANTS
            .iter()
            .find(|v| v.0 == command.tag())
            .unwrap();
        // Commands have no chunks, so the largest sample is the bound itself.
        assert_eq!(record.len(), max, "{}", command.name());
        assert_eq!(w::Command::decode(&record).unwrap(), command);
    }
}

#[test]
fn a_field_over_its_bound_builds_no_record() {
    let mut record = Vec::new();
    let error = w::Event::Info {
        text: "a".repeat(4097),
    }
    .encode(&mut record)
    .unwrap_err();
    assert_eq!(
        (error.field, error.len, error.max),
        ("Event.Info.text", 4097, 4096)
    );
}

#[test]
fn malformed_records_are_refused_without_panicking() {
    let events = std::fs::read(fixture("events.bin")).unwrap();
    for record in frames(&events) {
        for cut in 0..record.len() {
            assert!(w::Event::decode(&record[..cut]).is_err());
        }
        let mut longer = record.to_vec();
        longer.push(0);
        assert!(w::Event::decode(&longer).is_err());
        // Every single-byte change either decodes to something or is refused.
        for at in (0..record.len()).step_by(7) {
            let mut bad = record.to_vec();
            bad[at] ^= 0xa5;
            let _ = w::Event::decode(&bad);
        }
    }
    // A count far beyond the record is refused before anything is allocated.
    let mut contacts = Vec::new();
    contacts.extend_from_slice(&17u16.to_le_bytes());
    contacts.extend_from_slice(&[0, 0, 1, 0, 0, 0, 1]);
    contacts.extend_from_slice(&4096u32.to_le_bytes());
    assert_eq!(
        w::Event::decode(&contacts).unwrap_err().reason,
        "a count larger than the record"
    );
}

fn text_message(id: &str, text: String) -> zapfast::model::Message {
    zapfast::model::Message {
        id: id.into(),
        chat: "1@s.whatsapp.net".into(),
        sender: "1@s.whatsapp.net".into(),
        sender_name: None,
        from_me: false,
        timestamp: 1,
        history_order: None,
        content: zapfast::model::Content::text(text),
        status: zapfast::model::Delivery::None,
        delivered_at: None,
        read_at: None,
        quoted: None,
        reactions: Vec::new(),
        edited: false,
        mentions: Vec::new(),
        forwarded: false,
        thumbnail: Some(vec![1, 2, 3]),
    }
}

fn drain(shared: &Shared) -> Vec<w::Event> {
    let mut dst = vec![0u8; w::MAX_RECORD + 4];
    let mut out = Vec::new();
    while let crate::wake::Drained::Some { len, .. } = shared.queue.drain(&mut dst, false) {
        for record in frames(&dst[..len]) {
            out.push(w::Event::decode(record).unwrap());
        }
    }
    out
}

#[test]
fn a_long_chat_list_arrives_in_chunks_of_one_batch() {
    let shared = Shared::new(true).unwrap();
    let chats = (0..600)
        .map(|i| zapfast::model::Chat::new(format!("{i}@s.whatsapp.net"), format!("Chat {i}")))
        .collect();
    shared.emit(zapfast::backend::Event::Chats(chats));
    let records = drain(&shared);
    let shape: Vec<_> = records
        .iter()
        .map(|e| match e {
            w::Event::Chats { batch, last, chats } => (*batch, *last, chats.len()),
            other => panic!("{other:?}"),
        })
        .collect();
    assert_eq!(shape, [(1, false, 256), (1, false, 256), (1, true, 88)]);
}

#[test]
fn an_oversized_message_is_dropped_alone_and_its_blob_freed() {
    let shared = Shared::new(true).unwrap();
    // Each with a thumbnail of its own: the same bytes would share one blob.
    let message = |id: &str, text: String| {
        let mut m = text_message(id, text);
        m.thumbnail = Some(id.as_bytes().to_vec());
        m
    };
    shared.emit(zapfast::backend::Event::Messages {
        chat: "1@s.whatsapp.net".into(),
        messages: vec![
            message("a", "short".into()),
            message("b", "x".repeat(196_609)),
            message("c", "also short".into()),
        ],
        older: false,
        complete: true,
    });
    let records = drain(&shared);
    assert!(
        matches!(records[0], w::Event::EventDropped { kind: 14, .. }),
        "{records:?}"
    );
    match &records[1] {
        w::Event::Messages {
            messages,
            last: true,
            complete: true,
            ..
        } => {
            let ids: Vec<_> = messages.iter().map(|m| m.id.as_str()).collect();
            assert_eq!(ids, ["a", "c"]);
        }
        other => panic!("{other:?}"),
    }
    // The dropped message's thumbnail went with it; the others' are held.
    assert_eq!(shared.blobs.count(), 2);
}

#[test]
fn a_record_over_its_bound_is_dropped_and_the_stream_goes_on() {
    let shared = Shared::new(true).unwrap();
    // An id is never cut: this one cannot be carried.
    shared.emit(zapfast::backend::Event::Presence {
        id: "x".repeat(5000),
        online: true,
        last_seen: None,
    });
    shared.emit(zapfast::backend::Event::Syncing(true));
    let records = drain(&shared);
    assert_eq!(
        records,
        [
            w::Event::EventDropped {
                kind: 19,
                size: 5000
            },
            w::Event::Syncing { syncing: true }
        ]
    );
}

// ---- display text is cut, never the record around it ---------------------

/// A cut summary: within its bound, ending in an ellipsis, and the part
/// before it a whole number of `unit`s, so no character or emoji sequence
/// was split.
fn assert_cut(summary: &[u8], bound: usize, unit: &str) {
    let summary = std::str::from_utf8(summary).unwrap();
    assert!(summary.len() <= bound, "{} bytes", summary.len());
    let body = summary.strip_suffix('…').expect("ends with an ellipsis");
    assert!(!body.is_empty() && body.len() > bound - 3 - unit.len());
    assert_eq!(body.len() % unit.len(), 0, "cut inside a {unit:?}");
    assert!(body.starts_with(unit));
}

fn chat_with_last(summary: &str) -> zapfast::model::Chat {
    let mut chat = zapfast::model::Chat::new("1@s.whatsapp.net".into(), "Ada".into());
    chat.last = Some(zapfast::model::LastMessage {
        from_me: false,
        sender: "1@s.whatsapp.net".into(),
        sender_name: None,
        summary: summary.into(),
        full: summary.into(),
        status: zapfast::model::Delivery::None,
    });
    chat
}

/// Upstream's summary is the whole first line of the message, so a valid
/// message with a long first line used to lose its chat row.
#[test]
fn a_long_latest_message_is_cut_in_the_chat_row_not_dropped() {
    // A family emoji is one grapheme of 25 bytes (seven code points).
    let family = "👨‍👩‍👧‍👦";
    assert_eq!(family.len(), 25);
    let shared = Shared::new(true).unwrap();
    let long = family.repeat(300);
    shared.emit(zapfast::backend::Event::Chats(vec![chat_with_last(&long)]));
    shared.emit(zapfast::backend::Event::ChatUpdated(Box::new(
        chat_with_last(&"é".repeat(2000)),
    )));
    let records = drain(&shared);
    assert_eq!(records.len(), 2, "{records:?}");
    match &records[0] {
        w::Event::Chats { chats, .. } => {
            let last = chats[0].last.as_ref().unwrap();
            assert_cut(last.summary.as_bytes(), 1024, family);
            assert_eq!(last.full, long, "the whole text still travels");
        }
        other => panic!("{other:?}"),
    }
    match &records[1] {
        w::Event::ChatUpdated { chat } => {
            assert_cut(chat.last.as_ref().unwrap().summary.as_bytes(), 1024, "é")
        }
        other => panic!("{other:?}"),
    }
}

#[test]
fn a_long_quoted_message_is_cut_in_the_reply_not_dropped() {
    let shared = Shared::new(true).unwrap();
    let mut reply = text_message("reply", "short".into());
    reply.quoted = Some(zapfast::model::Quoted {
        chat: None,
        id: "original".into(),
        sender: "2@s.whatsapp.net".into(),
        sender_name: Some("n".repeat(5000)),
        summary: "é".repeat(2000),
        mentions: Vec::new(),
    });
    shared.emit(zapfast::backend::Event::Messages {
        chat: "1@s.whatsapp.net".into(),
        messages: vec![reply.clone()],
        older: false,
        complete: false,
    });
    shared.emit(zapfast::backend::Event::MessageUpdated(Box::new(reply)));
    let records = drain(&shared);
    assert_eq!(records.len(), 2, "{records:?}");
    let quoted = |m: &w::Message| m.quoted.clone().expect("the reply keeps its quote");
    let from_messages = match &records[0] {
        w::Event::Messages { messages, .. } => quoted(&messages[0]),
        other => panic!("{other:?}"),
    };
    let from_update = match &records[1] {
        w::Event::MessageUpdated { message } => quoted(message),
        other => panic!("{other:?}"),
    };
    for quote in [from_messages, from_update] {
        assert_cut(quote.summary.as_bytes(), 1024, "é");
        assert!(quote.sender_name.unwrap().len() <= 1024);
    }
}

#[test]
fn errors_toasts_and_previews_are_cut_not_dropped() {
    let shared = Shared::new(true).unwrap();
    shared.emit(zapfast::backend::Event::Error("e".repeat(5000)));
    shared.emit(zapfast::backend::Event::Info("é".repeat(5000)));
    let mut message = text_message("a", "see https://example.org".into());
    message.content = zapfast::model::Content::Text {
        text: "link".into(),
        preview: Some(zapfast::model::LinkPreview {
            url: "https://example.org".into(),
            title: Some("t".repeat(5000)),
            description: Some("d".repeat(9000)),
        }),
    };
    shared.emit(zapfast::backend::Event::MessageUpdated(Box::new(message)));
    let records = drain(&shared);
    assert_eq!(records.len(), 3, "{records:?}");
    match (&records[0], &records[1]) {
        (w::Event::Error { text: a }, w::Event::Info { text: b }) => {
            assert_eq!((a.len(), b.len() <= 4096), (4096, true));
            assert!(a.ends_with('…') && b.ends_with('…'));
        }
        other => panic!("{other:?}"),
    }
    match &records[2] {
        w::Event::MessageUpdated {
            message:
                w::Message {
                    content:
                        w::Content::Text {
                            preview: Some(preview),
                            ..
                        },
                    ..
                },
        } => {
            assert!(preview.title.as_ref().unwrap().len() <= 1024);
            assert!(preview.description.as_ref().unwrap().len() <= 4096);
        }
        other => panic!("{other:?}"),
    }
}

// ---- blobs of a dropped record are freed ---------------------------------

#[test]
fn a_dropped_standalone_message_frees_its_thumbnail_blob() {
    let shared = Shared::new(true).unwrap();
    let too_long = "x".repeat(196_609);
    shared.emit(zapfast::backend::Event::MessageUpdated(Box::new(
        text_message("a", too_long.clone()),
    )));
    shared.emit(zapfast::backend::Event::Incoming {
        chat: "1@s.whatsapp.net".into(),
        message: Box::new(text_message("b", too_long)),
    });
    let records = drain(&shared);
    assert_eq!(
        records,
        [
            w::Event::EventDropped {
                kind: 15,
                size: 196_609
            },
            w::Event::EventDropped {
                kind: 16,
                size: 196_609
            }
        ]
    );
    assert_eq!(shared.blobs.count(), 0, "the thumbnails leaked");
    // And a message that fits keeps its blob until the UI frees it.
    shared.emit(zapfast::backend::Event::MessageUpdated(Box::new(
        text_message("c", "fits".into()),
    )));
    assert_eq!(drain(&shared).len(), 1);
    assert_eq!(shared.blobs.count(), 1);
}

// ---- commands are checked before their blobs are taken -------------------

fn send_image(shared: &Shared, width: u32, height: u32, bytes: usize) -> (Result<(), String>, u64) {
    let rgba = shared.blobs.put(vec![0; bytes]);
    let held = shared.blobs.count() as u64;
    let result = crate::dto::command(
        shared,
        w::Command::SendImage {
            chat: "1@s.whatsapp.net".into(),
            width,
            height,
            rgba,
            caption: None,
            mentions: Vec::new(),
            quoting: None,
        },
    )
    .map(|_| ())
    .map_err(|e| format!("{e:?}"));
    (result, held)
}

#[test]
fn extreme_image_dimensions_are_refused_with_the_blob_untouched() {
    let shared = Shared::new(true).unwrap();
    // 2^31 × 2^31 × 4 wraps a u64 to zero, which an empty blob would match;
    // in a debug build the plain product panics.
    for (width, height) in [
        (1 << 31, 1 << 31),
        (u32::MAX, u32::MAX),
        (1 << 31, 2),
        (0, 0),
        (0, 5),
    ] {
        let (result, _) = send_image(&shared, width, height, 0);
        assert!(
            result.as_ref().is_err_and(|e| e.contains("Invalid")),
            "{width}×{height}: {result:?}"
        );
        assert_eq!(shared.blobs.count(), 1, "{width}×{height} took the blob");
        // Each round puts its own blob; clear them between rounds.
        for id in 1..=16 {
            shared.blobs.free(id);
        }
    }
    // A picture that is what it says goes through, and takes its blob.
    let (result, held) = send_image(&shared, 2, 3, 24);
    assert_eq!((result, held), (Ok(()), 1));
    assert_eq!(shared.blobs.count(), 0);
}

#[test]
fn extreme_pasted_picture_dimensions_are_refused_with_the_blob_untouched() {
    let shared = Shared::new(true).unwrap();
    let rgba = shared.blobs.put(Vec::new());
    let refused = crate::dto::command(
        &shared,
        w::Command::ApplyPictureEdit {
            target: 1,
            source: w::PictureSource::Pasted { rgba },
            width: 1 << 31,
            height: 1 << 31,
            crop: w::PictureCrop {
                x: 0,
                y: 0,
                width: 1,
                height: 1,
            },
            turns: 0,
        },
    );
    assert!(matches!(refused, Err(crate::dto::Refused::Invalid(_))));
    assert_eq!(shared.blobs.count(), 1);
}

// ---- decoders enforce what encoders do ------------------------------------

/// A `Messages` record of `count` messages of the longest text each.
/// Each text fits its field; three together pass the chunk's byte budget,
/// which the encoder refuses to write, so the decoder must refuse to read.
fn messages_record(count: u32) -> Vec<u8> {
    let one = |id: &str| {
        let message = crate::dto::message(
            &mut crate::dto::Out::new(&Shared::new(true).unwrap()),
            text_message(id, "a".repeat(196_608)),
        );
        let mut w = crate::wire::Writer::reuse(&mut Vec::new());
        crate::wire::Wire::put(&message, &mut w).unwrap();
        w.finish()
    };
    let mut record = Vec::new();
    record.extend_from_slice(&14u16.to_le_bytes());
    record.extend_from_slice(&0u16.to_le_bytes());
    record.extend_from_slice(&1u32.to_le_bytes()); // chat: "1"
    record.push(b'1');
    record.extend_from_slice(&1u32.to_le_bytes()); // batch
    record.extend_from_slice(&[1, 0, 0]); // last, older, complete
    record.extend_from_slice(&count.to_le_bytes());
    for id in ["a", "b", "c"].into_iter().take(count as usize) {
        record.extend_from_slice(&one(id));
    }
    assert!(record.len() < w::MAX_RECORD, "the record itself fits");
    record
}

#[test]
fn a_chunk_over_its_byte_budget_is_refused_though_every_field_fits() {
    assert_eq!(
        w::Event::decode(&messages_record(3)).unwrap_err().reason,
        "a chunk over its byte budget"
    );
    // Two are within the budget: they decode and re-encode to the same bytes.
    let two = messages_record(2);
    let event = w::Event::decode(&two).unwrap();
    let mut again = Vec::new();
    event.encode(&mut again).unwrap();
    assert_eq!(again, two);
}

#[test]
fn a_record_longer_than_max_record_is_refused_before_it_is_read() {
    let mut record = vec![0u8; w::MAX_RECORD + 1];
    record[0] = 22; // Syncing
    assert_eq!(
        w::Event::decode(&record).unwrap_err().reason,
        "a record over MAX_RECORD"
    );
    assert_eq!(
        w::Command::decode(&record).unwrap_err().reason,
        "a record over MAX_RECORD"
    );
}

// ---- the demo core answers or refuses every command -----------------------

use crate::cores::Core;
use crate::demo::{Disposition, disposition};
use crate::dto::Refused;

fn demo_core() -> std::sync::Arc<Core> {
    Core::open(w::OpenConfig {
        abi: w::ABI,
        schema_hash: w::SCHEMA_HASH,
        demo: true,
        state_dir: std::env::temp_dir(),
        cache_dir: std::env::temp_dir(),
        account: "1".into(),
        keyring_service: "ai.a3lab.zap.test".into(),
        device_name: String::new(),
    })
    .unwrap()
}

/// Everything the core queues until `done` holds, or five seconds pass.
fn events_until(
    shared: &Shared,
    mut seen: Vec<w::Event>,
    done: impl Fn(&[w::Event]) -> bool,
) -> Vec<w::Event> {
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(5);
    loop {
        seen.extend(drain(shared));
        if done(&seen) || std::time::Instant::now() > deadline {
            return seen;
        }
        std::thread::sleep(std::time::Duration::from_millis(10));
    }
}

const ADA: &str = "393331234567@s.whatsapp.net";

fn sent_message(events: &[w::Event]) -> Option<&w::Message> {
    events.iter().find_map(|e| match e {
        w::Event::Messages { messages, .. } => messages
            .iter()
            .find(|m| m.from_me && m.id.starts_with("DEMO")),
        _ => None,
    })
}

fn has_status(events: &[w::Event], status: w::Delivery) -> bool {
    events
        .iter()
        .any(|e| matches!(e, w::Event::MessageUpdated { message } if message.status == status))
}

#[test]
fn the_demo_core_sends_images_and_voice_notes_like_text() {
    let core = demo_core();
    let shared = &core.shared;
    let rgba = shared.blobs.put(vec![255; 4 * 3 * 2]);
    core.send(w::Command::SendImage {
        chat: ADA.into(),
        width: 3,
        height: 2,
        rgba,
        caption: Some("look".into()),
        mentions: Vec::new(),
        quoting: None,
    })
    .unwrap();
    let events = events_until(shared, Vec::new(), |e| has_status(e, w::Delivery::Read));
    let image = sent_message(&events).expect("the image was filed as a sent message");
    match &image.content {
        w::Content::Image { caption, media, .. } => {
            assert_eq!(caption.as_deref(), Some("look"));
            assert_eq!(
                (media.width, media.height, media.size),
                (Some(3), Some(2), 24)
            );
        }
        other => panic!("{other:?}"),
    }
    for status in [w::Delivery::Sent, w::Delivery::Delivered, w::Delivery::Read] {
        assert!(has_status(&events, status), "{status:?}");
    }
    assert_eq!(shared.blobs.count(), 0, "the command took its blob");

    // A second of audio.
    let samples: Vec<u8> = (0..48_000u32).flat_map(|_| 0.25f32.to_le_bytes()).collect();
    let samples = shared.blobs.put(samples);
    core.send(w::Command::SendVoice {
        chat: ADA.into(),
        samples,
        quoting: None,
    })
    .unwrap();
    let events = events_until(shared, Vec::new(), |e| {
        sent_message(e).is_some() && has_status(e, w::Delivery::Read)
    });
    match &sent_message(&events)
        .expect("the voice note was filed")
        .content
    {
        w::Content::Audio {
            voice_note,
            seconds,
            ..
        } => assert_eq!((*voice_note, *seconds), (true, Some(1))),
        other => panic!("{other:?}"),
    }
    core.shutdown();
}

/// A realistic command for each one the demo core answers, and what it
/// must queue in return.
type Produces = fn(&w::Event) -> bool;

fn answered() -> Vec<(w::Command, Produces)> {
    vec![
        (
            w::Command::LoadChat {
                chat: ADA.into(),
                before: None,
            },
            |e| matches!(e, w::Event::Messages { messages, .. } if !messages.is_empty()),
        ),
        (
            w::Command::SendText {
                chat: ADA.into(),
                text: "hi".into(),
                quoting: None,
                mentions: Vec::new(),
            },
            |e| matches!(e, w::Event::Messages { .. }),
        ),
        (
            w::Command::SendLocation {
                chat: ADA.into(),
                latitude: 1.0,
                longitude: 2.0,
                quoting: None,
            },
            |e| matches!(e, w::Event::Messages { .. }),
        ),
        (
            w::Command::MarkRead {
                chat: ADA.into(),
                receipts: true,
            },
            |e| matches!(e, w::Event::ChatUpdated { .. }),
        ),
        (
            w::Command::SetMuted {
                chat: ADA.into(),
                until: Some(0),
            },
            |e| matches!(e, w::Event::ChatUpdated { .. }),
        ),
        (
            w::Command::OpenLog {
                path: "/tmp/zapfast.log".into(),
            },
            |e| matches!(e, w::Event::OpenPath { path } if path.as_os_str() == "/tmp/zapfast.log"),
        ),
        (w::Command::Reconnect, |e| {
            matches!(
                e,
                w::Event::Link {
                    status: w::LinkStatus::Connected
                }
            )
        }),
        (
            w::Command::DemoIncoming {
                delay_ms: 0,
                chat: ADA.into(),
                sender: ADA.into(),
                sender_name: None,
                text: "hello".into(),
            },
            |e| matches!(e, w::Event::Incoming { .. }),
        ),
        (
            w::Command::DemoTyping {
                delay_ms: 0,
                chat: ADA.into(),
                sender: ADA.into(),
                composing: true,
            },
            |e| matches!(e, w::Event::Typing { .. }),
        ),
        (
            w::Command::WatchReceipts {
                target: Some(w::ReceiptTarget {
                    chat: ADA.into(),
                    message: "Ada-Lovelace-1".into(),
                }),
            },
            |e| matches!(e, w::Event::Receipts { recipients, .. } if recipients.len() == 1 && recipients[0].read_at.is_some()),
        ),
        (
            w::Command::FetchAvatar {
                id: ADA.into(),
                full: false,
            },
            |e| matches!(e, w::Event::Avatar { path: Some(path), .. } if path.is_file()),
        ),
        (
            w::Command::ListChatShared {
                chat: ADA.into(),
                request: 7,
            },
            |e| matches!(e, w::Event::ChatShared { request: 7, error: None, last: true, entries, .. } if entries.len() == 9),
        ),
        (
            w::Command::LoadStorageStats { token: 3 },
            |e| matches!(e, w::Event::StorageStats { stats: Some(s), token: 3, cleared: false } if s.images > 0),
        ),
        (
            w::Command::ClearMediaCache { token: 4 },
            |e| matches!(e, w::Event::StorageStats { stats: Some(s), token: 4, cleared: true } if s.images == 0),
        ),
        (
            w::Command::FetchAccountPrivacy,
            |e| matches!(e, w::Event::AccountPrivacy { values, failed: false } if values.len() == 7),
        ),
        (
            w::Command::SetAccountPrivacy {
                kind: w::PrivacyKind::About,
                choice: w::PrivacyChoice::Nobody,
            },
            |e| {
                matches!(
                    e,
                    w::Event::AccountPrivacySaved {
                        kind: w::PrivacyKind::About
                    }
                )
            },
        ),
        (
            w::Command::StartCall {
                chat: ADA.into(),
                video: false,
                generation: 1 << 48,
                cancel: 1,
            },
            |e| matches!(e, w::Event::Call { update } if update.generation == 1 << 48 && update.phase == w::CallPhase::Dialing),
        ),
        (
            w::Command::JoinCallLink {
                token: "abc".into(),
                video: true,
                generation: (1 << 48) + 1,
                cancel: 2,
            },
            |e| matches!(e, w::Event::Call { update } if update.phase == w::CallPhase::Connecting && update.group.as_ref().is_some_and(|g| g.link)),
        ),
        (
            w::Command::RefreshCallDevices,
            |e| matches!(e, w::Event::CallDevices { devices } if devices.cameras.len() == 2),
        ),
        (
            w::Command::LoadCallLog,
            |e| matches!(e, w::Event::CallLog { messages, last: true, .. } if messages.len() == 5),
        ),
        (
            w::Command::VotePoll {
                chat: "14155550199@s.whatsapp.net".into(),
                message: "DEMOPOLL0".into(),
                choices: vec![2],
            },
            |e| matches!(e, w::Event::MessageUpdated { message } if matches!(&message.content, w::Content::Poll { state, .. } if state.counts == [2, 1, 1] && state.voters == 4 && state.selected == [2])),
        ),
        (
            w::Command::SetLocked {
                chat: ADA.into(),
                locked: true,
            },
            |e| matches!(e, w::Event::ChatUpdated { chat } if chat.id == ADA && chat.locked),
        ),
        (
            w::Command::EnsureChat {
                chat: "4930123456@s.whatsapp.net".into(),
                name: "Dorothy Vaughan".into(),
            },
            |e| matches!(e, w::Event::ChatUpdated { chat } if chat.name == "Dorothy Vaughan"),
        ),
        (
            w::Command::LoadChatMedia {
                chat: ADA.into(),
                around: Some("Ada-media-0".into()),
            },
            |e| matches!(e, w::Event::ChatMedia { items, last: true, .. } if items.len() == 4 && items.iter().filter(|i| i.video).count() == 1 && items.iter().all(|i| i.path.as_ref().is_some_and(|p| p.is_file()))),
        ),
        (
            w::Command::SearchGifs {
                query: "rocket".into(),
                key: "any".into(),
            },
            |e| matches!(e, w::Event::Gifs { error: None, gifs, .. } if gifs.len() == 1 && gifs[0].still.as_ref().is_some_and(|p| p.is_file())),
        ),
        (
            w::Command::SendGif {
                chat: ADA.into(),
                gif: w::Gif {
                    id: "demo-gif-pug".into(),
                    still: None,
                    mp4: String::new(),
                    width: 320,
                    height: 240,
                },
                quoting: None,
            },
            |e| matches!(e, w::Event::Messages { messages, .. } if messages.iter().any(|m| m.from_me && m.thumbnail.is_some() && matches!(&m.content, w::Content::Video { gif: true, media, .. } if media.path.is_some()))),
        ),
        (
            w::Command::RecentStickers,
            |e| matches!(e, w::Event::Stickers { last: true, entries, .. } if entries.iter().filter(|e| matches!(e, w::StickerEntry::Recent { .. })).count() == 4),
        ),
        (
            w::Command::SendSticker {
                chat: ADA.into(),
                path: crate::demo::media_dir().join("stickers/owl.webp"),
                quoting: None,
            },
            |e| matches!(e, w::Event::Stickers { entries, .. } if matches!(entries.iter().find(|e| matches!(e, w::StickerEntry::Recent { .. })), Some(w::StickerEntry::Recent { path }) if path.ends_with("owl.webp"))),
        ),
        (
            w::Command::SaveSticker {
                path: crate::demo::media_dir().join("stickers/goat.webp"),
            },
            |e| matches!(e, w::Event::Stickers { entries, .. } if entries.iter().any(|e| matches!(e, w::StickerEntry::Favorite { path } if path.ends_with("goat.webp")))),
        ),
        (
            w::Command::RemoveRecentSticker {
                path: crate::demo::media_dir().join("stickers/pug.webp"),
            },
            |e| matches!(e, w::Event::Stickers { entries, .. } if entries.iter().filter(|e| matches!(e, w::StickerEntry::Recent { .. })).count() == 3),
        ),
        (
            w::Command::ForgetSticker {
                path: crate::demo::media_dir().join("stickers/owl.webp"),
            },
            |e| matches!(e, w::Event::Stickers { entries, .. } if entries.iter().filter(|e| matches!(e, w::StickerEntry::Favorite { .. })).count() == 1),
        ),
        (
            w::Command::SendStickerPack {
                chat: ADA.into(),
                dir: crate::demo::media_dir().join("stickers"),
            },
            |e| matches!(e, w::Event::Messages { messages, .. } if messages.iter().any(|m| m.from_me && matches!(&m.content, w::Content::StickerPack { count: 8, .. }))),
        ),
        (
            w::Command::ViewStickerPack {
                chat: "120363012345678901@g.us".into(),
                message: "DEMOPACK0".into(),
            },
            |e| matches!(e, w::Event::StickerPackPreview { result: w::StickerPackResult::Ok { publisher, .. } } if publisher == "Grace Hopper"),
        ),
        (
            w::Command::AddStickerPack {
                dir: crate::demo::media_dir().join("stickers"),
                name: "Launch party".into(),
            },
            |e| matches!(e, w::Event::Info { .. }),
        ),
    ]
}

#[test]
fn every_command_the_demo_core_answers_produces_events() {
    let mut named: Vec<&str> = Vec::new();
    for (command, produced) in answered() {
        assert_eq!(disposition(&command), Disposition::Answers);
        let name = command.name();
        named.push(name);
        let core = demo_core();
        core.send(command).unwrap();
        let events = events_until(&core.shared, Vec::new(), |e| e.iter().any(produced));
        // The demo's own opening events do not count.
        assert!(
            events.iter().any(produced),
            "{name} produced no events: {events:?}"
        );
        core.shutdown();
    }
    // SendImage and SendVoice are covered above; DemoPanic by the slice;
    // the controls of a call by the call tests below.
    named.extend([
        "SendImage",
        "SendVoice",
        "DemoPanic",
        "AnswerCall",
        "DeclineCall",
        "HangupCall",
        "SetCallMuted",
        "SetCallCamera",
        "SetCallMicrophone",
        "SetCallSpeaker",
        "SetCallCameraDevice",
    ]);
    for (command, _) in samples::commands(&mut Seed::new(0, false)) {
        if disposition(&command) == Disposition::Answers {
            assert!(
                named.contains(&command.name()),
                "{} is untested",
                command.name()
            );
        }
    }
}

#[test]
fn every_other_command_is_accepted_silently_by_design_or_refused_by_name() {
    let core = demo_core();
    let mut refused = 0;
    for (command, _) in sample_commands().into_iter().map(|c| (c, ())) {
        let name = command.name();
        let disposition = disposition(&command);
        if name == "DemoPanic" {
            continue;
        }
        let result = core.send(command);
        match disposition {
            Disposition::Refuses => {
                refused += 1;
                assert!(
                    matches!(result, Err(Refused::Unsupported(n)) if n == name),
                    "{name}: {result:?}"
                );
            }
            // Accepted, or refused only for what the sample's fields say
            // (a blob that is not held), never as unsupported.
            Disposition::Answers | Disposition::Accepts => {
                assert!(!matches!(result, Err(Refused::Unsupported(_))), "{name}");
            }
        }
    }
    assert!(refused > 60, "{refused}");
    core.shutdown();
}

#[test]
fn a_refused_demo_command_leaves_its_blob_held() {
    let core = demo_core();
    let jpeg = core.shared.blobs.put(vec![1, 2, 3]);
    let result = core.send(w::Command::SetGroupPicture {
        chat: "120363012345678901@g.us".into(),
        jpeg: Some(jpeg),
    });
    assert!(matches!(
        result,
        Err(Refused::Unsupported("SetGroupPicture"))
    ));
    assert_eq!(core.shared.blobs.count(), 1);
    core.shutdown();
}

// ---- the file-dialog bridge ------------------------------------------------

/// The worker's thread waits for the answer, so a request is never dropped:
/// what is over a bound is cut, and the UI's answer reaches upstream.
#[test]
fn a_pick_request_with_oversized_text_is_cut_and_still_delivered() {
    let shared = Shared::new(true).unwrap();
    shared.emit(zapfast::backend::Event::PickRequest(
        zapfast::pick::Request {
            id: 7,
            kind: zapfast::pick::Kind::Save,
            title: "t".repeat(5000),
            filters: (0..40)
                .map(|i| (format!("f{i}").repeat(300), vec!["e".repeat(100); 50]))
                .collect(),
            file_name: Some("n".repeat(5000)),
            directory: Some(PathBuf::from("/".repeat(5000))),
        },
    ));
    let records = drain(&shared);
    assert_eq!(records.len(), 1, "{records:?}");
    match &records[0] {
        w::Event::PickRequest {
            id: 7,
            kind: w::PickKind::Save,
            title,
            filters,
            file_name,
            directory,
        } => {
            assert!(title.len() <= 1024 && title.ends_with('…'));
            assert_eq!(filters.len(), 16);
            assert!(filters.iter().all(|f| f.name.len() <= 256
                && f.extensions.len() == 32
                && f.extensions.iter().all(|e| e.len() <= 32)));
            assert!(file_name.as_ref().unwrap().len() <= 1024);
            assert_eq!(directory, &None);
        }
        other => panic!("{other:?}"),
    }
}

#[test]
fn a_pick_answer_becomes_upstreams_command() {
    let shared = Shared::new(true).unwrap();
    let routed = crate::dto::command(
        &shared,
        w::Command::PickAnswer {
            id: 7,
            paths: vec![PathBuf::from("/tmp/a.png")],
        },
    );
    match routed {
        Ok(crate::dto::Routed::Upstream(zapfast::backend::Command::PickAnswer { id, paths })) => {
            assert_eq!((id, paths), (7, vec![PathBuf::from("/tmp/a.png")]));
        }
        _ => panic!("expected upstream's PickAnswer"),
    }
}

// ---- mentions round-trip ---------------------------------------------------

/// Text of exactly `n` bytes.
fn bytes(n: usize) -> String {
    "m".repeat(n)
}

/// A message at its largest: the longest text, and `mentions` in it and in
/// the message it quotes, every field at its bound.
fn largest_message(mentions: &[String]) -> zapfast::model::Message {
    let refs: Vec<zapfast::model::MentionRef> = mentions
        .iter()
        .map(|m| zapfast::model::MentionRef {
            user: m.clone(),
            id: m.clone(),
        })
        .collect();
    let mut message = text_message(&bytes(128), bytes(196_608));
    message.thumbnail = None;
    message.mentions = refs.clone();
    message.quoted = Some(zapfast::model::Quoted {
        chat: Some(bytes(128)),
        id: bytes(128),
        sender: bytes(128),
        sender_name: Some(bytes(1024)),
        summary: bytes(1024),
        mentions: refs,
    });
    message
}

fn mention_counts(events: &[w::Event]) -> Vec<(usize, usize)> {
    let counts = |m: &w::Message| (m.mentions.len(), m.quoted.as_ref().unwrap().mentions.len());
    events
        .iter()
        .map(|event| match event {
            w::Event::MessageUpdated { message } => counts(message),
            w::Event::Messages { messages, .. } => counts(&messages[0]),
            other => panic!("expected the message, got {other:?}"),
        })
        .collect()
}

#[test]
fn every_mention_an_edit_sends_comes_back_in_the_message_and_its_quote() {
    let shared = Shared::new(true).unwrap();
    let most = crate::dto::MENTIONS;
    // Mention ids at their own bound, as many as a command may carry.
    let ids: Vec<String> = (0..most).map(|n| format!("{n:0>123}@lid")).collect();
    assert!(ids.iter().all(|id| id.len() == 127));
    let routed = crate::dto::command(
        &shared,
        w::Command::EditText {
            chat: bytes(128),
            id: bytes(128),
            text: bytes(196_608),
            mentions: ids.clone(),
        },
    );
    let Ok(crate::dto::Routed::Upstream(zapfast::backend::Command::EditText { mentions, .. })) =
        routed
    else {
        panic!("expected upstream's EditText");
    };
    assert_eq!(mentions, ids);

    // The edited message, quoting another with as many: both events carry
    // every mention.
    let message = largest_message(&mentions);
    shared.emit(zapfast::backend::Event::MessageUpdated(Box::new(
        message.clone(),
    )));
    shared.emit(zapfast::backend::Event::Messages {
        chat: bytes(128),
        messages: vec![message],
        older: false,
        complete: false,
    });
    assert_eq!(
        mention_counts(&drain(&shared)),
        [(most, most), (most, most)]
    );

    // A message received with more than a command can send keeps the first
    // ones, never losing the message.
    let more: Vec<String> = (0..most + 44).map(|n| format!("{n:0>123}@lid")).collect();
    shared.emit(zapfast::backend::Event::MessageUpdated(Box::new(
        largest_message(&more),
    )));
    let events = drain(&shared);
    assert_eq!(mention_counts(&events), [(most, most)]);
    let w::Event::MessageUpdated { message } = &events[0] else {
        unreachable!()
    };
    assert_eq!(message.mentions[0].user, more[0]);
}

// ---- call cancel flags -----------------------------------------------------

fn start_call(shared: &Shared, token: u64) -> Result<crate::dto::Routed, Refused> {
    crate::dto::command(
        shared,
        w::Command::StartCall {
            chat: ADA.into(),
            video: false,
            generation: token,
            cancel: token,
        },
    )
}

/// The flag upstream's StartCall carries.
fn flag(routed: &crate::dto::Routed) -> &std::sync::Arc<std::sync::atomic::AtomicBool> {
    match routed {
        crate::dto::Routed::Upstream(zapfast::backend::Command::StartCall { cancel, .. }) => cancel,
        _ => panic!("expected upstream's StartCall"),
    }
}

fn cancel_call(shared: &Shared, token: u64) -> Result<crate::dto::Routed, Refused> {
    crate::dto::command(shared, w::Command::CancelCall { token })
}

#[test]
fn a_call_still_queued_is_cancelled_after_many_others_came_and_went() {
    let shared = Shared::new(false).unwrap();
    // Queued, holding its flag, while 40 later calls are placed and finish.
    let queued = start_call(&shared, 1).unwrap();
    for token in 2..42 {
        drop(start_call(&shared, token).unwrap());
    }
    assert!(cancel_call(&shared, 1).is_ok());
    assert!(flag(&queued).load(std::sync::atomic::Ordering::SeqCst));
}

#[test]
fn with_every_flag_held_a_new_call_is_refused_and_the_held_ones_still_cancel() {
    let shared = Shared::new(false).unwrap();
    let held: Vec<_> = (1..=16)
        .map(|token| start_call(&shared, token).unwrap())
        .collect();
    assert!(matches!(start_call(&shared, 17), Err(Refused::Invalid(_))));
    assert!(matches!(cancel_call(&shared, 18), Err(Refused::Invalid(_))));
    for token in 1..=16 {
        assert!(cancel_call(&shared, token).is_ok());
    }
    assert!(
        held.iter()
            .all(|routed| flag(routed).load(std::sync::atomic::Ordering::SeqCst))
    );
    // Once a call lets go of its flag, a new one has room.
    drop(held);
    assert!(start_call(&shared, 17).is_ok());
}

// ---- the demo core's calls ------------------------------------------------

fn call_in(events: &[w::Event], phase: w::CallPhase) -> Option<&w::CallUpdate> {
    events.iter().rev().find_map(|e| match e {
        w::Event::Call { update } if update.phase == phase => Some(update),
        _ => None,
    })
}

fn wait_phase(core: &Core, phase: w::CallPhase) -> w::CallUpdate {
    let events = events_until(&core.shared, Vec::new(), |e| call_in(e, phase).is_some());
    call_in(&events, phase)
        .unwrap_or_else(|| panic!("no {phase:?}: {events:?}"))
        .clone()
}

/// Waits for a frame on `stream` and checks it is a whole I420 picture;
/// the events drained meanwhile go to `seen`.
fn wait_frame(core: &Core, stream: u32, seen: &mut Vec<w::Event>) -> (usize, usize) {
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(5);
    loop {
        seen.extend(drain(&core.shared));
        if let Ok(frame) = core.shared.frames.acquire(stream) {
            let [w, h] = frame.picture.size;
            let s = frame.picture.strides;
            assert_eq!(s, [w, w.div_ceil(2), w.div_ceil(2)]);
            assert_eq!(frame.picture.data.len(), w * h + 2 * s[1] * h.div_ceil(2));
            core.shared.frames.release(stream);
            return (w, h);
        }
        assert!(
            std::time::Instant::now() < deadline,
            "no frame on stream {stream}"
        );
        std::thread::sleep(std::time::Duration::from_millis(10));
    }
}

#[test]
fn a_placed_video_call_rings_connects_streams_and_is_filed_when_hung_up() {
    let core = demo_core();
    core.send(w::Command::StartCall {
        chat: ADA.into(),
        video: true,
        generation: 1 << 48,
        cancel: 11,
    })
    .unwrap();
    for phase in [w::CallPhase::Dialing, w::CallPhase::Ringing] {
        assert_eq!(wait_phase(&core, phase).generation, 1 << 48);
    }
    let active = wait_phase(&core, w::CallPhase::Active);
    assert!(active.camera_on && active.video && active.active_for_ms.is_some());
    let mut seen = Vec::new();
    assert_eq!(
        wait_frame(&core, crate::frames::LOCAL, &mut seen),
        (320, 180)
    );
    assert_eq!(
        wait_frame(&core, crate::frames::REMOTE, &mut seen),
        (640, 360)
    );
    let flagged = events_until(&core.shared, seen, |e| {
        e.iter()
            .any(|e| matches!(e, w::Event::Call { update } if update.remote_video))
    });
    assert!(
        flagged
            .iter()
            .any(|e| matches!(e, w::Event::Call { update } if update.remote_video)),
        "the peer's picture is flagged"
    );
    assert!(
        core.shared.frames.acquire(crate::frames::MEMBER).is_err(),
        "no members on a one-to-one call"
    );

    // Mute and the camera come back in the call's state.
    core.send(w::Command::SetCallMuted { muted: true }).unwrap();
    core.send(w::Command::SetCallCamera { on: false }).unwrap();
    core.send(w::Command::SetCallSpeaker {
        device: Some("desk-speakers".into()),
    })
    .unwrap();
    let events = events_until(&core.shared, Vec::new(), |e| {
        e.iter().any(|e| {
            matches!(e, w::Event::Call { update } if update.muted && !update.camera_on && update.speaker.as_deref() == Some("desk-speakers"))
        })
    });
    assert!(
        events
            .iter()
            .any(|e| matches!(e, w::Event::Call { update } if update.muted && !update.camera_on))
    );
    assert!(
        core.shared.frames.acquire(crate::frames::LOCAL).is_err(),
        "our camera is off"
    );

    core.send(w::Command::HangupCall).unwrap();
    let events = events_until(&core.shared, Vec::new(), |e| {
        e.iter().any(|e| matches!(e, w::Event::Messages { .. }))
    });
    let ended = call_in(&events, w::CallPhase::Ended).expect("ended");
    assert_eq!(ended.outcome, Some(w::CallStatus::Answered));
    let filed = events
        .iter()
        .find_map(|e| match e {
            w::Event::Messages { chat, messages, .. } if chat == ADA => messages.first(),
            _ => None,
        })
        .expect("the call is filed in its chat");
    assert!(matches!(
        filed.content,
        w::Content::Call {
            video: true,
            status: w::CallStatus::Answered,
            group: false,
            ..
        }
    ));
    assert!(filed.from_me);
    // No frames once the call is over, and the log has it first.
    std::thread::sleep(std::time::Duration::from_millis(150));
    drain(&core.shared);
    assert!(core.shared.frames.acquire(crate::frames::REMOTE).is_err());
    core.send(w::Command::LoadCallLog).unwrap();
    let events = events_until(&core.shared, Vec::new(), |e| {
        e.iter().any(|e| matches!(e, w::Event::CallLog { .. }))
    });
    let log = events
        .iter()
        .find_map(|e| match e {
            w::Event::CallLog { messages, .. } => Some(messages),
            _ => None,
        })
        .unwrap();
    assert_eq!(log.len(), 6);
    assert_eq!(log[0].id, filed.id);
    core.shutdown();
}

#[test]
fn a_rung_in_group_video_call_is_answered_with_members_on_their_streams() {
    let core = demo_core();
    core.send(w::Command::DemoIncoming {
        delay_ms: 0,
        chat: "120363012345678901@g.us".into(),
        sender: "441632960123@s.whatsapp.net".into(),
        sender_name: None,
        text: crate::demo::VIDEO_CALL_TEXT.into(),
    })
    .unwrap();
    let ringing = wait_phase(&core, w::CallPhase::Incoming);
    assert_eq!(ringing.direction, w::CallDirection::Incoming);
    let group = ringing.group.as_ref().expect("a group call");
    assert_eq!(group.caller.as_deref(), Some("441632960123@s.whatsapp.net"));
    assert_eq!(group.participants.len(), 3);
    assert!(ringing.generation < 1 << 48, "numbered by the core");
    core.send(w::Command::AnswerCall).unwrap();
    assert_eq!(
        wait_phase(&core, w::CallPhase::Accepted).generation,
        ringing.generation
    );
    wait_phase(&core, w::CallPhase::Active);
    let mut seen = Vec::new();
    assert_eq!(
        wait_frame(&core, crate::frames::MEMBER, &mut seen),
        (320, 180)
    );
    assert_eq!(
        wait_frame(&core, crate::frames::MEMBER + 1, &mut seen),
        (320, 180)
    );
    assert!(core.shared.frames.acquire(crate::frames::REMOTE).is_err());
    let events = events_until(&core.shared, seen, |e| {
        e.iter().any(|e| matches!(e, w::Event::Call { update } if update.group.as_ref().is_some_and(|g| g.participants.iter().filter(|p| p.video).count() == 2)))
    });
    assert!(
        events.iter().any(|e| matches!(e, w::Event::Call { update } if update.group.as_ref().is_some_and(|g| g.participants.iter().filter(|p| p.video).count() == 2))),
        "both members' pictures are flagged"
    );
    // At most one frame a stream is held.
    let held = core.shared.frames.acquire(crate::frames::MEMBER).unwrap();
    assert_eq!(
        core.shared.frames.acquire(crate::frames::MEMBER).err(),
        Some(crate::frames::AcquireError::Held)
    );
    drop(held);
    core.shared.frames.release(crate::frames::MEMBER);
    core.send(w::Command::HangupCall).unwrap();
    let ended = wait_phase(&core, w::CallPhase::Ended);
    assert_eq!(ended.outcome, Some(w::CallStatus::Answered));
    core.shutdown();
}

#[test]
fn a_rung_in_call_can_be_declined_and_a_cancelled_one_is_never_placed() {
    let core = demo_core();
    core.send(w::Command::DemoIncoming {
        delay_ms: 0,
        chat: ADA.into(),
        sender: ADA.into(),
        sender_name: None,
        text: crate::demo::CALL_TEXT.into(),
    })
    .unwrap();
    let ringing = wait_phase(&core, w::CallPhase::Incoming);
    assert!(!ringing.video && ringing.group.is_none());
    // A call placed while one rings is refused as busy.
    core.send(w::Command::StartCall {
        chat: ADA.into(),
        video: false,
        generation: 1 << 50,
        cancel: 21,
    })
    .unwrap();
    let busy = wait_phase(&core, w::CallPhase::Failed);
    assert_eq!(busy.generation, 1 << 50);
    core.send(w::Command::DeclineCall).unwrap();
    let declined = wait_phase(&core, w::CallPhase::Ended);
    assert_eq!(declined.outcome, Some(w::CallStatus::Declined));

    core.send(w::Command::StartCall {
        chat: ADA.into(),
        video: false,
        generation: 1 << 51,
        cancel: 22,
    })
    .unwrap();
    core.send(w::Command::CancelCall { token: 22 }).unwrap();
    let failed = wait_phase(&core, w::CallPhase::Failed);
    assert_eq!(failed.generation, 1 << 51);
    assert_eq!(failed.outcome, Some(w::CallStatus::Failed));
    core.shutdown();
}

#[test]
fn call_generations_from_the_abi_grow_and_are_never_zero() {
    let a = crate::zf_call_generation();
    let b = crate::zf_call_generation();
    assert!(a > 0 && b > a);
}

// ---- voice notes ----------------------------------------------------------

fn voice_paths(events: &[w::Event]) -> Vec<(String, std::path::PathBuf, Option<u32>, usize)> {
    events
        .iter()
        .flat_map(|e| match e {
            w::Event::Messages { messages, .. } => messages.as_slice(),
            _ => &[],
        })
        .filter_map(|m| match &m.content {
            w::Content::Audio {
                media,
                seconds,
                voice_note: true,
                waveform,
            } => media
                .path
                .clone()
                .map(|path| (m.id.clone(), path, *seconds, waveform.len())),
            _ => None,
        })
        .collect()
}

#[test]
fn the_demo_chats_have_voice_notes_that_decode_and_nothing_outside_decodes() {
    let core = demo_core();
    core.send(w::Command::LoadChat {
        chat: ADA.into(),
        before: None,
    })
    .unwrap();
    let events = events_until(&core.shared, Vec::new(), |e| !voice_paths(e).is_empty());
    let (id, path, seconds, bars) = voice_paths(&events).pop().expect("Ada's voice note");
    assert_eq!((id.as_str(), seconds, bars), ("DEMOVOICE0", Some(4), 64));
    assert!(path.is_file(), "{path:?}");
    let decoded = core.decode_voice(&path).unwrap();
    let rate = u64::from(crate::voice::RATE);
    assert!(
        (4 * rate..5 * rate).contains(&decoded.frames),
        "{}",
        decoded.frames
    );
    assert!(decoded.waveform.iter().any(|&bar| bar > 80));
    core.shared.blobs.free(decoded.blob.id);

    for outside in [
        std::path::PathBuf::from("/etc/hosts"),
        std::env::temp_dir().join("zapfast-demo-avatars/ada.jpg"),
        path.join("../../zapfast-demo-avatars/ada.jpg"),
    ] {
        let refused = core.decode_voice(&outside).unwrap_err();
        assert!(
            matches!(
                refused,
                crate::voice::VoiceError::Outside | crate::voice::VoiceError::NotFound
            ),
            "{outside:?}: {refused:?}"
        );
    }
    core.shutdown();
}

#[test]
fn a_sent_voice_note_is_encoded_where_it_plays_back_and_a_too_long_one_is_refused() {
    let core = demo_core();
    let samples: Vec<u8> = crate::demo::speech(2.0, 180.0)
        .iter()
        .flat_map(|s| s.to_le_bytes())
        .collect();
    let samples = core.shared.blobs.put(samples);
    core.send(w::Command::SendVoice {
        chat: ADA.into(),
        samples,
        quoting: None,
    })
    .unwrap();
    let events = events_until(&core.shared, Vec::new(), |e| {
        voice_paths(e)
            .iter()
            .any(|(id, ..)| id.starts_with("DEMO0"))
    });
    let (_, path, seconds, bars) = voice_paths(&events)
        .into_iter()
        .find(|(id, ..)| id.starts_with("DEMO0"))
        .expect("the sent note");
    assert_eq!((seconds, bars), (Some(2), 64));
    let decoded = core.decode_voice(&path).unwrap();
    assert!(decoded.frames >= 2 * u64::from(crate::voice::RATE));

    // Over 15 minutes is refused before the blob is looked at.
    let long = crate::wire::BlobRef {
        id: 9_999,
        len: (15 * 60 * 48_000 + 1) * 4,
    };
    assert!(matches!(
        core.send(w::Command::SendVoice {
            chat: ADA.into(),
            samples: long,
            quoting: None,
        }),
        Err(Refused::Invalid(_))
    ));
    core.shutdown();
    assert!(
        !path.exists(),
        "a stopped demo removes the notes it was sent"
    );
}

// ---- quotes and mentions on our sends ---------------------------------------

/// The first message of ours the demo filed with id `DEMO…`, past `skip`.
fn sent_after(events: &[w::Event], skip: usize) -> Option<&w::Message> {
    events
        .iter()
        .filter_map(|e| match e {
            w::Event::Messages { messages, .. } => Some(messages),
            _ => None,
        })
        .flatten()
        .filter(|m| m.from_me && m.id.starts_with("DEMO0"))
        .nth(skip)
}

#[test]
fn a_reply_carries_its_quote_and_mentions_and_a_cross_chat_quote_its_source() {
    const GRACE: &str = "441632960123@s.whatsapp.net";
    const BERLIN: &str = "120363012345678901@g.us";
    let core = demo_core();
    core.send(w::Command::SendText {
        chat: ADA.into(),
        text: "@393331234567 here it is".into(),
        quoting: Some(w::ReplyTarget {
            id: "Ada-Lovelace-2".into(),
            chat: None,
        }),
        mentions: vec![ADA.into()],
    })
    .unwrap();
    // Grace's line, quoted into Rust Berlin.
    core.send(w::Command::SendLocation {
        chat: BERLIN.into(),
        latitude: 52.52,
        longitude: 13.405,
        quoting: Some(w::ReplyTarget {
            id: "Grace-Hopper-1".into(),
            chat: Some(GRACE.into()),
        }),
    })
    .unwrap();
    // A target the demo does not have: sent, unquoted.
    core.send(w::Command::SendText {
        chat: ADA.into(),
        text: "and this".into(),
        quoting: Some(w::ReplyTarget {
            id: "no-such-message".into(),
            chat: None,
        }),
        mentions: Vec::new(),
    })
    .unwrap();
    let events = events_until(&core.shared, Vec::new(), |e| sent_after(e, 2).is_some());

    let reply = sent_after(&events, 0).expect("the reply");
    assert_eq!(
        reply.quoted,
        Some(w::Quoted {
            chat: None,
            id: "Ada-Lovelace-2".into(),
            sender: ADA.into(),
            sender_name: None,
            summary: "That is wonderful news. Send me the branch when you can.".into(),
            mentions: Vec::new(),
        })
    );
    assert_eq!(
        reply.mentions,
        vec![w::MentionRef {
            user: "393331234567".into(),
            id: ADA.into(),
        }]
    );

    let location = sent_after(&events, 1).expect("the location");
    let quoted = location.quoted.as_ref().expect("its quote");
    assert_eq!(
        (
            quoted.chat.as_deref(),
            quoted.id.as_str(),
            quoted.sender.as_str()
        ),
        (Some(GRACE), "Grace-Hopper-1", GRACE)
    );
    assert_eq!(quoted.summary, "Literally?");

    assert_eq!(sent_after(&events, 2).expect("the third").quoted, None);
    core.shutdown();
}

// ---- stickers, groups and receipts ------------------------------------------

fn recent_of(events: &[w::Event]) -> Option<Vec<std::path::PathBuf>> {
    events.iter().rev().find_map(|e| match e {
        w::Event::Stickers { entries, .. } => Some(
            entries
                .iter()
                .filter_map(|e| match e {
                    w::StickerEntry::Recent { path } => Some(path.clone()),
                    _ => None,
                })
                .collect(),
        ),
        _ => None,
    })
}

#[test]
fn a_sticker_removed_from_recent_rejoins_it_when_sent_again_from_its_chat() {
    let core = demo_core();
    let pug = crate::demo::media_dir().join("stickers/pug.webp");
    core.send(w::Command::RemoveRecentSticker { path: pug.clone() })
        .unwrap();
    let events = events_until(&core.shared, Vec::new(), |e| recent_of(e).is_some());
    let recent = recent_of(&events).unwrap();
    assert!(!recent.contains(&pug), "{recent:?}");
    core.send(w::Command::SendSticker {
        chat: ADA.into(),
        path: pug.clone(),
        quoting: Some(w::ReplyTarget {
            id: "Ada-Lovelace-2".into(),
            chat: None,
        }),
    })
    .unwrap();
    let events = events_until(&core.shared, Vec::new(), |e| {
        recent_of(e).is_some() && sent_after(e, 0).is_some()
    });
    assert_eq!(recent_of(&events).unwrap().first(), Some(&pug));
    let sent = sent_after(&events, 0).unwrap();
    assert!(
        matches!(&sent.content, w::Content::Sticker { media, animated: false } if media.path.as_ref() == Some(&pug))
    );
    assert!(sent.quoted.is_some(), "a sticker quotes its reply too");
    core.shutdown();
}

#[test]
fn the_groups_have_their_members_and_receipts_have_partial_and_unrecorded_cases() {
    const BERLIN: &str = "120363012345678901@g.us";
    const FAMILY: &str = "120363098765432109@g.us";
    let core = demo_core();
    let events = events_until(&core.shared, Vec::new(), |e| {
        e.iter()
            .any(|e| matches!(e, w::Event::Chats { last: true, .. }))
    });
    let chats: Vec<&w::Chat> = events
        .iter()
        .filter_map(|e| match e {
            w::Event::Chats { chats, .. } => Some(chats),
            _ => None,
        })
        .flatten()
        .collect();
    let berlin = chats.iter().find(|c| c.id == BERLIN).unwrap();
    assert_eq!(berlin.participants.len(), 4, "us and three");
    assert!(berlin.participants.iter().any(|p| p == crate::demo::ME));
    assert!(berlin.admin && berlin.info_locked == Some(true));
    assert!(
        berlin
            .group_description
            .as_deref()
            .is_some_and(|d| !d.is_empty())
    );
    let family = chats.iter().find(|c| c.id == FAMILY).unwrap();
    assert!(!family.admin && family.info_locked == Some(false));

    let receipts = |chat: &str, message: &str| {
        core.send(w::Command::WatchReceipts {
            target: Some(w::ReceiptTarget {
                chat: chat.into(),
                message: message.into(),
            }),
        })
        .unwrap();
        let events = events_until(&core.shared, Vec::new(), |e| {
            e.iter().any(|e| matches!(e, w::Event::Receipts { .. }))
        });
        events
            .into_iter()
            .find_map(|e| match e {
                w::Event::Receipts { recipients, .. } => Some(recipients),
                _ => None,
            })
            .unwrap()
    };
    const GRACE: &str = "441632960123@s.whatsapp.net";
    const KATHERINE: &str = "4915112345678@s.whatsapp.net";
    let by = |list: &[w::Recipient], id: &str| list.iter().find(|r| r.id == id).unwrap().clone();
    // A saved audience with a recipient outside it.
    let partial = receipts(BERLIN, "Rust-Berlin-1");
    assert_eq!(partial.len(), 3);
    let (ada, grace, katherine) = (
        by(&partial, ADA),
        by(&partial, GRACE),
        by(&partial, KATHERINE),
    );
    assert!(ada.expected && ada.read_at.is_some());
    assert!(grace.expected && grace.delivered_at.is_some() && grace.read_at.is_none());
    assert!(
        !katherine.expected && katherine.delivered_at.is_none(),
        "outside the audience"
    );
    // No saved audience, some receipts: upstream's "not recorded" note.
    let unsaved = receipts(BERLIN, "DEMORECEIPTS0");
    assert!(unsaved.iter().all(|r| !r.expected), "{unsaved:?}");
    assert!(by(&unsaved, ADA).read_at.is_some());
    assert!(by(&unsaved, GRACE).delivered_at.is_some() && by(&unsaved, GRACE).read_at.is_none());
    assert!(by(&unsaved, KATHERINE).delivered_at.is_none());
    assert!(receipts(FAMILY, "Family-1").is_empty(), "none recorded");
    let played = receipts("441632960123@s.whatsapp.net", "DEMOVOICE2");
    assert!(played.len() == 1 && played[0].played_at.is_some());
    core.shutdown();
}

#[test]
fn margarets_chat_has_a_bubble_of_each_kind_for_the_demo_pages() {
    let core = demo_core();
    core.send(w::Command::LoadChat {
        chat: "14155550199@s.whatsapp.net".into(),
        before: None,
    })
    .unwrap();
    let events = events_until(&core.shared, Vec::new(), |e| {
        e.iter()
            .any(|e| matches!(e, w::Event::Messages { complete: true, .. }))
    });
    let messages: Vec<&w::Message> = events
        .iter()
        .filter_map(|e| match e {
            w::Event::Messages { messages, .. } => Some(messages),
            _ => None,
        })
        .flatten()
        .collect();
    let find = |id: &str| *messages.iter().find(|m| m.id == id).expect(id);
    assert!(matches!(find("DEMOPOLL0").content, w::Content::Poll { .. }));
    assert!(matches!(
        find("DEMOCONTACT0").content,
        w::Content::Contact { .. }
    ));
    assert!(matches!(
        &find("DEMOLOCATION0").content,
        w::Content::Location { name: Some(_), .. }
    ));
    assert!(
        matches!(&find("DEMONOTE0").content, w::Content::Video { note: true, media, .. } if media.path.as_ref().is_some_and(|p| p.is_file()))
    );
    assert!(find("DEMOEDITED0").edited && find("DEMOFORWARDED0").forwarded);
    assert_eq!(messages[0].reactions.len(), 2);
    assert_eq!(
        messages.last().map(|m| m.id.as_str()),
        Some("Margaret-Hamilton-1"),
        "her chat's last line is still its last"
    );
    core.shutdown();
}

// ---- transcript order ---------------------------------------------------------

#[test]
fn every_demo_chat_reads_in_time_order_ends_on_its_own_last_line_and_nothing_is_in_the_future() {
    let core = demo_core();
    let opened = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;
    let events = events_until(&core.shared, Vec::new(), |e| {
        e.iter()
            .any(|e| matches!(e, w::Event::Chats { last: true, .. }))
    });
    let chats: Vec<w::Chat> = events
        .into_iter()
        .filter_map(|e| match e {
            w::Event::Chats { chats, .. } => Some(chats),
            _ => None,
        })
        .flatten()
        .collect();
    assert_eq!(chats.len(), 7);
    for chat in &chats {
        core.send(w::Command::LoadChat {
            chat: chat.id.clone(),
            before: None,
        })
        .unwrap();
        let events = events_until(&core.shared, Vec::new(), |e| {
            e.iter().any(|e| matches!(e, w::Event::Messages { chat: c, complete: true, .. } if *c == chat.id))
        });
        let messages: Vec<&w::Message> = events
            .iter()
            .filter_map(|e| match e {
                w::Event::Messages {
                    chat: c, messages, ..
                } if *c == chat.id => Some(messages),
                _ => None,
            })
            .flatten()
            .collect();
        let times: Vec<(&str, i64)> = messages
            .iter()
            .map(|m| (m.id.as_str(), m.timestamp))
            .collect();
        assert!(
            times.windows(2).all(|w| w[0].1 <= w[1].1),
            "{}: times go backwards: {times:?}",
            chat.name
        );
        assert!(
            times.iter().all(|(_, t)| *t <= opened + 1),
            "{}: a line after now ({opened}): {times:?}",
            chat.name
        );
        // The chat list's preview and time are its last line's, which is
        // the sample's own last line.
        let last = messages.last().unwrap();
        let sample_last = format!(
            "{}-{}",
            chat.name.replace(' ', "-"),
            messages
                .iter()
                .filter(
                    |m| m.id.starts_with(&chat.name.replace(' ', "-")) && !m.id.contains("-media-")
                )
                .count()
                - 1
        );
        assert_eq!(last.id, sample_last, "{}", chat.name);
        assert_eq!(chat.last_activity, last.timestamp, "{}", chat.name);
        if chat.id == ADA {
            // Her shared media, a day apart before her lines: photos and
            // clip, documents, links, then today's lines.
            let at = |id: &str| times.iter().find(|(i, _)| *i == id).unwrap().1;
            for (before, after) in [
                ("Ada-media-3", "Ada-media-4"),
                ("Ada-media-5", "Ada-media-6"),
                ("Ada-media-7", "Ada-Lovelace-0"),
            ] {
                let gap = at(after) - at(before);
                assert!(
                    (20 * 3600..28 * 3600).contains(&gap),
                    "{before} → {after}: {gap} s"
                );
            }
        }
    }
    core.shutdown();
}

#[test]
fn dorothys_chat_starts_locked_with_a_photo_a_clip_and_a_voice_note_and_every_clip_has_its_poster()
{
    const DOROTHY: &str = "15550004444@s.whatsapp.net";
    let core = demo_core();
    let events = events_until(&core.shared, Vec::new(), |e| {
        e.iter()
            .any(|e| matches!(e, w::Event::Chats { last: true, .. }))
    });
    let locked: Vec<String> = events
        .iter()
        .filter_map(|e| match e {
            w::Event::Chats { chats, .. } => Some(chats),
            _ => None,
        })
        .flatten()
        .filter(|c| c.locked)
        .map(|c| c.name.clone())
        .collect();
    assert_eq!(locked, ["Dorothy Vaughan"]);

    let load = |chat: &str| {
        core.send(w::Command::LoadChat {
            chat: chat.into(),
            before: None,
        })
        .unwrap();
        let events = events_until(&core.shared, Vec::new(), |e| {
            e.iter()
                .any(|e| matches!(e, w::Event::Messages { complete: true, .. }))
        });
        events
            .into_iter()
            .filter_map(|e| match e {
                w::Event::Messages { messages, .. } => Some(messages),
                _ => None,
            })
            .flatten()
            .collect::<Vec<w::Message>>()
    };
    let jpeg = |m: &w::Message| {
        m.thumbnail.as_ref().is_some_and(|t| {
            let mut b = vec![0u8; t.len as usize];
            core.shared.blobs.read(t.id, 0, &mut b).is_ok() && b.starts_with(&[0xFF, 0xD8, 0xFF])
        })
    };
    let dorothy = load(DOROTHY);
    let kinds: Vec<&str> = dorothy
        .iter()
        .filter_map(|m| match &m.content {
            w::Content::Image { media, .. } if media.path.as_ref().is_some_and(|p| p.is_file()) => {
                Some("photo")
            }
            w::Content::Video {
                media,
                gif: false,
                note: false,
                ..
            } if media.path.as_ref().is_some_and(|p| p.is_file()) && jpeg(m) => Some("clip"),
            w::Content::Audio {
                media,
                voice_note: true,
                ..
            } if media.path.as_ref().is_some_and(|p| p.is_file()) => Some("voice"),
            _ => None,
        })
        .collect();
    assert_eq!(kinds, ["voice", "photo", "clip"]);

    // Every clip in the demo carries a poster, as real ones do.
    for (chat, id) in [
        (ADA, "Ada-media-3"),
        ("14155550199@s.whatsapp.net", "DEMONOTE0"),
        ("4915112345678@s.whatsapp.net", "DEMOGIF0"),
    ] {
        let messages = load(chat);
        let clip = messages.iter().find(|m| m.id == id).expect(id);
        assert!(jpeg(clip), "{id} has no JPEG thumbnail");
    }

    // Unlocked, as the worker files it.
    core.send(w::Command::SetLocked {
        chat: DOROTHY.into(),
        locked: false,
    })
    .unwrap();
    let events = events_until(&core.shared, Vec::new(), |e| {
        e.iter()
            .any(|e| matches!(e, w::Event::ChatUpdated { chat } if chat.id == DOROTHY))
    });
    assert!(events.iter().any(
        |e| matches!(e, w::Event::ChatUpdated { chat } if chat.id == DOROTHY && !chat.locked)
    ));
    core.shutdown();
}
