// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of PaneLayout: window.blp's breakpoints and pane ranges, the
// widths a window too narrow for the stored ones gets, and the navigation
// of the folded layouts (window.go's SetShowContent calls, search.go
// startSearch).

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class PaneLayoutTests
{
    [Theory]
    [InlineData(1200, PaneMode.Wide)]
    [InlineData(901, PaneMode.Wide)]
    // max-width: 900sp includes 900.
    [InlineData(900, PaneMode.Medium)]
    [InlineData(601, PaneMode.Medium)]
    [InlineData(600, PaneMode.Narrow)]
    [InlineData(360, PaneMode.Narrow)]
    public void TheBreakpointsAreWindowBlps(double width, PaneMode mode) => Assert.Equal(mode, PaneLayout.ModeFor(width));

    [Theory]
    [InlineData(0, 200)]
    [InlineData(240, 240)]
    [InlineData(999, 320)]
    public void TheSidebarKeepsItsRange(int stored, int width) => Assert.Equal(width, PaneLayout.ClampSidebar(stored));

    [Theory]
    [InlineData(0, 280)]
    [InlineData(380, 380)]
    [InlineData(999, 460)]
    public void TheListKeepsItsRange(int stored, int width) => Assert.Equal(width, PaneLayout.ClampList(stored));

    // window.blp assistant_split and its breakpoint: inline wider than
    // 1180, the fraction of the width within 280 to 480, never wider than
    // the window.
    [Theory]
    [InlineData(1180, false)]
    [InlineData(1181, true)]
    [InlineData(900, false)]
    [InlineData(2560, true)]
    public void TheAssistantPanelIsAPaneOnlyInAWideWindow(double width, bool inline) =>
        Assert.Equal(inline, PaneLayout.AssistantInline(width));

    [Theory]
    [InlineData(1500, 420)]
    [InlineData(1000, 280)]
    [InlineData(2560, 480)]
    [InlineData(200, 200)]
    [InlineData(0, 0)]
    public void TheAssistantPanelTakesItsShareOfTheWidth(double width, double panel) =>
        Assert.Equal(panel, PaneLayout.AssistantWidth(width), 3);

    [Fact]
    public void AWideWindowGetsTheStoredWidths() =>
        Assert.Equal(new PaneWidths(240, 380), PaneLayout.Widths(PaneMode.Wide, 1200, 240, 380));

    [Fact]
    public void ANarrowerWideWindowTakesFromTheListFirst()
    {
        // 901 - 300 - 320 = 281 for the list.
        Assert.Equal(new PaneWidths(320, 281), PaneLayout.Widths(PaneMode.Wide, 901, 320, 460));
        // Never below the list's minimum.
        Assert.Equal(new PaneWidths(320, 280), PaneLayout.Widths(PaneMode.Wide, 850, 320, 460));
    }

    [Fact]
    public void TheFoldedLayoutsGiveTheOverlayTheSidebarsWidth()
    {
        Assert.Equal(new PaneWidths(240, 380), PaneLayout.Widths(PaneMode.Medium, 800, 240, 380));
        Assert.Equal(new PaneWidths(240, 350), PaneLayout.Widths(PaneMode.Medium, 650, 240, 460));
        Assert.Equal(new PaneWidths(240, 500), PaneLayout.Widths(PaneMode.Narrow, 500, 240, 380));
    }

    [Fact]
    public void TheWideLayoutShowsEverything()
    {
        var l = new PaneLayout();
        Assert.False(l.Resize(1200));
        Assert.True(l.SidebarInline);
        Assert.False(l.SidebarOverlay);
        Assert.True(l.ListVisible);
        Assert.True(l.MessageVisible);
        Assert.False(l.BackVisible);
        Assert.False(l.PaneToggleVisible);
        Assert.True(l.KeepsWidths);
        // The pane button does nothing there.
        l.ToggleSidebar();
        Assert.False(l.SidebarOverlay);
    }

    [Fact]
    public void TheMediumLayoutFoldsTheSidebarIntoAnOverlay()
    {
        var l = new PaneLayout();
        Assert.True(l.Resize(800));
        Assert.Equal(PaneMode.Medium, l.Mode);
        Assert.False(l.SidebarInline);
        Assert.True(l.PaneToggleVisible);
        Assert.False(l.KeepsWidths);
        Assert.True(l.ListVisible);
        Assert.True(l.MessageVisible);
        l.ToggleSidebar();
        Assert.True(l.SidebarOverlay);
        // A folder chosen closes it.
        l.FolderChosen();
        Assert.False(l.SidebarOverlay);
        l.ToggleSidebar();
        l.CloseSidebar();
        Assert.False(l.SidebarOverlay);
    }

    [Fact]
    public void TheNarrowLayoutStacksListAndMessage()
    {
        var l = new PaneLayout();
        l.Resize(500);
        Assert.Equal(PaneMode.Narrow, l.Mode);
        Assert.True(l.ListVisible);
        Assert.False(l.MessageVisible);
        Assert.False(l.BackVisible);
        l.MessageChosen();
        Assert.False(l.ListVisible);
        Assert.True(l.MessageVisible);
        Assert.True(l.BackVisible);
        l.ShowList();
        Assert.True(l.ListVisible);
        Assert.False(l.BackVisible);
        // A folder chosen from the overlay shows its list, not an emptied message.
        l.MessageChosen();
        l.ToggleSidebar();
        l.FolderChosen();
        Assert.False(l.SidebarOverlay);
        Assert.True(l.ListVisible);
    }

    [Fact]
    public void TheStackKeepsItsPageAcrossWidths()
    {
        var l = new PaneLayout();
        l.Resize(500);
        l.MessageChosen();
        // Wider: both show; narrower again: the message, as show-content stays.
        l.Resize(700);
        Assert.True(l.ListVisible);
        Assert.True(l.MessageVisible);
        Assert.False(l.BackVisible);
        l.Resize(500);
        Assert.True(l.BackVisible);
    }

    [Fact]
    public void GoingWideClosesTheOverlay()
    {
        var l = new PaneLayout();
        l.Resize(800);
        l.ToggleSidebar();
        Assert.True(l.SidebarOpen);
        Assert.True(l.Resize(1000));
        Assert.False(l.SidebarOpen);
        Assert.True(l.SidebarInline);
    }
}
