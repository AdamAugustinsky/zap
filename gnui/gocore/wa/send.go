// Parts from zapfast-go 3104c46 (internal/backend/actions.go: sendWithID,
// attachQuote, attachMentions, forward, edit, revoke, react; media.go
// sendMedia), MIT. Changed for Zap: the sends are upstream's worker's: a
// send that cannot go out comes back as SendRefused, the row is stored
// pending and announced before the upload or the send, and an answer
// settles it as sent or failed.

package wa

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/av"
	"zapfast/gocore/store"
)

const (
	// forwardLimit is how many chats messages may be forwarded to at once
	// (upstream's FORWARD_LIMIT), and frequentlyForwarded the score from
	// which only one is allowed.
	forwardChats        = 5
	frequentlyForwarded = 5
	// fileMax is the largest file SendFiles reads.
	fileMax = 256 << 20
	// jpegQuality is a pasted picture's (upstream's encode_jpeg(…, 88)).
	jpegQuality = 88
	// readOnly is upstream's refusal of a chat this app cannot write to.
	readOnly = "This conversation is read-only in Zap"
)

// refuse hands a send that cannot go out back to the composer.
func (e *Engine) refuse(chat []byte, hasQuote bool, quote abi.ReplyTarget, reason abi.Refusal, unsent func(r *recordBlobs) abi.Unsent) {
	r := e.out.Record()
	ev := abi.Event{Tag: abi.EventTagSendRefused}
	ev.SendRefused = abi.EventSendRefused{Chat: chat, Quoting: quote, HasQuoting: hasQuote, Reason: reason}
	ev.SendRefused.Unsent = unsent(&recordBlobs{r})
	r.Emit(&ev)
}

// recordBlobs lets an Unsent name event blobs of its record.
type recordBlobs struct {
	r interface{ Blob([]byte) abi.BlobRef }
}

func (rb *recordBlobs) blob(p []byte) abi.BlobRef { return rb.r.Blob(p) }

// writable is upstream's destination_writable: a chat that is locked, read
// only, left, or a channel takes no messages.
func (e *Engine) writable(chat string) bool {
	c, err := e.arch.Chat(chat)
	switch {
	case err == nil:
		return c.CanSend()
	case noRows(err):
		return chatKind("", chat) != abi.ChatKindBroadcast
	}
	return false
}

// quote is the context of a reply to target (in chat, or in its own chat):
// false when the quoted message is not stored here.
func (e *Engine) quote(chat string, has bool, target abi.ReplyTarget) (*waE2E.ContextInfo, bool) {
	if !has {
		return nil, true
	}
	source := chat
	if target.HasChat {
		source = string(target.Chat)
	}
	m, err := e.arch.Message(source, string(target.ID))
	if err != nil || m.Deleted {
		return nil, false
	}
	sender := m.Sender
	if m.FromMe {
		sender = e.me()
	}
	ctx := &waE2E.ContextInfo{StanzaID: proto.String(m.ID), Participant: proto.String(sender)}
	if len(m.Raw) > 0 {
		var q waE2E.Message
		if proto.Unmarshal(m.Raw, &q) == nil {
			ctx.QuotedMessage = &q
		}
	}
	if source != chat {
		ctx.RemoteJID = proto.String(source)
	}
	return ctx, true
}

