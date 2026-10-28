package wa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// Client is the part of a whatsmeow client the engine uses: whatsmeow's own
// methods, or thin wrappers of them, so the tests can put a fake in its
// place and inject whatsmeow's events. Every method is safe from any
// goroutine, as whatsmeow's are.
type Client interface {
	// OwnID is the account's phone-number and privacy (LID) ids, without a
	// device; both empty while unlinked.
	OwnID() (pn, lid types.JID)
	// PushName is the account's own display name, as the phone set it.
	PushName() string
	// PNForLID is the phone-number id of a privacy id, empty when unknown.
	PNForLID(ctx context.Context, lid types.JID) (types.JID, error)

	// AddEventHandler registers the one handler of whatsmeow's events.
	AddEventHandler(handler func(evt any))
	// QRChannel is whatsmeow's GetQRChannel: call it before Connect while
	// unlinked.
	QRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error)
	// Connect is ConnectContext: ctx bounds the automatic reconnections.
	Connect(ctx context.Context) error
	Disconnect()
	IsConnected() bool
	// ResetConnection drops the socket and reconnects at once.
	ResetConnection()
	Logout(ctx context.Context) error
	// Forget deletes the device's keys from the store, for a logout that
	// could not reach WhatsApp.
	Forget(ctx context.Context) error
	// PairPhone asks for the code that links by phone number.
	PairPhone(ctx context.Context, phone string) (string, error)

	GenerateMessageID() types.MessageID
	// SendMessage sends msg to chat with the given id (empty: a new one).
	SendMessage(ctx context.Context, chat types.JID, msg *waE2E.Message, id types.MessageID) (whatsmeow.SendResponse, error)
	// SendPeerMessage sends a message to the account's own phone.
	SendPeerMessage(ctx context.Context, msg *waE2E.Message) (whatsmeow.SendResponse, error)
	ParseWebMessage(chat types.JID, msg *waWeb.WebMessageInfo) (*events.Message, error)
	MarkRead(ctx context.Context, ids []types.MessageID, at time.Time, chat, sender types.JID, kind types.ReceiptType) error
	SendChatPresence(ctx context.Context, chat types.JID, state types.ChatPresence) error
	SendPresence(ctx context.Context, state types.Presence) error
	SubscribePresence(ctx context.Context, jid types.JID) error
	SendAppState(ctx context.Context, patch appstate.PatchInfo) error

	Upload(ctx context.Context, data []byte, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
	// UploadStickerPack uploads a sticker pack's zip and its thumbnail,
	// which share one media key.
	UploadStickerPack(ctx context.Context, zip, thumbnail []byte) (pack, thumb whatsmeow.UploadResponse, err error)
	Download(ctx context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error)
	// ProfilePicture is GetProfilePictureInfo: nil, nil when there is none.
	ProfilePicture(ctx context.Context, jid types.JID, preview bool) (*types.ProfilePictureInfo, error)
	// Fetch reads an https URL (a profile picture), at most limit bytes.
	Fetch(ctx context.Context, rawURL string, limit int64) ([]byte, error)

	// Groups.
	GroupInfo(ctx context.Context, group types.JID) (*types.GroupInfo, error)
	LeaveGroup(ctx context.Context, group types.JID) error
	SetGroupName(ctx context.Context, group types.JID, name string) error
	// SetGroupPhoto sets a group's picture, or with the account's own id
	// its profile picture; nil jpeg removes it. It returns the picture id.
	SetGroupPhoto(ctx context.Context, jid types.JID, jpeg []byte) (string, error)
	// GroupInfoFromLink previews the group an invite code opens.
	GroupInfoFromLink(ctx context.Context, code string) (*types.GroupInfo, error)
	// JoinGroupWithLink joins with an invite code; pending is set when the
	// group's admins must approve the request first.
	JoinGroupWithLink(ctx context.Context, code string) (group types.JID, pending bool, err error)

	// Contacts, from whatsmeow's contact store.
	Contacts(ctx context.Context) (map[types.JID]types.ContactInfo, error)
	Contact(ctx context.Context, user types.JID) (types.ContactInfo, error)
	PutContactName(ctx context.Context, user types.JID, fullName, firstName string) error
	IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error)
	UserInfo(ctx context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error)

	// The account.
	// PrivacySettings fetches the account's privacy settings from WhatsApp.
	PrivacySettings(ctx context.Context) (*types.PrivacySettings, error)
	SetPrivacySetting(ctx context.Context, name types.PrivacySettingType, value types.PrivacySetting) (types.PrivacySettings, error)
	// SetAbout sets the account's About text.
	SetAbout(ctx context.Context, about string) error
	// SetProxy routes the connection through a proxy URL (empty: none),
	// from the next connection on.
	SetProxy(addr string) error

	// Polls: a vote encrypted for the poll it answers, and decrypted.
	BuildPollVote(ctx context.Context, poll *types.MessageInfo, options []string) (*waE2E.Message, error)
	DecryptPollVote(ctx context.Context, vote *events.Message) (*waE2E.PollVoteMessage, error)
}

