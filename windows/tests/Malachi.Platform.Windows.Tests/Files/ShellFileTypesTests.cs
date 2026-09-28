// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the tests of ShellFileTypes, the previewer panel's type name
// and icon by extension (docs/windows-port.md §6.6), against this machine's
// associations: .txt has a name and an icon on every Windows, and no file
// is needed for either.

using System;
using System.Linq;
using Malachi.Core.Platform;
using Malachi.Platform.Windows.Files;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Files;

public sealed class ShellFileTypesTests
{
    [Theory]
    [InlineData(48)]
    [InlineData(256)]
    [InlineData(32)]
    public void AKnownTypeHasANameAndAnIcon(int size)
    {
        var types = new ShellFileTypes();
        Assert.False(string.IsNullOrWhiteSpace(types.TypeName(".txt")));
        var icon = types.Icon(".txt", size);
        Assert.NotNull(icon);
        Assert.True(icon.Width == icon.Height && icon.Width <= size && icon.Width >= Math.Min(size, 48), "icon of " + icon.Width);
        Assert.Equal(icon.Width * icon.Height * 4, icon.Pixels.Length);
        // Something is drawn: some pixel is not transparent.
        var pixels = icon.Pixels.ToArray();
        Assert.Contains(Enumerable.Range(0, icon.Width * icon.Height), i => pixels[(i * 4) + 3] != 0);
    }

    // An extension nothing is registered for still has the shell's generic
    // name ("ZZZ File", localised) and icon; no file is looked at.
    [Fact]
    public void AnUnknownTypeHasTheGenericOnes()
    {
        var types = new ShellFileTypes();
        var extension = ".mz" + Guid.NewGuid().ToString("N")[..8];
        var name = types.TypeName(extension);
        Assert.True(name is null || name.Contains(extension[1..], StringComparison.OrdinalIgnoreCase), name);
        Assert.NotNull(types.Icon(extension, 48));
        Assert.NotNull(types.Icon("", 48));
    }

    // A small icon the jumbo list put in the corner of its square.
    [Fact]
    public void AnIconOnlyInTheCorner()
    {
        var pixels = new byte[4 * 4 * 4];
        pixels[3] = 255;
        Assert.True(ShellFileTypes.OnlyInCorner(new FileIcon(4, 4, pixels), 2));
        pixels[(((3 * 4) + 3) * 4) + 3] = 255;
        Assert.False(ShellFileTypes.OnlyInCorner(new FileIcon(4, 4, pixels), 2));
    }
}
