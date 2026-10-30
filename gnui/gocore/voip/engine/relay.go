// Ported from whatsapp-rust 5e72baf wacore/src/voip_control/relay_parse.rs
// and wacore/src/voip_control/spec_build.rs, MIT.

package engine

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	waBinary "go.mau.fi/whatsmeow/binary"

	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/warp"
)

// WebClientRelayPort is the relay port WhatsApp Web dials when it does not
// use the advertised one; an endpoint on it is preferred.
const WebClientRelayPort = 3480

// maxRelayTokens bounds a relay-supplied token index (the relay is
// untrusted).
const maxRelayTokens = 64

// RelayAddress is one te2 address of an endpoint.
type RelayAddress struct {
	Protocol uint8
	IPv4     string
	IPv6     string
	Port     uint16
}

// RelayEndpoint is one relay of a <relay> block.
type RelayEndpoint struct {
	RelayID     uint32
	Name        string
	TokenID     uint32
	AuthTokenID uint32
	FNA         bool
	Addresses   []RelayAddress
	C2RRTTms    uint32
}

// Relay is a parsed <relay> block. Key is the <key> text as it arrived
// (ASCII base64): the STUN MESSAGE-INTEGRITY key the relay HMACs with.
type Relay struct {
	Key          []byte
	HasKey       bool
	HBHKey       []byte
	WarpMITagLen int
	UUID         string
	Tokens       [][]byte
	AuthTokens   [][]byte
	Endpoints    []RelayEndpoint
}

// String keeps the keys and tokens out of logs.
func (r Relay) String() string {
	return fmt.Sprintf("Relay{%d endpoints, %d tokens, key: [redacted]}", len(r.Endpoints), len(r.Tokens))
}

// GoString keeps %#v from printing them.
func (r Relay) GoString() string { return r.String() }

// FindRelay is the first <relay> node in n's subtree (the offer's relay
// sits under <call> or <offer>, the caller's under the offer's ack).
func FindRelay(n *waBinary.Node) *waBinary.Node {
	if n == nil {
		return nil
	}
	if n.Tag == "relay" {
		return n
	}
	children := n.GetChildren()
	for i := range children {
		if r := FindRelay(&children[i]); r != nil {
			return r
		}
	}
	return nil
}

func contentBytes(n *waBinary.Node) ([]byte, bool) {
	switch c := n.Content.(type) {
	case []byte:
		return c, true
	case string:
		return []byte(c), true
	}
	return nil, false
}

func attr(n *waBinary.Node, key string) (string, bool) {
	switch v := n.Attrs[key].(type) {
	case string:
		return v, true
	case int64:
		return strconv.FormatInt(v, 10), true
	case int:
		return strconv.Itoa(v), true
	case nil:
		return "", false
	default:
		return fmt.Sprint(v), true
	}
}

func attrUint(n *waBinary.Node, key string) uint32 {
	s, _ := attr(n, key)
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}

func indexedTokens(children []waBinary.Node, tag string) [][]byte {
	var tokens [][]byte
	for i := range children {
		c := &children[i]
		if c.Tag != tag {
			continue
		}
		b, ok := contentBytes(c)
		if !ok {
			continue
		}
		id := len(tokens)
		if s, ok := attr(c, "id"); ok {
			if v, err := strconv.Atoi(s); err == nil && v >= 0 {
				id = v
			}
		}
		if id >= maxRelayTokens {
			continue
		}
		for len(tokens) <= id {
			tokens = append(tokens, nil)
		}
		tokens[id] = append([]byte(nil), b...)
	}
	return tokens
}

func te2Address(b []byte, protocol uint8) (RelayAddress, bool) {
	switch len(b) {
	case 6:
		return RelayAddress{Protocol: protocol, IPv4: fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3]),
			Port: uint16(b[4])<<8 | uint16(b[5])}, true
	case 18:
		parts := make([]string, 8)
		for i := range 8 {
			parts[i] = strconv.FormatUint(uint64(b[2*i])<<8|uint64(b[2*i+1]), 16)
		}
		return RelayAddress{Protocol: protocol, IPv6: strings.Join(parts, ":"),
			Port: uint16(b[16])<<8 | uint16(b[17])}, true
	}
	return RelayAddress{}, false
}

