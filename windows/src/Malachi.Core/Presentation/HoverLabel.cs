// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/WebViews/MessageWebView.swift (hover,
// statusMaxChars); GTK: ui/internal/htmlview/view.go (the status label:
// SetMaxWidthChars(80), EllipsizeMiddle, SetUseMarkup(false)).
//
// The link under the pointer, shown browser-style at the bottom left of the
// viewer. On Windows it comes from StatusBarTextChanged, which fires with the
// status bar off (measured) and carries Chromium's display form of the URL.
// It is content of the mail: plain text, capped as macOS caps it, and cut in
// the middle to GTK's 80 characters, which a WinUI TextBlock cannot do by
// itself (it trims only at the end).

using System;
using System.Globalization;

namespace Malachi.Core.Presentation;

/// <summary>The text of the viewer's link label.</summary>
public static class HoverLabel
{
    /// <summary>MessageWebView.statusMaxChars: the most characters of a link the view keeps.</summary>
    public const int MaxChars = 512;

    /// <summary>html_view: the label's maximum width in characters (SetMaxWidthChars).</summary>
    public const int DisplayChars = 80;

    private const string Ellipsis = "…";

    /// <summary>
    /// <paramref name="text"/> cut to <see cref="MaxChars"/> characters
    /// (user-perceived characters, as Swift's <c>prefix</c> counts them), so
    /// a combining sequence is never split.
    /// </summary>
    public static string Cap(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        return Prefix(text, MaxChars);
    }

    /// <summary>
    /// What the label shows for <paramref name="text"/>: capped, and when
    /// longer than <see cref="DisplayChars"/> characters, its start and end
    /// with an ellipsis between (GTK's EllipsizeMiddle); "" hides the label.
    /// </summary>
    public static string Display(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var capped = Cap(text);
        var info = new StringInfo(capped);
        var n = info.LengthInTextElements;
        if (n <= DisplayChars)
        {
            return capped;
        }
        var head = (DisplayChars - 1) / 2 + ((DisplayChars - 1) % 2);
        var tail = DisplayChars - 1 - head;
        return info.SubstringByTextElements(0, head) + Ellipsis + info.SubstringByTextElements(n - tail, tail);
    }

    private static string Prefix(string text, int count)
    {
        var index = 0;
        for (var i = 0; i < count && index < text.Length; i++)
        {
            index += StringInfo.GetNextTextElementLength(text, index);
        }
        return index >= text.Length ? text : text[..index];
    }
}
