// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalDAVWizardController.swift
// (ListedCalendar); Swift-first, no GTK controller yet (the rules are
// ui/internal/calendar/caldav.go, Cal.CalDAVWizard).

namespace Malachi.Core.Controllers;

/// <content>The found calendars as the sheet lists them.</content>
public sealed partial class CalDAVWizardController
{
    /// <summary>A found calendar as the sheet lists it: a checkbox, its colour, its name and its note.</summary>
    public sealed record ListedCalendar
    {
        /// <summary>The collection's href (the key of <see cref="Toggle"/>).</summary>
        public required string Href { get; init; }

        /// <summary>Its name, cleaned (plain text).</summary>
        public required string Name { get; init; }

        /// <summary><c>#rrggbb</c>; "" for none.</summary>
        public required string Color { get; init; }

        /// <summary>"default" / "read only" (<see cref="Calendars.Cal.CalDAVWizard.ListedNote"/>); "" for none.</summary>
        public required string Note { get; init; }

        /// <summary>The checkbox: the calendar is to be synchronised.</summary>
        public required bool Selected { get; init; }
    }
}
