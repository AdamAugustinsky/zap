package i18n

import (
	"encoding/json"
	"testing"
)

func TestSystemLocalesMapToTheRightCatalog(t *testing.T) {
	for tag, want := range map[string]Locale{
		"pt-BR": PortugueseBrazil, "pt_PT": PortugueseBrazil, "pt_BR.UTF-8": PortugueseBrazil,
		"de-DE": German, "es": Spanish, "it-IT": Italian, "fr-FR": French, "ru-RU": Russian,
		"zh-Hans": ChineseSimplified, "zh": ChineseSimplified, "zh_CN.GB2312": ChineseSimplified,
		// A script the tag names itself decides, even beside a region whose own
		// default is the other one.
		"zh-Hans-TW": ChineseSimplified, "zh-Hans-HK": ChineseSimplified, "zh-Hans-CN": ChineseSimplified,
		"zh-Hant-CN": ChineseTraditional, "zh-TW": ChineseTraditional, "zh-Hant": ChineseTraditional,
		"zh-HK": ChineseTraditional, "zh-MO": ChineseTraditional, "zh-CHT": ChineseTraditional,
		"zh_TW.UTF-8": ChineseTraditional, // macOS and Linux for a Traditional system
		"tr-TR":       Turkish, "tr": Turkish, "tr_TR.UTF-8": Turkish,
		"id-ID": Indonesian, "id": Indonesian, "en-US": English,
	} {
		if got, ok := FromSystem(tag); !ok || got != want {
			t.Errorf("FromSystem(%q) = %v, %v; want %v", tag, got, ok, want)
		}
	}
	for _, tag := range []string{"ja-JP", "", "-", ".UTF-8", "C", "POSIX"} {
		if _, ok := FromSystem(tag); ok {
			t.Errorf("FromSystem(%q) found a locale", tag)
		}
	}
	if Locale(0) != English {
		t.Error("the zero locale is not English")
	}
}

func TestEveryLocaleHasATagLabelAndCatalog(t *testing.T) {
	if len(All) != 11 {
		t.Fatalf("%d locales", len(All))
	}
	for _, l := range All {
		if got, ok := ParseTag(l.Tag()); !ok || got != l {
			t.Errorf("%v does not round-trip its tag", l)
		}
		if l.Label() == "" {
			t.Errorf("%v has no label", l)
		}
		if _, ok := catalogs[l.Tag()]; ok == (l == English) {
			t.Errorf("%v: catalog present = %v", l, ok)
		}
	}
	b, _ := json.Marshal(map[string]Locale{"l": PortugueseBrazil})
	if string(b) != `{"l":"pt-BR"}` {
		t.Errorf("json: %s", b)
	}
	var back struct{ L Locale }
	if err := json.Unmarshal([]byte(`{"L":"zh-Hant"}`), &back); err != nil || back.L != ChineseTraditional {
		t.Errorf("unmarshal: %v %v", back, err)
	}
	if PortugueseBrazil.Label() != "Português (Brasil)" || ChineseTraditional.Label() != "繁體中文" {
		t.Error("labels")
	}
}

// Every preferred language is tried in order, not only the first.
func TestTheFirstSupportedPreferredLanguageWins(t *testing.T) {
	defer func(f func() []string) { detectLanguages = f }(detectLanguages)
	detectLanguages = func() []string { return []string{"nb-NO", "de-DE", "fr-FR"} }
	if got := Detect(); got != German {
		t.Errorf("Detect = %v", got)
	}
	detectLanguages = func() []string { return []string{"ko-KR"} }
	if got := Detect(); got != English {
		t.Errorf("Detect = %v", got)
	}
	detectLanguages = func() []string { return []string{"tr_TR.UTF-8"} }
	if got := Resolve("pt-BR"); got != PortugueseBrazil {
		t.Errorf("an explicit choice must win, got %v", got)
	}
	if got := Resolve(""); got != Turkish {
		t.Errorf("Resolve(\"\") = %v", got)
	}
	if got := Resolve("xx"); got != Turkish {
		t.Errorf("Resolve(unknown) = %v", got)
	}
}

