package wa

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/out"
)

func fuzzEvent(p *waE2E.Message) *events.Message {
	return &events.Message{Info: types.MessageInfo{ID: "3EB0FUZZ", Timestamp: t0}, Message: p}
}

// FuzzLocationAndContactText checks what parse.go's comment promises of a
// location's name and address and a contact's name and vCard: kept apart
// in the row's text, and given back to the abi fields by convert.go, the
// name (one line) and the rest as they arrived.
func FuzzLocationAndContactText(f *testing.F) {
	f.Add("Alexanderplatz", "Berlin")
	f.Add("Katherine", "BEGIN:VCARD\nEND:VCARD")
	f.Add("", "")
	f.Add("Home", "Rua A, 1\nSão Paulo\n")
	f.Add("two\nlines", "address")
	f.Add("\r\n", "\n")
	f.Add("\xff", "\xfe\n")
	f.Fuzz(func(t *testing.T, name, rest string) {
		cv := conv{}
		var r out.Record

		loc := parseMessage(fuzzEvent(&waE2E.Message{LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude: proto.Float64(1), DegreesLongitude: proto.Float64(2), Name: proto.String(name), Address: proto.String(rest),
		}}), "1@s.whatsapp.net", "2@s.whatsapp.net")
		c := cv.content(&r, loc)
		if c.Tag != abi.ContentTagLocation {
			t.Fatalf("%q %q: tag %v", name, rest, c.Tag)
		}
		l := c.Location
		if l.HasName == (len(l.Name) == 0) || l.HasAddress == (len(l.Address) == 0) {
			t.Fatalf("%q %q: Has flags disagree with %+v", name, rest, l)
		}
		if strings.Contains(string(l.Name), "\n") {
			t.Fatalf("%q %q: the name %q is more than a line", name, rest, l.Name)
		}
		if string(l.Address) != rest {
			t.Fatalf("location %q %q: address %q", name, rest, l.Address)
		}
		if !strings.Contains(name, "\n") && string(l.Name) != name {
			t.Fatalf("location %q %q: name %q", name, rest, l.Name)
		}

		ct := parseMessage(fuzzEvent(&waE2E.Message{ContactMessage: &waE2E.ContactMessage{
			DisplayName: proto.String(name), Vcard: proto.String(rest),
		}}), "1@s.whatsapp.net", "2@s.whatsapp.net")
		c = cv.content(&r, ct)
		if c.Tag != abi.ContentTagContact {
			t.Fatalf("%q %q: tag %v", name, rest, c.Tag)
		}
		if string(c.Contact.Vcard) != rest {
			t.Fatalf("contact %q %q: vCard %q", name, rest, c.Contact.Vcard)
		}
		if strings.Contains(string(c.Contact.DisplayName), "\n") || len(c.Contact.DisplayName) > 1024 {
			t.Fatalf("contact %q %q: name %q", name, rest, c.Contact.DisplayName)
		}
		if !strings.Contains(name, "\n") && string(c.Contact.DisplayName) != out.Clip(name, 1024) {
			t.Fatalf("contact %q %q: name %q", name, rest, c.Contact.DisplayName)
		}
		// The summary a chat row shows is the name's line.
		if s := summary(ct); strings.Contains(s, "\n") && !strings.Contains(rest, "\n") {
			t.Fatalf("contact %q %q: summary %q", name, rest, s)
		}
	})
}

// FuzzNativeChoices checks a native-flow message's buttons, whose
// parameters are JSON from the sender: only the kinds this app shows, each
// with what it needs, a web link only when it is safe, and a bounded list.
func FuzzNativeChoices(f *testing.F) {
	f.Add("quick_reply", `{"display_text":"Yes","id":"y"}`)
	f.Add("cta_url", `{"display_text":"Open","url":"https://zap.example/x"}`)
	f.Add("cta_url", `{"display_text":"Open","url":"https://user:pw@zap.example/"}`)
	f.Add("cta_url", `{"url":"javascript:alert(1)"}`)
	f.Add("cta_url", `{"url":"http:///nohost"}`)
	f.Add("cta_copy", `{"display_text":"Copy","copy_code":"ABC123"}`)
	f.Add("single_select", `{"title":"Pick","sections":[{"title":"S","rows":[{"id":"1","title":"One","description":"d"},{"id":"","title":"x"}]}]}`)
	f.Add("single_select", `{"sections":null}`)
	f.Add("cta_call", `{"phone_number":"+1"}`)
	f.Add("quick_reply", `not json`)
	f.Fuzz(func(t *testing.T, name, params string) {
		buttons := []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			{Name: proto.String(name), ButtonParamsJSON: proto.String(params)},
			{Name: proto.String("single_select"), ButtonParamsJSON: proto.String(params)},
		}
		im := &waE2E.InteractiveMessage{InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{Buttons: buttons},
		}}
		choices := nativeChoices(im)
		lists := 0
		for _, c := range choices {
			switch c.kind {
			case "native-reply":
				if c.id == "" || c.label == "" {
					t.Fatalf("%q %q: reply %+v", name, params, c)
				}
			case "native-list":
				lists++
				if c.id == "" || c.label == "" {
					t.Fatalf("%q %q: row %+v", name, params, c)
				}
			case "copy":
				if c.value == "" {
					t.Fatalf("%q %q: copy %+v", name, params, c)
				}
			case "url":
				if !safeURL(c.value) {
					t.Fatalf("%q %q: unsafe link %+v", name, params, c)
				}
			default:
				t.Fatalf("%q %q: kind %q", name, params, c.kind)
			}
		}
		if lists > 256 || len(choices) > 256+len(buttons) {
			t.Fatalf("%q %q: %d choices, %d rows", name, params, len(choices), lists)
		}
		if !json.Valid([]byte(params)) && len(choices) != 0 {
			t.Fatalf("%q %q: choices from invalid JSON: %+v", name, params, choices)
		}
	})
}

// FuzzSafeURL checks the web addresses a button may open: http or https,
// a host, no credentials, also when read back from its own string.
func FuzzSafeURL(f *testing.F) {
	for _, s := range []string{
		"https://zap.example/x", "http://a.b", "https://user:pw@zap.example/", "https://@zap.example", "javascript:alert(1)",
		"http:///nohost", "HTTPS://ZAP.EXAMPLE", "https://zap.example\\@evil.example", "https://zap.example%40evil.example",
		"//zap.example", "https://[::1]:80/", "https://zap.example:99999", "https://a b", "", "https://\x00",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if !safeURL(raw) {
			return
		}
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			t.Fatalf("%q is safe: %+v %v", raw, u, err)
		}
		// A space in the path is allowed (the opener escapes it); a control
		// character, which could split a header or a command line, is not.
		if strings.ContainsAny(raw, "\x00\r\n\t\x7f") {
			t.Fatalf("%q is safe with a control character", raw)
		}
		back, err := url.Parse(u.String())
		if err != nil || back.User != nil || back.Host != u.Host {
			t.Fatalf("%q reads back from %q as %+v, %v", raw, u.String(), back, err)
		}
	})
}
