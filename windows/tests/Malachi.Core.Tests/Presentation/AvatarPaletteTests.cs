// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of AvatarPalette: GLib's g_str_hash (the reference values were
// computed with the C definition, djb2 over signed chars), libadwaita's
// colour classes and initials (adw-avatar.c), and AvatarView's sizes.

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class AvatarPaletteTests
{
    [Theory]
    [InlineData("", 5381u, 6)]
    [InlineData("a", 177670u, 11)]
    [InlineData("Alice", 215236003u, 4)]
    [InlineData("Bob Smith", 860411293u, 8)]
    [InlineData("test@example.test", 2530212063u, 2)]
    // Bytes above 0x7F are negative as signed chars.
    [InlineData("Jan Novák", 1702779744u, 9)]
    [InlineData("Žluťoučký kůň", 1077579614u, 7)]
    [InlineData("\U0001F600 Emoji", 2848225888u, 5)]
    public void TheHashAndClassAreLibadwaitas(string text, uint hash, int cls)
    {
        Assert.Equal(hash, AvatarPalette.Hash(text));
        Assert.Equal(cls, AvatarPalette.ColourClass(text));
    }

    [Fact]
    public void TheHashStopsAtANul() => Assert.Equal(AvatarPalette.Hash("Ab"), AvatarPalette.Hash("Ab\0cd"));

    [Fact]
    public void TheClassesAreTheFourteenOfLibadwaita()
    {
        Assert.Equal(new AvatarColours(0xcfe1f5, 0x83b6ec, 0x337fdc), AvatarPalette.ColoursFor(1));
        Assert.Equal(new AvatarColours(0xd8d7d3, 0xc0bfbc, 0x6e6d71), AvatarPalette.ColoursFor(14));
        // Out of range is clamped, never an exception.
        Assert.Equal(AvatarPalette.ColoursFor(1), AvatarPalette.ColoursFor(0));
        Assert.Equal(AvatarPalette.ColoursFor(14), AvatarPalette.ColoursFor(99));
        Assert.Equal(AvatarPalette.ColoursFor(4), AvatarPalette.ColoursOf("Alice"));
    }

    [Theory]
    [InlineData("Alice", "A")]
    [InlineData("alice cooper", "AC")]
    [InlineData("  bob  smith  ", "BS")]
    [InlineData("Jan van der Berg", "JB")]
    [InlineData("žofie šťastná", "ŽŠ")]
    [InlineData("test@example.test", "T")]
    [InlineData("\U0001F600 emoji", "\U0001F600E")]
    [InlineData("", "")]
    [InlineData("   ", "")]
    // Composed: e and a combining acute become é.
    [InlineData("éva", "É")]
    public void TheInitialsAreTheFirstLetterAndTheOneAfterTheLastSpace(string text, string initials) =>
        Assert.Equal(initials, AvatarPalette.Initials(text));

    [Fact]
    public void TheSizesAreAvatarViews()
    {
        Assert.Equal(15, AvatarPalette.FontSize(40));
        Assert.Equal(11, AvatarPalette.FontSize(28));
        Assert.Equal(20, AvatarPalette.SymbolSize(40));
        Assert.Equal(14, AvatarPalette.SymbolSize(28));
    }
}