func TestDetectReadsTheSystemWithoutPanicking(t *testing.T) {
	_ = Detect()
}

func TestUnknownKeysFallBackToTheEnglishSource(t *testing.T) {
	const missing = "A string nobody has translated"
	for _, c := range []struct {
		l          Locale
		id, expect string
	}{
		{PortugueseBrazil, "Chats", "Conversas"},
		{English, "Chats", "Chats"},
		{PortugueseBrazil, missing, missing},
		{German, missing, missing},
	} {
		if got := TIn(c.l, c.id); got != c.expect {
			t.Errorf("%v %q = %q, want %q", c.l, c.id, got, c.expect)
		}
	}
}

func TestPrivateReplyLabelsUseTheirOwnCatalogEntries(t *testing.T) {
	for l, want := range map[Locale][2]string{
		German:             {"Privat antworten", "Die ursprüngliche Gruppennachricht ist nicht verfügbar"},
		Spanish:            {"Responder en privado", "El mensaje original del grupo no está disponible"},
		French:             {"Répondre en privé", "Le message original du groupe est indisponible"},
		Italian:            {"Rispondi in privato", "Il messaggio originale del gruppo non è disponibile"},
		PortugueseBrazil:   {"Responder em particular", "Mensagem original do grupo indisponível"},
		Russian:            {"Ответить лично", "Исходное сообщение группы недоступно"},
		ChineseSimplified:  {"私下回复", "原始群组消息不可用"},
		ChineseTraditional: {"私下回覆", "無法取得原始群組訊息"},
		Indonesian:         {"Balas secara pribadi", "Pesan grup asli tidak tersedia"},
		Turkish:            {"Özel olarak yanıtla", "Orijinal grup mesajı kullanılamıyor"},
	} {
		if got := TIn(l, "Reply privately"); got != want[0] {
			t.Errorf("%v: %q", l, got)
		}
		if got := TIn(l, "Original group message unavailable"); got != want[1] {
			t.Errorf("%v: %q", l, got)
		}
	}
}

func TestPortugueseSelectionLabels(t *testing.T) {
	if got := TIn(PortugueseBrazil, "Select messages"); got != "Selecionar mensagens" {
		t.Error(got)
	}
	got := TIn(PortugueseBrazil, "Select message from {sender}, {time}: {summary}")
	if got != "Selecionar mensagem de {sender} ({time}): {summary}" {
		t.Error(got)
	}
}

func TestPilotCatalogsTranslate(t *testing.T) {
	for _, c := range []struct {
		l          Locale
		id, expect string
	}{
		{German, "Chats", "Chats"}, {German, "Search", "Suchen"}, {German, "Unread", "Ungelesen"},
		{German, "Type a message", "Nachricht eingeben"}, {German, "Settings", "Einstellungen"}, {German, "Monday", "Montag"},
		{Spanish, "Search", "Buscar"}, {French, "Search", "Rechercher"}, {Italian, "Search", "Cerca"},
		{Russian, "Search", "Поиск"}, {Spanish, "Unread", "No leídos"}, {French, "Settings", "Paramètres"},
		{Italian, "Type a message", "Scrivi un messaggio"}, {Russian, "Today", "Сегодня"},
		{ChineseSimplified, "Chats", "聊天"}, {ChineseSimplified, "Search", "搜索"}, {ChineseSimplified, "Settings", "设置"},
		{ChineseSimplified, "Type a message", "输入消息"}, {ChineseSimplified, "Monday", "周一"}, {ChineseSimplified, "Yesterday", "昨天"},
		{ChineseTraditional, "Chats", "對話"}, {ChineseTraditional, "Search", "搜尋"}, {ChineseTraditional, "Settings", "設定"},
		{ChineseTraditional, "Type a message", "輸入訊息"}, {ChineseTraditional, "Monday", "週一"}, {ChineseTraditional, "Yesterday", "昨天"},
		{Turkish, "Chats", "Sohbetler"}, {Turkish, "Search", "Ara"}, {Turkish, "Settings", "Ayarlar"},
		{Turkish, "Type a message", "Bir mesaj yazın"}, {Turkish, "Monday", "Pazartesi"}, {Turkish, "Yesterday", "Dün"},
		{Indonesian, "Chats", "Pesan"}, {Indonesian, "Search", "Cari"}, {Indonesian, "Settings", "Pengaturan"},
		{Indonesian, "Type a message", "Ketik pesan"}, {Indonesian, "Monday", "Senin"}, {Indonesian, "Yesterday", "Kemarin"},
	} {
		if got := TIn(c.l, c.id); got != c.expect {
			t.Errorf("%v %q = %q, want %q", c.l, c.id, got, c.expect)
		}
	}
}

