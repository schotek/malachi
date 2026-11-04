// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbar/text_test.go (TestEveryItemHasALabel,
// TestTexts, TestStripMnemonic). Go's msgid translator is L10n's English
// catalogue, which returns the msgids.

using System;
using Malachi.Core.Toolbars;
using Xunit;
using static Malachi.Core.Toolbars.Toolbar;

namespace Malachi.Core.Tests.Toolbars;

public sealed class ToolbarTextTests
{
    [Fact]
    public void EveryItemHasALabel()
    {
        foreach (var bar in Bars)
        {
            foreach (var it in Catalog(bar))
            {
                var l = ItemLabel(bar, it.Id);
                Assert.True(l.Length > 0 && l != it.Id && !l.Contains('_', StringComparison.Ordinal), $"{bar} {it.Id}: label {l}");
            }
            foreach (var s in Sections(bar))
            {
                var l = SectionTitle(bar, s);
                Assert.True(l.Length > 0 && l != Nick(s), $"{bar} {s}: section title {l}");
            }
            var t = BarTitle(bar);
            Assert.True(t.Length > 0 && t != Nick(bar), $"{bar}: bar title {t}");
        }
        Assert.NotEqual(Space, ItemLabel(Bar.Mail, Space));
        Assert.NotEqual(FlexibleSpace, ItemLabel(Bar.Mail, FlexibleSpace));
        Assert.Equal("bogus", ItemLabel(Bar.Mail, "bogus"));
    }

    [Fact]
    public void Texts()
    {
        foreach (var m in DisplayModes)
        {
            Assert.NotEmpty(DisplayModeLabel(m));
        }
        Assert.NotEqual(DisplayModeLabel(DisplayMode.Icon), DisplayModeLabel(DisplayMode.Label));
        Assert.Equal("Your changes to the “Board – List” toolbar will be lost.", RestoreBody(Bar.BoardList));
        foreach (var s in new[]
        {
            GroupTitle(), CustomizeTitle(), DialogTitle(), DisplayModeTitle(), RestoreTitle(), RestoreQuestion(),
            RestoreCancel(), AddItemTitle(), RemoveTitle(), UnavailableSubtitle(),
        })
        {
            Assert.NotEmpty(s);
        }
    }

    [Theory]
    [InlineData("Mark as _Read", "Mark as Read")]
    [InlineData("Load _Images", "Load Images")]
    [InlineData("a__b", "a_b")]
    [InlineData("_", "")]
    [InlineData("plain", "plain")]
    [InlineData("Před_vo_lby", "Předvolby")]
    public void StripMnemonicCases(string s, string want) => Assert.Equal(want, StripMnemonic(s));
}
