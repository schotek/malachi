// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §11.5): which keys the compose editor's
// page keeps while its WebView2 has focus. No XAML key event fires then, but
// every key passes the window's pre-translate handler (measured), which
// feeds the window's command router and may swallow the key before Chromium
// sees it. The bridge (EditorBridge.Script, keydown) handles these itself:
// Ctrl+B, Ctrl+I and Ctrl+U (with or without Shift, as GTK's Ctrl-only test
// reads them) format the selection, Ctrl+K and Escape are posted to the
// window as {type: 'key'} (EditorChannel.KeyPressed), and preventing
// Ctrl+Shift+I keeps Chromium from typing its Tab. The router must let
// exactly these through to the page and may act on every other key
// (Ctrl+Enter, Ctrl+S, Ctrl+W, Ctrl+Q, …); keys it does not map reach the
// page as typing.

namespace Malachi.Core.Presentation;

/// <summary>The keys the editor's bridge handles in the page.</summary>
public static class EditorKeys
{
    /// <summary>VK_ESCAPE.</summary>
    public const int Escape = 0x1B;

    /// <summary>VK_B (bold).</summary>
    public const int B = 0x42;

    /// <summary>VK_I (italic).</summary>
    public const int I = 0x49;

    /// <summary>VK_K (insert link).</summary>
    public const int K = 0x4B;

    /// <summary>VK_U (underline).</summary>
    public const int U = 0x55;

    /// <summary>
    /// Whether the key <paramref name="virtualKey"/> with these modifiers
    /// belongs to the page (the bridge's keydown handler): the router passes
    /// it on untouched. AltGr arrives as Ctrl+Alt and is typing, as in the
    /// bridge.
    /// </summary>
    public static bool BridgeHandles(int virtualKey, bool ctrl, bool shift, bool alt, bool windows)
    {
        if (alt || windows)
        {
            return false;
        }
        if (!ctrl)
        {
            return virtualKey == Escape && !shift;
        }
        return virtualKey switch
        {
            B or I or U => true,
            K => !shift,
            _ => false,
        };
    }
}
