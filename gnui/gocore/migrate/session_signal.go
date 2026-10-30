// From zapfast-go 3104c46 (internal/migration/session_signal.go), MIT: the
// Signal record conversion, unchanged but for errSignalRecord wrapping
// ErrIncompatible and sessionJID refusing a ':' left in the user.

package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/libsignal/groups/ratchet"
	groupRecord "go.mau.fi/libsignal/groups/state/record"
	"go.mau.fi/libsignal/kdf"
	"go.mau.fi/libsignal/keys/chain"
	"go.mau.fi/libsignal/keys/message"
	"go.mau.fi/libsignal/serialize"
	"go.mau.fi/libsignal/state/record"
	"go.mau.fi/libsignal/util/optional"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/util/keys"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var errSignalRecord = fmt.Errorf("%w: unsupported or invalid Rust Signal record", ErrIncompatible)

func strictProto(raw []byte, value proto.Message) error {
	if len(raw) == 0 || len(raw) > 8<<20 || proto.Unmarshal(raw, value) != nil || unknownProto(value.ProtoReflect()) {
		return errSignalRecord
	}
	return nil
}
func unknownProto(value protoreflect.Message) bool {
	if len(value.GetUnknown()) != 0 {
		return true
	}
	bad := false
	value.Range(func(field protoreflect.FieldDescriptor, data protoreflect.Value) bool {
		if field.Kind() == protoreflect.MessageKind {
			if field.IsList() {
				list := data.List()
				for i := 0; i < list.Len(); i++ {
					bad = bad || unknownProto(list.Get(i).Message())
				}
			} else {
				bad = bad || unknownProto(data.Message())
			}
		}
		return !bad
	})
	return bad
}

func keyPair(raw []byte) (*keys.KeyPair, error) {
	if len(raw) != 64 {
		return nil, errSignalRecord
	}
	key := keys.NewKeyPairFromPrivateKey(*(*[32]byte)(raw[:32]))
	if !bytes.Equal(key.Pub[:], raw[32:]) {
		return nil, errSignalRecord
	}
	return key, nil
}
func publicKey(raw []byte) bool { return len(raw) == 33 && raw[0] == ecc.DjbType }
func matchingPair(private, public []byte) bool {
	if len(private) != 32 || !publicKey(public) {
		return false
	}
	key := keys.NewKeyPairFromPrivateKey(*(*[32]byte)(private))
	return bytes.Equal(public[1:], key.Pub[:])
}

// Rust's top-level prekey store persists the unprefixed 32-byte public key;
// the libsignal record adapter's standard form includes the type byte.
func matchingPreKey(private, public []byte) bool {
	if len(public) == 32 {
		public = append([]byte{ecc.DjbType}, public...)
	}
	return matchingPair(private, public)
}

// Rust uses full legacy JIDs followed by Signal's .0 device field; Go puts
// the WhatsApp device in that field and encodes a LID's domain as _1.
func signalAddress(source string) (string, error) {
	if !strings.HasSuffix(source, ".0") {
		return "", errSignalRecord
	}
	jid, err := sessionJID(strings.TrimSuffix(source, ".0"))
	if err != nil || jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer && jid.Server != types.HostedServer && jid.Server != types.HostedLIDServer {
		return "", errSignalRecord
	}
	return jid.SignalAddress().String(), nil
}
func sessionJID(source string) (types.JID, error) {
	if len(source) == 0 || len(source) > 256 {
		return types.EmptyJID, errSignalRecord
	}
	jid, err := types.ParseJID(source)
	// types.ParseJID leaves a ':' in the user of a malformed address
	// (":.0@0"), which no account's address has.
	if err != nil || jid.User == "" || jid.Server == "" || strings.ContainsAny(jid.User, " \t\r\n/@:") {
		return types.EmptyJID, errSignalRecord
	}
	if jid.Server == types.LegacyUserServer {
		jid.Server = types.DefaultUserServer
	}
	return jid, nil
}
func senderAddress(source string) (string, string, error) {
	group, sender, ok := strings.Cut(source, ":")
	if !ok {
		return "", "", errSignalRecord
	}
	chat, err := sessionJID(group)
	if err != nil || chat.Server != types.GroupServer && chat != types.StatusBroadcastJID {
		return "", "", errSignalRecord
	}
	address, err := signalAddress(sender)
	return chat.String(), address, err
}

