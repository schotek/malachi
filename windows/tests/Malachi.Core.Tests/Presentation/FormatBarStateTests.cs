// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of Malachi.Core.Presentation.FormatBarState, the formatting bar of
// ui/internal/compose/compose.go (applyState, wireActions' block and align
// actions, wireToolbar) and FormatToolbar.swift, which neither side tests.

using Malachi.Core.Html;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class FormatBarStateTests
{
    [Fact]
    public void TheTogglesFollowTheCaret()
    {
        var s = FormatBarState.From(new EditorState { Bold = true, Underline = true, Ol = true, Block = "blockquote", Align = "right" });
        Assert.True(s.Bold);
        Assert.False(s.Italic);
        Assert.True(s.Underline);
        Assert.False(s.BulletedList);
        Assert.True(s.NumberedList);
        Assert.True(s.Quote);
        // A quote is no paragraph style of the menu: the button says Paragraph.
        Assert.Equal("p", s.Block);
        Assert.Equal("Paragraph", s.BlockLabel);
        Assert.Equal("right", s.Align);
        Assert.Equal("format-justify-right-symbolic", s.AlignIcon);
    }

    [Theory]
    [InlineData("h1", "h1", "Heading 1")]
    [InlineData("h2", "h2", "Heading 2")]
    [InlineData("h3", "h3", "Heading 3")]
    [InlineData("h4", "p", "Paragraph")]
    [InlineData("p", "p", "Paragraph")]
    [InlineData("div", "p", "Paragraph")]
    [InlineData("", "p", "Paragraph")]
    public void TheParagraphStyleIsAHeadingOrAParagraph(string reported, string block, string label)
    {
        var s = FormatBarState.From(new EditorState { Block = reported });
        Assert.Equal(block, s.Block);
        Assert.Equal(label, s.BlockLabel);
        Assert.False(s.Quote);
    }

    [Theory]
    [InlineData("center", "center")]
    [InlineData("right", "right")]
    [InlineData("left", "left")]
    [InlineData("justify", "left")]
    [InlineData("", "left")]
    public void TheAlignmentIsLeftCentreOrRight(string reported, string align)
    {
        var s = FormatBarState.From(new EditorState { Align = reported });
        Assert.Equal(align, s.Align);
        Assert.Equal("format-justify-" + align + "-symbolic", s.AlignIcon);
    }

    [Fact]
    public void TheInitialBarIsALeftAlignedParagraph()
    {
        Assert.Equal(FormatBarState.From(new EditorState()), FormatBarState.Initial);
        Assert.Equal("p", FormatBarState.Initial.Block);
        Assert.Equal("left", FormatBarState.Initial.Align);
        Assert.False(FormatBarState.Initial.Bold);
    }

    [Fact]
    public void TheMenusAreGtks()
    {
        Assert.Equal(["p", "h1", "h2", "h3"], FormatBarState.Blocks);
        Assert.Equal(["left", "center", "right"], FormatBarState.Aligns);
        Assert.Equal(["Paragraph", "Heading 1", "Heading 2", "Heading 3"], [.. System.Linq.Enumerable.Select(FormatBarState.Blocks, FormatBarState.BlockTitle)]);
        Assert.Equal(["Left", "Center", "Right"], [.. System.Linq.Enumerable.Select(FormatBarState.Aligns, FormatBarState.AlignTitle)]);
    }

    [Fact]
    public void TheCommandsAreTheDomsEditingCommands()
    {
        Assert.Equal(("formatBlock", "h2"), FormatBarState.BlockCommand("h2"));
        Assert.Equal(("formatBlock", "p"), FormatBarState.BlockCommand("blockquote"));
        Assert.Equal(("justifyCenter", (string?)null), FormatBarState.AlignCommand("center"));
        Assert.Equal(("justifyRight", (string?)null), FormatBarState.AlignCommand("right"));
        Assert.Equal(("justifyLeft", (string?)null), FormatBarState.AlignCommand("anything"));
        Assert.Equal(("formatBlock", "blockquote"), FormatBarState.QuoteCommand(on: true));
        Assert.Equal(("outdent", (string?)null), FormatBarState.QuoteCommand(on: false));
        Assert.Equal([("removeFormat", (string?)null), ("unlink", null)], FormatBarState.ClearCommands);
    }

    [Theory]
    [InlineData(0, 0, 0, "#000000")]
    [InlineData(255, 255, 255, "#ffffff")]
    [InlineData(28, 113, 216, "#1c71d8")]
    public void TheColourIsHex(byte r, byte g, byte b, string css)
    {
        Assert.Equal(css, FormatBarState.CssColor(r, g, b));
        Assert.Equal("#000000", FormatBarState.InitialColor);
    }
}
