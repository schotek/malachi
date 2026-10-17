// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of NotificationArguments.ForAlert and the alert half of Parse: what
// an event's alert toast and its Join and Snooze buttons carry back when
// clicked, the counterpart of the userInfo of NotificationService.swift
// (presentAlert, alertTarget). Each button carries the whole alert, the
// action says which was clicked, a click that is not an alert's stays an
// ordinary one, and what does not read (an occurrence, a day) is dropped
// without losing the click.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Platform.Windows.Notifications;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Notifications;

public sealed class CalendarAlertArgumentsTests
{
    private static readonly DateTimeOffset Day = new(2026, 10, 16, 9, 30, 0, TimeSpan.FromHours(2));

    [Fact]
    public void AnAlertToastCarriesTheAlertItsEventTheOccurrenceTheAccountAndTheDay()
    {
        var pairs = NotificationArguments.ForAlert(Request(), CalendarAlertResponse.Open);
        Assert.Equal(
            [
                new("action", "alert"), new("alertId", "al_1"), new("eventId", "ev_1"),
                new("recurrenceId", "2026-10-16T07:30:00Z"), new("account", "acc_1"),
                new KeyValuePair<string, string>("day", "2026-10-16T07:30:00Z"),
            ],
            pairs);
    }

    [Fact]
    public void ASingleEventWithoutADayCarriesNeither()
    {
        var pairs = NotificationArguments.ForAlert(Request() with { RecurrenceId = null, Day = null }, CalendarAlertResponse.Snooze);
        Assert.Equal(
            [new("action", "snooze"), new("alertId", "al_1"), new("eventId", "ev_1"), new KeyValuePair<string, string>("account", "acc_1")],
            pairs);
    }

    [Theory]
    [InlineData(CalendarAlertResponse.Open, "alert")]
    [InlineData(CalendarAlertResponse.Join, "join")]
    [InlineData(CalendarAlertResponse.Snooze, "snooze")]
    public void EachClickComesBackWithItsAlertAndNoMessage(CalendarAlertResponse response, string action)
    {
        var pairs = NotificationArguments.ForAlert(Request(), response);
        Assert.Equal(new KeyValuePair<string, string>("action", action), pairs[0]);

        var a = NotificationArguments.Parse(pairs);
        Assert.Null(a.AccountId);
        Assert.Null(a.MessageId);
        Assert.Equal(
            new CalendarAlertActivation(
                response, new CalendarAlertId("al_1"), new CalendarEventId("ev_1"), new DateOrTime("2026-10-16T07:30:00Z"),
                new AccountId("acc_1"), Day),
            a.Alert);
    }

    [Fact]
    public void TheArgumentStringOfAButtonIsSplitAndUnescaped()
    {
        var a = NotificationArguments.Parse("action=join;alertId=al%3B1;eventId=ev%3D1;recurrenceId=2026-10-16;account=acc_1");
        Assert.Equal(
            new CalendarAlertActivation(
                CalendarAlertResponse.Join, new CalendarAlertId("al;1"), new CalendarEventId("ev=1"), new DateOrTime("2026-10-16"),
                new AccountId("acc_1"), null),
            a.Alert);
    }

    [Fact]
    public void AnOccurrenceOrADayThatDoesNotReadIsDroppedNotTheClick()
    {
        var a = NotificationArguments.Parse(
            [new("action", "snooze"), new("alertId", "al_1"), new("eventId", "ev_1"), new("recurrenceId", "next tuesday"), new("day", "soon")]);
        Assert.Equal(
            new CalendarAlertActivation(CalendarAlertResponse.Snooze, new CalendarAlertId("al_1"), new CalendarEventId("ev_1"), null, new AccountId(""), null),
            a.Alert);
    }

    [Fact]
    public void AnAlertActionWithoutItsAlertOrEventIsAnOrdinaryClick()
    {
        Assert.Equal(
            new NotificationActivation(new AccountId("a"), null),
            NotificationArguments.Parse([new("action", "join"), new("eventId", "ev_1"), new("account", "a")]));
        Assert.Equal(
            new NotificationActivation(null, null),
            NotificationArguments.Parse([new("action", "alert"), new("alertId", "al_1"), new("eventId", "")]));
        Assert.Null(NotificationArguments.Parse([new("action", "open"), new("alertId", "al_1"), new("eventId", "ev_1")]).Alert);
    }

    [Fact]
    public void ANewMessageToastsClickCarriesNoAlert()
    {
        Assert.Null(NotificationArguments.Parse("action=open;account=acc_1;message=m_1").Alert);
    }

    private static CalendarAlertsController.Request Request() => new()
    {
        AlertId = new CalendarAlertId("al_1"),
        Title = "Standup",
        Body = "in 5 minutes · Room B",
        Join = true,
        EventId = new CalendarEventId("ev_1"),
        RecurrenceId = new DateOrTime("2026-10-16T07:30:00Z"),
        AccountId = new AccountId("acc_1"),
        Day = Day,
    };
}
