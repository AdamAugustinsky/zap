package wa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"

	"zapfast/abi"
	"zapfast/gocore/out"
)

// GIFs come from GIPHY with the reader's own API key, as upstream's
// search_gifs (src/backend/worker.rs) does: trending for an empty query,
// 24 results rated pg-13, the stills kept in cache/gifs.

const (
	// giphyAPI is GIPHY's GIF API; tests point e.giphy at a local server.
	giphyAPI = "https://api.giphy.com/v1/gifs"
	// gifResults is how many results one search asks for.
	gifResults = 24
	// gifMax bounds a downloaded clip, gifStillMax a still and giphyMax
	// GIPHY's answer.
	gifMax      = 16 << 20
	gifStillMax = 2 << 20
	giphyMax    = 4 << 20
)

// web is the client for what is not WhatsApp's: Options.Web, else one
// over the proxy setting.
func (e *Engine) web() *http.Client {
	if e.opts.Web != nil {
		return e.opts.Web
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if e.proxy != "" {
		if u, err := url.Parse(e.proxy); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}
}

// httpStatus is a refused request's status code.
type httpStatus int

func (s httpStatus) Error() string { return fmt.Sprintf("HTTP %d", int(s)) }

// fetchURL reads a URL, at most limit bytes.
func fetchURL(ctx context.Context, web *http.Client, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := web.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpStatus(resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("the file is too large")
	}
	return data, nil
}

// gifFailure is a search that found nothing to show.
func gifFailure(msg string, badKey bool) *abi.GifFailure {
	return &abi.GifFailure{Message: b(out.Clip(msg, 1024)), BadKey: badKey}
}

// giphyAnswer is the part of GIPHY's answer a search reads.
type giphyAnswer struct {
	Meta struct {
		Status int    `json:"status"`
		Msg    string `json:"msg"`
	} `json:"meta"`
	Data []struct {
		ID     string                       `json:"id"`
		Images map[string]map[string]string `json:"images"`
	} `json:"data"`
}

// searchGIPHY is upstream's search_gifs: the results with their stills
// downloaded into dir.
func searchGIPHY(ctx context.Context, web *http.Client, api, query, key, dir string) ([]abi.Gif, *abi.GifFailure) {
	if key == "" {
		return nil, gifFailure("GIF search needs a GIPHY API key.", true)
	}
	params := url.Values{"api_key": {key}, "limit": {strconv.Itoa(gifResults)}, "rating": {"pg-13"}}
	endpoint := api + "/trending"
	if q := strings.TrimSpace(query); q != "" {
		endpoint = api + "/search"
		params.Set("q", q)
	}
	body, err := fetchURL(ctx, web, endpoint+"?"+params.Encode(), giphyMax)
	if status, ok := errors.AsType[httpStatus](err); ok && (status == 401 || status == 403) {
		return nil, gifFailure(fmt.Sprintf("GIPHY rejected the API key (error %d).", int(status)), true)
	}
	if err != nil {
		// The error may quote the URL, and with it the key.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, gifFailure("GIPHY request failed: "+err.Error(), false)
	}
	var answer giphyAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, gifFailure("Invalid GIPHY response: "+err.Error(), false)
	}
	if answer.Meta.Status >= 400 && answer.Meta.Msg != "" {
		return nil, gifFailure("GIPHY: "+answer.Meta.Msg, answer.Meta.Status == 401 || answer.Meta.Status == 403)
	}
	if answer.Data == nil {
		return nil, gifFailure("GIPHY response contained no results", false)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, gifFailure(err.Error(), false)
	}
	pick := func(images map[string]map[string]string, field string, names ...string) string {
		for _, n := range names {
			if v := images[n][field]; v != "" {
				return v
			}
		}
		return ""
	}
	size := func(images map[string]map[string]string, field string) uint32 {
		if n, err := strconv.ParseUint(images["fixed_width"][field], 10, 32); err == nil {
			return uint32(n)
		}
		return 200
	}
	var gifs []abi.Gif
	var stills []string
	for _, item := range answer.Data {
		mp4 := pick(item.Images, "mp4", "fixed_width", "downsized_small", "original")
		if item.ID == "" || mp4 == "" {
			continue
		}
		gifs = append(gifs, abi.Gif{ID: b(item.ID), Mp4: b(mp4), Width: size(item.Images, "width"), Height: size(item.Images, "height")})
		stills = append(stills, pick(item.Images, "url", "fixed_width_small_still", "fixed_width_still", "original_still"))
	}
	var wg sync.WaitGroup
	for i := range gifs {
		if stills[i] == "" {
			continue
		}
		path := filepath.Join(dir, sanitizeName(string(gifs[i].ID))+".jpg")
		if _, err := os.Stat(path); err == nil {
			gifs[i].Still, gifs[i].HasStill = b(path), true
			continue
		}
		wg.Go(func() {
			data, err := fetchURL(ctx, web, stills[i], gifStillMax)
			if err == nil && writePrivate(path, data) == nil {
				gifs[i].Still, gifs[i].HasStill = b(path), true
			}
		})
	}
	wg.Wait()
	return gifs, nil
}

// sanitizeName is a name as a file name: its ASCII letters and digits,
// '_' for the rest (upstream's sanitize).
func sanitizeName(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('_')
		}
	}
	return sb.String()
}

// searchGifs answers with Gifs: the results, or why there are none.
func (e *Engine) searchGifs(query, key string) {
	web, api, dir := e.web(), e.giphy, filepath.Join(e.cacheDir, "gifs")
	e.job(func(ctx context.Context) func() {
		gifs, failure := searchGIPHY(ctx, web, api, query, key, dir)
		return func() {
			q := b(out.Clip(query, 1024))
			if failure != nil {
				e.out.Emit(&abi.Event{Tag: abi.EventTagGifs, Gifs: abi.EventGifs{Query: q, Error: *failure, HasError: true, Batch: e.out.Batch(), Last: true}})
				return
			}
			out.List(e.out, gifs, func(batch uint32, last bool, chunk []abi.Gif) *abi.Event {
				return &abi.Event{Tag: abi.EventTagGifs, Gifs: abi.EventGifs{Query: q, Batch: batch, Last: last, Gifs: chunk}}
			})
		}
	})
}

// sendGif downloads a GIF's clip and sends it as a looping video, as
// upstream's send_gif does.
func (e *Engine) sendGif(c *abi.CommandSendGif) {
	j, ctx, ok := e.ready(c.Chat, c.HasQuoting, c.Quoting, func(*recordBlobs) abi.Unsent {
		return abi.Unsent{Tag: abi.UnsentTagGif}
	})
	if !ok {
		return
	}
	mp4, w, h := string(c.Gif.Mp4), c.Gif.Width, c.Gif.Height
	u, err := url.Parse(mp4)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" {
		e.emitError("Could not send the GIF: it has no clip")
		return
	}
	web := e.web()
	e.sendUpload(string(c.Chat), j, func(cx context.Context) (*upload, error) {
		data, err := fetchURL(cx, web, mp4, gifMax)
		if err != nil {
			return nil, err
		}
		return &upload{kind: whatsmeow.MediaVideo, mime: "video/mp4", data: data, width: w, height: h, gif: true}, nil
	}, "", ctx, nil, "GIF")
}
