// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Calendar/CalendarEditor.swift; GTK:
// ui/internal/calendar/editor.go (Origin, MonthCellHour, Defaults,
// DefaultDuration, NewEventDefaults, Input, Problem, Validate, ProblemText,
// EventInput, SendsInvitations, PrimaryLabel, EditorDelete, Cancel, Footer,
// AlertChoices, AlertTitle, AllDayAlertChoices, AllDayAlertDefault,
// AlertChoicesFor, AlertTitleFor, AlertOnAllDayToggle, DurationChoices,
// DurationTitle, TimeZoneLink, ZoneSearch, TitlePlaceholder, Field, Fields,
// FieldLabel, AddVideoCall, PasteLink, VideoProviderName, Attach,
// CloseQuestion, MailKind, MailKindOf, MailTexts, capBytes, ScopeAction,
// ScopeChoice, ScopeQuestion, ScopeTitle, ScopeQuestionFor) and Swift's
// isBareAddress (Go's mail.ParseAddress test of Validate).
//
// The event editor, New Event / Edit Event (docs/calendar.md §5.6): where a
// new event starts, what makes the input invalid, the primary button, the
// footer note, the alert and duration choices (D31's all-day alerts at
// 9:00), the time-zone link, the iMIP subjects and texts the daemon sends
// (it is language-neutral, docs/calendar.md D9), the close question and the
// question of a recurring event's scope. Input carries its zone, as
// Swift's does (Go's times carry it). Windows: a zone's name on the wire
// is its IANA name (IanaZoneName), which a Windows zone id is converted to.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.I18n;

namespace Malachi.Core.Calendars;

public static partial class Cal
{
    /// <summary>editor.go <c>Origin</c>: where a new event was asked for.</summary>
    public enum Origin
    {
        /// <summary>The "+" button, the New Event key and menu item: the next half hour from now (an exact half hour stays).</summary>
        Now,

        /// <summary>A double click on empty time of the grid: the half hour the click is in.</summary>
        Slot,

        /// <summary>A double click on a month cell: <see cref="MonthCellHour"/>:00 that day.</summary>
        MonthCell,

        /// <summary>A double click on the all-day row: an all-day event that day.</summary>
        AllDay,
    }

    /// <summary>editor.go <c>MonthCellHour</c>: the start of an event made from a month cell.</summary>
    public const int MonthCellHour = 9;

    /// <summary>editor.go <c>Defaults</c>: a new event's start, end and alerts.</summary>
    public sealed record Defaults
    {
        /// <summary>An all-day event.</summary>
        public bool AllDay { get; init; }

        /// <summary>The start in the editor's zone; an all-day event's first midnight.</summary>
        public required DateTimeOffset Start { get; init; }

        /// <summary>The end in the editor's zone; an all-day event's midnight after its last day.</summary>
        public required DateTimeOffset End { get; init; }

        /// <summary>The default alert, or none (empty).</summary>
        public IReadOnlyList<int> Alerts { get; init; } = [];
    }

    /// <summary>editor.go <c>DefaultDuration</c>: the preference's default duration, 60 minutes when it is out of 5–1440.</summary>
    public static TimeSpan DefaultDuration(Api.CalendarPreferences p)
    {
        ArgumentNullException.ThrowIfNull(p);
        var m = p.DefaultDurationMinutes;
        if (m < 5 || m > 1440)
        {
            m = 60;
        }
        return TimeSpan.FromMinutes(m);
    }

