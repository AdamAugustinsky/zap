package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/store"
)

// flow is a native-flow message of buttons, each a name and its params.
func flow(body, footer string, buttons ...[2]string) *waE2E.Message {
	im := &waE2E.InteractiveMessage{InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{}}}
	if body != "" {
		im.Body = &waE2E.InteractiveMessage_Body{Text: proto.String(body)}
	}
	if footer != "" {
		im.Footer = &waE2E.InteractiveMessage_Footer{Text: proto.String(footer)}
	}
	nf := im.GetNativeFlowMessage()
	for _, b := range buttons {
		nf.Buttons = append(nf.Buttons, &waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{Name: proto.String(b[0]), ButtonParamsJSON: proto.String(b[1])})
	}
	return &waE2E.Message{InteractiveMessage: im}
}

// A business message is never an empty bubble: a footer stands in for an
// empty body, a button this app cannot carry out shows as one that opens on
// the phone, a Pix key can be copied, and a message with nothing to show
// says where it opens.
func TestInteractiveMessagesAreNeverEmpty(t *testing.T) {
	h := linked(t)
	card := func(id string, p *waE2E.Message) abi.ContentInteractive {
		t.Helper()
		h.inject(message(peer, peer, id, t0, p))
		c := messagesOf(h.take())[0].Content
		if c.Tag != abi.ContentTagInteractive {
			t.Fatalf("%s: %+v", id, c)
		}
		return c.Interactive
	}

	c := card("F1", flow("", "Reply by Friday", [2]string{"galaxy_message", `{"flow_cta":"Fill in the form","flow_id":"1"}`}))
	if string(c.Text) != "Reply by Friday" || !c.HasCard || len(c.Card.Buttons) != 1 {
		t.Fatalf("form: %+v", c)
	}
	if b := c.Card.Buttons[0]; string(b.Label) != "Fill in the form" || b.Action.Tag != abi.InteractiveActionTagUnavailable || b.HasURL {
		t.Fatalf("form button: %+v", b)
	}

	c = card("F2", flow("", "", [2]string{"send_location", `{}`}, [2]string{"something_new", `not json`}))
	if !c.HasCard || len(c.Card.Buttons) != 2 || string(c.Card.Buttons[0].Label) != "Send location" || string(c.Card.Buttons[1].Label) != "Open on your phone" {
		t.Fatalf("unknown buttons: %+v", c)
	}

	pix := `{"currency":"BRL","total_amount":{"value":123456,"offset":100},"order":{"items":[{"name":"Mug","amount":{"value":1250,"offset":100},"quantity":2},{"name":"","amount":{"value":1,"offset":100}}]},` +
		`"payment_settings":[{"type":"pix_static_code","pix_static_code":{"merchant_name":"Loja","key":"pix@example.com","key_type":"EMAIL"}}]}`
	c = card("F3", flow("Your order", "", [2]string{"payment_info", pix}))
	if b := c.Card.Buttons; len(b) != 1 || string(b[0].Label) != "Copy Pix key" || b[0].Action.Tag != abi.InteractiveActionTagCopy || string(b[0].Action.Copy.Code) != "pix@example.com" {
		t.Fatalf("pix: %+v", c.Card.Buttons)
	}
	if want := "Your order\n\nPix: Loja\n2× Mug · R$ 25,00\nTotal: R$ 1.234,56"; string(c.Text) != want {
		t.Fatalf("pix order: %q, want %q", c.Text, want)
	}

	c = card("F5", flow("Talk to us", "", [2]string{"cta_call", `{"display_text":"Call us","phone_number":"+55 (11) 94075-8022"}`}, [2]string{"cta_call", `{"phone_number":"12"}`},
		[2]string{"cta_url", `{"display_text":"Shop","url":"shop.example/x"}`}, [2]string{"cta_url", `{"display_text":"Run","url":"javascript:alert(1)"}`}))
	b := c.Card.Buttons
	if len(b) != 4 || string(b[0].URL) != "tel:+5511940758022" || string(b[0].Label) != "Call us" || b[1].HasURL || b[1].Action.Tag != abi.InteractiveActionTagUnavailable {
		t.Fatalf("calls: %+v", b)
	}
	if string(b[2].URL) != "https://shop.example/x" || b[3].HasURL || b[3].Action.Tag != abi.InteractiveActionTagUnavailable {
		t.Fatalf("links: %+v", b[2:])
	}

	// a body of invisible fillers and a link with no text of its own
	c = card("F6", flow("\u200e\n\u3164 ", "", [2]string{"cta_url", `{"url":"https://www.shop.example/x"}`}))
	if len(c.Text) != 0 || !c.HasCard || len(c.Card.Buttons) != 1 || string(c.Card.Buttons[0].Label) != "shop.example" || string(c.Card.Buttons[0].URL) != "https://www.shop.example/x" {
		t.Fatalf("invisible body: %q %+v", c.Text, c.Card)
	}
	c = card("F7", flow("\n\nHello\n", "", [2]string{"cta_copy", `{"copy_code":"X1"}`}))
	if string(c.Text) != "Hello" || string(c.Card.Buttons[0].Label) != "Copy code" {
		t.Fatalf("blank lines: %q %+v", c.Text, c.Card.Buttons)
	}

	c = card("F4", flow("", ""))
	if len(c.Text) != 0 || !c.HasCard || !c.Card.NeedsPhone {
		t.Fatalf("nothing to show: %+v", c)
	}

	// A button that opens on the phone sends no reply.
	h.send(abi.Command{Tag: abi.CommandTagReplyInteractive, ReplyInteractive: abi.CommandReplyInteractive{Chat: chatID(peer), Message: []byte("F1")}})
	if sent := h.f.sent; len(sent) != 0 {
		t.Fatalf("sent %d replies", len(sent))
	}
}

