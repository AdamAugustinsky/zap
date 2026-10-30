// Parts from zapfast-go 3104c46 (internal/backend/polls.go: pollVote,
// refreshPoll, pollSnapshots; interactive.go: InteractiveReply), MIT.
// Changed for Zap: the answers are upstream's PollCreated, PollVoted and
// InteractiveReplyState, and a poll's votes are counted on its row.

package wa

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/out"
	"zapfast/gocore/store"
)

// --- creating and voting -----------------------------------------------------

// validDraft is upstream's PollDraft::validated: a question of up to 255
// characters and 2 to 12 different answers of 1 to 100.
func validDraft(d abi.PollDraft) (string, []string, error) {
	question := strings.TrimSpace(string(d.Question))
	if question == "" || utf8.RuneCountInString(question) > 255 {
		return "", nil, errors.New("Enter a question of up to 255 characters.")
	}
	var options []string
	seen := map[string]bool{}
	for _, o := range d.Options {
		option := strings.TrimSpace(string(o))
		if option == "" || utf8.RuneCountInString(option) > 100 {
			return "", nil, errors.New("Add 2–12 answers, each with 1–100 characters.")
		}
		if seen[option] {
			return "", nil, errors.New("Each answer must be different.")
		}
		seen[option] = true
		options = append(options, option)
	}
	if len(options) < 2 || len(options) > 12 {
		return "", nil, errors.New("Add 2–12 answers, each with 1–100 characters.")
	}
	return question, options, nil
}

// pollCreation is whatsmeow's BuildPollCreation: its secret is what votes
// are encrypted with.
func pollCreation(question string, options []string, selectable int) *waE2E.Message {
	secret := make([]byte, 32)
	rand.Read(secret)
	p := &waE2E.PollCreationMessage{Name: proto.String(question), SelectableOptionsCount: proto.Uint32(uint32(selectable))}
	for _, o := range options {
		p.Options = append(p.Options, &waE2E.PollCreationMessage_Option{OptionName: proto.String(o)})
	}
	return &waE2E.Message{PollCreationMessage: p, MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret}}
}

func (e *Engine) pollCreated(chat string, err error) {
	ev := abi.Event{Tag: abi.EventTagPollCreated}
	ev.PollCreated.Chat = b(chat)
	if err != nil {
		ev.PollCreated.Error, ev.PollCreated.HasError = b(out.Toast(err.Error())), true
	}
	e.out.Emit(&ev)
}

// createPoll sends a poll; the row is filed once WhatsApp took it.
func (e *Engine) createPoll(chat string, draft abi.PollDraft) {
	j, ok := jidOf(chat)
	c, cerr := e.arch.Chat(chat)
	switch {
	case !ok || !e.cli.IsConnected():
		e.pollCreated(chat, errors.New("Not connected to WhatsApp"))
		return
	case chatKind("", chat) == abi.ChatKindBroadcast || cerr == nil && !c.CanSend():
		e.pollCreated(chat, errors.New("Polls cannot be sent to this chat."))
		return
	case cerr == nil && c.EphemeralSeconds > 0:
		e.pollCreated(chat, errors.New("Poll creation in disappearing-message chats is not supported yet."))
		return
	}
	question, options, err := validDraft(draft)
	if err != nil {
		e.pollCreated(chat, err)
		return
	}
	selectable := 1
	if draft.Multiple {
		selectable = len(options)
	}
	msg := pollCreation(question, options, selectable)
	id := e.cli.GenerateMessageID()
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		_, err := cli.SendMessage(ctx, j, msg, id)
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				e.pollCreated(chat, errors.New("Could not send the poll: "+err.Error()))
				return
			}
			row := e.ownRow(chat, j, id, msg)
			row.Status = "sent"
			if !e.store(row) {
				e.pollCreated(chat, errors.New("The poll was sent, but could not be saved here."))
				return
			}
			e.pollCreated(chat, nil)
		}
	})
}

func (e *Engine) pollVoted(chat, id string, err error) {
	ev := abi.Event{Tag: abi.EventTagPollVoted}
	ev.PollVoted = abi.EventPollVoted{Chat: b(chat), Message: b(id)}
	if err != nil {
		ev.PollVoted.Error, ev.PollVoted.HasError = b(out.Toast(err.Error())), true
	}
	e.out.Emit(&ev)
}

// pollInfo is a poll's message info, as its votes are encrypted for.
func (e *Engine) pollInfo(j types.JID, m store.Message) types.MessageInfo {
	sender := j
	if m.FromMe {
		sender, _ = types.ParseJID(e.me())
	} else if s, ok := jidOf(m.Sender); ok {
		sender = s
	}
	return types.MessageInfo{MessageSource: types.MessageSource{Chat: j, Sender: sender, IsFromMe: m.FromMe, IsGroup: j.Server == types.GroupServer}, ID: m.ID}
}