    /// <summary>
    /// editor.go <c>NewEventDefaults</c>: where a new event starts:
    /// <paramref name="at"/> is now (<see cref="Origin.Now"/>) or the time or
    /// day the user double-clicked, read in <paramref name="zone"/>.
    /// </summary>
    public static Defaults NewEventDefaults(DateTimeOffset at, Origin origin, Api.CalendarPreferences p, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(p);
        var t = PartsOf(at, zone);
        IReadOnlyList<int> alerts = p.DefaultAlertMinutes >= 0 && p.DefaultAlertMinutes <= API.Limits.MaxAlertMinutes
            ? [p.DefaultAlertMinutes]
            : [];
        DateTimeOffset start;
        switch (origin)
        {
            case Origin.AllDay:
                {
                    var day = StartOfDay(at, zone);
                    return new Defaults
                    {
                        AllDay = true,
                        Start = day,
                        End = AddDays(day, 1, zone),
                        Alerts = alerts.Count > 0 ? [AllDayAlertDefault] : [],
                    };
                }
            case Origin.MonthCell:
                start = Date(t.Year, t.Month, t.Day, MonthCellHour, 0, zone);
                break;
            case Origin.Slot:
                start = Date(t.Year, t.Month, t.Day, t.Hour, t.Minute / 30 * 30, zone);
                break;
            default:
                start = Date(t.Year, t.Month, t.Day, t.Hour, t.Minute / 30 * 30, zone);
                if (start < at)
                {
                    start = Date(t.Year, t.Month, t.Day, t.Hour, t.Minute / 30 * 30 + 30, zone);
                }
                break;
        }
        return new Defaults { Start = start, End = ToZone(start + DefaultDuration(p), zone), Alerts = alerts };
    }

    /// <summary>editor.go <c>Input</c>: what the editor holds, for validation and for <see cref="EventInput(Input, TimeZoneInfo)"/>.</summary>
    public sealed record Input
    {
        /// <summary>The title as typed.</summary>
        public string Title { get; init; } = "";

        /// <summary>An all-day event.</summary>
        public bool AllDay { get; init; }

        /// <summary>The start (an all-day event's first day).</summary>
        public required DateTimeOffset Start { get; init; }

        /// <summary>The end (an all-day event's midnight after its last day).</summary>
        public required DateTimeOffset End { get; init; }

        /// <summary>The editor's zone, which decides an all-day event's days (Go's times carry it).</summary>
        public required TimeZoneInfo Zone { get; init; }

        /// <summary>The location as typed.</summary>
        public string Location { get; init; } = "";

        /// <summary>The notes as typed.</summary>
        public string Notes { get; init; } = "";

        /// <summary>The recipient field's resolved addresses.</summary>
        public IReadOnlyList<EventInputAttendee> Attendees { get; init; } = [];

        /// <summary>The alerts in minutes before; null is sent as none (<c>[]</c>).</summary>
        public IReadOnlyList<int>? Alerts { get; init; }

        /// <summary>The chosen calendar (null none chosen).</summary>
        public CalendarId? CalendarId { get; init; }
    }

    /// <summary>editor.go <c>Problem</c>: what is wrong with an <see cref="Input"/>, in the order <see cref="Validate"/> reports them.</summary>
    public enum Problem
    {
        /// <summary>No calendar is chosen.</summary>
        NoCalendar,

        /// <summary>The end is not after the start.</summary>
        EndBeforeStart,

        /// <summary>The title is over the daemon's limit.</summary>
        TitleTooLong,

        /// <summary>The location is over the daemon's limit.</summary>
        LocationTooLong,

        /// <summary>The notes are over the daemon's limit.</summary>
        NotesTooLong,

        /// <summary>Too many attendees.</summary>
        TooManyAttendees,

        /// <summary>An attendee's address is not one.</summary>
        InvalidAttendee,

        /// <summary>Too many alerts.</summary>
        TooManyAlerts,
    }

    /// <summary>
    /// editor.go <c>Validate</c>: what keeps <paramref name="input"/> from
    /// being saved, in the order of <see cref="Problem"/>: no calendar, a
    /// timed end not after its start (an all-day event's last day before its
    /// first: End is the day after the last), a title, location or notes
    /// over the daemon's limit (UTF-8 bytes), too many attendees, an
    /// attendee address that is not one, too many alerts.
    /// </summary>
    public static IReadOnlyList<Problem> Validate(Input input)
    {
        ArgumentNullException.ThrowIfNull(input);
        var output = new List<Problem>();
        if (input.CalendarId?.Value is not { Length: > 0 })
        {
            output.Add(Problem.NoCalendar);
        }
        var z = input.Zone;
        if ((input.AllDay && StartOfDay(input.End, z) <= StartOfDay(input.Start, z)) || (!input.AllDay && input.End <= input.Start))
        {
            output.Add(Problem.EndBeforeStart);
        }
        if (Encoding.UTF8.GetByteCount(TrimSpace(input.Title)) > API.Limits.MaxEventTitleBytes)
        {
            output.Add(Problem.TitleTooLong);
        }
        if (Encoding.UTF8.GetByteCount(TrimSpace(input.Location)) > API.Limits.MaxEventLocationBytes)
        {
            output.Add(Problem.LocationTooLong);
        }
        if (Encoding.UTF8.GetByteCount(input.Notes ?? "") > API.Limits.MaxEventNotesBytes)
        {
            output.Add(Problem.NotesTooLong);
        }
        var attendees = input.Attendees ?? [];
        if (attendees.Count > API.Limits.MaxEventAttendeesIn)
        {
            output.Add(Problem.TooManyAttendees);
        }
        if (attendees.Any(a => !IsBareAddress(a.Address)))
        {
            output.Add(Problem.InvalidAttendee);
        }
        if ((input.Alerts?.Count ?? 0) > API.Limits.MaxEventAlerts)
        {
            output.Add(Problem.TooManyAlerts);
        }
        return output;
    }

