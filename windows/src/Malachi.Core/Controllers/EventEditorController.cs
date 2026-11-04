// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/EventEditorController.swift
// (EventEditorController: Mode, CalendarChoice, availabilityDebounce,
// freeSlotDays, start, stop, windowTitle, calendars, repeatChoices,
// alertChoices, alertTitle, primaryLabel, saveKeyAllowed, footer,
// problemTexts, showsAvailability, availabilityDay, availabilityRows,
// availabilitySummary, videoProviders, timeZoneLink, the setters,
// editAttendees, scheduleAvailability, availabilityPending, nextFreeSlot,
// attach, removeAttachment, eventInput, save, deleteTitle, delete, close);
// GTK: none yet, the rules are ui/internal/calendar/editor.go,
// availability.go and recurrence.go (Calendars/CalendarEditor.cs,
// CalendarAvailability.cs, CalendarRecurrence.cs).
//
// One New Event / Edit Event window (docs/calendar.md §5.6): the input
// (Cal.Input) with its validation, the writable calendars, the Repeat and
// Alert choices, the attendees through the compose window's RecipientTokens
// model, the attendees' availability (calendar.freeBusy 500 ms after the
// last change, only when the calendar's account has features.freeBusy),
// Next Free Slot (calendar.findFreeTime), the attachments
// (attachment.import), the primary button and the footer, and the save
// paths: event.create, or event.update with the scope the detail asked.
// Nothing is stored before Save (D6): closing with changes asks Save /
// Discard / Cancel, and Discard just closes.
//
// Windows: Swift's onChange is Changed, onSaved Saved, onClose
// CloseRequested; the questions (onQuestion, onDeleteQuestion) are the
// hooks AskQuestion and AskDeleteQuestion (one that throws answers Cancel,
// its failure reported), confirmDelete the property ConfirmDelete. Swift's
// now closure is the injected TimeProvider, its Sleep Cal.Sleep (by default
// on that clock). Swift's Mode enum is a record hierarchy (Mode.NewEvent,
// Mode.EditEvent). The availability's wait is untracked (RunDetached: a
// fake clock would hold IdleAsync for ever), its freeBusy call tracked.
// RecipientTokens is a class here, so editAttendees mutates the model in
// place (EditAttendees) and the macOS view's rebuild from the field's
// resolved value is SetAttendees. Swift's internal eventInput and
// availabilityPending are public (the tests are another assembly). Moved
// out of the macOS views (docs/windows-port.md §7.4): ShowsReloadEvent (the
// view's conflict rule), ReloadEventAsync (EventEditorManager.reload's
// event.get) and Start's findFreeSlot (the manager's findFreeSlot after
// start). Swift's == on Recurrence and InvitationCard-like values compares
// contents; here the contract's JSON (SameValue).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Compose;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.I18n;

namespace Malachi.Core.Controllers;

/// <summary>
/// The controller of one New Event / Edit Event window (Swift
/// <c>EventEditorController</c>).
/// </summary>
/// <remarks>
/// Create it, and call it, on the UI thread; every event is raised there.
/// Subscribe, then call <see cref="Start"/>; <see cref="Stop"/> when the
/// window closes.
/// </remarks>
public sealed class EventEditorController : IDisposable
{
    /// <summary>How long the availability waits after the last change.</summary>
    public static readonly TimeSpan AvailabilityDebounce = TimeSpan.FromMilliseconds(500);

    /// <summary>How far Next Free Slot looks, in days.</summary>
    public const int FreeSlotDays = 14;

    private static readonly CalendarFeatures NoFeatures = new()
    {
        FreeBusy = false,
        ProposeNewTime = false,
        Invitations = InvitationsVia.None,
        OnlineMeetings = false,
    };

    private readonly ControllerScope scope;
    private readonly TimeProvider time;
    private readonly Cal.Sleep sleep;
    private readonly Recurrence? original;
    private readonly List<DraftAttachment> attachments = [];
    private CancellationTokenSource? availabilityStop;
    private int availabilityGeneration;

