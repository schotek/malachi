// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Range<String.Index> of macos/Sources/MalachiCore/Compose/
// AddressList.swift and Suggest.swift; GTK: ui/internal/compose/address.go
// (span).
//
// Swift's String.Index becomes a UTF-16 index into the C# string, the unit
// of TextBox.SelectionStart; the ends always fall on scalar boundaries, as
// Go's byte offsets fall on rune boundaries.

namespace Malachi.Core.Compose;

/// <summary>
/// compose.span: the range [<see cref="Start"/>, <see cref="End"/>) of a
/// string, in UTF-16 code units.
/// </summary>
/// <param name="Start">The first code unit of the range.</param>
/// <param name="End">The code unit after the range.</param>
public readonly record struct TextSpan(int Start, int End)
{
    /// <summary>The number of code units.</summary>
    public int Length => End - Start;

    /// <summary>Whether the range is empty.</summary>
    public bool IsEmpty => End == Start;

    /// <summary>The part of <paramref name="text"/> the range covers.</summary>
    public string Of(string text) => text[Start..End];
}
