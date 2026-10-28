package sys

import "testing"

// Ported from zf:src/target.rs and zf:src/safety.rs.

const chat20 = "20123456789@s.whatsapp.net"

func chatOf(t *testing.T, target string) string {
	t.Helper()
	l, ok := parseChat(target)
	if !ok {
		t.Fatalf("%q names no chat", target)
	}
	return l.chat
}

func TestANumberInAnyFormattingNamesOneChat(t *testing.T) {
	for _, target := range []string{"20123456789", "+20123456789", "+20 123 456 789", "+20 (123) 456-789", "  20123456789  ",
		"+20-123-456-789", "+20.123.456.789"} {
		if got := chatOf(t, target); got != chat20 {
			t.Errorf("%q: %s", target, got)
		}
	}
}

// LN-001: wa.me, the web send link and the URI all name the same chat.
func TestEveryLinkShapeNamesTheSameChat(t *testing.T) {
	for _, target := range []string{
		"https://wa.me/20123456789", "http://wa.me/20123456789", "https://wa.me/20123456789/", "https://www.wa.me/20123456789",
		"wa.me/20123456789", "https://wa.me/%2B20123456789",
		"whatsapp://send?phone=20123456789", "whatsapp-send://?phone=20123456789", "whatsapp://send?phone=20123456789&text=hello",
		"whatsapp://send?phone=%2B20123456789", "https://api.whatsapp.com/send?phone=20123456789",
	} {
		if got := chatOf(t, target); got != chat20 {
			t.Errorf("%q: %s", target, got)
		}
	}
}

// LN-002: the wa: scheme opens chats too.
func TestTheWaSchemeOpensChats(t *testing.T) {
	for _, target := range []string{"wa://send?phone=20123456789", "wa:send?phone=%2B20123456789", "WA://send?text=hi&phone=20123456789"} {
		if got := chatOf(t, target); got != chat20 {
			t.Errorf("%q: %s", target, got)
		}
	}
	if _, ok := parseChat("wa://send?text=hi"); ok {
		t.Error("a wa: link with no number named a chat")
	}
}

func TestALinkThatOnlyLooksLikeOneIsRefused(t *testing.T) {
	for _, target := range []string{"", "   ", "hello", "reload-themes", "--verbose", "https://wa.me/", "https://wa.me",
		"https://example.com/20123456789", "https://example.com/wa.me/20123456789", "whatsapp://send?text=hello",
		"https://example.com/?phone=20123456789", "https://evil.test/send?phone=20123456789", "phone=20123456789",
		"call 20123456789", "20123456789 please", "https://wa.me/abc20123456789", "wa.me/20123456789abc",
		"https://wa.me/?text=20123456789", "https://wa.me/2012?text=999999", "2026", "12345", "1234567890123456"} {
		if l, ok := parseChat(target); ok {
			t.Errorf("%q named %s", target, l.chat)
		}
	}
}

func TestAPathNumberWinsOverAQueryOne(t *testing.T) {
	if got := chatOf(t, "https://wa.me/20123456789?phone=999999999"); got != chat20 {
		t.Error(got)
	}
}

func TestAFragmentIsNeitherPartOfTheNumberNorTheTemplate(t *testing.T) {
	if got := chatOf(t, "https://wa.me/20123456789#section"); got != chat20 {
		t.Error(got)
	}
	if got := chatOf(t, "20123456789#section"); got != chat20 {
		t.Error(got)
	}
	if l, _ := parseChat("https://wa.me/20123456789?text=Hello#section"); l.text != "Hello" {
		t.Errorf("%q", l.text)
	}
	if l, _ := parseChat("whatsapp://send?phone=20123456789&text=a%23b"); l.text != "a#b" {
		t.Errorf("an escaped hash is part of the template: %q", l.text)
	}
}

func TestTheBoundsOfE164AreInclusive(t *testing.T) {
	for _, target := range []string{"123456", "123456789012345"} {
		if _, ok := parseChat(target); !ok {
			t.Errorf("%q", target)
		}
	}
}

