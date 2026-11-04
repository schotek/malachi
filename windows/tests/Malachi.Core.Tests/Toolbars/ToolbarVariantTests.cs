// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbar/variant_test.go (TestClientDefaultsAreNormal,
// TestZeroVariantIsTheCatalog, TestGTKDefaultsAreTodaysHeaderBars,
// TestWindowsDefaultsAreTodaysBars, TestGTKOmitsAndExtras,
// TestPresentForLeavesOutOmitted, TestMissingActionsFor,
// TestStoredForKeepsWhatTheClientLacks,
// TestStoredForRoundTripsBetweenClients, TestStoredForOnEveryBar,
// TestStoredForPathological).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Toolbars;
using Xunit;
using static Malachi.Core.Tests.Toolbars.ToolbarTests;
using static Malachi.Core.Toolbars.Toolbar;

namespace Malachi.Core.Tests.Toolbars;

public sealed class ToolbarVariantTests
{
    private static readonly (string Name, ToolbarVariant Variant)[] Clients =
    [
        ("macOS", ToolbarVariant.Zero),
        ("GTK", ToolbarVariant.Gtk),
        ("Windows", ToolbarVariant.Windows),
    ];

    [Fact]
    public void ClientDefaultsAreNormal()
    {
        foreach (var (name, v) in Clients)
        {
            foreach (var bar in Bars)
            {
                var d = DefaultFor(v, bar);
                Assert.True(Join(NormalizeFor(v, bar, d)) == Join(d), $"{name} {bar}: NormalizeFor(DefaultFor)");
                Assert.True(Join(NormalizeFor(v, bar, null)) == Join(d), $"{name} {bar}: NormalizeFor(null)");
                foreach (var id in d.Where(id => !IsSpace(id) && !IsSeparator(id)))
                {
                    Assert.True(LookupFor(v, bar, id) is not null, $"{name} {bar}: default item {id} is not in the client's catalog");
                }
                Assert.Empty(StoredFor(v, bar, null, d));
            }
        }
    }

    [Fact]
    public void ZeroVariantIsTheCatalog()
    {
        foreach (var bar in Bars)
        {
            Assert.Equal(Join(DefaultLayout(bar)), Join(DefaultFor(ToolbarVariant.Zero, bar)));
            Assert.Equal(Describe(Catalog(bar)), Describe(CatalogFor(ToolbarVariant.Zero, bar)));
        }
        // StoredFor with the zero variant is Stored, whatever was stored.
        foreach (var l in new IReadOnlyList<string>[] { DefaultLayout(Bar.Mail), Ids("mode |list refresh |message reply junk"), [] })
        {
            foreach (var stored in new IReadOnlyList<string>?[] { null, Ids("mode |list filter |message star") })
            {
                Assert.Equal(Join(Stored(Bar.Mail, l)), Join(StoredFor(ToolbarVariant.Zero, Bar.Mail, stored, l)));
            }
        }
    }

    [Fact]
    public void GtkDefaultsAreTodaysHeaderBars()
    {
        var want = new Dictionary<Bar, string>
        {
            [Bar.Mail] = "mode newMessage flexibleSpace |list refresh flexibleSpace search "
                + "|message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions",
            [Bar.BoardList] = "mode boardStyle flexibleSpace |list boardTriage boardAccount "
                + "|detail boardDone boardRemind boardArchive boardTrash flexibleSpace boardReply",
            [Bar.BoardFlow] = "mode boardStyle flexibleSpace boardTriage boardAccount",
            [Bar.Calendar] = "mode |main toggleSidebar calendarToday calendarStep calendarView flexibleSpace "
                + "calendarSearch calendarNewEvent calendarAssistant",
        };
        foreach (var (bar, w) in want)
        {
            Assert.Equal(w, Join(DefaultFor(ToolbarVariant.Gtk, bar)));
        }
    }

    [Fact]
    public void WindowsDefaultsAreTodaysBars()
    {
        var want = new Dictionary<Bar, string>
        {
            [Bar.Mail] = "mode newMessage |list refresh filter "
                + "|message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions search",
            [Bar.BoardList] = "mode boardStyle flexibleSpace |list boardAccount boardTriage "
                + "|detail boardDone boardRemind boardArchive boardTrash flexibleSpace boardReply",
            [Bar.BoardFlow] = "mode boardStyle flexibleSpace boardAccount boardTriage",
            [Bar.Calendar] = "mode |main toggleSidebar calendarToday calendarStep calendarView flexibleSpace "
                + "calendarSearch calendarNewEvent calendarAssistant",
        };
        foreach (var (bar, w) in want)
        {
            Assert.Equal(w, Join(DefaultFor(ToolbarVariant.Windows, bar)));
        }
    }

