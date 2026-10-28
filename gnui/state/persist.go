package state

import (
	"errors"
	"hash/maphash"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"gonativeui/app/paths"
	"gonativeui/app/prefs"
)

// Paths are where the settings live, as upstream lays them out
// (zf:src/paths.rs): settings.json and accounts.json in Config, each
// account's settings.json in Data/accounts/<id>.
type Paths struct {
	Config, Data string
	// Cache is the app's cache root: a removed account's media cache
	// (Cache/accounts/<id>) goes with it; empty: left alone.
	Cache string
}

// AppPaths are the app's own folders (they touch no disk).
func AppPaths(appID string) (Paths, error) {
	c, err := paths.Config(appID)
	if err != nil {
		return Paths{}, err
	}
	d, err := paths.Data(appID)
	if err != nil {
		return Paths{}, err
	}
	k, err := paths.Cache(appID)
	return Paths{Config: c, Data: d, Cache: k}, err
}

func (p Paths) settings() string { return filepath.Join(p.Config, "settings.json") }
func (p Paths) roster() string   { return filepath.Join(p.Config, "accounts.json") }
func (p Paths) account(id string) string {
	return filepath.Join(p.Data, "accounts", id, "settings.json")
}

// saveDelay is how long after a change the files are written.
const saveDelay = 500 * time.Millisecond

// checkEvery is how often the state looks for settings a screen changed
// without saying so.
const checkEvery = 250 * time.Millisecond

// ErrRosterRead identifies an account-list failure in Loaded.ReadErr.
var ErrRosterRead = errors.New("could not read the account list")

// Loaded are the settings files, read (LoadSettings).
type Loaded struct {
	Settings Settings
	Roster   Roster
	// ReadErr blocks startup on a transient settings read failure.
	// Unlike malformed JSON, it must never discard saved privacy choices.
	ReadErr error
	// Errs are notices from loading, including malformed files moved aside
	// (<file>.corrupt) and replaced with defaults, as upstream does.
	Errs []error

	paths    Paths
	global   *prefs.Store[settingsFile]
	roster   *prefs.Store[Roster]
	accounts map[string]*accountStore
}

type accountStore struct {
	st    AccountSettings
	store *prefs.Store[accountFile]
	err   error
}

// Loading is a LoadSettings under way.
type Loading struct {
	done  chan struct{}
	l     *Loaded
	paths Paths
}

// LoadSettings reads the settings, the roster and every account's settings
// on a goroutine, so the window's setup goes on meanwhile; Wait joins it.
func LoadSettings(p Paths) *Loading {
	ld := &Loading{done: make(chan struct{}), paths: p}
	ld.start()
	return ld
}

func (ld *Loading) start() {
	go func() {
		defer close(ld.done)
		ld.l = load(ld.paths)
	}()
}

// Wait waits up to d for the files; ok is false when they are not read yet
// (a disk that hangs): the caller must keep private content hidden.
func (ld *Loading) Wait(d time.Duration) (*Loaded, bool) {
	select {
	case <-ld.done:
		return ld.l, true
	default:
	}
	if d <= 0 {
		return nil, false
	}
	select {
	case <-ld.done:
		return ld.l, true
	case <-time.After(d):
		return nil, false
	}
}

// Done is closed when the read under way ends. Call on the startup UI
// thread, like Wait.
func (ld *Loading) Done() <-chan struct{} { return ld.done }

// Retry restarts a completed, failed read. Call on the startup UI thread,
// like Wait; the failed store was closed without writing or renaming it.
func (ld *Loading) Retry() {
	if l, ok := ld.Wait(0); ok && l.ReadErr != nil {
		ld.done, ld.l = make(chan struct{}), nil
		ld.start()
	}
}

// Typed openers let regression tests inject a file arriving immediately before
// the read, without racing the filesystem or replacing the preferences store.
var openSettingsFile = prefs.Open[settingsFile]
var openAccountFile = prefs.Open[accountFile]

