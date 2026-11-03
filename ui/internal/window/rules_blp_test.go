// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The rules' Blueprint files are looked up by id from Go and carry no
// translatable string: every text of the editor and of the page comes from
// ui/internal/rules (the catalogue was written for it), so a `_("…")` in
// them would be a msgid that no catalogue has.
func TestRulesBlueprintIDsAndTexts(t *testing.T) {
	read := func(path string) string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	get := regexp.MustCompile(`(?:get|b\.GetObject)\("([a-z_]+)"\)`)
	for _, c := range []struct{ blp, goFile string }{
		{"../../data/ui/rules_editor.blp", "rules_editor.go"},
		{"../../data/ui/preferences.blp", "preferences_rules.go"},
	} {
		blp := read(c.blp)
		for _, m := range get.FindAllStringSubmatch(read(c.goFile), -1) {
			if !strings.Contains(blp, " "+m[1]+" {") && !strings.Contains(blp, " "+m[1]+" ") {
				t.Errorf("%s has no %s", c.blp, m[1])
			}
		}
	}
	if strings.Contains(read("../../data/ui/rules_editor.blp"), "_(") {
		t.Error("rules_editor.blp has a translatable string")
	}
	prefs := read("../../data/ui/preferences.blp")
	start := strings.Index(prefs, "Adw.PreferencesPage rules_page")
	end := strings.Index(prefs, "Adw.PreferencesPage general_page")
	if start < 0 || end < start {
		t.Fatal("the Rules page is not between Integrations and General")
	}
	if strings.Contains(prefs[start:end], "_(") {
		t.Error("the Rules page has a translatable string")
	}
	if i := strings.Index(prefs, "Adw.PreferencesPage integrations_page"); i < 0 || i > start {
		t.Error("the Rules page does not follow Integrations")
	}
}