    /// <summary>
    /// Swift's <c>isBareAddress</c>: Go's <c>mail.ParseAddress(s)</c>
    /// succeeding with <c>Address == s</c>: a bare addr-spec, a dot-atom
    /// local part (a quoted one comes back unquoted, so never equal), "@", a
    /// dot-atom domain or a domain literal, and nothing around it.
    /// </summary>
    public static bool IsBareAddress(string? s)
    {
        var u = (s ?? "").EnumerateRunes().Select(r => r.Value).ToArray();
        var at = Array.IndexOf(u, '@');
        if (at < 0 || !DotAtom(u.AsSpan(0, at)))
        {
            return false;
        }
        var domain = u.AsSpan(at + 1);
        if (domain.Length > 0 && domain[0] == '[')
        {
            if (domain.Length < 2 || domain[^1] != ']')
            {
                return false;
            }
            foreach (var c in domain[1..^1])
            {
                if (!(c is >= 33 and <= 90 || c is >= 94 and <= 126))
                {
                    return false;
                }
            }
            return true;
        }
        return DotAtom(domain);
    }

    // An RFC 5322 atext character; anything outside ASCII counts (as Go's
    // parser takes UTF-8).
    private static bool Atext(int c) =>
        c >= 0x80 || (c is >= 0x21 and <= 0x7E && "()<>[]:;@\\,\".".IndexOf((char)c, StringComparison.Ordinal) < 0);

    // A dot-atom: atext runs separated by single dots, no dot at either end.
    private static bool DotAtom(ReadOnlySpan<int> a)
    {
        if (a.Length == 0 || a[0] == '.' || a[^1] == '.')
        {
            return false;
        }
        var previousDot = false;
        foreach (var c in a)
        {
            if (c == '.')
            {
                if (previousDot)
                {
                    return false;
                }
                previousDot = true;
            }
            else
            {
                if (!Atext(c))
                {
                    return false;
                }
                previousDot = false;
            }
        }
        return true;
    }

    /// <summary>editor.go <c>ProblemText</c>: a problem's sentence under the form.</summary>
    public static string ProblemText(Problem p) => p switch
    {
        Problem.NoCalendar => L10n.T("Choose a calendar for the event."),
        Problem.EndBeforeStart => L10n.T("The event must end after it starts."),
        Problem.TitleTooLong => L10n.T("The title is too long."),
        Problem.LocationTooLong => L10n.T("The location is too long."),
        Problem.NotesTooLong => L10n.T("The notes are too long."),
        Problem.TooManyAttendees => L10n.N(
            "An event can have at most %d attendee.", "An event can have at most %d attendees.", API.Limits.MaxEventAttendeesIn),
        Problem.InvalidAttendee => L10n.T("An attendee’s address is not valid."),
        _ => L10n.N("An event can have at most %d alert.", "An event can have at most %d alerts.", API.Limits.MaxEventAlerts),
    };

