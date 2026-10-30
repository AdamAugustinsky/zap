package migrate

// The differential test: an account the Rust core kept is migrated, and the
// Go archive must show what the Rust core of Zap 0.19.0 showed for the same
// store. The oracle is testdata/oracle/*.json, written by that core's own
// Archive (testdata/oracle/migration_oracle.patch, a test on zfcore at
// 5a57d37, whose Rust sources are 0.19.0's):
//
//	git worktree add --detach /tmp/zap-0190 5a57d37
//	cd /tmp/zap-0190 && git apply <this folder>/testdata/oracle/migration_oracle.patch
//	ZF_WRITE_MIGRATION_ORACLE=1 cargo test -p zapfast_core --test migration_oracle
//
// It writes oracle-archive.db (a seeded synthetic archive) and the reports
// for it and for testdata/archive.db. Everything in them is synthetic.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"zapfast/gocore/store"
	"zapfast/gocore/store/sqlite"
)

// oracleMediaRoot starts the oracle archive's media paths; the test moves
// them under its own folder, where the files whose names do not start with
// "missing-" exist.
const oracleMediaRoot = "/zap-oracle-media/"

// oracleArchiveSHA256 is testdata/oracle/oracle-archive.db's, as written.
const oracleArchiveSHA256 = "deae779c403030de0f63de43015161f8f8ba5b363d994b951d48b6a91ae079aa"

// rustReport is what the Rust core reported for one archive.
type rustReport struct {
	Now        int64                    `json:"now"`
	Chats      []rustChat               `json:"chats"`
	Messages   map[string][]rustMessage `json:"messages"`
	Stars      map[string][]string      `json:"stars"`
	Pins       map[string][]rustPin     `json:"pins"`
	Drafts     [][2]string              `json:"drafts"`
	Labels     []store.Label            `json:"labels"`
	Favorites  []string                 `json:"favorites"`
	CallLog    []string                 `json:"call_log"`
	MediaPaths [][3]string              `json:"media_paths"`
}

type rustChat struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	LastActivity int64    `json:"last_activity"`
	Unread       int      `json:"unread"`
	MarkedUnread bool     `json:"marked_unread"`
	Archived     bool     `json:"archived"`
	Pinned       bool     `json:"pinned"`
	PinnedAt     int64    `json:"pinned_at"`
	MutedUntil   *int64   `json:"muted_until"`
	Favorite     bool     `json:"favorite"`
	Participants []string `json:"participants"`
	ReadOnly     bool     `json:"read_only"`
	Left         bool     `json:"left"`
	InfoLocked   *bool    `json:"info_locked"`
	Admin        bool     `json:"admin"`
	Locked       bool     `json:"locked"`
	Ephemeral    *uint32  `json:"ephemeral"`
	Labels       []string `json:"labels"`
	HistoryStart bool     `json:"history_start"`
}

type rustMessage struct {
	ID        string `json:"id"`
	Sender    string `json:"sender"`
	FromMe    bool   `json:"from_me"`
	Timestamp int64  `json:"timestamp"`
	// HistoryOrder is the phone's order of a message history brought.
	HistoryOrder *int64 `json:"history_order"`
	Status       string `json:"status"`
	Edited       bool   `json:"edited"`
	Forwarded    bool   `json:"forwarded"`
	Quoted       *struct {
		ID string `json:"id"`
	} `json:"quoted"`
	Reactions []struct {
		Sender string `json:"sender"`
		Emoji  string `json:"emoji"`
	} `json:"reactions"`
	Content struct {
		Kind      string `json:"kind"`
		Text      string `json:"text"`
		Caption   string `json:"caption"`
		VoiceNote bool   `json:"voice_note"`
		Note      bool   `json:"note"`
		Media     *struct {
			MIME string `json:"mime"`
			Size uint64 `json:"size"`
			Path string `json:"path"`
		} `json:"media"`
	} `json:"content"`
}

type rustPin struct {
	ID        string `json:"id"`
	PinnedAt  int64  `json:"pinned_at"`
	ExpiresAt int64  `json:"expires_at"`
	ByMe      bool   `json:"by_me"`
	Sender    string `json:"sender"`
}

func loadReport(t *testing.T, name string) rustReport {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "oracle", name))
	if err != nil {
		t.Fatal(err)
	}
	var r rustReport
	if err = json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// oracleAccount is rustAccount with the oracle's archive, its media moved