func convertSessionWithCounters(raw []byte) ([]byte, int64, error) {
	cleaned, reservation, err := localCounterFields(raw, true)
	if err != nil {
		return nil, 0, err
	}
	var source serialize.RecordStructure
	if proto.Unmarshal(cleaned, &source) != nil || source.CurrentSession == nil || len(source.PreviousSessions) > 40 {
		return nil, 0, errSignalRecord
	}
	states := append([]*serialize.SessionStructure{source.CurrentSession}, source.PreviousSessions...)
	for _, state := range states {
		if err = expandSkippedKeys(state); err != nil {
			return nil, 0, err
		}
	}
	if unknownProto(source.ProtoReflect()) {
		return nil, 0, errSignalRecord
	}
	state, err := convertState(source.CurrentSession)
	if err != nil {
		return nil, 0, err
	}
	target := &record.SessionStructure{SessionState: state, PreviousStates: make([]*record.StateStructure, len(source.PreviousSessions))}
	for i, previous := range source.PreviousSessions {
		target.PreviousStates[i], err = convertState(previous)
		if err != nil {
			return nil, 0, err
		}
	}
	// Rust burns the lease both on reload and archived-state promotion. Go
	// cannot carry that metadata, so burn it in every state before publication.
	var advanced int64
	for _, state := range append([]*record.StateStructure{target.SessionState}, target.PreviousStates...) {
		index := state.SenderChain.ChainKey.Index
		if reservation > index {
			if reservation-index > 2000 {
				return nil, 0, errors.New("Rust sender-counter reservation exceeds safe recovery bounds")
			}
			key := chain.NewKey(kdf.DeriveSecrets, state.SenderChain.ChainKey.Key, index)
			for key.Index() < reservation {
				key = key.NextKey()
				advanced++
			}
			state.SenderChain.ChainKey = chain.NewStructFromKey(key)
		}
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return nil, 0, errSignalRecord
	}
	if _, err = record.NewSessionFromBytes(encoded, store.SignalProtobufSerializer.Session, store.SignalProtobufSerializer.State); err != nil {
		return nil, 0, errSignalRecord
	}
	return encoded, advanced, nil
}

// Local fields 100/101 are documented by Rust's local_field.rs. Anything else
// stays unsupported; neither duplicate leases nor malformed incarnation IDs
// can authorize reuse of a potentially spent sender-chain counter.
func localCounterFields(raw []byte, session bool) ([]byte, uint32, error) {
	if len(raw) == 0 || len(raw) > 8<<20 {
		return nil, 0, errSignalRecord
	}
	cleaned := make([]byte, 0, len(raw))
	var reservation uint32
	seenCounter, seenIncarnation, seenCurrent := false, false, false
	for len(raw) != 0 {
		number, kind, tagLength := protowire.ConsumeTag(raw)
		if tagLength < 0 {
			return nil, 0, errSignalRecord
		}
		length := protowire.ConsumeFieldValue(number, kind, raw[tagLength:])
		if length < 0 {
			return nil, 0, errSignalRecord
		}
		field := raw[:tagLength+length]
		switch number {
		case 100:
			if kind != protowire.VarintType || seenCounter {
				return nil, 0, errSignalRecord
			}
			value, _ := protowire.ConsumeVarint(raw[tagLength:])
			if value > math.MaxUint32 {
				return nil, 0, errSignalRecord
			}
			reservation, seenCounter = uint32(value), true
		case 101:
			if kind != protowire.BytesType || seenIncarnation {
				return nil, 0, errSignalRecord
			}
			value, _ := protowire.ConsumeBytes(raw[tagLength:])
			if len(value) != 16 {
				return nil, 0, errSignalRecord
			}
			seenIncarnation = true
		case 1, 2:
			if kind != protowire.BytesType || !session && number == 2 || session && number == 1 && seenCurrent {
				return nil, 0, errSignalRecord
			}
			if number == 1 {
				seenCurrent = true
			}
			cleaned = append(cleaned, field...)
		default:
			return nil, 0, errSignalRecord
		}
		raw = raw[tagLength+length:]
	}
	return cleaned, reservation, nil
}