    /// <summary>
    /// editor.go <c>EventInput</c>: <paramref name="input"/> as event.create
    /// and event.update take it: timed in <paramref name="zone"/> (its IANA
    /// name, <see cref="IanaZoneName"/>), all-day as dates with the exclusive
    /// end (a last day before the first is the first day alone). An empty
    /// location, notes or attendee list is left out.
    /// </summary>
    public static Api.EventInput EventInput(Input input, TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(input);
        ArgumentNullException.ThrowIfNull(zone);
        var location = TrimSpace(input.Location);
        var output = new Api.EventInput
        {
            Title = TrimSpace(input.Title),
            AllDay = input.AllDay,
            Location = location.Length == 0 ? null : location,
            Notes = string.IsNullOrEmpty(input.Notes) ? null : input.Notes,
            Alerts = input.Alerts ?? [],
            Attendees = input.Attendees is { Count: > 0 } a ? a : null,
        };
        if (input.AllDay)
        {
            var first = StartOfDay(input.Start, zone);
            var end = StartOfDay(input.End, zone);
            if (end <= first)
            {
                end = AddDays(first, 1, zone);
            }
            return output with { StartDate = DateOf(input.Start, zone), EndDate = DateOf(end, zone) };
        }
        return output with
        {
            Start = LocalDateTimeOf(input.Start, zone),
            End = LocalDateTimeOf(input.End, zone),
            TimeZone = new TimeZoneName(IanaZoneName(zone)),
        };
    }

    // An instant's wall clock in zone, Go's layout 2006-01-02T15:04:05.
    private static LocalDateTime LocalDateTimeOf(DateTimeOffset t, TimeZoneInfo zone)
    {
        var p = PartsOf(t, zone);
        return new LocalDateTime(string.Create(
            CultureInfo.InvariantCulture, $"{p.Year:D4}-{p.Month:D2}-{p.Day:D2}T{p.Hour:D2}:{p.Minute:D2}:{p.Second:D2}"));
    }

    /// <summary>
    /// The IANA name of <paramref name="zone"/> (Go's <c>loc.String()</c>,
    /// Swift's <c>TimeZone.identifier</c>), what the API's zone names are.
    /// Windows-only: <see cref="TimeZoneInfo.Local"/> has a Windows id
    /// ("Central Europe Standard Time"), which is converted; a zone without
    /// an IANA counterpart keeps its id.
    /// </summary>
    public static string IanaZoneName(TimeZoneInfo zone)
    {
        ArgumentNullException.ThrowIfNull(zone);
        if (zone.HasIanaId)
        {
            return zone.Id;
        }
        return TimeZoneInfo.TryConvertWindowsIdToIanaId(zone.Id, out var iana) ? iana : zone.Id;
    }

    /// <summary>
    /// editor.go <c>SendsInvitations</c>: whether saving sends invitations:
    /// there are attendees and someone delivers them (a Graph account always
    /// does).
    /// </summary>
    public static bool SendsInvitations(int attendees, CalendarSource source, CalendarFeatures f)
    {
        ArgumentNullException.ThrowIfNull(f);
        return attendees > 0 && (source == CalendarSource.Graph || f.Invitations != InvitationsVia.None);
    }

    /// <summary>editor.go <c>PrimaryLabel</c>: the editor's primary button: Save, or Save and Send Invitations when saving sends them.</summary>
    public static string PrimaryLabel(int attendees, CalendarSource source, CalendarFeatures f) =>
        SendsInvitations(attendees, source, f) ? L10n.T("Save and Send Invitations") : L10n.T("Save");

    /// <summary>
    /// editor.go <c>EditorDelete</c>: the destructive button at the left of
    /// the footer of an Edit Event window (<paramref name="editing"/>; never
    /// for a new event), offered when the event can be deleted, titled as the
    /// detail's (<see cref="RemoveTitle"/>); null when there is none.
    /// Pressing it asks what the detail's Remove asks
    /// (<see cref="RemoveQuestion"/>, else <see cref="DeleteQuestion"/> under
    /// the Mail preference confirm-delete), then runs event.delete with the
    /// scope the editor was opened with and closes the window without the
    /// unsaved-changes question.
    /// </summary>
    public static string? EditorDelete(bool editing, CalendarEvent e)
    {
        ArgumentNullException.ThrowIfNull(e);
        return editing && e.CanDelete ? RemoveTitle(e) : null;
    }

    /// <summary>editor.go <c>Cancel</c>: the editor's and the questions' Cancel.</summary>
    public static string Cancel => L10n.T("Cancel");