    [Fact]
    public void GtkOmitsAndExtras()
    {
        foreach (var (bar, id) in new[] { (Bar.Mail, Filter), (Bar.Mail, ToggleSidebar), (Bar.BoardList, ToggleSidebar) })
        {
            Assert.Null(LookupFor(ToolbarVariant.Gtk, bar, id));
            Assert.False(CanInsertFor(ToolbarVariant.Gtk, bar, id, Section.List));
            Assert.DoesNotContain(id, AllowedFor(ToolbarVariant.Gtk, bar, Features.None));
        }
        Assert.NotNull(LookupFor(ToolbarVariant.Gtk, Bar.Calendar, ToggleSidebar));
        Assert.True(CanInsertFor(ToolbarVariant.Gtk, Bar.Mail, Search, Section.List));
        Assert.True(CanInsertFor(ToolbarVariant.Gtk, Bar.Mail, Search, Section.Message));
        Assert.False(CanInsert(Bar.Mail, Search, Section.List));
        Assert.Equal(Section.Message, LookupFor(ToolbarVariant.Gtk, Bar.Mail, Search)!.Home);
        // macOS's arrangement read on GTK: its filter and sidebar toggle go.
        Assert.Equal(
            "mode flexibleSpace |list newMessage contacts refresh flexibleSpace "
            + "|message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions search",
            Join(NormalizeFor(ToolbarVariant.Gtk, Bar.Mail, DefaultLayout(Bar.Mail))));
        // And GTK's read on macOS: the search goes home.
        Assert.Equal(Search, Normalize(Bar.Mail, DefaultFor(ToolbarVariant.Gtk, Bar.Mail))[^1]);
    }

    [Fact]
    public void PresentForLeavesOutOmitted()
    {
        var l = Ids("mode |list filter toggleSidebar refresh |message reply crm");
        Assert.Equal(
            [new Shown(Mode), new Shown("|list"), new Shown(Refresh), new Shown("|message"), new Shown(Reply), new Shown(Toolbar.Crm, Hidden: true)],
            PresentFor(ToolbarVariant.Gtk, Bar.Mail, l, Features.None));
        Assert.Equal(l.Length, PresentFor(ToolbarVariant.Windows, Bar.Mail, l, Features.None).Count);
    }

    [Fact]
    public void MissingActionsForCases()
    {
        Assert.Empty(MissingActionsFor(ToolbarVariant.Gtk, Bar.Mail, DefaultFor(ToolbarVariant.Gtk, Bar.Mail), Features.With(Feature.Crm)));
        Assert.Equal(
            [ReplyAll, Forward, Archive, Trash, Star],
            MissingActionsFor(ToolbarVariant.Windows, Bar.Mail, Ids("mode |list |message reply"), Features.None));
    }

    [Theory]
    [InlineData("a first edit on GTK keeps macOS's filter and sidebar toggle where the default had them",
        "",
        "mode newMessage flexibleSpace |list refresh flexibleSpace search |message reply archive",
        "mode newMessage flexibleSpace |list toggleSidebar filter refresh flexibleSpace search |message reply archive")]
    [InlineData("the filter follows its neighbour",
        "mode |list refresh filter contacts |message reply",
        "mode |list contacts refresh |message reply",
        "mode |list contacts refresh filter |message reply")]
    [InlineData("an anchor moved to another section: the nearest one before it in the section",
        "mode |list refresh filter |message reply",
        "mode refresh |list |message reply",
        "mode refresh |list filter |message reply")]
    [InlineData("an anchor the user removed: the nearest one before it",
        "mode newMessage filter |list refresh |message reply",
        "mode |list refresh |message reply",
        "mode filter |list refresh |message reply")]
    [InlineData("two omitted items keep their order",
        "mode |list refresh toggleSidebar filter |message",
        "mode |list refresh |message reply",
        "mode |list refresh toggleSidebar filter |message reply")]
    [InlineData("items the stored list lacks are not added",
        "mode |list refresh |message reply",
        "mode |list refresh |message forward",
        "mode |list refresh |message forward")]
    [InlineData("GTK's default over a stored list without the omitted items is not empty",
        "mode |list refresh |message reply",
        "mode newMessage flexibleSpace |list refresh flexibleSpace search |message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions",
        "mode newMessage flexibleSpace |list refresh flexibleSpace search |message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions")]
    [InlineData("GTK's default over a stored list with the default's omitted items is empty",
        "mode filter |list toggleSidebar |message reply",
        "mode newMessage flexibleSpace |list refresh flexibleSpace search |message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions",
        "")]
    [InlineData("unknown and duplicate stored tokens go",
        "mode |list filter bogus filter |message",
        "mode |list refresh |message",
        "mode |list filter refresh |message")]
    public void StoredForKeepsWhatTheClientLacks(string name, string stored, string edited, string want)
    {
        var got = StoredFor(ToolbarVariant.Gtk, Bar.Mail, Ids(stored), Ids(edited));
        Assert.True(Join(got) == want, $"{name}:\n got {Join(got)}\nwant {want}");
        // GTK reads back what it edited.
        Assert.Equal(Join(NormalizeFor(ToolbarVariant.Gtk, Bar.Mail, Ids(edited))), Join(NormalizeFor(ToolbarVariant.Gtk, Bar.Mail, got)));
        // The client's own save again changes nothing.
        Assert.Equal(Join(got), Join(StoredFor(ToolbarVariant.Gtk, Bar.Mail, got, Ids(edited))));
    }