// mentioned are the user ids a command mentions, canonical and once each.
func (e *Engine) mentioned(ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids[:min(len(ids), mentionsMax)] {
		j, ok := jidOf(id)
		if !ok || j.Server != types.DefaultUserServer && j.Server != types.HiddenUserServer {
			continue
		}
		c := e.canonical(j)
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// withContext sets a message's context: the quote and the mentions.
func withContext(p *waE2E.Message, ctx *waE2E.ContextInfo, mentions []string) {
	if ctx == nil && len(mentions) == 0 {
		return
	}
	if ctx == nil {
		ctx = &waE2E.ContextInfo{}
	}
	if len(mentions) > 0 {
		ctx.MentionedJID = mentions
	}
	setContext(p, ctx)
}

// ephemeral gives a message the chat's disappearing timer.
func (e *Engine) ephemeral(chat string, p *waE2E.Message) {
	c, err := e.arch.Chat(chat)
	if err != nil || c.EphemeralSeconds == 0 {
		return
	}
	ctx := messageContext(p)
	if ctx == nil {
		ctx = &waE2E.ContextInfo{}
	}
	ctx.Expiration = proto.Uint32(c.EphemeralSeconds)
	setContext(p, ctx)
}

// outgoing stores a message of ours as pending, announces it and sends it.
// path is the local copy of its attachment, if any.
func (e *Engine) outgoing(chat string, j types.JID, p *waE2E.Message, attach func(id string) string) {
	e.ephemeral(chat, p)
	id := e.cli.GenerateMessageID()
	row := e.ownRow(chat, j, id, p)
	if attach != nil && row.Media != nil {
		row.Media.Path = attach(id)
	}
	if !e.store(row) {
		return
	}
	e.sendJob(chat, j, []prepared{{id, p}})
}

// prepared is one message ready to go: its id and protobuf.
type prepared struct {
	id string
	p  *waE2E.Message
}

// ownRow is the archive's row of a message we send.
func (e *Engine) ownRow(chat string, j types.JID, id string, p *waE2E.Message) store.Message {
	me, _ := types.ParseJID(e.me())
	evt := &events.Message{Message: p, Info: types.MessageInfo{
		MessageSource: types.MessageSource{Chat: j, Sender: me, IsFromMe: true, IsGroup: j.Server == types.GroupServer},
		ID:            id,
		Timestamp:     time.Now(),
	}}
	m := parseMessage(evt, chat, e.me())
	m.Status = "pending"
	return m
}

// store keeps a row of ours, with its chat's activity in the same
// transaction, and announces it; false when it could not.
func (e *Engine) store(m store.Message) bool {
	if err := e.arch.SaveWithChat(m.ChatID, []store.Message{m}, func(c *store.Chat, _ []store.Message) {
		if c.Kind == "" {
			c.Kind = archiveKind(m.ChatID)
		}
		if m.Timestamp >= c.LastActivity {
			c.LastActivity, c.Preview = m.Timestamp, summary(m)
		}
		if !e.keepArchived {
			c.Archived = false
		}
	}); err != nil {
		// A COMMIT that reported an fsync error may have held: the UI
		// gets the chat as the archive has it (updateChat does the same).
		e.emitChat(m.ChatID)
		e.emitError("The message could not be saved: " + err.Error())
		return false
	}
	if c, err := e.arch.Chat(m.ChatID); err == nil && c.Kind == "group" {
		var audience []string
		for _, p := range c.Participants {
			if !e.isMe(p) {
				audience = append(audience, p)
			}
		}
		if err := e.arch.SetAudience(m.ChatID, m.ID, audience); err != nil {
			slog.Warn("zapfast wa: storing a group audience", "err", err)
		}
	}
	stored, err := e.arch.Message(m.ChatID, m.ID)
	if err != nil {
		return false
	}
	e.live(stored, false)
	return true
}

// sendJob sends messages to one chat, in order, each settled as it goes.
func (e *Engine) sendJob(chat string, j types.JID, list []prepared) {
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		for _, m := range list {
			_, err := cli.SendMessage(ctx, j, m.p, m.id)
			id := m.id
			e.post(func() {
				if gen == e.gen {
					e.sent(chat, id, err)
				}
			})
		}
		return nil
	})
}

// sent settles a message of ours.
func (e *Engine) sent(chat, id string, err error) {
	status := "sent"
	if err != nil {
		status = "failed"
	}
	if uerr := e.arch.UpdateMessage(chat, id, func(m *store.Message) {
		if status == "failed" || m.Status == "pending" || m.Status == "failed" {
			m.Status = status
		}
	}); uerr != nil {
		return
	}
	e.emitMessage(chat, id)
	e.emitChat(chat)
	if err != nil {
		e.emitError("Message not sent: " + err.Error())
	}
}