func TestPluralRules(t *testing.T) {
	const one, many = "{} member", "{} members"
	type row struct {
		l    Locale
		n    int
		want string
	}
	for _, r := range []row{
		{German, 1, "{} Mitglied"}, {German, 2, "{} Mitglieder"},
		{German, 0, "{} Mitglieder"}, // German treats zero as plural
		{Russian, 1, "{} участник"}, {Russian, 3, "{} участника"}, {Russian, 5, "{} участников"},
		{Russian, 21, "{} участник"}, {Russian, 11, "{} участников"}, {Russian, 12, "{} участников"},
		{Russian, 22, "{} участника"}, {Russian, 0, "{} участников"}, {Russian, 111, "{} участников"},
		{PortugueseBrazil, 1, "{} membro"}, {PortugueseBrazil, 2, "{} membros"}, {PortugueseBrazil, 5, "{} membros"},
		{Turkish, 1, "{} üye"}, {Turkish, 2, "{} üye"}, {Turkish, 5, "{} üye"},
		{Indonesian, 1, "{} anggota"}, {Indonesian, 2, "{} anggota"}, {Indonesian, 5, "{} anggota"},
		// Without a catalog the bare English count rule applies.
		{English, 1, one}, {English, 2, many}, {English, 0, many},
	} {
		if got := NIn(r.l, one, many, r.n); got != r.want {
			t.Errorf("%v %d: %q, want %q", r.l, r.n, got, r.want)
		}
	}
	// Chinese has a single plural form, so one text covers every count.
	for l, want := range map[Locale]string{ChineseSimplified: "{} 位成员", ChineseTraditional: "{} 位成員"} {
		for _, n := range []int{0, 1, 2, 21} {
			if got := NIn(l, one, many, n); got != want {
				t.Errorf("%v %d: %q", l, n, got)
			}
		}
	}
	// French counts zero as singular.
	if a, b := NIn(French, one, many, 0), NIn(French, one, many, 1); a != b {
		t.Errorf("fr: 0 -> %q, 1 -> %q", a, b)
	}
	// A plural nobody translated uses English rules.
	if got := NIn(Russian, "{} aardvark", "{} aardvarks", 2); got != "{} aardvarks" {
		t.Error(got)
	}
}

