// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarSource.swift (Cal.Phase,
// Cal.Snapshot, Cal.SourceChange, Cal.SourceError, Cal.errorMessage,
// rangeKey, eventKey, rangesOverlap); Swift-first, no GTK counterpart yet
// (docs/calendar.md §5.2 "Source seam", "As built (task M3)").
//
// Where the calendar's data comes from: the controllers read a snapshot
// (the accounts with their calendars and states, the unanswered
// invitations, the preferences), ask for the occurrences of a range and for
// single events, and write through the source (ICalendarDataSource); they
// are told what changed through Observe, with a SourceChange. The daemon's
// source caches the occurrences per range and the events by id and drops
// them when notify.calendarChanged names an overlapping range; the
// in-memory one (InMemoryCalendarSource) holds the invented samples of
// MALACHI_CALENDAR_SAMPLES, or what a test puts in.
//
// Windows: Swift's enum SourceChange with associated values is a closed
// record hierarchy whose cases are named for what changed (SnapshotChanged,
// OccurrencesChanged, EventChanged, AlertChanged, Failed), because Event and
// Error are Visual Basic keywords (CA1716) and a case Snapshot would hide
// Cal.Snapshot inside it. Swift's SourceError is an exception (Kind
// Preview); Cal.SourceError.Preview makes one. The snapshot's lookups are
// FindAccount, FindCalendarAccount and FindCalendar (the names Account and
// Calendar are the API's types), and its lists compare by content, as
// Swift's Equatable struct does.