    [Fact]
    public void StoredForRoundTripsBetweenClients()
    {
        // macOS arranges, GTK edits, macOS reads: its filter and sidebar
        // toggle are where they were; GTK's search in the list section goes
        // to its home on macOS.
        var mac = Ids("mode |list toggleSidebar filter refresh |message reply archive search");
        var stored = Stored(Bar.Mail, mac);
        Assert.Equal("mode |list refresh |message reply archive search", Join(NormalizeFor(ToolbarVariant.Gtk, Bar.Mail, stored)));
        var edited = Ids("mode |list refresh search |message archive reply");
        stored = StoredFor(ToolbarVariant.Gtk, Bar.Mail, stored, edited);
        Assert.Equal("mode |list toggleSidebar filter refresh search |message archive reply", Join(stored));
        Assert.Equal("mode |list toggleSidebar filter refresh |message archive reply search", Join(Normalize(Bar.Mail, stored)));
        // Windows keeps the search where GTK put it (its extra section), and
        // a Windows save keeps the omitted items as they are (it omits none).
        Assert.Equal(Join(stored), Join(NormalizeFor(ToolbarVariant.Windows, Bar.Mail, stored)));
    }

    [Fact]
    public void StoredForOnEveryBar()
    {
        // A GTK edit of the Board List keeps a sidebar toggle stored there.
        var got = StoredFor(ToolbarVariant.Gtk, Bar.BoardList, Ids("mode toggleSidebar |list boardStyle |detail boardDone"), Ids("mode |list boardStyle boardAccount |detail boardDone"));
        Assert.Equal("mode toggleSidebar |list boardStyle boardAccount |detail boardDone", Join(got));
        // Bars without omitted items store the edit as it is.
        foreach (var bar in new[] { Bar.BoardFlow, Bar.Calendar })
        {
            var l = bar == Bar.Calendar ? Ids("mode |main calendarToday") : Ids("mode");
            Assert.Equal(Join(l), Join(StoredFor(ToolbarVariant.Gtk, bar, null, l)));
        }
    }

    [Fact]
    public void StoredForPathological()
    {
        // Junk stored: separators only, a token too long, a thousand spaces.
        var junk = new List<string> { "|message", "|list", "|bogus", "", "|", new string('\0', 5000) };
        for (var i = 0; i < 1000; i++)
        {
            junk.Add(Filter);
            junk.Add(Space);
        }
        var got = StoredFor(ToolbarVariant.Gtk, Bar.Mail, junk, DefaultFor(ToolbarVariant.Gtk, Bar.Mail));
        Assert.Equal(Join(DefaultFor(ToolbarVariant.Gtk, Bar.Mail)), Join(NormalizeFor(ToolbarVariant.Gtk, Bar.Mail, got)));
        Assert.True(got.Count(t => t == Filter) <= 1, "filter merged more than once");
        // An edited layout with omitted and unknown items: they are not the
        // client's to store.
        got = StoredFor(ToolbarVariant.Gtk, Bar.Mail, null, Ids("mode filter bogus |list refresh |message"));
        Assert.Equal("mode |list toggleSidebar filter refresh |message", Join(got));
    }

    private static string Describe(IReadOnlyList<Item> items) =>
        string.Join(';', items.Select(it => $"{it.Id}:{string.Join(',', it.Sections)}:{it.Fixed}:{it.Feature}:{it.InMore}:{it.Priority}"));
}