// under the account's folder: those not named missing-* exist there.
func oracleAccount(t *testing.T, r rustReport) (dir string, keys *memKeys, media string) {
	t.Helper()
	dir, keys = rustAccount(t)
	data, err := os.ReadFile(filepath.Join("testdata", "oracle", "oracle-archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != oracleArchiveSHA256 {
		t.Fatal("testdata/oracle/oracle-archive.db is not the oracle's archive")
	}
	if err = os.WriteFile(filepath.Join(dir, RustArchiveFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	media = filepath.Join(filepath.Dir(dir), "media") + "/"
	if err := os.MkdirAll(media, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range r.MediaPaths {
		if name := strings.TrimPrefix(p[2], oracleMediaRoot); !strings.HasPrefix(name, "missing-") {
			if err := os.WriteFile(media+name, []byte("synthetic media"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	db, err := sqlite.Open(sqlite.Config{Path: filepath.Join(dir, RustArchiveFile), Key: testFixtures(t).archiveKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE messages SET content=replace(content,?,?)", oracleMediaRoot, media); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	return dir, keys, media
}

// migrated runs Account on dir, crashing once at crashAt when it is set,
// and opens the Go archive it left.
func migrated(t *testing.T, dir string, keys *memKeys, crashAt string) *store.Archive {
	t.Helper()
	opts := options(dir, keys)
	if crashAt != "" {
		opts.crash = func(p string) error {
			if p == crashAt {
				opts.crash = nil
				return errCrash
			}
			return nil
		}
		if _, err := Account(context.Background(), opts); !errors.Is(err, errCrash) {
			t.Fatalf("crash at %s: %v", crashAt, err)
		}
		// The marker is what a resumed run starts from.
		if _, err := os.Stat(filepath.Join(dir, MarkerFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if r, err := Account(context.Background(), opts); err != nil || r.State != StateActive {
		t.Fatalf("%+v: %v", r, err)
	}
	acct, err := store.OpenAccount(context.Background(), dir, keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { acct.Close() })
	return acct.Archive
}

func TestDifferentialFixtureArchive(t *testing.T) {
	dir, keys := rustAccount(t)
	compareWithRust(t, migrated(t, dir, keys, ""), loadReport(t, "fixture-archive.json"), "")
}

func TestDifferentialOracleArchive(t *testing.T) {
	r := loadReport(t, "oracle-archive.json")
	dir, keys, media := oracleAccount(t, r)
	compareWithRust(t, migrated(t, dir, keys, ""), r, media)
}

// A migration interrupted at any point and resumed (migration.json) gives
// the same archive as one that ran through.
func TestDifferentialResumed(t *testing.T) {
	r := loadReport(t, "oracle-archive.json")
	for _, point := range crashPoints {
		t.Run(point, func(t *testing.T) {
			dir, keys, media := oracleAccount(t, r)
			compareWithRust(t, migrated(t, dir, keys, point), r, media)
		})
	}
}

// compareWithRust checks the Go archive a shows what the Rust core reported.
// media is where the oracle's media were moved ("" when they were not).
func compareWithRust(t *testing.T, a *store.Archive, r rustReport, media string) {
	t.Helper()
	var diffs []string
	diff := func(format string, args ...any) { diffs = append(diffs, fmt.Sprintf(format, args...)) }
	defer func() {
		if len(diffs) > 0 {
			if len(diffs) > 40 {
				diffs = append(diffs[:40], fmt.Sprintf("... and %d more", len(diffs)-40))
			}
			t.Fatalf("the Go archive differs from the Rust core's:\n%s", strings.Join(diffs, "\n"))
		}
	}()
	movedPath := func(p string) string {
		if media != "" && strings.HasPrefix(p, oracleMediaRoot) {
			return media + strings.TrimPrefix(p, oracleMediaRoot)
		}
		return p
	}
	// A path survives when its file does; Go drops the others (MissingMedia).
	kept := func(p string) string {
		p = movedPath(p)
		if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() || !filepath.IsAbs(p) {
			return ""
		}
		return p
	}

	// The chat list: the same chats, in the same order but among chats of
	// one last activity, which Rust's ORDER BY left undefined.
	chats, err := a.Chats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != len(r.Chats) {
		diff("%d chats, Rust %d", len(chats), len(r.Chats))
	}
	for i := range min(len(chats), len(r.Chats)) {
		if chats[i].LastActivity != r.Chats[i].LastActivity {
			diff("chat list position %d: %s at %d, Rust %s at %d", i, chats[i].ID, chats[i].LastActivity, r.Chats[i].ID, r.Chats[i].LastActivity)
		}
	}
	favorites := map[string]bool{}
	for _, id := range r.Favorites {
		favorites[id] = true
	}
	for _, rc := range r.Chats {
		c, err := a.Chat(rc.ID)
		if err != nil {
			diff("chat %s: %v", rc.ID, err)
			continue
		}
		kind := map[string]string{"direct": "direct", "group": "group", "broadcast": "channel"}[rc.Kind]
		muted := int64(0)
		if rc.MutedUntil != nil {
			if muted = *rc.MutedUntil; muted == 0 {
				muted = math.MaxInt64
			}
		}
		// Unknown is the safe side for a group: only admins edit.
		infoLocked := rc.InfoLocked != nil && *rc.InfoLocked || rc.InfoLocked == nil && rc.Kind == "group"
		ephemeral := uint32(0)
		if rc.Ephemeral != nil {
			ephemeral = *rc.Ephemeral
		}
		type view struct {
			Name, Kind                                string
			LastActivity, PinnedAt, MutedUntil        int64
			Unread                                    int
			MarkedUnread, Archived, Pinned, Favorite  bool
			ReadOnly, Left, InfoLocked, Admin, Locked bool
			HistoryStart                              bool
			Ephemeral                                 uint32
			Participants, Labels                      string
		}
		want := view{rc.Name, kind, rc.LastActivity, rc.PinnedAt, muted, rc.Unread, rc.MarkedUnread, rc.Archived, rc.Pinned, rc.Favorite || favorites[rc.ID],
			rc.ReadOnly, rc.Left, infoLocked, rc.Admin, rc.Locked, rc.HistoryStart, ephemeral, strings.Join(rc.Participants, ","), strings.Join(rc.Labels, ",")}
		got := view{c.Name, c.Kind, c.LastActivity, c.PinnedAt, c.MutedUntil, c.Unread, c.MarkedUnread, c.Archived, c.Pinned, c.Favorite,
			c.ReadOnly, c.Left, c.InfoLocked, c.Admin, c.Locked, c.HistoryStart, c.EphemeralSeconds, strings.Join(c.Participants, ","), strings.Join(c.Labels, ",")}
		if got != want {
			diff("chat %s:\n  go   %+v\n  rust %+v", rc.ID, got, want)
		}

		// The messages, in the order the conversation shows them.
		var list []store.Message
		for before := ""; ; {
			page, older, err := a.Messages(rc.ID, before, 500)
			if err != nil {
				t.Fatal(err)
			}
			list = append(page, list...)
			if !older || len(page) == 0 {
				break
			}
			before = page[0].ID
		}
		rust := r.Messages[rc.ID]
		// Rust broke a tie of second and history order by rowid, Go by id
		// (store.Archive.Messages): within such a tie the order may differ,
		// so each run of them is compared sorted. Nothing else may move.
		goIDs := make([]string, len(list))
		for i, m := range list {
			goIDs[i] = m.ID
		}
		rustIDs := make([]string, len(rust))
		for i, rm := range rust {
			rustIDs[i] = rm.ID
		}
		for i := 0; i < len(rust); {
			j := i + 1
			for rust[i].HistoryOrder != nil && j < len(rust) && rust[j].Timestamp == rust[i].Timestamp && rust[j].HistoryOrder != nil && *rust[j].HistoryOrder == *rust[i].HistoryOrder {
				j++
			}
			slices.Sort(rustIDs[i:j])
			if j <= len(goIDs) {
				slices.Sort(goIDs[i:j])
			}
			i = j
		}
		if !slices.Equal(goIDs, rustIDs) {
			diff("chat %s message order:\n  go   %v\n  rust %v", rc.ID, goIDs, rustIDs)
		}
		byID := map[string]store.Message{}
		for _, m := range list {
			byID[m.ID] = m
		}
		for _, rm := range rust {
			m, ok := byID[rm.ID]
			if !ok {
				diff("%s/%s: missing", rc.ID, rm.ID)
				continue
			}
			compareMessage(rc.ID, m, rm, kept, diff)
		}

		stars, err := a.StarredIDs(rc.ID)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(stars)
		if want := r.Stars[rc.ID]; !slices.Equal(stars, want) && len(stars)+len(want) > 0 {
			diff("chat %s stars: go %v, rust %v", rc.ID, stars, want)
		}
		pins, err := a.ChatPins(rc.ID, r.Now)
		if err != nil {
			t.Fatal(err)
		}
		gotPins := make([]rustPin, len(pins))
		for i, p := range pins {
			gotPins[i] = rustPin{p.ID, p.PinnedAt, p.ExpiresAt, p.Pinner.ByMe, p.Pinner.Sender}
		}
		if want := r.Pins[rc.ID]; !slices.Equal(gotPins, want) && len(gotPins)+len(want) > 0 {
			diff("chat %s pins: go %+v, rust %+v", rc.ID, gotPins, want)
		}
		wantDraft := ""
		for _, d := range r.Drafts {
			if d[0] == rc.ID {
				wantDraft = d[1]
			}
		}
		if d, err := a.Draft(rc.ID); err != nil || d != wantDraft {
			diff("chat %s draft %q, rust %q: %v", rc.ID, d, wantDraft, err)
		}
	}

	labels, err := a.Labels()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(labels, r.Labels) {
		diff("labels: go %+v, rust %+v", labels, r.Labels)
	}
	calls, err := a.CallLog(1000)
	if err != nil {
		t.Fatal(err)
	}
	callIDs := make([]string, len(calls))
	for i, m := range calls {
		callIDs[i] = m.ID
	}
	if !slices.Equal(callIDs, r.CallLog) {
		diff("call log: go %v, rust %v", callIDs, r.CallLog)
	}
	// Every media reference whose file is there is kept, and no other.
	var wantPaths, gotPaths []string
	for _, p := range r.MediaPaths {
		if path := kept(p[2]); path != "" {
			wantPaths = append(wantPaths, p[0]+" "+p[1]+" "+path)
		}
	}
	for _, rc := range r.Chats {
		for _, rm := range r.Messages[rc.ID] {
			if m, err := a.Message(rc.ID, rm.ID); err == nil && m.Media != nil && m.Media.Path != "" {
				gotPaths = append(gotPaths, rc.ID+" "+rm.ID+" "+m.Media.Path)
			}
		}
	}
	slices.Sort(wantPaths)
	slices.Sort(gotPaths)
	if !slices.Equal(wantPaths, gotPaths) {
		diff("media paths:\n  go   %v\n  rust %v", gotPaths, wantPaths)
	}
}

// compareMessage checks one message's fields against Rust's.
func compareMessage(chat string, m store.Message, rm rustMessage, kept func(string) string, diff func(string, ...any)) {
	where := chat + "/" + rm.ID
	status := rm.Status
	if status == "none" {
		status = ""
	}
	if m.Sender != rm.Sender || m.FromMe != rm.FromMe || m.Timestamp != rm.Timestamp || m.Status != status || m.Edited != rm.Edited || m.Forwarded != rm.Forwarded {
		diff("%s: go sender %q me %v at %d %q edited %v forwarded %v; rust %q %v %d %q %v %v", where,
			m.Sender, m.FromMe, m.Timestamp, m.Status, m.Edited, m.Forwarded, rm.Sender, rm.FromMe, rm.Timestamp, status, rm.Edited, rm.Forwarded)
	}
	quote := ""
	if m.Quote != nil {
		quote = m.Quote.ID
	}
	if rm.Quoted != nil && quote != rm.Quoted.ID || rm.Quoted == nil && quote != "" {
		diff("%s: quote %q, rust %+v", where, quote, rm.Quoted)
	}
	var reactions, rustReactions []string
	for _, x := range m.Reactions {
		reactions = append(reactions, x.Sender+" "+x.Emoji)
	}
	for _, x := range rm.Reactions {
		rustReactions = append(rustReactions, x.Sender+" "+x.Emoji)
	}
	slices.Sort(reactions)
	slices.Sort(rustReactions)
	if !slices.Equal(reactions, rustReactions) {
		diff("%s: reactions %v, rust %v", where, reactions, rustReactions)
	}

	c := rm.Content
	var kind, text string
	switch c.Kind {
	case "text":
		kind, text = "text", c.Text
	case "image", "document", "sticker":
		kind, text = c.Kind, c.Caption
	case "video":
		kind, text = "video", c.Caption
		if c.Note {
			kind = "ptv"
		}
	case "audio":
		kind = "audio"
		if c.VoiceNote {
			kind = "voice"
		}
	case "call":
		kind = store.KindCall
	case "revoked":
		kind, text = "text", "This message was deleted"
	default:
		// The other kinds' mapping is TestImportArchiveRowForRow's.
		return
	}
	if m.Kind != kind || m.Text != text {
		diff("%s: %s %q, rust %s %q", where, m.Kind, m.Text, kind, text)
	}
	if c.Media == nil {
		if m.Media != nil {
			diff("%s: media %+v, rust none", where, m.Media)
		}
		return
	}
	if m.Media == nil {
		diff("%s: no media, rust %+v", where, c.Media)
		return
	}
	if m.Media.MIME != c.Media.MIME || m.Media.Size != c.Media.Size || m.Media.Path != kept(c.Media.Path) {
		diff("%s: media %q %d %q, rust %q %d %q", where, m.Media.MIME, m.Media.Size, m.Media.Path, c.Media.MIME, c.Media.Size, kept(c.Media.Path))
	}
}