// LN-003's input: the template arrives decoded, wherever it sits.
func TestATemplateArrivesDecoded(t *testing.T) {
	for target, want := range map[string]string{
		"whatsapp://send?phone=20123456789&text=Hello%20there":        "Hello there",
		"https://wa.me/20123456789?text=Hi%2C%20how%20are%20you%3F":   "Hi, how are you?",
		"whatsapp://send?phone=20123456789&text=Caf%C3%A9%20%2B%2020": "Café + 20",
		"whatsapp://send?text=Hi&phone=20123456789":                   "Hi",
		"https://wa.me/20123456789?text=%E2%9C%93":                    "✓",
		"whatsapp://send?phone=20123456789&text=a%ZZb":                "a%ZZb",
		"whatsapp://send?phone=20123456789&text=":                     "",
		"whatsapp://send?phone=20123456789&text=%20%20":               "",
		"20123456789": "",
		"whatsapp://send?phone=20123456789&text=trailing%": "trailing%",
		"whatsapp://send?phone=20123456789&text=%C3":       "\uFFFD",
		"whatsapp://send?phone=20123456789&text=a%2Bb%20c": "a+b c",
	} {
		l, ok := parseChat(target)
		if !ok || l.text != want {
			t.Errorf("%q: %q %v, want %q", target, l.text, ok, want)
		}
	}
}

func TestGroupInviteLinksAreRecognisedAndNothingElse(t *testing.T) {
	const code = "AbCdEf1234567890XyZ"
	for _, l := range []string{"https://chat.whatsapp.com/" + code, "https://chat.whatsapp.com/invite/" + code,
		"http://CHAT.whatsapp.com/" + code + "/", "https://chat.whatsapp.com/" + code + "?utm=1"} {
		if got, ok := inviteCode(l); !ok || got != code {
			t.Errorf("%q: %q %v", l, got, ok)
		}
	}
	for _, l := range []string{"https://chat.whatsapp.com/", "https://chat.whatsapp.com/short", "https://evil.example/" + code,
		"https://chat.whatsapp.com.evil.example/" + code, "https://chat.whatsapp.com/" + code + "/extra",
		"https://chat.whatsapp.com/AbCd-Ef1234567890XyZ", "whatsapp://chat.whatsapp.com/" + code} {
		if got, ok := inviteCode(l); ok {
			t.Errorf("%q: %q", l, got)
		}
	}
}

func TestCallLinksAreRecognisedAndNothingElse(t *testing.T) {
	const token = "AbCdEf1234567890"
	for l, video := range map[string]bool{"https://call.whatsapp.com/voice/" + token: false,
		"https://call.whatsapp.com/audio/" + token + "?utm=1": false, "https://CALL.whatsapp.com/video/" + token + "/": true} {
		if got, v, ok := callLink(l); !ok || got != token || v != video {
			t.Errorf("%q: %q %v %v", l, got, v, ok)
		}
	}
	for _, l := range []string{"https://call.whatsapp.com/", "https://call.whatsapp.com/voice/", "https://call.whatsapp.com/voice/short",
		"https://call.whatsapp.com/screen/" + token, "https://call.whatsapp.com.evil.example/voice/" + token,
		"https://call.whatsapp.com/voice/" + token + "/extra", "https://call.whatsapp.com/voice/AbCd%20Ef1234567890",
		"https://chat.whatsapp.com/" + token, "whatsapp://call.whatsapp.com/voice/" + token} {
		if got, _, ok := callLink(l); ok {
			t.Errorf("%q: %q", l, got)
		}
	}
}

func TestParseLinkPicksTheKind(t *testing.T) {
	if l, ok := parseLink("https://chat.whatsapp.com/AbCdEf1234567890XyZ"); !ok || l.kind != linkInvite || l.code != "AbCdEf1234567890XyZ" {
		t.Errorf("%+v", l)
	}
	if l, ok := parseLink("https://call.whatsapp.com/video/AbCdEf1234567890"); !ok || l.kind != linkCall || !l.video {
		t.Errorf("%+v", l)
	}
	if l, ok := parseLink("wa.me/20123456789?text=Hi"); !ok || l.kind != linkChat || l.chat != chat20 || l.text != "Hi" {
		t.Errorf("%+v", l)
	}
	if _, ok := parseLink("quit"); ok {
		t.Error("a command is no link")
	}
}

func TestOnlyAPhoneJIDIsANumber(t *testing.T) {
	if n, ok := numberOfJID(chat20); !ok || n != "20123456789" {
		t.Error(n, ok)
	}
	for _, id := range []string{"", "20123456789", "12345@s.whatsapp.net", "1234567890123456@s.whatsapp.net", "notadigits@s.whatsapp.net",
		"20123456789@lid", "20123456789@s.whatsapp.net.evil"} {
		if _, ok := numberOfJID(id); ok {
			t.Errorf("%q", id)
		}
	}
}