// votePoll sends our vote: the chosen options, none to take it back.
func (e *Engine) votePoll(chat, id string, choices []uint64) {
	key := [2]string{chat, id}
	if e.polls[key] {
		return
	}
	notReady := errors.New("This poll is not ready for voting. Reconnect and try again.")
	j, ok := jidOf(chat)
	m, err := e.arch.Message(chat, id)
	if !ok || err != nil || m.Kind != kindPoll || m.Deleted || !e.cli.IsConnected() {
		e.pollVoted(chat, id, notReady)
		return
	}
	slices.Sort(choices)
	choices = slices.Compact(choices)
	limit := m.PollSelectable
	if limit <= 0 {
		limit = len(m.Poll)
	}
	var names []string
	for _, c := range choices {
		if c >= uint64(len(m.Poll)) {
			e.pollVoted(chat, id, notReady)
			return
		}
		names = append(names, m.Poll[c].Name)
	}
	if len(names) > limit {
		e.pollVoted(chat, id, notReady)
		return
	}
	e.polls[key] = true
	info := e.pollInfo(j, m)
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		var resp whatsmeow.SendResponse
		msg, err := cli.BuildPollVote(ctx, &info, names)
		if err == nil {
			resp, err = cli.SendMessage(ctx, j, msg, "")
		}
		at := time.Now().UnixMilli()
		return func() {
			delete(e.polls, key)
			if gen != e.gen {
				return
			}
			if err != nil {
				e.pollVoted(chat, id, errors.New("Could not send your vote: "+err.Error()))
				return
			}
			vote := store.Vote{Sender: e.me(), Timestamp: at, ID: resp.ID, Options: whatsmeow.HashPollOptions(names)}
			if err := e.arch.SaveVote(chat, id, vote); err != nil {
				e.pollVoted(chat, id, errors.New("Your vote was sent, but could not be saved here."))
				return
			}
			e.recount(chat, id)
			e.pollVoted(chat, id, nil)
		}
	})
}

// refreshPoll asks the phone again for the history around a poll, whose
// votes from before this device linked are in it.
func (e *Engine) refreshPoll(chat, id string) {
	m, err := e.arch.Message(chat, id)
	if err != nil || m.Kind != kindPoll {
		return
	}
	if !m.PollHistoryComplete && e.older[chat] == nil && e.cli.IsConnected() {
		e.requestHistory(chat, m, false)
	}
	e.emitMessage(chat, id)
}

// --- votes -------------------------------------------------------------------

// validVote reports whether choices are option hashes of a poll, each once
// and no more than it lets choose.
func validVote(m store.Message, choices [][]byte) bool {
	limit := m.PollSelectable
	if limit <= 0 {
		limit = len(m.Poll)
	}
	if len(choices) > limit {
		return false
	}
	hashes := optionHashes(m)
	seen := map[string]bool{}
	for _, c := range choices {
		if seen[string(c)] || !slices.ContainsFunc(hashes, func(h []byte) bool { return bytes.Equal(h, c) }) {
			return false
		}
		seen[string(c)] = true
	}
	return true
}

func optionHashes(m store.Message) [][]byte {
	names := make([]string, len(m.Poll))
	for i, o := range m.Poll {
		names[i] = o.Name
	}
	return whatsmeow.HashPollOptions(names)
}

// pollVote files a vote that arrived: decrypted with the poll's secret,
// which whatsmeow kept when the poll arrived or was sent.
func (e *Engine) pollVote(evt *events.Message) error {
	update := unwrap(evt.Message).GetPollUpdateMessage()
	parent := update.GetPollCreationMessageKey().GetID()
	if parent == "" {
		return nil
	}
	chat := e.canonical(evt.Info.Chat)
	m, err := e.arch.Message(chat, parent)
	if noRows(err) {
		return nil
	}
	if err != nil || m.Deleted || m.Kind != kindPoll {
		return err
	}
	vote, err := e.cli.DecryptPollVote(e.ctx, evt)
	if err != nil {
		// The poll's secret has not arrived (a poll from before this
		// device linked): the phone's history holds the counts.
		slog.Debug("zapfast wa: a poll vote could not be read", "err", err)
		return nil
	}
	if !validVote(m, vote.GetSelectedOptions()) {
		return nil
	}
	at := update.GetSenderTimestampMS()
	if at == 0 {
		at = evt.Info.Timestamp.UnixMilli()
	}
	if err := e.arch.SaveVote(chat, parent, store.Vote{Sender: e.canonical(evt.Info.Sender), Timestamp: at, ID: evt.Info.ID, Options: vote.GetSelectedOptions()}); err != nil {
		return err
	}
	e.recount(chat, parent)
	return nil
}

