// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows addition, ahead of a Swift port (APICodingTests.swift has no
// calendar yet): the calendar of docs/api.md §4.14 to §4.16 and its errors
// (§2: readOnly, invitationsUnavailable, calendarNotFound, eventNotFound,
// calendarConsentRequired, eventGone, malformedCalendar,
// recurrenceUnsupported) through the typed layer, with invented data. The
// samples of every record are in ApiRoundTripTests; here are the parts that
// are more than a round trip: the flat Event and InvitationCard, the one
// "invitation" key of Message, DateOrTime, what the params leave out, the
// error data and the notifications.

using System;
using System.Text.Json;
using Malachi.Core.Api;
using Xunit;
using static Malachi.Core.Tests.Api.ApiJson;

namespace Malachi.Core.Tests.Api;

public sealed class CalendarRoundTripTests
{
    private const string Card = """
        {"method":"request","kind":"invitation","state":"unanswered","title":"Workshop","partId":"2","uid":"u@example.org",
         "sequence":0,"attendeeCount":1,"conflicts":[],"day":{"from":"2026-10-14T00:00:00Z","to":"2026-10-15T00:00:00Z","busy":[]},
         "targets":[{"calendarId":"cal_1","isDefault":true}],"canRespond":true,"canProposeNewTime":false,"replyVia":"mail"}
        """;

    private const string MinimalEvent = """
        {"accountId":"acc_1","title":"Standup","allDay":false,"status":"confirmed","showAs":"busy","etag":"v2",
         "canEdit":true,"canDelete":true,"canRespond":false,"canProposeNewTime":false,"removeIsDelete":false}
        """;

    [Fact]
    public void DateOrTimeIsADateOrAnInstant()
    {
        var date = new DateOrTime("2026-10-14");
        Assert.True(date.IsDate);
        Assert.Null(date.Instant);
        var time = new DateOrTime("2026-10-14T08:00:00Z");
        Assert.False(time.IsDate);
        Assert.Equal(Rfc3339.Parse("2026-10-14T08:00:00Z"), time.Instant);
        Assert.Null(new DateOrTime("not an instant, and not a date").Instant);
        Assert.Equal("\"2026-10-14\"", JsonCoding.EncodeToString<DateOrTime>("2026-10-14"));
        Assert.True(Decode<DateOrTime>("\"2026-10-14\"").IsDate);

        var all = Decode<Occurrence>("""{"accountId":"acc_1","title":"","allDay":true,"status":"confirmed","showAs":"free","recurrenceId":"2026-10-14"}""");
        Assert.True(all.RecurrenceId!.Value.IsDate);
        var timed = Decode<Occurrence>("""{"accountId":"acc_1","title":"","allDay":false,"status":"confirmed","showAs":"free","recurrenceId":"2026-10-14T08:00:00Z"}""");
        Assert.False(timed.RecurrenceId!.Value.IsDate);
    }

    [Fact]
    public void DiscoverCalDavPinIsOptional()
    {
        Assert.Equal(["url"], Keys(EncodeObject(new AccountDiscoverCalDAVParams { Url = "cloud.example.org" })));
        var pinned = EncodeObject(new AccountDiscoverCalDAVParams { Url = "cloud.example.org", CertificateSha256 = "ab" });
        Assert.Equal(["certificateSha256", "url"], Keys(pinned));
        Assert.Null(Decode<AccountDiscoverCalDAVParams>("""{"url":"x"}""").CertificateSha256);
        Assert.Equal("ab", Decode<AccountDiscoverCalDAVParams>("""{"url":"x","certificateSha256":"ab"}""").CertificateSha256);
    }

    [Fact]
    public void CalendarConfigDefaultsAreTrue()
    {
        var config = new CalendarConfig { Sync = true };
        Assert.True(config.ShowsUnanswered);
        Assert.True(config.ShowsInvitationCard);
        Assert.False((config with { ShowUnanswered = false }).ShowsUnanswered);
        Assert.False((config with { InvitationCard = false }).ShowsInvitationCard);
        Assert.Equal(["sync"], Keys(EncodeObject(config))); // the other members stay out when null
    }

