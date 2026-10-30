// Live profile changes: pictures, the account's own name and About, and
// channel pictures, as upstream's worker follows them
// (zf:src/backend/worker.rs:3622, zf:src/backend/worker/channel_pictures.rs).

package wa

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// channelMediaHost serves a channel picture's direct path
// (upstream's DEFAULT_MEDIA_HOSTS[0]).
const channelMediaHost = "https://mmg.whatsapp.net"

// channelPicture is where a channel's picture is, in both sizes.
type channelPicture struct{ full, preview string }

// channelPictures are the followed channels' pictures, as last listed, for
// the client generation gen.
type channelPictures struct {
	gen    uint64
	listed bool // the followed channels were listed, or listing them failed
	known  map[string]channelPicture
}

// channelPictureOf reads a channel's picture from its metadata.
func channelPictureOf(m *types.NewsletterMetadata) channelPicture {
	var p channelPicture
	if m == nil {
		return p
	}
	if m.ThreadMeta.Picture != nil {
		p.full = pictureAddress(*m.ThreadMeta.Picture)
	}
	p.preview = pictureAddress(m.ThreadMeta.Preview)
	return p
}

// pictureAddress is where a channel picture is fetched from
// (zf:src/backend/worker/channel_pictures.rs:44).
func pictureAddress(info types.ProfilePictureInfo) string {
	switch {
	case strings.HasPrefix(info.URL, "https://"):
		return info.URL
	case strings.HasPrefix(info.DirectPath, "https://"):
		return info.DirectPath
	case info.DirectPath != "":
		return channelMediaHost + info.DirectPath
	}
	return ""
}

// address is one size's, falling back to the other
// (zf:src/backend/worker/channel_pictures.rs:33).
func (p channelPicture) address(full bool) string {
	wanted, other := p.preview, p.full
	if full {
		wanted, other = other, wanted
	}
	if wanted != "" {
		return wanted
	}
	return other
}

// channels is the current client's channel pictures: a new client (after a
// logout) starts with none.
func (e *Engine) channels() *channelPictures {
	if e.channelPics.gen != e.gen || e.channelPics.known == nil {
		e.channelPics = channelPictures{gen: e.gen, known: map[string]channelPicture{}}
	}
	return &e.channelPics
}

// profileConnected asks, on connecting, for what a profile event may have
// changed while offline: the followed channels' pictures and the
// account's About (zf:src/backend/worker.rs:3313).
func (e *Engine) profileConnected() {
	cli, gen := e.cli, e.gen
	pn, _ := cli.OwnID()
	e.job(func(ctx context.Context) func() {
		list, err := cli.Channels(ctx)
		if err != nil {
			slog.Debug("zapfast wa: followed channels not listed", "err", err)
		}
		var about *string
		if !pn.IsEmpty() {
			if info, err := cli.UserInfo(ctx, []types.JID{pn}); err != nil {
				slog.Debug("zapfast wa: own info not fetched", "err", err)
			} else if i, ok := info[pn]; ok {
				about = &i.Status
			}
		}
		return func() {
			if gen != e.gen {
				return
			}
			e.channelPicturesListed(list)
			if about != nil && *about != e.meAbout() {
				if err := e.arch.SetMetadata(meAboutKey, []byte(*about)); err == nil {
					e.emitMe()
				}
			}
		}
	})
}

// channelPicturesListed takes the followed channels' pictures, refreshing
// any that changed since the last list, and asks again for those that
// waited for the first list (zf:src/backend/worker/channel_pictures.rs:55).
func (e *Engine) channelPicturesListed(list []*types.NewsletterMetadata) {
	ch := e.channels()
	first := !ch.listed
	ch.listed = true
	for _, m := range list {
		if m == nil {
			continue
		}
		id, p := m.ID.String(), channelPictureOf(m)
		prev, had := ch.known[id]
		ch.known[id] = p
		if had && prev != p {
			e.refreshAvatar(id, p.address(true) == "")
		}
	}
	if !first {
		return // retryAvatars asked again on connecting
	}
	for k := range e.avatars {
		if isChannel(k.id) {
			e.fetchAvatar(k.id, k.full)
		}
	}
}

func isChannel(id string) bool { return strings.HasSuffix(id, "@"+types.NewsletterServer) }

// fetchChannelAvatar fetches a channel's picture from its metadata: the
// profile-picture lookup finds none. fetchAvatar found no fresh copy
// (zf:src/backend/worker/channel_pictures.rs:71).
func (e *Engine) fetchChannelAvatar(id string, full bool) {
	ch := e.channels()
	known, ok := ch.known[id]
	if !e.cli.IsConnected() || (!ok && !ch.listed) {
		// Wait for the connection and the list of followed channels.
		e.avatarLater(id, full)
		return
	}
	j, valid := jidOf(id)
	if !valid {
		e.avatar(id, full, "")
		return
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		var err error
		p := known
		if !ok {
			// Not followed: its own metadata.
			var m *types.NewsletterMetadata
			if m, err = cli.Channel(ctx, j); err == nil {
				p = channelPictureOf(m)
			}
		}
		var data []byte
		if address := p.address(full); err == nil && address != "" {
			data, err = fetchPicture(ctx, cli, address)
		}
		return func() { e.avatarFetched(id, full, data, err) }
	})
}

// refreshAvatar drops a chat's cached picture, in both sizes, and sends the
// new one, or none when it was removed (zf:src/backend/worker.rs:9054).
func (e *Engine) refreshAvatar(id string, removed bool) {
	if removed {
		e.pictureChanged(id, nil)
		return
	}
	for _, full := range []bool{false, true} {
		os.Remove(e.avatarFile(id, full))
		e.fetchAvatar(id, full)
	}
}

// pictureEvent is a contact's, a group's or the account's picture
// changing (zf:src/backend/worker.rs:3622).
func (e *Engine) pictureEvent(v *events.Picture) {
	e.refreshAvatar(e.canonical(v.JID), v.Remove)
}

// aboutEvent is a changed About: the account's is kept and shown; others'
// are read when their info is opened (zf:src/backend/worker.rs:3626).
func (e *Engine) aboutEvent(v *events.UserAbout) {
	if !e.isMe(e.canonical(v.JID)) {
		return
	}
	if err := e.arch.SetMetadata(meAboutKey, []byte(v.Status)); err != nil {
		slog.Warn("zapfast wa: keeping the About", "err", err)
		return
	}
	e.emitMe()
}

// pushNameSet is the account's name changed on another device: kept, shown,
// and announced with the presence, which carries it
// (zf:src/backend/worker.rs:3631).
func (e *Engine) pushNameSet(v *events.PushNameSetting) {
	if name := v.Action.GetName(); name != "" {
		if err := e.arch.SetMetadata(meNameKey, []byte(name)); err != nil {
			slog.Warn("zapfast wa: keeping the name", "err", err)
		}
	}
	e.emitMe()
	e.announcePresence()
}
