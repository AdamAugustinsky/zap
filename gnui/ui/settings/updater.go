package settings

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/app/fetch"
	"gonativeui/app/selfupdate"

	"zapfast/i18n"
	"zapfast/state"
)

// The updater (zf:src/updates.rs, UP-001…UP-005), built against a local test
// feed: a folder or an address that serves latest.json and the file it names.
// Until a release feed exists Settings → Updates says so. The feed is named by
// the ZAPFAST_UPDATE_FEED environment variable (a folder, or an http(s)
// address that ends where latest.json is).
//
// latest.json:
//
//	{"version": "0.20.0", "url": "https://…/release", "notes": "…",
//	 "file": "zapfast-0.20.0.zip", "sha256": "<hex>", "signature": "<hex>"}
//
// The signature is Ed25519 over the manifest line "<sha256>  <file>\n" with the
// publisher's key; a file whose digest differs, or a signature that does not
// verify, stops the update (UP-004).

// publisherKey is the key releases are signed with (zf:assets/update-public-key.hex).
//
//go:embed update-public-key.hex
var publisherKey string

// nextPublisherKey is the next release key, backed up outside GitHub: a
// signature by it is accepted too, so that releases can move to it without
// stranding installed copies (zf:src/updates.rs additional_publisher_keys,
// assets/update-public-key-next.hex).
//
//go:embed update-public-key-next.hex
var nextPublisherKey string

// publisherKeys are the keys a release may be signed with: the current one,
// then the next.
func publisherKeys() []ed25519.PublicKey {
	var ks []ed25519.PublicKey
	for _, s := range []string{publisherKey, nextPublisherKey} {
		if k, err := hex.DecodeString(strings.TrimSpace(s)); err == nil && len(k) == ed25519.PublicKeySize {
			ks = append(ks, k)
		}
	}
	return ks
}

// maxUpdateBytes is the most an update archive may be (today's is about 21
// MB); a body that is longer is stopped at this many bytes, also when the
// server does not say how long it is. A variable so that tests can lower it.
var maxUpdateBytes int64 = 200 << 20

// errTooLarge is the refusal of an archive over the cap.
var errTooLarge error = refusal{}

// refusal is errTooLarge: comparable, so errors.Is finds it, and worded in the
// language set when it is shown, not when the program starts.
type refusal struct{}

func (refusal) Error() string {
	return i18n.T("The update is larger than Zap allows (200 MB), so it was stopped and not installed.")
}

// CheckInterval is how often the daily check runs (zf:fastframe-update
// CHECK_INTERVAL).
const CheckInterval = 24 * time.Hour

