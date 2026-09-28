// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// IconGlyphs (Core/Presentation): Icon.symbolName of
// macos/Sources/MalachiMail/App/Icons.swift with Segoe Fluent Icons glyphs:
// the -symbolic suffix, the goa-account-* family, the unknown name, and
// every GTK icon name the Blueprints and the Go UI use has a glyph.

using System.IO;
using System.Linq;
using System.Text.RegularExpressions;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.I18n;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed partial class IconGlyphsTests
{
    // Segoe Fluent Icons Refresh.
    private static readonly string Refresh = ((char)0xE72C).ToString();

    [Fact]
    public void TheSymbolicSuffixIsOptional()
    {
        Assert.Equal(Refresh, IconGlyphs.Glyph("view-refresh-symbolic"));
        Assert.Equal(Refresh, IconGlyphs.Glyph("view-refresh"));
        Assert.True(IconGlyphs.IsKnown("starred-symbolic"));
    }

    [Fact]
    public void EveryOnlineAccountsProviderHasTheEnvelope()
    {
        Assert.Equal(IconGlyphs.Glyph("mail-unread"), IconGlyphs.Glyph("goa-account-google-symbolic"));
        Assert.Equal(IconGlyphs.Glyph("goa-account"), IconGlyphs.Glyph("goa-account-msn"));
    }

    [Fact]
    public void AnUnknownNameIsAQuestionMark()
    {
        Assert.False(IconGlyphs.TryGlyph("no-such-icon", out var glyph));
        Assert.Equal(IconGlyphs.Unknown, glyph);
        Assert.Equal(IconGlyphs.Unknown, IconGlyphs.Glyph(null));
        Assert.Equal(IconGlyphs.Unknown, IconGlyphs.Glyph(""));
    }

    [Fact]
    public void EveryGlyphIsOnePrivateUseCharacter()
    {
        foreach (var (name, glyph) in IconGlyphs.Table)
        {
            Assert.True(glyph.Length == 1 && glyph[0] >= (char)0xE000 && glyph[0] <= (char)0xF8FF, name);
        }
    }

    [Fact]
    public void EveryIconNameOfTheGtkUiHasAGlyph()
    {
        var root = RepositoryPo.Root;
        var names = Directory.EnumerateFiles(Path.Combine(root, "ui"), "*.*", SearchOption.AllDirectories)
            .Where(f => f.EndsWith(".go", System.StringComparison.Ordinal) || f.EndsWith(".blp", System.StringComparison.Ordinal))
            .SelectMany(f => SymbolicName().Matches(File.ReadAllText(f)).Select(m => m.Groups[1].Value))
            .Distinct()
            .Order(System.StringComparer.Ordinal)
            .ToList();
        Assert.NotEmpty(names);
        var missing = names.Where(n => !IconGlyphs.IsKnown(n)).ToList();
        Assert.True(missing.Count == 0, "no glyph for: " + string.Join(", ", missing));
    }

    [GeneratedRegex("\"([a-z][a-z0-9-]*)-symbolic\"")]
    private static partial Regex SymbolicName();
}
