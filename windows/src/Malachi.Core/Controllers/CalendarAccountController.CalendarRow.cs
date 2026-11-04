// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalendarAccountController.swift
// (CalendarRow); Swift-first, no GTK controller yet (the rules are
// ui/internal/calendar/preferences.go, Cal.Prefs).

namespace Malachi.Core.Controllers;

/// <content>A calendar row of the Calendar group.</content>
public sealed partial class CalendarAccountController
{
    /// <summary>A calendar row of the Calendar group: a checkbox, a colour, a name and a note.</summary>
    public sealed record CalendarRow
    {
        /// <summary>The remote id (a CalDAV href); null when the contract has none.</summary>
        public string? RemoteId { get; init; }

        /// <summary>The calendar's name, cleaned (plain text).</summary>
        public required string Name { get; init; }

        /// <summary><c>#rrggbb</c>; "" for none.</summary>
        public required string Color { get; init; }

        /// <summary>Synchronised (not in <c>disabledCalendars</c>): the checkbox.</summary>
        public required bool Enabled { get; init; }

        /// <summary>"default" / "read only" / "shared · %s"; "" for none.</summary>
        public required string Note { get; init; }

        /// <summary>New events may go here.</summary>
        public required bool Writable { get; init; }

        /// <summary>New events go here (the configured default, else the server's).</summary>
        public bool IsDefault { get; init; }

        /// <summary>The checkbox works (a remote id is known).</summary>
        public bool CanToggle => RemoteId is not null;
    }
}