func load(p Paths) *Loaded {
	l := &Loaded{paths: p, accounts: map[string]*accountStore{}}
	opt := prefs.Options{Debounce: saveDelay, OnError: func(err error) { slog.Warn("zapfast: saving the settings", "err", err) }}
	g, err := openSettingsFile(p.settings(), defaultSettingsFile(), opt)
	if err != nil {
		l.Errs = append(l.Errs, err)
		if !errors.Is(err, prefs.ErrCorrupt) {
			l.ReadErr = err
			g.Close()
			return l
		}
	}
	l.global = g
	f := g.Get()
	st, dirty := f.settings()
	if !f.present || err != nil {
		st, dirty = DefaultSettings(), false
	}
	l.Settings = st
	if dirty {
		g.Set(st.toFile())
	}

	_, rosterMissing := os.Stat(p.roster())
	// Unlike settings, an unreadable roster stops startup (src/settings.rs:932-939).
	rosterOpt := opt
	rosterOpt.KeepCorrupt = true
	r, err := prefs.Open(p.roster(), DefaultRoster(), rosterOpt)
	if err != nil {
		l.Errs = append(l.Errs, err)
		l.ReadErr = errors.Join(ErrRosterRead, err)
		g.Close()
		_ = r.Close() // ErrNotLoaded is expected; retain the useful read/parse error.
		return l
	}
	l.roster = r
	l.Roster = r.Get()
	l.Roster.Order = slices.Clone(l.Roster.Order)
	l.Roster.Sanitize()
	if len(l.Roster.Order) == 0 {
		l.Roster = DefaultRoster()
	}
	for _, id := range l.Roster.Order {
		as := openAccount(p, id, &l.Settings, opt)
		if as.err != nil {
			l.ReadErr = as.err
			l.Errs = append(l.Errs, as.err)
			g.Close()
			r.Close()
			for _, opened := range l.accounts {
				opened.store.Close()
			}
			return l
		}
		l.accounts[id] = as
	}
	if errors.Is(rosterMissing, os.ErrNotExist) {
		// Upstream writes the roster at every start (zf:src/app.rs:1196).
		r.Set(l.Roster)
	}
	return l
}

// openAccount reads one account's settings; the first account without a
// file starts from the single-account releases' settings and saves them
// (zf:src/account.rs:230-243).
func openAccount(p Paths, id string, global *Settings, opt prefs.Options) *accountStore {
	path := p.account(id)
	s, err := openAccountFile(path, defaultAccountFile(), opt)
	if err != nil {
		slog.Warn("zapfast: reading an account's settings", "account", id, "err", err)
		if !errors.Is(err, prefs.ErrCorrupt) {
			s.Close()
			return &accountStore{err: err}
		}
	}
	f := s.Get()
	as := &accountStore{st: f.settings(), store: s}
	if !f.present && err == nil {
		as.st = DefaultAccountSettings()
		if id == "1" {
			as.st = AccountSettingsFrom(global)
			s.Set(as.st.toFile())
		}
	}
	return as
}

// persist is the state's side of the files: what was last saved, by hash.
type persist struct {
	l         *Loaded
	hash      uint64
	rosterGen uint64
	saved     Roster
	checked   time.Time
	gen       uint64 // State.settingsGen at the last check
}

// UseSettings makes the state run on the loaded settings and save them
// from now on. Call it before adding the accounts (AddAccount then takes
// each one's settings).
func (s *State) UseSettings(l *Loaded) {
	if l == nil {
		return
	}
	s.Settings = l.Settings
	s.persist = &persist{l: l, saved: l.Roster}
	s.persist.hash = hashOf(&s.Settings)
	s.gen++
}

// Roster is the accounts the settings list, in order, and which was on
// screen: what cmd/zapfast opens at start. Without settings, account "1".
func (s *State) Roster() Roster {
	if s.persist == nil {
		return DefaultRoster()
	}
	r := s.persist.l.Roster
	r.Order = slices.Clone(r.Order)
	return r
}

// NextAccountID allocates the id of an account being added.
func (s *State) NextAccountID() string {
	if s.persist == nil {
		n := 1
		for _, a := range s.Accounts {
			if v := atoi(a.ID); v >= n {
				n = v + 1
			}
		}
		return itoa(n)
	}
	r := &s.persist.l.Roster
	id := itoa(int(r.NextID))
	r.NextID++
	return id
}

// attach gives a new account its settings from the files.
func (s *State) attach(a *Account) {
	p := s.persist
	if p == nil {
		return
	}
	as := p.l.accounts[a.ID]
	if as == nil {
		as = openAccount(p.l.paths, a.ID, &s.Settings, prefs.Options{Debounce: saveDelay})
		if as.err != nil {
			// A new account added after startup has no prior in-memory
			// choices. Keep all privacy-affecting options off and attach no
			// writer; a restart can retry without having replaced its file.
			a.Settings = AccountSettings{}
			return
		}
		p.l.accounts[a.ID] = as
	}
	a.Settings = as.st
	a.prefs = as.store
	if c := p.l.paths.Cache; c != "" && SafeAccountID(a.ID) {
		a.Model.CacheDir = []byte(filepath.Join(c, "accounts", a.ID, "media") + string(filepath.Separator))
	}
	a.savedHash = hashOf(&a.Settings)
	a.Filter = FilterAll
	a.Label.Reset()
	if f, label := ParseListFilter(a.Settings.ListFilter); f != FilterAll {
		a.Filter = f
		if label != nil {
			a.Label.Add(label)
		}
	}
}

