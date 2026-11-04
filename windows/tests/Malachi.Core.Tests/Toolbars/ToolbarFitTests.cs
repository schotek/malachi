// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbar/fit_test.go (TestFitEverythingFits,
// TestFitLowestPriorityRightmostFirst,
// TestFitNeverDropsFixedSpacesOrSeparators, TestFitHiddenTakesNoRoom,
// TestFitPathological). Go's widths are ints, Windows measures doubles.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Toolbars;
using Xunit;
using static Malachi.Core.Tests.Toolbars.ToolbarTests;
using static Malachi.Core.Toolbars.Toolbar;

namespace Malachi.Core.Tests.Toolbars;

public sealed class ToolbarFitTests
{
    private static Shown[] ShownOf(string s) => [.. Ids(s).Select(id => new Shown(id))];

    [Fact]
    public void EverythingFits()
    {
        var got = Fit(ToolbarVariant.Gtk, Bar.Mail, ShownOf("reply replyAll forward flexibleSpace archive trash"), [30, 30, 30, 0, 30, 30], 150);
        Assert.Equal([true, true, true, true, true, true], got.Visible);
        Assert.Empty(got.Overflow);
    }

    [Fact]
    public void LowestPriorityRightmostFirst()
    {
        // moreActions is high, the others standard, junk and undo low.
        var sh = ShownOf("reply junk forward moreActions undo");
        double[] w = [30, 30, 30, 30, 30];
        var got = Fit(ToolbarVariant.Zero, Bar.Mail, sh, w, 100);
        // 150 > 100: undo (low, rightmost) goes, then junk; 90 fits.
        Assert.Equal([true, false, true, true, false], got.Visible);
        Assert.Equal([Junk, Undo], got.Overflow);
        // Narrower: of the standard ones the rightmost goes first.
        got = Fit(ToolbarVariant.Zero, Bar.Mail, sh, w, 40);
        Assert.Equal([false, false, false, true, false], got.Visible);
        Assert.Equal([Reply, Junk, Forward, Undo], got.Overflow);
    }

    [Fact]
    public void NeverDropsFixedSpacesOrSeparators()
    {
        var got = Fit(ToolbarVariant.Zero, Bar.Mail, ShownOf("mode space |list refresh flexibleSpace"), [80, 12, 1, 30, 0], 0);
        Assert.Equal([true, true, true, false, true], got.Visible);
        Assert.Equal([Refresh], got.Overflow);
    }

    [Fact]
    public void HiddenTakesNoRoom()
    {
        Shown[] sh = [new(Reply), new(Toolbar.Crm, Hidden: true), new(Star)];
        var got = Fit(ToolbarVariant.Zero, Bar.Mail, sh, [30, 500, 30], 60);
        Assert.Equal([true, false, true], got.Visible);
        Assert.Empty(got.Overflow);
    }

    [Fact]
    public void Pathological()
    {
        // Missing and negative widths count as nothing; unknown ids are
        // standard; nothing at all is fine.
        var got = Fit(ToolbarVariant.Zero, Bar.Mail, ShownOf("reply bogus forward"), [-50, 40], 30);
        Assert.Equal([true, false, true], got.Visible);
        got = Fit(ToolbarVariant.Gtk, Bar.Mail, new List<Shown>(), [], -1);
        Assert.Empty(got.Visible);
        Assert.Empty(got.Overflow);
        // An omitted item (on GTK) counts as standard if a client passes one.
        got = Fit(ToolbarVariant.Gtk, Bar.Mail, ShownOf("filter refresh"), [30, 30], 30);
        Assert.Equal([Refresh], got.Overflow);
    }
}
