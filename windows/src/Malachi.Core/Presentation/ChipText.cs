// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/AttachmentChipView.swift
// (middleEllipsis) and AddressHeaderView.swift (AddressChipView.
// tailEllipsis); GTK: Pango's EllipsizeMiddle with SetMaxWidthChars
// (attachments.go buildChip, chipNameChars) and EllipsizeEnd
// (addresses.go chip, addressNameChars). A WinUI TextBlock trims only at
// the end, and by pixels, so the chips are cut by characters here, as
// macOS cuts them: user-perceived characters (text elements), so a
// combining sequence or an emoji is never split.

using System;
using System.Globalization;

namespace Malachi.Core.Presentation;

/// <summary>The labels of the reader's chips, cut to their width in characters.</summary>
public static class ChipText
{
    private const string Ellipsis = "…";

    /// <summary>
    /// <paramref name="s"/> with its middle elided to at most
    /// <paramref name="max"/> characters, the end (the extension) kept whole
    /// (AttachmentChipView.middleEllipsis).
    /// </summary>
    public static string MiddleEllipsis(string s, int max)
    {
        ArgumentNullException.ThrowIfNull(s);
        var info = new StringInfo(s);
        var n = info.LengthInTextElements;
        if (max < 2 || n <= max)
        {
            return s;
        }
        var keep = max - 1;
        var head = (keep + 1) / 2;
        var tail = keep - head;
        return info.SubstringByTextElements(0, head) + Ellipsis + info.SubstringByTextElements(n - tail, tail);
    }

    /// <summary>
    /// <paramref name="s"/> cut to at most <paramref name="max"/> characters,
    /// the end replaced by an ellipsis (AddressChipView.tailEllipsis).
    /// </summary>
    public static string TailEllipsis(string s, int max)
    {
        ArgumentNullException.ThrowIfNull(s);
        var info = new StringInfo(s);
        if (max < 1 || info.LengthInTextElements <= max)
        {
            return s;
        }
        return info.SubstringByTextElements(0, max - 1) + Ellipsis;
    }
}