func expandSkippedKeys(state *serialize.SessionStructure) error {
	if state == nil {
		return errSignalRecord
	}
	chains := append([]*serialize.SessionStructure_Chain{state.SenderChain}, state.ReceiverChains...)
	for _, chain := range chains {
		if chain == nil {
			continue
		}
		for _, key := range chain.MessageKeys {
			if key == nil {
				return errSignalRecord
			}
			unknown := key.ProtoReflect().GetUnknown()
			if len(unknown) == 0 {
				continue
			}
			number, kind, tagLength := protowire.ConsumeTag(unknown)
			if tagLength < 0 || number != 100 || kind != protowire.BytesType {
				return errSignalRecord
			}
			seed, n := protowire.ConsumeBytes(unknown[tagLength:])
			if n < 0 || len(seed) != 32 || tagLength+n != len(unknown) {
				return errSignalRecord
			}
			derived, err := kdf.DeriveSecrets(seed, nil, []byte(message.KdfSalt), 80)
			if err != nil {
				return errSignalRecord
			}
			if len(key.CipherKey)+len(key.MacKey)+len(key.Iv) != 0 && (!bytes.Equal(key.CipherKey, derived[:32]) || !bytes.Equal(key.MacKey, derived[32:64]) || !bytes.Equal(key.Iv, derived[64:])) {
				return errSignalRecord
			}
			key.CipherKey, key.MacKey, key.Iv = derived[:32], derived[32:64], derived[64:]
			key.ProtoReflect().SetUnknown(nil)
		}
	}
	return nil
}

func convertState(source *serialize.SessionStructure) (*record.StateStructure, error) {
	if source == nil || source.GetSessionVersion() != 3 || !publicKey(source.LocalIdentityPublic) || !publicKey(source.RemoteIdentityPublic) || !publicKey(source.AliceBaseKey) || len(source.RootKey) != 32 || source.SenderChain == nil || len(source.ReceiverChains) > 5 {
		return nil, errSignalRecord
	}
	target := &record.StateStructure{LocalIdentityPublic: source.LocalIdentityPublic, LocalRegistrationID: source.GetLocalRegistrationId(), NeedsRefresh: source.GetNeedsRefresh(), PreviousCounter: source.GetPreviousCounter(), RemoteIdentityPublic: source.RemoteIdentityPublic, RemoteRegistrationID: source.GetRemoteRegistrationId(), RootKey: source.RootKey, SenderBaseKey: source.AliceBaseKey, SessionVersion: 3}
	var err error
	target.SenderChain, err = convertChain(source.SenderChain, true)
	if err != nil {
		return nil, err
	}
	target.ReceiverChains = make([]*record.ChainStructure, len(source.ReceiverChains))
	for i, receiver := range source.ReceiverChains {
		target.ReceiverChains[i], err = convertChain(receiver, false)
		if err != nil {
			return nil, err
		}
	}
	if pending := source.PendingPreKey; pending != nil {
		if !publicKey(pending.BaseKey) || pending.GetSignedPreKeyId() < 0 {
			return nil, errSignalRecord
		}
		id := optional.NewEmptyUint32()
		if pending.PreKeyId != nil {
			id = optional.NewOptionalUint32(pending.GetPreKeyId())
		}
		target.PendingPreKey = &record.PendingPreKeyStructure{PreKeyID: id, SignedPreKeyID: uint32(pending.GetSignedPreKeyId()), BaseKey: pending.BaseKey}
	}
	// WhatsApp does not use interactive Signal key exchanges; fail closed if
	// a future source adds one instead of guessing its key representation.
	if source.PendingKeyExchange != nil {
		return nil, errSignalRecord
	}
	return target, nil
}

