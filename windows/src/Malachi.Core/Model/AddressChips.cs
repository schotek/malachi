// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AddressChips.swift
// (addressChipsFolded, addressNameChars, foldAddresses, addressKey); GTK:
// ui/internal/window/addresses.go (the same names).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Text;

namespace Malachi.Core.Model;

/// <summary>
/// The pure helpers behind the address chips above a message: which
/// addresses a From, To or Cc row shows, and a key that says whether a row
/// has to be rebuilt. Names and addresses are server data and are shown as
/// plain text.
/// </summary>
public static class AddressChips
{
    /// <summary>
    /// How many chips a row shows while folded. A fold that would hide a
    /// single address shows it instead: "+1 more" takes the room of the chip
    /// it hides (addresses.go <c>addressChipsFolded</c>).
    /// </summary>
    public const int AddressChipsFolded = 5;

    /// <summary>
    /// Caps the name on a chip, in characters; the tooltip has all of it
    /// (addresses.go <c>addressNameChars</c>).
    /// </summary>
    public const int AddressNameChars = 28;

    /// <summary>
    /// What a row shows (addresses.go <c>foldAddresses</c>): the addresses
    /// with something to display, all of them when
    /// <paramref name="expanded"/> or when at most one would fold away,
    /// otherwise the first <see cref="AddressChipsFolded"/> and how many more
    /// there are.
    /// </summary>
    public static (IReadOnlyList<Address> Shown, int More) FoldAddresses(IReadOnlyList<Address>? list, bool expanded)
    {
        var shown = (list ?? []).Where(a => Format.DisplayName(a).Length > 0).ToList();
        if (expanded || shown.Count <= AddressChipsFolded + 1)
        {
            return (shown, 0);
        }
        return (shown.GetRange(0, AddressChipsFolded), shown.Count - AddressChipsFolded);
    }

    /// <summary>
    /// Identifies what a row shows (addresses.go <c>addressKey</c>): the
    /// account the chips write from, each name and address, and the fold. A
    /// render whose key is unchanged leaves the row, and a menu open on it,
    /// alone.
    /// </summary>
    public static string AddressKey(AccountId account, IReadOnlyList<Address> shown, int more)
    {
        ArgumentNullException.ThrowIfNull(shown);
        var key = new StringBuilder(account.Value ?? "");
        foreach (var a in shown)
        {
            key.Append('\0').Append(a.Name ?? "").Append('\u0001').Append(a.Email);
        }
        key.Append('\0').Append(more.ToString(CultureInfo.InvariantCulture));
        return key.ToString();
    }
}
