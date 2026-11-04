// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/AddressHeaderView.swift
// (Row: its chips, its "+N more" and its key); GTK:
// ui/internal/window/addresses.go (addressRow). One line of the header
// grid as the view draws it.

using System.Collections.Generic;
using Malachi.Core.I18n;

namespace Malachi.Core.Presentation;

/// <summary>The From, To or Cc line above a message.</summary>
/// <param name="Kind">Which line it is.</param>
/// <param name="Chips">The chips it shows.</param>
/// <param name="More">How many addresses the "+N more" chip stands for; 0 without one.</param>
/// <param name="Key">What it was built from (addresses.go <c>addressKey</c>): a render with the same key changes nothing.</param>
public sealed record AddressRow(AddressRowKind Kind, IReadOnlyList<AddressChip> Chips, int More, string Key)
{
    /// <summary>A line with nobody to show is hidden, its label too.</summary>
    public bool Visible => Chips.Count > 0 || More > 0;

    /// <summary>
    /// The "+N more" chip's label (addresses.go <c>moreChip</c>); "" without
    /// one.
    /// </summary>
    public string MoreText =>
        More > 0
            // TRANSLATORS: the chip after the first few recipients of a message;
            // %d is how many more there are. Clicking it lists them all.
            ? L10n.N("+%d more", "+%d more", More)
            : "";

    /// <summary>The label of the line: From, To or Cc (window.blp <c>message_from_label</c> and friends).</summary>
    public string Label => Kind switch
    {
        AddressRowKind.From => L10n.T("From"),
        AddressRowKind.To => L10n.T("To"),
        _ => L10n.T("Cc"),
    };
}
