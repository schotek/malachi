// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package widget holds reusable widgets built from Blueprint definitions,
// plus the small pure formatting helpers they display data with.
package widget

import (
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// DisplayName is the short form of an address for lists and avatars: the
// name if the backend parsed one, otherwise the bare address. Both fields
// are attacker-controlled text; callers must show the result as plain text.
func DisplayName(a api.Address) string {
	if name := strings.TrimSpace(a.Name); name != "" {
		return name
	}
	return strings.TrimSpace(a.Address)
}

// FormatAddress is the long form: "Name <addr>", or just the address.
func FormatAddress(a api.Address) string {
	name := strings.TrimSpace(a.Name)
	addr := strings.TrimSpace(a.Address)
	switch {
	case name == "":
		return addr
	case addr == "":
		return name
	default:
		return name + " <" + addr + ">"
	}
}

// FormatParticipants joins the display names of a conversation's
// participants in the order given (newest first), each address once,
// compared case-insensitively; entries with neither name nor address are
// skipped. Plain text, like DisplayName.
func FormatParticipants(list []api.Address) string {
	seen := make(map[string]bool, len(list))
	names := make([]string, 0, len(list))
	for _, a := range list {
		key := strings.ToLower(strings.TrimSpace(a.Address))
		if key == "" {
			key = "name:" + strings.ToLower(strings.TrimSpace(a.Name))
		}
		if key == "name:" || seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, DisplayName(a))
	}
	// TRANSLATORS: put between the names of a conversation's participants ("Alice, Bob").
	return strings.Join(names, i18n.C("participant list separator", ", "))
}

// ThreadCountText is the badge of a conversation row: the member count
// from two on, nothing below.
func ThreadCountText(n int) string {
	if n < 2 {
		return ""
	}
	return fmt.Sprint(n)
}

// FormatDate renders a message date for the list, relative to now: the time
// for today, day and month for the current year, the full date otherwise.
// A zero time renders as an empty string.
func FormatDate(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	t, now = t.Local(), now.Local()
	ty, tm, td := t.Date()
	ny, nm, nd := now.Date()
	switch {
	case ty == ny && tm == nm && td == nd:
		return FormatTime(t)
	case ty == ny:
		// TRANSLATORS: strftime format for a date in the current year in
		// the message list, e.g. "2 Sep".
		// xgettext:no-c-format
		return strftime(t, i18n.T("%-d %b"))
	default:
		// TRANSLATORS: strftime format for a date in another year in the
		// message list, e.g. "2025-09-02".
		// xgettext:no-c-format
		return strftime(t, i18n.T("%Y-%m-%d"))
	}
}

// FormatTime renders a wall-clock time.
func FormatTime(t time.Time) string {
	// TRANSLATORS: strftime format for a time of day, e.g. "15:04".
	// xgettext:no-c-format
	return strftime(t, i18n.T("%H:%M"))
}

// FormatDateTime renders a full date with time (quote headers, "draft
// saved" status).
func FormatDateTime(t time.Time) string {
	// TRANSLATORS: strftime format for a full date and time, e.g.
	// "Wed, 2 Sep 2026 at 15:04".
	// xgettext:no-c-format
	return strftime(t, i18n.T("%a, %-d %b %Y at %H:%M"))
}

// FormatSize renders a byte count for attachment chips: MiB, KiB or B.
func FormatSize(n int64) string {
	switch {
	case n >= 1<<20:
		// TRANSLATORS: file size in mebibytes.
		return fmt.Sprintf(i18n.T("%.1f MiB"), float64(n)/(1<<20))
	case n >= 1<<10:
		// TRANSLATORS: file size in kibibytes.
		return fmt.Sprintf(i18n.T("%.0f KiB"), float64(n)/(1<<10))
	default:
		// TRANSLATORS: file size in bytes.
		return fmt.Sprintf(i18n.T("%d B"), n)
	}
}

// strftime formats through GLib so month and day names follow the locale.
func strftime(t time.Time, format string) string {
	dt := glib.NewDateTimeFromUnixLocal(t.Unix())
	if dt == nil {
		return t.Format(time.RFC3339)
	}
	return dt.Format(format)
}
