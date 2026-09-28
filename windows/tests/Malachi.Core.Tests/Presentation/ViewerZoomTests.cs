// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of ViewerZoom, the CSS form of htmlview.SetZoom and
// MessageWebView.setZoom (percent / 100, 100 for zero or less).

using System.Globalization;
using Malachi.Core.Html;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class ViewerZoomTests
{
    [Theory]
    [InlineData(100, "1")]
    [InlineData(125, "1.25")]
    [InlineData(50, "0.5")]
    [InlineData(200, "2")]
    [InlineData(133, "1.33")]
    [InlineData(0, "1")]
    [InlineData(-10, "1")]
    public void TheFactorIsTheHundredth(int percent, string factor) => Assert.Equal(factor, ViewerZoom.Factor(percent));

    // Invariant whatever the user's culture (a Czech decimal comma would be
    // invalid CSS).
    [Fact]
    public void TheFactorIsInvariant()
    {
        var saved = CultureInfo.CurrentCulture;
        try
        {
            CultureInfo.CurrentCulture = new CultureInfo("cs-CZ");
            Assert.Equal("1.25", ViewerZoom.Factor(125));
            Assert.Contains("'1.25'", ViewerZoom.Script(125), System.StringComparison.Ordinal);
        }
        finally
        {
            CultureInfo.CurrentCulture = saved;
        }
    }

    // At 100 % the served document is the viewer document byte for byte;
    // otherwise its root carries the zoom, important.
    [Fact]
    public void TheDocumentCarriesTheZoom()
    {
        var document = ViewerDocument.Document("<p>x</p>");
        Assert.Same(document, ViewerZoom.Apply(document, 100));
        Assert.Same(document, ViewerZoom.Apply(document, 0));
        var zoomed = ViewerZoom.Apply(document, 150);
        Assert.Equal(document.Replace("<html>", "<html style=\"zoom: 1.5 !important\">", System.StringComparison.Ordinal), zoomed);
        Assert.Equal("no root", ViewerZoom.Apply("no root", 150));
    }

    [Fact]
    public void TheScriptReachesTheRootThroughThePrototype() =>
        Assert.Equal(
            "Object.getOwnPropertyDescriptor(Document.prototype, 'documentElement').get.call(document)"
                + ".style.setProperty('zoom', '0.75', 'important')",
            ViewerZoom.Script(75));
}