func convertChain(source *serialize.SessionStructure_Chain, sender bool) (*record.ChainStructure, error) {
	if source == nil || !publicKey(source.SenderRatchetKey) || source.ChainKey == nil || len(source.ChainKey.Key) != 32 || len(source.MessageKeys) > 2000 {
		return nil, errSignalRecord
	}
	if sender && !matchingPair(source.SenderRatchetKeyPrivate, source.SenderRatchetKey) || !sender && len(source.SenderRatchetKeyPrivate) != 0 {
		return nil, errSignalRecord
	}
	target := &record.ChainStructure{SenderRatchetKeyPublic: source.SenderRatchetKey, SenderRatchetKeyPrivate: source.SenderRatchetKeyPrivate, ChainKey: &chain.KeyStructure{Key: source.ChainKey.Key, Index: source.ChainKey.GetIndex()}, MessageKeys: make([]*message.KeysStructure, len(source.MessageKeys))}
	for i, key := range source.MessageKeys {
		if key == nil || len(key.CipherKey) != 32 || len(key.MacKey) != 32 || len(key.Iv) != 16 {
			return nil, errSignalRecord
		}
		target.MessageKeys[i] = &message.KeysStructure{CipherKey: key.CipherKey, MacKey: key.MacKey, IV: key.Iv, Index: key.GetIndex()}
	}
	return target, nil
}

func convertSenderKeyWithCounters(raw []byte) ([]byte, int64, error) {
	cleaned, reservation, err := localCounterFields(raw, false)
	if err != nil {
		return nil, 0, err
	}
	var source serialize.SenderKeyRecordStructure
	if strictProto(cleaned, &source) != nil || len(source.SenderKeyStates) == 0 || len(source.SenderKeyStates) > 5 {
		return nil, 0, errSignalRecord
	}
	target := &groupRecord.SenderKeyStructure{SenderKeyStates: make([]*groupRecord.SenderKeyStateStructure, len(source.SenderKeyStates))}
	for i, state := range source.SenderKeyStates {
		if state == nil || state.SenderChainKey == nil || len(state.SenderChainKey.Seed) != 32 || state.SenderSigningKey == nil || !publicKey(state.SenderSigningKey.Public) || len(state.SenderMessageKeys) > 2000 || len(state.SenderSigningKey.Private) != 0 && !matchingPair(state.SenderSigningKey.Private, state.SenderSigningKey.Public) {
			return nil, 0, errSignalRecord
		}
		converted := &groupRecord.SenderKeyStateStructure{KeyID: state.GetSenderKeyId(), SenderChainKey: &ratchet.SenderChainKeyStructure{Iteration: state.SenderChainKey.GetIteration(), ChainKey: state.SenderChainKey.Seed}, SigningKeyPublic: state.SenderSigningKey.Public, SigningKeyPrivate: state.SenderSigningKey.Private, Keys: make([]*ratchet.SenderMessageKeyStructure, len(state.SenderMessageKeys))}
		for j, key := range state.SenderMessageKeys {
			if key == nil || len(key.Seed) != 32 {
				return nil, 0, errSignalRecord
			}
			derived, err := ratchet.NewSenderMessageKey(key.GetIteration(), key.Seed)
			if err != nil {
				return nil, 0, errSignalRecord
			}
			converted.Keys[j] = ratchet.NewStructFromSenderMessageKey(derived)
		}
		target.SenderKeyStates[i] = converted
	}
	var advanced int64
	state := target.SenderKeyStates[0]
	if iteration := state.SenderChainKey.Iteration; reservation > iteration {
		if reservation-iteration > 2000 {
			return nil, 0, errors.New("Rust group sender-counter reservation exceeds safe recovery bounds")
		}
		key := ratchet.NewSenderChainKey(iteration, state.SenderChainKey.ChainKey)
		for key.Iteration() < reservation {
			key = key.Next()
			advanced++
		}
		state.SenderChainKey = &ratchet.SenderChainKeyStructure{Iteration: key.Iteration(), ChainKey: key.Seed()}
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return nil, 0, errSignalRecord
	}
	if _, err = groupRecord.NewSenderKeyFromBytes(encoded, store.SignalProtobufSerializer.SenderKeyRecord, store.SignalProtobufSerializer.SenderKeyState); err != nil {
		return nil, 0, errSignalRecord
	}
	return encoded, advanced, nil
}
