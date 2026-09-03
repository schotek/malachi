// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package i18n is the UI's gettext binding. Every string shown to the user
// from Go goes through T, N or C; Blueprint files use _("…") and the same
// domain. The backend is deliberately language-neutral (codes and English
// technical messages only): localisation is entirely the desktop
// application's job.
//
// Rules: never build sentences by concatenation, use fmt.Sprintf(T("… %s
// …"), x) so translators can reorder; put a "// TRANSLATORS:" comment above
// ambiguous msgids; date formats are strftime msgids of their own.
package i18n

import (
	"os"
	"path/filepath"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// Domain is the gettext text domain; it must match the gschema's
// gettext-domain and the installed .mo file name.
const Domain = "malachi"

// LocaleDirEnv overrides the compiled-in locale directory (used by
// scripts/dev-run.sh for uninstalled runs).
const LocaleDirEnv = "MALACHI_LOCALE_DIR"

// Init binds the domain to localeDir and sets the process locale from the
// environment. Call it before any widget is built.
func Init(localeDir string) {
	coreglib.InitI18n(Domain, localeDir)
}

// LocaleDir picks the locale directory: the environment override, then the
// compiled-in path (-X main.localeDir), then <executable>/../share/locale.
func LocaleDir(compiled string) string {
	if dir := os.Getenv(LocaleDirEnv); dir != "" {
		return dir
	}
	if compiled != "" {
		return compiled
	}
	exe, err := os.Executable()
	if err != nil {
		return "/usr/share/locale"
	}
	return filepath.Join(filepath.Dir(exe), "..", "share", "locale")
}

// T translates msgid.
func T(msgid string) string {
	return glib.Dgettext(Domain, msgid)
}

// N translates a plural form for n.
func N(singular, plural string, n int) string {
	if n < 0 {
		n = -n
	}
	return glib.Dngettext(Domain, singular, plural, uint32(n))
}

// C translates msgid disambiguated by context.
func C(context, msgid string) string {
	return glib.Dpgettext2(Domain, context, msgid)
}
