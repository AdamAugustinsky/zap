package main

import (
	"os"
	"path/filepath"
	"testing"

	"zapfast/i18n"
	"zapfast/state"
)

// settingsChoosing reads a settings file whose interface language is lang
// (absent when empty) from a folder of its own, as main does.
func settingsChoosing(t *testing.T, lang string) *state.Loading {
	t.Helper()
	dir := t.TempDir()
	body := `{}`
	if lang != "" {
		body = `{"interface_language":"` + lang + `"}`
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return state.LoadSettings(state.Paths{Config: dir, Data: dir})
}

// start is startState with the system preferring langs, put back afterwards.
func start(t *testing.T, loading *state.Loading, scripted bool, langs ...string) *state.State {
	t.Helper()
	t.Cleanup(i18n.SetSystemLanguages(func() []string { return langs }))
	t.Cleanup(func() { i18n.SetLocale(i18n.English) })
	var reader settingsReader
	if loading != nil {
		reader = loading
	}
	s := startState(reader, scripted, func(*settingsStartup) error {
		t.Fatal("fast settings opened a startup window")
		return nil
	})
	t.Cleanup(s.CloseSettings)
	return s
}

// The saved interface language is the one the app starts in, whatever the
// system prefers (zf:src/app.rs:1276); a scripted run stays in English.
func TestASavedLanguageAppliesAtStart(t *testing.T) {
	s := start(t, settingsChoosing(t, "de"), false, "fr-FR")
	if string(s.Settings.InterfaceLanguage) != "de" || i18n.Current() != i18n.German || i18n.T("Settings") != "Einstellungen" {
		t.Errorf("saved %q: started in %v (%q)", s.Settings.InterfaceLanguage, i18n.Current(), i18n.T("Settings"))
	}
	start(t, settingsChoosing(t, "de"), true, "fr-FR")
	if i18n.Current() != i18n.English || i18n.T("Settings") != "Settings" {
		t.Errorf("a scripted run started in %v", i18n.Current())
	}
}

// With no choice saved, or "system", the app starts in the first language
// the system prefers that it has, else English; the demo (no settings files)
// too.
func TestAnUnsetLanguageFollowsTheSystem(t *testing.T) {
	for _, c := range []struct {
		name    string
		loading func(t *testing.T) *state.Loading
	}{
		{"no choice", func(t *testing.T) *state.Loading { return settingsChoosing(t, "") }},
		{"system", func(t *testing.T) *state.Loading { return settingsChoosing(t, "system") }},
		{"the demo", func(*testing.T) *state.Loading { return nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			start(t, c.loading(t), false, "nb-NO", "de-DE", "fr-FR")
			if i18n.Current() != i18n.German || i18n.T("Settings") != "Einstellungen" {
				t.Errorf("the system prefers German: started in %v (%q)", i18n.Current(), i18n.T("Settings"))
			}
			start(t, c.loading(t), false, "ko-KR")
			if i18n.Current() != i18n.English {
				t.Errorf("no language the app has: started in %v", i18n.Current())
			}
		})
	}
}