// NewClient returns whatsmeow's client over device, named name on the
// phone's list of linked devices (empty: "Zap").
func NewClient(device *wastore.Device, name string) Client {
	configureDevice(name)
	c := whatsmeow.NewClient(device, waLog.Noop)
	c.EmitAppStateEventsOnFullSync = true
	return &waClient{c: c, http: &http.Client{Timeout: 30 * time.Second}}
}

var deviceOnce sync.Once

// configureDevice sets what the phone shows for this computer and asks
// for on-demand history, once per process (whatsmeow keeps them global).
func configureDevice(name string) {
	deviceOnce.Do(func() {
		if name == "" {
			name = "Zap"
		}
		wastore.SetOSInfo(name, [3]uint32{0, 1, 0})
		platform := waCompanionReg.DeviceProps_DESKTOP
		if runtime.GOOS == "darwin" {
			platform = waCompanionReg.DeviceProps_CATALINA
		}
		wastore.DeviceProps.PlatformType = platform.Enum()
		wastore.DeviceProps.HistorySyncConfig.OnDemandReady = proto.Bool(true)
		wastore.DeviceProps.HistorySyncConfig.CompleteOnDemandReady = proto.Bool(true)
	})
}

type waClient struct {
	c    *whatsmeow.Client
	http *http.Client

	mu    sync.Mutex
	proxy string // SetProxy's, for the uploads whatsmeow does not make
}

func (w *waClient) OwnID() (types.JID, types.JID) {
	var pn types.JID
	if w.c.Store.ID != nil {
		pn = w.c.Store.ID.ToNonAD()
	}
	return pn, w.c.Store.GetLID().ToNonAD()
}

func (w *waClient) PushName() string { return w.c.Store.PushName }

func (w *waClient) PNForLID(ctx context.Context, lid types.JID) (types.JID, error) {
	if w.c.Store.LIDs == nil {
		return types.EmptyJID, nil
	}
	return w.c.Store.LIDs.GetPNForLID(ctx, lid)
}

func (w *waClient) AddEventHandler(h func(any)) { w.c.AddEventHandler(h) }

func (w *waClient) QRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return w.c.GetQRChannel(ctx)
}

func (w *waClient) Connect(ctx context.Context) error { return w.c.ConnectContext(ctx) }
func (w *waClient) Disconnect()                       { w.c.Disconnect() }
func (w *waClient) IsConnected() bool                 { return w.c.IsConnected() && w.c.IsLoggedIn() }
func (w *waClient) ResetConnection()                  { w.c.ResetConnection() }
func (w *waClient) Logout(ctx context.Context) error  { return w.c.Logout(ctx) }
func (w *waClient) Forget(ctx context.Context) error  { return w.c.Store.Delete(ctx) }

func (w *waClient) PairPhone(ctx context.Context, phone string) (string, error) {
	return w.c.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (macOS)")
}

func (w *waClient) GenerateMessageID() types.MessageID { return w.c.GenerateMessageID() }

func (w *waClient) SendMessage(ctx context.Context, chat types.JID, msg *waE2E.Message, id types.MessageID) (whatsmeow.SendResponse, error) {
	return w.c.SendMessage(ctx, chat, msg, whatsmeow.SendRequestExtra{ID: id})
}

func (w *waClient) SendPeerMessage(ctx context.Context, msg *waE2E.Message) (whatsmeow.SendResponse, error) {
	return w.c.SendPeerMessage(ctx, msg)
}

func (w *waClient) ParseWebMessage(chat types.JID, msg *waWeb.WebMessageInfo) (*events.Message, error) {
	return w.c.ParseWebMessage(chat, msg)
}

func (w *waClient) MarkRead(ctx context.Context, ids []types.MessageID, at time.Time, chat, sender types.JID, kind types.ReceiptType) error {
	if kind == types.ReceiptTypeRead {
		return w.c.MarkRead(ctx, ids, at, chat, sender)
	}
	return w.c.MarkRead(ctx, ids, at, chat, sender, kind)
}

func (w *waClient) SendChatPresence(ctx context.Context, chat types.JID, state types.ChatPresence) error {
	return w.c.SendChatPresence(ctx, chat, state, types.ChatPresenceMediaText)
}