    /// <summary>
    /// editor.go <c>Footer</c>: the editor's footer note, line by line: who
    /// sends the invitations (only with attendees), then the attachments.
    /// <paramref name="mailAccount"/> and <paramref name="mailAddress"/> name
    /// the account invitations leave from (features invitations "mail").
    /// </summary>
    public static IReadOnlyList<string> Footer(
        int attendees, CalendarFeatures f, string mailAccount, string mailAddress, IReadOnlyList<string>? attachments)
    {
        ArgumentNullException.ThrowIfNull(f);
        var lines = new List<string>();
        if (attendees > 0)
        {
            switch (f.Invitations.Value)
            {
                case InvitationsVia.Mail:
                    lines.Add(L10n.T(
                        "Invitations leave by e-mail from account %s (%s) through the Outbox.", CleanLine(mailAccount), CleanLine(mailAddress)));
                    break;
                case InvitationsVia.Server:
                    lines.Add(L10n.T("The server sends the invitations."));
                    break;
                default:
                    lines.Add(L10n.T("No account can send invitations from this calendar."));
                    break;
            }
        }
        foreach (var a in attachments ?? [])
        {
            // TRANSLATORS: the event editor's footer: a file attached to the
            // event; %s is its name.
            lines.Add(L10n.T("Attachment: %s", CleanLine(a)));
        }
        return lines;
    }

    /// <summary>editor.go <c>AlertChoices</c>: the Alert popup's values in minutes before the start; -1 is None.</summary>
    public static IReadOnlyList<int> AlertChoices { get; } = [-1, 0, 5, 10, 15, 30, 60, 1440];

    /// <summary>
    /// editor.go <c>AlertTitle</c>: an alert's title: None, At time of
    /// event, "15 minutes before", "1 hour before", "1 day before" (any
    /// value, not only the choices: a server's 120 is "2 hours before").
    /// </summary>
    public static string AlertTitle(int minutes)
    {
        if (minutes < 0)
        {
            // TRANSLATORS: the event editor's Alert popup: no alert.
            return L10n.C("alert", "None");
        }
        if (minutes == 0)
        {
            return L10n.T("At time of event");
        }
        if (minutes % 1440 == 0)
        {
            return L10n.N("%d day before", "%d days before", minutes / 1440);
        }
        if (minutes % 60 == 0)
        {
            return L10n.N("%d hour before", "%d hours before", minutes / 60);
        }
        return L10n.N("%d minute before", "%d minutes before", minutes);
    }

    /// <summary>
    /// editor.go <c>AllDayAlertChoices</c>: the Alert popup's values of an
    /// all-day event: None, then days before at 9:00, stored as minutes
    /// before its midnight (1 day before at 9:00 is 15 hours before; the
    /// daemon takes nothing negative, so "on the day at 9:00" cannot be
    /// stored).
    /// </summary>
    public static IReadOnlyList<int> AllDayAlertChoices { get; } = [-1, 900, 2340, 9540];

    /// <summary>editor.go <c>AllDayAlertDefault</c>: an all-day event's alert when it has one: 1 day before at 9:00.</summary>
    public const int AllDayAlertDefault = 900;

    // The hour of the all-day alerts, in minutes after midnight.
    private const int AllDayAlertHour = 9 * 60;

    /// <summary>editor.go <c>AlertChoicesFor</c>: the Alert popup's values of a timed or an all-day event.</summary>
    public static IReadOnlyList<int> AlertChoicesFor(bool allDay) => allDay ? AllDayAlertChoices : AlertChoices;

    /// <summary>
    /// editor.go <c>AlertTitleFor</c>: an alert's title. An all-day event's
    /// value that is a whole number of days before 9:00 reads "1 day before
    /// (9:00)", "2 days before (9:00)", "1 week before (9:00)"; any other
    /// value (Outlook's 18 hours) and every timed one is <see cref="AlertTitle"/>'s.
    /// </summary>
    public static string AlertTitleFor(int minutes, bool allDay)
    {
        if (!allDay || minutes <= 0 || (minutes + AllDayAlertHour) % 1440 != 0)
        {
            return AlertTitle(minutes);
        }
        var d = (minutes + AllDayAlertHour) / 1440;
        if (d == 7)
        {
            // TRANSLATORS: the Alert popup of an all-day event: a week before
            // the event's day, at 9:00.
            return L10n.T("1 week before (9:00)");
        }
        // TRANSLATORS: the Alert popup of an all-day event: a number of days
        // before the event's day, at 9:00.
        return L10n.N("%d day before (9:00)", "%d days before (9:00)", d);
    }

