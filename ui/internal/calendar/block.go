// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import "github.com/schotek/malachi/backend/pkg/api"

// What a block of the time grid, a bar of the all-day row or a pill shows
// of a server's strings (docs/calendar.md §6.5 G11): cleaned like every
// other line (control, bidi and invisible characters out, one line,
// bounded), the GTK port of macOS CalendarText.

// BlockTitle is an occurrence's title as a block shows it, Untitled
// Event when empty.
func BlockTitle(o api.Occurrence, tr Translator) string { return cleanTitle(o.Title, tr) }

// BlockPlace is an occurrence's location as the Day view's block shows it
// ("" none).
func BlockPlace(o api.Occurrence) string { return cleanLine(o.Location) }