// saveSettings writes what changed: the settings, each account's, and the
// roster (the accounts, their order, the one on screen). It runs from Pump,
// every checkEvery or at once after SettingsChanged; force runs it now.
func (s *State) saveSettings(force bool) {
	p := s.persist
	if p == nil {
		return
	}
	now := s.Now()
	if !force && s.settingsGen == p.gen && now.Sub(p.checked) < checkEvery && !s.accountSettingsChanged() {
		return
	}
	p.checked, p.gen = now, s.settingsGen
	if h := hashOf(&s.Settings); h != p.hash {
		p.hash = h
		p.l.global.Set(s.Settings.toFile())
	}
	for _, a := range s.Accounts {
		if a.prefs == nil {
			continue
		}
		key := ListFilterKey(a.Filter, labelOf(a))
		if string(key) != string(a.Settings.ListFilter) {
			a.Settings.ListFilter = key
		}
		if h := hashOf(&a.Settings); h != a.savedHash {
			a.savedHash = h
			a.prefs.Set(a.Settings.toFile())
		}
		a.savedGen = a.settingsGen
	}
	r := p.l.Roster
	r.Order = r.Order[:0:0]
	for _, a := range s.Accounts {
		if !a.Removed {
			r.Order = append(r.Order, a.ID)
		}
	}
	if acct := s.Acct(); acct != nil {
		r.Active = acct.ID
	}
	if len(r.Order) > 0 && (r.Active != p.saved.Active || r.NextID != p.saved.NextID || !slices.Equal(r.Order, p.saved.Order)) {
		p.saved, p.l.Roster = r, r
		p.l.roster.Set(r)
	}
}

func (s *State) accountSettingsChanged() bool {
	for _, a := range s.Accounts {
		if a.settingsGen != a.savedGen {
			return true
		}
	}
	return false
}

func labelOf(a *Account) []byte {
	if a.Label.Len() == 0 {
		return nil
	}
	return a.Label.At(0)
}

// CloseSettings writes what is pending and stops saving (at quit: it waits
// for the disk).
func (s *State) CloseSettings() {
	p := s.persist
	if p == nil {
		return
	}
	s.saveSettings(true)
	var errs []error
	errs = append(errs, p.l.global.Close(), p.l.roster.Close())
	for _, as := range p.l.accounts {
		errs = append(errs, as.store.Close())
	}
	if err := errors.Join(errs...); err != nil {
		slog.Warn("zapfast: saving the settings at quit", "err", err)
	}
	s.persist = nil
}

// forgetAccount stops saving a removed account's settings.
func (s *State) forgetAccount(a *Account) {
	if p := s.persist; p != nil {
		if as := p.l.accounts[a.ID]; as != nil {
			as.store.Close()
			delete(p.l.accounts, a.ID)
		}
	}
	a.prefs = nil
}

var hashSeed = maphash.MakeSeed()

// hashOf hashes a settings struct's fields (bools, numbers, bytes, lists
// of bytes, nested structs) without allocating, so the state can notice a
// change nobody announced.
func hashOf[T any](v *T) uint64 {
	var h maphash.Hash
	h.SetSeed(hashSeed)
	hashValue(&h, reflect.ValueOf(v).Elem())
	return h.Sum64()
}

func hashValue(h *maphash.Hash, v reflect.Value) {
	var b [8]byte
	put := func(x uint64) {
		for i := range b {
			b[i] = byte(x >> (8 * i))
		}
		h.Write(b[:])
	}
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			h.WriteByte(1)
		} else {
			h.WriteByte(0)
		}
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uint:
		put(v.Uint())
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
		put(uint64(v.Int()))
	case reflect.Float32, reflect.Float64:
		put(uint64(int64(v.Float() * 1e6)))
	case reflect.String:
		put(uint64(v.Len()))
		h.WriteString(v.String())
	case reflect.Slice:
		put(uint64(v.Len()))
		if v.Type().Elem().Kind() == reflect.Uint8 {
			h.Write(v.Bytes())
			return
		}
		for i := range v.Len() {
			hashValue(h, v.Index(i))
		}
	case reflect.Struct:
		for i := range v.NumField() {
			hashValue(h, v.Field(i))
		}
	}
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