// ready checks a send: its chat may be written, its quote is here, and the
// link is up. A refusal goes back to the composer with unsent.
func (e *Engine) ready(chat []byte, hasQuote bool, quote abi.ReplyTarget, unsent func(r *recordBlobs) abi.Unsent) (types.JID, *waE2E.ContextInfo, bool) {
	id := string(chat)
	if !e.writable(id) {
		e.emitError(readOnly)
		return types.EmptyJID, nil, false
	}
	ctx, ok := e.quote(id, hasQuote, quote)
	if !ok {
		e.refuse(chat, hasQuote, quote, abi.RefusalQuoteUnavailable, unsent)
		return types.EmptyJID, nil, false
	}
	j, valid := jidOf(id)
	if !valid || !e.cli.IsConnected() {
		e.refuse(chat, hasQuote, quote, abi.RefusalOffline, unsent)
		return types.EmptyJID, nil, false
	}
	return j, ctx, true
}

func (e *Engine) sendText(c *abi.CommandSendText) {
	text := string(c.Text)
	if strings.TrimSpace(text) == "" {
		return
	}
	j, ctx, ok := e.ready(c.Chat, c.HasQuoting, c.Quoting, func(*recordBlobs) abi.Unsent {
		var u abi.Unsent
		u.Tag = abi.UnsentTagText
		u.Text.Text = c.Text
		return u
	})
	if !ok {
		return
	}
	p := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(text)}}
	withContext(p, ctx, e.mentioned(strs(c.Mentions)))
	e.outgoing(string(c.Chat), j, p, nil)
}

func (e *Engine) sendLocation(c *abi.CommandSendLocation) {
	j, ctx, ok := e.ready(c.Chat, c.HasQuoting, c.Quoting, func(*recordBlobs) abi.Unsent {
		var u abi.Unsent
		u.Tag = abi.UnsentTagLocation
		u.Location = abi.UnsentLocation{Latitude: c.Latitude, Longitude: c.Longitude}
		return u
	})
	if !ok {
		return
	}
	p := &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(c.Latitude), DegreesLongitude: proto.Float64(c.Longitude)}}
	withContext(p, ctx, nil)
	e.outgoing(string(c.Chat), j, p, nil)
}

// upload is an attachment uploaded and described, for media messages.
type upload struct {
	kind    whatsmeow.MediaType
	mime    string
	name    string
	data    []byte
	resp    whatsmeow.UploadResponse
	width   uint32
	height  uint32
	thumb   []byte
	seconds uint32
	wave    []byte
	ptt     bool
	// sticker sends an image as a sticker (animated: an animated one), gif
	// a video as a looping GIF.
	sticker  bool
	animated bool
	gif      bool
}