func (w *waClient) SendPresence(ctx context.Context, state types.Presence) error {
	return w.c.SendPresence(ctx, state)
}

func (w *waClient) SubscribePresence(ctx context.Context, jid types.JID) error {
	return w.c.SubscribePresence(ctx, jid)
}

func (w *waClient) SendAppState(ctx context.Context, patch appstate.PatchInfo) error {
	return w.c.SendAppState(ctx, patch)
}

func (w *waClient) Upload(ctx context.Context, data []byte, kind whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	return w.c.Upload(ctx, data, kind)
}

func (w *waClient) Download(ctx context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error) {
	return w.c.Download(ctx, msg)
}

func (w *waClient) ProfilePicture(ctx context.Context, jid types.JID, preview bool) (*types.ProfilePictureInfo, error) {
	info, err := w.c.GetProfilePictureInfo(ctx, jid, &whatsmeow.GetProfilePictureParams{Preview: preview})
	if errors.Is(err, whatsmeow.ErrProfilePictureNotSet) || errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized) {
		return nil, nil
	}
	return info, err
}

func (w *waClient) GroupInfo(ctx context.Context, group types.JID) (*types.GroupInfo, error) {
	return w.c.GetGroupInfo(ctx, group)
}

func (w *waClient) LeaveGroup(ctx context.Context, group types.JID) error {
	return w.c.LeaveGroup(ctx, group)
}

func (w *waClient) SetGroupName(ctx context.Context, group types.JID, name string) error {
	return w.c.SetGroupName(ctx, group, name)
}

func (w *waClient) SetGroupPhoto(ctx context.Context, jid types.JID, jpeg []byte) (string, error) {
	return w.c.SetGroupPhoto(ctx, jid, jpeg)
}

func (w *waClient) GroupInfoFromLink(ctx context.Context, code string) (*types.GroupInfo, error) {
	return w.c.GetGroupInfoFromLink(ctx, code)
}

func (w *waClient) JoinGroupWithLink(ctx context.Context, code string) (types.JID, bool, error) {
	// whatsmeow answers a request waiting for approval with the group's id
	// too; the preview says whether the group asks for one.
	info, _ := w.c.GetGroupInfoFromLink(ctx, code)
	jid, err := w.c.JoinGroupWithLink(ctx, code)
	return jid, err == nil && info != nil && info.IsJoinApprovalRequired, err
}

func (w *waClient) Contacts(ctx context.Context) (map[types.JID]types.ContactInfo, error) {
	return w.c.Store.Contacts.GetAllContacts(ctx)
}

func (w *waClient) Contact(ctx context.Context, user types.JID) (types.ContactInfo, error) {
	return w.c.Store.Contacts.GetContact(ctx, user)
}

func (w *waClient) PutContactName(ctx context.Context, user types.JID, fullName, firstName string) error {
	return w.c.Store.Contacts.PutContactName(ctx, user, fullName, firstName)
}

func (w *waClient) IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	return w.c.IsOnWhatsApp(ctx, phones)
}

func (w *waClient) UserInfo(ctx context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error) {
	return w.c.GetUserInfo(ctx, jids)
}

func (w *waClient) PrivacySettings(ctx context.Context) (*types.PrivacySettings, error) {
	return w.c.TryFetchPrivacySettings(ctx, true)
}

func (w *waClient) SetPrivacySetting(ctx context.Context, name types.PrivacySettingType, value types.PrivacySetting) (types.PrivacySettings, error) {
	return w.c.SetPrivacySetting(ctx, name, value)
}

func (w *waClient) SetAbout(ctx context.Context, about string) error {
	return w.c.SetStatusMessage(ctx, types.SetStatusInput{Text: proto.String(about)})
}

func (w *waClient) SetProxy(addr string) error {
	w.mu.Lock()
	w.proxy = addr
	w.mu.Unlock()
	if addr == "" {
		w.c.SetProxy(nil)
		return nil
	}
	return w.c.SetProxyAddress(addr)
}

func (w *waClient) BuildPollVote(ctx context.Context, poll *types.MessageInfo, options []string) (*waE2E.Message, error) {
	return w.c.BuildPollVote(ctx, poll, options)
}

func (w *waClient) DecryptPollVote(ctx context.Context, vote *events.Message) (*waE2E.PollVoteMessage, error) {
	return w.c.DecryptPollVote(ctx, vote)
}

func (w *waClient) Fetch(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return nil, errors.New("not an https address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("the server answered " + resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("too large")
	}
	return data, nil
}
