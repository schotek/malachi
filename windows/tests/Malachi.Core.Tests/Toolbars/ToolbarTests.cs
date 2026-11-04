// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbar/toolbar_test.go (TestDefaultsAreNormal,
// TestDefaultsAreTheToolbarsBeforeCustomization,
// TestNewItemsAreOnlyInThePalette, TestEmptyIsTheDefault, TestNormalize,
// TestNormalizeIsBounded, TestCanInsert, TestImmovable, TestSectionAt,
// TestAllowedLeavesOutMissingFeatures, TestPresentHidesAndKeepsPlaces,
// TestPresentLeavesOutSeparatorsAndSections, TestMissingActions,
// TestSearchIndex, TestDisplayModeAndKeys, TestCatalogsAreConsistent).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Toolbars;
using Xunit;
using static Malachi.Core.Toolbars.Toolbar;

namespace Malachi.Core.Tests.Toolbars;

public sealed class ToolbarTests
{
    /// <summary>The test's ids: the words of <paramref name="s"/>.</summary>
    internal static string[] Ids(string s) => s.Split(' ', StringSplitOptions.RemoveEmptyEntries);

    internal static string Join(IReadOnlyList<string> l) => string.Join(' ', l);

    [Fact]
    public void DefaultsAreNormal()
    {
        foreach (var bar in Bars)
        {
            var d = DefaultLayout(bar);
            Assert.Equal(Join(d), Join(Normalize(bar, d)));
            foreach (var id in d.Where(id => !IsSpace(id) && !IsSeparator(id)))
            {
                Assert.True(Lookup(bar, id) is not null, $"{bar}: default item {id} is not in the catalog");
            }
        }
    }

    [Fact]
    public void DefaultsAreTheToolbarsBeforeCustomization()
    {
        Assert.Equal(
            "mode flexibleSpace |list toggleSidebar newMessage contacts filter refresh flexibleSpace "
            + "|message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions search",
            Join(DefaultLayout(Bar.Mail)));
        Assert.Equal("mode boardStyle contacts flexibleSpace boardAccount boardTriage", Join(DefaultLayout(Bar.BoardFlow)));
    }

    [Fact]
    public void NewItemsAreOnlyInThePalette()
    {
        var d = DefaultLayout(Bar.Mail);
        foreach (var id in new[] { MarkRead, Junk, MoveTo, Print, Undo, LoadPictures })
        {
            Assert.DoesNotContain(id, d);
            Assert.NotNull(Lookup(Bar.Mail, id));
        }
    }

    [Fact]
    public void EmptyIsTheDefault()
    {
        Assert.Equal(Join(DefaultLayout(Bar.Mail)), Join(Normalize(Bar.Mail, null)));
        Assert.Empty(Stored(Bar.Mail, DefaultLayout(Bar.Mail)));
    }

    [Theory]
    [InlineData("unknown and duplicate items go", Bar.Mail,
        "mode |list refresh bogus refresh |message reply reply junk", "mode |list refresh |message reply junk")]
    [InlineData("a missing fixed item and missing separators come back", Bar.Mail, "reply", "mode |list |message reply")]
    [InlineData("a message action in the list section goes to its home", Bar.Mail,
        "mode |list archive refresh |message reply", "mode |list refresh |message reply archive")]
    [InlineData("a list tool may stand in the sidebar section", Bar.Mail, "mode refresh |list |message", "mode refresh |list |message")]
    [InlineData("a fixed item moved elsewhere goes back to the start", Bar.Mail,
        "flexibleSpace |list newMessage mode |message", "mode flexibleSpace |list newMessage |message")]
    [InlineData("separators out of order and twice are ignored", Bar.Mail,
        "mode |message reply |list refresh |message forward", "mode |list refresh |message reply forward")]
    [InlineData("spaces stand any number of times", Bar.Mail,
        "mode |list space space |message flexibleSpace flexibleSpace", "mode |list space space |message flexibleSpace flexibleSpace")]
    [InlineData("a board detail action goes to the detail section", Bar.BoardList,
        "mode |list boardDone |detail boardReply", "mode |list |detail boardReply boardDone")]
    [InlineData("a section's separator of another toolbar is unknown", Bar.BoardFlow,
        "|list mode contacts |detail boardStyle", "mode contacts boardStyle")]
    [InlineData("print in the calendar", Bar.Calendar, "mode |main calendarToday print", "mode |main calendarToday print")]
    [InlineData("a mail item in the calendar is unknown", Bar.Calendar, "mode |main reply calendarToday", "mode |main calendarToday")]
    public void NormalizeCases(string name, Bar bar, string stored, string want)
    {
        var got = Normalize(bar, Ids(stored));
        Assert.True(Join(got) == want, $"{name}: Normalize({stored}) = {Join(got)}, want {want}");
        Assert.Equal(Join(got), Join(Normalize(bar, got)));
    }

    [Fact]
    public void NormalizeIsBounded()
    {
        var stored = Enumerable.Repeat(Space, 10 * MaxTokens).ToArray();
        Assert.True(Normalize(Bar.Mail, stored).Count <= MaxTokens + Sections(Bar.Mail).Count);
    }

    [Theory]
    [InlineData(Bar.Mail, Reply, Section.Message, true)]
    [InlineData(Bar.Mail, Reply, Section.List, false)]
    [InlineData(Bar.Mail, Refresh, Section.Sidebar, true)]
    [InlineData(Bar.Mail, Refresh, Section.Message, false)]
    [InlineData(Bar.Mail, Space, Section.Sidebar, true)]
    [InlineData(Bar.Mail, FlexibleSpace, Section.List, true)]
    [InlineData(Bar.Mail, Mode, Section.Sidebar, false)]
    [InlineData(Bar.Mail, "|list", Section.List, false)]
    [InlineData(Bar.Mail, BoardDone, Section.Message, false)]
    [InlineData(Bar.BoardList, BoardDone, Section.Detail, true)]
    [InlineData(Bar.BoardList, BoardDone, Section.List, false)]
    [InlineData(Bar.Calendar, Print, Section.Main, true)]
    public void CanInsertCases(Bar bar, string id, Section s, bool want) => Assert.Equal(want, CanInsert(bar, id, s));

