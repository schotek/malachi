// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of AddressHeader and AddressChip: ui/internal/window/addresses.go
// (show, render, fill, moreChip, chip, addressMenu) and macos
// AddressHeaderView.swift; the pure fold and key are AddressChipsTests'.

using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
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
        Assert.Equal("\u2068Alice\u2069 <alice@example.invalid>", named.Tooltip);
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

    [Fact]
    public void AnOverrideInTheNameCannotTurnTheAddressAround()
    {
        // Windows-only (DisplayText, docs/security.md §4): the review's From
        // name drew "<admin@evil.example>" backwards in the tooltip. The
        // texts shown are cleaned, the name isolated from the address; what
        // Copy Address copies and New Message writes to is the address as
        // received.
        var chip = AddressChip.For(new Address { Name = Text.DisplayTextTests.HostileName, Email = "admin@evil.example\u202E" }, Acc);
        Assert.Equal(ChipText.TailEllipsis(Text.DisplayTextTests.CleanedName, AddressChips.AddressNameChars), chip.Label);
        Assert.Equal("\u2068" + Text.DisplayTextTests.CleanedName + "\u2069 <admin@evil.example>", chip.Tooltip);
        Assert.Equal(Text.DisplayTextTests.CleanedName, chip.MenuName);
        Assert.Equal("admin@evil.example", chip.MenuAddress);
        Assert.Equal("admin@evil.example\u202E", chip.Email);

        // A Hebrew name reads as written, and the address stays whole after it.
        var hebrew = AddressChip.For(new Address { Name = "שלום כהן", Email = "shalom@example.org" }, Acc);
        Assert.Equal("שלום כהן", hebrew.Label);
        Assert.Equal("\u2068שלום כהן\u2069 <shalom@example.org>", hebrew.Tooltip);
        Assert.Equal("shalom@example.org", hebrew.Email);
    }
}
