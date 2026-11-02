// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/AttachmentChipView.swift
// (middleEllipsis) and AddressHeaderView.swift (AddressChipView.
// tailEllipsis); GTK: Pango's EllipsizeMiddle with SetMaxWidthChars
// (attachments.go buildChip, chipNameChars) and EllipsizeEnd
// (addresses.go chip, addressNameChars). A WinUI TextBlock trims only at
// the end, and by pixels, so the chips are cut by characters here, as
// macOS cuts them: user-perceived characters (text elements), so a
// combining sequence or an emoji is never split. The name on an attachment
// tile (Shared/AttachmentTileView.swift) is cut in its middle to the tile's
// width, which the view measures (MiddleEllipsisToFit).

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
    /// <paramref name="s"/> with its middle elided just enough to be at most
    /// <paramref name="width"/> wide as <paramref name="measure"/> measures a
    /// text, the end (the extension) kept whole: the name on an attachment
    /// tile (AttachmentTileView.swift, an NSTextField truncating its middle
    /// to the tile's width). Never less than one character at each end
    /// and the ellipsis between, however narrow.
    /// </summary>
    public static string MiddleEllipsisToFit(string s, double width, Func<string, double> measure)
    {
        ArgumentNullException.ThrowIfNull(s);
        ArgumentNullException.ThrowIfNull(measure);
        if (measure(s) <= width)
        {
            return s;
        }
        // The most characters whose cut fits: the cut's width grows with max.
        int lo = 3, hi = new StringInfo(s).LengthInTextElements - 1;
        while (lo < hi)
        {
            var mid = (lo + hi + 1) / 2;
            if (measure(MiddleEllipsis(s, mid)) <= width)
            {
                lo = mid;
            }
            else
            {
                hi = mid - 1;
            }
        }
        return MiddleEllipsis(s, lo);
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
