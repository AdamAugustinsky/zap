package calls

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Client is what the signaling needs of the WhatsApp connection: whatsmeow
// (Whatsmeow), or a test's fake. Its methods are safe from any goroutine
// and may block on the network.
type Client interface {
	// Own is our device's ids, with the device: its phone-number JID and
	// its LID; empty while unlinked.
	Own() (pn, lid types.JID)
	// NewID is a fresh stanza id.
	NewID() string
	// Send sends a node.
	Send(ctx context.Context, n waBinary.Node) error
	// SendAwait sends n and waits for the server's <ack> to its id.
	SendAwait(ctx context.Context, n waBinary.Node, timeout time.Duration) (*waBinary.Node, error)
	// DecryptKey decrypts an offer's <enc> from the caller's device into
	// the callKey.
	DecryptKey(ctx context.Context, enc *waBinary.Node, from types.JID, at time.Time) ([]byte, error)
	// EncryptKey encrypts the callKey for each device (in their order,
	// skipping one that fails); identity is our <device-identity> when one
	// key is a pkmsg.
	EncryptKey(ctx context.Context, devices []types.JID, key []byte, id string) (keys []DeviceKey, identity []byte, err error)
	// PeerLID is user's LID (no device).
	PeerLID(ctx context.Context, user types.JID) (types.JID, error)
	// Devices is the devices the server rings for user, hosted ones left
	// out.
	Devices(ctx context.Context, user types.JID) ([]types.JID, error)
	// PrivacyToken is our stored token for peer, nil when there is none.
	PrivacyToken(ctx context.Context, peer types.JID) []byte
	// Offered is told an offer reached peer: a privacy token is issued to
	// them when this week's is due, as after a message.
	Offered(ctx context.Context, peer types.JID)
}

// Whatsmeow is Client over a whatsmeow client, through its
// DangerousInternals: whatsmeow places no calls itself, so the stanzas and
// the callKey's Signal messages go through its socket and sessions here.
func Whatsmeow(c *whatsmeow.Client) Client { return &wm{c: c, in: c.DangerousInternals()} }

type wm struct {
	c  *whatsmeow.Client
	in *whatsmeow.DangerousInternalClient
}

func (w *wm) Own() (types.JID, types.JID) {
	var pn types.JID
	if w.c.Store.ID != nil {
		pn = *w.c.Store.ID
	}
	return pn, w.c.Store.GetLID()
}

func (w *wm) NewID() string { return w.in.GenerateRequestID() }

func (w *wm) Send(ctx context.Context, n waBinary.Node) error { return w.in.SendNode(ctx, n) }

func (w *wm) SendAwait(ctx context.Context, n waBinary.Node, timeout time.Duration) (*waBinary.Node, error) {
	id, _ := n.Attrs["id"].(string)
	if id == "" {
		return nil, errors.New("the stanza has no id to await")
	}
	// Listening before the send: a fast answer cannot slip past.
	ch := w.in.WaitResponse(id)
	if err := w.in.SendNode(ctx, n); err != nil {
		w.in.CancelResponse(id, ch)
		return nil, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case ack, ok := <-ch:
		if !ok || ack == nil {
			return nil, errors.New("the connection closed before the server answered")
		}
		return ack, nil
	case <-t.C:
		w.in.CancelResponse(id, ch)
		return nil, errors.New("the server did not answer the offer")
	case <-ctx.Done():
		w.in.CancelResponse(id, ch)
		return nil, ctx.Err()
	}
}

func (w *wm) DecryptKey(ctx context.Context, enc *waBinary.Node, from types.JID, at time.Time) ([]byte, error) {
	plain, _, err := w.in.DecryptDM(ctx, enc, from, enc.AttrGetter().OptionalString("type") == "pkmsg", at)
	if err != nil {
		return nil, err
	}
	var msg waE2E.Message
	if err := proto.Unmarshal(plain, &msg); err != nil {
		return nil, fmt.Errorf("the call message: %w", err)
	}
	key := msg.GetCall().GetCallKey()
	if len(key) == 0 {
		return nil, errors.New("the offer carried no callKey")
	}
	return key, nil
}

func (w *wm) EncryptKey(ctx context.Context, devices []types.JID, key []byte, id string) ([]DeviceKey, []byte, error) {
	plain, err := proto.Marshal(&waE2E.Message{Call: &waE2E.Call{CallKey: key}})
	if err != nil {
		return nil, nil, err
	}
	nodes, pkmsg, err := w.in.EncryptMessageForDevices(ctx, devices, id, plain, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	keys := make([]DeviceKey, 0, len(nodes))
	for _, to := range nodes {
		jid, ok := to.Attrs["jid"].(types.JID)
		children := to.GetChildren()
		if !ok || len(children) != 1 {
			continue
		}
		ct, _ := children[0].Content.([]byte)
		keys = append(keys, DeviceKey{Device: jid, Ciphertext: ct, Type: children[0].AttrGetter().OptionalString("type")})
	}
	var identity []byte
	if pkmsg {
		identity, _ = w.in.MakeDeviceIdentityNode().Content.([]byte)
		if identity == nil {
			return nil, nil, errors.New("a new session needs this device's identity, which the store lacks")
		}
	}
	return keys, identity, nil
}

func (w *wm) PeerLID(ctx context.Context, user types.JID) (types.JID, error) {
	user = user.ToNonAD()
	if user.Server == types.HiddenUserServer {
		return user, nil
	}
	if lid, err := w.c.Store.LIDs.GetLIDForPN(ctx, user); err == nil && !lid.IsEmpty() {
		return lid.ToNonAD(), nil
	}
	// The device list query teaches the store the mapping.
	if _, err := w.c.GetUserDevices(ctx, []types.JID{user}); err != nil {
		return types.EmptyJID, err
	}
	lid, err := w.c.Store.LIDs.GetLIDForPN(ctx, user)
	if err != nil || lid.IsEmpty() {
		return types.EmptyJID, errors.New("WhatsApp gave no privacy id for this contact, which a call's keys need")
	}
	return lid.ToNonAD(), nil
}

func (w *wm) Devices(ctx context.Context, user types.JID) ([]types.JID, error) {
	all, err := w.c.GetUserDevices(ctx, []types.JID{user})
	if err != nil {
		return nil, err
	}
	devices := all[:0]
	for _, d := range all {
		if d.Server != types.HostedServer && d.Server != types.HostedLIDServer {
			devices = append(devices, d)
		}
	}
	return devices, nil
}

func (w *wm) PrivacyToken(ctx context.Context, peer types.JID) []byte {
	token, err := w.in.EnsureTCToken(ctx, peer)
	if err != nil {
		return nil
	}
	return token
}

// tokenBucket is whatsmeow's privacy-token bucket (7 days).
const tokenBucket = 604800

func (w *wm) Offered(ctx context.Context, peer types.JID) {
	storage := w.in.ResolveTCTokenStorageLID(ctx, peer)
	now := time.Now()
	if sent := w.in.GetTCTokenSenderTS(storage); now.Unix()/tokenBucket > sent.Unix()/tokenBucket {
		go w.in.IssuePrivacyTokenAndSave(storage, now)
	}
}
