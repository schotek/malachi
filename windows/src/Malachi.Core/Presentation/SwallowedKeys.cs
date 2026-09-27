// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5; spikes2/INPUT-SPIKES.md
// §5, recommendation 1): which key ups the command router swallows for a
// focused WebView2. A key down that runs a command, or is one of the
// browser's own keys, never reaches Chromium, so neither may its key up.
// The key up belongs to the press only when it comes to the window the key
// down was taken from: a command that moves the focus (Ctrl+F from the
// viewer to the search box, Ctrl+N from the editor to a new window) sends
// the key up elsewhere, where the router does not see it. A later press of
// the same key starts afresh, so that press's key up is Chromium's again
// (otherwise the first 'n' typed in the editor after Ctrl+N would reach it
// without its key up).

using System.Collections.Generic;

namespace Malachi.Core.Presentation;

/// <summary>The swallowed key downs of a window's WebView2s, awaiting their key ups. UI-thread-affine.</summary>
public sealed class SwallowedKeys
{
    private readonly Dictionary<int, nint> pressed = [];

    /// <summary>How many swallowed presses still wait for their key up.</summary>
    public int Count => pressed.Count;

    /// <summary>
    /// A key down of <paramref name="virtualKey"/> came to
    /// <paramref name="window"/> and was <paramref name="swallowed"/> or
    /// passed on; either way it replaces an earlier press of the key whose
    /// key up went elsewhere.
    /// </summary>
    public void KeyDown(int virtualKey, nint window, bool swallowed)
    {
        if (swallowed)
        {
            pressed[virtualKey] = window;
        }
        else
        {
            pressed.Remove(virtualKey);
        }
    }

    /// <summary>
    /// A key up of <paramref name="virtualKey"/> came to
    /// <paramref name="window"/>: true when it ends a swallowed press in
    /// that window, and is swallowed too.
    /// </summary>
    public bool KeyUp(int virtualKey, nint window) =>
        pressed.Remove(virtualKey, out var down) && down == window;
}
