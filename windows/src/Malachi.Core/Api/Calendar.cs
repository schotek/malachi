// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows mirror of the calendar contract, written beside the Swift mirror
// (macos/Sources/MalachiCore/API/CalendarAPI.swift); Go:
// backend/pkg/api/calendar.go (all of it), the CalDAV and calendar parts of
// types.go (AccountConfig, AccountTestResult, AccountOAuthStartParams,
// MessageSummary.Invitation, Message.Invitation) and errors.go
// (CalendarConsentRequiredData, ConflictEventData); contract: docs/api.md
// §2, §4.1, §4.14 to §4.16, §5; design: docs/calendar.md §4.
//
// Calendars, events and invitations. Every string of these types that
// comes from a server, an .ics or another person (titles, locations, notes,
// names, calendar names) is untrusted display text, cleaned by the daemon
// and shown as plain text; no HTML of an event ever crosses the API.
//
// Windows: Go's embedded Occurrence and InvitationSummary are base records
// (CalendarEvent and InvitationCard derive from them), so the wire object
// stays flat. Go's Event is CalendarEvent, because Event is a Visual Basic
// keyword; Go's EventID and AlertID are CalendarEventId and CalendarAlertId,
// because EventId is Microsoft.Extensions.Logging's; Go's Date and TimeZone
// are CalendarDate and TimeZoneName, because System.TimeZone exists and a
// type named Date would read as the many members of that name. The
// calendar's limits (api.Max*, api.Default*, api.CalendarFutureDays of
// calendar.go) are in API.Limits.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

// ----------------------------------------------------------------------
// Scalars and identifiers
// ----------------------------------------------------------------------

