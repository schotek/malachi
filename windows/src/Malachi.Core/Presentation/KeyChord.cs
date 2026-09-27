// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5): one keyboard shortcut, a
// virtual key with its modifiers; the counterpart of a GTK accelerator
// string ("<Control>r") and of an AppKit key equivalent.

using System.Globalization;
using System.Text;

namespace Malachi.Core.Presentation;

/// <summary>A key and the modifiers held with it.</summary>
/// <param name="Key">The virtual-key code (<see cref="VirtualKey"/>).</param>
/// <param name="Modifiers">The modifiers held with it.</param>
public readonly record struct KeyChord(int Key, KeyModifiers Modifiers = KeyModifiers.None)
{
    /// <summary><paramref name="key"/> with Ctrl.</summary>
    public static KeyChord Ctrl(int key) => new(key, KeyModifiers.Control);

    /// <summary><paramref name="key"/> with Ctrl and Shift.</summary>
    public static KeyChord CtrlShift(int key) => new(key, KeyModifiers.Control | KeyModifiers.Shift);

    /// <summary><paramref name="key"/> alone.</summary>
    public static KeyChord Bare(int key) => new(key);

    /// <summary>Whether no modifier is held (the single-key shortcuts GTK lifts while typing).</summary>
    public bool IsBare => Modifiers == KeyModifiers.None;

    /// <summary>The Windows spelling, as a tooltip shows it: <c>Ctrl+Shift+R</c>, <c>Delete</c>, <c>F5</c>.</summary>
    public override string ToString()
    {
        var text = new StringBuilder();
        if (Modifiers.HasFlag(KeyModifiers.Control))
        {
            text.Append("Ctrl+");
        }
        if (Modifiers.HasFlag(KeyModifiers.Alt))
        {
            text.Append("Alt+");
        }
        if (Modifiers.HasFlag(KeyModifiers.Shift))
        {
            text.Append("Shift+");
        }
        if (Modifiers.HasFlag(KeyModifiers.Windows))
        {
            text.Append("Win+");
        }
        text.Append(KeyName(Key));
        return text.ToString();
    }

    private static string KeyName(int key) => key switch
    {
        VirtualKey.Enter => "Enter",
        VirtualKey.Escape => "Esc",
        VirtualKey.Up => "Up",
        VirtualKey.Down => "Down",
        VirtualKey.Delete => "Delete",
        VirtualKey.Comma => ",",
        // VK_F1 to VK_F24.
        >= 0x70 and <= 0x87 => "F" + (key - 0x6F).ToString(CultureInfo.InvariantCulture),
        >= 0x30 and <= 0x39 or >= 0x41 and <= 0x5A => ((char)key).ToString(),
        _ => "0x" + key.ToString("X2", CultureInfo.InvariantCulture),
    };
}
