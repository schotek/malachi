// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of Malachi.Core.Presentation.HeaderBarFit, the Windows addition
// that keeps a compose window's end buttons clear of the caption buttons
// (docs/windows-port.md §11.3). The measures are those of a compose window
// at 96 DPI in Czech: Attach and the app icon before the title (92), the
// Draft Menu and "Odeslat" (145), the drag strip (48) and the tall caption
// buttons (138) after it.

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class HeaderBarFitTests
{
    private const double Start = 92;
    private const double End = 145 + 48 + 138;

    [Fact]
    public void TheDefaultWidthIsWide()
    {
        Assert.False(HeaderBarFit.Narrow(760, Start, End));
    }

    [Fact]
    public void TheBarTurnsNarrowWhenTheTitleWouldGetLessThanItsLeast()
    {
        var least = Start + HeaderBarFit.MinTitleWidth + End;
        Assert.False(HeaderBarFit.Narrow(least, Start, End));
        Assert.True(HeaderBarFit.Narrow(least - 1, Start, End));
        // The window's smallest width.
        Assert.True(HeaderBarFit.Narrow(360, Start, End));
    }

    [Fact]
    public void ShorterEndButtonsLeaveTheTitleMoreRoom()
    {
        Assert.True(HeaderBarFit.Narrow(480, Start, End));
        // A shorter label than "Odeslat" (English "Send").
        Assert.False(HeaderBarFit.Narrow(480, Start, End - 60));
    }

    [Fact]
    public void AnUnmeasuredBarStaysWide()
    {
        Assert.False(HeaderBarFit.Narrow(360, 0, End));
        Assert.False(HeaderBarFit.Narrow(double.NaN, Start, End));
        Assert.False(HeaderBarFit.Narrow(360, Start, double.NaN));
    }
}
