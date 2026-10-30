// Parts from zapfast-go 3104c46 (internal/backend/profile.go: groupInfo,
// group invites), MIT. Changed for Zap: upstream's worker's group edits
// (GroupSaving around a change, the name stored once WhatsApp accepts it)
// and invites (InvitePreview, InviteJoined).

package wa

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"golang.org/x/image/draw"

	"zapfast/abi"
	"zapfast/gocore/av"
	"zapfast/gocore/out"
	"zapfast/gocore/store"
)

const (
	// groupEditRefused is what a refused change to a group's info says
	// (upstream's GROUP_EDIT_REFUSED).
	groupEditRefused = "Only admins can change this group's name and photo."
	// groupNameLimit is the longest group name WhatsApp accepts, in
	// characters.
	groupNameLimit = 100
	// pictureSide is a profile or group picture's largest side (upstream's
	// PROFILE_PICTURE_SIDE).
	pictureSide = 640
	// pictureMax is the largest picture file read for one.
	pictureMax = 32 << 20
)

// pictureFilter is the Images filter of upstream's picture dialogs.
var pictureFilter = []abi.PickFilter{{Name: b("Images"), Extensions: [][]byte{b("jpg"), b("jpeg"), b("png"), b("webp"), b("gif")}}}

// canEditInfo is upstream's Chat::can_edit_info.
func canEditInfo(c store.Chat) bool {
	return chatKind(c.Kind, c.ID) == abi.ChatKindGroup && !c.Left && (c.Admin || !c.InfoLocked)
}

// mayEditGroup reports whether a group's info may be changed, and says so
// when it may not.
func (e *Engine) mayEditGroup(chat string) bool {
	c, err := e.arch.Chat(chat)
	if err != nil || !canEditInfo(c) {
		e.emitError(groupEditRefused)
		return false
	}
	return true
}

func (e *Engine) groupSaving(chat string, saving bool) {
	ev := abi.Event{Tag: abi.EventTagGroupSaving}
	ev.GroupSaving = abi.EventGroupSaving{Chat: b(chat), Saving: saving}
	e.out.Emit(&ev)
}

// refusedEdit reports whether WhatsApp refused a group change because we
// may not make it (upstream's group_edit_refused).
func refusedEdit(err error) bool {
	if errors.Is(err, errForbidden) {
		return true
	}
	s := err.Error()
	for _, w := range []string{"forbidden", "not-authorized", "401", "403"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// errForbidden stands for whatsmeow's refusals in the tests.
var errForbidden = errors.New("forbidden")

// setGroupName renames a group on WhatsApp. The name changes here once
// WhatsApp accepts it: a refused rename would flash everywhere.
func (e *Engine) setGroupName(chat, name string) {
	name = strings.TrimSpace(name)
	c, err := e.arch.Chat(chat)
	if err != nil || name == "" || name == c.Name {
		return
	}
	if !e.mayEditGroup(chat) {
		return
	}
	if utf8.RuneCountInString(name) > groupNameLimit {
		e.emitError("A group name can have at most 100 characters.")
		return
	}
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		e.emitError("Connect to WhatsApp to change the group's name.")
		return
	}
	e.groupSaving(chat, true)
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		err := cli.SetGroupName(ctx, j, name)
		return func() {
			e.groupSaving(chat, false)
			if gen != e.gen {
				return
			}
			if err != nil {
				e.groupEditFailed(chat, err, "Could not rename the group.")
				return
			}
			if err := e.updateChat(chat, func(c *store.Chat) { c.Name = name }); err != nil {
				slog.Warn("zapfast wa: renaming a group", "err", err)
			}
		}
	})
}

func (e *Engine) groupEditFailed(chat string, err error, msg string) {
	if refusedEdit(err) {
		e.emitError(groupEditRefused)
		// Our rights changed without our knowing: learn them.
		e.requestGroupInfo(chat, true)
		return
	}
	e.emitError(msg)
}

// setGroupPicture sets a group's photo to a JPEG, or removes it (nil).
func (e *Engine) setGroupPicture(chat string, jpeg []byte) {
	if !e.mayEditGroup(chat) {
		return
	}
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		e.emitError("Connect to WhatsApp to change the group's photo.")
		return
	}
	e.groupSaving(chat, true)
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		_, err := cli.SetGroupPhoto(ctx, j, jpeg)
		return func() {
			e.groupSaving(chat, false)
			if gen != e.gen {
				return
			}
			if err != nil {
				msg := "Could not change the group's photo."
				if jpeg == nil {
					msg = "Could not remove the group's photo."
				}
				e.groupEditFailed(chat, err, msg)
				return
			}
			e.pictureChanged(chat, jpeg)
		}
	})
}

