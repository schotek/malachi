// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package i18n

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocaleDir(t *testing.T) {
	t.Setenv(LocaleDirEnv, "/tmp/x")
	if got := LocaleDir("/compiled"); got != "/tmp/x" {
		t.Errorf("env override: %s", got)
	}
	t.Setenv(LocaleDirEnv, "")
	if got := LocaleDir("/compiled"); got != "/compiled" {
		t.Errorf("compiled: %s", got)
	}
	if got := LocaleDir(""); !strings.HasSuffix(got, "/share/locale") {
		t.Errorf("fallback: %s", got)
	}
}

// Without a bound domain gettext returns the msgid, so the helpers must be
// usable (and deterministic) in tests that never call Init.
func TestUnboundReturnsMsgid(t *testing.T) {
	if T("Hello") != "Hello" {
		t.Error("T")
	}
	if N("one", "many", 1) != "one" || N("one", "many", 2) != "many" {
		t.Error("N")
	}
	if C("ctx", "Hello") != "Hello" {
		t.Error("C")
	}
}

// TestCzechCatalogue binds the real catalogue compiled by `make locale` and
// checks a translation end to end through the Go binding. Skipped when the
// catalogue or the locale is missing.
func TestCzechCatalogue(t *testing.T) {
	dir, err := filepath.Abs("../../../build/locale")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cs", "LC_MESSAGES", Domain+".mo")); err != nil {
		t.Skip("run `make locale` first")
	}
	t.Setenv("LC_ALL", "cs_CZ.UTF-8")
	t.Setenv("LANGUAGE", "cs")
	Init(dir)
	if got := T("Connected"); got != "Připojeno" {
		t.Skipf("locale cs_CZ.UTF-8 not usable here (got %q)", got)
	}
	if got := N("%d unsafe element was removed from the message", "%d unsafe elements were removed from the message", 3); got != "%d nebezpečné prvky byly ze zprávy odstraněny" {
		t.Errorf("plural form 2-4: %q", got)
	}
}
