// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// SwallowedKeys (Core/Presentation): the key ups the command router takes
// from a focused WebView2 (docs/windows-port.md §11.5), which must pair
// with the key downs it took, in the same window, and never outlive a key
// up that went elsewhere.

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class SwallowedKeysTests
{
    private const int N = 0x4E;
    private const int F5 = 0x74;
    private static readonly nint Viewer = 0x1001;
    private static readonly nint Editor = 0x2002;

    [Fact]
    public void TheKeyUpOfASwallowedPressIsSwallowed()
    {
        var keys = new SwallowedKeys();
        keys.KeyDown(F5, Viewer, swallowed: true);
        Assert.True(keys.KeyUp(F5, Viewer));
        Assert.Equal(0, keys.Count);
        // Its next key up is Chromium's.
        Assert.False(keys.KeyUp(F5, Viewer));
    }

    [Fact]
    public void AKeyThatWasPassedOnKeepsItsKeyUp()
    {
        var keys = new SwallowedKeys();
        keys.KeyDown(N, Editor, swallowed: false);
        Assert.False(keys.KeyUp(N, Editor));
    }

    [Fact]
    public void AKeyUpThatWentElsewhereDoesNotTakeTheNextPresssKeyUp()
    {
        // Ctrl+N in the editor opens a new window: the key up of N goes
        // there. Back in the editor, a plain n types, down and up.
        var keys = new SwallowedKeys();
        keys.KeyDown(N, Editor, swallowed: true);
        keys.KeyDown(N, Editor, swallowed: false);
        Assert.False(keys.KeyUp(N, Editor));
        Assert.Equal(0, keys.Count);
    }

    [Fact]
    public void AKeyUpInAnotherWebViewIsNotSwallowed()
    {
        var keys = new SwallowedKeys();
        keys.KeyDown(F5, Viewer, swallowed: true);
        Assert.False(keys.KeyUp(F5, Editor));
        // And the press is over.
        Assert.False(keys.KeyUp(F5, Viewer));
    }

    [Fact]
    public void AnAutoRepeatedPressEndsWithOneKeyUp()
    {
        var keys = new SwallowedKeys();
        keys.KeyDown(F5, Viewer, swallowed: true);
        keys.KeyDown(F5, Viewer, swallowed: true);
        keys.KeyDown(F5, Viewer, swallowed: true);
        Assert.Equal(1, keys.Count);
        Assert.True(keys.KeyUp(F5, Viewer));
    }

    [Fact]
    public void KeysArePairedEachWithItsOwnKeyUp()
    {
        var keys = new SwallowedKeys();
        keys.KeyDown(F5, Viewer, swallowed: true);
        keys.KeyDown(N, Viewer, swallowed: true);
        Assert.True(keys.KeyUp(N, Viewer));
        Assert.True(keys.KeyUp(F5, Viewer));
    }
}
