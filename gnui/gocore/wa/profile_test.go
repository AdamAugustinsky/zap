package wa

import (
	"context"
	"errors"
	"os"
	"testing"

	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
)

// fakeChannels answers the channel metadata calls.
type fakeChannels struct {
	followed []*types.NewsletterMetadata
	listErr  error
	info     map[types.JID]*types.NewsletterMetadata
	fetched  []string
}

func (f *fakeClient) Channels(context.Context) ([]*types.NewsletterMetadata, error) {
	f.call("Channels")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.channels.followed, f.channels.listErr
}

func (f *fakeClient) Channel(_ context.Context, j types.JID) (*types.NewsletterMetadata, error) {
	f.call("Channel")
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.channels.info[j]; m != nil {
		return m, nil
	}
	return nil, errors.New("no such channel")
}

var channel = types.NewJID("120363000000000001", types.NewsletterServer)

func channelMeta(j types.JID, path string) *types.NewsletterMetadata {
	m := &types.NewsletterMetadata{ID: j}
	if path != "" {
		m.ThreadMeta.Picture = &types.ProfilePictureInfo{DirectPath: "/" + path}
		m.ThreadMeta.Preview = types.ProfilePictureInfo{DirectPath: "/" + path + "-preview"}
	}
	return m
}

func fetchAvatar(h *harness, id types.JID, full bool) {
	h.send(abi.Command{Tag: abi.CommandTagFetchAvatar, FetchAvatar: abi.CommandFetchAvatar{ID: chatID(id), Full: full}})
}

func TestPictureEventRefreshesAvatar(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	h.f.picture = &types.ProfilePictureInfo{URL: "https://pps.example/1"}
	h.f.pictureBytes = []byte("\xff\xd8\xff\xe0 first")
	fetchAvatar(h, peer, false)
	h.take()

	// A new picture: the cache is dropped and both sizes fetched again.
	h.f.pictureBytes = []byte("\xff\xd8\xff\xe0 second")
	asked := h.f.called("ProfilePicture")
	h.inject(&events.Picture{JID: peer, PictureID: "2"})
	evs := only(h.take(), abi.EventTagAvatar)
	if len(evs) != 2 || h.f.called("ProfilePicture") != asked+2 {
		t.Fatalf("refreshed: %d avatars, %d lookups", len(evs), h.f.called("ProfilePicture")-asked)
	}
	for _, ev := range evs {
		data, err := os.ReadFile(string(ev.Avatar.Path))
		if !ev.Avatar.HasPath || err != nil || string(data) != string(h.f.pictureBytes) {
			t.Fatalf("new picture: %+v %v", ev.Avatar, err)
		}
	}

	// Removed: no lookup, both sizes without a path, remembered as none.
	asked = h.f.called("ProfilePicture")
	h.inject(&events.Picture{JID: group, Remove: true})
	evs = only(h.take(), abi.EventTagAvatar)
	if len(evs) != 2 || evs[0].Avatar.HasPath || evs[1].Avatar.HasPath || string(evs[0].Avatar.ID) != group.String() {
		t.Fatalf("removed: %+v", evs)
	}
	fetchAvatar(h, group, true)
	if b := one(t, h.take(), abi.EventTagAvatar).Avatar; b.HasPath || h.f.called("ProfilePicture") != asked {
		t.Fatal("a removed picture was looked up")
	}
}

func TestChannelPictures(t *testing.T) {
	h := newHarness(t, options{})
	if err := h.c.Start(); err != nil {
		t.Fatal(err)
	}
	h.take()
	h.f.pictureBytes = []byte("\xff\xd8\xff\xe0 channel")
	h.f.channels.followed = []*types.NewsletterMetadata{channelMeta(channel, "first")}

	// Asked for before connecting: waits for the list, not the lookup.
	fetchAvatar(h, channel, false)
	expectTags(t, h.take(), "")
	h.f.setConnected(true)
	h.inject(&events.Connected{})
	a := one(t, h.take(), abi.EventTagAvatar).Avatar
	if !a.HasPath || string(a.ID) != channel.String() || h.f.called("ProfilePicture") != 0 {
		t.Fatalf("listed: %+v", a)
	}

	// Listed again unchanged: the cache stays.
	h.inject(&events.Connected{})
	expectTags(t, only(h.take(), abi.EventTagAvatar), "")
	if _, err := os.Stat(string(a.Path)); err != nil {
		t.Fatal("an unchanged picture lost its cache")
	}

	// Changed: fetched again in both sizes.
	fetches := h.f.called("Fetch")
	h.f.channels.followed = []*types.NewsletterMetadata{channelMeta(channel, "second")}
	h.inject(&events.Connected{})
	if evs := only(h.take(), abi.EventTagAvatar); len(evs) != 2 || !evs[0].Avatar.HasPath || h.f.called("Fetch") != fetches+2 {
		t.Fatalf("changed: %+v", evs)
	}

	// Removed: no path.
	h.f.channels.followed = []*types.NewsletterMetadata{channelMeta(channel, "")}
	h.inject(&events.Connected{})
	if evs := only(h.take(), abi.EventTagAvatar); len(evs) != 2 || evs[0].Avatar.HasPath || evs[1].Avatar.HasPath {
		t.Fatalf("removed: %+v", evs)
	}

	// Not followed: its own metadata.
	other := types.NewJID("120363000000000002", types.NewsletterServer)
	h.f.channels.info = map[types.JID]*types.NewsletterMetadata{other: channelMeta(other, "other")}
	fetchAvatar(h, other, true)
	if b := one(t, h.take(), abi.EventTagAvatar).Avatar; !b.HasPath || h.f.called("Channel") != 1 {
		t.Fatalf("unfollowed: %+v", b)
	}
}

func TestChannelPictureAddress(t *testing.T) {
	p := channelPictureOf(channelMeta(channel, "v/t61/fixture.jpg?oh=1"))
	if got := p.address(true); got != "https://mmg.whatsapp.net/v/t61/fixture.jpg?oh=1" {
		t.Fatalf("full: %s", got)
	}
	if got := p.address(false); got != "https://mmg.whatsapp.net/v/t61/fixture.jpg?oh=1-preview" {
		t.Fatalf("preview: %s", got)
	}
	if got := (channelPicture{full: "https://a.invalid/x"}).address(false); got != "https://a.invalid/x" {
		t.Fatalf("fallback: %s", got)
	}
	if got := pictureAddress(types.ProfilePictureInfo{URL: "https://b.invalid/y", DirectPath: "/z"}); got != "https://b.invalid/y" {
		t.Fatalf("url first: %s", got)
	}
}

func TestOwnNameAndAboutFollowOtherDevices(t *testing.T) {
	h := newHarness(t, options{})
	h.start()
	states := len(h.f.states)

	h.inject(&events.PushNameSetting{Action: &waSyncAction.PushNameSetting{Name: proto.String("Grace")}})
	me1 := one(t, h.take(), abi.EventTagMe).Me
	if string(me1.Name) != "Grace" || len(h.f.states) != states+1 {
		t.Fatalf("name: %+v, %d presences", me1, len(h.f.states)-states)
	}

	h.inject(&events.UserAbout{JID: peer, Status: "theirs"})
	expectTags(t, h.take(), "")
	h.inject(&events.UserAbout{JID: me, Status: "Busy"})
	if m := one(t, h.take(), abi.EventTagMe).Me; string(m.About) != "Busy" || string(m.Name) != "Grace" {
		t.Fatalf("about: %+v", m)
	}
}
