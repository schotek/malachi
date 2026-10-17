// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: what a new-message toast carries back when it is
// clicked (docs/windows-port.md §10), the counterpart of the userInfo of
// macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (accountId, messageId) and of GTK's default action app.show. The toast
// gets the pairs through AppNotificationBuilder.AddArgument, which escapes
// "%", ";" and "=" in keys and values (%25, %3B, %3D) and joins them as
// "key=value;key=value"; a click hands them back already split
// (AppNotificationActivatedEventArgs.Arguments) and as that string
// (Argument), which Parse also reads.
//
// An event's alert toast (docs/calendar.md §5.9; macOS presentAlert's
// userInfo and its actions JOIN and SNOOZE, alertTarget) carries the
// alert, its event, the occurrence, the account and the day a click opens
// in the Calendar (RFC 3339, UTC), once for the toast and once for each of
// its buttons, since a button hands back only its own pairs; the action says
// which was clicked (ForAlert). Parse gives such a click its Alert and no
// message.
//
// A new message with a calendar part (docs/calendar.md §5.9; macOS's
// categories INVITATION and CANCELLATION) has buttons: Accept (an
// invitation) or Remove from Calendar (a cancellation), each with the
// account and the message and its own action (ForInvitation), and Show,
// which carries the toast's own pairs (For) and so opens the message as a
// click on the toast does. Parse gives Accept and Remove from Calendar their
// Invitation and no message: they answer the card without opening it.

using System;
using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;

namespace Malachi.Platform.Windows.Notifications;

/// <summary>The arguments of a new-message toast and of an event's alert toast.</summary>
public static class NotificationArguments
{
    /// <summary>The key of what a click does.</summary>
    public const string ActionKey = "action";

    /// <summary>A new-message toast's action: open the message (the main window without one).</summary>
    public const string OpenAction = "open";

    /// <summary>An alert toast's click: the Calendar with the event's detail (Swift's default action).</summary>
    public const string AlertOpenAction = "alert";

    /// <summary>An alert toast's Join button (Swift <c>JOIN</c>).</summary>
    public const string AlertJoinAction = "join";

    /// <summary>An alert toast's Snooze button (Swift <c>SNOOZE</c>).</summary>
    public const string AlertSnoozeAction = "snooze";

    /// <summary>An invitation's toast's Accept (Swift <c>ACCEPT</c>).</summary>
    public const string InvitationAcceptAction = "accept";

    /// <summary>A cancellation's toast's Remove from Calendar (Swift <c>REMOVE_FROM_CALENDAR</c>).</summary>
    public const string InvitationRemoveAction = "removeFromCalendar";

    /// <summary>The key of an alert toast's alert.</summary>
    public const string AlertKey = "alertId";

    /// <summary>The key of an alert toast's event.</summary>
    public const string EventKey = "eventId";

    /// <summary>The key of an alert toast's occurrence (its wire form); absent for a single event.</summary>
    public const string RecurrenceKey = "recurrenceId";

    /// <summary>The key of the day an alert toast's click opens (RFC 3339, UTC); absent without one.</summary>
    public const string DayKey = "day";

    /// <summary>The key of the message's account.</summary>
    public const string AccountKey = "account";

    /// <summary>The key of the message.</summary>
    public const string MessageKey = "message";

    /// <summary>The pairs a toast of <paramref name="n"/> carries, in order.</summary>
    public static IReadOnlyList<KeyValuePair<string, string>> For(DesktopNotification n)
    {
        ArgumentNullException.ThrowIfNull(n);
        return
        [
            new(ActionKey, OpenAction),
            new(AccountKey, n.AccountId.Value ?? ""),
            new(MessageKey, n.MessageId.Value ?? ""),
        ];
    }

    /// <summary>
    /// The pairs of the Accept or Remove from Calendar button of
    /// <paramref name="n"/>'s toast, in order: the action, the account, the
    /// message.
    /// </summary>
    public static IReadOnlyList<KeyValuePair<string, string>> ForInvitation(DesktopNotification n, InvitationNotificationResponse response)
    {
        ArgumentNullException.ThrowIfNull(n);
        return
        [
            new(ActionKey, response == InvitationNotificationResponse.RemoveFromCalendar ? InvitationRemoveAction : InvitationAcceptAction),
            new(AccountKey, n.AccountId.Value ?? ""),
            new(MessageKey, n.MessageId.Value ?? ""),
        ];
    }

    /// <summary>
    /// The pairs of an event's alert toast (<paramref name="response"/>
    /// <see cref="CalendarAlertResponse.Open"/>) or of one of its buttons
    /// (Join, Snooze), in order: the action, the alert, the event, the
    /// occurrence when there is one, the account, the day when there is one.
    /// </summary>
    public static IReadOnlyList<KeyValuePair<string, string>> ForAlert(CalendarAlertsController.Request request, CalendarAlertResponse response)
    {
        ArgumentNullException.ThrowIfNull(request);
        var pairs = new List<KeyValuePair<string, string>>
        {
            new(ActionKey, response switch
            {
                CalendarAlertResponse.Join => AlertJoinAction,
                CalendarAlertResponse.Snooze => AlertSnoozeAction,
                _ => AlertOpenAction,
            }),
            new(AlertKey, request.AlertId.Value ?? ""),
            new(EventKey, request.EventId.Value ?? ""),
        };
        if (request.RecurrenceId is { Value.Length: > 0 } recurrence)
        {
            pairs.Add(new(RecurrenceKey, recurrence.Value));
        }
        pairs.Add(new(AccountKey, request.AccountId.Value ?? ""));
        if (request.Day is { } day)
        {
            pairs.Add(new(DayKey, Rfc3339.Format(day)));
        }
        return pairs;
    }

