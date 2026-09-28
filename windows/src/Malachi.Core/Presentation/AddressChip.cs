// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/AddressHeaderView.swift
// (AddressChipView: the title, the tooltip, the menu's name and address);
// GTK: ui/internal/window/addresses.go (chip, addressMenu). What one chip
// shows, decided here so the WinUI view only draws it: the name on the pill
// cut at the end to addressNameChars, the whole address as the tooltip, and
// the menu's heading (the name) and its address, the latter cut in the
// middle as macOS cuts it so a pathological address cannot make the menu
// wider than the screen. Names and addresses are server data: plain text,
// and on Windows cleaned for display (DisplayText, docs/security.md §4),
// the name isolated in the tooltip so that an override in it cannot turn
// the address around. Copy Address and New Message take the address as
// received.

using System;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Text;

namespace Malachi.Core.Presentation;

/// <summary>One sender or recipient above a message.</summary>
/// <param name="Address">The address the chip stands for.</param>
/// <param name="Account">The account a New Message from its menu writes from (the message's).</param>
/// <param name="Label">The name on the pill.</param>
/// <param name="Tooltip">The whole address (<see cref="Format.FormatAddress"/>), when the pill does not show all of it; null otherwise.</param>
/// <param name="MenuName">The menu's heading (the name, cleaned for display), "" without one.</param>
/// <param name="MenuAddress">The address in the menu (cleaned for display), "" without one.</param>
public sealed record AddressChip(Address Address, AccountId Account, string Label, string? Tooltip, string MenuName, string MenuAddress)
{
    /// <summary>The longest name or address a chip's menu shows (AddressChipView.showMenu).</summary>
    public const int MenuChars = 60;

    /// <summary>The bare address, trimmed: what Copy Address copies and New Message writes to.</summary>
    public string Email => Address.Email.Trim();

    /// <summary>Whether Copy Address and New Message apply (the chip has an address; addresses.go <c>SetEnabled(addr != "")</c>).</summary>
    public bool CanAct => Email.Length > 0;

    /// <summary>The chip of <paramref name="a"/> on a message of <paramref name="account"/> (addresses.go <c>chip</c>).</summary>
    public static AddressChip For(Address a, AccountId account)
    {
        ArgumentNullException.ThrowIfNull(a);
        var name = Format.DisplayName(a);
        var full = Format.FormatAddress(a);
        var label = ChipText.TailEllipsis(name, AddressChips.AddressNameChars);
        var tooltip = !string.Equals(full, name, StringComparison.Ordinal) || !string.Equals(label, name, StringComparison.Ordinal) ? full : null;
        var menuName = DisplayText.Clean(a.Name).Trim();
        return new AddressChip(
            a, account, label, tooltip,
            ChipText.TailEllipsis(menuName, MenuChars),
            ChipText.MiddleEllipsis(DisplayText.Clean(a.Email).Trim(), MenuChars));
    }
}
