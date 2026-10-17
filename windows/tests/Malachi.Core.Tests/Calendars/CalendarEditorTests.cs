// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarEditorTests.swift; GTK:
// ui/internal/calendar/editor_test.go (TestNewEventDefaults, TestValidate,
// TestEventInput, TestPrimaryLabelAndFooter, TestAlertAndDurationTitles,
// TestMailTexts, TestScopeQuestion, TestAllDayAlerts, TestEditorDelete),
// with Swift's bareAddresses. The preferences are api.DefaultCalendarPreferences
// written out here (Prefs), so that the suite stands on the editor alone.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarEditorTests
{
    private static readonly TimeZoneInfo P = F.Prague;

    // api.DefaultCalendarPreferences.
    private static readonly CalendarPreferences Prefs = new()
    {
        WeekStart = WeekStart.Locale,
        DefaultDurationMinutes = 60,
        DefaultAlertMinutes = 15,
        ShowDeclined = false,
        WorkingHours = new WorkingHours { Days = ["MO", "TU", "WE", "TH", "FR"], Start = "08:00", End = "17:00" },
    };

    private static CalendarPreferences PrefsCase(string name) => name switch
    {
        "noAlert" => Prefs with { DefaultAlertMinutes = -1, DefaultDurationMinutes = 30 },
        "badDuration" => Prefs with { DefaultDurationMinutes = 0 },
        _ => Prefs,
    };

    [Theory]
    [InlineData("next half hour", "2026-10-14T10:07", Cal.Origin.Now, "", "2026-10-14T10:30", "2026-10-14T11:30", false, 15)]
    [InlineData("an exact half hour stays", "2026-10-14T10:30", Cal.Origin.Now, "", "2026-10-14T10:30", "2026-10-14T11:30", false, 15)]
    [InlineData("into tomorrow", "2026-10-14T23:45", Cal.Origin.Now, "noAlert", "2026-10-15", "2026-10-15T00:30", false, null)]
    [InlineData("spring gap", "2026-03-29T01:45", Cal.Origin.Now, "", "2026-03-29T03:00", "2026-03-29T04:00", false, 15)]
    [InlineData("slot", "2026-10-14T14:40", Cal.Origin.Slot, "", "2026-10-14T14:30", "2026-10-14T15:30", false, 15)]
    [InlineData("month cell", "2026-10-20", Cal.Origin.MonthCell, "badDuration", "2026-10-20T09:00", "2026-10-20T10:00", false, 15)]
    [InlineData("all-day row", "2026-10-25T13:00", Cal.Origin.AllDay, "", "2026-10-25", "2026-10-26", true, 900)]
    [InlineData("all-day row without an alert", "2026-10-25T13:00", Cal.Origin.AllDay, "noAlert", "2026-10-25", "2026-10-26", true, null)]
    public void NewEventDefaults(string name, string at, Cal.Origin origin, string prefs, string start, string end, bool allDay, int? alert)
    {
        var d = Cal.NewEventDefaults(F.Local(at), origin, PrefsCase(prefs), P);
        Assert.True(d.Start == F.Local(start) && d.End == F.Local(end), $"{name}: {d.Start:o}–{d.End:o}");
        Assert.Equal(allDay, d.AllDay);
        Assert.Equal(alert is { } a ? [a] : [], d.Alerts);
    }

    [Fact]
    public void NewEventDefaultsSecondsPast()
    {
        var d = Cal.NewEventDefaults(F.At(2026, 10, 14, 10, 30).AddSeconds(1), Cal.Origin.Now, Prefs, P);
        Assert.Equal(F.At(2026, 10, 14, 11), d.Start);
        Assert.Equal(F.At(2026, 10, 14, 12), d.End);
        Assert.Equal(TimeSpan.FromHours(1), Cal.DefaultDuration(Prefs));
        Assert.Equal(TimeSpan.FromHours(1), Cal.DefaultDuration(Prefs with { DefaultDurationMinutes = 1441 }));
        Assert.Equal(TimeSpan.FromMinutes(5), Cal.DefaultDuration(Prefs with { DefaultDurationMinutes = 5 }));
    }

    private static readonly Cal.Input Ok = new()
    {
        Title = "T",
        Start = F.At(2026, 10, 14, 10),
        End = F.At(2026, 10, 14, 11),
        Zone = P,
        Attendees = [new EventInputAttendee { Address = "jana@example.org" }],
        Alerts = [15],
        CalendarId = new CalendarId("cal"),
    };

    private static Cal.Input InputCase(string name) => name switch
    {
        "valid" => Ok,
        "no calendar" => Ok with { CalendarId = null },
        "end at start" => Ok with { End = Ok.Start },
        "end before start" => Ok with { End = Ok.Start.AddMinutes(-1) },
        "all-day one day" => Ok with { AllDay = true, End = Cal.AddDays(Cal.StartOfDay(Ok.Start, P), 1, P) },
        "all-day same day" => Ok with { AllDay = true, End = Ok.Start },
        "long title" => Ok with { Title = new string('x', API.Limits.MaxEventTitleBytes + 1) },
        "title of spaces" => Ok with { Title = new string(' ', API.Limits.MaxEventTitleBytes + 10) },
        "long location" => Ok with { Location = new string('x', API.Limits.MaxEventLocationBytes + 1) },
        "long notes" => Ok with { Notes = new string('x', API.Limits.MaxEventNotesBytes + 1) },
        // Windows-only: the limits are UTF-8 bytes, not UTF-16 characters.
        "long title in bytes" => Ok with { Title = new string('é', API.Limits.MaxEventTitleBytes / 2 + 1) },
        "bad attendee" => Ok with
        {
            Attendees = [new EventInputAttendee { Address = "jana" }, new EventInputAttendee { Address = "x y@z" }],
        },
        "too many attendees" => Ok with
        {
            Attendees = [.. Enumerable.Range(0, API.Limits.MaxEventAttendeesIn + 1).Select(i => new EventInputAttendee { Address = $"a{i}@example.org" })],
        },
        "too many alerts" => Ok with { Alerts = [1, 2, 3, 4, 5] },
        "several" => Ok with { CalendarId = null, End = Ok.Start },
        _ => throw new ArgumentOutOfRangeException(nameof(name)),
    };

    [Theory]
    [InlineData("valid")]
    [InlineData("no calendar", Cal.Problem.NoCalendar)]
    [InlineData("end at start", Cal.Problem.EndBeforeStart)]
    [InlineData("end before start", Cal.Problem.EndBeforeStart)]
    [InlineData("all-day one day")]
    [InlineData("all-day same day", Cal.Problem.EndBeforeStart)]
    [InlineData("long title", Cal.Problem.TitleTooLong)]
    [InlineData("title of spaces")]
    [InlineData("long location", Cal.Problem.LocationTooLong)]
    [InlineData("long notes", Cal.Problem.NotesTooLong)]
    [InlineData("long title in bytes", Cal.Problem.TitleTooLong)]
    [InlineData("bad attendee", Cal.Problem.InvalidAttendee)]
    [InlineData("too many attendees", Cal.Problem.TooManyAttendees)]
    [InlineData("too many alerts", Cal.Problem.TooManyAlerts)]
    [InlineData("several", Cal.Problem.NoCalendar, Cal.Problem.EndBeforeStart)]
    public void Validate(string name, params Cal.Problem[] want) => Assert.Equal(want, Cal.Validate(InputCase(name)));

    [Fact]
    public void ProblemTexts()
    {
        Assert.Equal("An event can have at most 100 attendees.", Cal.ProblemText(Cal.Problem.TooManyAttendees));
        Assert.Equal("An event can have at most 4 alerts.", Cal.ProblemText(Cal.Problem.TooManyAlerts));
        Assert.All(Enum.GetValues<Cal.Problem>(), p => Assert.NotEqual("", Cal.ProblemText(p)));
    }

    // Swift's bareAddresses: Go's mail.ParseAddress with Address equal to the input.
    [Theory]
    [InlineData("jana@example.org")]
    [InlineData("a.b+c@sub.example.org")]
    [InlineData("žluť@example.cz")]
    [InlineData("x@[127.0.0.1]")]
    [InlineData("o'brien@example.ie")]
    public void BareAddresses(string good) => Assert.True(Cal.IsBareAddress(good), good);

    [Theory]
    [InlineData("jana")]
    [InlineData("x y@z")]
    [InlineData(" a@b")]
    [InlineData("a@b ")]
    [InlineData(".a@b")]
    [InlineData("a.@b")]
    [InlineData("a..b@c")]
    [InlineData("a@")]
    [InlineData("@b")]
    [InlineData("\"a\"@b")]
    [InlineData("Jana <j@x.org>")]
    [InlineData("a@b@c")]
    [InlineData("a@b.")]
    [InlineData("a@[x")]
    [InlineData("a\u0000@b")]
    [InlineData("")]
    public void NotBareAddresses(string bad) => Assert.False(Cal.IsBareAddress(bad), bad);

    [Fact]
    public void EventInput()
    {
        var input = new Cal.Input
        {
            Title = "  Planning ",
            Start = F.At(2026, 10, 14, 10),
            End = F.At(2026, 10, 14, 11, 30),
            Zone = P,
            Location = " B ",
        };
        var got = Cal.EventInput(input, P);
        Assert.Equal("Planning", got.Title);
        Assert.Equal(new LocalDateTime("2026-10-14T10:00:00"), got.Start);
        Assert.Equal(new LocalDateTime("2026-10-14T11:30:00"), got.End);
        Assert.Equal(new TimeZoneName("Europe/Prague"), got.TimeZone);
        Assert.Equal("B", got.Location);
        Assert.Equal([], got.Alerts!);
        Assert.Null(got.StartDate);
        Assert.Null(got.Notes);
        Assert.Null(got.Attendees);
        input = input with { AllDay = true, End = F.At(2026, 10, 16) };
        got = Cal.EventInput(input, P);
        Assert.Equal(new CalendarDate("2026-10-14"), got.StartDate);
        Assert.Equal(new CalendarDate("2026-10-16"), got.EndDate);
        Assert.Null(got.Start);
        Assert.Null(got.TimeZone);
        Assert.Equal(new CalendarDate("2026-10-15"), Cal.EventInput(input with { End = input.Start }, P).EndDate);
    }

    // Windows-only: TimeZoneInfo.Local carries a Windows id, the API IANA names.
    [Fact]
    public void ZoneNamesAreIana()
    {
        Assert.Equal("Europe/Prague", Cal.IanaZoneName(P));
        var windows = TimeZoneInfo.FindSystemTimeZoneById("Central Europe Standard Time");
        Assert.Equal("Europe/Budapest", Cal.IanaZoneName(windows));
        var input = new Cal.Input { Start = F.At(2026, 10, 14, 10), End = F.At(2026, 10, 14, 11), Zone = windows };
        Assert.Equal(new TimeZoneName("Europe/Budapest"), Cal.EventInput(input, windows).TimeZone);
    }

    private static CalendarFeatures Features(string via) =>
        new() { FreeBusy = false, ProposeNewTime = false, Invitations = new InvitationsVia(via), OnlineMeetings = false };

    [Theory]
    [InlineData(0, CalendarSource.Graph, InvitationsVia.Server, "Save")]
    [InlineData(2, CalendarSource.Graph, InvitationsVia.None, "Save and Send Invitations")]
    [InlineData(2, CalendarSource.CalDAV, InvitationsVia.Mail, "Save and Send Invitations")]
    [InlineData(2, CalendarSource.CalDAV, InvitationsVia.Server, "Save and Send Invitations")]
    [InlineData(2, CalendarSource.CalDAV, InvitationsVia.None, "Save")]
    public void PrimaryLabel(int n, string source, string via, string want)
    {
        Assert.Equal(want, Cal.PrimaryLabel(n, new CalendarSource(source), Features(via)));
        Assert.Equal(want != "Save", Cal.SendsInvitations(n, new CalendarSource(source), Features(via)));
    }

    [Fact]
    public void Footer()
    {
        Assert.Equal(
            ["Invitations leave by e-mail from account Gmail (me@example.com) through the Outbox.", "Attachment: agenda.pdf"],
            Cal.Footer(1, Features(InvitationsVia.Mail), "Gmail", "me@example.com", ["agenda.pdf"]));
        Assert.Equal(["The server sends the invitations."], Cal.Footer(1, Features(InvitationsVia.Server), "", "", null));
        Assert.Equal(["No account can send invitations from this calendar."], Cal.Footer(3, Features(InvitationsVia.None), "", "", []));
        Assert.Empty(Cal.Footer(0, Features(InvitationsVia.Mail), "Gmail", "x", []));
        // Windows-only: a file's or an account's name is display text.
        Assert.Equal(["Attachment: evil.exe.pdf"], Cal.Footer(0, Features(InvitationsVia.Mail), "", "", ["evil\u202E.exe.pdf"]));
    }

    [Theory]
    [InlineData(-1, "None")]
    [InlineData(0, "At time of event")]
    [InlineData(1, "1 minute before")]
    [InlineData(5, "5 minutes before")]
    [InlineData(60, "1 hour before")]
    [InlineData(120, "2 hours before")]
    [InlineData(90, "90 minutes before")]
    [InlineData(1440, "1 day before")]
    [InlineData(2880, "2 days before")]
    public void AlertTitles(int minutes, string want) => Assert.Equal(want, Cal.AlertTitle(minutes));

    [Fact]
    public void DurationTitles()
    {
        Assert.Equal(["15 min", "30 min", "45 min", "1 h", "1 h 30 min", "2 h"], Cal.DurationChoices.Select(Cal.DurationTitle));
        Assert.Equal([-1, 0, 5, 10, 15, 30, 60, 1440], Cal.AlertChoices);
    }

    [Theory]
    [InlineData(900, true, "1 day before (9:00)")]
    [InlineData(2340, true, "2 days before (9:00)")]
    [InlineData(9540, true, "1 week before (9:00)")]
    [InlineData(1080, true, "18 hours before")]
    [InlineData(-1, true, "None")]
    [InlineData(0, true, "At time of event")]
    [InlineData(900, false, "15 hours before")]
    [InlineData(3780, true, "3 days before (9:00)")]
    public void AllDayAlertTitles(int minutes, bool allDay, string want) => Assert.Equal(want, Cal.AlertTitleFor(minutes, allDay));

    [Theory]
    [InlineData(15, true, "", 900)]
    [InlineData(-1, true, "", -1)]
    [InlineData(2340, true, "", 2340)]
    [InlineData(0, true, "", 900)]
    [InlineData(900, false, "", 15)]
    [InlineData(-1, false, "", -1)]
    [InlineData(9540, false, "none", -1)]
    public void AlertOnAllDayToggle(int current, bool on, string prefs, int want) =>
        Assert.Equal(want, Cal.AlertOnAllDayToggle(current, on, prefs == "none" ? Prefs with { DefaultAlertMinutes = -1 } : Prefs));

    [Fact]
    public void AlertChoicesFor()
    {
        Assert.Same(Cal.AllDayAlertChoices, Cal.AlertChoicesFor(true));
        Assert.Same(Cal.AlertChoices, Cal.AlertChoicesFor(false));
        Assert.Equal([-1, 900, 2340, 9540], Cal.AllDayAlertChoices);
        Assert.Equal(900, Cal.AllDayAlertDefault);
    }

    [Theory]
    [InlineData("new event", false, true, true, null)]
    [InlineData("no canDelete", true, false, true, null)]
    [InlineData("own event", true, true, true, "Delete")]
    [InlineData("invitation", true, true, false, "Remove from Calendar")]
    public void EditorDelete(string name, bool editing, bool canDelete, bool removeIsDelete, string? want)
    {
        var e = F.Event() with
        {
            CanDelete = canDelete,
            RemoveIsDelete = removeIsDelete,
            MyResponse = removeIsDelete ? default(EventResponse?) : new EventResponse(EventResponse.Accepted),
        };
        Assert.True(want == Cal.EditorDelete(editing, e), name);
    }

    [Fact]
    public void MailTexts()
    {
        var env = F.Env();
        var o = F.Timed("x", F.At(2026, 10, 13, 10), F.At(2026, 10, 13, 11)) with { Title = "Q4 planning", Location = "Room B" };
        var subjects = new Dictionary<Cal.MailKind, string>
        {
            [Cal.MailKind.Invitation] = "Invitation: Q4 planning",
            [Cal.MailKind.Update] = "Updated invitation: Q4 planning",
            [Cal.MailKind.Cancel] = "Cancelled: Q4 planning",
            [Cal.MailKind.Accepted] = "Accepted: Q4 planning",
            [Cal.MailKind.Tentative] = "Tentative: Q4 planning",
            [Cal.MailKind.Declined] = "Declined: Q4 planning",
        };
        foreach (var (k, want) in subjects)
        {
            Assert.Equal(want, Cal.MailTexts(k, o, "Me", env).Subject);
        }
        Assert.Equal(
            "Jana has accepted the invitation to “Q4 planning”.\n\nWhen: Tuesday 13 October 2026 · 10:00–11:00 · 1 h\nWhere: Room B",
            Cal.MailTexts(Cal.MailKind.Accepted, o, "Jana", env).Text);
        var bare = Cal.MailTexts(Cal.MailKind.Invitation, F.Occ(), "", env);
        Assert.Equal("Invitation: Untitled Event", bare.Subject);
        Assert.Equal("You are invited to “Untitled Event”.", bare.Text);
        var longTitle = Cal.MailTexts(Cal.MailKind.Invitation, o with { Title = new string('é', API.Limits.MaxMailTextBytes) }, "", env);
        Assert.True(Encoding.UTF8.GetByteCount(longTitle.Subject) <= API.Limits.MaxMailTextBytes);
        Assert.StartsWith("Invitation: ", longTitle.Subject, StringComparison.Ordinal);
        Assert.Equal(Cal.MailKind.Declined, Cal.MailKindOf(EventResponse.Declined));
        Assert.Equal(Cal.MailKind.Tentative, Cal.MailKindOf(EventResponse.Tentative));
        Assert.Equal(Cal.MailKind.Accepted, Cal.MailKindOf(EventResponse.Accepted));
        Assert.Equal("a", Cal.CapBytes("aé", 2));
        Assert.Equal("abc", Cal.CapBytes("abc", 5));
        Assert.Equal("", Cal.CapBytes("é", 1));
    }

    [Theory]
    [InlineData(Cal.ScopeAction.Edit, false, "Change a Repeating Event?", EditScope.This, EditScope.ThisAndFuture, EditScope.All)]
    [InlineData(Cal.ScopeAction.Delete, false, "Delete a Repeating Event?", EditScope.This, EditScope.ThisAndFuture, EditScope.All)]
    [InlineData(Cal.ScopeAction.Respond, false, "Answer for the Whole Series?", EditScope.This, EditScope.All)]
    [InlineData(Cal.ScopeAction.Edit, true, "Change a Repeating Event?", EditScope.All)]
    [InlineData(Cal.ScopeAction.Delete, true, "Delete a Repeating Event?", EditScope.All)]
    [InlineData(Cal.ScopeAction.Respond, true, "Answer for the Whole Series?", EditScope.This, EditScope.All)]
    public void ScopeQuestion(Cal.ScopeAction a, bool complex, string heading, params string[] want)
    {
        var q = Cal.ScopeQuestionFor(a, complex);
        Assert.Equal(heading, q.Heading);
        Assert.Equal(want, q.Choices.Select(c => c.Scope.Value));
        Assert.Equal(
            complex && a != Cal.ScopeAction.Respond,
            q.Body == "This event repeats in a way that can be changed only for the whole series.");
    }

    [Fact]
    public void ScopeTitlesAndCloseQuestion()
    {
        var q = Cal.ScopeQuestionFor(Cal.ScopeAction.Edit, false);
        Assert.Equal(["This Event Only", "This and Future Events", "All Events"], q.Choices.Select(c => c.Title));
        var c = Cal.CloseQuestion;
        Assert.Equal("Save", c.Confirm);
        Assert.Equal("Discard", c.Alternative);
        Assert.Equal("Cancel", Cal.Cancel);
    }

    [Fact]
    public void FieldsAndLabels()
    {
        Assert.Equal(Enum.GetValues<Cal.Field>(), Cal.Fields);
        Assert.Equal(
            ["Calendar:", "All Day:", "Starts:", "Ends:", "Repeat:", "Location:", "Attendees:", "Availability:", "Alert:", "Notes:"],
            Cal.Fields.Select(Cal.FieldLabel));
        Assert.Equal("Microsoft Teams", Cal.VideoProviderName("teamsForBusiness"));
        Assert.Equal("Microsoft Teams", Cal.VideoProviderName("teamsForConsumer"));
        Assert.Equal("Skype for Business", Cal.VideoProviderName("skypeForBusiness"));
        Assert.Equal("Skype", Cal.VideoProviderName("skypeForConsumer"));
        Assert.Equal("Online Meeting", Cal.VideoProviderName("zoom"));
        Assert.Equal("Time zone: Europe/Prague", Cal.TimeZoneLink("Europe/Prague"));
        Assert.Equal(9, Cal.MonthCellHour);
    }
}