    /// <summary>
    /// The activation of a click with <paramref name="arguments"/>, the
    /// pairs as the platform split them. The ids are kept when present and
    /// not empty; a click without them shows the main window. An alert
    /// toast's click (one of the alert actions, with its alert and event)
    /// names no message and carries <see cref="NotificationActivation.Alert"/>
    /// (macOS <c>alertTarget</c>): an occurrence or a day that does not read
    /// is dropped, not the click. Accept and Remove from Calendar of an
    /// invitation's toast (with their account and message) name no message
    /// either and carry <see cref="NotificationActivation.Invitation"/>; one
    /// without both ids is an ordinary click.
    /// </summary>
    public static NotificationActivation Parse(IEnumerable<KeyValuePair<string, string>>? arguments)
    {
        var values = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var (key, value) in arguments ?? [])
        {
            if (!string.IsNullOrEmpty(value))
            {
                values[key] = value;
            }
        }
        if (AlertOf(values) is { } alert)
        {
            return new NotificationActivation(null, null) { Alert = alert };
        }
        if (InvitationOf(values) is { } invitation)
        {
            return new NotificationActivation(null, null) { Invitation = invitation };
        }
        AccountId? accountId = values.TryGetValue(AccountKey, out var account) ? new AccountId(account) : default(AccountId?);
        MessageId? messageId = values.TryGetValue(MessageKey, out var message) ? new MessageId(message) : default(MessageId?);
        return new NotificationActivation(accountId, messageId);
    }

    /// <summary>
    /// The activation of a click whose arguments came as one string,
    /// <c>key=value;key=value</c> with <c>%</c>, <c>;</c> and <c>=</c>
    /// escaped as AppNotificationBuilder escapes them.
    /// </summary>
    public static NotificationActivation Parse(string? argument) => Parse(Split(argument));

    // The alert of an alert toast's click; null for any other click.
    private static CalendarAlertActivation? AlertOf(Dictionary<string, string> values)
    {
        if (!values.TryGetValue(ActionKey, out var action))
        {
            return null;
        }
        CalendarAlertResponse? response = action switch
        {
            AlertOpenAction => CalendarAlertResponse.Open,
            AlertJoinAction => CalendarAlertResponse.Join,
            AlertSnoozeAction => CalendarAlertResponse.Snooze,
            _ => null,
        };
        if (response is not { } r || !values.TryGetValue(AlertKey, out var alert) || !values.TryGetValue(EventKey, out var ev))
        {
            return null;
        }
        var recurrence = values.TryGetValue(RecurrenceKey, out var rec) && IsDateOrTime(rec) ? new DateOrTime(rec) : default(DateOrTime?);
        var day = values.TryGetValue(DayKey, out var d) ? Rfc3339.Parse(d) : null;
        return new CalendarAlertActivation(
            r, new CalendarAlertId(alert), new CalendarEventId(ev), recurrence, new AccountId(values.GetValueOrDefault(AccountKey, "")), day);
    }

    // The answer of an invitation toast's button; null for any other click.
    private static InvitationActivation? InvitationOf(Dictionary<string, string> values)
    {
        if (!values.TryGetValue(ActionKey, out var action))
        {
            return null;
        }
        InvitationNotificationResponse? response = action switch
        {
            InvitationAcceptAction => InvitationNotificationResponse.Accept,
            InvitationRemoveAction => InvitationNotificationResponse.RemoveFromCalendar,
            _ => null,
        };
        if (response is not { } r || !values.TryGetValue(AccountKey, out var account) || !values.TryGetValue(MessageKey, out var message))
        {
            return null;
        }
        return new InvitationActivation(r, new AccountId(account), new MessageId(message));
    }

    // An occurrence's wire form: a timestamp (RFC 3339) or a day
    // (YYYY-MM-DD), as macOS alertTarget reads it back.
    private static bool IsDateOrTime(string s) =>
        Rfc3339.Parse(s) is not null
        || (s.Length == 10 && DateTime.TryParseExact(s, "yyyy-MM-dd", CultureInfo.InvariantCulture, DateTimeStyles.None, out _));

    /// <summary>The pairs of an argument string, unescaped; a part without "=" has an empty value.</summary>
    public static IReadOnlyList<KeyValuePair<string, string>> Split(string? argument)
    {
        var pairs = new List<KeyValuePair<string, string>>();
        if (string.IsNullOrEmpty(argument))
        {
            return pairs;
        }
        foreach (var part in argument.Split(';'))
        {
            if (part.Length == 0)
            {
                continue;
            }
            var eq = part.IndexOf('=', StringComparison.Ordinal);
            var key = eq < 0 ? part : part[..eq];
            var value = eq < 0 ? "" : part[(eq + 1)..];
            pairs.Add(new(Unescape(key), Unescape(value)));
        }
        return pairs;
    }

    // %25, %3B and %3D back to %, ; and =; any other % sequence stays as it is.
    private static string Unescape(string s)
    {
        if (!s.Contains('%', StringComparison.Ordinal))
        {
            return s;
        }
        var sb = new System.Text.StringBuilder(s.Length);
        for (var i = 0; i < s.Length; i++)
        {
            if (s[i] == '%' && i + 2 < s.Length)
            {
                var code = s.AsSpan(i + 1, 2);
                char? c = code.Equals("25", StringComparison.Ordinal) ? '%'
                    : code.Equals("3B", StringComparison.OrdinalIgnoreCase) ? ';'
                    : code.Equals("3D", StringComparison.OrdinalIgnoreCase) ? '='
                    : null;
                if (c is { } decoded)
                {
                    sb.Append(decoded);
                    i += 2;
                    continue;
                }
            }
            sb.Append(s[i]);
        }
        return sb.ToString();
    }
}
