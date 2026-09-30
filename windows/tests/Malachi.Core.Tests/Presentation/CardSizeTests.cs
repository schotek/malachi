// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/htmlview/size_test.go (validHeight): the height of a
// conversation card is taken only as a finite number that is not negative,
// capped; the script reads through the prototypes and changes nothing.

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class CardSizeTests
{
    [Theory]
    [InlineData("412", 412.0)]
    [InlineData("0", 0.0)]
    [InlineData("12.5", 12.5)]
    [InlineData("1e9", CardSize.MaxReported)] // capped
    public void AHeightIsTaken(string json, double want) => Assert.Equal(want, CardSize.Parse(json));

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("null")]
    [InlineData("-3")]
    [InlineData("\"412\"")]
    [InlineData("{\"h\": 412}")]
    [InlineData("NaN")]
    [InlineData("Infinity")]
    public void AnythingElseIsDropped(string? json) => Assert.Null(CardSize.Parse(json));

    // What the script reads it reads through the prototypes (a named element
    // of the message cannot stand in for document.getElementById, the body
    // or a size), and it assigns nothing on the page.
    [Fact]
    public void TheScriptReadsThroughThePrototypesOnly()
    {
        Assert.Contains("Document.prototype.getElementById.call(document, 'malachi-column')", CardSize.Script, System.StringComparison.Ordinal);
        Assert.Contains("Element.prototype.getBoundingClientRect.call(col)", CardSize.Script, System.StringComparison.Ordinal);
        Assert.DoesNotContain("document.getElementById", CardSize.Script.Replace("Document.prototype.getElementById", "", System.StringComparison.Ordinal), System.StringComparison.Ordinal);
        Assert.DoesNotContain("document.body", CardSize.Script, System.StringComparison.Ordinal);
        Assert.DoesNotContain(".style", CardSize.Script, System.StringComparison.Ordinal);
        Assert.DoesNotContain("innerHTML", CardSize.Script, System.StringComparison.Ordinal);
    }
}
