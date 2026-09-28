// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Actions.swift (mn); GTK: the
// use-underline of GtkLabel and GtkButton, which the msgids carry
// ("_Add Account…"). macOS strips the marker; Windows keeps the letter as
// the control's access key (Alt+letter, docs/windows-port.md §9), so this
// returns both. The rule is mn's: the first single "_" before a character
// marks that character and goes, a doubled "__" is a literal underscore,
// and a later single "_" stays as it is.

using System;
using System.Text;

namespace Malachi.Core.Presentation;

/// <summary>A GTK label split into its text and its access key.</summary>
/// <param name="Label">The label without the marker.</param>
/// <param name="AccessKey">The marked character, upper-cased as Windows shows access keys; null without one.</param>
public readonly record struct Mnemonic(string Label, string? AccessKey)
{
    /// <summary>Splits <paramref name="text"/> (a translated GTK label) into its label and its access key.</summary>
    public static Mnemonic Parse(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var label = new StringBuilder(text.Length);
        string? key = null;
        for (var i = 0; i < text.Length; i++)
        {
            var c = text[i];
            if (c == '_' && i + 1 < text.Length)
            {
                if (text[i + 1] == '_')
                {
                    label.Append('_');
                    i++;
                    continue;
                }
                if (key is null)
                {
                    // A surrogate pair is one character, and no access key.
                    var next = text[i + 1];
                    key = char.IsSurrogate(next) || char.IsWhiteSpace(next) ? "" : char.ToUpperInvariant(next).ToString();
                    continue;
                }
            }
            label.Append(c);
        }
        return new Mnemonic(label.ToString(), string.IsNullOrEmpty(key) ? null : key);
    }

    /// <summary><paramref name="text"/> without its mnemonic marker (mn).</summary>
    public static string Strip(string text) => Parse(text).Label;
}
