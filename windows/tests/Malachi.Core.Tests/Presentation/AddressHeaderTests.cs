// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of AddressHeader and AddressChip: ui/internal/window/addresses.go
// (show, render, fill, moreChip, chip, addressMenu) and macos
// AddressHeaderView.swift; the pure fold and key are AddressChipsTests'.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class AddressHeaderTests
{
    private static readonly AccountId Acc = "acc";

    private static Address[] People(int n) =>
        [.. Enumerable.Range(1, n).Select(i => new Address { Name = $"Person {i}", Email = $"p{i}@example.invalid" })];

    [Fact]
    public void RowsFoldAfterFiveAndUnfoldTogether()
    {
        var h = new AddressHeader();
        var changed = new List<AddressRowKind>();
        h.RowChanged += (_, k) => changed.Add(k);

        h.Show("m1", Acc, People(1), People(8), People(7));
        Assert.Equal([AddressRowKind.From, AddressRowKind.To, AddressRowKind.Cc], changed);
        Assert.Single(h.Row(AddressRowKind.From).Chips);
        Assert.Equal(5, h.Row(AddressRowKind.To).Chips.Count);
        Assert.Equal(3, h.Row(AddressRowKind.To).More);
        Assert.Equal("+3 more", h.Row(AddressRowKind.To).MoreText);
        Assert.Equal(2, h.Row(AddressRowKind.Cc).More);

        // "+N more" unfolds every line of the message; the From line did not change.
        changed.Clear();
        h.Expand();
        Assert.True(h.Expanded);
        Assert.Equal([AddressRowKind.To, AddressRowKind.Cc], changed);
        Assert.Equal(8, h.Row(AddressRowKind.To).Chips.Count);
        Assert.Equal(0, h.Row(AddressRowKind.To).More);
        Assert.Equal("", h.Row(AddressRowKind.To).MoreText);

        // A render of the same message keeps the unfold and changes nothing.
        changed.Clear();
        h.Show("m1", Acc, People(1), People(8), People(7));
        Assert.Empty(changed);

        // Another message starts folded again.
        h.Show("m2", Acc, People(1), People(8), null);
        Assert.False(h.Expanded);
        Assert.Equal(3, h.Row(AddressRowKind.To).More);
        Assert.False(h.Row(AddressRowKind.Cc).Visible);
    }

    [Fact]
    public void AFoldThatWouldHideOneShowsItAndEmptyRowsHide()
    {
        var h = new AddressHeader();
        h.Show("m1", Acc, People(1), People(6), [new Address { Email = " " }]);
        Assert.Equal(6, h.Row(AddressRowKind.To).Chips.Count);
        Assert.Equal(0, h.Row(AddressRowKind.To).More);
        Assert.False(h.Row(AddressRowKind.Cc).Visible);
        Assert.True(h.Row(AddressRowKind.From).Visible);
    }

    [Fact]
    public void AnotherAccountRebuildsTheRow()
    {
        var h = new AddressHeader();
        var changed = new List<AddressRowKind>();
        h.Show("m1", Acc, People(1), null, null);
        h.RowChanged += (_, k) => changed.Add(k);
        h.Show("m1", "other", People(1), null, null);
        Assert.Contains(AddressRowKind.From, changed);
        Assert.Equal((AccountId)"other", h.Row(AddressRowKind.From).Chips[0].Account);
    }

    [Fact]
    public void AChipShowsTheNameAndTheWholeAddressInItsTooltip()
    {
        var named = AddressChip.For(new Address { Name = " Alice ", Email = " alice@example.invalid " }, Acc);
        Assert.Equal("Alice", named.Label);
        Assert.Equal("Alice <alice@example.invalid>", named.Tooltip);
        Assert.Equal("Alice", named.MenuName);
        Assert.Equal("alice@example.invalid", named.MenuAddress);
        Assert.Equal("alice@example.invalid", named.Email);
        Assert.True(named.CanAct);

        // A bare address is its own label: no tooltip.
        var bare = AddressChip.For(new Address { Email = "bob@example.invalid" }, Acc);
        Assert.Equal("bob@example.invalid", bare.Label);
        Assert.Null(bare.Tooltip);
        Assert.Equal("", bare.MenuName);

        // A long name is cut at 28 characters and the tooltip has all of it.
        var longName = new string('x', 40);
        var cut = AddressChip.For(new Address { Name = longName, Email = "" }, Acc);
        Assert.Equal(new string('x', 27) + "…", cut.Label);
        Assert.Equal(longName, cut.Tooltip);
        Assert.False(cut.CanAct);

        // A pathological address is cut in the menu.
        var huge = AddressChip.For(new Address { Email = new string('a', 100) + "@example.invalid" }, Acc);
        Assert.Equal(AddressChip.MenuChars, huge.MenuAddress.Length);
    }
}