    /// <summary>
    /// editor.go <c>AlertOnAllDayToggle</c>: the editor's alert after the
    /// All Day switch changed (<paramref name="on"/>: now all-day): turning
    /// all-day on keeps None and an all-day choice, anything else becomes
    /// <see cref="AllDayAlertDefault"/>; turning it off keeps None and
    /// otherwise takes the preference's default alert.
    /// </summary>
    public static int AlertOnAllDayToggle(int current, bool on, Api.CalendarPreferences p)
    {
        ArgumentNullException.ThrowIfNull(p);
        if (current < 0)
        {
            return -1;
        }
        if (on)
        {
            return AllDayAlertChoices.Contains(current) ? current : AllDayAlertDefault;
        }
        if (p.DefaultAlertMinutes < 0 || p.DefaultAlertMinutes > API.Limits.MaxAlertMinutes)
        {
            return -1;
        }
        return p.DefaultAlertMinutes;
    }

    /// <summary>editor.go <c>DurationChoices</c>: the Default Duration popup's values in minutes.</summary>
    public static IReadOnlyList<int> DurationChoices { get; } = [15, 30, 45, 60, 90, 120];

    /// <summary>editor.go <c>DurationTitle</c>: a duration choice's title ("1 h 30 min").</summary>
    public static string DurationTitle(int minutes) => DurationText(TimeSpan.FromMinutes(minutes));

    /// <summary>editor.go <c>TimeZoneLink</c>: the editor's link that opens the zone popover; <paramref name="zone"/> is an IANA name.</summary>
    public static string TimeZoneLink(string zone) =>
        // TRANSLATORS: the event editor's link to the time-zone popover; %s is
        // a zone's name such as "Europe/Prague".
        L10n.T("Time zone: %s", zone);

    /// <summary>editor.go <c>ZoneSearch</c>: the zone popover's search field's placeholder.</summary>
    public static string ZoneSearch => L10n.T("Search Time Zones");

    /// <summary>editor.go <c>TitlePlaceholder</c>: the title field's placeholder.</summary>
    public static string TitlePlaceholder => L10n.T("Title");

    /// <summary>editor.go <c>Field</c>: a row of the editor's form, in the form's order.</summary>
    public enum Field
    {
        /// <summary>The calendar popup.</summary>
        Calendar,

        /// <summary>The All Day switch.</summary>
        AllDay,

        /// <summary>The start.</summary>
        Start,

        /// <summary>The end.</summary>
        End,

        /// <summary>The Repeat popup.</summary>
        Repeat,

        /// <summary>The location.</summary>
        Location,

        /// <summary>The attendees.</summary>
        Attendees,

        /// <summary>The availability rows.</summary>
        Availability,

        /// <summary>The Alert popup.</summary>
        Alert,

        /// <summary>The notes.</summary>
        Notes,
    }

    /// <summary>editor.go <c>Fields</c>: the form's rows in order.</summary>
    public static IReadOnlyList<Field> Fields { get; } =
    [
        Field.Calendar, Field.AllDay, Field.Start, Field.End, Field.Repeat, Field.Location, Field.Attendees,
        Field.Availability, Field.Alert, Field.Notes,
    ];

    /// <summary>editor.go <c>FieldLabel</c>: the label of an editor field.</summary>
    public static string FieldLabel(Field f) => f switch
    {
        Field.Calendar => L10n.T("Calendar:"),
        Field.Start => L10n.T("Starts:"),
        Field.End => L10n.T("Ends:"),
        Field.Repeat => L10n.T("Repeat:"),
        Field.Location => L10n.T("Location:"),
        Field.Attendees => L10n.T("Attendees:"),
        Field.Availability => L10n.T("Availability:"),
        Field.Alert => L10n.T("Alert:"),
        Field.Notes => L10n.T("Notes:"),
        _ => L10n.T("All Day:"),
    };