func decodeBase64Loose(b []byte) ([]byte, bool) {
	s := string(b)
	if len(s) < 4 {
		return nil, false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=') {
			return nil, false
		}
	}
	if d, err := base64.StdEncoding.DecodeString(s); err == nil {
		return d, true
	}
	if d, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "=")); err == nil {
		return d, true
	}
	return nil, false
}

// decodeHBHKey is the 30-byte hop-by-hop key (possibly double base64).
func decodeHBHKey(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	d, ok := decodeBase64Loose(b)
	if !ok {
		d = b
	}
	if len(d) != 30 {
		if inner, ok := decodeBase64Loose(d); ok && len(inner) == 30 {
			d = inner
		}
	}
	if len(d) != 30 {
		return nil
	}
	return d
}

// ParseRelay reads a <relay> node.
func ParseRelay(n *waBinary.Node) Relay {
	children := n.GetChildren()
	find := func(tag string) ([]byte, bool) {
		for i := range children {
			if children[i].Tag == tag {
				return contentBytes(&children[i])
			}
		}
		return nil, false
	}
	var r Relay
	if k, ok := find("key"); ok {
		r.Key, r.HasKey = append([]byte(nil), k...), true
	}
	if h, ok := find("hbh_key"); ok {
		r.HBHKey = decodeHBHKey(h)
	}
	if t, ok := find("warp_mi_tag_len"); ok {
		if v, err := strconv.ParseUint(strings.TrimSpace(string(t)), 10, 32); err == nil && v > 0 {
			r.WarpMITagLen = int(v)
		}
	}
	r.Tokens = indexedTokens(children, "token")
	r.AuthTokens = indexedTokens(children, "auth_token")
	index := map[string]int{}
	for i := range children {
		c := &children[i]
		if c.Tag != "te2" {
			continue
		}
		b, ok := contentBytes(c)
		if !ok {
			continue
		}
		name, _ := attr(c, "relay_name")
		fna, _ := attr(c, "is_fna")
		addr, ok := te2Address(b, uint8(attrUint(c, "protocol")))
		if !ok {
			continue
		}
		key := fmt.Sprintf("%d:%s", attrUint(c, "relay_id"), name)
		idx, seen := index[key]
		if !seen {
			r.Endpoints = append(r.Endpoints, RelayEndpoint{
				RelayID: attrUint(c, "relay_id"), Name: name, TokenID: attrUint(c, "token_id"),
				AuthTokenID: attrUint(c, "auth_token_id"), FNA: fna == "1",
			})
			idx = len(r.Endpoints) - 1
			index[key] = idx
		}
		ep := &r.Endpoints[idx]
		ep.Addresses = append(ep.Addresses, addr)
		if rtt := attrUint(c, "c2r_rtt"); rtt != 0 {
			ep.C2RRTTms = rtt
		}
	}
	r.UUID, _ = attr(n, "uuid")
	return r
}

// PrimaryIPv4 is the endpoint's first IPv4 address.
func (e RelayEndpoint) PrimaryIPv4() (string, uint16, bool) {
	for _, a := range e.Addresses {
		if a.IPv4 != "" {
			return a.IPv4, a.Port, true
		}
	}
	return "", 0, false
}

func (e RelayEndpoint) outboundCandidate() bool { return !e.FNA && e.AuthTokenID != 0 }

// MediaEndpoint is the endpoint the media transport dials: one on the web
// client's port, else a relaylatency candidate, else any non-FNA one, else
// the first; at each tier a usable one (an IPv4 address and a token we
// hold) first.
func (r Relay) MediaEndpoint() (RelayEndpoint, bool) {
	usable := func(e RelayEndpoint) bool {
		_, _, ok := e.PrimaryIPv4()
		return ok && int(e.TokenID) < len(r.Tokens) && len(r.Tokens[e.TokenID]) > 0
	}
	webPort := func(e RelayEndpoint) bool {
		_, port, ok := e.PrimaryIPv4()
		return ok && port == WebClientRelayPort
	}
	tiers := []func(RelayEndpoint) bool{webPort, RelayEndpoint.outboundCandidate,
		func(e RelayEndpoint) bool { return !e.FNA }, func(RelayEndpoint) bool { return true }}
	for _, usableOnly := range []bool{true, false} {
		for _, tier := range tiers {
			for _, e := range r.Endpoints {
				if tier(e) && (!usableOnly || usable(e)) {
					return e, true
				}
			}
		}
	}
	return RelayEndpoint{}, false
}

