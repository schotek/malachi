// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the look-alikes of the reserved characters
// (Files/AnsiLookAlikes.cs) and what Malachi.Core.Platform.WindowsFileNames
// does with them. The code pages are scanned here with WideCharToMultiByte,
// as a program's command line is converted, so the list in Malachi.Core
// cannot go stale: a character that any ANSI code page turns into a
// reserved one is replaced, or it is one of the few that are ordinary in
// names wherever the code page is another, replaced where it is this
// machine's.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Platform;
using Malachi.Platform.Windows.Files;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Files;

public sealed class AnsiLookAlikesTests
{
    // The ANSI code pages of Windows.
    public static readonly TheoryData<int> AnsiCodePages = new()
    {
        874, 932, 936, 949, 950, 1250, 1251, 1252, 1253, 1254, 1255, 1256, 1257, 1258,
    };

    // What code pages other than 1252 turn into a reserved character, and
    // is ordinary in names wherever the code page is another (measured on
    // Windows 11 26100): ¥ (932), ¦ (932–950), ´ (1253), „ (874), ₩ (949),
    // ← → (1251, 1253), ↕ ↨ (1250, 1254), ► ◄ ♂ (1250, 1251, 1253, 1254).
    private static readonly HashSet<char> MachineOnly = ['¥', '¦', '´', '„', '₩', '←', '→', '↕', '↨', '►', '◄', '♂'];

    [Fact]
    public void NothingCodePage1252TurnsIntoAReservedCharacterSurvives()
    {
        var fits = AnsiLookAlikes.BestFits(1252);

        Assert.Equal('"', fits['＂']); // the scan sees them
        foreach (var (c, reserved) in fits)
        {
            var got = WindowsFileNames.Sanitize("a" + c + "b");
            Assert.False(got.Contains(c), $"U+{(int)c:X4} becomes '{reserved}' in code page 1252 and survives: {got}");
        }
    }

    [Fact]
    public void NothingThisMachinesCodePageTurnsIntoAReservedCharacterSurvives()
    {
        foreach (var codePage in AnsiLookAlikes.CodePagesOfThisMachine())
        {
            foreach (var (c, reserved) in AnsiLookAlikes.BestFits(codePage))
            {
                var got = WindowsFileNames.Sanitize("a" + c + "b", WindowsFileNames.MaxLength, AnsiLookAlikes.OfThisMachine);
                Assert.False(got.Contains(c), $"U+{(int)c:X4} becomes '{reserved}' in code page {codePage} and survives: {got}");
            }
        }
    }

    [Theory]
    [MemberData(nameof(AnsiCodePages))]
    public void EveryLookAlikeIsReplacedOrOrdinaryElsewhere(int codePage)
    {
        var fits = AnsiLookAlikes.BestFits(codePage);

        foreach (var (c, reserved) in fits)
        {
            var replaced = !WindowsFileNames.Sanitize("a" + c + "b").Contains(c);
            if (reserved == '"' && c != '„')
            {
                // A quotation mark is replaced everywhere.
                Assert.True(replaced, $"U+{(int)c:X4} becomes '\"' in code page {codePage} and survives");
            }
            Assert.True(
                replaced || MachineOnly.Contains(c),
                $"U+{(int)c:X4} becomes '{reserved}' in code page {codePage}: replace it in WindowsFileNames, or list it here with the reason");
            // On a machine of that code page, nothing survives.
            var own = fits.Keys.ToHashSet();
            Assert.DoesNotContain(c, WindowsFileNames.Sanitize("a" + c + "b", WindowsFileNames.MaxLength, own));
        }
    }

    [Fact]
    public void TheOrdinaryCharactersAreLookAlikesSomewhereAndKeptElsewhere()
    {
        var everywhere = AnsiCodePages.SelectMany(row => AnsiLookAlikes.BestFits(row.Data).Keys).ToHashSet();
        foreach (var c in MachineOnly)
        {
            Assert.Contains(c, everywhere);
            Assert.Contains(c, WindowsFileNames.Sanitize("a" + c + "b"));
            Assert.DoesNotContain(c, AnsiLookAlikes.BestFits(1252).Keys);
        }
    }

    [Fact]
    public void OfThisMachineIsWhatItsCodePagesTurnIntoReservedCharacters()
    {
        var pages = AnsiLookAlikes.CodePagesOfThisMachine();

        Assert.NotEmpty(pages);
        Assert.Equal(pages.Distinct().Count(), pages.Count);
        var expected = pages.SelectMany(page => AnsiLookAlikes.BestFits(page).Keys).ToHashSet();
        Assert.True(expected.SetEquals(AnsiLookAlikes.OfThisMachine));
    }

    [Theory]
    [InlineData(65001)] // UTF-8: no best fit
    [InlineData(12345)] // no such code page
    public void ACodePageWithoutBestFitHasNone(int codePage)
    {
        Assert.Empty(AnsiLookAlikes.BestFits(codePage));
    }

    [Fact]
    public void WhatIsMappedIsReservedAndWhatIsNotMappedIsNoLookAlike()
    {
        var fits = AnsiLookAlikes.BestFits(1252);

        Assert.All(fits, pair => Assert.Contains(pair.Value, AnsiLookAlikes.Reserved));
        Assert.All(fits.Keys, c => Assert.True(c > '\x7F'));
        // Code page 1252 has no 資; it becomes the default '?' and is none.
        Assert.DoesNotContain('資', fits.Keys);
    }
}
