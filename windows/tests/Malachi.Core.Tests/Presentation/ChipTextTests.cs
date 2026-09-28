// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of ChipText: macos AttachmentChipView.middleEllipsis and
// AddressChipView.tailEllipsis (Pango's EllipsizeMiddle and EllipsizeEnd of
// attachments.go and addresses.go, by characters).

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class ChipTextTests
{
    [Theory]
    [InlineData("report.pdf", 14, "report.pdf")]
    [InlineData("faktura-2026-0917.pdf", 14, "faktura…17.pdf")]
    [InlineData("faktura.pdf.exe", 14, "faktura…df.exe")]
    [InlineData("abcdef", 5, "ab…ef")]
    [InlineData("abcdef", 1, "abcdef")]
    [InlineData("", 14, "")]
    public void MiddleEllipsisKeepsTheEnd(string name, int max, string expected) =>
        Assert.Equal(expected, ChipText.MiddleEllipsis(name, max));

    [Theory]
    [InlineData("Alice", 28, "Alice")]
    [InlineData("Příliš žluťoučký kůň úpěl ďábelské ódy", 10, "Příliš žl…")]
    [InlineData("abc", 1, "…")]
    public void TailEllipsisCutsTheEnd(string name, int max, string expected) =>
        Assert.Equal(expected, ChipText.TailEllipsis(name, max));

    [Fact]
    public void ACharacterIsNeverSplit()
    {
        // Four flags (two code units each) and a combining accent: counted as
        // what the user sees.
        var flags = "🇨🇿🇩🇪🇦🇹🇵🇱";
        Assert.Equal("🇨🇿…🇵🇱", ChipText.MiddleEllipsis(flags, 3));
        Assert.Equal("🇨🇿🇩🇪…", ChipText.TailEllipsis(flags, 3));
        var combining = "éééé";
        Assert.Equal("éé…", ChipText.TailEllipsis(combining, 3));
    }
}
