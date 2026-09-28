// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of IconLookups: the allowance of shell icon lookups per set of
// attachment chips, which keeps a message with hundreds of different
// extensions from stalling its first render, and the bounded memory.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class IconLookupsTests
{
    private readonly List<string> asked = [];

    private IconLookups<string> Lookups(int perBatch = 3, int capacity = 8) =>
        new(ext =>
        {
            asked.Add(ext);
            return ext == ".none" ? null : "icon" + ext;
        }, perBatch, capacity);

    [Fact]
    public void ABatchLooksUpAtMostItsAllowanceAndTheRestGetTheGlyph()
    {
        var icons = Lookups(perBatch: 3);
        var batch = icons.StartBatch();
        string[] extensions = [".a", ".b", ".c", ".d", ".e"];
        var got = extensions.Select(batch.For).ToList();
        Assert.Equal(["icon.a", "icon.b", "icon.c", null, null], got);
        Assert.Equal([".a", ".b", ".c"], asked);

        // What was looked up is remembered and costs nothing; what was not
        // is looked up by a later set.
        var next = icons.StartBatch();
        Assert.Equal("icon.a", next.For(".a"));
        Assert.Equal("icon.d", next.For(".d"));
        Assert.Equal([".a", ".b", ".c", ".d"], asked);
    }

    [Fact]
    public void NoIconIsRememberedToo()
    {
        var icons = Lookups();
        Assert.Null(icons.StartBatch().For(".none"));
        Assert.Null(icons.StartBatch().For(".none"));
        Assert.Equal([".none"], asked);
    }

    [Fact]
    public void NoExtensionIsNeverLookedUp()
    {
        var icons = Lookups();
        var batch = icons.StartBatch();
        Assert.Null(batch.For(""));
        Assert.Empty(asked);
        Assert.Equal("icon.a", batch.For(".a"));
    }

    [Fact]
    public void TheMemoryStartsOverWhenFull()
    {
        var icons = Lookups(perBatch: 100, capacity: 4);
        var batch = icons.StartBatch();
        foreach (var ext in new[] { ".a", ".b", ".c", ".d" })
        {
            batch.For(ext);
        }
        Assert.Equal(4, icons.Count);
        batch.For(".e");
        Assert.Equal(1, icons.Count);
        batch.For(".a");
        Assert.Equal(6, asked.Count);
    }

    [Fact]
    public void ANoneAllowanceLooksNothingUp()
    {
        var icons = Lookups(perBatch: 0);
        Assert.Null(icons.StartBatch().For(".a"));
        Assert.Empty(asked);
    }
}
