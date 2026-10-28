package settings

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// files are the paths the Files section shows, worked out once per account.
type files struct {
	acct                  string
	loc                   i18n.Locale
	state, archive, media string
	log                   string
	proxyEnv, proxyNote   string
	envDone               bool
}

// sync works the paths out for account a.
func (f *files) sync(a *state.Account) {
	if !f.envDone {
		f.envDone = true
		f.proxyEnv = proxyFromEnv()
	}
	if f.proxyEnv != "" && (f.proxyNote == "" || f.loc != i18n.Current()) {
		f.loc = i18n.Current()
		f.proxyNote = i18n.Format(i18n.T("Using {proxy} from the environment."), "{proxy}", f.proxyEnv)
	}
	if a == nil || (f.acct == a.ID && f.log != "") {
		return
	}
	f.acct = a.ID
	f.state, f.media, f.log, f.archive = "", "", "", ""
	data, cache, err := state.AccountDirs(a.ID)
	if err != nil {
		return
	}
	f.state = data
	f.archive = filepath.Join(data, "archive.db")
	f.media = filepath.Join(cache, "media")
	f.log = filepath.Join(filepath.Dir(filepath.Dir(data)), "zapfast.log")
}

// downloads is the folder downloads go to: the chosen one or the cache's.
func (f *files) downloads(e *screen.Env) string {
	if b := e.S.Settings.DownloadFolder; len(b) > 0 {
		return model.S(b)
	}
	return f.media
}

// proxyVars are the variables the proxy comes from when the setting is empty
// (zf:src/proxy.rs ENV_VARS).
var proxyVars = []string{"ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy"}

// firstEnv is the first of the variables that is set and not blank.
func firstEnv(names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// proxyFromEnv is the proxy the environment names, its password hidden
// (zf:src/proxy.rs from_env, redacted); empty when it names none that is valid.
func proxyFromEnv() string {
	v := firstEnv(proxyVars...)
	if checkProxy(strings.TrimSpace(v)) != "" || v == "" {
		return ""
	}
	if !strings.Contains(v, "://") {
		v = "http://" + strings.TrimSpace(v)
	}
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil {
		return ""
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			return u.Scheme + "://" + u.User.Username() + ":***@" + u.Host + u.EscapedPath()
		}
	}
	return u.String()
}