    /// <summary>A window, on the calling (UI) thread.</summary>
    /// <param name="source">The calendar's data.</param>
    /// <param name="mode">New Event (null) or Edit Event with the scope the detail chose.</param>
    /// <param name="defaults">New Event's start, end and alert (<see cref="Cal.NewEventDefaults"/>); now's when null.</param>
    /// <param name="calendarId">New Event's calendar; the first writable one (each account's default first) when null.</param>
    /// <param name="env">The dates' zone and writer; the system's zone and the current culture when null.</param>
    /// <param name="time">Now (Swift's <c>now</c>); the system's clock when null.</param>
    /// <param name="sleep">The availability's debounce; a wait on <paramref name="time"/> when null.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    public EventEditorController(
        ICalendarDataSource source,
        Mode? mode = null,
        Cal.Defaults? defaults = null,
        CalendarId? calendarId = null,
        Cal.Env? env = null,
        TimeProvider? time = null,
        Cal.Sleep? sleep = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(source);
        Source = source;
        EditorMode = mode ?? new Mode.NewEvent();
        Env = env ?? Cal.Env.ForCulture(TimeZoneInfo.Local, CultureInfo.CurrentCulture);
        this.time = time ?? TimeProvider.System;
        this.sleep = sleep ?? Cal.SleepOn(this.time);
        scope = new ControllerScope(pending);
        var now = this.time.GetUtcNow();
        if (EditorMode is Mode.EditEvent { Edited: var e })
        {
            var zone = e.TimeZone is { Value: { Length: > 0 } name } && TimeZoneInfo.TryFindSystemTimeZoneById(name, out var z) ? z : Env.Zone;
            var span = Cal.Span(e, zone) ?? (Start: now, End: now);
            Input = new Cal.Input
            {
                Title = e.Title,
                AllDay = e.AllDay,
                Start = span.Start,
                End = span.End,
                Zone = zone,
                Location = e.Location ?? "",
                Notes = e.Notes ?? "",
                CalendarId = e.CalendarId,
            };
            Alert = e.Alerts is { Count: > 0 } alerts ? alerts[0] : -1;
            Recurrence = e.Recurrence;
            original = e.Recurrence;
            RepeatChoice = Cal.RepeatOf(e.Recurrence, span.Start, zone);
            if (e.VideoCallLink is { Url.Length: > 0 } v)
            {
                VideoCall = new VideoCallInput { Mode = VideoCallMode.Link, Url = v.Url };
            }
            foreach (var a in e.Attendees ?? [])
            {
                if (a.Me != true && a.Organizer != true)
                {
                    Attendees.Add(new Address { Name = a.Name, Email = a.Address });
                }
            }
        }
        else
        {
            var d = defaults ?? Cal.NewEventDefaults(now, Cal.Origin.Now, source.Snapshot.Preferences, Env.Zone);
            var cal = calendarId ?? (source.Snapshot.WritableCalendars is { Count: > 0 } w ? w[0].Id : (CalendarId?)null);
            Input = new Cal.Input { AllDay = d.AllDay, Start = d.Start, End = d.End, Zone = Env.Zone, CalendarId = cal };
            Alert = d.Alerts.Count > 0 ? d.Alerts[0] : -1;
            RepeatChoice = Cal.Repeat.Never;
        }
        Input = Input with { Alerts = AlertList(Alert) };
        SyncAttendees();
    }

    // Outputs

    /// <summary>What the window shows changed: it reads the controller again (Swift <c>onChange</c>).</summary>
    public event EventHandler? Changed;

    /// <summary>Saved: the window closes (Swift <c>onSaved</c>; the event as the daemon stored it).</summary>
    public event EventHandler<CalendarEvent>? Saved;

    /// <summary>The window may close (Swift <c>onClose</c>).</summary>
    public event EventHandler? CloseRequested;

    /// <summary>Asks a question (the close question; Swift <c>onQuestion</c>). Null, or a hook that throws, answers Cancel.</summary>
    public Func<Cal.Question, Task<Cal.Answer>>? AskQuestion { get; set; }

    /// <summary>
    /// Asks a question of Delete (Cancel the default, the confirmation
    /// destructive; Swift <c>onDeleteQuestion</c>); null asks through
    /// <see cref="AskQuestion"/>.
    /// </summary>
    public Func<Cal.Question, Task<Cal.Answer>>? AskDeleteQuestion { get; set; }

    /// <summary>
    /// The Mail preference "Ask before moving to the Trash" (confirm-delete),
    /// as the detail reads it (Swift <c>confirmDelete</c>).
    /// </summary>
    public Func<bool> ConfirmDelete { get; set; } = static () => true;

    // State

    /// <summary>The calendar's data.</summary>
    public ICalendarDataSource Source { get; }

    /// <summary>The dates' zone and writer.</summary>
    public Cal.Env Env { get; }

    /// <summary>New Event or Edit Event (Swift <c>mode</c>).</summary>
    public Mode EditorMode { get; }

    /// <summary>What the form holds.</summary>
    public Cal.Input Input { get; private set; }

    /// <summary>The Repeat popup's choice.</summary>
    public Cal.Repeat RepeatChoice { get; private set; }

    /// <summary>The rule sent (the choice's, a custom one, the event's own).</summary>
    public Recurrence? Recurrence { get; private set; }

    /// <summary>The Alert popup's minutes before the start (-1 None).</summary>
    public int Alert { get; private set; }

    /// <summary>The attendee field's model; <see cref="EditAttendees"/> and <see cref="SetAttendees"/> change it.</summary>
    public RecipientTokens Attendees { get; private set; } = new();

    /// <summary>The imported attachments.</summary>
    public IReadOnlyList<DraftAttachment> Attachments => attachments;

    /// <summary>
    /// The video call (Add Video Call ▾: a provider of
    /// <see cref="VideoProviders"/>, or Paste Link… with an https URL); null none.
    /// </summary>
    public VideoCallInput? VideoCall { get; private set; }

    /// <summary>The problems of the last Save (cleared by the next change).</summary>
    public IReadOnlyList<Cal.Problem> Problems { get; private set; } = [];

    /// <summary>calendar.freeBusy's answer for the current input (null none).</summary>
    public CalendarFreeBusyResult? Availability { get; private set; }

    /// <summary>The Next Free Slot line ("" none).</summary>
    public string FreeSlotText { get; private set; } = "";

    /// <summary>Something was changed since the window opened.</summary>
    public bool Dirty { get; private set; }

    /// <summary>Save or Delete is on its way.</summary>
    public bool Saving { get; private set; }

    /// <summary>The inline error of Save or Attach ("" none).</summary>
    public string Error { get; private set; } = "";

    /// <summary>
    /// A request of the availability is waiting or on its way (Swift's
    /// internal <c>availabilityPending</c>, public for the tests).
    /// </summary>
    public bool AvailabilityPending { get; private set; }

    /// <summary>Whether <see cref="Stop"/> ran.</summary>
    public bool IsClosed => scope.IsClosed;

    // Derived

    /// <summary>"New Event" / "Edit Event".</summary>
    public string WindowTitle => EditorMode is Mode.EditEvent ? Cal.Text.EditEvent : Cal.Text.NewEvent;

    /// <summary>The Calendar popup: the writable calendars (Edit: the event's own).</summary>
    public IReadOnlyList<CalendarChoice> Calendars
    {
        get
        {
            var s = Source.Snapshot;
            IReadOnlyList<Api.Calendar> records;
            if (EditorMode is Mode.EditEvent { Edited: var e })
            {
                records = e.CalendarId is { } id && s.FindCalendar(id) is { } c ? [c] : [];
            }
            else
            {
                records = s.WritableCalendars;
            }
            return
            [
                .. records.Select(c => new CalendarChoice(
                    c.Id, c.AccountId, Cal.Text.Dot(s.AccountName(c.AccountId), Cal.CleanLine(c.Name)), c.Color)),
            ];
        }
    }

    /// <summary>The Repeat popup's choices.</summary>
    public static IReadOnlyList<Cal.Repeat> RepeatChoices => Cal.Repeats;

    /// <summary>The Alert popup's values: day-based at 9:00 for an all-day event.</summary>
    public IReadOnlyList<int> AlertChoices => Cal.AlertChoicesFor(Input.AllDay);

    /// <summary>Save / Save and Send Invitations.</summary>
    public string PrimaryLabel => CalendarAccount is { } a
        ? Cal.PrimaryLabel(Input.Attendees.Count, a.Source, a.Features)
        : Cal.PrimaryLabel(0, CalendarSource.CalDAV, NoFeatures);

    /// <summary>The save key (Ctrl+S) saves only without attendees (Ctrl+Enter is the primary).</summary>
    public bool SaveKeyAllowed => Input.Attendees.Count == 0;

    /// <summary>The footer's lines: who sends the invitations, the attachments.</summary>
    public IReadOnlyList<string> Footer
    {
        get
        {
            var a = CalendarAccount;
            var f = a?.Features ?? NoFeatures;
            var s = Source.Snapshot;
            var mail = f.MailAccountId ?? a?.AccountId;
            return Cal.Footer(
                Input.Attendees.Count, f, mail is { } m ? s.AccountName(m) : "", mail is { } n ? s.MyAddress(n) : "",
                [.. attachments.Select(x => x.Filename)]);
        }
    }

    /// <summary>The problem lines of the last Save.</summary>
    public IReadOnlyList<string> ProblemTexts => [.. Problems.Select(Cal.ProblemText)];

    /// <summary>The availability rows are shown (the account answers for others).</summary>
    public bool ShowsAvailability => (CalendarAccount?.Features.FreeBusy ?? false) && Input.Attendees.Count > 0;

    /// <summary>The day the availability rows span.</summary>
    public (DateTimeOffset From, DateTimeOffset To) AvailabilityDay
    {
        get
        {
            var d = Cal.StartOfDay(Input.Start, Env.Zone);
            return (d, Cal.AddDays(d, 1, Env.Zone));
        }
    }

    /// <summary>The availability rows of the attendees (the user first).</summary>
    public IReadOnlyList<Cal.AvailabilityRow> AvailabilityRows
    {
        get
        {
            if (!ShowsAvailability || Availability is not { } a)
            {
                return [];
            }
            var names = new Dictionary<string, string>(StringComparer.Ordinal);
            foreach (var t in Attendees.Tokens)
            {
                if (t.Address is { } ad)
                {
                    names[ad.Email] = t.Label;
                }
            }
            var day = AvailabilityDay;
            return Cal.AvailabilityRows(a, names, day.From, day.To);
        }
    }

    /// <summary>The summary line of the availability.</summary>
    public IReadOnlyList<string> AvailabilitySummary =>
        ShowsAvailability && Availability is { } a ? Cal.Summary(a, Input.Start, Input.End, Env) : [];

    /// <summary>
    /// The providers Add Video Call offers: the chosen calendar's
    /// <c>onlineMeetingProviders</c> when its account can create meetings.
    /// </summary>
    public IReadOnlyList<string> VideoProviders
    {
        get
        {
            if (CalendarAccount?.Features.OnlineMeetings != true || Input.CalendarId is not { } id
                || Source.Snapshot.FindCalendar(id) is not { } c)
            {
                return [];
            }
            return c.OnlineMeetingProviders ?? [];
        }
    }

    /// <summary>The time-zone link's text.</summary>
    public string TimeZoneLink => Cal.TimeZoneLink(Cal.IanaZoneName(Input.Zone));

    /// <summary>
    /// Edit mode's footer button at the left ("Delete" / "Remove from
    /// Calendar"); null without one (New Event, an event that cannot go).
    /// </summary>
    public string? DeleteTitle => EditorMode is Mode.EditEvent { Edited: var e } ? Cal.EditorDelete(editing: true, e) : null;

    /// <summary>
    /// The error is a conflict of Edit's Save: the view offers Reload Event
    /// (<see cref="Cal.Text.ReloadEvent"/>) beside it (the macOS view's rule).
    /// </summary>
    public bool ShowsReloadEvent => EditorMode is Mode.EditEvent && Error == Cal.Text.ErrorText(ErrorCode.Conflict);

    /// <summary>What Save would send (Swift's internal <c>eventInput</c>, public for the tests).</summary>
    public Api.EventInput EventInput
    {
        get
        {
            var e = Cal.EventInput(Input, Input.Zone);
            if (EditorMode is Mode.EditEvent { Scope: var editScope })
            {
                if (editScope == EditScope.This)
                {
                    e = e with { Recurrence = null };
                }
                else if (original is { Complex: true } o && SameValue(Recurrence, o))
                {
                    e = e with { KeepRecurrence = true };
                }
                else
                {
                    e = e with { Recurrence = Recurrence };
                }
            }
            else
            {
                e = e with { Recurrence = Recurrence };
            }
            e = e with { VideoCall = VideoCall };
            if (attachments.Count > 0)
            {
                e = e with { Attachments = [.. attachments.Select(a => a.Id)] };
            }
            return e;
        }
    }

    private CalendarAccount? CalendarAccount
    {
        get
        {
            var s = Source.Snapshot;
            return Input.CalendarId is { } id && s.FindCalendar(id) is { } c ? s.FindCalendarAccount(c.AccountId) : null;
        }
    }

    /// <summary>The Alert popup's title of <paramref name="minutes"/> for the current All Day state.</summary>
    public string AlertTitle(int minutes) => Cal.AlertTitleFor(minutes, Input.AllDay);

    /// <summary>
    /// Starts the availability for the first input; with
    /// <paramref name="findFreeSlot"/> (the detail's Find Another Time) runs
    /// Next Free Slot as the window opens.
    /// </summary>
    public void Start(bool findFreeSlot = false)
    {
        scope.VerifyAccess();
        ScheduleAvailability();
        if (findFreeSlot)
        {
            NextFreeSlot();
        }
    }

    /// <summary>The window closed: nothing is taken any more.</summary>
    public void Stop()
    {
        scope.Close();
        CancelAvailability();
    }

    /// <inheritdoc/>
    public void Dispose() => Stop();

    // Editing

    /// <summary>The title as typed.</summary>
    public void SetTitle(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (string.Equals(s, Input.Title, StringComparison.Ordinal))
        {
            return;
        }
        Input = Input with { Title = s };
        Edited();
    }

    /// <summary>The All Day switch: the times and the alert follow.</summary>
    public void SetAllDay(bool on)
    {
        if (on == Input.AllDay)
        {
            return;
        }
        var input = Input with { AllDay = on };
        if (on)
        {
            var start = Cal.StartOfDay(input.Start, input.Zone);
            var end = Cal.StartOfDay(input.End, input.Zone);
            input = input with { Start = start, End = end > start ? end : Cal.AddDays(start, 1, input.Zone) };
        }
        else
        {
            var p = Cal.PartsOf(input.Start, input.Zone);
            var start = Cal.Date(p.Year, p.Month, p.Day, 9, 0, input.Zone);
            input = input with { Start = start, End = start + Cal.DefaultDuration(Source.Snapshot.Preferences) };
        }
        // The alert follows: 15 minutes → 1 day before (9:00) and back to
        // the preference's default; None stays None.
        Alert = Cal.AlertOnAllDayToggle(Alert, on, Source.Snapshot.Preferences);
        Input = input with { Alerts = AlertList(Alert) };
        Edited(availability: true);
    }

    /// <summary>The start; the end moves with it (the duration stays).</summary>
    public void SetStart(DateTimeOffset t)
    {
        if (t == Input.Start)
        {
            return;
        }
        var d = Input.End - Input.Start;
        Input = Input with { Start = t, End = t + (d > TimeSpan.Zero ? d : TimeSpan.Zero) };
        Edited(availability: true);
    }

    /// <summary>The end.</summary>
    public void SetEnd(DateTimeOffset t)
    {
        if (t == Input.End)
        {
            return;
        }
        Input = Input with { End = t };
        Edited(availability: true);
    }

    /// <summary>The time-zone popover: the wall-clock times stay, the zone changes.</summary>
    public void SetZone(TimeZoneInfo z)
    {
        ArgumentNullException.ThrowIfNull(z);
        if (string.Equals(Cal.IanaZoneName(z), Cal.IanaZoneName(Input.Zone), StringComparison.Ordinal))
        {
            return;
        }
        var s = Cal.PartsOf(Input.Start, Input.Zone);
        var e = Cal.PartsOf(Input.End, Input.Zone);
        Input = Input with
        {
            Zone = z,
            Start = Cal.Date(s.Year, s.Month, s.Day, s.Hour, s.Minute, z),
            End = Cal.Date(e.Year, e.Month, e.Day, e.Hour, e.Minute, z),
        };
        Edited(availability: true);
    }

    /// <summary>
    /// Add Video Call ▾ / Paste Link… / remove (null or mode none). A link
    /// must be an https URL with a host, a meeting one of
    /// <see cref="VideoProviders"/>; false (nothing changed) otherwise.
    /// </summary>
    public bool SetVideoCall(VideoCallInput? v)
    {
        VideoCallInput? next;
        switch (v?.Mode.Value)
        {
            case null:
            case VideoCallMode.None:
                next = null;
                break;
            case VideoCallMode.Link when v is not null:
                {
                    var url = Cal.TrimSpace(v.Url);
                    if (!Uri.TryCreate(url, UriKind.Absolute, out var u) || !string.Equals(u.Scheme, Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase)
                        || string.IsNullOrEmpty(u.Host))
                    {
                        return false;
                    }
                    next = new VideoCallInput { Mode = VideoCallMode.Link, Url = url };
                    break;
                }
            case VideoCallMode.Create when v is not null:
                if (v.Provider is not { } p || !VideoProviders.Contains(p, StringComparer.Ordinal))
                {
                    return false;
                }
                next = new VideoCallInput { Mode = VideoCallMode.Create, Provider = p };
                break;
            default:
                return false;
        }
        if (next == VideoCall)
        {
            return true;
        }
        VideoCall = next;
        Edited();
        return true;
    }

    /// <summary>The location as typed.</summary>
    public void SetLocation(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (string.Equals(s, Input.Location, StringComparison.Ordinal))
        {
            return;
        }
        Input = Input with { Location = s };
        Edited();
    }

    /// <summary>The notes as typed.</summary>
    public void SetNotes(string s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (string.Equals(s, Input.Notes, StringComparison.Ordinal))
        {
            return;
        }
        Input = Input with { Notes = s };
        Edited();
    }

    /// <summary>The Calendar popup; a meeting of a provider the new calendar does not offer goes.</summary>
    public void SetCalendar(CalendarId id)
    {
        if (id == Input.CalendarId || !Calendars.Any(c => c.Id == id))
        {
            return;
        }
        Input = Input with { CalendarId = id };
        if (VideoCall is { } v && v.Mode == VideoCallMode.Create && !VideoProviders.Contains(v.Provider ?? "", StringComparer.Ordinal))
        {
            VideoCall = null;
        }
        Edited(availability: true);
    }

    /// <summary>The Repeat popup (Custom… opens the popover: <see cref="SetRecurrence"/>).</summary>
    public void SetRepeat(Cal.Repeat c)
    {
        if (c == Cal.Repeat.Custom)
        {
            return;
        }
        RepeatChoice = c;
        Recurrence = Cal.RepeatRecurrence(c, Input.Start, Input.Zone);
        Edited();
    }

    /// <summary>The Custom… popover's rule.</summary>
    public void SetRecurrence(Recurrence r)
    {
        ArgumentNullException.ThrowIfNull(r);
        Recurrence = r;
        RepeatChoice = Cal.RepeatOf(r, Input.Start, Input.Zone);
        Edited();
    }

    /// <summary>The Alert popup (-1 None).</summary>
    public void SetAlert(int minutes)
    {
        if (minutes == Alert)
        {
            return;
        }
        Alert = minutes;
        Input = Input with { Alerts = AlertList(minutes) };
        Edited();
    }

    /// <summary>
    /// The attendee field: applies a keystroke, paste or token change to the
    /// model in place (Swift <c>editAttendees</c>).
    /// </summary>
    public void EditAttendees(Action<RecipientTokens> change)
    {
        ArgumentNullException.ThrowIfNull(change);
        var tokens = Attendees.Tokens.ToList();
        var typed = Attendees.Pending;
        change(Attendees);
        AttendeesChanged(tokens, typed);
    }

    /// <summary>
    /// The attendee field's badges and typed text, as they resolve
    /// (<c>RecipientTokenBox.Resolved()</c>: what a commit would make of the
    /// typed text), become the model (the macOS view's <c>attendeesEdited</c>).
    /// </summary>
    public void SetAttendees(IReadOnlyList<Address> addresses, IReadOnlyList<string> invalid)
    {
        ArgumentNullException.ThrowIfNull(addresses);
        ArgumentNullException.ThrowIfNull(invalid);
        var t = new RecipientTokens();
        foreach (var a in addresses)
        {
            t.Add(a);
        }
        foreach (var raw in invalid)
        {
            t.SetPending(raw);
            t.Commit();
        }
        var tokens = Attendees.Tokens.ToList();
        var typed = Attendees.Pending;
        Attendees = t;
        AttendeesChanged(tokens, typed);
    }

    // Swift's `attendees != before` (the tokens and the typed text), then
    // the resolved attendees and the availability when they changed.
    private void AttendeesChanged(IReadOnlyList<RecipientTokens.Token> tokens, string typed)
    {
        if (Attendees.Tokens.SequenceEqual(tokens) && string.Equals(Attendees.Pending, typed, StringComparison.Ordinal))
        {
            return;
        }
        var before = Input.Attendees;
        SyncAttendees();
        Edited(availability: !Input.Attendees.SequenceEqual(before));
    }

    private void SyncAttendees() =>
        Input = Input with
        {
            Attendees =
            [
                .. Attendees.Resolved().Addresses.Select(a => new EventInputAttendee
                {
                    Name = Cal.CleanLine(a.Name).Length == 0 ? null : a.Name,
                    Address = a.Email,
                }),
            ],
        };

    // Availability

    private void ScheduleAvailability()
    {
        CancelAvailability();
        var g = ++availabilityGeneration;
        if (!ShowsAvailability || CalendarAccount?.AccountId is not { } account)
        {
            Availability = null;
            return;
        }
        var day = AvailabilityDay;
        var parameters = new CalendarFreeBusyParams
        {
            AccountId = account,
            Attendees = [.. Input.Attendees.Take(API.Limits.MaxFreeBusyAttendees).Select(a => a.Address)],
            From = day.From,
            To = day.To,
        };
        var stop = new CancellationTokenSource();
        availabilityStop = stop;
        AvailabilityPending = true;
        var token = stop.Token;
        var source = Source;
        scope.RunDetached(async lifetime =>
        {
            await sleep(AvailabilityDebounce, token);
            if (token.IsCancellationRequested || IsClosed || g != availabilityGeneration)
            {
                return;
            }
            _ = scope.Perform(
                _ => source.FreeBusyAsync(parameters),
                outcome =>
                {
                    if (g != availabilityGeneration)
                    {
                        return;
                    }
                    AvailabilityPending = false;
                    Availability = outcome.IsSuccess ? outcome.Value : null;
                    Notify();
                });
        });
    }

    private void CancelAvailability()
    {
        availabilityStop?.Cancel();
        availabilityStop?.Dispose();
        availabilityStop = null;
        AvailabilityPending = false;
    }

    /// <summary>
    /// Next Free Slot: the first free slot of the event's length after its
    /// start, in the next two weeks; moves the event there.
    /// </summary>
    public void NextFreeSlot()
    {
        scope.VerifyAccess();
        if (CalendarAccount?.AccountId is not { } account || Input.AllDay)
        {
            return;
        }
        var minutes = Math.Max(15, (int)(Input.End - Input.Start).TotalMinutes);
        var now = time.GetUtcNow();
        var from = Input.Start > now ? Input.Start : now;
        var parameters = new CalendarFindFreeTimeParams
        {
            AccountId = account,
            Attendees = [.. Input.Attendees.Select(a => a.Address)],
            DurationMinutes = minutes,
            From = from,
            To = Cal.AddDays(from, FreeSlotDays, Env.Zone),
            Limit = 5,
        };
        var source = Source;
        scope.Perform(
            _ => source.FindFreeTimeAsync(parameters),
            outcome =>
            {
                if (!outcome.TryGetValue(out var r, out var error))
                {
                    Error = Cal.ErrorMessage(error);
                    Notify();
                    return;
                }
                if (Cal.NextFreeSlot(r, from) is { } slot)
                {
                    Input = Input with { Start = slot.Start, End = slot.End };
                    FreeSlotText = Cal.NextFreeSlotText(slot, Env);
                    Edited(availability: true);
                }
                else
                {
                    FreeSlotText = Cal.NoFreeSlot;
                    Notify();
                }
            });
    }

    // Attachments

    /// <summary>Attach: attachment.import of a file the user picked, under the calendar's account.</summary>
    public void Attach(string path)
    {
        ArgumentNullException.ThrowIfNull(path);
        scope.VerifyAccess();
        if ((CalendarAccount?.AccountId ?? (Calendars is { Count: > 0 } c ? c[0].AccountId : (AccountId?)null)) is not { } account)
        {
            return;
        }
        if (attachments.Count >= API.Limits.MaxEventAttachments)
        {
            return;
        }
        var source = Source;
        var parameters = new AttachmentImportParams { AccountId = account, Path = path };
        scope.Perform(
            _ => source.ImportAttachmentAsync(parameters),
            outcome =>
            {
                if (outcome.TryGetValue(out var r, out var error))
                {
                    attachments.Add(r.Attachment);
                    Edited();
                }
                else
                {
                    Error = Cal.ErrorMessage(error, L10n.T("Saving the attachment"));
                    Notify();
                }
            });
    }

    /// <summary>Removes the attachment <paramref name="id"/>.</summary>
    public void RemoveAttachment(string id)
    {
        if (attachments.RemoveAll(a => string.Equals(a.Id, id, StringComparison.Ordinal)) > 0)
        {
            Edited();
        }
    }

    // Save and close

    /// <summary>
    /// The primary button: validates, then event.create or event.update.
    /// True when it was stored (the window then closes).
    /// </summary>
    public async Task<bool> SaveAsync()
    {
        scope.VerifyAccess();
        if (Saving)
        {
            return false;
        }
        var p = Cal.Validate(Input).ToList();
        if (Attendees.HasInvalid && !p.Contains(Cal.Problem.InvalidAttendee))
        {
            p.Add(Cal.Problem.InvalidAttendee);
        }
        Problems = p;
        if (p.Count > 0 || Input.CalendarId is not { } cal)
        {
            Notify();
            return false;
        }
        Saving = true;
        Error = "";
        Notify();
        try
        {
            var s = Source.Snapshot;
            var account = s.FindCalendar(cal)?.AccountId ?? new AccountId("");
            var hasAttendees = Input.Attendees.Count > 0;
            var allDay = Input.AllDay;
            var o = Cal.BareOccurrence(
                title: Input.Title,
                start: allDay ? null : Input.Start,
                end: allDay ? null : Input.End,
                startDate: allDay ? Cal.DateOf(Input.Start, Input.Zone) : default(CalendarDate?),
                endDate: allDay ? Cal.DateOf(Input.End, Input.Zone) : default(CalendarDate?),
                allDay: allDay,
                location: Input.Location.Length == 0 ? null : Input.Location);
            CalendarEvent saved;
            if (EditorMode is Mode.EditEvent { Edited: var e, Scope: var editScope })
            {
                var mail = hasAttendees ? Cal.MailTexts(Cal.MailKind.Update, o, s.MyName(account), Env) : null;
                if (e.EventId is not { } id)
                {
                    return false;
                }
                saved = (await Source.UpdateEventAsync(new EventUpdateParams
                {
                    EventId = id,
                    RecurrenceId = editScope == EditScope.All ? null : e.RecurrenceId,
                    Scope = editScope,
                    ETag = e.ETag,
                    Event = EventInput,
                    SendUpdates = hasAttendees ? true : null,
                    Mail = mail,
                })).Event;
            }
            else
            {
                var mail = hasAttendees ? Cal.MailTexts(Cal.MailKind.Invitation, o, s.MyName(account), Env) : null;
                saved = (await Source.CreateEventAsync(new EventCreateParams
                {
                    CalendarId = cal,
                    Event = EventInput,
                    SendInvitations = hasAttendees ? true : null,
                    Mail = mail,
                })).Event;
            }
            if (IsClosed)
            {
                return true;
            }
            Dirty = false;
            scope.Raise(Saved, this, saved);
            return true;
        }
#pragma warning disable CA1031 // Every failure of Save is its inline error, as Swift's catch.
        catch (Exception error)
#pragma warning restore CA1031
        {
            if (IsClosed)
            {
                return false;
            }
            Error = Cal.ErrorMessage(error);
            return false;
        }
        finally
        {
            Saving = false;
            Notify();
        }
    }

    /// <summary>
    /// After a conflict, Reload Event: the event as the daemon has it now
    /// (event.get with reload), for a new editor in this window's place (the
    /// macOS EventEditorManager.reload, whose window closes unasked: the
    /// user chose to drop the edits); null for New Event or when it fails.
    /// </summary>
    public async Task<CalendarEvent?> ReloadEventAsync()
    {
        scope.VerifyAccess();
        if (EditorMode is not Mode.EditEvent { Edited: var e } || e.EventId is not { } id)
        {
            return null;
        }
        try
        {
            return await Source.EventAsync(id, e.RecurrenceId, reload: true);
        }
#pragma warning disable CA1031 // Swift's try?: no event, no new window.
        catch (Exception)
#pragma warning restore CA1031
        {
            return null;
        }
    }

    /// <summary>
    /// The footer's Delete: the detail's flow with the scope the editor was
    /// opened with — the remove question (confirm = tell the organiser /
    /// attendees, alternative = remove only), else the delete question when
    /// <see cref="ConfirmDelete"/> asks, then event.delete. Success closes the
    /// window without the unsaved question; a refusal shows inline. True when
    /// it was deleted.
    /// </summary>
    public async Task<bool> DeleteAsync()
    {
        scope.VerifyAccess();
        if (EditorMode is not Mode.EditEvent { Edited: var e, Scope: var editScope } || !e.CanDelete || e.EventId is not { } id || Saving)
        {
            return false;
        }
        bool? notify = null;
        if (Cal.RemoveQuestion(e) is { } q)
        {
            switch (await AskDeleteAsync(q))
            {
                case Cal.Answer.Confirm:
                    notify = true;
                    break;
                case Cal.Answer.Alternative:
                    notify = false;
                    break;
                default:
                    return false;
            }
        }
        else if (ConfirmDelete())
        {
            if (await AskDeleteAsync(Cal.DeleteQuestion(e)) != Cal.Answer.Confirm)
            {
                return false;
            }
        }
        if (IsClosed)
        {
            return false;
        }
        var s = Source.Snapshot;
        MailText? mail = null;
        if (notify == true)
        {
            var kind = e.RemoveIsDelete ? Cal.MailKind.Cancel : Cal.MailKind.Declined;
            mail = Cal.MailTexts(kind, e, s.MyName(e.AccountId), Env);
        }
        var parameters = new EventDeleteParams
        {
            EventId = id,
            RecurrenceId = e.RecurrenceId,
            Scope = editScope,
            ETag = e.ETag,
            Notify = notify,
            Mail = mail,
        };
        Saving = true;
        Error = "";
        Notify();
        try
        {
            await Source.DeleteEventAsync(parameters);
            if (IsClosed)
            {
                return true;
            }
            Dirty = false;
            RaiseEmpty(CloseRequested);
            return true;
        }
#pragma warning disable CA1031 // Every failure of Delete is its inline error, as Swift's catch.
        catch (Exception error)
#pragma warning restore CA1031
        {
            if (IsClosed)
            {
                return false;
            }
            Error = Cal.ErrorMessage(error);
            return false;
        }
        finally
        {
            Saving = false;
            Notify();
        }
    }

    /// <summary>
    /// The window's close button: with changes asks Save / Discard / Cancel.
    /// True when the window may close.
    /// </summary>
    public async Task<bool> CloseAsync()
    {
        scope.VerifyAccess();
        if (!Dirty)
        {
            RaiseEmpty(CloseRequested, always: true);
            return true;
        }
        switch (await AskAsync(AskQuestion, Cal.CloseQuestion))
        {
            case Cal.Answer.Alternative:
                Dirty = false;
                RaiseEmpty(CloseRequested, always: true);
                return true;
            case Cal.Answer.Confirm:
                if (!await SaveAsync())
                {
                    return false;
                }
                RaiseEmpty(CloseRequested, always: true);
                return true;
            default:
                return false;
        }
    }

    // Helpers

    private static IReadOnlyList<int> AlertList(int minutes) => minutes >= 0 ? [minutes] : [];

    // Swift's == of two contract values: the same JSON.
    private static bool SameValue<T>(T? a, T? b)
        where T : class
    {
        if (a is null || b is null)
        {
            return a is null && b is null;
        }
        return JsonCoding.Encode(a).AsSpan().SequenceEqual(JsonCoding.Encode(b));
    }

    private void Edited(bool availability = false)
    {
        Dirty = true;
        Problems = [];
        Error = "";
        if (availability)
        {
            ScheduleAvailability();
        }
        Notify();
    }

    private Task<Cal.Answer> AskDeleteAsync(Cal.Question q) => AskAsync(AskDeleteQuestion ?? AskQuestion, q);

    // The hook's answer; Cancel without one or when it fails.
    private async Task<Cal.Answer> AskAsync(Func<Cal.Question, Task<Cal.Answer>>? ask, Cal.Question q)
    {
        if (ask is null)
        {
            return Cal.Answer.Cancel;
        }
        try
        {
            return await ask(q);
        }
#pragma warning disable CA1031 // A failed question is a Cancel, and reported.
        catch (Exception error)
#pragma warning restore CA1031
        {
            scope.Pending.Report(error);
            return Cal.Answer.Cancel;
        }
    }

    private void Notify() => RaiseEmpty(Changed);

    // Raises a parameterless event, every handler guarded (ControllerEvents);
    // nothing once closed unless always (Swift's onClose has no guard).
    private void RaiseEmpty(EventHandler? handlers, bool always = false)
    {
        if ((IsClosed && !always) || handlers is null)
        {
            return;
        }
        foreach (var handler in handlers.GetInvocationList())
        {
            scope.Guard(() => ((EventHandler)handler).Invoke(this, EventArgs.Empty));
        }
    }

    /// <summary>New Event or Edit Event (Swift <c>EventEditorController.Mode</c>).</summary>
    public abstract record Mode
    {
        // Only the cases below derive from it.
        private Mode()
        {
        }

        /// <summary>Swift <c>.new</c>: a new event.</summary>
        public sealed record NewEvent : Mode;

        /// <summary>Swift <c>.edit</c>: the event and the scope the detail's series question chose.</summary>
        /// <param name="Edited">The event as event.get answered.</param>
        /// <param name="Scope">The occurrences the change is for.</param>
        public sealed record EditEvent(CalendarEvent Edited, EditScope Scope) : Mode;
    }

    /// <summary>A choice of the Calendar popup.</summary>
    /// <param name="Id">The calendar.</param>
    /// <param name="AccountId">Its account.</param>
    /// <param name="Title">"Account · Calendar".</param>
    /// <param name="Color">Its colour (#rrggbb).</param>
    public sealed record CalendarChoice(CalendarId Id, AccountId AccountId, string Title, string Color);
}