func TestContextualLookupsStaySeparateFromPlainOnes(t *testing.T) {
	// An unknown context is distinct from T and still falls back to the source.
	if got := PIn(PortugueseBrazil, "verb", "Chats"); got != "Chats" {
		t.Error(got)
	}
	if got := PIn(English, "verb", "Chats"); got != "Chats" {
		t.Error(got)
	}
	if got := TIn(PortugueseBrazil, "Chats"); got != "Conversas" {
		t.Error(got)
	}
	// The mention-everyone word uses its own context.
	if got := PIn(Russian, "mention", "all"); got != "все" {
		t.Error(got)
	}
	if got := TIn(Russian, "all"); got != "all" {
		t.Error(got)
	}
	for _, c := range []struct {
		l               Locale
		ctx, id, expect string
	}{
		{ChineseSimplified, "", "About", "关于"}, {ChineseSimplified, "privacy", "About", "个人简介"},
		{ChineseSimplified, "profile", "About", "个人简介"}, {ChineseSimplified, "", "Groups", "群组"},
		{ChineseSimplified, "sound", "None", "无"},
		{ChineseTraditional, "", "About", "關於"}, {ChineseTraditional, "privacy", "About", "個人簡介"},
		{ChineseTraditional, "", "Groups", "群組"}, {ChineseTraditional, "sound", "None", "無"},
		// The wallpaper section's "Theme" is a colour choice, not the interface theme.
		{ChineseTraditional, "", "Theme", "主題"}, {ChineseTraditional, "wallpaper colour", "Theme", "桌布主題"},
		{Turkish, "", "About", "Hakkımda"}, {Turkish, "privacy", "About", "Hakkımda"},
		{Turkish, "", "Groups", "Gruplar"}, {Turkish, "sound", "None", "Yok"}, {Turkish, "", "None", "None"},
	} {
		if got := PIn(c.l, c.ctx, c.id); got != c.expect {
			t.Errorf("%v [%s] %q = %q, want %q", c.l, c.ctx, c.id, got, c.expect)
		}
	}
}

func TestSetLocaleSwitchesTAndFriends(t *testing.T) {
	defer SetLocale(English)
	if T("Chats") != "Chats" || Current() != English {
		t.Fatal("not English by default")
	}
	SetLocale(PortugueseBrazil)
	if T("Chats") != "Conversas" || Current() != PortugueseBrazil {
		t.Error("T after SetLocale")
	}
	if N("{} member", "{} members", 2) != "{} membros" || P("verb", "Chats") != "Chats" {
		t.Error("N or P after SetLocale")
	}
	SetLocale(English)
	if T("Chats") != "Chats" {
		t.Error("back to English")
	}
}

func TestFormat(t *testing.T) {
	got := Format(N("{} unread message", "{} unread messages", 3), "{}", "3")
	if got != "3 unread messages" {
		t.Error(got)
	}
	got = Format("Match {index} of {count}: {text}", "{index}", "2", "{count}", "9", "{text}", "hi {count}")
	if got != "Match 2 of 9: hi {count}" {
		t.Error(got) // in order, as chained replace: {text} is last, so its value is left alone
	}
	if got := Format("{a}{b}", "{a}", "{b}", "{b}", "x"); got != "xx" {
		t.Error(got) // a value is searched by the later pairs
	}
	if got := Format("no placeholders"); got != "no placeholders" {
		t.Error(got)
	}
}

func TestLookupAllocatesNothing(t *testing.T) {
	SetLocale(Russian)
	defer SetLocale(English)
	var sink string
	allocs := testing.AllocsPerRun(100, func() {
		sink = T("Search")
		sink = P("mention", "all")
		sink = N("{} member", "{} members", 21)
		sink = T("not translated")
	})
	_ = sink
	if allocs != 0 {
		t.Errorf("%v allocations per lookup round", allocs)
	}
}

// Every entry of every table is sorted, unique and non-empty, which find relies on.
func TestTablesAreSortedAndComplete(t *testing.T) {
	for tag, c := range catalogs {
		c.once.Do(c.unpack)
		if len(c.entries) == 0 {
			t.Errorf("%s: an empty table", tag)
		}
		for i, e := range c.entries {
			if e.id == "" || len(e.forms) == 0 {
				t.Errorf("%s: empty entry %d", tag, i)
			}
			for _, f := range e.forms {
				if f == "" {
					t.Errorf("%s: %q has an empty form", tag, e.id)
				}
			}
			if i > 0 {
				p := c.entries[i-1]
				if p.ctx > e.ctx || (p.ctx == e.ctx && p.id >= e.id) {
					t.Errorf("%s: %q before %q", tag, p.id, e.id)
				}
			}
			if got := c.find(e.ctx, e.id); len(got) == 0 || got[0] != e.forms[0] {
				t.Errorf("%s: cannot find %q", tag, e.id)
			}
		}
	}
}