    /// <summary>editor.go <c>AddVideoCall</c>: the location row's menu button.</summary>
    public static string AddVideoCall => L10n.T("Add Video Call");

    /// <summary>editor.go <c>PasteLink</c>: the Add Video Call menu's item for a typed link.</summary>
    public static string PasteLink => L10n.T("Paste Link…");

    /// <summary>
    /// editor.go <c>VideoProviderName</c>: an online-meeting provider's name
    /// in the Add Video Call menu (Graph's open enum; brand names are not
    /// translated).
    /// </summary>
    public static string VideoProviderName(string? p) => p switch
    {
        "teamsForBusiness" or "teamsForConsumer" => "Microsoft Teams",
        "skypeForBusiness" => "Skype for Business",
        "skypeForConsumer" => "Skype",
        // TRANSLATORS: an online-meeting provider the calendar does not know.
        _ => L10n.T("Online Meeting"),
    };

    /// <summary>editor.go <c>Attach</c>: the editor's toolbar button for files.</summary>
    public static string Attach => L10n.T("Attach");

    /// <summary>editor.go <c>CloseQuestion</c>: asked when an editor with changes closes (Save / Discard / Cancel); nothing is stored before Save.</summary>
    public static Question CloseQuestion => new()
    {
        Heading = L10n.T("Save the Changes to This Event?"),
        Body = L10n.T("If you don’t save, your changes are lost."),
        Confirm = L10n.T("Save"),
        Alternative = L10n.T("Discard"),
    };

    /// <summary>editor.go <c>MailKind</c>: an iMIP message the daemon sends with the client's texts.</summary>
    public enum MailKind
    {
        /// <summary>A new invitation.</summary>
        Invitation,

        /// <summary>An updated invitation.</summary>
        Update,

        /// <summary>A cancellation.</summary>
        Cancel,

        /// <summary>An acceptance.</summary>
        Accepted,

        /// <summary>A tentative answer.</summary>
        Tentative,

        /// <summary>A decline.</summary>
        Declined,
    }

    /// <summary>editor.go <c>MailKindOf</c>: the reply kind of an answer.</summary>
    public static MailKind MailKindOf(EventResponse r) => r.Value switch
    {
        EventResponse.Tentative => MailKind.Tentative,
        EventResponse.Declined => MailKind.Declined,
        _ => MailKind.Accepted,
    };

    /// <summary>
    /// editor.go <c>MailTexts</c>: the subject and text of an iMIP message
    /// about <paramref name="o"/> (its title, time and place);
    /// <paramref name="me"/> is the user's name or address (a reply's text
    /// names who answered). Each is capped at
    /// <see cref="API.Limits.MaxMailTextBytes"/> UTF-8 bytes.
    /// </summary>
    public static MailText MailTexts(MailKind kind, Occurrence o, string me, Env env)
    {
        ArgumentNullException.ThrowIfNull(o);
        ArgumentNullException.ThrowIfNull(env);
        var title = CleanTitle(o.Title);
        me = CleanLine(me);
        string subject;
        string sentence;
        switch (kind)
        {
            case MailKind.Update:
                subject = L10n.T("Updated invitation: %s", title);
                sentence = L10n.T("The event “%s” has changed.", title);
                break;
            case MailKind.Cancel:
                subject = L10n.T("Cancelled: %s", title);
                sentence = L10n.T("The event “%s” has been cancelled.", title);
                break;
            case MailKind.Accepted:
                subject = L10n.T("Accepted: %s", title);
                sentence = L10n.T("%s has accepted the invitation to “%s”.", me, title);
                break;
            case MailKind.Tentative:
                subject = L10n.T("Tentative: %s", title);
                sentence = L10n.T("%s has tentatively accepted the invitation to “%s”.", me, title);
                break;
            case MailKind.Declined:
                subject = L10n.T("Declined: %s", title);
                sentence = L10n.T("%s has declined the invitation to “%s”.", me, title);
                break;
            default:
                subject = L10n.T("Invitation: %s", title);
                sentence = L10n.T("You are invited to “%s”.", title);
                break;
        }
        var lines = new List<string> { sentence, "" };
        var when = TimeLine(o, env);
        if (when.Length > 0)
        {
            // TRANSLATORS: a line of an invitation e-mail: the event's time.
            lines.Add(L10n.T("When: %s", when));
        }
        var where = CleanLine(o.Location);
        if (where.Length > 0)
        {
            // TRANSLATORS: a line of an invitation e-mail: the event's place.
            lines.Add(L10n.T("Where: %s", where));
        }
        return new MailText
        {
            Subject = CapBytes(subject, API.Limits.MaxMailTextBytes),
            Text = CapBytes(string.Join('\n', lines).TrimEnd('\n'), API.Limits.MaxMailTextBytes),
        };
    }