    [Fact]
    public void CalDavAccountConfigCarriesItsBlocks()
    {
        var config = new AccountConfig
        {
            Name = "Cloud",
            Email = "jana@example.org",
            Kind = AccountKind.CalDAV,
            CalDAV = new CalDAVConfig { ServerUrl = "https://cloud.example.org/remote.php/dav", Username = "jana" },
            Calendar = new CalendarConfig { Sync = true },
        };
        var json = EncodeObject(config);
        Assert.Equal("caldav", json.GetProperty("kind").GetString());
        Assert.Equal("jana", json.GetProperty("caldav").GetProperty("username").GetString());
        Assert.True(json.GetProperty("calendar").GetProperty("sync").GetBoolean());
        Assert.Null(Member(json, "imap"));
        Assert.Equal(AccountKind.CalDAV, config.ProtocolKind.Value);
        Assert.Equal("calendar", Capability.Calendar);

        var start = EncodeObject(new AccountOAuthStartParams { AccountId = "acc_1" });
        Assert.Equal(["accountId"], Keys(start));
        var calendar = EncodeObject(new AccountOAuthStartParams { AccountId = "acc_1", Calendar = true });
        Assert.True(calendar.GetProperty("calendar").GetBoolean());

        var test = Decode<AccountTestResult>("""{"caldav":{"ok":true,"capabilities":["calendar-access","calendar-auto-schedule","sync-collection"],"latencyMs":40}}""");
        Assert.Equal(3, test.CalDAV!.Capabilities!.Count);
        Assert.Null(test.Imap);
    }

    [Fact]
    public void EventAndCardStayFlat()
    {
        var e = Decode<CalendarEvent>(MinimalEvent);
        Assert.IsAssignableFrom<Occurrence>(e);
        Assert.Equal("Standup", e.Title);
        Assert.Equal("v2", e.ETag);
        var written = EncodeObject(e);
        Assert.Equal("Standup", written.GetProperty("title").GetString());
        Assert.Equal("v2", written.GetProperty("etag").GetString());
        Assert.Null(Member(written, "occurrence"));

        var card = Decode<InvitationCard>(Card);
        Assert.IsAssignableFrom<InvitationSummary>(card);
        Assert.Equal(InvitationKind.Invitation, card.Kind.Value);
        Assert.Equal(InvitationState.Unanswered, card.State.Value);
        Assert.Equal(InvitationsVia.Mail, card.ReplyVia.Value);
        Assert.Equal("cal_1", Assert.Single(card.Targets).CalendarId);
        Assert.Empty(card.Conflicts);
        Assert.Null(card.Attendees);
        var cardJson = EncodeObject(card);
        Assert.Equal("Workshop", cardJson.GetProperty("title").GetString());
        Assert.Equal("2", cardJson.GetProperty("partId").GetString());
        Assert.Null(Member(cardJson, "summary"));
    }

    [Fact]
    public void MessageHoldsTheCardUnderTheKeyOfTheSummary()
    {
        var json = $$$"""
            {"id":"m_1","accountId":"acc_1","folderId":"f_inbox","from":[],"subject":"Workshop","date":"2026-10-14T07:00:00Z",
             "snippet":"","flags":[],"hasAttachments":true,"size":10,"attachments":[],"invitation":{{{Card}}}}
            """;
        var message = Decode<Message>(json);
        Assert.Equal(InvitationMethod.Request, message.Invitation!.Method.Value);
        Assert.Equal("2", message.Invitation.PartId);
        Assert.Null(message.Summary.Invitation); // the card shadows the summary, as in Go

        var text = JsonCoding.EncodeToString(message);
        var first = text.IndexOf("\"invitation\":", StringComparison.Ordinal);
        Assert.True(first >= 0);
        Assert.Equal(-1, text.IndexOf("\"invitation\":", first + 1, StringComparison.Ordinal)); // one key, not two
        Assert.Equal("2", EncodeObject(message).GetProperty("invitation").GetProperty("partId").GetString());

        // A list row carries the summary only.
        var row = Decode<MessageSummary>("""
            {"id":"m_1","accountId":"acc_1","folderId":"f_inbox","subject":"Workshop","date":"2026-10-14T07:00:00Z",
             "snippet":"","hasAttachments":true,"size":10,
             "invitation":{"method":"cancel","kind":"cancelled","state":"cancelled","title":"Workshop"}}
            """);
        Assert.Equal(InvitationKind.Cancelled, row.Invitation!.Kind.Value);
        Assert.Null(Member(EncodeObject(Decode<MessageSummary>("""{"id":"m","accountId":"a","folderId":"f","subject":"","date":"2026-10-14T07:00:00Z","snippet":"","hasAttachments":false,"size":0}""")), "invitation"));
    }

