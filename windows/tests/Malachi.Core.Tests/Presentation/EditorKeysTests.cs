// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the tests of EditorKeys, the keys the editor's bridge keeps
// (EditorBridge.Script keydown; docs/windows-port.md §11.5).

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class EditorKeysTests
{
    private const int Enter = 0x0D;
    private const int S = 0x53;
    private const int W = 0x57;
    private const int Q = 0x51;

    [Theory]
    // Formatting, with or without Shift (GTK's Ctrl-only test).
    [InlineData(EditorKeys.B, true, false, false, false, true)]
    [InlineData(EditorKeys.I, true, false, false, false, true)]
    [InlineData(EditorKeys.U, true, false, false, false, true)]
    [InlineData(EditorKeys.B, true, true, false, false, true)]
    // Ctrl+Shift+I would type a Tab: the bridge prevents it.
    [InlineData(EditorKeys.I, true, true, false, false, true)]
    [InlineData(EditorKeys.K, true, false, false, false, true)]
    [InlineData(EditorKeys.K, true, true, false, false, false)]
    [InlineData(EditorKeys.Escape, false, false, false, false, true)]
    [InlineData(EditorKeys.Escape, false, true, false, false, false)]
    [InlineData(EditorKeys.Escape, true, false, false, false, false)]
    // AltGr is Ctrl+Alt: typing. The Windows key never counts.
    [InlineData(EditorKeys.B, true, false, true, false, false)]
    [InlineData(EditorKeys.B, true, false, false, true, false)]
    // The window's keys go to the router.
    [InlineData(Enter, true, false, false, false, false)]
    [InlineData(S, true, false, false, false, false)]
    [InlineData(W, true, false, false, false, false)]
    [InlineData(Q, true, false, false, false, false)]
    // Plain letters are typing.
    [InlineData(EditorKeys.B, false, false, false, false, false)]
    public void TheBridgesKeys(int key, bool ctrl, bool shift, bool alt, bool windows, bool expected) =>
        Assert.Equal(expected, EditorKeys.BridgeHandles(key, ctrl, shift, alt, windows));
}