// pictureChanged shows a picture WhatsApp accepted at once (nil: it was
// removed), as upstream's show_sent_picture: the cached avatar is replaced.
func (e *Engine) pictureChanged(id string, jpeg []byte) {
	for _, full := range []bool{false, true} {
		path := e.avatarFile(id, full)
		if err := writePrivate(path, jpeg); err != nil {
			os.Remove(path)
			continue
		}
		if len(jpeg) == 0 {
			path = ""
		}
		e.avatar(id, full, path)
	}
}

// pickGroupPicture asks for a picture, squares it and sets it.
func (e *Engine) pickGroupPicture(chat string) {
	c, err := e.arch.Chat(chat)
	if err != nil || !canEditInfo(c) {
		e.emitError(groupEditRefused)
		return
	}
	e.pickPicture("Choose a group photo", func(jpeg []byte) { e.setGroupPicture(chat, jpeg) })
}

// pickPicture asks for a picture with the Pick broker, then hands set a
// square JPEG of it on the engine's goroutine.
func (e *Engine) pickPicture(title string, set func(jpeg []byte)) {
	e.job(func(ctx context.Context) func() {
		paths := e.h.Pick(ctx, &abi.EventPickRequest{Kind: abi.PickKindFile, Title: b(title), Filters: pictureFilter})
		if len(paths) == 0 {
			return nil
		}
		jpeg, err := squareJPEG(paths[0])
		return func() {
			if err != nil {
				e.emitError("Could not use this picture: " + err.Error())
				return
			}
			set(jpeg)
		}
	})
}

// squareJPEG is upstream's profile_picture_jpeg: the picture upright,
// cropped to a centred square, scaled down to pictureSide, as a JPEG.
func squareJPEG(path string) ([]byte, error) {
	data, err := readBounded(path, pictureMax)
	if err != nil {
		return nil, err
	}
	w, h, err := av.InspectPicture(data)
	if err != nil {
		return nil, err
	}
	side := min(w, h)
	edited, err := av.EditPicture(data, av.PictureCrop{X: uint32((w - side) / 2), Y: uint32((h - side) / 2), Width: uint32(side), Height: uint32(side)}, 0)
	if err != nil {
		return nil, err
	}
	m, _, err := image.Decode(bytes.NewReader(edited.Bytes))
	if err != nil {
		return nil, err
	}
	size := min(side, pictureSide)
	square := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(square, square.Rect, image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(square, square.Rect, m, m.Bounds(), draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, square, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readBounded reads a regular file of at most limit bytes.
func readBounded(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a file")
	}
	if info.Size() > limit {
		return nil, errors.New("the file is too large")
	}
	return os.ReadFile(path)
}

// leaveGroup tells WhatsApp we leave, then keeps the history: the chat
// stays with its messages and only stops taking new ones. Offline, nothing
// changes here.
func (e *Engine) leaveGroup(chat string, archive bool) {
	if chatKind("", chat) != abi.ChatKindGroup {
		return
	}
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() {
		e.emitError("Could not leave the group.")
		e.emitChat(chat)
		return
	}
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		err := cli.LeaveGroup(ctx, j)
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				slog.Warn("zapfast wa: leaving a group", "err", err)
				e.emitError("Could not leave the group.")
				// The archive's state undoes the mark the UI made.
				e.emitChat(chat)
				return
			}
			e.left(chat, archive)
		}
	})
}

// left marks a group as one we no longer post in.
func (e *Engine) left(chat string, archive bool) {
	err := e.updateChat(chat, func(c *store.Chat) {
		c.Left, c.Admin = true, false
		var kept []string
		for _, p := range c.Participants {
			if !e.isMe(p) {
				kept = append(kept, p)
			}
		}
		c.Participants = kept
		if archive {
			c.Archived = true
		}
	})
	if err != nil || !archive {
		return
	}
	j, _ := jidOf(chat)
	at, m := e.lastKey(chat)
	e.tellPhone(appstate.BuildArchive(j, true, at, e.keyOf(j, m)))
}