// message is the media message of an upload, with its caption.
func (u *upload) message(caption string) *waE2E.Message {
	r := u.resp
	var capt *string
	if caption != "" {
		capt = proto.String(caption)
	}
	switch {
	case u.sticker:
		return &waE2E.Message{StickerMessage: &waE2E.StickerMessage{URL: proto.String(r.URL), DirectPath: proto.String(r.DirectPath), MediaKey: r.MediaKey, FileEncSHA256: r.FileEncSHA256, FileSHA256: r.FileSHA256, FileLength: proto.Uint64(r.FileLength), Mimetype: proto.String(u.mime), Width: nonzero(u.width), Height: nonzero(u.height), IsAnimated: proto.Bool(u.animated)}}
	case u.gif:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{URL: proto.String(r.URL), DirectPath: proto.String(r.DirectPath), MediaKey: r.MediaKey, FileEncSHA256: r.FileEncSHA256, FileSHA256: r.FileSHA256, FileLength: proto.Uint64(r.FileLength), Mimetype: proto.String(u.mime), Caption: capt, Width: nonzero(u.width), Height: nonzero(u.height), Seconds: nonzero(u.seconds), JPEGThumbnail: u.thumb, GifPlayback: proto.Bool(true)}}
	}
	switch u.kind {
	case whatsmeow.MediaImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{URL: proto.String(r.URL), DirectPath: proto.String(r.DirectPath), MediaKey: r.MediaKey, FileEncSHA256: r.FileEncSHA256, FileSHA256: r.FileSHA256, FileLength: proto.Uint64(r.FileLength), Mimetype: proto.String(u.mime), Caption: capt, Width: nonzero(u.width), Height: nonzero(u.height), JPEGThumbnail: u.thumb}}
	case whatsmeow.MediaVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{URL: proto.String(r.URL), DirectPath: proto.String(r.DirectPath), MediaKey: r.MediaKey, FileEncSHA256: r.FileEncSHA256, FileSHA256: r.FileSHA256, FileLength: proto.Uint64(r.FileLength), Mimetype: proto.String(u.mime), Caption: capt, Width: nonzero(u.width), Height: nonzero(u.height), Seconds: nonzero(u.seconds), JPEGThumbnail: u.thumb}}
	case whatsmeow.MediaAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{URL: proto.String(r.URL), DirectPath: proto.String(r.DirectPath), MediaKey: r.MediaKey, FileEncSHA256: r.FileEncSHA256, FileSHA256: r.FileSHA256, FileLength: proto.Uint64(r.FileLength), Mimetype: proto.String(u.mime), PTT: proto.Bool(u.ptt), Seconds: nonzero(u.seconds), Waveform: u.wave}}
	}
	return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{URL: proto.String(r.URL), DirectPath: proto.String(r.DirectPath), MediaKey: r.MediaKey, FileEncSHA256: r.FileEncSHA256, FileSHA256: r.FileSHA256, FileLength: proto.Uint64(r.FileLength), Mimetype: proto.String(u.mime), FileName: proto.String(u.name), Caption: capt, JPEGThumbnail: u.thumb}}
}

func nonzero(n uint32) *uint32 {
	if n == 0 {
		return nil
	}
	return proto.Uint32(n)
}

// sendUpload uploads on a job, then stores and sends the message: the
// caption, mentions and quote given, and the attachment kept in the cache.
func (e *Engine) sendUpload(chat string, j types.JID, prepare func(ctx context.Context) (*upload, error), caption string, ctx *waE2E.ContextInfo, mentions []string, what string) {
	cli, gen := e.cli, e.gen
	e.job(func(c context.Context) func() {
		u, err := prepare(c)
		if err == nil {
			u.resp, err = cli.Upload(c, u.data, u.kind)
		}
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				e.emitError("Could not send the " + what + ": " + err.Error())
				return
			}
			p := u.message(caption)
			withContext(p, ctx, mentions)
			e.outgoing(chat, j, p, func(id string) string {
				path, err := e.keepMedia(chat, id, u.mime, u.data)
				if err != nil {
					slog.Warn("zapfast wa: keeping a sent attachment", "err", err)
				}
				return path
			})
		}
	})
}

func (e *Engine) sendImage(c *abi.CommandSendImage, rgba []byte) {
	w, h := c.Width, c.Height
	j, ctx, ok := e.ready(c.Chat, c.HasQuoting, c.Quoting, func(r *recordBlobs) abi.Unsent {
		var u abi.Unsent
		u.Tag = abi.UnsentTagImage
		u.Image = abi.UnsentImage{Width: w, Height: h, RGBA: r.blob(rgba), Caption: c.Caption, HasCaption: c.HasCaption}
		return u
	})
	if !ok {
		return
	}
	mentions := e.mentioned(strs(c.Mentions))
	e.sendUpload(string(c.Chat), j, func(context.Context) (*upload, error) {
		data, err := encodeRGBA(rgba, int(w), int(h), jpegQuality)
		if err != nil {
			return nil, err
		}
		return &upload{kind: whatsmeow.MediaImage, mime: "image/jpeg", data: data, width: w, height: h, thumb: thumbnail(data)}, nil
	}, string(c.Caption), ctx, mentions, "picture")
}

