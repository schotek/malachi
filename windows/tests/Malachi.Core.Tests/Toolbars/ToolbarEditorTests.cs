// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbaredit/toolbaredit_test.go
// (TestOpensOnTheClientsDefault, TestUnavailableRows,
// TestMoveWithinAndAcrossSections, TestSpacesGoAnywhere,
// TestAddAndAvailable, TestDrop, TestTokensKeepOtherClientsItems,
// TestReset, TestEveryBarOfEveryClient, TestBounded).

using System;
using System.Linq;
using Malachi.Core.Toolbars;
using Xunit;
using static Malachi.Core.Tests.Toolbars.ToolbarTests;
using static Malachi.Core.Toolbars.Toolbar;

namespace Malachi.Core.Tests.Toolbars;

public sealed class ToolbarEditorTests
{
    private static readonly Features All = Features.With(Feature.Crm, Feature.Assistant, Feature.Triage);

    private static string LayoutOf(ToolbarEditor e) => Join(e.Layout);

    private static int IndexOf(ToolbarEditor e, string id) => e.Layout.ToList().IndexOf(id);

    [Fact]
    public void OpensOnTheClientsDefault()
    {
        var e = new ToolbarEditor(ToolbarVariant.Gtk, Bar.Mail, null, All);
        Assert.Equal(Join(DefaultFor(ToolbarVariant.Gtk, Bar.Mail)), LayoutOf(e));
        Assert.True(e.IsDefault);
        Assert.Empty(e.Tokens);
        var secs = e.Sections();
        Assert.Equal(Sections(Bar.Mail), secs.Select(s => s.Section));
        var first = secs[0].Rows[0];
        Assert.True(first.Id == Mode && first.Fixed && !first.CanMoveUp && !first.CanMoveDown, $"mode row = {first}");
        foreach (var s in secs)
        {
            foreach (var r in s.Rows)
            {
                Assert.False(IsSeparator(r.Id), $"separator {r.Id} is a row");
                Assert.Equal(s.Section, SectionAt(Bar.Mail, e.Layout, r.Index));
            }
        }
    }

    [Fact]
    public void UnavailableRows()
    {
        var e = new ToolbarEditor(ToolbarVariant.Windows, Bar.Mail, null, Features.None);
        foreach (var r in e.Sections().SelectMany(s => s.Rows))
        {
            Assert.True(r.Unavailable == (r.Id is Crm or Assistant), $"{r.Id} unavailable = {r.Unavailable}");
        }
        Assert.DoesNotContain(Print, e.Available(Section.Message));
    }

    [Fact]
    public void MoveWithinAndAcrossSections()
    {
        var e = new ToolbarEditor(ToolbarVariant.Zero, Bar.Mail, Ids("mode |list refresh newMessage |message reply forward"), All);
        var i = IndexOf(e, NewMessage);
        Assert.True(e.MoveUp(i, out var j));
        Assert.Equal("mode |list newMessage refresh |message reply forward", LayoutOf(e));
        Assert.Equal(i - 1, j);
        // Past the heading into the sidebar section, right after the mode switch.
        Assert.True(e.MoveUp(j, out j));
        Assert.Equal("mode newMessage |list refresh |message reply forward", LayoutOf(e));
        // Not above the fixed mode switch.
        Assert.False(e.MoveUp(j, out _));
        // A list tool never into the message section.
        Assert.False(e.MoveDown(IndexOf(e, Refresh), out _));
        // A message action never up into the list section.
        i = IndexOf(e, Reply);
        Assert.False(e.MoveUp(i, out _));
        Assert.True(e.MoveDown(i, out j));
        Assert.Equal("mode newMessage |list refresh |message forward reply", LayoutOf(e));
        Assert.False(e.MoveDown(j, out _));
        // Separators and fixed items do not move; bad indexes are refused.
        foreach (var k in new[] { 0, IndexOf(e, "|list"), -1, 99 })
        {
            Assert.False(e.MoveUp(k, out _), $"MoveUp({k}) moved");
            Assert.False(e.MoveDown(k, out _), $"MoveDown({k}) moved");
        }
    }

    [Fact]
    public void SpacesGoAnywhere()
    {
        var e = new ToolbarEditor(ToolbarVariant.Zero, Bar.Mail, Ids("mode |list refresh |message reply"), All);
        Assert.True(e.AddSpace(FlexibleSpace, IndexOf(e, Refresh)));
        Assert.Equal("mode |list refresh flexibleSpace |message reply", LayoutOf(e));
        // Down across the heading into the message section and back.
        Assert.True(e.MoveDown(IndexOf(e, FlexibleSpace), out var i));
        Assert.Equal("mode |list refresh |message flexibleSpace reply", LayoutOf(e));
        Assert.True(e.MoveUp(i, out _));
        // At the start of the first section: after the mode switch.
        Assert.True(e.AddSpace(Space, -1));
        Assert.Equal("mode space |list refresh flexibleSpace |message reply", LayoutOf(e));
        // After a heading.
        Assert.True(e.AddSpace(Space, IndexOf(e, "|message")));
        Assert.Equal("mode space |list refresh flexibleSpace |message space reply", LayoutOf(e));
        Assert.False(e.AddSpace(Reply, 0) || e.AddSpace(Space, 99) || e.AddSpace(Space, -2));
        // Remove a space and an item, never the mode switch or a heading.
        Assert.True(e.Remove(IndexOf(e, Space)));
        Assert.True(e.Remove(IndexOf(e, Reply)));
        Assert.False(e.Remove(0) || e.Remove(IndexOf(e, "|list")) || e.Remove(-1) || e.Remove(99));
        Assert.Equal("mode |list refresh flexibleSpace |message space", LayoutOf(e));
    }