// Release is a newer version of ZapFast.
type Release struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	Notes     string `json:"notes"`
	File      string `json:"file"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

// DownloadState is where the download of the release has got to
// (zf:src/updates.rs DownloadState).
type DownloadState uint8

const (
	DownloadIdle DownloadState = iota
	Downloading
	DownloadReady
	Installing
	DownloadFailed
)

// Snapshot is what the window shows of the updater.
type Snapshot struct {
	Checking     bool
	Checked      bool   // a check has finished
	Failed       string // why the last check failed
	Release      Release
	Line         string // "0.19.0 → 0.20.0"
	State        DownloadState
	Received     int64
	Total        int64
	Error        string // why the download failed
	Support      string // why the update cannot be installed here; empty: it can
	SupportKnown bool   // the probe has answered
	Last         string // what the update that led to this run came to, for the Updates section
	At           time.Time
}

// Feed is where releases come from.
type Feed interface {
	// Latest is the newest release, or nil when there is none.
	Latest(ctx context.Context) (*Release, error)
	// Fetch streams the release's file to w, reporting progress.
	// It writes at most limit bytes: a longer body fails with errTooLarge.
	Fetch(ctx context.Context, r *Release, w io.Writer, limit int64, progress func(received, total int64)) error
}

// Installer replaces the running app with the downloaded, verified file and
// restarts it. TODO(updater): the install step needs the release packaging
// (plan: Packaging); until then Restart to update reports it cannot.
type Installer func(path string) error

// Updates is the updater: it checks a feed, downloads and verifies a
// release, and reports its state to the window and to the shell's banner.
type Updates struct {
	mu                          sync.Mutex
	snap                        Snapshot
	feed                        Feed
	keys                        []ed25519.PublicKey
	install                     Installer
	dir                         string
	file                        string // the verified download
	wake                        func()
	now                         func() time.Time
	st                          *state.State
	tick                        func(*state.Settings) // what else runs with the updater's hook, which the shell calls with or without a window (Screen: a finished chat lock verifier)
	sys                         SelfUpdater
	quit                        func()
	probed                      bool
	verified                    Release // the release the download was verified against
	frames                      int
	started, noted, resultReady atomic.Bool
	result                      selfupdate.Result
	proxy                       string // the proxy the feed goes through: the setting, else the environment's
}

// NewUpdates returns the updater over the feed the environment names, or
// over none.
func NewUpdates() *Updates {
	u := &Updates{now: time.Now, wake: func() {}}
	u.keys = publisherKeys()
	if v := os.Getenv("ZAPFAST_UPDATE_FEED"); v != "" {
		u.SetFeed(NewFeed(v))
	}
	u.sys = osSelf{}
	u.install = u.selfInstall
	return u
}

// SetFeed replaces the feed; SetKey the publisher's key; SetInstaller the
// installer; SetWake what asks the windows for a frame.
func (u *Updates) SetFeed(f Feed) {
	if h, ok := f.(*httpFeed); ok {
		h.proxy = u.currentProxy
	}
	u.mu.Lock()
	u.feed = f
	u.mu.Unlock()
}
func (u *Updates) SetKey(k ed25519.PublicKey) { u.SetKeys(k) }

// SetKeys replaces the keys a release may be signed with (tests).
func (u *Updates) SetKeys(ks ...ed25519.PublicKey) { u.mu.Lock(); u.keys = ks; u.mu.Unlock() }
func (u *Updates) SetInstaller(i Installer)        { u.mu.Lock(); u.install = i; u.mu.Unlock() }
func (u *Updates) SetWake(f func())                { u.mu.Lock(); u.wake = f; u.mu.Unlock() }
func (u *Updates) SetDir(dir string)               { u.mu.Lock(); u.dir = dir; u.mu.Unlock() }

// attached is the state attach gave (nil before the window first opened).
func (u *Updates) attached() *state.State {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

// attach lets the updater wake the main window as well.
func (u *Updates) attach(s *state.State) {
	u.mu.Lock()
	u.st = s
	u.mu.Unlock()
}

// Snapshot is the updater's state now.
func (u *Updates) Snapshot() Snapshot {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.snap
}

// Available is the release a check found, for the banner "ZapFast X is
// available" (UP-002).
func (u *Updates) Available() (Release, bool) {
	s := u.Snapshot()
	return s.Release, s.Release.Version != ""
}

// HasFeed reports whether a feed is configured.
func (u *Updates) HasFeed() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.feed != nil
}

func (u *Updates) poke() {
	u.mu.Lock()
	w, st := u.wake, u.st
	u.mu.Unlock()
	w()
	if st != nil {
		st.Wake()
	}
}

// CheckIfDue checks when the setting is on, a day has passed since the last
// check and no download is running (UP-001, once a day), and downloads the
// release at once when automatic downloads are on (UP-005). The shell calls it
// once per frame.
func (u *Updates) CheckIfDue(settings *state.Settings) {
	if u.tick != nil {
		u.tick(settings)
	}
	u.mu.Lock()
	if string(settings.Proxy) != u.proxy { // the setting changes on the main thread; the feed reads it from a worker
		u.proxy = string(settings.Proxy)
	}
	probe := !u.probed
	due := settings.CheckForUpdates && u.feed != nil && !u.snap.Checking && u.now().Sub(u.snap.At) >= CheckInterval
	auto := settings.CheckForUpdates && settings.AutoUpdate && u.snap.Release.Version != "" &&
		u.snap.State == DownloadIdle && u.snap.SupportKnown && u.snap.Support == ""
	u.mu.Unlock()
	if probe {
		u.probe()
	}
	if due {
		u.Check()
	}
	if auto {
		u.Download()
	}
}

// Check asks the feed for a newer release.
func (u *Updates) Check() {
	u.mu.Lock()
	feed := u.feed
	if u.snap.Checking {
		u.mu.Unlock()
		return
	}
	u.snap.Checking, u.snap.Failed = true, ""
	u.snap.At = u.now()
	u.mu.Unlock()
	u.poke()
	go func() {
		var rel *Release
		var err error
		if feed == nil {
			err = errNoFeed
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rel, err = feed.Latest(ctx)
		}
		u.mu.Lock()
		u.snap.Checking, u.snap.Checked = false, err == nil
		switch {
		case err != nil:
			u.snap.Failed = err.Error()
		case rel != nil && newer(rel.Version, state.Version):
			if u.snap.Release.Version != rel.Version {
				u.snap.State, u.snap.Error, u.file = DownloadIdle, "", ""
			}
			u.snap.Release = *rel
			u.snap.Line = state.Version + " → " + rel.Version
		default:
			u.snap.Release, u.snap.Line = Release{}, ""
		}
		u.mu.Unlock()
		u.poke()
	}()
}

var errNoFeed = errors.New("no update feed is configured")

// newer reports whether version a is newer than b (dotted numbers).
func newer(a, b string) bool {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// Download fetches the release and verifies it against its signed digest
// (UP-003, UP-004).
func (u *Updates) Download() {
	u.mu.Lock()
	rel, feed, keys, dir := u.snap.Release, u.feed, u.keys, u.dir
	if (u.snap.State != DownloadIdle && u.snap.State != DownloadFailed) || rel.Version == "" || feed == nil || !u.snap.SupportKnown || u.snap.Support != "" {
		u.mu.Unlock()
		return
	}
	u.snap.State, u.snap.Received, u.snap.Total, u.snap.Error = Downloading, 0, 0, ""
	u.mu.Unlock()
	u.poke()
	go func() {
		path, err := u.fetch(feed, &rel, keys, dir)
		u.mu.Lock()
		if err != nil {
			u.snap.State, u.snap.Error = DownloadFailed, err.Error()
		} else {
			u.snap.State, u.file, u.verified = DownloadReady, path, rel
		}
		u.mu.Unlock()
		u.poke()
	}()
}

// fetch downloads rel to a temporary file and verifies it; the file is
// removed when it does not verify.
func (u *Updates) fetch(feed Feed, rel *Release, keys []ed25519.PublicKey, dir string) (string, error) {
	// the signature on the manifest first: a feed that cannot show one is not
	// asked for the archive, which could otherwise fill the disk unchecked
	if err := verifyManifest(rel, keys); err != nil {
		return "", err
	}
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "zapfast-update-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(name)
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	h := sha256.New()
	err = feed.Fetch(ctx, rel, io.MultiWriter(f, h), maxUpdateBytes, func(received, total int64) {
		u.mu.Lock()
		u.snap.Received, u.snap.Total = received, total
		u.mu.Unlock()
		u.poke()
	})
	if err != nil {
		return fail(err)
	}
	// the digest is checked, and the signature on it; the progress bar reads
	// "Checking download…" while this runs
	if err := verifyRelease(rel, hex.EncodeToString(h.Sum(nil)), keys); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// verifyRelease checks that the downloaded file's digest is the release's,
// and that the release's signature verifies under the publisher's key.
func verifyRelease(rel *Release, digest string, keys []ed25519.PublicKey) error {
	if err := verifyManifest(rel, keys); err != nil {
		return err
	}
	if !strings.EqualFold(rel.SHA256, digest) {
		return errors.New(i18n.T("The download does not match its checksum, so it was not installed."))
	}
	return nil
}

// verifyManifest checks the release's signature over "<sha256>  <file>\n"
// under the publisher's key. It needs only the manifest, so it runs before a
// byte of the archive is downloaded: nothing unauthenticated is fetched or
// written.
func verifyManifest(rel *Release, keys []ed25519.PublicKey) error {
	if len(keys) == 0 {
		return errors.New(i18n.T("The update cannot be verified: this build has no publisher key."))
	}
	sig, err := hex.DecodeString(rel.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New(i18n.T("The update is not signed, so it was not installed."))
	}
	if sum, err := hex.DecodeString(rel.SHA256); err != nil || len(sum) != sha256.Size || rel.File == "" {
		return errors.New(i18n.T("The update's checksum is not valid, so it was not installed."))
	}
	// The signature covers the checksum and the file name, not the version the
	// feed announces: an older release's package, name and signature could be
	// replayed under a newer version. The name `gnui package -zip` gives the
	// archive carries its version, so it is the version's proof (upstream
	// derives the name from the version it asks for, likewise).
	if rel.File != releaseFileName(rel.Version) {
		return errors.New(i18n.T("The update's file does not belong to the version it announces, so it was not installed."))
	}
	line := manifestLine(rel)
	for _, k := range keys {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, line, sig) {
			return nil
		}
	}
	return errors.New(i18n.T("The update's signature is not valid, so it was not installed."))
}

// releaseFileName is the archive a version is published as: what
// `gnui package -zip` writes (<Name>-<version>.zip, the name without spaces)
// and packaging/macos/gnui-sign-update.sh signs. "" for a version that cannot
// be part of a file name, which no file matches.
func releaseFileName(version string) string {
	v := strings.TrimPrefix(version, "v")
	if v == "" || v[0] < '0' || v[0] > '9' || strings.Trim(v, "0123456789.+-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return ""
	}
	return "ZapFast-" + v + ".zip"
}

func manifestLine(rel *Release) []byte {
	return []byte(strings.ToLower(rel.SHA256) + "  " + rel.File + "\n")
}

// Install restarts into the downloaded update (UP-003: Restart to update).
func (u *Updates) Install() {
	u.mu.Lock()
	if u.snap.State != DownloadReady {
		u.mu.Unlock()
		return
	}
	u.snap.State = Installing
	install, file := u.install, u.file
	u.mu.Unlock()
	u.poke()
	go func() {
		err := errors.New(i18n.T("Installing updates is not built into this version yet."))
		if install != nil {
			err = install(file)
		}
		if err != nil {
			u.mu.Lock()
			u.snap.State, u.snap.Error = DownloadFailed, err.Error()
			u.mu.Unlock()
			u.poke()
		}
	}()
}

// NewFeed is the feed at base: a folder, or an http(s) address under which
// latest.json and the file it names are served.
func NewFeed(base string) Feed {
	if u, err := url.Parse(base); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return &httpFeed{base: strings.TrimRight(base, "/"), host: u.Hostname()}
	}
	return dirFeed(base)
}

type dirFeed string

func (d dirFeed) Latest(ctx context.Context) (*Release, error) {
	b, err := os.ReadFile(filepath.Join(string(d), "latest.json"))
	if err != nil {
		return nil, err
	}
	return parseRelease(b)
}

func (d dirFeed) Fetch(ctx context.Context, r *Release, w io.Writer, limit int64, progress func(int64, int64)) error {
	f, err := os.Open(filepath.Join(string(d), filepath.Base(r.File)))
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	return copyProgress(ctx, w, f, st.Size(), limit, progress)
}

type httpFeed struct {
	base  string
	host  string
	proxy func(host string) *fetch.Proxy // nil: the system's
}

// opts are the transfer's options: only the version is sent (UP-001), and the
// app's proxy is used as upstream's updater does (zf:src/updates.rs).
func (h *httpFeed) opts() fetch.Options {
	o := fetch.Options{Headers: map[string]string{"User-Agent": "ZapFast/" + state.Version}}
	if h.proxy != nil {
		o.Proxy = h.proxy(h.host)
	}
	return o
}

func feedError(err error) error {
	var se *fetch.StatusError
	if errors.As(err, &se) {
		return errors.New(i18n.Format(i18n.T("the update feed answered {status}"), "{status}", strconv.Itoa(se.Code)))
	}
	if errors.Is(err, fetch.ErrTooLarge) {
		return errTooLarge
	}
	return err
}

func (h *httpFeed) Latest(ctx context.Context) (*Release, error) {
	b, _, err := fetch.Bytes(ctx, h.base+"/latest.json", 1<<20, h.opts())
	if err != nil {
		return nil, feedError(err)
	}
	return parseRelease(b)
}

func (h *httpFeed) Fetch(ctx context.Context, r *Release, w io.Writer, limit int64, progress func(int64, int64)) error {
	o := h.opts()
	o.MaxBytes = limit
	var last time.Time
	var got, total int64
	o.Progress = func(n, t int64) {
		got, total = n, t
		if now := time.Now(); now.Sub(last) > 50*time.Millisecond {
			last = now
			progress(n, t)
		}
	}
	if _, err := fetch.Get(ctx, h.base+"/"+url.PathEscape(filepath.Base(r.File)), w, o); err != nil {
		return feedError(err)
	}
	progress(got, max(total, got))
	return nil
}

// currentProxy is the proxy the updater's transfers use: the setting, else
// ALL_PROXY or HTTPS_PROXY (zf:src/proxy.rs), nil when neither names one (the
// system's settings then apply). With the setting empty, a host NO_PROXY names
// is reached directly (ProxyNone), as upstream's updater does.
func (u *Updates) currentProxy(host string) *fetch.Proxy {
	u.mu.Lock()
	setting := u.proxy
	u.mu.Unlock()
	if setting == "" {
		if proxyBypassed(host, firstEnv("NO_PROXY", "no_proxy")) {
			return &fetch.Proxy{Kind: fetch.ProxyNone} // direct, not through the system's proxy
		}
		setting = firstEnv(proxyVars...)
	}
	return parseProxy(setting)
}

// proxyBypassed is zf:src/proxy.rs bypassed: whether NO_PROXY names host.
func proxyBypassed(host, noProxy string) bool {
	for _, entry := range strings.Split(noProxy, ",") {
		entry = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(entry), "*."), ".")
		if entry == "*" || (entry != "" && (host == entry || strings.HasSuffix(host, "."+entry))) {
			return true
		}
	}
	return false
}

// parseProxy maps a proxy address (zf:src/proxy.rs Proxy::parse) to the
// transfer's proxy; nil for none or one that is not valid. socks5:// resolves
// names at the proxy, as socks5h:// does (fetch has no local resolution).
func parseProxy(value string) *fetch.Proxy {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	p := &fetch.Proxy{Host: u.Hostname()}
	switch u.Scheme {
	case "http":
		p.Kind, p.Port = fetch.ProxyHTTP, 80
	case "socks5", "socks", "socks5h":
		p.Kind, p.Port = fetch.ProxySOCKS5, 1080
	default:
		return nil
	}
	if n, err := strconv.Atoi(u.Port()); err == nil {
		p.Port = n
	}
	if u.User != nil {
		p.User = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	return p
}

// releasesPage is where the release notes link goes when the feed names no
// usable page.
const releasesPage = "https://github.com/crmne/zapfast/releases"

// releaseURL is the release page the feed names if it is one a person may be
// sent to: an absolute https address with a host and no credentials. The feed's
// signature covers the checksum and the file name, not this address, so
// anything else (file:, a custom scheme the system hands to some app, plain
// http, a relative path, nothing) is replaced by the releases page. Upstream's
// address is GitHub's html_url, which its transport vouches for; the test feed
// has no such host to name.
func releaseURL(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return releasesPage
	}
	u.Scheme = "https"
	return u.String()
}

func parseRelease(b []byte) (*Release, error) {
	var r Release
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Version == "" {
		return nil, nil
	}
	r.URL = releaseURL(r.URL)
	return &r, nil
}

func copyProgress(ctx context.Context, w io.Writer, r io.Reader, total, limit int64, progress func(int64, int64)) error {
	if limit > 0 && total > limit {
		return errTooLarge
	}
	buf := make([]byte, 64<<10)
	var n int64
	last := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m, err := r.Read(buf)
		if m > 0 {
			if limit > 0 && n+int64(m) > limit {
				return errTooLarge
			}
			if _, werr := w.Write(buf[:m]); werr != nil {
				return werr
			}
			n += int64(m)
			if now := time.Now(); now.Sub(last) > 50*time.Millisecond || err == io.EOF {
				last = now
				progress(n, total)
			}
		}
		if err == io.EOF {
			progress(n, max(total, n))
			return nil
		}
		if err != nil {
			return err
		}
	}
}
