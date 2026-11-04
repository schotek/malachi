// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the SF Symbols of macos/Sources/MalachiMail/Calendar/EventDetailViewController.swift
// (the rows' clock, repeat, mappin.and.ellipse, link, person.2, note.text,
// envelope) and EventDetailViews.swift (EventDetailAttendeeRow.symbol:
// checkmark, questionmark, xmark, arrow.right); GTK: the event detail
// follows (docs/calendar.md §6.4), so there are no GTK icon names to look
// up in Core's IconGlyphs. Windows-only: the Segoe Fluent Icons glyphs that
// stand in for them, by code point.

namespace Malachi.App.Calendars;

/// <summary>The glyphs of the event detail and the editor (Segoe Fluent Icons).</summary>
internal static class EventGlyphs
{
    /// <summary>The time row (clock: Recent).</summary>
    public const string Clock = "";

    /// <summary>The recurrence row (repeat: RepeatAll).</summary>
    public const string Repeat = "";

    /// <summary>The location row (mappin.and.ellipse: MapPin).</summary>
    public const string Location = "";

    /// <summary>The video call row (link: Link).</summary>
    public const string Link = "";

    /// <summary>The people row (person.2: People).</summary>
    public const string People = "";

    /// <summary>The notes row (note.text: QuickNote).</summary>
    public const string Notes = "";

    /// <summary>Show Invitation in Mail (envelope: Mail).</summary>
    public const string Mail = "";

    /// <summary>An attendee who accepted (checkmark: CheckMark).</summary>
    public const string Accepted = "";

    /// <summary>An attendee who has not answered, or answered tentatively (questionmark: Help).</summary>
    public const string Question = "";

    /// <summary>An attendee who declined (xmark: Cancel).</summary>
    public const string Declined = "";

    /// <summary>An attendee who delegated (arrow.right: Forward).</summary>
    public const string Delegated = "";

    /// <summary>Add Video Call (video: Video).</summary>
    public const string Video = "";
}
