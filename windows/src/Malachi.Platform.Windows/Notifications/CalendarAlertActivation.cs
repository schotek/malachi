// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (NotificationService.AlertTarget with the response it came with);
// Swift-first, no GTK counterpart yet (docs/calendar.md §5.9). What an
// event's alert toast carries back when it or one of its buttons is
// clicked (NotificationArguments.ForAlert): the alert, its event and the
// day a click opens in the Calendar. The video call's link is not in the
// toast: Join asks event.get for it, as macOS does.

using System;
using Malachi.Core.Api;

namespace Malachi.Platform.Windows.Notifications;

/// <summary>A click on an event's alert toast or on one of its buttons (Swift <c>AlertTarget</c> and <c>AlertResponse</c>).</summary>
/// <param name="Response">What the click asks for.</param>
/// <param name="AlertId">The alert.</param>
/// <param name="EventId">The event.</param>
/// <param name="RecurrenceId">The occurrence of a series; null for a single event.</param>
/// <param name="AccountId">The event's account.</param>
/// <param name="Day">The day a click opens in the Calendar; null when the alert had none.</param>
public sealed record CalendarAlertActivation(
    CalendarAlertResponse Response,
    CalendarAlertId AlertId,
    CalendarEventId EventId,
    DateOrTime? RecurrenceId,
    AccountId AccountId,
    DateTimeOffset? Day);
