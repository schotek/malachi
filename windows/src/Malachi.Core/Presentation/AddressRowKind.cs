// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only type: the three lines of the header grid of window.blp
// (message_from, message_to, message_cc), which addresses.go and
// AddressHeaderView.swift keep as three fields or an array.

namespace Malachi.Core.Presentation;

/// <summary>A line of the address grid above a message.</summary>
public enum AddressRowKind
{
    /// <summary>The sender (window.blp <c>message_from</c>).</summary>
    From,

    /// <summary>The recipients (<c>message_to</c>).</summary>
    To,

    /// <summary>The copies (<c>message_cc</c>).</summary>
    Cc,
}