// pollSnapshots files the votes a history copy of a poll carries; a
// complete set ends the poll's history.
func (e *Engine) pollSnapshots(evt *events.Message, chat string) error {
	updates := evt.SourceWebMsg.GetPollUpdates()
	if len(updates) == 0 {
		return nil
	}
	m, err := e.arch.Message(chat, evt.Info.ID)
	if err != nil {
		return nil
	}
	accepted := 0
	for _, u := range updates {
		k := u.GetPollUpdateMessageKey()
		sender := k.GetParticipant()
		switch {
		case k.GetFromMe():
			sender = e.me()
		case sender == "" && evt.Info.Chat.Server != types.GroupServer:
			sender = chat
		}
		j, ok := jidOf(sender)
		if !ok || k.GetID() == "" || u.GetVote() == nil || !validVote(m, u.GetVote().GetSelectedOptions()) {
			continue
		}
		at := u.GetSenderTimestampMS()
		if at == 0 {
			at = u.GetServerTimestampMS()
		}
		if err := e.arch.SaveVote(chat, m.ID, store.Vote{Sender: e.canonical(j), Timestamp: at, ID: k.GetID(), Options: u.GetVote().GetSelectedOptions()}); err != nil {
			return err
		}
		accepted++
	}
	if accepted == len(updates) {
		if err := e.arch.UpdateMessage(chat, m.ID, func(m *store.Message) { m.PollHistoryComplete = true }); err != nil {
			return err
		}
	}
	e.recount(chat, m.ID)
	return nil
}

// recount counts a poll's votes onto its options, each voter's latest
// vote once, and sends the row.
func (e *Engine) recount(chat, id string) {
	m, err := e.arch.Message(chat, id)
	if err != nil || m.Kind != kindPoll {
		return
	}
	votes, err := e.arch.Votes(chat, id)
	if err != nil {
		return
	}
	sort.Slice(votes, func(i, j int) bool {
		if votes[i].Timestamp != votes[j].Timestamp {
			return votes[i].Timestamp > votes[j].Timestamp
		}
		return votes[i].Sender < votes[j].Sender
	})
	hashes := optionHashes(m)
	poll := slices.Clone(m.Poll)
	for i := range poll {
		poll[i].Votes, poll[i].Selected, poll[i].Voters, poll[i].VoterTimes = 0, false, nil, nil
		for _, v := range votes {
			if slices.ContainsFunc(v.Options, func(c []byte) bool { return bytes.Equal(c, hashes[i]) }) {
				poll[i].Votes++
				poll[i].Voters = append(poll[i].Voters, v.Sender)
				poll[i].VoterTimes = append(poll[i].VoterTimes, v.Timestamp/1000)
				if e.isMe(v.Sender) {
					poll[i].Selected = true
				}
			}
		}
	}
	if err := e.arch.UpdateMessage(chat, id, func(t *store.Message) { t.Poll = poll }); err != nil {
		slog.Warn("zapfast wa: counting a poll", "err", err)
		return
	}
	e.emitMessage(chat, id)
}

// --- interactive replies -----------------------------------------------------

func (e *Engine) replyState(chat, id string, pending bool) {
	ev := abi.Event{Tag: abi.EventTagInteractiveReplyState}
	ev.InteractiveReplyState = abi.EventInteractiveReplyState{Chat: b(chat), Message: b(id), Pending: pending}
	e.out.Emit(&ev)
}

// replyInteractive answers a business message's button or list row: the
// reply is a message of ours, quoting it.
func (e *Engine) replyInteractive(c *abi.CommandReplyInteractive) {
	chat, id := string(c.Chat), string(c.Message)
	key := [2]string{chat, id}
	if e.replies[key] {
		return
	}
	m, err := e.arch.Message(chat, id)
	if err != nil || m.Kind != kindInteractive || m.Deleted || m.FromMe {
		e.emitError("This message cannot be answered here")
		return
	}
	choice := -1
	if c.HasChoice {
		choice = int(min(c.Choice, 1<<20))
	}
	action, ok := replyIndex(m.Actions, int(min(c.Button, 1<<20)), choice)
	switch action.Kind {
	case "reply", "template-reply", "list", "native-reply", "native-list":
	default:
		ok = false
	}
	if !ok {
		e.emitError("This button opens on your phone")
		return
	}
	var p waE2E.Message
	if len(m.Raw) == 0 || proto.Unmarshal(m.Raw, &p) != nil {
		e.emitError("The original message is not available to answer")
		return
	}
	reply, err := interactiveReply(m, &p, action.Index)
	if err != nil {
		e.emitError(err.Error())
		return
	}
	j, ok := jidOf(chat)
	if !ok || !e.cli.IsConnected() || !e.writable(chat) {
		e.emitError("Not connected to WhatsApp")
		return
	}
	e.replies[key] = true
	e.replyState(chat, id, true)
	mid := e.cli.GenerateMessageID()
	if !e.store(e.ownRow(chat, j, mid, reply)) {
		delete(e.replies, key)
		e.replyState(chat, id, false)
		return
	}
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		_, err := cli.SendMessage(ctx, j, reply, mid)
		return func() {
			delete(e.replies, key)
			if gen != e.gen {
				return
			}
			e.replyState(chat, id, false)
			e.sent(chat, mid, err)
		}
	})
}

