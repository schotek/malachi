// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests of Toolbar.SectionRun and SectionRuns (Toolbar.Runs.cs):
// each bar of Windows takes its section's entries of Toolbar.PresentFor.

using System.Linq;
using Malachi.Core.Toolbars;
using Xunit;
using static Malachi.Core.Tests.Toolbars.ToolbarTests;
using static Malachi.Core.Toolbars.Toolbar;

namespace Malachi.Core.Tests.Toolbars;

public sealed class ToolbarRunsTests
{
    [Fact]
    public void TheWindowsMailDefaultFallsIntoItsThreeBars()
    {
        var shown = PresentFor(ToolbarVariant.Windows, Bar.Mail, DefaultFor(ToolbarVariant.Windows, Bar.Mail), Features.None);
        Assert.Equal("mode newMessage", Join([.. shown.SectionRun(Bar.Mail, Section.Sidebar).Select(e => e.Id)]));
        Assert.Equal("refresh filter", Join([.. shown.SectionRun(Bar.Mail, Section.List).Select(e => e.Id)]));
        Assert.Equal(
            "reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions search",
            Join([.. shown.SectionRun(Bar.Mail, Section.Message).Select(e => e.Id)]));
    }

    [Fact]
    public void ABarOfOneSectionIsOneRun()
    {
        var shown = PresentFor(ToolbarVariant.Windows, Bar.BoardFlow, DefaultFor(ToolbarVariant.Windows, Bar.BoardFlow), Features.None);
        Assert.Equal("mode boardStyle flexibleSpace boardAccount boardTriage", Join([.. shown.SectionRun(Bar.BoardFlow, Section.Main).Select(e => e.Id)]));
        Assert.Empty(shown.SectionRun(Bar.BoardFlow, Section.Detail));
    }

    [Fact]
    public void RunsJoinSectionsAndAHiddenSectionHasNone()
    {
        var l = DefaultFor(ToolbarVariant.Windows, Bar.BoardList);
        var shown = PresentFor(ToolbarVariant.Windows, Bar.BoardList, l, Features.None);
        Assert.Equal(
            "mode boardStyle flexibleSpace boardAccount boardTriage",
            Join([.. shown.SectionRuns(Bar.BoardList, Section.Sidebar, Section.List).Select(e => e.Id)]));
        var hidden = PresentFor(ToolbarVariant.Windows, Bar.BoardList, l, new Features { HiddenSections = new System.Collections.Generic.HashSet<Section> { Section.Detail } });
        Assert.Empty(hidden.SectionRun(Bar.BoardList, Section.Detail));
        // The calendar's main section holds what its one bar draws.
        var cal = PresentFor(ToolbarVariant.Windows, Bar.Calendar, DefaultFor(ToolbarVariant.Windows, Bar.Calendar), Features.None);
        Assert.Equal(
            "mode toggleSidebar calendarToday calendarStep calendarView flexibleSpace calendarSearch calendarNewEvent calendarAssistant",
            Join([.. cal.SectionRuns(Bar.Calendar, Section.Sidebar, Section.Main).Select(e => e.Id)]));
    }
}
