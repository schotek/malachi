// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/AddressHeaderView.swift
// (show, render, fill, the "+N more" unfold); GTK:
// ui/internal/window/addresses.go (addressHeader show, render, fill,
// moreChip, foldAddresses). The From, To and Cc lines of one message view,
// minus the widgets: which chips each line shows, folded after
// addressChipsFolded until the user unfolds every line of the message, and
// which lines changed, so that a render that changes nothing leaves a line
// (and a menu open on one of its chips) alone. Another message starts
// folded again. UI-thread-affine.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <summary>The sender and recipient lines of one message view.</summary>
public sealed class AddressHeader
{
    private readonly AddressRow[] rows =
    [
        new(AddressRowKind.From, [], 0, ""),
        new(AddressRowKind.To, [], 0, ""),
        new(AddressRowKind.Cc, [], 0, ""),
    ];

    private readonly IReadOnlyList<Address>[] lists = [[], [], []];
    private MessageId? message;
    private AccountId account;
    private bool expanded;

    /// <summary>A line changed: the view rebuilds its chips from <see cref="Row"/>.</summary>
    public event EventHandler<AddressRowKind>? RowChanged;

    /// <summary>Whether the user unfolded the lines of the message on display.</summary>
    public bool Expanded => expanded;

    /// <summary>The line of <paramref name="kind"/> as it is now.</summary>
    public AddressRow Row(AddressRowKind kind) => rows[(int)kind];

    /// <summary>
    /// Renders the three lines for message <paramref name="id"/> of
    /// <paramref name="accountId"/> (addresses.go <c>show</c>); a line whose
    /// content did not change is not announced.
    /// </summary>
    public void Show(MessageId id, AccountId accountId, IReadOnlyList<Address>? from, IReadOnlyList<Address>? to, IReadOnlyList<Address>? cc)
    {
        if (message != id)
        {
            message = id;
            expanded = false;
        }
        account = accountId;
        lists[0] = from ?? [];
        lists[1] = to ?? [];
        lists[2] = cc ?? [];
        Render();
    }

    /// <summary>
    /// The "+N more" chip was used: every line of the message shows all of
    /// its addresses (addresses.go <c>moreChip</c>).
    /// </summary>
    public void Expand()
    {
        if (expanded)
        {
            return;
        }
        expanded = true;
        Render();
    }

    // addresses.go render / fill: rebuilds the lines whose content changed.
    private void Render()
    {
        for (var i = 0; i < rows.Length; i++)
        {
            var (shown, more) = AddressChips.FoldAddresses(lists[i], expanded);
            var key = AddressChips.AddressKey(account, shown, more);
            if (key == rows[i].Key)
            {
                continue;
            }
            var acc = account;
            rows[i] = new AddressRow((AddressRowKind)i, [.. shown.Select(a => AddressChip.For(a, acc))], more, key);
            RowChanged?.Invoke(this, (AddressRowKind)i);
        }
    }
}