// interactiveReply is the answer to button index of an interactive
// message, its protocol id read again from the archived original, so a
// reply can never be made up (from zapfast-go 3104c46 InteractiveReply).
func interactiveReply(m store.Message, p *waE2E.Message, index int) (*waE2E.Message, error) {
	unavailable := errors.New("This button opens on your phone")
	if index < 0 {
		return nil, unavailable
	}
	c := &waE2E.ContextInfo{StanzaID: proto.String(m.ID), Participant: proto.String(m.Sender), QuotedMessage: p}
	if b := p.GetButtonsMessage(); b != nil {
		if index >= len(b.GetButtons()) {
			return nil, unavailable
		}
		button := b.GetButtons()[index]
		if button.GetType() != waE2E.ButtonsMessage_Button_RESPONSE || button.GetButtonID() == "" {
			return nil, unavailable
		}
		return &waE2E.Message{ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{SelectedButtonID: button.ButtonID, Response: &waE2E.ButtonsResponseMessage_SelectedDisplayText{SelectedDisplayText: button.GetButtonText().GetDisplayText()}, Type: waE2E.ButtonsResponseMessage_DISPLAY_TEXT.Enum(), ContextInfo: c}}, nil
	}
	if list := p.GetListMessage(); list != nil {
		i := 0
		for _, section := range list.GetSections() {
			for _, row := range section.GetRows() {
				if i == index {
					if row.GetRowID() == "" {
						return nil, unavailable
					}
					return &waE2E.Message{ListResponseMessage: &waE2E.ListResponseMessage{Title: row.Title, ListType: waE2E.ListResponseMessage_SINGLE_SELECT.Enum(), SingleSelectReply: &waE2E.ListResponseMessage_SingleSelectReply{SelectedRowID: row.RowID}, ContextInfo: c}}, nil
				}
				i++
			}
		}
		return nil, unavailable
	}
	if template := p.GetTemplateMessage(); template != nil {
		h := template.GetHydratedTemplate()
		if h == nil {
			h = template.GetHydratedFourRowTemplate()
		}
		if h == nil || index >= len(h.GetHydratedButtons()) {
			return nil, unavailable
		}
		button := h.GetHydratedButtons()[index].GetQuickReplyButton()
		if button == nil || button.GetID() == "" {
			return nil, unavailable
		}
		return &waE2E.Message{TemplateButtonReplyMessage: &waE2E.TemplateButtonReplyMessage{SelectedID: button.ID, SelectedDisplayText: button.DisplayText, SelectedIndex: proto.Uint32(uint32(index)), ContextInfo: c}}, nil
	}
	if im := p.GetInteractiveMessage(); im != nil {
		choices := nativeChoices(im)
		if index >= len(choices) {
			return nil, unavailable
		}
		choice := choices[index]
		name := "quick_reply"
		switch choice.kind {
		case "native-list":
			name = "single_select"
		case "native-reply":
		default:
			return nil, unavailable
		}
		params, err := json.Marshal(struct {
			ID string `json:"id"`
		}{choice.id})
		if err != nil {
			return nil, err
		}
		return &waE2E.Message{InteractiveResponseMessage: &waE2E.InteractiveResponseMessage{
			Body: &waE2E.InteractiveResponseMessage_Body{Text: proto.String(choice.label)},
			InteractiveResponseMessage: &waE2E.InteractiveResponseMessage_NativeFlowResponseMessage_{NativeFlowResponseMessage: &waE2E.InteractiveResponseMessage_NativeFlowResponseMessage{
				Name: proto.String(name), ParamsJSON: proto.String(string(params)), Version: proto.Int32(1),
			}},
			ContextInfo: c,
		}}, nil
	}
	return nil, unavailable
}