    [Fact]
    public void ImmovableItems()
    {
        Assert.Equal([Mode, Separator(Section.List), Separator(Section.Message)], Immovable(Bar.Mail));
        Assert.Equal([Mode], Immovable(Bar.BoardFlow));
    }

    [Fact]
    public void SectionAtCases()
    {
        var l = DefaultLayout(Bar.Mail);
        var want = new Dictionary<string, Section>
        {
            [Mode] = Section.Sidebar,
            ["|list"] = Section.List,
            [Refresh] = Section.List,
            [Reply] = Section.Message,
            [Search] = Section.Message,
        };
        for (var i = 0; i < l.Count; i++)
        {
            if (want.TryGetValue(l[i], out var s))
            {
                Assert.Equal(s, SectionAt(Bar.Mail, l, i));
            }
        }
        Assert.Equal(Section.Main, SectionAt(Bar.BoardFlow, DefaultLayout(Bar.BoardFlow), 3));
    }

    [Fact]
    public void AllowedLeavesOutMissingFeatures()
    {
        var got = Allowed(Bar.Mail, Features.With(Feature.Assistant));
        Assert.DoesNotContain(Crm, got);
        Assert.DoesNotContain(Print, got);
        Assert.Contains(Assistant, got);
        Assert.Contains(Reply, got);
        Assert.Contains(Mode, got);
    }

    [Fact]
    public void PresentHidesAndKeepsPlaces()
    {
        var l = DefaultLayout(Bar.Mail);
        var got = Present(Bar.Mail, l, Features.With(Feature.Assistant));
        Assert.Equal(l.Count, got.Count);
        for (var i = 0; i < got.Count; i++)
        {
            Assert.Equal(l[i], got[i].Id);
            Assert.Equal(got[i].Id == Crm, got[i].Hidden);
        }
    }

    [Fact]
    public void PresentLeavesOutSeparatorsAndSections()
    {
        // The list pane collapsed: its separator goes, its items stay.
        var got = Present(Bar.Mail, Ids("mode |list refresh |message reply"), new Features { HiddenSeparators = new HashSet<string>(StringComparer.Ordinal) { "|list" } });
        Assert.Equal([new Shown(Mode), new Shown(Refresh), new Shown("|message"), new Shown(Reply)], got);
        // The Board List without a case: the detail section goes whole.
        got = Present(Bar.BoardList, Ids("mode |list boardStyle |detail boardDone boardReply"), new Features { HiddenSections = new HashSet<Section> { Section.Detail } });
        Assert.Equal([new Shown(Mode), new Shown("|list"), new Shown(BoardStyle)], got);
    }

    [Fact]
    public void MissingActionsCases()
    {
        var l = Ids("mode |list |message reply archive crm");
        // Only actions of the default toolbar: the palette's own are in the menus.
        Assert.Equal([ReplyAll, Forward, Trash, Star], MissingActions(Bar.Mail, l, Features.With(Feature.Crm)));
        // A hidden item is missing from the toolbar, but its feature is too.
        Assert.DoesNotContain(Crm, MissingActions(Bar.Mail, l, Features.None));
        Assert.Empty(MissingActions(Bar.BoardList, DefaultLayout(Bar.BoardList), Features.None));
    }

    [Fact]
    public void SearchIndexCases()
    {
        Assert.Equal(-1, SearchIndex(Bar.Mail, DefaultLayout(Bar.Mail)));
        var l = Ids("mode |list refresh |message reply");
        Assert.Equal(l.Length, SearchIndex(Bar.Mail, l));
        l = Ids("mode |main calendarToday");
        Assert.Equal(l.Length, SearchIndex(Bar.Calendar, l));
        Assert.Equal(-1, SearchIndex(Bar.BoardFlow, DefaultLayout(Bar.BoardFlow)));
    }

    [Theory]
    [InlineData("icon", DisplayMode.Icon)]
    [InlineData("icon-and-label", DisplayMode.IconAndLabel)]
    [InlineData("label", DisplayMode.Label)]
    [InlineData("", DisplayMode.Icon)]
    [InlineData("LABEL", DisplayMode.Icon)]
    public void ParseDisplayModeCases(string s, DisplayMode want) => Assert.Equal(want, ParseDisplayMode(s));

    [Fact]
    public void Keys()
    {
        Assert.Equal(["toolbar-mail", "toolbar-board-list", "toolbar-board-flow", "toolbar-calendar"], Bars.Select(Key));
        Assert.Equal("toolbar-display-mode", DisplayModeKey);
        foreach (var m in DisplayModes)
        {
            Assert.Equal(m, ParseDisplayMode(Nick(m)));
        }
    }

    [Fact]
    public void CatalogsAreConsistent()
    {
        foreach (var bar in Bars)
        {
            var seen = new HashSet<string>(StringComparer.Ordinal);
            var secs = Sections(bar).ToHashSet();
            foreach (var it in Catalog(bar))
            {
                Assert.True(seen.Add(it.Id), $"{bar}: {it.Id} twice");
                Assert.NotEmpty(it.Sections);
                Assert.All(it.Sections, s => Assert.Contains(s, secs));
                Assert.False(IsSpace(it.Id) || IsSeparator(it.Id), $"{bar}: {it.Id} is not a catalog item");
            }
        }
    }
}
