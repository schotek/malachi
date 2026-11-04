// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The counterpart of the NSRange that
// macos/Sources/MalachiCore/Model/SearchModel.swift (highlightRanges)
// returns: a range of UTF-16 units, which is what a C# string and WinUI's
// text ranges (TextRange.StartIndex, Length) count.

namespace Malachi.Core.Model;

/// <summary>A range of UTF-16 units in a string.</summary>
/// <param name="Start">The first unit.</param>
/// <param name="Length">How many units.</param>
public readonly record struct Utf16Range(int Start, int Length);
