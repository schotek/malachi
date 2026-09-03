// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package widget holds reusable widgets built from Blueprint definitions,
// plus the small pure formatting helpers they display data with.
package widget

import (
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
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
		return t.Format("15:04")
	case ty == ny:
		return t.Format("2 Jan")
	default:
		return t.Format("2006-01-02")
	}
}