    [Fact]
    public void ErrorDataDecodes()
    {
        var consent = Decode<RpcError>("""{"code":1204,"message":"consent","data":{"accountId":"acc_1","signIn":true}}""");
        Assert.Equal(ErrorCode.CalendarConsentRequired, consent.Code.Value);
        var data = consent.Data!.Value.Deserialize(JsonCoding.TypeInfo<CalendarConsentRequiredData>())!;
        Assert.Equal("acc_1", data.AccountId);
        Assert.True(data.SignIn);

        var conflict = Decode<RpcError>("""{"code":1002,"message":"conflict","data":{"event":""" + MinimalEvent + "}}");
        Assert.Equal(ErrorCode.Conflict, conflict.Code.Value);
        var current = conflict.Data!.Value.Deserialize(JsonCoding.TypeInfo<ConflictEventData>())!;
        Assert.Equal("v2", current.Event.ETag);

        foreach (var (code, name) in new[]
        {
            (ErrorCode.ReadOnly, "readOnly"), (ErrorCode.InvitationsUnavailable, "invitationsUnavailable"),
            (ErrorCode.CalendarNotFound, "calendarNotFound"), (ErrorCode.EventNotFound, "eventNotFound"),
            (ErrorCode.CalendarConsentRequired, "calendarConsentRequired"), (ErrorCode.EventGone, "eventGone"),
            (ErrorCode.MalformedCalendar, "malformedCalendar"), (ErrorCode.RecurrenceUnsupported, "recurrenceUnsupported"),
        })
        {
            Assert.Equal(name, new ErrorCode(code).Name);
        }
    }

    [Fact]
    public void ParamsLeaveOutWhatGoLeavesOut()
    {
        Assert.Empty(Keys(EncodeObject(new CalendarListParams())));
        Assert.Empty(Keys(EncodeObject(new CalendarSyncParams())));
        Assert.Equal(["showDeclined"], Keys(EncodeObject(new CalendarPreferencesUpdate { ShowDeclined = true })));

        var start = DateTimeOffset.FromUnixTimeSeconds(1_788_343_200);
        var list = EncodeObject(new EventListParams { From = start, To = start.AddDays(7), TimeZone = "Europe/Prague" });
        Assert.Equal(["from", "timeZone", "to"], Keys(list));

        var busy = EncodeObject(new CalendarFreeBusyParams { AccountId = "acc_1", From = start, To = start.AddDays(1) });
        Assert.Equal(0, busy.GetProperty("attendees").GetArrayLength()); // a list Go never sends as null is written
        Assert.Null(Member(busy, "intervalMinutes"));

        var create = EncodeObject(new EventCreateParams
        {
            CalendarId = "cal_1",
            Event = new EventInput { Title = "Standup", AllDay = true, StartDate = "2026-10-14", EndDate = "2026-10-15" },
        });
        Assert.Equal(["calendarId", "event"], Keys(create));
        var input = create.GetProperty("event");
        Assert.Null(Member(input, "alerts")); // absent: the default alert of calendar.preferences
        Assert.Equal("2026-10-14", input.GetProperty("startDate").GetString());
        var none = EncodeObject(new EventInput { Title = "", AllDay = true, StartDate = "2026-10-14", EndDate = "2026-10-15", Alerts = [] });
        Assert.Equal(0, none.GetProperty("alerts").GetArrayLength()); // empty: no alerts at all

        var update = EncodeObject(new EventUpdateParams
        {
            EventId = "ev_1",
            Scope = EditScope.ThisAndFuture,
            ETag = "v2",
            Event = new EventInput { Title = "", AllDay = true },
            SendUpdates = false,
        });
        Assert.Equal("thisAndFuture", update.GetProperty("scope").GetString());
        Assert.False(update.GetProperty("sendUpdates").GetBoolean());
        Assert.Null(Member(update, "recurrenceId"));
    }

