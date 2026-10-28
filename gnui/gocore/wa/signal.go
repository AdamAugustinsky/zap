// from zapfast-go 3104c46, MIT (internal/media/signal.go: ParseSignalURL,
// DecryptSignalBlob, ParseSignalManifest, ImportSignalPack, and
// signal-ca.pem). Changed for Zap: as upstream's import_signal_pack
// (src/backend/sticker_import.rs), a sticker that cannot be fetched is left
// out rather than failing the pack, each keeps its emoji in its metadata,
// and the pack is written with writePack.

package wa

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"

	"zapfast/gocore/av"
)

//go:embed signal-ca.pem
var signalCA []byte

// signalCDN is where signal.art's packs are; tests point e.signal at a
// local server.
const signalCDN = "https://cdn.signal.org/stickers/"

// signalStickerMax bounds one encrypted sticker and signalPackMax a pack.
const (
	signalStickerMax = 4 << 20
	signalPackMax    = 64 << 20
)

type signalSticker struct {
	id    uint64
	emoji string
}

func parseSignalURL(raw string) (string, []byte, error) {
	if len(raw) > 4096 {
		return "", nil, errors.New("Signal pack link is too long")
	}
	fragment := strings.TrimSpace(raw)
	if u, err := url.Parse(fragment); err == nil && u.Host != "" {
		if u.Scheme != "https" || u.Hostname() != "signal.art" {
			return "", nil, errors.New("use a complete https://signal.art pack link")
		}
		fragment = u.Fragment
	}
	values, err := url.ParseQuery(strings.ReplaceAll(fragment, ";", "&"))
	if err != nil {
		return "", nil, errors.New("invalid Signal pack link")
	}
	id := strings.ToLower(values.Get("pack_id"))
	idBytes, err := hex.DecodeString(id)
	if err != nil || len(idBytes) != 16 {
		return "", nil, errors.New("Signal pack ID is missing or invalid")
	}
	key, err := hex.DecodeString(values.Get("pack_key"))
	if err != nil || len(key) != 32 {
		return "", nil, errors.New("Signal pack key is missing or invalid")
	}
	return id, key, nil
}

func decryptSignalBlob(payload, key []byte) ([]byte, error) {
	if len(key) != 32 || len(payload) < 64 || len(payload) > signalStickerMax {
		return nil, errors.New("invalid encrypted sticker")
	}
	derived, err := hkdf.Key(sha256.New, key, make([]byte, 32), "Sticker Pack", 64)
	if err != nil {
		return nil, err
	}
	defer clear(derived)
	text, tag := payload[:len(payload)-32], payload[len(payload)-32:]
	mac := hmac.New(sha256.New, derived[32:])
	mac.Write(text)
	if !hmac.Equal(tag, mac.Sum(nil)) {
		return nil, errors.New("Signal pack authentication failed")
	}
	iv, encrypted := text[:16], text[16:]
	if len(encrypted) == 0 || len(encrypted)%16 != 0 {
		return nil, errors.New("invalid sticker ciphertext")
	}
	block, err := aes.NewCipher(derived[:32])
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, encrypted)
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > 16 || pad > len(plain) {
		clear(plain)
		return nil, errors.New("invalid sticker padding")
	}
	for _, b := range plain[len(plain)-pad:] {
		if int(b) != pad {
			clear(plain)
			return nil, errors.New("invalid sticker padding")
		}
	}
	return plain[:len(plain)-pad], nil
}

