//! Synthetic fixtures for the Go backend's migration (gnui/gocore/migrate),
//! written by the Rust core's own code: an upstream-format SQLCipher
//! archive.db, through `zapfast::archive::Archive`, and two whatsapp-rust
//! 5e72baf session.db files (ours and a peer's), through its SQLite store,
//! persistence manager and Signal adapters, opened as Zap's device store
//! opens them (src/backend/worker/device_store.rs re-adds
//! msg_secrets.created_at).
//!
//! Every key, number and message is synthetic. Nothing is read from this
//! computer's accounts or keyring: the archive key is a fixed test key kept
//! in an in-memory keyring.
//!
//! `ZF_WRITE_MIGRATION_FIXTURES=1 cargo test -p zapfast_core --test
//! migration_fixtures` writes them into gnui/gocore/migrate/testdata;
//! without it the test checks the committed ones still open with this
//! build's code.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use keyring_core::api::CredentialStoreApi;
use whatsapp_rust::{wacore, waproto};
use wacore::appstate::hash::HashState;
use wacore::libsignal::protocol::{
    CiphertextMessage, IdentityKey, KeyPair, PreKeyBundle, UsePQRatchet,
    create_sender_key_distribution_message, group_decrypt, group_encrypt, message_decrypt,
    message_encrypt, process_prekey_bundle, process_sender_key_distribution_message,
};
use wacore::libsignal::store::record_helpers::{encode_pre_key_record_to, new_signed_pre_key_record};
use wacore::pair::{DeviceState, PairUtils};
use wacore::store::traits::{
    AppStateSyncKey, AppSyncStore, DeviceStore, LidPnMappingEntry, MsgSecretEntry,
    MsgSecretStore, ProtocolStore, SignalStore, TcTokenEntry,
};
use wacore::types::jid::{JidExt, make_sender_key_name_for_jid};
use whatsapp_rust::buffa::Message as _;
use whatsapp_rust::store::{
    SqliteStore, persistence_manager::PersistenceManager,
    signal_adapter::SignalProtocolStoreAdapter, signal_cache::SignalStoreCache,
};
use whatsapp_rust::Jid;
use waproto::whatsapp as wa;

use zapfast::archive::{Archive, Pinner, PollVote};
use zapfast::model::{
    CallStatus, Chat, ChatKind, Contact, Content, Delivery, InteractiveAction, InteractiveButton,
    InteractiveCard, LinkPreview, Media, MentionRef, Message, Motion, PollState, Quoted, Reaction,
};

/// The keyring service the fixtures' archive key is kept under, in memory.
const SERVICE: &str = "ai.a3lab.zap.fixtures";

/// The archive's raw SQLCipher key. Synthetic, committed with the fixture.
const ARCHIVE_KEY: [u8; 32] = [
    0x5a, 0x61, 0x70, 0x2d, 0x6d, 0x69, 0x67, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x2d, 0x66, 0x69,
    0x78, 0x74, 0x75, 0x72, 0x65, 0x2d, 0x6b, 0x65, 0x79, 0x2d, 0x73, 0x79, 0x6e, 0x74, 0x68, 0x21,
];

// The accounts, all synthetic: 1555 numbers and 1000… LIDs.
const OUR_PN: &str = "15550001001:7@s.whatsapp.net";
const OUR_LID: &str = "100000000001001:7@lid";
const PEER_PN: &str = "15550002002:3@s.whatsapp.net";
const PEER_LID: &str = "100000000002002:3@lid";
const THIRD_PN: &str = "15550003003:1@s.whatsapp.net";
const GROUP: &str = "120363000000000001@g.us";

const FILES: &[&str] = &["archive.db", "session.db", "peer-session.db"];

fn testdata() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../gnui/gocore/migrate/testdata")
}

