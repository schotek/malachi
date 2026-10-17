// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarViewTests.swift
// (activeCalendars); GTK: ui/internal/calendar/calendars_test.go
// (TestActiveCalendars).

using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarActiveTests
{
    private static Calendar Make(string id, bool? disabled = null, bool visible = true) => new()
    {
        Id = new CalendarId(id),
        AccountId = new AccountId("a"),
        RemoteId = id,
        Name = id,
        Color = "#3584e4",
        Visible = visible,
        Access = new CalendarAccess("owner"),
        Kind = new CalendarKind("events"),
        Disabled = disabled,
    };

    [Fact]
    public void ActiveCalendars()
    {
        var got = Cal.ActiveCalendars([Make("a"), Make("b", disabled: true), Make("c", visible: false), Make("d"), Make("e", disabled: false)]);
        Assert.Equal(["a", "c", "d", "e"], got.Select(c => c.Id.Value));
        Assert.Empty(Cal.ActiveCalendars(null));
        Assert.Empty(Cal.ActiveCalendars([]));
        Assert.Empty(Cal.ActiveCalendars([Make("x", disabled: true)]));
    }
}
