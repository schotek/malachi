// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of HoverLabel: macOS's cap (MessageWebView.swift statusMaxChars,
// prefix by characters) and GTK's middle ellipsis at 80 characters
// (htmlview/view.go).

using System.Globalization;
using System.Linq;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class HoverLabelTests
{
    [Fact]
    public void TheCapIsMacOss()
    {
        Assert.Equal(512, HoverLabel.MaxChars);
        Assert.Equal("", HoverLabel.Cap(""));
        Assert.Equal("https://example.org/", HoverLabel.Cap("https://example.org/"));
        var long_ = new string('a', 600);
        Assert.Equal(new string('a', 512), HoverLabel.Cap(long_));
    }

    // Characters as the user sees them: a base with its combining mark and
    // a surrogate pair each count once and are never split.
    [Fact]
    public void TheCapNeverSplitsACharacter()
    {
        var accented = string.Concat(Enumerable.Repeat("é", 600));
        var capped = HoverLabel.Cap(accented);
        Assert.Equal(512, new StringInfo(capped).LengthInTextElements);
        Assert.Equal(1024, capped.Length);
        var emoji = string.Concat(Enumerable.Repeat("\U0001F600", 513));
        Assert.Equal(1024, HoverLabel.Cap(emoji).Length);
    }

    [Fact]
    public void TheLabelIsCutInTheMiddle()
    {
        Assert.Equal("", HoverLabel.Display(""));
        var eighty = new string('x', 80);
        Assert.Equal(eighty, HoverLabel.Display(eighty));
        var url = "https://" + new string('a', 60) + "/" + new string('b', 60);
        var shown = HoverLabel.Display(url);
        Assert.Equal(80, new StringInfo(shown).LengthInTextElements);
        Assert.StartsWith("https://aaaa", shown, System.StringComparison.Ordinal);
        Assert.EndsWith("bbbb", shown, System.StringComparison.Ordinal);
        Assert.Equal(40, shown.IndexOf('…', System.StringComparison.Ordinal));
    }
}
