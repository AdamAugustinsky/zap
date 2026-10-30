package wa

import (
	"bytes"
	"context"
	"errors"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// The fake's answers for WP4b's calls: groups, contacts, the account,
// polls and sticker packs. Each is set by a test before it acts; the zero
// value answers as an empty account would.
type fakeMore struct {
	mu sync.Mutex

	groups                      map[types.JID]*types.GroupInfo
	groupErr, leaveErr, nameErr error
	photoErr                    error
	photos                      map[types.JID][]byte
	invite                      *types.GroupInfo
	inviteErr, joinErr          error
	joined                      types.JID
	joinPending                 bool
	contacts                    map[types.JID]types.ContactInfo
	named                       []string
	onWhatsApp                  []types.IsOnWhatsAppResponse
	onWhatsAppErr               error
	privacySettings             *types.PrivacySettings
	privacyErr, setPrivacyErr   error
	privacySet                  []types.PrivacySettingType
	about                       string
	aboutErr                    error
	proxyAddr                   string
	vote                        *waE2E.PollVoteMessage
	voteErr                     error
	packZip, packThumb          []byte
	packErr                     error
}

func newFakeMore() *fakeMore {
	return &fakeMore{groups: map[types.JID]*types.GroupInfo{}, contacts: map[types.JID]types.ContactInfo{}, photos: map[types.JID][]byte{}}
}

func (f *fakeClient) more() *fakeMore { return f.x }

func (f *fakeClient) GroupInfo(_ context.Context, g types.JID) (*types.GroupInfo, error) {
	f.call("GroupInfo")
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.groupErr != nil {
		return nil, m.groupErr
	}
	if info := m.groups[g]; info != nil {
		c := *info
		return &c, nil
	}
	return nil, errors.New("no such group")
}

func (f *fakeClient) LeaveGroup(context.Context, types.JID) error {
	f.call("LeaveGroup")
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.leaveErr
}

func (f *fakeClient) SetGroupName(_ context.Context, _ types.JID, name string) error {
	f.call("SetGroupName:" + name)
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nameErr
}

func (f *fakeClient) SetGroupPhoto(_ context.Context, j types.JID, jpeg []byte) (string, error) {
	f.call("SetGroupPhoto")
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.photoErr != nil {
		return "", m.photoErr
	}
	m.photos[j] = bytes.Clone(jpeg)
	return "pic-1", nil
}

func (f *fakeClient) GroupInfoFromLink(_ context.Context, code string) (*types.GroupInfo, error) {
	f.call("GroupInfoFromLink:" + code)
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.invite, m.inviteErr
}

func (f *fakeClient) JoinGroupWithLink(_ context.Context, code string) (types.JID, bool, error) {
	f.call("JoinGroupWithLink:" + code)
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.joined, m.joinPending, m.joinErr
}

func (f *fakeClient) Contacts(context.Context) (map[types.JID]types.ContactInfo, error) {
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[types.JID]types.ContactInfo{}
	for j, c := range m.contacts {
		out[j] = c
	}
	return out, nil
}

func (f *fakeClient) Contact(_ context.Context, j types.JID) (types.ContactInfo, error) {
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.contacts[j], nil
}

func (f *fakeClient) PutContactName(_ context.Context, j types.JID, full, first string) error {
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.contacts[j]
	c.Found, c.FullName, c.FirstName = true, full, first
	m.contacts[j] = c
	m.named = append(m.named, full)
	return nil
}

func (f *fakeClient) IsOnWhatsApp(_ context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	f.call("IsOnWhatsApp:" + phones[0])
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onWhatsApp, m.onWhatsAppErr
}

func (f *fakeClient) UserInfo(context.Context, []types.JID) (map[types.JID]types.UserInfo, error) {
	return map[types.JID]types.UserInfo{}, nil
}

func (f *fakeClient) PrivacySettings(context.Context) (*types.PrivacySettings, error) {
	f.call("PrivacySettings")
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.privacySettings, m.privacyErr
}

func (f *fakeClient) SetPrivacySetting(_ context.Context, name types.PrivacySettingType, _ types.PrivacySetting) (types.PrivacySettings, error) {
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.privacySet = append(m.privacySet, name)
	return types.PrivacySettings{}, m.setPrivacyErr
}

func (f *fakeClient) SetAbout(_ context.Context, about string) error {
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.aboutErr == nil {
		m.about = about
	}
	return m.aboutErr
}

func (f *fakeClient) SetProxy(addr string) error {
	f.call("SetProxy:" + addr)
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxyAddr = addr
	return nil
}

// BuildPollVote is a vote naming the poll and the options' names (the
// real one carries their encrypted hashes).
func (f *fakeClient) BuildPollVote(_ context.Context, poll *types.MessageInfo, options []string) (*waE2E.Message, error) {
	f.call("BuildPollVote")
	return &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{
		PollCreationMessageKey: &waCommon.MessageKey{ID: proto.String(poll.ID)},
		Vote:                   &waE2E.PollEncValue{EncPayload: []byte(stringsJoin(options))},
	}}, nil
}

func stringsJoin(list []string) string {
	var buf bytes.Buffer
	for i, s := range list {
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(s)
	}
	return buf.String()
}

func (f *fakeClient) DecryptPollVote(context.Context, *events.Message) (*waE2E.PollVoteMessage, error) {
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vote, m.voteErr
}

func (f *fakeClient) UploadStickerPack(_ context.Context, zip, thumb []byte) (whatsmeow.UploadResponse, whatsmeow.UploadResponse, error) {
	f.call("UploadStickerPack")
	m := f.more()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.packZip, m.packThumb = bytes.Clone(zip), bytes.Clone(thumb)
	pack := whatsmeow.UploadResponse{DirectPath: "/v/pack", MediaKey: []byte("pack-key"), FileLength: uint64(len(zip))}
	return pack, whatsmeow.UploadResponse{DirectPath: "/v/thumb"}, m.packErr
}