// requestGroupInfo asks for a group's info, once a session unless forced
// (after a refused edit, or a join): its name, description, members, who
// administers it, and who may edit its info.
func (e *Engine) requestGroupInfo(chat string, force bool) {
	j, ok := jidOf(chat)
	if !ok || j.Server != types.GroupServer || !e.cli.IsConnected() || e.groupsAsked[chat] && !force {
		return
	}
	if !force {
		if c, err := e.arch.Chat(chat); err == nil && (c.Left || c.Name != "" && len(c.Participants) > 0 && c.Description != nil) {
			return
		}
	}
	e.groupsAsked[chat] = true
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		info, err := cli.GroupInfo(ctx, j)
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				slog.Warn("zapfast wa: could not fetch a group's info")
				return
			}
			e.applyGroup(chat, info)
		}
	})
}

// applyGroup files a group's info.
func (e *Engine) applyGroup(chat string, info *types.GroupInfo) {
	var members []string
	admin, in := false, false
	for _, p := range info.Participants {
		id := p.JID
		if !p.PhoneNumber.IsEmpty() {
			id = p.PhoneNumber
		}
		member := e.canonical(id)
		mine := e.isMe(member) || e.isMe(p.JID.ToNonAD().String()) || !p.LID.IsEmpty() && e.isMe(p.LID.ToNonAD().String())
		if mine {
			in = true
			admin = p.IsAdmin || p.IsSuperAdmin
		}
		members = append(members, member)
	}
	if err := e.updateChat(chat, func(c *store.Chat) {
		c.Kind = "group"
		if info.Name != "" {
			c.Name = info.Name
		}
		desc := strings.TrimSpace(info.Topic)
		c.Description = &desc
		c.Participants = members
		c.Admin = admin
		c.InfoLocked = info.IsLocked
		c.ReadOnly = info.IsAnnounce && !admin
		if !c.Left || in {
			c.Left = !in && len(members) > 0
		}
		if info.DisappearingTimer > 0 || info.IsEphemeral {
			c.EphemeralSeconds = info.DisappearingTimer
		}
	}); err != nil {
		slog.Warn("zapfast wa: filing a group's info", "err", err)
	}
}

// groupChanged files a change WhatsApp announced in a group.
func (e *Engine) groupChanged(v *events.GroupInfo) error {
	chat := e.canonical(v.JID)
	if _, err := e.arch.Chat(chat); err != nil {
		if noRows(err) {
			// A group we do not know yet: its info says the rest.
			e.requestGroupInfo(chat, true)
			return nil
		}
		return err
	}
	leftUs := false
	for _, j := range v.Leave {
		if e.isMe(e.canonical(j)) || e.isMe(j.ToNonAD().String()) {
			leftUs = true
		}
	}
	err := e.updateChat(chat, func(c *store.Chat) {
		if v.Name != nil && v.Name.Name != "" {
			c.Name = v.Name.Name
		}
		if v.Topic != nil {
			desc := strings.TrimSpace(v.Topic.Topic)
			if v.Topic.TopicDeleted {
				desc = ""
			}
			c.Description = &desc
		}
		if v.Locked != nil {
			c.InfoLocked = v.Locked.IsLocked
		}
		if v.Announce != nil {
			c.ReadOnly = v.Announce.IsAnnounce && !c.Admin
		}
		if v.Ephemeral != nil {
			c.EphemeralSeconds = 0
			if v.Ephemeral.IsEphemeral {
				c.EphemeralSeconds = v.Ephemeral.DisappearingTimer
			}
		}
		for _, j := range v.Join {
			id := e.canonical(j)
			if !contains(c.Participants, id) {
				c.Participants = append(c.Participants, id)
			}
			if e.isMe(id) {
				c.Left = false
			}
		}
		for _, j := range v.Leave {
			c.Participants = without(c.Participants, e.canonical(j))
		}
		if leftUs {
			c.Left, c.Admin = true, false
		}
		for _, j := range v.Promote {
			if e.isMe(e.canonical(j)) {
				c.Admin = true
			}
		}
		for _, j := range v.Demote {
			if e.isMe(e.canonical(j)) {
				c.Admin = false
			}
		}
	})
	if len(v.Promote) > 0 || len(v.Demote) > 0 {
		// Who may post and edit follows the roles: ask again.
		e.requestGroupInfo(chat, true)
	}
	return err
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func without(list []string, s string) []string {
	var kept []string
	for _, x := range list {
		if x != s {
			kept = append(kept, x)
		}
	}
	return kept
}

// --- invites -----------------------------------------------------------------

// inviteError is a short explanation of a failed invite lookup or join:
// protocol errors can carry identifiers, so only the kind of failure is
// told (upstream's invite_error).
func inviteError(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "401") || strings.Contains(s, "not-authorized") || strings.Contains(s, "revoked"):
		return "This link was reset or you are not allowed to join."
	case strings.Contains(s, "404") || strings.Contains(s, "item-not-found") || strings.Contains(s, "invalid"):
		return "This invite link is invalid or has been reset."
	case strings.Contains(s, "410") || strings.Contains(s, "gone"):
		return "This invite link has expired."
	case strings.Contains(s, "409") || strings.Contains(s, "conflict"):
		return "You are already in this group."
	case strings.Contains(s, "406") || strings.Contains(s, "full"):
		return "This group is full."
	}
	return "WhatsApp could not open this invite link. Try again later."
}

