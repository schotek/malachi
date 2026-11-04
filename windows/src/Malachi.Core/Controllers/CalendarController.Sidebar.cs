// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalendarController.swift
// (CalendarController.Filter, SidebarCalendar, SidebarAccount, Sidebar);
// Swift-first, no GTK counterpart yet (docs/calendar.md §5.3–5.4).
//
// What the calendar page's content filters by and the sidebar's model the
// page lays out. Windows: a nested type cannot share its name with the
// property that holds it, so Swift's Filter is ContentFilter (the property
// Filter) and its Sidebar is SidebarModel (the property Sidebar); the
// models compare their lists by content, as Swift's Equatable structs do.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;

namespace Malachi.Core.Controllers;

public sealed partial class CalendarController
{
    /// <summary>What the content shows besides the view (Swift <c>Filter</c>).</summary>
    public enum ContentFilter
    {
        /// <summary>The view.</summary>
        None,

        /// <summary>The unanswered invitations as an agenda (the sidebar's INVITATIONS row).</summary>
        Unanswered,
    }

    /// <summary>A calendar row of the sidebar.</summary>
    /// <param name="Id">The calendar.</param>
    /// <param name="Name">Its name, cleaned.</param>
    /// <param name="Color">"#rrggbb".</param>
    /// <param name="Visible">Its checkbox.</param>
    /// <param name="Note">"default" / "read only" / "shared · %s" (<see cref="Cal.Prefs.CalendarRowNote(Api.Calendar)"/>).</param>
    public sealed record SidebarCalendar(CalendarId Id, string Name, string Color, bool Visible, string Note);

    /// <summary>
    /// An account of the sidebar: its header (the view picks the capsule with
    /// <c>FolderTree.AccountHeaderBadge(Account)</c>), its calendars and the
    /// state row.
    /// </summary>
    public sealed record SidebarAccount
    {
        /// <summary>The account.</summary>
        public required AccountId AccountId { get; init; }

        /// <summary>Its record of account.list; null while unknown.</summary>
        public Account? Account { get; init; }

        /// <summary>Its name, cleaned.</summary>
        public required string Name { get; init; }

        /// <summary>Where its calendars come from.</summary>
        public required CalendarSource Source { get; init; }

        /// <summary>Its active calendars.</summary>
        public IReadOnlyList<SidebarCalendar> Calendars { get; init => field = value ?? []; } = [];

        /// <summary>The consent or state row ("" none).</summary>
        public string StateLine { get; init; } = "";

        /// <summary>The state row offers Sign In… (<see cref="SignIn"/>).</summary>
        public bool OffersSignIn { get; init; }

        /// <summary>Whether both hold the same, the calendars compared in order.</summary>
        public bool Equals(SidebarAccount? other) =>
            other is not null && AccountId == other.AccountId && Account == other.Account && Name == other.Name
            && Source == other.Source && Board.SameList(Calendars, other.Calendars) && StateLine == other.StateLine
            && OffersSignIn == other.OffersSignIn;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(AccountId, Name, Source, Calendars.Count, StateLine, OffersSignIn);
    }

    /// <summary>The sidebar's model (Swift <c>Sidebar</c>).</summary>
    public sealed record SidebarModel
    {
        /// <summary>The mini month.</summary>
        public required Cal.MiniMonth MiniMonth { get; init; }

        /// <summary>The INVITATIONS heading.</summary>
        public required string InvitationsTitle { get; init; }

        /// <summary>Its "Unanswered" row.</summary>
        public required string UnansweredTitle { get; init; }

        /// <summary>That row's count badge (0: no badge).</summary>
        public int Unanswered { get; init; }

        /// <summary>The accounts with their calendars.</summary>
        public IReadOnlyList<SidebarAccount> Accounts { get; init => field = value ?? []; } = [];

        /// <summary>"Add Calendar…".</summary>
        public required string AddCalendar { get; init; }

        /// <summary>Its menu item for a CalDAV calendar.</summary>
        public required string AddCalDAV { get; init; }

        /// <summary>Its menu item for a Microsoft 365 account.</summary>
        public required string SyncMicrosoft { get; init; }

        /// <summary>Whether both hold the same, the accounts compared in order.</summary>
        public bool Equals(SidebarModel? other) =>
            other is not null && MiniMonth == other.MiniMonth && InvitationsTitle == other.InvitationsTitle
            && UnansweredTitle == other.UnansweredTitle && Unanswered == other.Unanswered && Board.SameList(Accounts, other.Accounts)
            && AddCalendar == other.AddCalendar && AddCalDAV == other.AddCalDAV && SyncMicrosoft == other.SyncMicrosoft;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(InvitationsTitle, Unanswered, Accounts.Count);
    }
}