/// <summary>api.CalendarID: a stored calendar (<c>cal_…</c>). Opaque.</summary>
[JsonConverter(typeof(StringWireValueConverter<CalendarId>))]
public readonly record struct CalendarId(string Value) : IOpaqueId<CalendarId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator CalendarId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.EventID: a stored event (<c>ev_…</c>). Opaque; the instances of a series share the id of their master and differ by recurrence id.</summary>
[JsonConverter(typeof(StringWireValueConverter<CalendarEventId>))]
public readonly record struct CalendarEventId(string Value) : IOpaqueId<CalendarEventId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator CalendarEventId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.AlertID: one alert of one occurrence (<c>al_…</c>). Opaque.</summary>
[JsonConverter(typeof(StringWireValueConverter<CalendarAlertId>))]
public readonly record struct CalendarAlertId(string Value) : IOpaqueId<CalendarAlertId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator CalendarAlertId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.Date: a calendar date without a zone, <c>2026-10-14</c>.</summary>
[JsonConverter(typeof(StringWireValueConverter<CalendarDate>))]
public readonly record struct CalendarDate(string Value) : IStringWireValue<CalendarDate>
{
    /// <summary>The value of a wire string.</summary>
    public static implicit operator CalendarDate(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.LocalDateTime: a wall-clock time, <c>2026-10-14T10:00:00</c>, always with a separate <see cref="TimeZoneName"/>.</summary>
[JsonConverter(typeof(StringWireValueConverter<LocalDateTime>))]
public readonly record struct LocalDateTime(string Value) : IStringWireValue<LocalDateTime>
{
    /// <summary>The value of a wire string.</summary>
    public static implicit operator LocalDateTime(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.TimeZone: an IANA zone name (<c>Europe/Prague</c>); an empty one is floating.</summary>
[JsonConverter(typeof(StringWireValueConverter<TimeZoneName>))]
public readonly record struct TimeZoneName(string Value) : IStringWireValue<TimeZoneName>
{
    /// <summary>The value of a wire string.</summary>
    public static implicit operator TimeZoneName(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.DateOrTime: either an RFC 3339 UTC instant (a timed event) or a
/// date (an all-day event): a recurrence id, or the previous time of an
/// update.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<DateOrTime>))]
public readonly record struct DateOrTime(string Value) : IStringWireValue<DateOrTime>
{
    /// <summary>Whether this is a date (an all-day event), not an instant.</summary>
    public bool IsDate => Value is { Length: 10 };

    /// <summary>The instant of a timed value; null for a date and for text that is no RFC 3339 instant.</summary>
    public DateTimeOffset? Instant => Value is { Length: not 10 } ? Rfc3339.Parse(Value) : null;

    /// <summary>The value of a wire string.</summary>
    public static implicit operator DateOrTime(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

// ----------------------------------------------------------------------
// Enumerations
// ----------------------------------------------------------------------

/// <summary>api.CalendarSource: where an account's calendars come from. An unknown value is treated as absent.</summary>
[JsonConverter(typeof(StringWireValueConverter<CalendarSource>))]
public readonly record struct CalendarSource(string Value) : IWireEnumeration<CalendarSource>
{
    /// <summary>Microsoft Graph.</summary>
    public const string Graph = "graph";

    /// <summary>A CalDAV server.</summary>
    public const string CalDAV = "caldav";

    /// <summary>Reserved; not produced yet.</summary>
    public const string Google = "google";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator CalendarSource(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.CalendarAccess: what the user may do with a calendar.</summary>
[JsonConverter(typeof(StringWireValueConverter<CalendarAccess>))]
public readonly record struct CalendarAccess(string Value) : IWireEnumeration<CalendarAccess>
{
    /// <summary>The user's own calendar.</summary>
    public const string Owner = "owner";

    /// <summary>Events can be changed.</summary>
    public const string Write = "write";

    /// <summary>Read only.</summary>
    public const string Read = "read";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator CalendarAccess(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.CalendarKind: what a calendar holds. An unknown value is treated as absent.</summary>
[JsonConverter(typeof(StringWireValueConverter<CalendarKind>))]
public readonly record struct CalendarKind(string Value) : IWireEnumeration<CalendarKind>
{
    /// <summary>Ordinary events.</summary>
    public const string Events = "events";

    /// <summary>Birthdays; read only.</summary>
    public const string Birthdays = "birthdays";

    /// <summary>Holidays; read only.</summary>
    public const string Holidays = "holidays";

    /// <summary>Anything else.</summary>
    public const string Other = "other";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator CalendarKind(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.EventResponse: an attendee's answer (iCalendar PARTSTAT).</summary>
[JsonConverter(typeof(StringWireValueConverter<EventResponse>))]
public readonly record struct EventResponse(string Value) : IWireEnumeration<EventResponse>
{
    /// <summary>Not answered.</summary>
    public const string NeedsAction = "needsAction";

    /// <summary>Accepted.</summary>
    public const string Accepted = "accepted";

    /// <summary>Tentatively accepted.</summary>
    public const string Tentative = "tentative";

    /// <summary>Declined.</summary>
    public const string Declined = "declined";

    /// <summary>Delegated to someone else.</summary>
    public const string Delegated = "delegated";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator EventResponse(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.ShowAs: how an event counts for free/busy. An unknown value is treated as <see cref="Unknown"/>.</summary>
[JsonConverter(typeof(StringWireValueConverter<ShowAs>))]
public readonly record struct ShowAs(string Value) : IWireEnumeration<ShowAs>
{
    /// <summary>Free.</summary>
    public const string Free = "free";

    /// <summary>Tentatively busy.</summary>
    public const string Tentative = "tentative";

    /// <summary>Busy.</summary>
    public const string Busy = "busy";

    /// <summary>Out of office.</summary>
    public const string Oof = "oof";

    /// <summary>Working elsewhere.</summary>
    public const string WorkingElsewhere = "workingElsewhere";

    /// <summary>Not known.</summary>
    public const string Unknown = "unknown";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator ShowAs(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.EventStatus: an event's iCalendar STATUS.</summary>
[JsonConverter(typeof(StringWireValueConverter<EventStatus>))]
public readonly record struct EventStatus(string Value) : IWireEnumeration<EventStatus>
{
    /// <summary>Confirmed.</summary>
    public const string Confirmed = "confirmed";

    /// <summary>Tentative.</summary>
    public const string Tentative = "tentative";

    /// <summary>Cancelled.</summary>
    public const string Cancelled = "cancelled";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator EventStatus(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.EditScope: what a change of a recurring event applies to (docs/calendar.md D19).</summary>
[JsonConverter(typeof(StringWireValueConverter<EditScope>))]
public readonly record struct EditScope(string Value) : IWireEnumeration<EditScope>
{
    /// <summary>The instance: an override, a Graph exception.</summary>
    public const string This = "this";

    /// <summary>The series is split at the instance.</summary>
    public const string ThisAndFuture = "thisAndFuture";

    /// <summary>The master.</summary>
    public const string All = "all";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator EditScope(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.InvitationMethod: the iTIP METHOD of an invitation's calendar part.</summary>
[JsonConverter(typeof(StringWireValueConverter<InvitationMethod>))]
public readonly record struct InvitationMethod(string Value) : IWireEnumeration<InvitationMethod>
{
    /// <summary>An invitation or an update.</summary>
    public const string Request = "request";

    /// <summary>An attendee's answer.</summary>
    public const string Reply = "reply";

    /// <summary>A cancellation.</summary>
    public const string Cancel = "cancel";

    /// <summary>A proposed new time.</summary>
    public const string Counter = "counter";

    /// <summary>A proposed time refused.</summary>
    public const string DeclineCounter = "declineCounter";

    /// <summary>A request for the latest version.</summary>
    public const string Refresh = "refresh";

    /// <summary>Instances added to a series.</summary>
    public const string Add = "add";

    /// <summary>A published calendar part.</summary>
    public const string Publish = "publish";

    /// <summary>Anything else.</summary>
    public const string Other = "other";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator InvitationMethod(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.InvitationKind: the list pill of a message with an invitation.</summary>
[JsonConverter(typeof(StringWireValueConverter<InvitationKind>))]
public readonly record struct InvitationKind(string Value) : IWireEnumeration<InvitationKind>
{
    /// <summary>A request.</summary>
    public const string Invitation = "invitation";

    /// <summary>A request changing a known event, or SEQUENCE above 0 of a known UID.</summary>
    public const string Update = "update";

    /// <summary>A cancel.</summary>
    public const string Cancelled = "cancelled";

    /// <summary>A reply.</summary>
    public const string Reply = "reply";

    /// <summary>Everything else: no pill.</summary>
    public const string Other = "other";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator InvitationKind(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.InvitationState: what is left to do about an invitation.</summary>
[JsonConverter(typeof(StringWireValueConverter<InvitationState>))]
public readonly record struct InvitationState(string Value) : IWireEnumeration<InvitationState>
{
    /// <summary>A request the user has not answered, its event not in the past.</summary>
    public const string Unanswered = "unanswered";

    /// <summary>Answered.</summary>
    public const string Answered = "answered";

    /// <summary>A newer SEQUENCE of the UID arrived, or the event ended.</summary>
    public const string Outdated = "outdated";

    /// <summary>A cancel, or a request a later cancel cancelled.</summary>
    public const string Cancelled = "cancelled";

    /// <summary>A reply or other message with nothing to ask.</summary>
    public const string Processed = "processed";

    /// <summary>The part could not be read: no card.</summary>
    public const string Invalid = "invalid";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator InvitationState(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.CalendarSyncStatus: the status of an account's calendar synchronisation (<see cref="CalendarSyncState"/>).</summary>
[JsonConverter(typeof(StringWireValueConverter<CalendarSyncStatus>))]
public readonly record struct CalendarSyncStatus(string Value) : IWireEnumeration<CalendarSyncStatus>
{
    /// <summary>Nothing to do.</summary>
    public const string Idle = "idle";

    /// <summary>A pass is running.</summary>
    public const string Syncing = "syncing";

    /// <summary>The server cannot be reached.</summary>
    public const string Offline = "offline";

    /// <summary>The sign-in must be renewed.</summary>
    public const string AuthRequired = "authRequired";

    /// <summary>Graph only: the sign-in lacks calendar access.</summary>
    public const string ConsentRequired = "consentRequired";

    /// <summary>The last pass failed.</summary>
    public const string Error = "error";

    /// <summary>The account is paused.</summary>
    public const string Disabled = "disabled";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator CalendarSyncStatus(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.InvitationsVia: who delivers the invitations, updates and replies of an event: the server, the daemon by iMIP mail through the outbox, or nobody.</summary>
[JsonConverter(typeof(StringWireValueConverter<InvitationsVia>))]
public readonly record struct InvitationsVia(string Value) : IWireEnumeration<InvitationsVia>
{
    /// <summary>The server schedules.</summary>
    public const string Server = "server";

    /// <summary>The daemon sends iMIP mail through the outbox.</summary>
    public const string Mail = "mail";

    /// <summary>Nobody.</summary>
    public const string None = "none";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator InvitationsVia(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.VideoProvider: the service of a video-call link. An unknown value is treated as absent.</summary>
[JsonConverter(typeof(StringWireValueConverter<VideoProvider>))]
public readonly record struct VideoProvider(string Value) : IWireEnumeration<VideoProvider>
{
    /// <summary>Microsoft Teams.</summary>
    public const string Teams = "teams";

    /// <summary>Zoom.</summary>
    public const string Zoom = "zoom";

    /// <summary>Google Meet.</summary>
    public const string Meet = "meet";

    /// <summary>Webex.</summary>
    public const string Webex = "webex";

    /// <summary>Jitsi.</summary>
    public const string Jitsi = "jitsi";

    /// <summary>Whereby.</summary>
    public const string Whereby = "whereby";

    /// <summary>Another service.</summary>
    public const string Other = "other";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator VideoProvider(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.CalDAVScheduling: who sends the invitations and replies of a caldav account's events (docs/calendar.md D8); <c>auto</c> follows the server's calendar-auto-schedule, <c>account.listCalDAVCalendars</c> reports <c>server</c> or <c>client</c> only.</summary>
[JsonConverter(typeof(StringWireValueConverter<CalDAVScheduling>))]
public readonly record struct CalDAVScheduling(string Value) : IWireEnumeration<CalDAVScheduling>
{
    /// <summary>Follow the server.</summary>
    public const string Auto = "auto";

    /// <summary>The server schedules.</summary>
    public const string Server = "server";

    /// <summary>The daemon schedules by iMIP mail.</summary>
    public const string Client = "client";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator CalDAVScheduling(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.AfterAnswer: what happens to an invitation message once the user answered it.</summary>
[JsonConverter(typeof(StringWireValueConverter<AfterAnswer>))]
public readonly record struct AfterAnswer(string Value) : IWireEnumeration<AfterAnswer>
{
    /// <summary>It stays where it is.</summary>
    public const string Keep = "keep";

    /// <summary>It is archived.</summary>
    public const string Archive = "archive";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator AfterAnswer(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.WeekStart: the first day of the week of the calendar views.</summary>
[JsonConverter(typeof(StringWireValueConverter<WeekStart>))]
public readonly record struct WeekStart(string Value) : IWireEnumeration<WeekStart>
{
    /// <summary>The locale's.</summary>
    public const string Locale = "locale";

    /// <summary>Monday.</summary>
    public const string Monday = "monday";

    /// <summary>Sunday.</summary>
    public const string Sunday = "sunday";

    /// <summary>Saturday.</summary>
    public const string Saturday = "saturday";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator WeekStart(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.RecurrenceFrequency: the FREQ of a <see cref="Recurrence"/>.</summary>
[JsonConverter(typeof(StringWireValueConverter<RecurrenceFrequency>))]
public readonly record struct RecurrenceFrequency(string Value) : IWireEnumeration<RecurrenceFrequency>
{
    /// <summary>Every day.</summary>
    public const string Daily = "daily";

    /// <summary>Every week.</summary>
    public const string Weekly = "weekly";

    /// <summary>Every month.</summary>
    public const string Monthly = "monthly";

    /// <summary>Every year.</summary>
    public const string Yearly = "yearly";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator RecurrenceFrequency(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.AttendeeRole: an attendee's iCalendar ROLE.</summary>
[JsonConverter(typeof(StringWireValueConverter<AttendeeRole>))]
public readonly record struct AttendeeRole(string Value) : IWireEnumeration<AttendeeRole>
{
    /// <summary>Required.</summary>
    public const string Required = "required";

    /// <summary>Optional.</summary>
    public const string Optional = "optional";

    /// <summary>The chair.</summary>
    public const string Chair = "chair";

    /// <summary>Only informed.</summary>
    public const string NonParticipant = "nonParticipant";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator AttendeeRole(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.AttendeeType: an attendee's iCalendar CUTYPE. An unknown value is treated as <see cref="Unknown"/>.</summary>
[JsonConverter(typeof(StringWireValueConverter<AttendeeType>))]
public readonly record struct AttendeeType(string Value) : IWireEnumeration<AttendeeType>
{
    /// <summary>A person.</summary>
    public const string Person = "person";

    /// <summary>A room.</summary>
    public const string Room = "room";

    /// <summary>A resource.</summary>
    public const string Resource = "resource";

    /// <summary>A group.</summary>
    public const string Group = "group";

    /// <summary>Not known.</summary>
    public const string Unknown = "unknown";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator AttendeeType(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.EventAlertState: the state <c>notify.eventAlert</c> announces.</summary>
[JsonConverter(typeof(StringWireValueConverter<EventAlertState>))]
public readonly record struct EventAlertState(string Value) : IWireEnumeration<EventAlertState>
{
    /// <summary>Fired, or a snooze ended.</summary>
    public const string Due = "due";

    /// <summary>A client dismissed it: withdraw the notification.</summary>
    public const string Dismissed = "dismissed";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator EventAlertState(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.VideoCallMode: what <see cref="EventInput.VideoCall"/> asks for.</summary>
[JsonConverter(typeof(StringWireValueConverter<VideoCallMode>))]
public readonly record struct VideoCallMode(string Value) : IWireEnumeration<VideoCallMode>
{
    /// <summary>No video call.</summary>
    public const string None = "none";

    /// <summary>The source creates a meeting (<see cref="CalendarFeatures.OnlineMeetings"/>).</summary>
    public const string Create = "create";

    /// <summary>A link the user typed.</summary>
    public const string Link = "link";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator VideoCallMode(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.CalDAVFoundVia: how <c>account.discoverCalDAV</c> found the server.</summary>
[JsonConverter(typeof(StringWireValueConverter<CalDAVFoundVia>))]
public readonly record struct CalDAVFoundVia(string Value) : IWireEnumeration<CalDAVFoundVia>
{
    /// <summary>By /.well-known/caldav.</summary>
    public const string WellKnown = "wellKnown";

    /// <summary>By a DNS SRV record.</summary>
    public const string Srv = "srv";

    /// <summary>The URL typed was the server.</summary>
    public const string Url = "url";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator CalDAVFoundVia(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

// ----------------------------------------------------------------------
// Accounts (docs/calendar.md §4.3)
// ----------------------------------------------------------------------

/// <summary>
/// api.CalDAVConfig: the connection of a caldav account (the <c>caldav</c> block of an
/// AccountConfig of kind caldav). The password is <see cref="Credentials.Password"/>, kept in the
/// keyring.
/// </summary>
public sealed record CalDAVConfig
{
    /// <summary>Normalised by <c>account.discoverCalDAV</c>.</summary>
    [JsonPropertyName("serverUrl")]
    public required string ServerUrl { get; init; }

    /// <summary>From <c>account.listCalDAVCalendars</c>.</summary>
    [JsonPropertyName("principalUrl")]
    public string? PrincipalUrl { get; init; }

    /// <summary>From <c>account.listCalDAVCalendars</c>.</summary>
    [JsonPropertyName("homeSetUrl")]
    public string? HomeSetUrl { get; init; }

    /// <summary>The user name.</summary>
    [JsonPropertyName("username")]
    public required string Username { get; init; }

    /// <summary>
    /// Pins the server's leaf certificate, as <see cref="ServerConfig.CertificateSha256"/>;
    /// not allowed with an http URL.
    /// </summary>
    [JsonPropertyName("certificateSha256")]
    public string? CertificateSha256 { get; init; }

    /// <summary>
    /// The mail account invitations and replies leave from when the daemon schedules
    /// (client scheduling); null: events with attendees answer invitationsUnavailable.
    /// </summary>
    [JsonPropertyName("mailAccountId")]
    public AccountId? MailAccountId { get; init; }

    /// <summary>Null is <see cref="CalDAVScheduling.Auto"/>.</summary>
    [JsonPropertyName("scheduling")]
    public CalDAVScheduling? Scheduling { get; init; }

    /// <summary>The CardDAV addressbook-home-set (from <c>account.listCalDAVCalendars</c>).</summary>
    [JsonPropertyName("addressBookHomeSetUrl")]
    public string? AddressBookHomeSetUrl { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>
    /// Synchronise the address books of <see cref="AddressBookHomeSetUrl"/>
    /// (docs/api.md §4.19); false is written as absent.
    /// </summary>
    [JsonPropertyName("contacts")]
    public bool? Contacts { get; init => field = value == true ? true : null; }

    /// <summary>Whether <see cref="Contacts"/> is set.</summary>
    [JsonIgnore]
    public bool ContactsOn => Contacts == true;
}

/// <summary>
/// api.CalendarConfig: the calendar part of a graph or caldav account (the <c>calendar</c> block of
/// an AccountConfig).
/// </summary>
public sealed record CalendarConfig
{
    /// <summary>
    /// Synchronise the calendars. A Graph account's default is false; a caldav account's is
    /// always true.
    /// </summary>
    [JsonPropertyName("sync")]
    public required bool Sync { get; init; }

    /// <summary>
    /// Remote ids (graph) or hrefs (caldav) not synchronised; at most
    /// <see cref="API.Limits.MaxCalendarsPerAccount"/> entries of 2048 bytes.
    /// </summary>
    [JsonPropertyName("disabledCalendars")]
    public IReadOnlyList<string>? DisabledCalendars { get; init; }

    /// <summary>The remote id or href new events of the account go to; null is the server's default.</summary>
    [JsonPropertyName("defaultCalendar")]
    public string? DefaultCalendar { get; init; }

    /// <summary>
    /// How far back events are kept: 30, 90, 180, 365 or 730; null or 0 is
    /// <see cref="API.Limits.DefaultCalendarKeepDays"/>.
    /// </summary>
    [JsonPropertyName("keepDays")]
    public int? KeepDays { get; init; }

    /// <summary>Draws unanswered invitations dashed; null is true (<see cref="ShowsUnanswered"/>).</summary>
    [JsonPropertyName("showUnanswered")]
    public bool? ShowUnanswered { get; init; }

    /// <summary>Shows the invitation card in Mail; null is true (<see cref="ShowsInvitationCard"/>).</summary>
    [JsonPropertyName("invitationCard")]
    public bool? InvitationCard { get; init; }

    /// <summary>Null is <c>keep</c>.</summary>
    [JsonPropertyName("afterAnswer")]
    public AfterAnswer? AfterAnswer { get; init; }

    /// <summary>api.CalendarConfig.ShowsUnanswered: <see cref="ShowUnanswered"/> with its default resolved.</summary>
    [JsonIgnore]
    public bool ShowsUnanswered => ShowUnanswered ?? true;

    /// <summary>api.CalendarConfig.ShowsInvitationCard: <see cref="InvitationCard"/> with its default resolved.</summary>
    [JsonIgnore]
    public bool ShowsInvitationCard => InvitationCard ?? true;
}

/// <summary>
/// api.CalDAVCalendar: a VEVENT collection of a CalDAV server (<c>account.listCalDAVCalendars</c>).
/// Its name is untrusted display text.
/// </summary>
public sealed record CalDAVCalendar
{
    /// <summary>The collection's href.</summary>
    [JsonPropertyName("href")]
    public required string Href { get; init; }

    /// <summary>Display text.</summary>
    [JsonPropertyName("name")]
    public required string Name { get; init; }

    /// <summary><c>#rrggbb</c>.</summary>
    [JsonPropertyName("color")]
    public string? Color { get; init; }

    /// <summary>What the user may do.</summary>
    [JsonPropertyName("access")]
    public required CalendarAccess Access { get; init; }

    /// <summary>The server's schedule-default-calendar-URL.</summary>
    [JsonPropertyName("isDefault")]
    public bool? IsDefault { get; init; }
}

/// <summary>api.AccountDiscoverCalDAVParams: <see cref="Url"/> is a host or a full URL; https is assumed.</summary>
public sealed record AccountDiscoverCalDAVParams
{
    /// <summary>The host or URL typed.</summary>
    [JsonPropertyName("url")]
    public required string Url { get; init; }

    /// <summary>Pins the server's leaf certificate, as <see cref="ServerConfig.CertificateSha256"/>; left out when null.</summary>
    [JsonPropertyName("certificateSha256")]
    public string? CertificateSha256 { get; init; }
}

/// <summary>
/// api.AccountDiscoverCalDAVResult: names the server found. <see cref="ServerUrl"/> may name
/// another host than the one typed (a .well-known redirect); <see cref="Certificate"/> describes
/// the verified certificate.
/// </summary>
public sealed record AccountDiscoverCalDAVResult
{
    /// <summary>The normalised server URL.</summary>
    [JsonPropertyName("serverUrl")]
    public required string ServerUrl { get; init; }

    /// <summary>How it was found.</summary>
    [JsonPropertyName("foundVia")]
    public required CalDAVFoundVia FoundVia { get; init; }

    /// <summary>The server asks for a sign-in.</summary>
    [JsonPropertyName("needsCredentials")]
    public required bool NeedsCredentials { get; init; }

    /// <summary>The certificate the server presented, once verified.</summary>
    [JsonPropertyName("certificate")]
    public CertificateInfo? Certificate { get; init; }

    /// <summary>
    /// The anonymous discovery saw CalDAV (calendar-access); a server that
    /// authenticates before it answers sets neither this nor
    /// <see cref="AddressBooks"/>. False is written as absent.
    /// </summary>
    [JsonPropertyName("calendars")]
    public bool? Calendars { get; init => field = value == true ? true : null; }

    /// <summary>The anonymous discovery saw CardDAV (addressbook); false is written as absent.</summary>
    [JsonPropertyName("addressBooks")]
    public bool? AddressBooks { get; init => field = value == true ? true : null; }
}

/// <summary>
/// api.AccountListCalDAVCalendarsParams: with <see cref="AccountId"/> and no password the stored
/// one is used, only for a caldav account of the same realm.
/// </summary>
public sealed record AccountListCalDAVCalendarsParams
{
    /// <summary>The account whose stored password to use.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>A configuration of kind caldav.</summary>
    [JsonPropertyName("config")]
    public required AccountConfig Config { get; init; }

    /// <summary>Its secrets; sent as <c>{}</c> when there are none.</summary>
    [JsonPropertyName("credentials")]
    [JsonRequired]
    public Credentials Credentials { get; init; } = new();
}

/// <summary>api.AccountListCalDAVCalendarsResult: what the account settings choose from.</summary>
public sealed record AccountListCalDAVCalendarsResult
{
    /// <summary>The principal's URL.</summary>
    [JsonPropertyName("principalUrl")]
    public required string PrincipalUrl { get; init; }

    /// <summary>The calendar home set's URL.</summary>
    [JsonPropertyName("homeSetUrl")]
    public required string HomeSetUrl { get; init; }

    /// <summary>The principal's name.</summary>
    [JsonPropertyName("displayName")]
    public string? DisplayName { get; init; }

    /// <summary>The mailto: values of calendar-user-address-set; always written.</summary>
    [JsonPropertyName("addresses")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Addresses { get; init => field = value ?? []; } = [];

    /// <summary>Server or client; never auto.</summary>
    [JsonPropertyName("scheduling")]
    public required CalDAVScheduling Scheduling { get; init; }

    /// <summary>At most <see cref="API.Limits.MaxCalendarsPerAccount"/>; always written.</summary>
    [JsonPropertyName("calendars")]
    [JsonConverter(typeof(NullAsEmptyListConverter<CalDAVCalendar>))]
    public IReadOnlyList<CalDAVCalendar> Calendars { get; init => field = value ?? []; } = [];

    /// <summary>The principal's CardDAV addressbook-home-set; null when the server has none.</summary>
    [JsonPropertyName("addressBookHomeSetUrl")]
    public string? AddressBookHomeSetUrl { get; init => field = string.IsNullOrEmpty(value) ? null : value; }

    /// <summary>The home set's address books (at most <see cref="API.Limits.MaxAddressBooksPerAccount"/>); always written.</summary>
    [JsonPropertyName("addressBooks")]
    [JsonConverter(typeof(NullAsEmptyListConverter<CardDAVAddressBook>))]
    public IReadOnlyList<CardDAVAddressBook> AddressBooks { get; init => field = value ?? []; } = [];
}

// ----------------------------------------------------------------------
// Common types (docs/calendar.md §4.2)
// ----------------------------------------------------------------------

/// <summary>api.Calendar: one stored calendar. Its name is untrusted display text.</summary>
public sealed record Calendar
{
    /// <summary>The calendar.</summary>
    [JsonPropertyName("id")]
    public required CalendarId Id { get; init; }

    /// <summary>Its account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The remote id (graph) or href (caldav) on the server; always sent.</summary>
    [JsonPropertyName("remoteId")]
    public required string RemoteId { get; init; }

    /// <summary>Display text.</summary>
    [JsonPropertyName("name")]
    public required string Name { get; init; }

    /// <summary>
    /// The user's colour if set, else the server's, else a palette colour by id;
    /// <c>#rrggbb</c>, lower case, always valid.
    /// </summary>
    [JsonPropertyName("color")]
    public required string Color { get; init; }

    /// <summary>The server's colour.</summary>
    [JsonPropertyName("serverColor")]
    public string? ServerColor { get; init; }

    /// <summary>The user's colour.</summary>
    [JsonPropertyName("userColor")]
    public string? UserColor { get; init; }

    /// <summary>Shown in the views (<c>calendar.update</c>).</summary>
    [JsonPropertyName("visible")]
    public required bool Visible { get; init; }

    /// <summary>What the user may do.</summary>
    [JsonPropertyName("access")]
    public required CalendarAccess Access { get; init; }

    /// <summary>What it holds.</summary>
    [JsonPropertyName("kind")]
    public required CalendarKind Kind { get; init; }

    /// <summary>Set for a calendar shared with the user.</summary>
    [JsonPropertyName("owner")]
    public Address? Owner { get; init; }

    /// <summary>The account's calendar for new events.</summary>
    [JsonPropertyName("isDefault")]
    public bool? IsDefault { get; init; }

    /// <summary>Its zone.</summary>
    [JsonPropertyName("timeZone")]
    public TimeZoneName? TimeZone { get; init; }

    /// <summary>Graph allowedOnlineMeetingProviders, an open enumeration (<c>teamsForBusiness</c>).</summary>
    [JsonPropertyName("onlineMeetingProviders")]
    public IReadOnlyList<string>? OnlineMeetingProviders { get; init; }

    /// <summary>Listed in <see cref="CalendarConfig.DisabledCalendars"/>, so not synchronised; left out when false.</summary>
    [JsonPropertyName("disabled")]
    public bool? Disabled { get; init; }
}

/// <summary>api.CalendarFeatures: what an account's source can do.</summary>
public sealed record CalendarFeatures
{
    /// <summary><c>calendar.freeBusy</c> can ask about other people.</summary>
    [JsonPropertyName("freeBusy")]
    public required bool FreeBusy { get; init; }

    /// <summary>Some events may offer it.</summary>
    [JsonPropertyName("proposeNewTime")]
    public required bool ProposeNewTime { get; init; }

    /// <summary>Who delivers the invitations and replies of its events.</summary>
    [JsonPropertyName("invitations")]
    public required InvitationsVia Invitations { get; init; }

    /// <summary>With invitations <c>mail</c>: the account they leave from.</summary>
    [JsonPropertyName("mailAccountId")]
    public AccountId? MailAccountId { get; init; }

    /// <summary>"Add video call" can create a meeting.</summary>
    [JsonPropertyName("onlineMeetings")]
    public required bool OnlineMeetings { get; init; }
}

/// <summary>api.CalendarSyncState: an account's calendar synchronisation state. A Graph account's is separate from its mail <see cref="SyncState"/>.</summary>
public sealed record CalendarSyncState
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Where it stands.</summary>
    [JsonPropertyName("status")]
    public required CalendarSyncStatus Status { get; init; }

    /// <summary>0 to 100, -1 when unknown.</summary>
    [JsonPropertyName("progress")]
    public required int Progress { get; init; }

    /// <summary>The last successful pass.</summary>
    [JsonPropertyName("lastSync")]
    public DateTimeOffset? LastSync { get; init; }

    /// <summary>The last failure.</summary>
    [JsonPropertyName("error")]
    public RpcError? Error { get; init; }
}

/// <summary>api.CalendarWindow: the span of an account's stored events.</summary>
public sealed record CalendarWindow
{
    /// <summary>The first day.</summary>
    [JsonPropertyName("from")]
    public required CalendarDate From { get; init; }

    /// <summary>The last day.</summary>
    [JsonPropertyName("to")]
    public required CalendarDate To { get; init; }
}

/// <summary>api.CalendarAccount: an account with <see cref="Capability.Calendar"/> (<c>calendar.list</c>).</summary>
public sealed record CalendarAccount
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Where its calendars come from.</summary>
    [JsonPropertyName("source")]
    public required CalendarSource Source { get; init; }

    /// <summary><see cref="CalendarConfig.Sync"/> (always true for caldav).</summary>
    [JsonPropertyName("sync")]
    public required bool Sync { get; init; }

    /// <summary>Its synchronisation state.</summary>
    [JsonPropertyName("state")]
    public required CalendarSyncState State { get; init; }

    /// <summary>What its source can do.</summary>
    [JsonPropertyName("features")]
    public required CalendarFeatures Features { get; init; }

    /// <summary>What is stored.</summary>
    [JsonPropertyName("window")]
    public required CalendarWindow Window { get; init; }

    /// <summary>In server order; empty while sync is off; always written.</summary>
    [JsonPropertyName("calendars")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Calendar>))]
    public IReadOnlyList<Calendar> Calendars { get; init => field = value ?? []; } = [];
}

/// <summary>api.Recurrence: a recurrence rule in the shape the editor shows.</summary>
public sealed record Recurrence
{
    /// <summary>The FREQ.</summary>
    [JsonPropertyName("frequency")]
    public required RecurrenceFrequency Frequency { get; init; }

    /// <summary>1 to <see cref="API.Limits.MaxRecurrenceInterval"/> in input; up to 1000 read.</summary>
    [JsonPropertyName("interval")]
    public required int Interval { get; init; }

    /// <summary>
    /// Weekly: the days (<c>MO</c>); monthly and yearly: with an ordinal (<c>2TU</c>,
    /// <c>-1FR</c>).
    /// </summary>
    [JsonPropertyName("byDay")]
    public IReadOnlyList<string>? ByDay { get; init; }

    /// <summary>-31 to 31, not 0.</summary>
    [JsonPropertyName("byMonthDay")]
    public IReadOnlyList<int>? ByMonthDay { get; init; }

    /// <summary>1 to 12.</summary>
    [JsonPropertyName("byMonth")]
    public IReadOnlyList<int>? ByMonth { get; init; }

    /// <summary>The last day; at most one of this and <see cref="Count"/>, neither is no end.</summary>
    [JsonPropertyName("until")]
    public CalendarDate? Until { get; init; }

    /// <summary>
    /// The number of instances (up to <see cref="API.Limits.MaxRecurrenceCount"/> in
    /// input).
    /// </summary>
    [JsonPropertyName("count")]
    public int? Count { get; init; }

    /// <summary><c>MO</c> to <c>SU</c>.</summary>
    [JsonPropertyName("weekStart")]
    public string? WeekStart { get; init; }

    /// <summary>
    /// Read only: the rule has more than this shape; shown as Custom, edited only with
    /// <see cref="EditScope.All"/> and kept unless replaced.
    /// </summary>
    [JsonPropertyName("complex")]
    public bool? Complex { get; init; }

    /// <summary>Read only: modified or removed instances in the window.</summary>
    [JsonPropertyName("exceptions")]
    public int? Exceptions { get; init; }
}

/// <summary>api.Attendee: one attendee of an event or an invitation. Name and address are untrusted display text.</summary>
public sealed record Attendee
{
    /// <summary>The display name.</summary>
    [JsonPropertyName("name")]
    public string? Name { get; init; }

    /// <summary>The e-mail address.</summary>
    [JsonPropertyName("address")]
    public required string Address { get; init; }

    /// <summary>The ROLE.</summary>
    [JsonPropertyName("role")]
    public required AttendeeRole Role { get; init; }

    /// <summary>The answer.</summary>
    [JsonPropertyName("response")]
    public required EventResponse Response { get; init; }

    /// <summary>The CUTYPE.</summary>
    [JsonPropertyName("type")]
    public required AttendeeType Type { get; init; }

    /// <summary>The user.</summary>
    [JsonPropertyName("me")]
    public bool? Me { get; init; }

    /// <summary>The organiser.</summary>
    [JsonPropertyName("organizer")]
    public bool? Organizer { get; init; }
}

/// <summary>api.VideoCall: a video-call link found in an event; opened only through the client's "Open this link?".</summary>
public sealed record VideoCall
{
    /// <summary>The link.</summary>
    [JsonPropertyName("url")]
    public required string Url { get; init; }

    /// <summary>The service.</summary>
    [JsonPropertyName("provider")]
    public required VideoProvider Provider { get; init; }

    /// <summary>The link's host.</summary>
    [JsonPropertyName("host")]
    public required string Host { get; init; }
}

/// <summary>api.MessageRef: names a stored message: an invitation of an occurrence, or the invitation messages of an event.</summary>
public sealed record MessageRef
{
    /// <summary>Its account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}

/// <summary>
/// api.Occurrence: one instance in a view (<c>event.list</c>, <c>event.search</c>). Timed:
/// <see cref="Start"/> and <see cref="End"/> are UTC instants; all-day: <see cref="StartDate"/> and
/// <see cref="EndDate"/> (exclusive) instead.
/// </summary>
public record Occurrence
{
    /// <summary>Null for an invitation not in any calendar.</summary>
    [JsonPropertyName("eventId")]
    public CalendarEventId? EventId { get; init; }

    /// <summary>For an instance of a series: its original start.</summary>
    [JsonPropertyName("recurrenceId")]
    public DateOrTime? RecurrenceId { get; init; }

    /// <summary>The master of a series instance.</summary>
    [JsonPropertyName("seriesId")]
    public CalendarEventId? SeriesId { get; init; }

    /// <summary>Its calendar.</summary>
    [JsonPropertyName("calendarId")]
    public CalendarId? CalendarId { get; init; }

    /// <summary>Its account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Empty when private and not the user's.</summary>
    [JsonPropertyName("title")]
    public required string Title { get; init; }

    /// <summary>A timed occurrence's start.</summary>
    [JsonPropertyName("start")]
    public DateTimeOffset? Start { get; init; }

    /// <summary>A timed occurrence's end.</summary>
    [JsonPropertyName("end")]
    public DateTimeOffset? End { get; init; }

    /// <summary>An all-day occurrence's first day.</summary>
    [JsonPropertyName("startDate")]
    public CalendarDate? StartDate { get; init; }

    /// <summary>An all-day occurrence's end, exclusive.</summary>
    [JsonPropertyName("endDate")]
    public CalendarDate? EndDate { get; init; }

    /// <summary>An all-day occurrence.</summary>
    [JsonPropertyName("allDay")]
    public required bool AllDay { get; init; }

    /// <summary>
    /// No zone in the source; <see cref="Start"/> and <see cref="End"/> are computed in the
    /// zone the caller passed.
    /// </summary>
    [JsonPropertyName("floating")]
    public bool? Floating { get; init; }

    /// <summary>Its zone.</summary>
    [JsonPropertyName("timeZone")]
    public TimeZoneName? TimeZone { get; init; }

    /// <summary>Display text.</summary>
    [JsonPropertyName("location")]
    public string? Location { get; init; }

    /// <summary>The STATUS.</summary>
    [JsonPropertyName("status")]
    public required EventStatus Status { get; init; }

    /// <summary>How it counts for free/busy.</summary>
    [JsonPropertyName("showAs")]
    public required ShowAs ShowAs { get; init; }

    /// <summary>Null when the user is not an attendee.</summary>
    [JsonPropertyName("myResponse")]
    public EventResponse? MyResponse { get; init; }

    /// <summary>The user organises it.</summary>
    [JsonPropertyName("organizer")]
    public bool? Organizer { get; init; }

    /// <summary>All attendees, also those not listed.</summary>
    [JsonPropertyName("attendeeCount")]
    public int? AttendeeCount { get; init; }

    /// <summary>An instance or the master of a series.</summary>
    [JsonPropertyName("recurring")]
    public bool? Recurring { get; init; }

    /// <summary>It has a video-call link.</summary>
    [JsonPropertyName("videoCall")]
    public bool? VideoCall { get; init; }

    /// <summary>Marked private.</summary>
    [JsonPropertyName("private")]
    public bool? Private { get; init; }

    /// <summary>An alert is set.</summary>
    [JsonPropertyName("alert")]
    public bool? Alert { get; init; }

    /// <summary>The user cannot change it.</summary>
    [JsonPropertyName("readOnly")]
    public bool? ReadOnly { get; init; }

    /// <summary>An unanswered invitation shown tentatively, not in a calendar.</summary>
    [JsonPropertyName("invitation")]
    public MessageRef? Invitation { get; init; }
}

/// <summary>
/// api.Event: an occurrence with everything the detail shows (<c>event.get</c> and the results of
/// the writes). Go embeds Occurrence, so on the wire its members sit beside these.
/// </summary>
public sealed record CalendarEvent : Occurrence
{
    /// <summary>Opaque; passed back on update, delete and respond.</summary>
    [JsonPropertyName("etag")]
    public required string ETag { get; init; }

    /// <summary>Plain text.</summary>
    [JsonPropertyName("notes")]
    public string? Notes { get; init; }

    /// <summary>The https links found in <see cref="Notes"/>.</summary>
    [JsonPropertyName("links")]
    public IReadOnlyList<string>? Links { get; init; }

    /// <summary>The video-call link.</summary>
    [JsonPropertyName("videoCallLink")]
    public VideoCall? VideoCallLink { get; init; }

    /// <summary>The organiser.</summary>
    [JsonPropertyName("organizerAddress")]
    public Address? OrganizerAddress { get; init; }

    /// <summary>At most <see cref="API.Limits.MaxEventAttendeesStored"/>.</summary>
    [JsonPropertyName("attendees")]
    public IReadOnlyList<Attendee>? Attendees { get; init; }

    /// <summary>More attendees than listed.</summary>
    [JsonPropertyName("attendeesTruncated")]
    public bool? AttendeesTruncated { get; init; }

    /// <summary>The series' rule (on any instance).</summary>
    [JsonPropertyName("recurrence")]
    public Recurrence? Recurrence { get; init; }

    /// <summary>Minutes before start.</summary>
    [JsonPropertyName("alerts")]
    public IReadOnlyList<int>? Alerts { get; init; }

    /// <summary>The organiser asks for an answer.</summary>
    [JsonPropertyName("responseRequested")]
    public bool? ResponseRequested { get; init; }

    /// <summary>The user may change it.</summary>
    [JsonPropertyName("canEdit")]
    public required bool CanEdit { get; init; }

    /// <summary>The user may delete it.</summary>
    [JsonPropertyName("canDelete")]
    public required bool CanDelete { get; init; }

    /// <summary>The user may answer it.</summary>
    [JsonPropertyName("canRespond")]
    public required bool CanRespond { get; init; }

    /// <summary>The user may propose a new time.</summary>
    [JsonPropertyName("canProposeNewTime")]
    public required bool CanProposeNewTime { get; init; }

    /// <summary>Remove from Calendar deletes the event for everyone (the user organises it).</summary>
    [JsonPropertyName("removeIsDelete")]
    public required bool RemoveIsDelete { get; init; }

    /// <summary>Show Invitation in Mail.</summary>
    [JsonPropertyName("invitationMessages")]
    public IReadOnlyList<MessageRef>? InvitationMessages { get; init; }

    /// <summary>When it was created.</summary>
    [JsonPropertyName("created")]
    public DateTimeOffset? Created { get; init; }

    /// <summary>When it was last changed.</summary>
    [JsonPropertyName("updated")]
    public DateTimeOffset? Updated { get; init; }
}

/// <summary>api.EventInputAttendee: an attendee as the editor gives it.</summary>
public sealed record EventInputAttendee
{
    /// <summary>The display name.</summary>
    [JsonPropertyName("name")]
    public string? Name { get; init; }

    /// <summary>The e-mail address.</summary>
    [JsonPropertyName("address")]
    public required string Address { get; init; }

    /// <summary>Required or optional; null is required.</summary>
    [JsonPropertyName("role")]
    public AttendeeRole? Role { get; init; }
}

/// <summary>api.VideoCallInput: the editor's video call.</summary>
public sealed record VideoCallInput
{
    /// <summary>What is asked for.</summary>
    [JsonPropertyName("mode")]
    public required VideoCallMode Mode { get; init; }

    /// <summary>With create: an onlineMeetingProviders value.</summary>
    [JsonPropertyName("provider")]
    public string? Provider { get; init; }

    /// <summary>With link: an https URL.</summary>
    [JsonPropertyName("url")]
    public string? Url { get; init; }
}

/// <summary>
/// api.EventInput: an event as <c>event.create</c> and <c>event.update</c> take it: timed
/// (<see cref="Start"/>, <see cref="End"/> and <see cref="TimeZone"/> required, an empty zone is
/// refused) or all-day (<see cref="StartDate"/>, <see cref="EndDate"/> exclusive and after the
/// start).
/// </summary>
public sealed record EventInput
{
    /// <summary>At most <see cref="API.Limits.MaxEventTitleBytes"/> after cleaning; may be empty.</summary>
    [JsonPropertyName("title")]
    public required string Title { get; init; }

    /// <summary>An all-day event.</summary>
    [JsonPropertyName("allDay")]
    public required bool AllDay { get; init; }

    /// <summary>Timed: the start.</summary>
    [JsonPropertyName("start")]
    public LocalDateTime? Start { get; init; }

    /// <summary>Timed: the end.</summary>
    [JsonPropertyName("end")]
    public LocalDateTime? End { get; init; }

    /// <summary>Timed: the zone.</summary>
    [JsonPropertyName("timeZone")]
    public TimeZoneName? TimeZone { get; init; }

    /// <summary>All-day: the first day.</summary>
    [JsonPropertyName("startDate")]
    public CalendarDate? StartDate { get; init; }

    /// <summary>All-day: the end, exclusive.</summary>
    [JsonPropertyName("endDate")]
    public CalendarDate? EndDate { get; init; }

    /// <summary>At most <see cref="API.Limits.MaxEventLocationBytes"/>.</summary>
    [JsonPropertyName("location")]
    public string? Location { get; init; }

    /// <summary>Plain text, at most <see cref="API.Limits.MaxEventNotesBytes"/>.</summary>
    [JsonPropertyName("notes")]
    public string? Notes { get; init; }

    /// <summary>Without Complex and Exceptions; null is none.</summary>
    [JsonPropertyName("recurrence")]
    public Recurrence? Recurrence { get; init; }

    /// <summary>An update of a complex rule keeps it as it is (<see cref="Recurrence"/> ignored).</summary>
    [JsonPropertyName("keepRecurrence")]
    public bool? KeepRecurrence { get; init; }

    /// <summary>
    /// Minutes before start, at most <see cref="API.Limits.MaxEventAlerts"/> values of 0 to
    /// <see cref="API.Limits.MaxAlertMinutes"/>. Null (left out) is the default alert of
    /// <c>calendar.preferences</c>; an empty list is none and is written as <c>[]</c>.
    /// </summary>
    [JsonPropertyName("alerts")]
    public IReadOnlyList<int>? Alerts { get; init; }

    /// <summary>How it counts for free/busy.</summary>
    [JsonPropertyName("showAs")]
    public ShowAs? ShowAs { get; init; }

    /// <summary>Marked private.</summary>
    [JsonPropertyName("private")]
    public bool? Private { get; init; }

    /// <summary>At most <see cref="API.Limits.MaxEventAttendeesIn"/>.</summary>
    [JsonPropertyName("attendees")]
    public IReadOnlyList<EventInputAttendee>? Attendees { get; init; }

    /// <summary>The video call.</summary>
    [JsonPropertyName("videoCall")]
    public VideoCallInput? VideoCall { get; init; }

    /// <summary>
    /// <c>attachment.import</c> ids, at most <see cref="API.Limits.MaxEventAttachments"/>,
    /// each at most <see cref="API.Limits.MaxEventAttachmentBytes"/>.
    /// </summary>
    [JsonPropertyName("attachments")]
    public IReadOnlyList<string>? Attachments { get; init; }
}

/// <summary>api.MailText: a client's localised iMIP subject and body (the daemon is language-neutral); each at most <see cref="API.Limits.MaxMailTextBytes"/>.</summary>
public sealed record MailText
{
    /// <summary>The subject.</summary>
    [JsonPropertyName("subject")]
    public required string Subject { get; init; }

    /// <summary>The body.</summary>
    [JsonPropertyName("text")]
    public required string Text { get; init; }
}

/// <summary>api.BusyInterval: a busy span of an <see cref="Availability"/>.</summary>
public sealed record BusyInterval
{
    /// <summary>The start.</summary>
    [JsonPropertyName("start")]
    public required DateTimeOffset Start { get; init; }

    /// <summary>The end.</summary>
    [JsonPropertyName("end")]
    public required DateTimeOffset End { get; init; }

    /// <summary>How it counts.</summary>
    [JsonPropertyName("showAs")]
    public required ShowAs ShowAs { get; init; }
}

/// <summary>api.Availability: one person's free/busy (<c>calendar.freeBusy</c>).</summary>
public sealed record Availability
{
    /// <summary>The e-mail address.</summary>
    [JsonPropertyName("address")]
    public required string Address { get; init; }

    /// <summary>False: no free/busy for this person.</summary>
    [JsonPropertyName("known")]
    public required bool Known { get; init; }

    /// <summary>The busy spans; always written.</summary>
    [JsonPropertyName("busy")]
    [JsonConverter(typeof(NullAsEmptyListConverter<BusyInterval>))]
    public IReadOnlyList<BusyInterval> Busy { get; init => field = value ?? []; } = [];
}

/// <summary>api.TimeSlot: a free slot (<c>calendar.findFreeTime</c>).</summary>
public sealed record TimeSlot
{
    /// <summary>The start.</summary>
    [JsonPropertyName("start")]
    public required DateTimeOffset Start { get; init; }

    /// <summary>The end.</summary>
    [JsonPropertyName("end")]
    public required DateTimeOffset End { get; init; }
}

/// <summary>api.EventAlert: an alert <c>notify.eventAlert</c> announces.</summary>
public sealed record EventAlert
{
    /// <summary>The alert.</summary>
    [JsonPropertyName("alertId")]
    public required CalendarAlertId AlertId { get; init; }

    /// <summary>Due or dismissed.</summary>
    [JsonPropertyName("state")]
    public required EventAlertState State { get; init; }

    /// <summary>The event.</summary>
    [JsonPropertyName("eventId")]
    public required CalendarEventId EventId { get; init; }

    /// <summary>The instance of a series.</summary>
    [JsonPropertyName("recurrenceId")]
    public DateOrTime? RecurrenceId { get; init; }

    /// <summary>Its account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Its calendar.</summary>
    [JsonPropertyName("calendarId")]
    public required CalendarId CalendarId { get; init; }

    /// <summary>Display text.</summary>
    [JsonPropertyName("title")]
    public required string Title { get; init; }

    /// <summary>A timed event's start.</summary>
    [JsonPropertyName("start")]
    public DateTimeOffset? Start { get; init; }

    /// <summary>A timed event's end.</summary>
    [JsonPropertyName("end")]
    public DateTimeOffset? End { get; init; }

    /// <summary>An all-day event's first day.</summary>
    [JsonPropertyName("startDate")]
    public CalendarDate? StartDate { get; init; }

    /// <summary>An all-day event.</summary>
    [JsonPropertyName("allDay")]
    public required bool AllDay { get; init; }

    /// <summary>Display text.</summary>
    [JsonPropertyName("location")]
    public string? Location { get; init; }

    /// <summary>It has a video-call link.</summary>
    [JsonPropertyName("videoCall")]
    public bool? VideoCall { get; init; }

    /// <summary>How long before the start it fires.</summary>
    [JsonPropertyName("minutesBefore")]
    public required int MinutesBefore { get; init; }
}

/// <summary>api.WorkingHours: the user's working days and hours, <c>HH:MM</c> in the daemon's local zone.</summary>
public sealed record WorkingHours
{
    /// <summary><c>MO</c> to <c>SU</c>, at least one; always written.</summary>
    [JsonPropertyName("days")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Days { get; init => field = value ?? []; } = [];

    /// <summary>The first hour.</summary>
    [JsonPropertyName("start")]
    public required string Start { get; init; }

    /// <summary>The last hour, after the start.</summary>
    [JsonPropertyName("end")]
    public required string End { get; init; }
}

/// <summary>api.CalendarPreferences: the calendar's daemon-side preferences (<c>calendar.preferences</c>).</summary>
public sealed record CalendarPreferences
{
    /// <summary>The first day of the week.</summary>
    [JsonPropertyName("weekStart")]
    public required WeekStart WeekStart { get; init; }

    /// <summary>5 to 1440.</summary>
    [JsonPropertyName("defaultDurationMinutes")]
    public required int DefaultDurationMinutes { get; init; }

    /// <summary>-1 none, 0 to <see cref="API.Limits.MaxAlertMinutes"/>.</summary>
    [JsonPropertyName("defaultAlertMinutes")]
    public required int DefaultAlertMinutes { get; init; }

    /// <summary>Declined events are listed.</summary>
    [JsonPropertyName("showDeclined")]
    public required bool ShowDeclined { get; init; }

    /// <summary>The working days and hours.</summary>
    [JsonPropertyName("workingHours")]
    public required WorkingHours WorkingHours { get; init; }
}

/// <summary>
/// api.CalendarPreferencesUpdate: <see cref="CalendarPreferences"/> as
/// <c>calendar.setPreferences</c> takes it: a member left null keeps its value;
/// <see cref="WorkingHours"/>, when given, is replaced whole.
/// </summary>
public sealed record CalendarPreferencesUpdate
{
    /// <summary>The first day of the week.</summary>
    [JsonPropertyName("weekStart")]
    public WeekStart? WeekStart { get; init; }

    /// <summary>5 to 1440.</summary>
    [JsonPropertyName("defaultDurationMinutes")]
    public int? DefaultDurationMinutes { get; init; }

    /// <summary>-1 none, 0 to <see cref="API.Limits.MaxAlertMinutes"/>.</summary>
    [JsonPropertyName("defaultAlertMinutes")]
    public int? DefaultAlertMinutes { get; init; }

    /// <summary>Declined events are listed.</summary>
    [JsonPropertyName("showDeclined")]
    public bool? ShowDeclined { get; init; }

    /// <summary>The working days and hours.</summary>
    [JsonPropertyName("workingHours")]
    public WorkingHours? WorkingHours { get; init; }
}

// ----------------------------------------------------------------------
// Error data
// ----------------------------------------------------------------------

/// <summary>api.CalendarConsentRequiredData: the <c>data</c> of a calendarConsentRequired error.</summary>
public sealed record CalendarConsentRequiredData
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>
    /// True: <c>account.oauthStart</c> with <see cref="AccountOAuthStartParams.Calendar"/>
    /// can fix it (source daemon); false for GOA (the desktop's Online Accounts settings).
    /// </summary>
    [JsonPropertyName("signIn")]
    public required bool SignIn { get; init; }
}

/// <summary>
/// api.ConflictEventData: the <c>data</c> of a conflict error for an event write whose etag is not
/// the server's current one: the current event, as <c>event.get</c> returns it.
/// </summary>
public sealed record ConflictEventData
{
    /// <summary>The current event.</summary>
    [JsonPropertyName("event")]
    public required CalendarEvent Event { get; init; }
}

// ----------------------------------------------------------------------
// calendar.* (docs/api.md §4.14)
// ----------------------------------------------------------------------

/// <summary>api.CalendarListParams: null <see cref="AccountId"/> is every account with the capability.</summary>
public sealed record CalendarListParams
{
    /// <summary>One account.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }
}

/// <summary>api.CalendarListResult.</summary>
public sealed record CalendarListResult
{
    /// <summary>In the accounts' order; always written.</summary>
    [JsonPropertyName("accounts")]
    [JsonConverter(typeof(NullAsEmptyListConverter<CalendarAccount>))]
    public IReadOnlyList<CalendarAccount> Accounts { get; init => field = value ?? []; } = [];

    /// <summary>The invitations of enabled accounts waiting for an answer.</summary>
    [JsonPropertyName("unanswered")]
    public required int Unanswered { get; init; }
}

/// <summary>api.CalendarUpdateParams: the user's own settings of one calendar; null members are left as they are, an empty <see cref="UserColor"/> clears it.</summary>
public sealed record CalendarUpdateParams
{
    /// <summary>The calendar.</summary>
    [JsonPropertyName("calendarId")]
    public required CalendarId CalendarId { get; init; }

    /// <summary>Shown in the views.</summary>
    [JsonPropertyName("visible")]
    public bool? Visible { get; init; }

    /// <summary><c>#rrggbb</c>, or empty to clear.</summary>
    [JsonPropertyName("userColor")]
    public string? UserColor { get; init; }
}

/// <summary>api.CalendarUpdateResult.</summary>
public sealed record CalendarUpdateResult
{
    /// <summary>The calendar as it is now.</summary>
    [JsonPropertyName("calendar")]
    public required Calendar Calendar { get; init; }
}

/// <summary>api.CalendarSyncParams: null members are every account, or the account's calendars.</summary>
public sealed record CalendarSyncParams
{
    /// <summary>One account.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>One calendar.</summary>
    [JsonPropertyName("calendarId")]
    public CalendarId? CalendarId { get; init; }
}

/// <summary>api.CalendarSetPreferencesParams.</summary>
public sealed record CalendarSetPreferencesParams
{
    /// <summary>What to change.</summary>
    [JsonPropertyName("preferences")]
    public required CalendarPreferencesUpdate Preferences { get; init; }
}

/// <summary>api.CalendarPreferencesResult.</summary>
public sealed record CalendarPreferencesResult
{
    /// <summary>The preferences.</summary>
    [JsonPropertyName("preferences")]
    public required CalendarPreferences Preferences { get; init; }
}

/// <summary>api.CalendarSetPreferencesResult.</summary>
public sealed record CalendarSetPreferencesResult
{
    /// <summary>The preferences as stored.</summary>
    [JsonPropertyName("preferences")]
    public required CalendarPreferences Preferences { get; init; }
}

/// <summary>
/// api.CalendarFreeBusyParams: at most <see cref="API.Limits.MaxFreeBusyAttendees"/> attendees, to
/// minus from at most <see cref="API.Limits.MaxFreeBusyDays"/> days; interval 15, 30 or 60 minutes
/// (null or 0 is 30).
/// </summary>
public sealed record CalendarFreeBusyParams
{
    /// <summary>The account whose source answers.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The addresses; always written.</summary>
    [JsonPropertyName("attendees")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Attendees { get; init => field = value ?? []; } = [];

    /// <summary>The start of the range.</summary>
    [JsonPropertyName("from")]
    public required DateTimeOffset From { get; init; }

    /// <summary>The end of the range.</summary>
    [JsonPropertyName("to")]
    public required DateTimeOffset To { get; init; }

    /// <summary>15, 30 or 60.</summary>
    [JsonPropertyName("intervalMinutes")]
    public int? IntervalMinutes { get; init; }
}

/// <summary>api.CalendarFreeBusyResult: the attendees in the order asked.</summary>
public sealed record CalendarFreeBusyResult
{
    /// <summary>The user.</summary>
    [JsonPropertyName("me")]
    public required Availability Me { get; init; }

    /// <summary>The others; always written.</summary>
    [JsonPropertyName("attendees")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Availability>))]
    public IReadOnlyList<Availability> Attendees { get; init => field = value ?? []; } = [];
}

/// <summary>api.CalendarFindFreeTimeParams: null <see cref="WorkingHoursOnly"/> is true; a null or zero <see cref="Limit"/> is 5, at most <see cref="API.Limits.MaxFreeSlots"/>.</summary>
public sealed record CalendarFindFreeTimeParams
{
    /// <summary>The account whose source answers.</summary>
    [JsonPropertyName("accountId")]
    public AccountId? AccountId { get; init; }

    /// <summary>The addresses.</summary>
    [JsonPropertyName("attendees")]
    public IReadOnlyList<string>? Attendees { get; init; }

    /// <summary>How long the slot must be.</summary>
    [JsonPropertyName("durationMinutes")]
    public required int DurationMinutes { get; init; }

    /// <summary>The start of the range.</summary>
    [JsonPropertyName("from")]
    public required DateTimeOffset From { get; init; }

    /// <summary>The end of the range.</summary>
    [JsonPropertyName("to")]
    public required DateTimeOffset To { get; init; }

    /// <summary>Only inside the working hours.</summary>
    [JsonPropertyName("workingHoursOnly")]
    public bool? WorkingHoursOnly { get; init; }

    /// <summary>The most slots.</summary>
    [JsonPropertyName("limit")]
    public int? Limit { get; init; }
}

/// <summary>api.CalendarFindFreeTimeResult.</summary>
public sealed record CalendarFindFreeTimeResult
{
    /// <summary>Earliest first; always written.</summary>
    [JsonPropertyName("slots")]
    [JsonConverter(typeof(NullAsEmptyListConverter<TimeSlot>))]
    public IReadOnlyList<TimeSlot> Slots { get; init => field = value ?? []; } = [];

    /// <summary>The attendees without free/busy; always written.</summary>
    [JsonPropertyName("unknown")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Unknown { get; init => field = value ?? []; } = [];
}

// ----------------------------------------------------------------------
// event.* (docs/api.md §4.15)
// ----------------------------------------------------------------------

/// <summary>
/// api.EventListParams: to minus from at most <see cref="API.Limits.MaxEventListDays"/> days; a
/// null or zero limit is <see cref="API.Limits.DefaultEventListResults"/>, at most
/// <see cref="API.Limits.MaxEventListResults"/>. A null <see cref="IncludeDeclined"/> is the
/// showDeclined preference; a null <see cref="IncludeInvitations"/> is true.
/// </summary>
public sealed record EventListParams
{
    /// <summary>The start of the range.</summary>
    [JsonPropertyName("from")]
    public required DateTimeOffset From { get; init; }

    /// <summary>The end of the range.</summary>
    [JsonPropertyName("to")]
    public required DateTimeOffset To { get; init; }

    /// <summary>The zone all-day events and floating events are placed in.</summary>
    [JsonPropertyName("timeZone")]
    public required TimeZoneName TimeZone { get; init; }

    /// <summary>Only these calendars.</summary>
    [JsonPropertyName("calendarIds")]
    public IReadOnlyList<CalendarId>? CalendarIds { get; init; }

    /// <summary>Only these accounts.</summary>
    [JsonPropertyName("accountIds")]
    public IReadOnlyList<AccountId>? AccountIds { get; init; }

    /// <summary>Also the hidden calendars.</summary>
    [JsonPropertyName("includeHidden")]
    public bool? IncludeHidden { get; init; }

    /// <summary>Also declined events.</summary>
    [JsonPropertyName("includeDeclined")]
    public bool? IncludeDeclined { get; init; }

    /// <summary>Also unanswered invitations not in any calendar.</summary>
    [JsonPropertyName("includeInvitations")]
    public bool? IncludeInvitations { get; init; }

    /// <summary>The most occurrences.</summary>
    [JsonPropertyName("limit")]
    public int? Limit { get; init; }
}

/// <summary>api.EventListResult.</summary>
public sealed record EventListResult
{
    /// <summary>Sorted by start; always written.</summary>
    [JsonPropertyName("occurrences")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Occurrence>))]
    public IReadOnlyList<Occurrence> Occurrences { get; init => field = value ?? []; } = [];

    /// <summary>The limit was reached.</summary>
    [JsonPropertyName("truncated")]
    public required bool Truncated { get; init; }

    /// <summary>
    /// False: a part of the range outside an account's window could not be fetched; what is
    /// stored is returned.
    /// </summary>
    [JsonPropertyName("complete")]
    public required bool Complete { get; init; }
}

/// <summary>api.EventGetParams.</summary>
public sealed record EventGetParams
{
    /// <summary>The event.</summary>
    [JsonPropertyName("eventId")]
    public required CalendarEventId EventId { get; init; }

    /// <summary>The instance of a series.</summary>
    [JsonPropertyName("recurrenceId")]
    public DateOrTime? RecurrenceId { get; init; }

    /// <summary>The zone a floating event is placed in.</summary>
    [JsonPropertyName("timeZone")]
    public TimeZoneName? TimeZone { get; init; }
}

/// <summary>api.EventGetResult.</summary>
public sealed record EventGetResult
{
    /// <summary>The event.</summary>
    [JsonPropertyName("event")]
    public required CalendarEvent Event { get; init; }
}

/// <summary>api.EventSearchParams: the query is 1 to 256 bytes; a null or zero limit is 50, at most <see cref="API.Limits.MaxEventSearchResults"/>.</summary>
public sealed record EventSearchParams
{
    /// <summary>The words.</summary>
    [JsonPropertyName("query")]
    public required string Query { get; init; }

    /// <summary>Only from here.</summary>
    [JsonPropertyName("from")]
    public DateTimeOffset? From { get; init; }

    /// <summary>Only up to here.</summary>
    [JsonPropertyName("to")]
    public DateTimeOffset? To { get; init; }

    /// <summary>Only these calendars.</summary>
    [JsonPropertyName("calendarIds")]
    public IReadOnlyList<CalendarId>? CalendarIds { get; init; }

    /// <summary>The most occurrences.</summary>
    [JsonPropertyName("limit")]
    public int? Limit { get; init; }
}

/// <summary>api.EventSearchResult.</summary>
public sealed record EventSearchResult
{
    /// <summary>One per event; always written.</summary>
    [JsonPropertyName("occurrences")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Occurrence>))]
    public IReadOnlyList<Occurrence> Occurrences { get; init => field = value ?? []; } = [];

    /// <summary>The matches, at most 1000.</summary>
    [JsonPropertyName("total")]
    public required int Total { get; init; }
}

/// <summary>api.InvitationsSent: how the invitations, updates or cancellations of a write left.</summary>
public sealed record InvitationsSent
{
    /// <summary>Who delivered them.</summary>
    [JsonPropertyName("via")]
    public required InvitationsVia Via { get; init; }

    /// <summary>How many.</summary>
    [JsonPropertyName("count")]
    public required int Count { get; init; }
}

/// <summary>api.EventCreateParams: a null <see cref="SendInvitations"/> is true.</summary>
public sealed record EventCreateParams
{
    /// <summary>The calendar.</summary>
    [JsonPropertyName("calendarId")]
    public required CalendarId CalendarId { get; init; }

    /// <summary>The event.</summary>
    [JsonPropertyName("event")]
    public required EventInput Event { get; init; }

    /// <summary>Send the invitations.</summary>
    [JsonPropertyName("sendInvitations")]
    public bool? SendInvitations { get; init; }

    /// <summary>The iMIP subject and body.</summary>
    [JsonPropertyName("mail")]
    public MailText? Mail { get; init; }
}

/// <summary>api.EventCreateResult.</summary>
public sealed record EventCreateResult
{
    /// <summary>The stored event.</summary>
    [JsonPropertyName("event")]
    public required CalendarEvent Event { get; init; }

    /// <summary>How the invitations left.</summary>
    [JsonPropertyName("invitations")]
    public required InvitationsSent Invitations { get; init; }
}

/// <summary>api.EventUpdateParams: <see cref="Event"/> replaces every member of the scope's target; a null <see cref="SendUpdates"/> is true.</summary>
public sealed record EventUpdateParams
{
    /// <summary>The event.</summary>
    [JsonPropertyName("eventId")]
    public required CalendarEventId EventId { get; init; }

    /// <summary>The instance of a series.</summary>
    [JsonPropertyName("recurrenceId")]
    public DateOrTime? RecurrenceId { get; init; }

    /// <summary>What the change applies to.</summary>
    [JsonPropertyName("scope")]
    public required EditScope Scope { get; init; }

    /// <summary>The etag the client read.</summary>
    [JsonPropertyName("etag")]
    public required string ETag { get; init; }

    /// <summary>The whole edited event.</summary>
    [JsonPropertyName("event")]
    public required EventInput Event { get; init; }

    /// <summary>Send the updates.</summary>
    [JsonPropertyName("sendUpdates")]
    public bool? SendUpdates { get; init; }

    /// <summary>The iMIP subject and body.</summary>
    [JsonPropertyName("mail")]
    public MailText? Mail { get; init; }
}

/// <summary>api.EventUpdateResult.</summary>
public sealed record EventUpdateResult
{
    /// <summary>The stored event.</summary>
    [JsonPropertyName("event")]
    public required CalendarEvent Event { get; init; }

    /// <summary>Overrides that no longer fit the changed rule.</summary>
    [JsonPropertyName("droppedExceptions")]
    public required int DroppedExceptions { get; init; }

    /// <summary>How the updates left.</summary>
    [JsonPropertyName("invitations")]
    public required InvitationsSent Invitations { get; init; }
}

/// <summary>api.EventDeleteParams: a null <see cref="Notify"/> is true; the comment is at most <see cref="API.Limits.MaxCommentBytes"/>.</summary>
public sealed record EventDeleteParams
{
    /// <summary>The event.</summary>
    [JsonPropertyName("eventId")]
    public required CalendarEventId EventId { get; init; }

    /// <summary>The instance of a series.</summary>
    [JsonPropertyName("recurrenceId")]
    public DateOrTime? RecurrenceId { get; init; }

    /// <summary>What the delete applies to.</summary>
    [JsonPropertyName("scope")]
    public required EditScope Scope { get; init; }

    /// <summary>The etag the client read.</summary>
    [JsonPropertyName("etag")]
    public required string ETag { get; init; }

    /// <summary>Tell the attendees.</summary>
    [JsonPropertyName("notify")]
    public bool? Notify { get; init; }

    /// <summary>A note to them.</summary>
    [JsonPropertyName("comment")]
    public string? Comment { get; init; }

    /// <summary>The iMIP subject and body.</summary>
    [JsonPropertyName("mail")]
    public MailText? Mail { get; init; }
}

/// <summary>api.EventDeleteResult.</summary>
public sealed record EventDeleteResult
{
    /// <summary>How the cancellations left.</summary>
    [JsonPropertyName("invitations")]
    public required InvitationsSent Invitations { get; init; }
}

/// <summary>
/// api.EventRespondParams: scope this or all; the response accepted, tentative or declined; a null
/// <see cref="SendResponse"/> is true; a proposed time needs both ends and
/// <see cref="CalendarEvent.CanProposeNewTime"/>.
/// </summary>
public sealed record EventRespondParams
{
    /// <summary>The event.</summary>
    [JsonPropertyName("eventId")]
    public required CalendarEventId EventId { get; init; }

    /// <summary>The instance of a series.</summary>
    [JsonPropertyName("recurrenceId")]
    public DateOrTime? RecurrenceId { get; init; }

    /// <summary>This or all.</summary>
    [JsonPropertyName("scope")]
    public required EditScope Scope { get; init; }

    /// <summary>The etag the client read.</summary>
    [JsonPropertyName("etag")]
    public required string ETag { get; init; }

    /// <summary>Accepted, tentative or declined.</summary>
    [JsonPropertyName("response")]
    public required EventResponse Response { get; init; }

    /// <summary>Send the answer.</summary>
    [JsonPropertyName("sendResponse")]
    public bool? SendResponse { get; init; }

    /// <summary>A note to the organiser.</summary>
    [JsonPropertyName("comment")]
    public string? Comment { get; init; }

    /// <summary>A proposed new start.</summary>
    [JsonPropertyName("proposedStart")]
    public DateTimeOffset? ProposedStart { get; init; }

    /// <summary>A proposed new end.</summary>
    [JsonPropertyName("proposedEnd")]
    public DateTimeOffset? ProposedEnd { get; init; }

    /// <summary>The iMIP subject and body.</summary>
    [JsonPropertyName("mail")]
    public MailText? Mail { get; init; }
}

/// <summary>api.EventRespondResult.</summary>
public sealed record EventRespondResult
{
    /// <summary>The stored event.</summary>
    [JsonPropertyName("event")]
    public required CalendarEvent Event { get; init; }

    /// <summary>Who delivered the answer.</summary>
    [JsonPropertyName("via")]
    public required InvitationsVia Via { get; init; }
}

/// <summary>api.EventSnoozeAlertParams: 1 to 1440 minutes.</summary>
public sealed record EventSnoozeAlertParams
{
    /// <summary>The alert.</summary>
    [JsonPropertyName("alertId")]
    public required CalendarAlertId AlertId { get; init; }

    /// <summary>How long to snooze.</summary>
    [JsonPropertyName("minutes")]
    public required int Minutes { get; init; }
}

/// <summary>api.EventDismissAlertParams.</summary>
public sealed record EventDismissAlertParams
{
    /// <summary>The alert.</summary>
    [JsonPropertyName("alertId")]
    public required CalendarAlertId AlertId { get; init; }
}

// ----------------------------------------------------------------------
// invitation.* (docs/api.md §4.16)
// ----------------------------------------------------------------------

/// <summary>api.InvitationSummary: rides on a listed message (<see cref="MessageSummary.Invitation"/>). Title and names are untrusted display text.</summary>
public record InvitationSummary
{
    /// <summary>The iTIP METHOD.</summary>
    [JsonPropertyName("method")]
    public required InvitationMethod Method { get; init; }

    /// <summary>The list pill.</summary>
    [JsonPropertyName("kind")]
    public required InvitationKind Kind { get; init; }

    /// <summary>What is left to do.</summary>
    [JsonPropertyName("state")]
    public required InvitationState State { get; init; }

    /// <summary>Display text.</summary>
    [JsonPropertyName("title")]
    public required string Title { get; init; }

    /// <summary>A timed event's start.</summary>
    [JsonPropertyName("start")]
    public DateTimeOffset? Start { get; init; }

    /// <summary>A timed event's end.</summary>
    [JsonPropertyName("end")]
    public DateTimeOffset? End { get; init; }

    /// <summary>An all-day event's first day.</summary>
    [JsonPropertyName("startDate")]
    public CalendarDate? StartDate { get; init; }

    /// <summary>An all-day event.</summary>
    [JsonPropertyName("allDay")]
    public bool? AllDay { get; init; }

    /// <summary>The organiser.</summary>
    [JsonPropertyName("organizer")]
    public Address? Organizer { get; init; }

    /// <summary>For a reply: the attendee who answered.</summary>
    [JsonPropertyName("replyFrom")]
    public Address? ReplyFrom { get; init; }

    /// <summary>The organiser is one of the account's own addresses: such a request is processed, with no response path.</summary>
    [JsonPropertyName("ownEvent")]
    public bool? OwnEvent { get; init; }
}

/// <summary>api.InvitationMe: the user on an invitation's attendee list.</summary>
public sealed record InvitationMe
{
    /// <summary>The user's address on the list.</summary>
    [JsonPropertyName("address")]
    public required string Address { get; init; }

    /// <summary>The user's answer.</summary>
    [JsonPropertyName("response")]
    public required EventResponse Response { get; init; }
}

/// <summary>api.InvitationPrevious: what the stored event had before an update; only what changed is set.</summary>
public sealed record InvitationPrevious
{
    /// <summary>The previous start.</summary>
    [JsonPropertyName("start")]
    public DateOrTime? Start { get; init; }

    /// <summary>The previous end.</summary>
    [JsonPropertyName("end")]
    public DateOrTime? End { get; init; }

    /// <summary>The previous location, when it changed.</summary>
    [JsonPropertyName("location")]
    public string? Location { get; init; }

    /// <summary>The previous rule, when it changed.</summary>
    [JsonPropertyName("recurrence")]
    public Recurrence? Recurrence { get; init; }
}

/// <summary>api.InvitationConflict: a stored occurrence overlapping an invitation.</summary>
public sealed record InvitationConflict
{
    /// <summary>Null for a private event of a shared calendar.</summary>
    [JsonPropertyName("title")]
    public string? Title { get; init; }

    /// <summary>The start.</summary>
    [JsonPropertyName("start")]
    public required DateTimeOffset Start { get; init; }

    /// <summary>The end.</summary>
    [JsonPropertyName("end")]
    public required DateTimeOffset End { get; init; }

    /// <summary>Its calendar.</summary>
    [JsonPropertyName("calendarId")]
    public required CalendarId CalendarId { get; init; }

    /// <summary>The event.</summary>
    [JsonPropertyName("eventId")]
    public required CalendarEventId EventId { get; init; }
}

/// <summary>api.DayBusy: a busy span of an invitation's mini day strip.</summary>
public sealed record DayBusy
{
    /// <summary>The start.</summary>
    [JsonPropertyName("start")]
    public required DateTimeOffset Start { get; init; }

    /// <summary>The end.</summary>
    [JsonPropertyName("end")]
    public required DateTimeOffset End { get; init; }

    /// <summary>Its calendar.</summary>
    [JsonPropertyName("calendarId")]
    public CalendarId? CalendarId { get; init; }
}

/// <summary>api.InvitationDay: the mini day strip of an invitation card.</summary>
public sealed record InvitationDay
{
    /// <summary>The start of the strip.</summary>
    [JsonPropertyName("from")]
    public required DateTimeOffset From { get; init; }

    /// <summary>The end of the strip.</summary>
    [JsonPropertyName("to")]
    public required DateTimeOffset To { get; init; }

    /// <summary>The busy spans; always written.</summary>
    [JsonPropertyName("busy")]
    [JsonConverter(typeof(NullAsEmptyListConverter<DayBusy>))]
    public IReadOnlyList<DayBusy> Busy { get; init => field = value ?? []; } = [];
}

/// <summary>api.InvitationTarget: a calendar the invitation's event can go to.</summary>
public sealed record InvitationTarget
{
    /// <summary>The calendar.</summary>
    [JsonPropertyName("calendarId")]
    public required CalendarId CalendarId { get; init; }

    /// <summary>The account's default.</summary>
    [JsonPropertyName("isDefault")]
    public bool? IsDefault { get; init; }
}

/// <summary>
/// api.InvitationCard: the invitation card of a message (<see cref="Message.Invitation"/>),
/// rendered by a client from these members, never from HTML. Go embeds InvitationSummary, so on the
/// wire its members sit beside these.
/// </summary>
public sealed record InvitationCard : InvitationSummary
{
    /// <summary>The calendar part of the message.</summary>
    [JsonPropertyName("partId")]
    public required string PartId { get; init; }

    /// <summary>The iCalendar UID.</summary>
    [JsonPropertyName("uid")]
    public required string Uid { get; init; }

    /// <summary>The instance of a series.</summary>
    [JsonPropertyName("recurrenceId")]
    public DateOrTime? RecurrenceId { get; init; }

    /// <summary>The SEQUENCE.</summary>
    [JsonPropertyName("sequence")]
    public required int Sequence { get; init; }

    /// <summary>An all-day event's end, exclusive.</summary>
    [JsonPropertyName("endDate")]
    public CalendarDate? EndDate { get; init; }

    /// <summary>Its zone.</summary>
    [JsonPropertyName("timeZone")]
    public TimeZoneName? TimeZone { get; init; }

    /// <summary>The rule.</summary>
    [JsonPropertyName("recurrence")]
    public Recurrence? Recurrence { get; init; }

    /// <summary>Display text.</summary>
    [JsonPropertyName("location")]
    public string? Location { get; init; }

    /// <summary>The video-call link.</summary>
    [JsonPropertyName("videoCall")]
    public VideoCall? VideoCall { get; init; }

    /// <summary>Plain text.</summary>
    [JsonPropertyName("notes")]
    public string? Notes { get; init; }

    /// <summary>The organiser's domain is not the From domain.</summary>
    [JsonPropertyName("organizerMismatch")]
    public bool? OrganizerMismatch { get; init; }

    /// <summary>All attendees, also those not listed.</summary>
    [JsonPropertyName("attendeeCount")]
    public required int AttendeeCount { get; init; }

    /// <summary>At most <see cref="API.Limits.MaxInvitationAttendees"/>.</summary>
    [JsonPropertyName("attendees")]
    public IReadOnlyList<Attendee>? Attendees { get; init; }

    /// <summary>Null: the user is not on the list.</summary>
    [JsonPropertyName("me")]
    public InvitationMe? Me { get; init; }

    /// <summary>The user's answer; may be set with <see cref="RespondedAt"/> null (an answer given in another client).</summary>
    [JsonPropertyName("response")]
    public EventResponse? Response { get; init; }

    /// <summary>When the user answered.</summary>
    [JsonPropertyName("respondedAt")]
    public DateTimeOffset? RespondedAt { get; init; }

    /// <summary>The stored event it is linked to.</summary>
    [JsonPropertyName("eventId")]
    public CalendarEventId? EventId { get; init; }

    /// <summary>The calendar of that event.</summary>
    [JsonPropertyName("calendarId")]
    public CalendarId? CalendarId { get; init; }

    /// <summary>The stored event's time before an update.</summary>
    [JsonPropertyName("previous")]
    public InvitationPrevious? Previous { get; init; }

    /// <summary>At most 3; always written.</summary>
    [JsonPropertyName("conflicts")]
    [JsonConverter(typeof(NullAsEmptyListConverter<InvitationConflict>))]
    public IReadOnlyList<InvitationConflict> Conflicts { get; init => field = value ?? []; } = [];

    /// <summary>The mini day strip.</summary>
    [JsonPropertyName("day")]
    public required InvitationDay Day { get; init; }

    /// <summary>"Add to:", the account's default first; empty means the answer only; always written.</summary>
    [JsonPropertyName("targets")]
    [JsonConverter(typeof(NullAsEmptyListConverter<InvitationTarget>))]
    public IReadOnlyList<InvitationTarget> Targets { get; init => field = value ?? []; } = [];

    /// <summary>The server chose the calendar (Graph).</summary>
    [JsonPropertyName("fixedTarget")]
    public bool? FixedTarget { get; init; }

    /// <summary>The user may answer it.</summary>
    [JsonPropertyName("canRespond")]
    public required bool CanRespond { get; init; }

    /// <summary>The user may propose a new time.</summary>
    [JsonPropertyName("canProposeNewTime")]
    public required bool CanProposeNewTime { get; init; }

    /// <summary>Who delivers the answer.</summary>
    [JsonPropertyName("replyVia")]
    public required InvitationsVia ReplyVia { get; init; }

    /// <summary>The account the answer leaves from.</summary>
    [JsonPropertyName("replyAccountId")]
    public AccountId? ReplyAccountId { get; init; }

    /// <summary>The organiser it goes to.</summary>
    [JsonPropertyName("replyTo")]
    public Address? ReplyTo { get; init; }

    /// <summary>For a reply: the attendee's answer was applied to the user's event.</summary>
    [JsonPropertyName("replyApplied")]
    public bool? ReplyApplied { get; init; }
}

/// <summary>api.InvitationListParams: a null or zero limit is 200, at most <see cref="API.Limits.MaxInvitationList"/>.</summary>
public sealed record InvitationListParams
{
    /// <summary>Only these accounts.</summary>
    [JsonPropertyName("accountIds")]
    public IReadOnlyList<AccountId>? AccountIds { get; init; }

    /// <summary>Only this state.</summary>
    [JsonPropertyName("state")]
    public InvitationState? State { get; init; }

    /// <summary>The most invitations.</summary>
    [JsonPropertyName("limit")]
    public int? Limit { get; init; }
}

/// <summary>api.InvitationListItem: one invitation (the newest message of its UID).</summary>
public sealed record InvitationListItem
{
    /// <summary>Its account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>What the list shows.</summary>
    [JsonPropertyName("summary")]
    public required InvitationSummary Summary { get; init; }
}

/// <summary>api.InvitationListResult.</summary>
public sealed record InvitationListResult
{
    /// <summary>By start; always written.</summary>
    [JsonPropertyName("invitations")]
    [JsonConverter(typeof(NullAsEmptyListConverter<InvitationListItem>))]
    public IReadOnlyList<InvitationListItem> Invitations { get; init => field = value ?? []; } = [];
}

/// <summary>api.InvitationRespondParams: the response accepted, tentative or declined; the calendar one of the card's targets; a null <see cref="SendResponse"/> is true.</summary>
public sealed record InvitationRespondParams
{
    /// <summary>The message's account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>Accepted, tentative or declined.</summary>
    [JsonPropertyName("response")]
    public required EventResponse Response { get; init; }

    /// <summary>The calendar to add the event to.</summary>
    [JsonPropertyName("calendarId")]
    public CalendarId? CalendarId { get; init; }

    /// <summary>Send the answer.</summary>
    [JsonPropertyName("sendResponse")]
    public bool? SendResponse { get; init; }

    /// <summary>A note to the organiser.</summary>
    [JsonPropertyName("comment")]
    public string? Comment { get; init; }

    /// <summary>A proposed new start.</summary>
    [JsonPropertyName("proposedStart")]
    public DateTimeOffset? ProposedStart { get; init; }

    /// <summary>A proposed new end.</summary>
    [JsonPropertyName("proposedEnd")]
    public DateTimeOffset? ProposedEnd { get; init; }

    /// <summary>The iMIP subject and body.</summary>
    [JsonPropertyName("mail")]
    public MailText? Mail { get; init; }
}

/// <summary>api.InvitationRespondResult.</summary>
public sealed record InvitationRespondResult
{
    /// <summary>The card as it is now.</summary>
    [JsonPropertyName("invitation")]
    public required InvitationCard Invitation { get; init; }

    /// <summary>The stored event.</summary>
    [JsonPropertyName("eventId")]
    public CalendarEventId? EventId { get; init; }

    /// <summary>The folder the message was archived to (afterAnswer).</summary>
    [JsonPropertyName("movedTo")]
    public FolderId? MovedTo { get; init; }
}

/// <summary>api.InvitationApplyCancelParams.</summary>
public sealed record InvitationApplyCancelParams
{
    /// <summary>The message's account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>A message of kind cancelled.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}

/// <summary>api.InvitationApplyCancelResult.</summary>
public sealed record InvitationApplyCancelResult
{
    /// <summary>False: no stored event was linked.</summary>
    [JsonPropertyName("removed")]
    public required bool Removed { get; init; }
}

// ----------------------------------------------------------------------
// Notifications (docs/api.md §5)
// ----------------------------------------------------------------------

/// <summary>
/// api.CalendarChangedNotification (<c>notify.calendarChanged</c>): stored calendars or occurrences
/// changed; <see cref="From"/> and <see cref="To"/> bound the changed occurrences when known.
/// Without calendars: the preferences changed. Every member is optional.
/// </summary>
public sealed record CalendarChangedNotification
{
    /// <summary>The accounts.</summary>
    [JsonPropertyName("accountIds")]
    public IReadOnlyList<AccountId>? AccountIds { get; init; }

    /// <summary>The calendars.</summary>
    [JsonPropertyName("calendarIds")]
    public IReadOnlyList<CalendarId>? CalendarIds { get; init; }

    /// <summary>The start of the changed span.</summary>
    [JsonPropertyName("from")]
    public DateTimeOffset? From { get; init; }

    /// <summary>The end of the changed span.</summary>
    [JsonPropertyName("to")]
    public DateTimeOffset? To { get; init; }
}

/// <summary>api.CalendarStateNotification (<c>notify.calendarState</c>): an account's calendar synchronisation state changed.</summary>
public sealed record CalendarStateNotification
{
    /// <summary>The new state.</summary>
    [JsonPropertyName("state")]
    public required CalendarSyncState State { get; init; }
}

/// <summary>api.EventAlertNotification (<c>notify.eventAlert</c>): an alert is due, or was dismissed.</summary>
public sealed record EventAlertNotification
{
    /// <summary>The alert.</summary>
    [JsonPropertyName("alert")]
    public required EventAlert Alert { get; init; }
}

/// <summary>api.InvitationsChangedNotification (<c>notify.invitationsChanged</c>): the unanswered invitations of these accounts (none named is any) changed.</summary>
public sealed record InvitationsChangedNotification
{
    /// <summary>The accounts.</summary>
    [JsonPropertyName("accountIds")]
    public IReadOnlyList<AccountId>? AccountIds { get; init; }
}

// ----------------------------------------------------------------------
// Timeouts
// ----------------------------------------------------------------------

/// <summary>
/// How long a client waits for the calendar calls that talk to a server, as
/// docs/api.md §4.1, §4.14 to §4.16 say ("A client allows ..."). The other
/// calendar methods are local and wait <c>RpcTimeouts.Default</c>.
/// </summary>
public static class CalendarTimeouts
{
    /// <summary><c>account.discoverCalDAV</c>.</summary>
    public static readonly TimeSpan Discover = TimeSpan.FromSeconds(20);

    /// <summary><c>account.listCalDAVCalendars</c>.</summary>
    public static readonly TimeSpan ListCalendars = TimeSpan.FromSeconds(45);

    /// <summary><c>calendar.freeBusy</c>, <c>calendar.findFreeTime</c>.</summary>
    public static readonly TimeSpan FreeBusy = TimeSpan.FromSeconds(20);

    /// <summary><c>event.list</c>: a part of the range may be fetched online.</summary>
    public static readonly TimeSpan EventList = TimeSpan.FromSeconds(10);

    /// <summary>
    /// <c>event.create</c>, <c>event.update</c>, <c>event.delete</c>,
    /// <c>event.respond</c>, <c>invitation.respond</c> and
    /// <c>invitation.applyCancel</c>: the server answers before the result.
    /// </summary>
    public static readonly TimeSpan Write = TimeSpan.FromSeconds(45);
}