    [Fact]
    public void NotificationsDecode()
    {
        var changed = DaemonNotification.Decode("notify.calendarChanged", """{"jsonrpc":"2.0","method":"notify.calendarChanged"}"""u8);
        Assert.Null(Assert.IsType<DaemonNotification.CalendarChanged>(changed).Payload.AccountIds); // any account
        var named = DaemonNotification.Decode(
            "notify.calendarChanged",
            """{"jsonrpc":"2.0","method":"notify.calendarChanged","params":{"calendarIds":["cal_1"],"from":"2026-10-01T00:00:00Z"}}"""u8);
        var payload = Assert.IsType<DaemonNotification.CalendarChanged>(named).Payload;
        Assert.Equal<CalendarId>(["cal_1"], payload.CalendarIds!);
        Assert.Equal(Rfc3339.Parse("2026-10-01T00:00:00Z"), payload.From);
        Assert.Null(payload.To);

        var invitations = DaemonNotification.Decode("notify.invitationsChanged", """{"jsonrpc":"2.0","method":"notify.invitationsChanged","params":null}"""u8);
        Assert.Null(Assert.IsType<DaemonNotification.InvitationsChanged>(invitations).Payload.AccountIds);

        var state = DaemonNotification.Decode(
            "notify.calendarState",
            """{"jsonrpc":"2.0","method":"notify.calendarState","params":{"state":{"accountId":"acc_1","status":"consentRequired","progress":-1}}}"""u8);
        Assert.Equal(CalendarSyncStatus.ConsentRequired, Assert.IsType<DaemonNotification.CalendarState>(state).State.Status.Value);
        Assert.Throws<JsonException>(() => DaemonNotification.Decode("notify.calendarState", """{"jsonrpc":"2.0","method":"notify.calendarState"}"""u8));

        var alert = DaemonNotification.Decode(
            "notify.eventAlert",
            """{"jsonrpc":"2.0","method":"notify.eventAlert","params":{"alert":{"alertId":"al_1","state":"dismissed","eventId":"ev_1","accountId":"acc_1","calendarId":"cal_1","title":"Standup","allDay":true,"minutesBefore":0}}}"""u8);
        var due = Assert.IsType<DaemonNotification.EventAlert>(alert).Payload.Alert;
        Assert.Equal(EventAlertState.Dismissed, due.State.Value);
        Assert.Equal("al_1", due.AlertId);
        Assert.Throws<JsonException>(() => DaemonNotification.Decode("notify.eventAlert", """{"jsonrpc":"2.0","method":"notify.eventAlert","params":{}}"""u8));
    }

    [Fact]
    public void MethodNamesAreTheContracts()
    {
        Assert.Equal("account.discoverCalDAV", API.AccountDiscoverCalDAV.Name);
        Assert.Equal("account.listCalDAVCalendars", API.AccountListCalDAVCalendars.Name);
        Assert.Equal("calendar.list", API.CalendarList.Name);
        Assert.Equal("calendar.setPreferences", API.CalendarSetPreferences.Name);
        Assert.Equal("calendar.findFreeTime", API.CalendarFindFreeTime.Name);
        Assert.Equal("event.snoozeAlert", API.EventSnoozeAlert.Name);
        Assert.Equal("event.dismissAlert", API.EventDismissAlert.Name);
        Assert.Equal("invitation.applyCancel", API.InvitationApplyCancel.Name);
    }
}