func (e *Engine) sendFiles(c *abi.CommandSendFiles) {
	j, ctx, ok := e.ready(c.Chat, c.HasQuoting, c.Quoting, func(*recordBlobs) abi.Unsent {
		var u abi.Unsent
		u.Tag = abi.UnsentTagFiles
		u.Files = abi.UnsentFiles{Paths: c.Paths, Documents: c.Documents, Caption: c.Caption, HasCaption: c.HasCaption}
		return u
	})
	if !ok {
		return
	}
	mentions := e.mentioned(strs(c.Mentions))
	for i, raw := range c.Paths {
		path := string(raw)
		// The caption, the mentions and the reply belong to the first file.
		caption, quote, names := "", ctx, mentions
		if i == 0 {
			caption = string(c.Caption)
		} else {
			quote, names = nil, nil
		}
		documents := c.Documents
		e.sendUpload(string(c.Chat), j, func(context.Context) (*upload, error) { return readFile(path, documents) }, caption, quote, names, "file")
	}
}

// readFile reads an attachment and decides what it is sent as: a picture,
// a video, an audio file, or (always for documents) a document.
func readFile(path string, documents bool) (*upload, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a file", filepath.Base(path))
	}
	if info.Size() > fileMax {
		return nil, fmt.Errorf("%s is larger than %d MiB", filepath.Base(path), fileMax>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	kind := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if kind == "" {
		kind = http.DetectContentType(data)
	}
	if base, _, err := mime.ParseMediaType(kind); err == nil {
		kind = base
	}
	u := &upload{kind: whatsmeow.MediaDocument, mime: kind, name: filepath.Base(path), data: data}
	switch {
	case documents:
	case strings.HasPrefix(kind, "image/") && kind != "image/svg+xml":
		if w, h, ok := pictureSize(data); ok {
			u.kind, u.width, u.height, u.thumb = whatsmeow.MediaImage, w, h, thumbnail(data)
		}
	case strings.HasPrefix(kind, "video/"):
		// The picture, size and length phones show before downloading it
		// (zf:src/backend/worker.rs:11427).
		u.kind = whatsmeow.MediaVideo
		u.width, u.height, u.seconds, u.thumb = preview(path, data)
	case strings.HasPrefix(kind, "audio/"):
		u.kind = whatsmeow.MediaAudio
	}
	return u, nil
}

func (e *Engine) sendVoice(c *abi.CommandSendVoice, pcm []byte) {
	j, ctx, ok := e.ready(c.Chat, c.HasQuoting, c.Quoting, func(r *recordBlobs) abi.Unsent {
		var u abi.Unsent
		u.Tag = abi.UnsentTagVoice
		u.Voice.Samples = r.blob(pcm)
		return u
	})
	if !ok {
		return
	}
	e.sendUpload(string(c.Chat), j, func(context.Context) (*upload, error) {
		samples, err := av.SamplesFromPCM(pcm)
		if err != nil {
			return nil, err
		}
		v, err := av.PrepareVoice(samples)
		if err != nil {
			return nil, err
		}
		return &upload{kind: whatsmeow.MediaAudio, mime: "audio/ogg; codecs=opus", data: v.Ogg, seconds: v.Seconds, wave: v.Waveform[:], ptt: true}, nil
	}, "", ctx, nil, "voice message")
}

// forwardLimit is how many chats these messages may go to at once: one
// when any was forwarded many times.
func (e *Engine) forwardLimit(chat string, ids []string) int {
	for _, id := range ids {
		m, err := e.arch.Message(chat, id)
		if err != nil || len(m.Raw) == 0 {
			continue
		}
		var p waE2E.Message
		if proto.Unmarshal(m.Raw, &p) == nil {
			if ctx := messageContext(&p); ctx.GetForwardingScore() >= frequentlyForwarded {
				return 1
			}
		}
	}
	return forwardChats
}

// forward sends several messages to several chats, in order, or refuses
// the whole batch when it names more chats than WhatsApp allows.
func (e *Engine) forward(c *abi.CommandForward) {
	from, ids := string(c.FromChat), strs(c.Messages)
	if limit := e.forwardLimit(from, ids); len(c.ToChats) > limit {
		if limit == 1 {
			e.emitError("Messages forwarded many times can be forwarded to only one chat")
		} else {
			e.emitError(fmt.Sprintf("Messages can be forwarded to up to %d chats at once", limit))
		}
		return
	}
	for _, to := range c.ToChats {
		if !e.writable(string(to)) {
			e.emitError(readOnly)
			return
		}
	}
	if !e.cli.IsConnected() {
		e.emitError("Not connected to WhatsApp")
		return
	}
	for _, raw := range c.ToChats {
		to := string(raw)
		j, ok := jidOf(to)
		if !ok {
			continue
		}
		var list []prepared
		for _, id := range ids {
			p, ok := e.forwarded(from, id)
			if !ok {
				continue
			}
			e.ephemeral(to, p)
			mid := e.cli.GenerateMessageID()
			row := e.ownRow(to, j, mid, p)
			if src, err := e.arch.Message(from, id); err == nil && src.Media != nil && row.Media != nil {
				row.Media.Path = src.Media.Path
			}
			if e.store(row) {
				list = append(list, prepared{mid, p})
			}
		}
		if len(list) > 0 {
			e.sendJob(to, j, list)
		}
	}
}

// forwarded is a stored message as a forward: its quote dropped, marked
// forwarded once more. False (and an Error) when it cannot be forwarded.
func (e *Engine) forwarded(chat, id string) (*waE2E.Message, bool) {
	m, err := e.arch.Message(chat, id)
	if err != nil {
		e.emitError("This message is not stored on this computer")
		return nil, false
	}
	switch {
	case m.Deleted, m.Kind == kindUnsupported, m.Kind == kindWaiting, m.Kind == kindPhoneOnly, m.Kind == kindViewOnce, m.Kind == kindPoll, m.Kind == kindInteractive,
		m.Kind == kindLiveLocation && (m.LiveLocation == nil || !m.LiveLocation.Available):
		e.emitError("This message cannot be forwarded")
		return nil, false
	}
	var p waE2E.Message
	if len(m.Raw) == 0 || proto.Unmarshal(m.Raw, &p) != nil {
		e.emitError("The original message data is not available to forward")
		return nil, false
	}
	q := unwrap(&p)
	if q.Conversation != nil {
		q = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: q.Conversation}}
	}
	ctx := messageContext(q)
	if ctx == nil {
		ctx = &waE2E.ContextInfo{}
	} else {
		ctx = proto.Clone(ctx).(*waE2E.ContextInfo)
	}
	ctx.StanzaID, ctx.Participant, ctx.QuotedMessage, ctx.RemoteJID, ctx.Expiration = nil, nil, nil, nil, nil
	ctx.IsForwarded = proto.Bool(true)
	ctx.ForwardingScore = proto.Uint32(ctx.GetForwardingScore() + 1)
	setContext(q, ctx)
	return q, true
}

