// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// FormatWeekday is the abbreviated weekday name in the locale ("Thu";
// strftime "%a"), for ui/internal/board.Env.Dates (Dates.Weekday): the
// board's remind presets name the day a remind falls on. Through GLib, like
// format.go's dates, so month and day names follow the locale; format.go's
// own strftime helper is unexported, hence this small duplicate rather than
// editing that file.
func FormatWeekday(t time.Time) string {
	dt := glib.NewDateTimeFromUnixLocal(t.Unix())
	if dt == nil {
		return t.Format("Mon")
	}
	// xgettext:no-c-format
	return dt.Format("%a")
}
