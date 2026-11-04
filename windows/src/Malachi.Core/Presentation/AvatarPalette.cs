// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageList/AvatarView.swift
// (AvatarPalette, and the sizes AvatarView draws with), which macOS keeps in
// AppKit and Windows in Core (docs/windows-port.md §7.4); GTK: Adw.Avatar as
// message_row.blp shows it (libadwaita adw-avatar.c set_class_color and
// extract_initials_from_text, _avatar.scss $avatar_colors) and the
// monochrome-avatars rule of ui/internal/style. A sender gets the colour and
// the letters the GTK client gives it: the class is g_str_hash(name) % 14 + 1
// over the display name, the initials are its first character and the one
// after its last space.
//
// Two small differences from libadwaita, both Swift's: a name without text
// gets class 6 (the hash of "") where Adw.Avatar picks a random class, and
// upper-casing is .NET's invariant simple mapping, which leaves the few
// characters that expand (ß, the ligatures) as they are.

using System;
using System.Text;

namespace Malachi.Core.Presentation;

/// <summary>Adw.Avatar's colour classes and initials.</summary>
public static class AvatarPalette
{
    /// <summary>How many colour classes libadwaita has.</summary>
    public const int ClassCount = 14;

    /// <summary>The disc of a monochrome avatar: the text colour at this opacity (style.go).</summary>
    public const double MonochromeFillOpacity = 0.12;

    /// <summary>The initials of a monochrome avatar: the text colour at this opacity (style.go).</summary>
    public const double MonochromeTextOpacity = 0.8;

    // libadwaita _avatar.scss $avatar_colors: text, gradient top, gradient
    // bottom, for classes 1 to 14.
    private static readonly AvatarColours[] Table =
    [
        new(0xcfe1f5, 0x83b6ec, 0x337fdc),
        new(0xcaeaf2, 0x7ad9f1, 0x0f9ac8),
        new(0xcef8d8, 0x8de6b1, 0x29ae74),
        new(0xe6f9d7, 0xb5e98a, 0x6ab85b),
        new(0xf9f4e1, 0xf8e359, 0xd29d09),
        new(0xffead1, 0xffcb62, 0xd68400),
        new(0xffe5c5, 0xffa95a, 0xed5b00),
        new(0xf8d2ce, 0xf78773, 0xe62d42),
        new(0xfac7de, 0xe973ab, 0xe33b6a),
        new(0xe7c2e8, 0xcb78d4, 0x9945b5),
        new(0xd5d2f5, 0x9e91e8, 0x7a59ca),
        new(0xf2eade, 0xe3cf9c, 0xb08952),
        new(0xe5d6ca, 0xbe916d, 0x785336),
        new(0xd8d7d3, 0xc0bfbc, 0x6e6d71),
    ];

    /// <summary>
    /// GLib's <c>g_str_hash</c>: djb2 over the UTF-8 bytes read as
    /// <c>signed char</c>, up to the first NUL, wrapping at 32 bits.
    /// </summary>
    public static uint Hash(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var h = 5381u;
        foreach (var b in Encoding.UTF8.GetBytes(text))
        {
            if (b == 0)
            {
                break;
            }
            h = unchecked((h << 5) + h + (uint)(sbyte)b);
        }
        return h;
    }

    /// <summary>The colour class of a name, 1 to 14 (adw-avatar.c <c>set_class_color</c>).</summary>
    public static int ColourClass(string text) => (int)(Hash(text) % ClassCount) + 1;

    /// <summary>The colours of class <paramref name="cls"/> (clamped to 1 to 14).</summary>
    public static AvatarColours ColoursFor(int cls) => Table[Math.Clamp(cls, 1, ClassCount) - 1];

    /// <summary>The colours of <paramref name="text"/>'s class.</summary>
    public static AvatarColours ColoursOf(string text) => ColoursFor(ColourClass(text));

    /// <summary>
    /// The initials on the avatar (adw-avatar.c
    /// <c>extract_initials_from_text</c>): the text upper-cased, stripped and
    /// composed; its first character, and the character after the last space
    /// when there is one. Characters are Unicode scalars, a surrogate pair
    /// one of them. Empty for a text without any, which shows the person
    /// symbol instead.
    /// </summary>
    public static string Initials(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var normalized = text.ToUpperInvariant().Trim().Normalize(NormalizationForm.FormC);
        if (normalized.Length == 0)
        {
            return "";
        }
        var output = new StringBuilder();
        Rune.DecodeFromUtf16(normalized, out var first, out _);
        output.Append(first.ToString());
        var space = normalized.LastIndexOf(' ');
        if (space >= 0 && space + 1 < normalized.Length && normalized[space + 1] != ' ')
        {
            Rune.DecodeFromUtf16(normalized.AsSpan(space + 1), out var next, out _);
            output.Append(next.ToString());
        }
        return output.ToString();
    }

    /// <summary>The size of the initials for an avatar of <paramref name="size"/> (AvatarView: bold, 0.38 of it).</summary>
    public static double FontSize(double size) => Math.Round(size * 0.38);

    /// <summary>The size of the person symbol of an avatar without initials: half of it (adw-avatar.c).</summary>
    public static double SymbolSize(double size) => Math.Round(size / 2);
}