// What an earlier parser filed for a native-flow message with no body and
// buttons it dropped is read again from the original at start.
func TestFiledInteractiveMessagesAreReadAgain(t *testing.T) {
	h := newHarness(t, options{})
	raw, err := proto.Marshal(flow("", "Reply by Friday", [2]string{"galaxy_message", `{"flow_cta":"Fill in the form"}`}))
	if err != nil {
		t.Fatal(err)
	}
	storeMessage(t, h, store.Message{ID: "OLDFLOW", ChatID: peer.String(), Sender: peer.String(), Timestamp: t0.Unix() - 60, Kind: kindInteractive, Status: "delivered", Raw: raw})
	h.start()
	m, err := h.e.arch.Message(peer.String(), "OLDFLOW")
	if err != nil || m.Text != "Reply by Friday" || len(m.Actions) != 1 || m.Actions[0].Kind != "unavailable" {
		t.Fatalf("read again: %+v %v", m, err)
	}
	if c, err := h.e.arch.Chat(peer.String()); err != nil || c.Preview != "Reply by Friday" {
		t.Fatalf("preview: %+v %v", c.Preview, err)
	}
}

func TestMoneyAndLinkChecks(t *testing.T) {
	for _, c := range []struct {
		a        amount
		currency string
		want     string
	}{
		{amount{123456, 100}, "BRL", "R$ 1.234,56"},
		{amount{5, 100}, "BRL", "R$ 0,05"},
		{amount{100000000, 100}, "BRL", "R$ 1.000.000,00"},
		{amount{1999, 100}, "USD", "USD 19.99"},
		{amount{7, 0}, "", "7"},
	} {
		if got := money(c.a, c.currency); got != c.want {
			t.Errorf("money(%v, %q) = %q, want %q", c.a, c.currency, got, c.want)
		}
	}
	for raw, want := range map[string]string{
		"https://shop.example/x": "ok", "": "empty", "shop.example/x": "no-scheme-fixed", "localhost": "no-scheme",
		"javascript:alert(1)": "scheme-other", "https:///x": "no-host", "https://a:b@shop.example": "credentials", "https://x\x00": "control",
	} {
		if got := linkCheck(raw); got != want {
			t.Errorf("linkCheck(%q) = %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]string{"+55 11 94075-8022": "+5511940758022", "1234": "", "+1 (555) 010-9999 x2": "", "55+11": ""} {
		if got := dialable(raw); got != want {
			t.Errorf("dialable(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A business template now comes as a template carrying a native-flow
// message, with no hydrated template: its body, header picture and buttons
// are read, and its quick replies go.
func TestTemplatesCarryingANativeFlowMessage(t *testing.T) {
	h := linked(t)
	im := flow("Your order shipped", "", [2]string{"cta_url", `{"display_text":"Track","url":"https://track.example/1"}`}, [2]string{"quick_reply", `{"display_text":"Thanks","id":"ok"}`}).GetInteractiveMessage()
	im.Header = &waE2E.InteractiveMessage_Header{Media: &waE2E.InteractiveMessage_Header_ImageMessage{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(100)}}}
	tm := &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{Format: &waE2E.TemplateMessage_InteractiveMessageTemplate{InteractiveMessageTemplate: im}}}
	h.inject(message(peer, peer, "T1", t0, tm))
	c := messagesOf(h.take())[0].Content.Interactive
	if string(c.Text) != "Your order shipped" || !c.Card.HasImage || len(c.Card.Buttons) != 2 || string(c.Card.Buttons[0].URL) != "https://track.example/1" {
		t.Fatalf("template: %q %+v", c.Text, c.Card)
	}
	h.send(abi.Command{Tag: abi.CommandTagReplyInteractive, ReplyInteractive: abi.CommandReplyInteractive{Chat: chatID(peer), Message: []byte("T1"), Button: 1}})
	h.take()
	r := h.f.lastSent().msg.GetInteractiveResponseMessage()
	if r.GetBody().GetText() != "Thanks" || r.GetNativeFlowResponseMessage().GetName() != "quick_reply" || r.GetContextInfo().GetStanzaID() != "T1" {
		t.Fatalf("reply: %v", r)
	}
}

// A template filed empty by an earlier parser is read again at start.
func TestFiledTemplatesAreReadAgain(t *testing.T) {
	h := newHarness(t, options{})
	im := flow("Hello from the shop", "", [2]string{"cta_url", `{"display_text":"Open","url":"https://shop.example"}`}).GetInteractiveMessage()
	raw, err := proto.Marshal(&waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{Format: &waE2E.TemplateMessage_InteractiveMessageTemplate{InteractiveMessageTemplate: im}}})
	if err != nil {
		t.Fatal(err)
	}
	storeMessage(t, h, store.Message{ID: "OLDT", ChatID: peer.String(), Sender: peer.String(), Timestamp: t0.Unix() - 60, Kind: kindInteractive, Status: "delivered", Raw: raw})
	h.start()
	if m, err := h.e.arch.Message(peer.String(), "OLDT"); err != nil || m.Text != "Hello from the shop" || len(m.Actions) != 1 {
		t.Fatalf("read again: %+v %v", m, err)
	}
}