using System;
using System.Collections.Generic;
using System.Diagnostics.CodeAnalysis;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Text;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>How far a source's data is.</summary>
    public enum Phase
    {
        /// <summary>Nothing has arrived yet.</summary>
        Loading,

        /// <summary>The snapshot holds the daemon's (or the samples') data.</summary>
        Ready,

        /// <summary>No connection; the last snapshot stays.</summary>
        Unavailable,

        /// <summary>A load failed; the last snapshot stays.</summary>
        Failed,

        /// <summary>A daemon without the calendar (methodNotFound).</summary>
        Unsupported,
    }

    /// <summary>
    /// What the calendar shows outside a range: the accounts and their
    /// calendars, the unanswered invitations and the preferences.
    /// </summary>
    public sealed record Snapshot
    {
        /// <summary>Nothing yet: <see cref="Phase.Loading"/>, no account, the default preferences.</summary>
        public static Snapshot Empty { get; } = new();

        /// <summary>How far the data is.</summary>
        public Phase Phase { get; init; } = Phase.Loading;

        /// <summary>
        /// account.list: every account of the daemon (names, kinds, the
        /// calendar settings of <c>config.calendar</c>).
        /// </summary>
        public IReadOnlyList<Account> Accounts { get; init => field = value ?? []; } = [];

        /// <summary>
        /// calendar.list: the accounts with the calendar capability, in the
        /// accounts' order, with their calendars (the disabled ones too) and
        /// states.
        /// </summary>
        public IReadOnlyList<CalendarAccount> CalendarAccounts { get; init => field = value ?? []; } = [];

        /// <summary>calendar.list <c>unanswered</c>.</summary>
        public int Unanswered { get; init; }

        /// <summary>invitation.list with state unanswered.</summary>
        public IReadOnlyList<InvitationListItem> Invitations { get; init => field = value ?? []; } = [];

        /// <summary>calendar.preferences.</summary>
        public Api.CalendarPreferences Preferences { get; init => field = value ?? DefaultPreferences; } = DefaultPreferences;

        /// <summary>The account <paramref name="id"/> (Swift <c>account(_:)</c>); null for an unknown one.</summary>
        public Account? FindAccount(AccountId id) => Accounts.FirstOrDefault(a => a.Id == id);

        /// <summary>The calendar account <paramref name="id"/> with every calendar, the disabled ones too (Swift <c>calendarAccount(_:)</c>).</summary>
        public CalendarAccount? FindCalendarAccount(AccountId id) => CalendarAccounts.FirstOrDefault(a => a.AccountId == id);

        /// <summary>
        /// The calendar accounts with only their active calendars
        /// (<see cref="ActiveCalendars"/>): what every place but Preferences →
        /// Calendar shows. <see cref="CalendarAccounts"/> and
        /// <see cref="FindCalendarAccount"/> keep the disabled ones for
        /// Preferences.
        /// </summary>
        public IReadOnlyList<CalendarAccount> ActiveCalendarAccounts =>
            [.. CalendarAccounts.Select(a => a with { Calendars = ActiveCalendars(a.Calendars) })];

        /// <summary>An active calendar (Swift <c>calendar(_:)</c>); null for an unknown or disabled one.</summary>
        public Api.Calendar? FindCalendar(CalendarId id)
        {
            foreach (var a in CalendarAccounts)
            {
                foreach (var c in a.Calendars)
                {
                    if (c.Id == id)
                    {
                        return c.Disabled == true ? null : c;
                    }
                }
            }
            return null;
        }

        /// <summary>The account's name, cleaned ("" for an unknown id).</summary>
        public string AccountName(AccountId id) => CleanLine(FindAccount(id)?.Config.Name);

        /// <summary>The calendar's name, cleaned ("" for an unknown or disabled id).</summary>
        public string CalendarName(CalendarId id) => CleanLine(FindCalendar(id)?.Name);

        /// <summary>The states of the accounts, for the status line.</summary>
        public IReadOnlyList<CalendarSyncState> States => [.. CalendarAccounts.Select(a => a.State)];

        /// <summary>The visible active calendars.</summary>
        public IReadOnlySet<CalendarId> VisibleCalendars =>
            ActiveCalendarAccounts.SelectMany(a => a.Calendars.Where(c => c.Visible).Select(c => c.Id)).ToHashSet();

        /// <summary>
        /// The calendars a new event can go to (<see cref="Prefs.Writable"/>),
        /// each account's default first, accounts in their order.
        /// </summary>
        public IReadOnlyList<Api.Calendar> WritableCalendars
        {
            get
            {
                var output = new List<Api.Calendar>();
                foreach (var a in ActiveCalendarAccounts)
                {
                    var w = a.Calendars.Where(Prefs.Writable).ToList();
                    output.AddRange(w.Where(c => c.IsDefault == true));
                    output.AddRange(w.Where(c => c.IsDefault != true));
                }
                return output;
            }
        }

        /// <summary>The user's own address in an account, cleaned ("" unknown).</summary>
        public string MyAddress(AccountId id) => CleanLine(FindAccount(id)?.Config.Email);

        /// <summary>The user's name in an account, else the address (<c>me</c> of the mail texts).</summary>
        public string MyName(AccountId id)
        {
            var n = CleanLine(FindAccount(id)?.Config.DisplayName);
            return n.Length == 0 ? MyAddress(id) : n;
        }

        /// <summary>Whether both hold the same, the lists compared in order (Swift's Equatable).</summary>
        public bool Equals(Snapshot? other) =>
            other is not null && Phase == other.Phase && Board.SameList(Accounts, other.Accounts)
            && Board.SameList(CalendarAccounts, other.CalendarAccounts) && Unanswered == other.Unanswered
            && Board.SameList(Invitations, other.Invitations) && Preferences == other.Preferences;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Phase, Accounts.Count, CalendarAccounts.Count, Unanswered, Invitations.Count);
    }

    /// <summary>What changed in a source, for its observers.</summary>
    public abstract record SourceChange
    {
        // Only the cases below derive from it.
        private SourceChange()
        {
        }

        /// <summary>Swift <c>.snapshot</c>: the snapshot (accounts, calendars, states, invitations, preferences).</summary>
        public sealed record SnapshotChanged : SourceChange;

        /// <summary>
        /// Swift <c>.occurrences(from:to:)</c>: the occurrences of ranges
        /// overlapping [<paramref name="From"/>, <paramref name="To"/>) were
        /// dropped (both null: every range); a controller showing one asks again.
        /// </summary>
        /// <param name="From">The start of the span; null is open.</param>
        /// <param name="To">The end of the span; null is open.</param>
        public sealed record OccurrencesChanged(DateTimeOffset? From, DateTimeOffset? To) : SourceChange;

        /// <summary>
        /// Swift <c>.event</c>: a cached event was replaced or dropped (a
        /// write's answer, a conflict's current version).
        /// </summary>
        /// <param name="Id">The event.</param>
        public sealed record EventChanged(CalendarEventId Id) : SourceChange;

        /// <summary>Swift <c>.alert</c>: <c>notify.eventAlert</c>.</summary>
        /// <param name="Alert">The alert, due or dismissed.</param>
        public sealed record AlertChanged(Api.EventAlert Alert) : SourceChange;

        /// <summary>
        /// Swift <c>.error</c>: a fire-and-forget write (a calendar's
        /// visibility) was refused and undone; a short sentence for a toast.
        /// </summary>
        /// <param name="Message">The sentence, translated.</param>
        public sealed record Failed(string Message) : SourceChange;
    }

    /// <summary>What <see cref="SourceError"/> is (Swift's cases).</summary>
    public enum SourceErrorKind
    {
        /// <summary>"The preview cannot do that." (<see cref="Text.PreviewOnly"/>): the samples cannot write.</summary>
        Preview,
    }

    /// <summary>
    /// The error of a source that cannot write (Swift <c>Cal.SourceError</c>):
    /// the samples' writes throw <see cref="Preview"/>. The message is
    /// technical English; <see cref="ErrorMessage"/> gives the sentence.
    /// </summary>
    [SuppressMessage("Naming", "CA1710", Justification = "Swift's name (Cal.SourceError, the case .preview); the sources and controllers name it so.")]
    public sealed class SourceError : Exception
    {
        /// <summary>An error of <paramref name="kind"/>.</summary>
        public SourceError(SourceErrorKind kind)
            : base("calendar: the preview cannot write")
        {
            Kind = kind;
        }

        /// <summary>A <see cref="SourceErrorKind.Preview"/> error; for serialisers and analyzers.</summary>
        public SourceError()
            : this(SourceErrorKind.Preview)
        {
        }

        /// <summary>A <see cref="SourceErrorKind.Preview"/> error with <paramref name="message"/>.</summary>
        public SourceError(string message)
            : base(message)
        {
            Kind = SourceErrorKind.Preview;
        }

        /// <summary>A <see cref="SourceErrorKind.Preview"/> error with <paramref name="message"/> and its cause.</summary>
        public SourceError(string message, Exception innerException)
            : base(message, innerException)
        {
            Kind = SourceErrorKind.Preview;
        }

        /// <summary>Swift <c>.preview</c>: a new error of <see cref="SourceErrorKind.Preview"/> to throw.</summary>
        public static SourceError Preview => new(SourceErrorKind.Preview);

        /// <summary>What went wrong.</summary>
        public SourceErrorKind Kind { get; }
    }

    /// <summary>
    /// Swift <c>Cal.errorMessage</c>: the sentence for a failed calendar
    /// call: the preview's (<see cref="SourceError"/>), the calendar's own by
    /// code (<see cref="Text.ErrorText"/>), else the generic one
    /// (<see cref="RpcErrorText.Text(string, Exception?)"/>) with
    /// <paramref name="what"/> (already translated; the mode's name when null).
    /// </summary>
    public static string ErrorMessage(Exception? error, string? what = null)
    {
        if (error is SourceError { Kind: SourceErrorKind.Preview })
        {
            return Text.PreviewOnly;
        }
        var (kind, e) = RpcErrorText.Classify(error);
        if (kind == RpcErrorText.FailureKind.Daemon && e is not null && Text.ErrorText(e.Code) is { } t)
        {
            return t;
        }
        return RpcErrorText.Text(what ?? Text.ModeName, error);
    }

    /// <summary>
    /// Swift <c>rangeKey</c>: the key of a range in the occurrence caches:
    /// its two instants and its zone.
    /// </summary>
    public static string RangeKey(Range r)
    {
        ArgumentNullException.ThrowIfNull(r);
        return string.Create(CultureInfo.InvariantCulture, $"{r.From.UtcTicks}|{r.To.UtcTicks}|{r.Zone.Id}");
    }

    /// <summary>Swift <c>eventKey</c>: the key of an event in the event caches: its id and recurrence id.</summary>
    public static string EventKey(CalendarEventId id, DateOrTime? recurrenceId) =>
        (id.Value ?? "") + "|" + (recurrenceId?.Value ?? "");

    /// <summary>
    /// Swift <c>rangesOverlap</c>: [<paramref name="a"/>, <paramref name="b"/>)
    /// and [<paramref name="from"/>, <paramref name="to"/>) overlap; a null
    /// bound is open.
    /// </summary>
    public static bool RangesOverlap(DateTimeOffset a, DateTimeOffset b, DateTimeOffset? from, DateTimeOffset? to) =>
        (to is not { } t || a < t) && (from is not { } f || b > f);
}