// editText edits a text of ours: the archive takes the new text once
// WhatsApp accepts it, and a refusal puts the original back.
func (e *Engine) editText(chat, id, text string, mentions []string) {
	m, err := e.arch.Message(chat, id)
	if err != nil || !m.FromMe || m.Deleted || m.Kind != kindText {
		e.emitError("This message cannot be edited")
		e.emitMessage(chat, id)
		return
	}
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		e.emitError("Not connected to WhatsApp")
		e.emitMessage(chat, id)
		return
	}
	names := e.mentioned(mentions)
	content := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(text)}}
	withContext(content, nil, names)
	edit := buildEdit(j, id, content)
	raw, _ := proto.Marshal(content)
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		_, err := cli.SendMessage(ctx, j, edit, "")
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				e.emitError("Could not send the edit: " + err.Error())
				e.emitMessage(chat, id)
				return
			}
			if err := e.arch.UpdateMessage(chat, id, func(m *store.Message) {
				if m.Deleted {
					return
				}
				m.Text, m.Edited, m.LinkPreview, m.Raw = text, true, nil, raw
				m.Mentions = nil
				for _, n := range names {
					m.Mentions = append(m.Mentions, mentionOf(n))
				}
			}); err != nil {
				return
			}
			e.emitMessage(chat, id)
			e.emitChat(chat)
		}
	})
}