func parseSignalManifest(data []byte) (string, []signalSticker, error) {
	if len(data) > 1<<20 {
		return "", nil, errors.New("sticker manifest too large")
	}
	title := ""
	var stickers []signalSticker
	seen := map[uint64]bool{}
	for len(data) > 0 {
		number, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return "", nil, errors.New("invalid sticker manifest")
		}
		data = data[n:]
		if typ != protowire.BytesType {
			n := protowire.ConsumeFieldValue(number, typ, data)
			if n < 0 {
				return "", nil, errors.New("invalid manifest wire type")
			}
			data = data[n:]
			continue
		}
		body, n := protowire.ConsumeBytes(data)
		if n < 0 {
			return "", nil, errors.New("invalid manifest field")
		}
		data = data[n:]
		switch number {
		case 1:
			if utf8.Valid(body) && len(body) <= 1024 {
				title = string(body)
			}
		case 4:
			var sticker signalSticker
			valid := false
			for len(body) > 0 {
				field, wire, n := protowire.ConsumeTag(body)
				if n < 0 {
					return "", nil, errors.New("invalid sticker manifest entry")
				}
				body = body[n:]
				switch {
				case field == 1 && wire == protowire.VarintType:
					id, n := protowire.ConsumeVarint(body)
					if n < 0 {
						return "", nil, errors.New("invalid sticker ID")
					}
					sticker.id, valid = id, true
					body = body[n:]
				case field == 2 && wire == protowire.BytesType:
					emoji, n := protowire.ConsumeBytes(body)
					if n < 0 {
						return "", nil, errors.New("invalid sticker emoji")
					}
					if utf8.Valid(emoji) && len(emoji) <= 128 {
						sticker.emoji = string(emoji)
					}
					body = body[n:]
				default:
					n := protowire.ConsumeFieldValue(field, wire, body)
					if n < 0 {
						return "", nil, errors.New("invalid manifest entry")
					}
					body = body[n:]
				}
			}
			if valid && !seen[sticker.id] {
				stickers = append(stickers, sticker)
				seen[sticker.id] = true
				if len(stickers) > packLimit {
					return "", nil, fmt.Errorf("pack exceeds %d stickers", packLimit)
				}
			}
		}
	}
	if len(stickers) == 0 {
		return "", nil, errors.New("this Signal pack has no stickers")
	}
	if strings.TrimSpace(title) == "" {
		title = "Signal stickers"
	}
	return title, stickers, nil
}

// signalWeb is a client that trusts only Signal's CDN authority, over the
// proxy setting.
func signalWeb(proxy string) (*http.Client, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(signalCA) {
		return nil, errors.New("invalid Signal certificate authority")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 3 || r.URL.Scheme != "https" || r.URL.Hostname() != "cdn.signal.org" {
			return errors.New("unexpected Signal CDN redirect")
		}
		return nil
	}}, nil
}

// importSignalPack downloads a signal.art pack into a new pack under packs,
// and returns its folder's name.
func importSignalPack(ctx context.Context, web *http.Client, cdn, rawURL, packs string) (string, error) {
	id, key, err := parseSignalURL(rawURL)
	if err != nil {
		return "", err
	}
	defer clear(key)
	var total sync.Mutex
	downloaded := 0
	fetch := func(suffix string) ([]byte, error) {
		encrypted, err := fetchURL(ctx, web, cdn+id+"/"+suffix, signalStickerMax)
		if err != nil {
			return nil, errors.New("signal.art request failed")
		}
		total.Lock()
		downloaded += len(encrypted)
		over := downloaded > signalPackMax
		total.Unlock()
		if over {
			return nil, errors.New("Signal pack exceeds 64 MiB")
		}
		return decryptSignalBlob(encrypted, key)
	}
	manifest, err := fetch("manifest.proto")
	if err != nil {
		return "", err
	}
	title, stickers, err := parseSignalManifest(manifest)
	if err != nil {
		return "", err
	}
	files := make([][]byte, len(stickers))
	lanes := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, s := range stickers {
		wg.Go(func() {
			lanes <- struct{}{}
			defer func() { <-lanes }()
			data, err := fetch(fmt.Sprintf("full/%d", s.id))
			if err != nil {
				return
			}
			webp, ok := webpBytes(data)
			if !ok {
				return
			}
			info := av.StickerInfo{PackID: "signal." + id, PackName: title}
			if s.emoji != "" {
				info.Emojis = []string{s.emoji}
			}
			if tagged, err := av.WriteStickerInfo(webp, info); err == nil {
				webp = tagged
			}
			files[i] = webp
		})
	}
	wg.Wait()
	var kept [][]byte
	for _, f := range files {
		if f != nil {
			kept = append(kept, f)
		}
	}
	return writePack(packs, title, kept)
}