// Direction is which side placed the call.
type Direction int

const (
	Incoming Direction = iota
	Outgoing
)

// Config is everything the engine needs for one call: the relay endpoint
// and its credentials from the <relay> block, and the keys from the
// callKey and both LIDs.
type Config struct {
	CallID    string
	Direction Direction
	// SelfLID keys our send side, PeerLID the receive side (until a rekey
	// names the device that answered).
	SelfLID, PeerLID string
	CallKey          []byte
	SSRC             uint32
	Audio            AudioFormat
	RelayToken       []byte
	AuthToken        []byte
	RelayIP          string
	RelayPort        uint16
	IntegrityKey     []byte
	WarpMITagLen     int
	// Video builds the video plane from the start.
	Video bool
	// SFrame decrypts inbound SFrame, with a plaintext fallback.
	SFrame bool
}

// String keeps the credentials out of logs.
func (c Config) String() string {
	return fmt.Sprintf("engine.Config{CallID: %q, Direction: %d, Relay: %s:%d, Audio: %s, Video: %v, keys: [redacted]}",
		c.CallID, c.Direction, c.RelayIP, c.RelayPort, c.Audio.Codec, c.Video)
}

// GoString keeps %#v from printing them.
func (c Config) GoString() string { return c.String() }

// SetupError is a <relay> block or key a call cannot run on.
type SetupError struct{ Msg string }

func (e *SetupError) Error() string { return "the call's media: " + e.Msg }

// NewConfig is MediaSessionSpec::from_relay: the relay endpoint, token and
// integrity key out of r, our SSRC from our LID. The call's audio is MLow
// and SFrame decryption is on; the caller sets Audio and Video after.
func NewConfig(direction Direction, callID, selfLID, peerLID string, callKey []byte, r Relay) (Config, error) {
	if len(callKey) < 32 {
		return Config{}, &SetupError{"the callKey is too short for E2E keys (need 32 bytes)"}
	}
	ep, ok := r.MediaEndpoint()
	if !ok {
		return Config{}, &SetupError{"the relay has no endpoints"}
	}
	ip, port, ok := ep.PrimaryIPv4()
	if !ok {
		return Config{}, &SetupError{"the relay endpoint has no IPv4 address"}
	}
	if int(ep.TokenID) >= len(r.Tokens) || len(r.Tokens[ep.TokenID]) == 0 {
		return Config{}, &SetupError{fmt.Sprintf("the relay has no token #%d", ep.TokenID)}
	}
	var auth []byte
	if int(ep.AuthTokenID) < len(r.AuthTokens) {
		auth = r.AuthTokens[ep.AuthTokenID]
	}
	if !r.HasKey {
		return Config{}, &SetupError{"the relay has no <key> (STUN integrity key)"}
	}
	tag := r.WarpMITagLen
	if tag == 0 {
		tag = warp.MITagLen
	}
	if tag < 1 || tag > warp.MITagMaxLen {
		return Config{}, &SetupError{fmt.Sprintf("the relay advertised an unsupported WARP MI tag length: %d", tag)}
	}
	return Config{
		CallID: callID, Direction: direction, SelfLID: selfLID, PeerLID: peerLID,
		CallKey: append([]byte(nil), callKey...),
		SSRC:    kdf.ParticipantSSRC(callID, kdf.FormatParticipantID(selfLID), 0),
		Audio:   MLow16k60ms, RelayToken: r.Tokens[ep.TokenID], AuthToken: auth,
		RelayIP: ip, RelayPort: port, IntegrityKey: r.Key, WarpMITagLen: tag, SFrame: true,
	}, nil
}
