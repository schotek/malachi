// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/message_view_fit_test.go (fitsBody) with the
// cases of the Windows panel: the body area takes what the body needs while
// it fits, the room otherwise, the whole room for the spinner and for a
// document not measured yet.

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class BodyFitTests
{
    [Fact]
    public void ThePageSaysTheMode()
    {
        Assert.Equal(BodyFitMode.Content, BodyFit.ModeFor(ReaderBodyPage.Text, null));
        Assert.Equal(BodyFitMode.Content, BodyFit.ModeFor(ReaderBodyPage.Text, 120));
        Assert.Equal(BodyFitMode.Fill, BodyFit.ModeFor(ReaderBodyPage.Loading, 120));
        Assert.Equal(BodyFitMode.Fill, BodyFit.ModeFor(ReaderBodyPage.Html, null)); // not measured yet
        Assert.Equal(BodyFitMode.Fill, BodyFit.ModeFor(ReaderBodyPage.Html, double.NaN));
        Assert.Equal(BodyFitMode.Fill, BodyFit.ModeFor(ReaderBodyPage.Html, -1));
        Assert.Equal(BodyFitMode.Height, BodyFit.ModeFor(ReaderBodyPage.Html, 0));
        Assert.Equal(BodyFitMode.Height, BodyFit.ModeFor(ReaderBodyPage.Html, 128));
    }

    [Theory]
    [InlineData(BodyFitMode.Fill, 700, 50, 50, 700)]
    [InlineData(BodyFitMode.Content, 700, 128, 0, 128)] // a short text
    [InlineData(BodyFitMode.Content, 700, 5000, 0, 700)] // a long text scrolls
    [InlineData(BodyFitMode.Height, 700, 0, 126, 126)] // a short document
    [InlineData(BodyFitMode.Height, 700, 0, 700, 700)] // just fits
    [InlineData(BodyFitMode.Height, 700, 0, 11152, 700)] // a long document scrolls
    [InlineData(BodyFitMode.Height, -5, 0, 126, 0)]
    [InlineData(BodyFitMode.Content, double.PositiveInfinity, 128, 0, 128)]
    [InlineData(BodyFitMode.Fill, double.PositiveInfinity, 128, 0, 0)]
    [InlineData(BodyFitMode.Height, 700, 0, double.NaN, 0)]
    public void TheBodyTakesWhatItNeedsWhileItFits(BodyFitMode mode, double room, double desired, double height, double want) =>
        Assert.Equal(want, BodyFit.Height(mode, room, desired, height));
}