fn jid(s: &str) -> Jid {
    s.parse().unwrap()
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

fn digest(path: &Path) -> String {
    use sha2::{Digest, Sha256};
    hex(&Sha256::digest(std::fs::read(path).unwrap()))
}

fn rng() -> rand::rngs::StdRng {
    rand::make_rng::<rand::rngs::StdRng>()
}

/// One linked device in its own session.db, opened as Zap opens it.
struct Device {
    store: Arc<SqliteStore>,
    manager: Arc<PersistenceManager>,
    cache: Arc<SignalStoreCache>,
    signal: SignalProtocolStoreAdapter,
    pn: Jid,
    lid: Jid,
}

impl Device {
    async fn open(path: &Path, pn: &str, lid: &str, name: &str) -> Self {
        let store = SqliteStore::open(path.to_str().unwrap()).await.unwrap();
        legacy_created_at(path);
        let store = Arc::new(store);
        let manager = Arc::new(PersistenceManager::new(store.clone()).await.unwrap());
        let cache = Arc::new(SignalStoreCache::new());
        let signal = SignalProtocolStoreAdapter::new(manager.clone(), cache.clone());
        let device = Self {
            store,
            manager,
            cache,
            signal,
            pn: jid(pn),
            lid: jid(lid),
        };
        device.pair(name).await;
        device
    }

    /// Pairs as `src/pair.rs` does on pair-success: the phone (a synthetic
    /// account key here) signs the device identity, `do_pair_crypto` checks it
    /// and adds the device signature, and the result is stored.
    async fn pair(&self, name: &str) {
        let snapshot = self.manager.get_device_snapshot();
        let state = DeviceState {
            identity_key: snapshot.identity_key.clone(),
            noise_key: snapshot.noise_key.clone(),
            adv_secret_key: snapshot.adv_secret_key,
        };
        let account = KeyPair::generate(&mut rng());
        let mut details = wa::ADVDeviceIdentity::default();
        details.raw_id = Some(0x5157_0001);
        details.timestamp = Some(1_760_000_000);
        details.key_index = Some(self.pn.device as u32);
        let details = details.encode_to_vec();
        let mut message = vec![6, 0];
        message.extend_from_slice(&details);
        message.extend_from_slice(state.identity_key.public_key.public_key_bytes());
        let mut signed = wa::ADVSignedDeviceIdentity::default();
        signed.details = Some(details);
        signed.account_signature_key = Some(account.public_key.public_key_bytes().to_vec());
        signed.account_signature = Some(
            account
                .private_key
                .calculate_signature(&message, &mut rng())
                .unwrap()
                .to_vec(),
        );
        let signed = signed.encode_to_vec();
        use hmac::Mac;
        let mut mac = <hmac::Hmac<sha2::Sha256> as hmac::KeyInit>::new_from_slice(&state.adv_secret_key).unwrap();
        mac.update(&signed);
        let mut container = wa::ADVSignedDeviceIdentityHMAC::default();
        container.details = Some(signed);
        container.hmac = Some(mac.finalize().into_bytes().to_vec());
        let (identity, _) =
            PairUtils::do_pair_crypto(&state, &container.encode_to_vec()).unwrap();
        let identity =
            waproto::codec::adv_signed_device_identity_decode(&identity).unwrap();
        let (pn, lid, name) = (self.pn.clone(), self.lid.clone(), name.to_owned());
        self.manager
            .modify_device(move |device| {
                device.pn = Some(pn);
                device.lid = Some(lid);
                device.account = Some(Arc::new(identity));
                device.push_name = name;
                device.lid_migrated = true;
                device.server_has_prekeys = true;
                device.nct_salt = Some(vec![0x4e; 32]);
            })
            .await;
        self.manager.flush().await.unwrap();
    }

    /// Generates prekeys 1..=total as the client does (src/prekeys.rs): the
    /// first `uploaded` marked uploaded, the watermarks after them.
    async fn prekeys(&self, total: u32, uploaded: u32) -> Vec<KeyPair> {
        let mut pairs = Vec::new();
        let (mut sent, mut kept) = (Vec::new(), Vec::new());
        for id in 1..=total {
            let pair = KeyPair::generate(&mut rng());
            let mut record = Vec::new();
            encode_pre_key_record_to(id, &pair, &mut record);
            if id <= uploaded {
                sent.push((id, record.into()));
            } else {
                kept.push((id, record.into()));
            }
            pairs.push(pair);
        }
        self.store.store_prekeys_batch(&sent, true).await.unwrap();
        if !kept.is_empty() {
            self.store.store_prekeys_batch(&kept, false).await.unwrap();
        }
        self.manager
            .modify_device(move |device| {
                device.next_pre_key_id = total + 1;
                device.first_unupload_pre_key_id = uploaded + 1;
            })
            .await;
        self.manager.flush().await.unwrap();
        pairs
    }

    fn bundle(&self, prekey: Option<(u32, &KeyPair)>) -> PreKeyBundle {
        let device = self.manager.get_device_snapshot();
        PreKeyBundle::new(
            device.registration_id,
            0.into(),
            prekey.map(|(id, pair)| (id.into(), pair.public_key)),
            device.signed_pre_key_id.into(),
            device.signed_pre_key.public_key,
            device.signed_pre_key_signature.to_vec(),
            IdentityKey::new(device.identity_key.public_key),
        )
        .unwrap()
    }

    async fn send(&mut self, to: &Jid, text: &[u8]) -> CiphertextMessage {
        message_encrypt(
            text,
            &to.to_protocol_address(),
            &mut self.signal.session_store,
            &mut self.signal.identity_store,
        )
        .await
        .unwrap()
    }

    async fn receive(&mut self, from: &Jid, message: &CiphertextMessage) -> Vec<u8> {
        message_decrypt(
            message,
            &from.to_protocol_address(),
            &mut self.signal.session_store,
            &mut self.signal.identity_store,
            &mut self.signal.pre_key_store,
            &self.signal.signed_pre_key_store,
            &mut rng(),
            UsePQRatchet::No,
        )
        .await
        .unwrap()
        .plaintext
    }

    async fn close(self, path: &Path) {
        self.cache.flush(&*self.store).await.unwrap();
        self.manager.flush().await.unwrap();
        drop(self);
        // Fold the write-ahead log in, so the fixture is one file.
        let connection = rusqlite::Connection::open(path).unwrap();
        connection
            .query_row("PRAGMA wal_checkpoint(TRUNCATE)", [], |_| Ok(()))
            .unwrap();
        drop(connection);
        for suffix in ["-wal", "-shm"] {
            let side = PathBuf::from(format!("{}{suffix}", path.display()));
            if side.exists() {
                assert_eq!(std::fs::metadata(&side).unwrap().len() > 0, suffix == "-shm");
                std::fs::remove_file(side).unwrap();
            }
        }
    }
}

/// What device_store.rs does after the library's migrations: keep the
/// msg_secrets.created_at column that whatsapp-rust 5e72baf drops.
fn legacy_created_at(path: &Path) {
    let connection = rusqlite::Connection::open(path).unwrap();
    let columns: Vec<String> = connection
        .prepare("PRAGMA table_info(msg_secrets)")
        .unwrap()
        .query_map([], |row| row.get(1))
        .unwrap()
        .collect::<rusqlite::Result<_>>()
        .unwrap();
    if !columns.is_empty() && !columns.iter().any(|c| c == "created_at") {
        connection
            .execute_batch("ALTER TABLE msg_secrets ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0")
            .unwrap();
    }
}

fn fingerprint(raw_id: u32) -> Vec<u8> {
    let mut fingerprint = wa::message::AppStateSyncKeyFingerprint::default();
    fingerprint.raw_id = Some(raw_id);
    fingerprint.current_index = Some(2);
    fingerprint.device_indexes = vec![0, 7];
    waproto::codec::app_state_sync_key_fingerprint_to_vec(&fingerprint)
}

/// The rest of the store: app-state keys and versions, LID mappings, message
/// secrets, privacy tokens, a rotated signed prekey and the tables the Go
/// store only preserves.
async fn our_store_rows(ours: &Device, rotated: &KeyPair) {
    let store = &ours.store;
    for (id, raw) in [(&[0x01, 0x00, 0x00, 0x01][..], 11), (&[0x01, 0x00, 0x00, 0x02][..], 12)] {
        let mut key = AppStateSyncKey::default();
        key.key_data = vec![raw as u8; 32];
        key.fingerprint = fingerprint(raw);
        key.timestamp = 1_760_000_000 + i64::from(raw);
        store.set_sync_key(id, key).await.unwrap();
    }
    for (name, version, fatal, bootstrapped) in [
        ("regular", 41, false, true),
        ("regular_high", 17, false, true),
        ("critical_block", 3, false, true),
        // Reset by the converter: a fresh snapshot is asked for.
        ("regular_low", 9, false, false),
        ("critical_unblock_low", 5, true, true),
    ] {
        let mut state = HashState::default();
        state.version = version;
        state.hash = [version as u8; 128];
        state.mac_mismatch_fatal = fatal;
        state.bootstrapped = bootstrapped;
        store.set_version(name, state).await.unwrap();
        let macs: Vec<_> = (0..3u8)
            .map(|i| wacore::appstate::processor::AppStateMutationMAC {
                index_mac: vec![version as u8 ^ i; 32],
                value_mac: vec![i; 32],
            })
            .collect();
        store.put_mutation_macs(name, version, &macs).await.unwrap();
    }
    let now = 1_760_000_100;
    for (lid, pn, at) in [
        ("100000000001001", "15550001001", now),
        ("100000000002002", "15550002002", now),
        ("100000000003003", "15550003003", now),
        // An older LID of the same number, which whatsmeow cannot keep.
        ("100000000003999", "15550003003", now - 500),
    ] {
        store
            .put_lid_mapping(&LidPnMappingEntry {
                lid: lid.into(),
                phone_number: pn.into(),
                created_at: at,
                updated_at: at,
                learning_source: "usync".into(),
            })
            .await
            .unwrap();
    }
    let secrets = (0..4u8)
        .map(|i| MsgSecretEntry {
            chat: Arc::from(if i % 2 == 0 { "15550002002@s.whatsapp.net" } else { GROUP }),
            sender: Arc::from(if i < 2 { "15550001001@s.whatsapp.net" } else { "100000000002002@lid" }),
            msg_id: Arc::from(format!("3EB0SYNTHSECRET{i}").as_str()),
            secret: [0x30 + i; 32],
            expires_at: 0,
            message_ts: 1_760_000_000 + i64::from(i),
        })
        .collect();
    store.put_msg_secrets(secrets).await.unwrap();
    for (jid, token) in [("100000000002002@lid", 0x71), ("15550003003@s.whatsapp.net", 0x72)] {
        store
            .put_tc_token(
                jid,
                &TcTokenEntry {
                    token: vec![token; 24],
                    token_timestamp: 1_760_000_200,
                    sender_timestamp: (token == 0x71).then_some(1_760_000_250),
                },
            )
            .await
            .unwrap();
    }
    // A rotation: the old signed prekey is kept for in-flight messages and a
    // new one, signed by the identity key, takes its place.
    let device = ours.manager.get_device_snapshot();
    let old = new_signed_pre_key_record(
        device.signed_pre_key_id,
        &device.signed_pre_key,
        device.signed_pre_key_signature,
        wacore::chrono::DateTime::from_timestamp(1_750_000_000, 0).unwrap(),
    );
    store
        .store_signed_prekey(device.signed_pre_key_id, &old.encode_to_vec())
        .await
        .unwrap();
    let signature = device
        .identity_key
        .private_key
        .calculate_signature(&rotated.public_key.serialize(), &mut rng())
        .unwrap();
    let (rotated, id) = (rotated.clone(), device.signed_pre_key_id + 1);
    ours.manager
        .modify_device(move |device| {
            device.signed_pre_key = rotated;
            device.signed_pre_key_id = id;
            device.signed_pre_key_signature = signature;
            device.last_signed_pre_key_rotation_ms = 1_760_000_400_000;
        })
        .await;
    store
        .set_sender_key_status(GROUP, &[("100000000002002:3@lid", true), ("15550003003:1@s.whatsapp.net", false)])
        .await
        .unwrap();
    let mut message = wa::Message::default();
    message.conversation = Some("synthetic retry payload".into());
    let payload = waproto::codec::message_to_vec(&message);
    store
        .store_sent_message("15550002002@s.whatsapp.net", "3EB0SYNTHSENT", &payload)
        .await
        .unwrap();
    store
        .store_pending_inbound("15550002002@s.whatsapp.net", "15550002002@s.whatsapp.net", "3EB0SYNTHPENDING", &payload)
        .await
        .unwrap();
    store
        .save_base_key("100000000002002:3@lid.0", "3EB0SYNTHBASE", &[0x05; 33])
        .await
        .unwrap();
    store.put_group_metadata(GROUP, b"synthetic group metadata").await.unwrap();
}

/// The fixture's ciphertexts and what they hold, for the Go tests.
#[derive(Default, serde::Serialize)]
struct Messages {
    /// Signal messages the peer sent to us that we never decrypted, oldest
    /// first: decrypted by the converted store from its skipped keys.
    dm_skipped: Vec<(String, String)>,
    /// A group message the peer sent that we never decrypted.
    group_skipped: (String, String),
}

async fn sessions(dir: &Path) -> (Messages, serde_json::Value) {
    let (ours_path, peer_path) = (dir.join("session.db"), dir.join("peer-session.db"));
    let third_dir = tempfile::tempdir().unwrap();
    let third_path = third_dir.path().join("session.db");
    let mut ours = Device::open(&ours_path, OUR_PN, OUR_LID, "Synthetic Us").await;
    let mut peer = Device::open(&peer_path, PEER_PN, PEER_LID, "Synthetic Bea").await;
    let third = Device::open(&third_path, THIRD_PN, "100000000003003:1@lid", "Synthetic Cy").await;
    ours.prekeys(12, 9).await;
    let peer_prekeys = peer.prekeys(5, 5).await;
    let third_prekeys = third.prekeys(3, 3).await;

    // Us and the peer, by LID, as WhatsApp addresses peers after migration.
    let (our_lid, peer_lid) = (ours.lid.clone(), peer.lid.clone());
    process_prekey_bundle(
        &peer_lid.to_protocol_address(),
        &mut ours.signal.session_store,
        &mut ours.signal.identity_store,
        &peer.bundle(Some((2, &peer_prekeys[1]))),
        &mut rng(),
        UsePQRatchet::No,
    )
    .await
    .unwrap();
    let first = ours.send(&peer_lid, b"synthetic hello").await;
    assert_eq!(peer.receive(&our_lid, &first).await, b"synthetic hello");
    let reply = peer.send(&our_lid, b"synthetic reply").await;
    assert_eq!(ours.receive(&peer_lid, &reply).await, b"synthetic reply");
    for i in 0..3 {
        let text = format!("synthetic follow-up {i}");
        let m = ours.send(&peer_lid, text.as_bytes()).await;
        assert_eq!(peer.receive(&our_lid, &m).await, text.as_bytes());
    }
    let mut messages = Messages::default();
    for text in ["synthetic skipped 1", "synthetic skipped 2"] {
        let m = peer.send(&our_lid, text.as_bytes()).await;
        messages.dm_skipped.push((hex(&m.serialize()), text.into()));
    }
    let later = peer.send(&our_lid, b"synthetic later").await;
    assert_eq!(ours.receive(&peer_lid, &later).await, b"synthetic later");

    // A session we started with a third device by phone number, which it
    // never answered: our state keeps its pending prekey.
    process_prekey_bundle(
        &third.pn.to_protocol_address(),
        &mut ours.signal.session_store,
        &mut ours.signal.identity_store,
        &third.bundle(Some((3, &third_prekeys[2]))),
        &mut rng(),
        UsePQRatchet::No,
    )
    .await
    .unwrap();
    ours.send(&third.pn.clone(), b"synthetic unanswered").await;

    // A group: each side's sender key reaches the other.
    let group = jid(GROUP);
    let peer_name = make_sender_key_name_for_jid(&group, &peer_lid);
    let our_name = make_sender_key_name_for_jid(&group, &our_lid);
    let distribution =
        create_sender_key_distribution_message(&peer_name, &mut peer.signal.sender_key_store, &mut rng())
            .await
            .unwrap();
    process_sender_key_distribution_message(&peer_name, &distribution, &mut ours.signal.sender_key_store)
        .await
        .unwrap();
    let skipped = group_encrypt(&mut peer.signal.sender_key_store, &peer_name, b"synthetic group skipped", &mut rng())
        .await
        .unwrap();
    messages.group_skipped = (hex(skipped.serialized()), "synthetic group skipped".into());
    let later = group_encrypt(&mut peer.signal.sender_key_store, &peer_name, b"synthetic group later", &mut rng())
        .await
        .unwrap();
    assert_eq!(
        group_decrypt(later.serialized(), &mut ours.signal.sender_key_store, &peer_name).await.unwrap(),
        b"synthetic group later"
    );
    let distribution =
        create_sender_key_distribution_message(&our_name, &mut ours.signal.sender_key_store, &mut rng())
            .await
            .unwrap();
    process_sender_key_distribution_message(&our_name, &distribution, &mut peer.signal.sender_key_store)
        .await
        .unwrap();
    for i in 0..2 {
        let text = format!("synthetic group from us {i}");
        let m = group_encrypt(&mut ours.signal.sender_key_store, &our_name, text.as_bytes(), &mut rng())
            .await
            .unwrap();
        assert_eq!(
            group_decrypt(m.serialized(), &mut peer.signal.sender_key_store, &our_name).await.unwrap(),
            text.as_bytes()
        );
    }

    our_store_rows(&ours, &KeyPair::generate(&mut rng())).await;
    peer.store
        .put_lid_mapping(&LidPnMappingEntry {
            lid: "100000000001001".into(),
            phone_number: "15550001001".into(),
            created_at: 1_760_000_100,
            updated_at: 1_760_000_100,
            learning_source: "usync".into(),
        })
        .await
        .unwrap();

    let accounts = serde_json::json!({
        "ours": {"pn": OUR_PN, "lid": OUR_LID},
        "peer": {"pn": PEER_PN, "lid": PEER_LID},
        "third": {"pn": THIRD_PN},
        "group": GROUP,
    });
    ours.close(&ours_path).await;
    peer.close(&peer_path).await;
    third.close(&third_path).await;
    (messages, accounts)
}

/// An archive keyed with ARCHIVE_KEY in a fresh in-memory keyring.
fn keyring(path: &Path) {
    static KEYS: std::sync::OnceLock<Arc<keyring_core::mock::Store>> = std::sync::OnceLock::new();
    let keys = KEYS.get_or_init(|| {
        let keys = keyring_core::mock::Store::new().unwrap();
        assert!(zapfast::archive::use_credential_store(keys.clone()));
        assert!(zapfast::archive::set_keyring_service(SERVICE));
        keys
    });
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    let identity = zapfast::archive::archive_key_identity(path).unwrap();
    keys.build(SERVICE, &identity, None).unwrap().set_secret(&ARCHIVE_KEY).unwrap();
}

fn media(mime: &str, size: u64, w: Option<u32>, h: Option<u32>, path: Option<&str>) -> Media {
    Media {
        mime: mime.into(),
        size,
        width: w,
        height: h,
        album: None,
        path: path.map(PathBuf::from),
        state: Default::default(),
    }
}

fn raw(text: &str) -> Vec<u8> {
    let mut message = wa::Message::default();
    message.conversation = Some(text.into());
    waproto::codec::message_to_vec(&message)
}

fn poll_raw(question: &str, options: &[&str]) -> Vec<u8> {
    let mut poll = wa::message::PollCreationMessage::default();
    poll.name = Some(question.into());
    poll.selectable_options_count = Some(1);
    poll.options = options
        .iter()
        .map(|name| {
            let mut option = wa::message::poll_creation_message::Option::default();
            option.option_name = Some((*name).into());
            option
        })
        .collect();
    let mut message = wa::Message::default();
    message.poll_creation_message = whatsapp_rust::buffa::MessageField::some(poll);
    waproto::codec::message_to_vec(&message)
}

#[allow(clippy::too_many_arguments)]
fn message(chat: &str, id: &str, sender: &str, from_me: bool, ts: i64, content: Content) -> Message {
    Message {
        id: id.into(),
        chat: chat.into(),
        sender: sender.into(),
        sender_name: (!from_me).then(|| "Synthetic Sender".into()),
        from_me,
        timestamp: ts,
        history_order: None,
        content,
        status: if from_me { Delivery::Read } else { Delivery::None },
        delivered_at: from_me.then_some(ts + 1),
        read_at: from_me.then_some(ts + 2),
        quoted: None,
        reactions: Vec::new(),
        edited: false,
        mentions: Vec::new(),
        forwarded: false,
        thumbnail: None,
    }
}

/// The archive: every chat kind and message kind, receipts, labels, stars,
/// pins, drafts, favorites, polls, removals, contacts, LIDs and stickers.
fn archive(path: &Path) -> serde_json::Value {
    keyring(path);
    let a = Archive::open(path).unwrap();
    let me = "15550001001@s.whatsapp.net";
    let bea = "15550002002@s.whatsapp.net";
    let cy = "100000000003003@lid";
    let gone = "15550004004@s.whatsapp.net";
    let news = "120363000000000099@newsletter";
    let left = "120363000000000002@g.us";
    for (id, name, kind, at) in [
        (bea, "Synthetic Bea", ChatKind::Direct, 1_760_000_900),
        (cy, "Synthetic Cy", ChatKind::Direct, 1_760_000_800),
        (GROUP, "Synthetic Group", ChatKind::Group, 1_760_000_950),
        (left, "Synthetic Old Group", ChatKind::Group, 1_759_000_000),
        (news, "Synthetic News", ChatKind::Broadcast, 1_760_000_700),
        (gone, "Synthetic Cleared", ChatKind::Direct, 1_760_000_600),
    ] {
        let mut chat = Chat::new(id.into(), name.into());
        chat.kind = kind;
        chat.last_activity = at;
        chat.group_subject_known = kind == ChatKind::Group;
        chat.unread = if id == GROUP { 2 } else { 0 };
        a.upsert_chat(&chat).unwrap();
    }
    a.set_pinned_at(bea, true, 1_760_000_123_000).unwrap();
    a.set_muted_at(bea, Some(0), 1_760_000_124).unwrap();
    a.set_archived_at(cy, true, 1_760_000_125).unwrap();
    a.set_locked_at(cy, true, 1_760_000_126).unwrap();
    a.set_marked_unread(cy, true).unwrap();
    let members = vec![me.to_string(), bea.to_string(), cy.to_string()];
    a.set_group_info(GROUP, Some("Synthetic Group"), &members, false).unwrap();
    a.set_group_description(GROUP, "A synthetic group for migration tests").unwrap();
    a.set_group_rights(GROUP, true, true).unwrap();
    a.set_ephemeral(GROUP, 86_400, 1_760_000_127).unwrap();
    a.set_muted_at(GROUP, Some(1_900_000_000), 1_760_000_128).unwrap();
    a.set_history_start(GROUP).unwrap();
    a.set_left(left, true).unwrap();
    a.set_group_info(news, Some("Synthetic News"), &[], true).unwrap();

    let photo = media("image/jpeg", 2048, Some(640), Some(480), Some("media/synthetic-photo.jpg"));
    let mut text = message(bea, "3EB0TEXT01", me, true, 1_760_000_001, Content::Text {
        text: "Synthetic hello https://example.com/page and www.example.org.".into(),
        preview: Some(LinkPreview {
            url: "https://example.com/page".into(),
            title: Some("Example page".into()),
            description: Some("A synthetic link card".into()),
        }),
    });
    text.history_order = Some(10);
    text.mentions = vec![MentionRef { user: "15550002002".into(), id: bea.into() }];
    let mut reply = message(bea, "3EB0TEXT02", bea, false, 1_760_000_002, Content::Text {
        text: "Synthetic reply @15550001001".into(),
        preview: None,
    });
    reply.quoted = Some(Quoted {
        chat: None,
        id: "3EB0TEXT01".into(),
        sender: me.into(),
        sender_name: None,
        summary: "Synthetic hello".into(),
        mentions: Vec::new(),
    });
    reply.reactions = vec![Reaction { sender: me.into(), from_me: true, emoji: "👍".into() }];
    reply.mentions = vec![MentionRef { user: "15550001001".into(), id: me.into() }];
    reply.forwarded = true;
    reply.edited = true;
    let kinds: Vec<(Message, Option<Vec<u8>>)> = vec![
        (text, Some(raw("Synthetic hello"))),
        (reply, Some(raw("Synthetic reply"))),
        (message(bea, "3EB0IMAGE1", bea, false, 1_760_000_010, Content::Image {
            caption: Some("Synthetic photo".into()),
            media: photo.clone(),
            motion: Some(Motion::default()),
        }), Some(raw("image"))),
        (message(bea, "3EB0VIDEO1", me, true, 1_760_000_011, Content::Video {
            caption: None,
            media: media("video/mp4", 40_000, Some(320), Some(320), None),
            seconds: Some(12),
            gif: false,
            note: true,
        }), Some(raw("ptv"))),
        (message(bea, "3EB0GIF001", bea, false, 1_760_000_012, Content::Video {
            caption: Some("Synthetic gif".into()),
            media: media("video/mp4", 9_000, Some(200), Some(150), None),
            seconds: Some(3),
            gif: true,
            note: false,
        }), None),
        (message(bea, "3EB0VOICE1", bea, false, 1_760_000_013, Content::Audio {
            media: media("audio/ogg; codecs=opus", 5_000, None, None, Some("media/synthetic-voice.ogg")),
            seconds: Some(4),
            voice_note: true,
            waveform: (0..64).collect(),
        }), Some(raw("voice"))),
        (message(bea, "3EB0AUDIO1", me, true, 1_760_000_014, Content::Audio {
            media: media("audio/mpeg", 70_000, None, None, None),
            seconds: Some(61),
            voice_note: false,
            waveform: Vec::new(),
        }), None),
        (message(bea, "3EB0DOC001", bea, false, 1_760_000_015, Content::Document {
            media: media("application/pdf", 12_345, None, None, None),
            file_name: "synthetic.pdf".into(),
            caption: Some("Synthetic document".into()),
            pages: Some(3),
        }), Some(raw("document"))),
        (message(bea, "3EB0STICK1", bea, false, 1_760_000_016, Content::Sticker {
            media: media("image/webp", 3_000, Some(512), Some(512), None),
            animated: true,
        }), Some(raw("sticker"))),
        (message(bea, "3EB0PACK01", bea, false, 1_760_000_017, Content::StickerPack {
            name: "Synthetic pack".into(),
            publisher: "Synthetic".into(),
            count: 4,
            caption: None,
        }), None),
        (message(bea, "3EB0LOC001", me, true, 1_760_000_018, Content::Location {
            latitude: -23.5,
            longitude: -46.6,
            name: Some("Synthetic place".into()),
            address: Some("1 Synthetic Street".into()),
        }), None),
        (message(bea, "3EB0LIVE01", bea, false, 1_760_000_019, Content::LiveLocation {
            latitude: 51.5,
            longitude: -0.12,
            accuracy_m: Some(10),
            speed_mps: Some(1.5),
            heading_deg: Some(90),
            sequence: 3,
            ended: false,
            updated: 1_760_000_050,
            newer_on_phone: true,
        }), None),
        (message(bea, "3EB0VCARD1", bea, false, 1_760_000_020, Content::Contact {
            display_name: "Synthetic Contact".into(),
            vcard: "BEGIN:VCARD\nFN:Synthetic Contact\nEND:VCARD".into(),
        }), None),
        (message(bea, "3EB0REVOK1", bea, false, 1_760_000_021, Content::Revoked), None),
        (message(bea, "3EB0UNSUP1", bea, false, 1_760_000_022, Content::Unsupported {
            what: "A synthetic unsupported message".into(),
        }), Some(raw("unsupported"))),
        (message(bea, "3EB0ONCE01", bea, false, 1_760_000_023, Content::PhoneOnly {
            view_once: true,
            live_location: false,
            once: Some(zapfast::model::OnceMedia::Photo),
        }), None),
        (message(bea, "zapfast-call-0001", bea, false, 1_760_000_024, Content::Call {
            video: true,
            status: CallStatus::Missed,
            duration: 0,
            group: false,
        }), None),
        (message(bea, "3EB0CALL02", me, true, 1_760_000_025, Content::Call {
            video: false,
            status: CallStatus::Answered,
            duration: 95,
            group: false,
        }), None),
        (message(cy, "3EB0INTER1", cy, false, 1_760_000_030, Content::Interactive {
            text: "Synthetic menu".into(),
            card: Some(Box::new(InteractiveCard {
                body: "Pick one".into(),
                buttons: vec![
                    InteractiveButton { label: "Site".into(), url: Some("https://example.com".into()), action: InteractiveAction::Unavailable },
                    InteractiveButton { label: "Code".into(), url: None, action: InteractiveAction::Copy("SYN123".into()) },
                ],
                image: None,
                needs_phone: false,
                carousel: Vec::new(),
                thumbnail: None,
            })),
        }), Some(raw("interactive"))),
        (message(GROUP, "3EB0GROUP1", me, true, 1_760_000_040, Content::Text {
            text: "Synthetic group message for @all".into(),
            preview: None,
        }), Some(raw("group"))),
        (message(GROUP, "3EB0POLL01", bea, false, 1_760_000_041, Content::Poll {
            question: "Synthetic question?".into(),
            options: vec!["Yes".into(), "No".into(), "Maybe".into()],
            state: PollState { selectable: 1, counts: vec![1, 0, 1], ..Default::default() },
        }), Some(poll_raw("Synthetic question?", &["Yes", "No", "Maybe"]))),
        (message(GROUP, "3EB0GROUP2", cy, false, 1_760_000_042, Content::Text {
            text: "Synthetic group reply".into(),
            preview: None,
        }), Some(raw("group reply"))),
        (message(news, "3EB0NEWS01", news, false, 1_760_000_043, Content::Text {
            text: "Synthetic channel post".into(),
            preview: None,
        }), None),
        (message(left, "3EB0LEFT01", bea, false, 1_759_000_000, Content::Text {
            text: "Synthetic old group message".into(),
            preview: None,
        }), None),
        (message(gone, "3EB0GONE01", gone, false, 1_760_000_500, Content::Text {
            text: "Synthetic cleared message".into(),
            preview: None,
        }), None),
        (message(gone, "3EB0GONE02", gone, false, 1_760_000_600, Content::Text {
            text: "Synthetic message after the clear".into(),
            preview: None,
        }), None),
        (message(bea, "3EB0DELME1", bea, false, 1_760_000_026, Content::Text {
            text: "Synthetic message deleted here".into(),
            preview: None,
        }), None),
        (message(bea, "3EB0DELME2", me, true, 1_760_000_027, Content::Text {
            text: "Synthetic message being deleted".into(),
            preview: None,
        }), None),
    ];
    let mut group_mention = None;
    for (mut m, raw) in kinds {
        if m.id == "3EB0GROUP1" {
            m.mentions = vec![MentionRef { user: "all".into(), id: "mention-all".into() }];
            group_mention = Some(m.id.clone());
        }
        if m.id == "3EB0IMAGE1" || m.id == "3EB0LIVE01" {
            m.thumbnail = Some(vec![0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10]);
        }
        a.insert_message(&m, raw.as_deref()).unwrap();
    }
    assert!(group_mention.is_some());
    a.put_motion_clip(bea, "3EB0IMAGE1", bea, &raw("motion clip")).unwrap();
    a.set_reaction(GROUP, "3EB0POLL01", me, true, "❤️").unwrap();

    // Group receipts: everyone expected, one read, one delivered.
    a.snapshot_group_recipients(GROUP, "3EB0GROUP1", &[bea.to_string(), cy.to_string()]).unwrap();
    a.group_receipt(GROUP, "3EB0GROUP1", bea, Delivery::Read, 1_760_000_045).unwrap();
    a.group_receipt(GROUP, "3EB0GROUP1", cy, Delivery::Delivered, 1_760_000_046).unwrap();

    // Labels, stars, pins, drafts, favorites, polls.
    let work = a.create_label("Synthetic Work", "#ff8800", 1_760_000_301).unwrap().unwrap();
    let home = a.create_label("Synthetic Home", "#00aa55", 1_760_000_302).unwrap().unwrap();
    a.set_chat_labels(bea, &[work.id.clone(), home.id.clone()]).unwrap();
    a.set_chat_labels(GROUP, &[work.id.clone()]).unwrap();
    a.star(bea, "3EB0TEXT01", 1_760_000_310).unwrap();
    a.star(bea, "3EB0DOC001", 1_760_000_311).unwrap();
    a.unstar(bea, "3EB0DOC001", 1_760_000_312).unwrap();
    a.star(GROUP, "3EB0POLL01", 1_760_000_313).unwrap();
    a.pin(bea, "3EB0TEXT02", 1_760_000_320, 1_900_000_000).unwrap();
    a.pin_from(GROUP, "3EB0GROUP2", 1_760_000_321, 1_900_000_000, &Pinner { by_me: false, sender: cy.into() }).unwrap();
    a.pin(GROUP, "3EB0GROUP1", 1_760_000_322, 1_900_000_000).unwrap();
    a.unpin(GROUP, "3EB0GROUP1", 1_760_000_323).unwrap();
    a.set_draft(bea, "Synthetic draft to Bea", 1_760_000_330).unwrap();
    a.set_draft(GROUP, "Synthetic group draft", 1_760_000_331).unwrap();
    a.apply_phone_favorites(
        &[
            zapfast::archive::Favorite { chat: GROUP.into(), jid: GROUP.into() },
            zapfast::archive::Favorite { chat: bea.into(), jid: bea.into() },
        ],
        1_760_000_340,
    )
    .unwrap();
    a.set_favorite(cy, true).unwrap();
    a.save_poll(GROUP, "3EB0POLL01", bea, &[0x50; 32]).unwrap();
    a.mark_poll_history(GROUP, "3EB0POLL01").unwrap();
    for (voter, choices, encrypted) in [
        (bea, Some(vec![0]), None),
        (cy, Some(vec![2]), None),
        (me, None, Some(vec![0xe1; 40])),
    ] {
        a.save_poll_vote(&PollVote {
            chat: GROUP.into(),
            poll: "3EB0POLL01".into(),
            voter: voter.into(),
            sender: voter.into(),
            update_id: format!("3EB0VOTE{}", &voter[..4]),
            at: 1_760_000_350,
            from_me: voter == me,
            choices,
            encrypted,
        })
        .unwrap();
    }

    // Removals: one confirmed, one waiting, one chat cleared through a time.
    a.delete_message(bea, "3EB0DELME1").unwrap();
    a.queue_message_removal(me, bea, "3EB0DELME2").unwrap();
    a.remove_chat_through(gone, 1_760_000_500, false).unwrap();

    // Contacts, LIDs, stickers, meta.
    for (id, full, first, push) in [
        (bea, Some("Synthetic Bea Full"), Some("Bea"), Some("Bea~")),
        (cy, None, None, Some("Cy push")),
    ] {
        a.upsert_contact(&Contact {
            id: id.into(),
            full_name: full.map(Into::into),
            first_name: first.map(Into::into),
            push_name: push.map(Into::into),
        })
        .unwrap();
    }
    a.put_lid("100000000002002@lid", bea).unwrap();
    a.upsert_phone_sticker("c3ludGhldGljLXN0aWNrZXI=", &raw("sticker raw"), 1_760_000_360, 1.5).unwrap();
    a.set_favorite_sticker("c3ludGhldGljLXN0aWNrZXI=", true, 1_760_000_361, None, true).unwrap();
    a.remove_recent_sticker("c3ludGhldGljLXJlbW92ZWQ=", 1_760_000_362).unwrap();
    drop(a);

    // One file: fold the log in.
    let connection = rusqlite::Connection::open(path).unwrap();
    connection.pragma_update(None, "key", format!("x'{}'", hex(&ARCHIVE_KEY))).unwrap();
    connection.query_row("PRAGMA wal_checkpoint(TRUNCATE)", [], |_| Ok(())).unwrap();
    drop(connection);
    for suffix in ["-wal", "-shm"] {
        let _ = std::fs::remove_file(format!("{}{suffix}", path.display()));
    }
    serde_json::json!({"labels": [work.id, home.id]})
}

fn produce(out: &Path) {
    let work = tempfile::tempdir().unwrap();
    let account = work.path().join("accounts/1");
    std::fs::create_dir_all(&account).unwrap();
    let archive_info = archive(&account.join("archive.db"));
    let runtime = tokio::runtime::Builder::new_multi_thread().enable_all().build().unwrap();
    let (messages, accounts) = runtime.block_on(sessions(&account));
    drop(runtime);
    std::fs::create_dir_all(out).unwrap();
    for name in FILES {
        std::fs::copy(account.join(name), out.join(name)).unwrap();
    }
    let files: std::collections::BTreeMap<_, _> =
        FILES.iter().map(|name| (*name, digest(&out.join(name)))).collect();
    let manifest = serde_json::json!({
        "synthetic": true,
        "generator": "zfcore/tests/migration_fixtures.rs",
        "whatsapp_rust": "5e72baf000aa4fb65ed89d1c955a54029a7f29a6",
        "archive_key": hex(&ARCHIVE_KEY),
        "accounts": accounts,
        "archive": archive_info,
        "messages": messages,
        "files": files,
    });
    std::fs::write(out.join("fixtures.json"), serde_json::to_vec_pretty(&manifest).unwrap()).unwrap();
}

/// The committed fixtures open with this build's archive and store code.
fn check(dir: &Path) {
    let manifest: serde_json::Value =
        serde_json::from_slice(&std::fs::read(dir.join("fixtures.json")).unwrap()).unwrap();
    for name in FILES {
        assert_eq!(manifest["files"][name], digest(&dir.join(name)), "{name} changed");
    }
    let scratch = tempfile::tempdir().unwrap();
    let account = scratch.path().join("accounts/1");
    std::fs::create_dir_all(&account).unwrap();
    for name in FILES {
        std::fs::copy(dir.join(name), account.join(name)).unwrap();
    }
    keyring(&account.join("archive.db"));
    let archive = Archive::open(&account.join("archive.db")).unwrap();
    assert_eq!(archive.chats().unwrap().len(), 6);
    drop(archive);
    let runtime = tokio::runtime::Builder::new_multi_thread().enable_all().build().unwrap();
    runtime.block_on(async {
        for name in ["session.db", "peer-session.db"] {
            let store = SqliteStore::open(account.join(name).to_str().unwrap()).await.unwrap();
            let device = store.load().await.unwrap().unwrap();
            assert!(device.pn.is_some() && device.account.is_some());
        }
    });
}

#[test]
fn migration_fixtures() {
    let dir = testdata();
    if std::env::var_os("ZF_WRITE_MIGRATION_FIXTURES").is_some() {
        produce(&dir);
    }
    check(&dir);
}