    /// <summary>Go's <c>capBytes</c>: <paramref name="s"/> cut to <paramref name="n"/> UTF-8 bytes on a character boundary.</summary>
    public static string CapBytes(string s, int n)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (Encoding.UTF8.GetByteCount(s) <= n)
        {
            return s;
        }
        var b = Encoding.UTF8.GetBytes(s);
        while (n > 0 && (b[n] & 0xC0) == 0x80)
        {
            n--;
        }
        return Encoding.UTF8.GetString(b, 0, n);
    }

    /// <summary>editor.go <c>ScopeAction</c>: what a scope question is about.</summary>
    public enum ScopeAction
    {
        /// <summary>Editing an occurrence.</summary>
        Edit,

        /// <summary>Deleting an occurrence.</summary>
        Delete,

        /// <summary>Answering for an occurrence.</summary>
        Respond,
    }

    /// <summary>editor.go <c>ScopeChoice</c>: a button of the scope question.</summary>
    /// <param name="Scope">The scope the button picks.</param>
    /// <param name="Title">Its title (<see cref="ScopeTitle"/>).</param>
    public sealed record ScopeChoice(EditScope Scope, string Title);

    /// <summary>editor.go <c>ScopeQuestion</c>: the question asked before a change of an occurrence of a series; Cancel is always there.</summary>
    public sealed record ScopeQuestion
    {
        /// <summary>The heading.</summary>
        public string Heading { get; init; } = "";

        /// <summary>The text under the heading.</summary>
        public string Body { get; init; } = "";

        /// <summary>The choices in the buttons' order.</summary>
        public IReadOnlyList<ScopeChoice> Choices { get; init; } = [];
    }

    /// <summary>editor.go <c>ScopeTitle</c>: a scope's button: This Event Only, This and Future Events, All Events.</summary>
    public static string ScopeTitle(EditScope s) => s.Value switch
    {
        EditScope.ThisAndFuture => L10n.T("This and Future Events"),
        EditScope.All => L10n.T("All Events"),
        _ => L10n.T("This Event Only"),
    };

    /// <summary>
    /// editor.go <c>ScopeQuestionFor</c>: the question of an action on an
    /// occurrence of a series. An answer offers this and all only; a rule
    /// outside the editor's shape (<paramref name="complex"/>) is changed
    /// only as a whole series (docs/calendar.md D19), so the edit and delete
    /// offer All Events alone.
    /// </summary>
    public static ScopeQuestion ScopeQuestionFor(ScopeAction a, bool complex)
    {
        EditScope[] scopes = [EditScope.This, EditScope.ThisAndFuture, EditScope.All];
        string heading;
        string body;
        switch (a)
        {
            case ScopeAction.Delete:
                heading = L10n.T("Delete a Repeating Event?");
                body = L10n.T("Do you want to delete only this occurrence, or also the ones after it?");
                break;
            case ScopeAction.Respond:
                heading = L10n.T("Answer for the Whole Series?");
                body = L10n.T("Do you want to answer for this occurrence only, or for every occurrence?");
                scopes = [EditScope.This, EditScope.All];
                break;
            default:
                heading = L10n.T("Change a Repeating Event?");
                body = L10n.T("Do you want to change only this occurrence, or also the ones after it?");
                break;
        }
        if (complex && a != ScopeAction.Respond)
        {
            scopes = [EditScope.All];
            body = L10n.T("This event repeats in a way that can be changed only for the whole series.");
        }
        return new ScopeQuestion
        {
            Heading = heading,
            Body = body,
            Choices = [.. scopes.Select(s => new ScopeChoice(s, ScopeTitle(s)))],
        };
    }
}
