// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarAvailabilityTests.swift;
// GTK: ui/internal/calendar/availability_test.go (TestAvailability,
// TestNextFreeSlot).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarAvailabilityTests
{
    // Wednesday 14 October 2026 in Prague.
    private static DateTimeOffset H(int hh, int mm = 0) => F.At(2026, 10, 14, hh, mm);

    private static BusyInterval Busy(DateTimeOffset s, DateTimeOffset e, string showAs) =>
        new() { Start = s, End = e, ShowAs = new ShowAs(showAs) };

    private static readonly CalendarFreeBusyResult Res = new()
    {
        Me = new Availability { Address = "me@example.org", Known = true, Busy = [Busy(H(9), H(10), ShowAs.Busy)] },
        Attendees =
        [
            new Availability
            {
                Address = "jana@example.org",
                Known = true,
                Busy =
                [
                    Busy(H(13), H(14), ShowAs.Oof), Busy(H(7), H(9), ShowAs.Busy), Busy(H(10), H(11), ShowAs.Free),
                    Busy(H(11), H(12), ShowAs.WorkingElsewhere),
                ],
            },
            new Availability { Address = "petr@example.org", Known = true, Busy = [Busy(H(10), H(10, 30), ShowAs.Tentative)] },
            new Availability { Address = "eva@example.org", Known = false },
        ],
    };

    [Fact]
    public void Rows()
    {
        var rows = Cal.AvailabilityRows(Res, new Dictionary<string, string> { ["jana@example.org"] = "Jana" }, H(8), H(16));
        Assert.Equal(4, rows.Count);
        Assert.Equal(["You", "Jana", "petr@example.org", "eva@example.org"], [rows[0].Label, rows[1].Label, rows[2].Label, rows[3].Label]);
        Assert.False(rows[3].Known);
        Assert.Equal([new Cal.AxisSpan(0, 0.125, ShowAs.Busy), new Cal.AxisSpan(0.625, 0.75, ShowAs.Oof)], rows[1].Busy);
        Assert.Equal([new Cal.AxisSpan(0.125, 0.25, ShowAs.Busy)], rows[0].Busy);
        Assert.Equal([new Cal.AxisSpan(0.25, 0.3125, ShowAs.Tentative)], rows[2].Busy);
        Assert.Empty(rows[3].Busy);
        Assert.Equal("me@example.org", rows[0].Address);
        Assert.All(Cal.AvailabilityRows(Res, null, H(10), H(10)), r => Assert.Empty(r.Busy));
        // Windows-only: a name from the recipient field is display text, and
        // a cleaned-away one falls back to the address.
        rows = Cal.AvailabilityRows(Res, new Dictionary<string, string> { ["jana@example.org"] = "\u202E\u200B" }, H(8), H(16));
        Assert.Equal("jana@example.org", rows[1].Label);
        Assert.Equal("eva@example.org", Cal.AvailabilityRows(Res, null, H(8), H(16))[3].Label);
    }

    [Theory]
    [InlineData(15, 0, 16, 0, "15:00–16:00: all known attendees are free", "Availability of 1 attendee is unknown")]
    [InlineData(10, 0, 11, 0, "1 of 2 attendees is busy", "Availability of 1 attendee is unknown")]
    [InlineData(8, 30, 10, 15, "2 of 2 attendees are busy", "Availability of 1 attendee is unknown")]
    [InlineData(11, 0, 12, 0, "11:00–12:00: all known attendees are free", "Availability of 1 attendee is unknown")]
    public void Summary(int sh, int sm, int eh, int em, params string[] want) =>
        Assert.Equal(want, Cal.Summary(Res, H(sh, sm), H(eh, em), F.Env()));

    [Fact]
    public void SummaryEdges()
    {
        Assert.Empty(Cal.Summary(new CalendarFreeBusyResult { Me = Res.Me }, H(9), H(10), F.Env()));
        Assert.Equal(
            ["Availability of 1 attendee is unknown"],
            Cal.Summary(new CalendarFreeBusyResult { Me = Res.Me, Attendees = [Res.Attendees[2]] }, H(9), H(10), F.Env()));
        Assert.Equal(
            ["Availability of 2 attendees is unknown"],
            Cal.Summary(new CalendarFreeBusyResult { Me = Res.Me, Attendees = [Res.Attendees[2], Res.Attendees[2]] }, H(9), H(10), F.Env()));
    }

    [Fact]
    public void NextFreeSlot()
    {
        var res = new CalendarFindFreeTimeResult
        {
            Slots =
            [
                new TimeSlot { Start = H(15), End = H(16) }, new TimeSlot { Start = H(9), End = H(10) },
                new TimeSlot { Start = H(12), End = H(12) }, new TimeSlot { Start = H(11), End = H(12) },
            ],
        };
        var s = Cal.NextFreeSlot(res, H(10));
        Assert.NotNull(s);
        Assert.Equal(H(11), s.Start);
        Assert.Null(Cal.NextFreeSlot(res, H(17)));
        Assert.Equal("Next free slot: Wednesday 14 October 2026 · 11:00–12:00", Cal.NextFreeSlotText(s, F.Env()));
        Assert.True(Cal.Busy(ShowAs.Unknown));
        Assert.True(Cal.Busy(ShowAs.Busy) && Cal.Busy(ShowAs.Oof) && Cal.Busy(ShowAs.Tentative));
        Assert.False(Cal.Busy(ShowAs.Free));
        Assert.False(Cal.Busy(ShowAs.WorkingElsewhere));
        Assert.Equal("Next Free Slot", Cal.NextFreeSlotButton);
        Assert.Equal("No free slot was found in the next two weeks.", Cal.NoFreeSlot);
        Assert.Equal("availability unknown", Cal.Unknown);
    }
}
