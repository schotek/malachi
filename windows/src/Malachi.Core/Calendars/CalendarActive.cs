// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/Calendar.swift (activeCalendars);
// GTK: ui/internal/calendar/calendars.go (ActiveCalendars).
//
// The calendars a client shows (docs/calendar.md §5.2): calendar.list lists
// the disabled ones too (unchecked in Preferences → Calendar), and only
// Preferences shows those, so that their checkbox can switch them back on.

using System.Collections.Generic;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>
    /// calendars.go <c>ActiveCalendars</c>: the calendars a client shows and
    /// offers: every one of calendar.list that is not
    /// <see cref="Api.Calendar.Disabled"/> (in its account's
    /// disabledCalendars), in the order given; never null. It applies
    /// wherever calendars appear outside Preferences: the sidebar's rows and
    /// the set of visible calendars, the editor's Calendar popup, the
    /// detail's calendar name, the free/busy and search scopes, and the
    /// counts. Only Preferences → Calendar lists the disabled ones.
    /// </summary>
    public static IReadOnlyList<Api.Calendar> ActiveCalendars(IReadOnlyList<Api.Calendar>? calendars)
    {
        var output = new List<Api.Calendar>(calendars?.Count ?? 0);
        foreach (var c in calendars ?? [])
        {
            if (c is not null && c.Disabled != true)
            {
                output.Add(c);
            }
        }
        return output;
    }
}