    [Fact]
    public void AddAndAvailable()
    {
        var e = new ToolbarEditor(ToolbarVariant.Gtk, Bar.Mail, Ids("mode |list refresh |message reply"), Features.None);
        Assert.Equal([NewMessage, Contacts, Search], e.Available(Section.List));
        foreach (var id in e.Available(Section.Message))
        {
            Assert.False(id is Reply or Crm or Print or Assistant or Filter, $"Available(message) offers {id}");
        }
        Assert.True(e.Add(Junk, Section.Message));
        Assert.Equal("mode |list refresh |message reply junk", LayoutOf(e));
        Assert.False(e.Add(Junk, Section.Message) || e.Add(Archive, Section.List)
            || e.Add(Filter, Section.List) || e.Add(Mode, Section.Sidebar) || e.Add(Reply, Section.Detail));
        Assert.True(e.Add(Search, Section.List));
        Assert.Equal("mode |list refresh search |message reply junk", LayoutOf(e));
        Assert.True(e.Add(FlexibleSpace, Section.Sidebar));
        Assert.Equal("mode flexibleSpace |list refresh search |message reply junk", LayoutOf(e));
    }

    [Fact]
    public void Drop()
    {
        var e = new ToolbarEditor(ToolbarVariant.Zero, Bar.Mail, Ids("mode |list refresh contacts |message reply forward archive"), All);
        // Archive to before reply: without archive the layout is
        // mode |list refresh contacts |message reply forward; reply is at 5.
        var from = IndexOf(e, Archive);
        Assert.True(e.CanDrop(from, 5));
        Assert.True(e.Drop(from, 5, out var i));
        Assert.Equal(5, i);
        Assert.Equal("mode |list refresh contacts |message archive reply forward", LayoutOf(e));
        // Archive into the list section is refused and nothing changes.
        var before = LayoutOf(e);
        Assert.False(e.Drop(5, 2, out i));
        Assert.Equal(5, i);
        Assert.Equal(before, LayoutOf(e));
        // Before the mode switch is refused; after it is fine.
        from = IndexOf(e, Contacts);
        Assert.False(e.CanDrop(from, 0));
        Assert.True(e.Drop(from, 1, out _));
        Assert.Equal("mode contacts |list refresh |message archive reply forward", LayoutOf(e));
        // Out of range, headings and the mode switch never drop.
        foreach (var (a, b) in new[] { (-1, 0), (99, 0), (3, 99), (3, -1), (0, 2), (2, 4) })
        {
            Assert.False(e.CanDrop(a, b), $"CanDrop({a}, {b})");
        }
    }

    [Fact]
    public void TokensKeepOtherClientsItems()
    {
        var e = new ToolbarEditor(ToolbarVariant.Gtk, Bar.Mail, Ids("mode |list toggleSidebar filter refresh |message reply search"), All);
        Assert.Equal("mode |list refresh |message reply search", LayoutOf(e));
        e.Add(Archive, Section.Message);
        var got = e.Tokens;
        Assert.Equal("mode |list toggleSidebar filter refresh |message reply search archive", Join(got));
        // The client writes them and the key comes back: nothing changes.
        e.Reload(got);
        Assert.Equal("mode |list refresh |message reply search archive", LayoutOf(e));
        Assert.Equal(Join(got), Join(e.Tokens));
        // A change from elsewhere replaces the arrangement.
        e.Reload(Ids("mode |list |message star"));
        Assert.Equal("mode |list |message star", LayoutOf(e));
    }

    [Fact]
    public void Reset()
    {
        var e = new ToolbarEditor(ToolbarVariant.Windows, Bar.Calendar, Ids("mode |main calendarToday print"), All);
        Assert.False(e.IsDefault);
        var got = e.Reset();
        Assert.NotNull(got);
        Assert.Empty(got);
        Assert.True(e.IsDefault);
        Assert.Equal(Join(DefaultFor(ToolbarVariant.Windows, Bar.Calendar)), LayoutOf(e));
        Assert.Empty(e.Tokens);
    }

    [Fact]
    public void EveryBarOfEveryClient()
    {
        foreach (var v in new[] { ToolbarVariant.Zero, ToolbarVariant.Gtk, ToolbarVariant.Windows })
        {
            foreach (var bar in Bars)
            {
                var e = new ToolbarEditor(v, bar, null, All);
                // Walk every row down and up as far as it goes: the
                // arrangement stays the client's valid one.
                for (var i = 0; i < e.Layout.Count; i++)
                {
                    var j = i;
                    for (var k = 0; k < 64 && e.MoveDown(j, out j); k++)
                    {
                    }
                    for (var k = 0; k < 64 && e.MoveUp(j, out j); k++)
                    {
                    }
                    Assert.Equal(Join(e.Layout), Join(NormalizeFor(v, bar, e.Layout)));
                }
                Assert.Equal(DefaultFor(v, bar).Count, e.Layout.Count);
                foreach (var s in Sections(bar))
                {
                    foreach (var id in e.Available(s))
                    {
                        Assert.True(e.Add(id, s), $"{bar}: Available({s}) offers {id}, Add refuses");
                    }
                }
            }
        }
    }

    [Fact]
    public void Bounded()
    {
        var e = new ToolbarEditor(ToolbarVariant.Zero, Bar.BoardFlow, null, All);
        var n = 0;
        while (e.AddSpace(Space, 0))
        {
            n++;
            Assert.True(n <= 1000, "AddSpace is unbounded");
        }
        Assert.True(e.Layout.Count <= ToolbarEditor.MaxTokens);
        Assert.Equal(Join(e.Layout), Join(NormalizeFor(ToolbarVariant.Zero, Bar.BoardFlow, e.Tokens)));
    }
}
