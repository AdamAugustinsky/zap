// from zapfast-go 3104c46, MIT (internal/backend/sticker_packs.go:
// encryptPackThumbnail and uploadPackThumbnail). Changed for Zap: it is a
// method of the whatsmeow client wrapper, and the proxy is the client's.

package wa

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/util/cbcutil"
	"go.mau.fi/whatsmeow/util/hkdfutil"
)

// whatsmeow uploads the pack's zip but has no upload of its thumbnail,
// which shares the zip's media key: this follows WhatsApp's protocol for
// it over whatsmeow's media connection.

func (w *waClient) UploadStickerPack(ctx context.Context, zip, thumbnail []byte) (whatsmeow.UploadResponse, whatsmeow.UploadResponse, error) {
	pack, err := w.c.Upload(ctx, zip, whatsmeow.MediaStickerPack)
	if err != nil {
		return pack, whatsmeow.UploadResponse{}, err
	}
	thumb, err := w.uploadPackThumbnail(ctx, thumbnail, pack.MediaKey)
	return pack, thumb, err
}

// encryptPackThumbnail encrypts a pack's thumbnail with the pack's key.
func encryptPackThumbnail(plaintext, key []byte) ([]byte, whatsmeow.UploadResponse, error) {
	var response whatsmeow.UploadResponse
	if len(plaintext) > 1<<20 {
		return nil, response, errors.New("sticker thumbnail exceeds size limit")
	}
	if len(key) != 32 {
		return nil, response, errors.New("invalid sticker pack media key")
	}
	derived := hkdfutil.SHA256(key, nil, []byte("WhatsApp Sticker Pack Thumbnail Keys"), 112)
	ciphertext, err := cbcutil.Encrypt(derived[16:48], derived[:16], plaintext)
	if err != nil {
		return nil, response, err
	}
	mac := hmac.New(sha256.New, derived[48:80])
	mac.Write(derived[:16])
	mac.Write(ciphertext)
	encrypted := append(ciphertext, mac.Sum(nil)[:10]...)
	plainHash := sha256.Sum256(plaintext)
	encHash := sha256.Sum256(encrypted)
	response.MediaKey = append([]byte(nil), key...)
	response.FileSHA256 = plainHash[:]
	response.FileEncSHA256 = encHash[:]
	response.FileLength = uint64(len(plaintext))
	return encrypted, response, nil
}

func (w *waClient) uploadPackThumbnail(ctx context.Context, data, key []byte) (whatsmeow.UploadResponse, error) {
	encrypted, response, err := encryptPackThumbnail(data, key)
	if err != nil {
		return response, err
	}
	connection, err := w.c.DangerousInternals().RefreshMediaConn(ctx, false)
	if err != nil || connection == nil || len(connection.Hosts) == 0 {
		return response, errors.New("sticker thumbnail media connection unavailable")
	}
	host := connection.Hosts[0].Hostname
	if strings.ContainsAny(host, "/:@") || !(strings.HasSuffix(host, ".whatsapp.net") || strings.HasSuffix(host, ".fbcdn.net")) {
		return response, errors.New("untrusted sticker media host")
	}
	token := base64.URLEncoding.EncodeToString(response.FileEncSHA256)
	endpoint := url.URL{Scheme: "https", Host: host, Path: "/mms/thumbnail-sticker-pack/" + token, RawQuery: url.Values{"auth": {connection.Auth}, "token": {token}}.Encode()}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 15 * time.Second
	w.mu.Lock()
	px := w.proxy
	w.mu.Unlock()
	if px != "" {
		u, err := url.Parse(px)
		if err != nil {
			return response, errors.New("invalid proxy")
		}
		// net/http dials http, socks5 and socks5h proxies itself.
		transport.Proxy = http.ProxyURL(u)
	}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 30 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encrypted))
	if err != nil {
		return response, errors.New("thumbnail request could not be prepared")
	}
	request.Header.Set("Origin", "https://web.whatsapp.com")
	request.Header.Set("Referer", "https://web.whatsapp.com/")
	request.ContentLength = int64(len(encrypted))
	result, err := client.Do(request)
	if err != nil {
		return response, errors.New("sticker thumbnail upload failed")
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		return response, errors.New("sticker thumbnail upload was not accepted")
	}
	if err = json.NewDecoder(io.LimitReader(result.Body, 1<<20)).Decode(&response); err != nil || response.DirectPath == "" {
		return response, errors.New("sticker thumbnail response invalid")
	}
	return response, nil
}