// buildEdit is whatsmeow's BuildEdit.
func buildEdit(chat types.JID, id string, content *waE2E.Message) *waE2E.Message {
	return &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Key:           &waCommon.MessageKey{FromMe: proto.Bool(true), ID: proto.String(id), RemoteJID: proto.String(chat.String())},
		Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		EditedMessage: content,
		TimestampMS:   proto.Int64(time.Now().UnixMilli()),
	}}}}
}

// revoke deletes a message for everyone: ours, or anyone's in a group we
// administer. The row shows revoked at once; a refusal is reported.
func (e *Engine) revoke(chat, id string) {
	m, err := e.arch.Message(chat, id)
	if err != nil {
		return
	}
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		e.emitError("Not connected to WhatsApp")
		return
	}
	if !m.FromMe {
		if c, err := e.arch.Chat(chat); err != nil || !c.Admin || c.Kind != "group" {
			e.emitError("Only a group's admins can delete others' messages for everyone")
			return
		}
	}
	msg := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: e.messageKey(j, m)}}
	if err := e.arch.UpdateMessage(chat, id, revoked); err != nil {
		return
	}
	e.emitMessage(chat, id)
	e.emitChat(chat)
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		if _, err := cli.SendMessage(ctx, j, msg, ""); err != nil {
			return func() { e.emitError("Could not delete the message for everyone: " + err.Error()) }
		}
		return nil
	})
}

// deleteLocal deletes a message for this account's devices: the phone
// first, then here.
func (e *Engine) deleteLocal(chat, id string) {
	m, err := e.arch.Message(chat, id)
	if err != nil {
		return
	}
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		e.emitError("Connect to WhatsApp to delete this message")
		return
	}
	fromMe, sender := "0", "0"
	if m.FromMe {
		fromMe = "1"
	} else if j.Server == types.GroupServer {
		sender = m.Sender
	}
	patch := appstate.PatchInfo{Type: appstate.WAPatchRegularHigh, Mutations: []appstate.MutationInfo{{
		Index:   []string{appstate.IndexDeleteMessageForMe, j.String(), id, fromMe, sender},
		Version: 3,
		Value:   &waSyncAction.SyncActionValue{DeleteMessageForMeAction: &waSyncAction.DeleteMessageForMeAction{DeleteMedia: proto.Bool(false), MessageTimestamp: proto.Int64(m.Timestamp)}},
	}}}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		err := cli.SendAppState(ctx, patch)
		return func() {
			if err != nil {
				e.emitError("Could not confirm the deletion: " + err.Error())
				return
			}
			if err := e.arch.DeleteLocal(chat, id); err != nil {
				e.emitError("The message could not be deleted here: " + err.Error())
				return
			}
			e.messageDeleted(chat, id)
		}
	})
}

// react sets our reaction (empty: takes it off).
func (e *Engine) react(chat, id, emoji string) {
	m, err := e.arch.Message(chat, id)
	if err != nil {
		return
	}
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		e.emitError("Not connected to WhatsApp")
		return
	}
	at := time.Now().UnixMilli()
	msg := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: e.messageKey(j, m), Text: proto.String(emoji), SenderTimestampMS: proto.Int64(at)}}
	if changed, err := e.arch.ApplyReaction(chat, id, store.Reaction{Sender: e.me(), Emoji: emoji, Timestamp: at / 1000}, at); err == nil && changed {
		e.emitMessage(chat, id)
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		if _, err := cli.SendMessage(ctx, j, msg, ""); err != nil {
			return func() { e.emitError("Could not react: " + err.Error()) }
		}
		return nil
	})
}
