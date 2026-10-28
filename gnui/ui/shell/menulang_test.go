package shell

import (
	"testing"

	"gonativeui/menu"

	"zapfast/i18n"
	"zapfast/state"
)

// The menu bar follows the interface language: built in it, rebuilt (a new
// revision, for the windows to pick up) when it changes, and kept otherwise.
func TestMenuBarFollowsTheLanguage(t *testing.T) {
	i18n.SetLocale(i18n.English)
	t.Cleanup(func() { i18n.SetLocale(i18n.English) })
	sh, err := New(state.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	undo := func() string {
		c, _ := sh.Menu().Command(CmdUndo)
		return c.Title
	}
	if undo() != "Undo" || sh.Menu().Menus()[1].Title != "File" {
		t.Fatalf("English menu: %q %q", undo(), sh.Menu().Menus()[1].Title)
	}
	rev := sh.RefreshMenus()
	if sh.RefreshMenus() != rev {
		t.Error("the models were rebuilt without a language change")
	}
	i18n.SetLocale(i18n.German)
	if sh.RefreshMenus() == rev {
		t.Fatal("the language changed and the models were not rebuilt")
	}
	if undo() != "Widerrufen" || sh.Menu().Menus()[1].Title != "Ablage" {
		t.Errorf("German menu: %q %q", undo(), sh.Menu().Menus()[1].Title)
	}
	if c, _ := sh.MenuOf(state.WinViewer).Command(CmdMinimize); c.Title != "Minimieren" {
		t.Errorf("viewer menu: Minimize is %q", c.Title)
	}
	for _, m := range []*menu.Model{sh.Menu(), sh.MenuOf(state.WinViewer)} {
		for role, want := range map[menu.Role]string{
			menu.RoleAbout: "Über „Zap“", menu.RoleQuit: "„Zap“ beenden",
			menu.RoleHide: "„Zap“ ausblenden", menu.RoleHideOthers: "Andere ausblenden",
			menu.RoleShowAll: "Alle einblenden",
		} {
			got := ""
			for _, cmd := range m.Commands() {
				if cmd.Role == role {
					got = cmd.Title
				}
			}
			if got != want {
				t.Errorf("role %v: %q, want %q", role, got, want)
			}
		}
		services := ""
		for _, entry := range m.Menus() {
			if entry.Role == menu.MenuServices {
				services = entry.Title
			}
		}
		if services != "Dienste" {
			t.Errorf("Services: %q, want Dienste", services)
		}
	}
}