// inviteCode is the code of an invite link or a bare code.
func inviteCode(code string) string {
	code = strings.TrimSpace(code)
	for _, prefix := range []string{"https://chat.whatsapp.com/invite/", "https://chat.whatsapp.com/", "chat.whatsapp.com/"} {
		code = strings.TrimPrefix(code, prefix)
	}
	code, _, _ = strings.Cut(code, "?")
	return strings.Trim(code, "/")
}

func (e *Engine) previewInvite(code string) {
	fail := func(msg string) {
		ev := abi.Event{Tag: abi.EventTagInvitePreview}
		ev.InvitePreview.Code = b(code)
		ev.InvitePreview.Result.Tag = abi.InviteResultTagErr
		ev.InvitePreview.Result.Err.Error = b(out.Toast(msg))
		e.out.Emit(&ev)
	}
	if !e.cli.IsConnected() {
		fail("Zap is not connected to WhatsApp")
		return
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		info, err := cli.GroupInfoFromLink(ctx, inviteCode(code))
		return func() {
			if err != nil || info == nil {
				if err == nil {
					err = errors.New("invalid")
				}
				fail(inviteError(err))
				return
			}
			ev := abi.Event{Tag: abi.EventTagInvitePreview}
			ev.InvitePreview.Code = b(code)
			r := &ev.InvitePreview.Result
			r.Tag = abi.InviteResultTagOk
			members := info.ParticipantCount
			if members == 0 {
				members = len(info.Participants)
			}
			r.Ok.Info = abi.InviteInfo{ID: b(info.JID.ToNonAD().String()), Subject: b(out.Clip(info.Name, 1024)), Members: uint64(max(members, 0)), Approval: info.IsJoinApprovalRequired}
			if d := strings.TrimSpace(info.Topic); d != "" {
				r.Ok.Info.Description, r.Ok.Info.HasDescription = b(out.Clip(d, 8192)), true
			}
			e.out.Emit(&ev)
		}
	})
}

func (e *Engine) joinInvite(code string) {
	answer := func(r abi.JoinResult) {
		ev := abi.Event{Tag: abi.EventTagInviteJoined}
		ev.InviteJoined = abi.EventInviteJoined{Code: b(code), Result: r}
		e.out.Emit(&ev)
	}
	fail := func(msg string) {
		var r abi.JoinResult
		r.Tag = abi.JoinResultTagErr
		r.Err.Error = b(out.Toast(msg))
		answer(r)
	}
	if !e.cli.IsConnected() {
		fail("Zap is not connected to WhatsApp")
		return
	}
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		j, pending, err := cli.JoinGroupWithLink(ctx, inviteCode(code))
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				fail(inviteError(err))
				return
			}
			chat := e.canonical(j)
			if !pending {
				e.ensureChat(chat, "")
				if err := e.updateChat(chat, func(c *store.Chat) { c.Left, c.Kind = false, "group" }); err != nil {
					slog.Warn("zapfast wa: filing a joined group", "err", err)
				}
				e.requestGroupInfo(chat, true)
			}
			var r abi.JoinResult
			r.Tag = abi.JoinResultTagOk
			r.Ok.Chat, r.Ok.Pending = b(chat), pending
			answer(r)
		}
	})
}
