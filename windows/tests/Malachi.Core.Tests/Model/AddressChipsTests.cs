// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AddressChipsTests.swift, the
// counterpart of ui/internal/window/addresses_test.go (TestFoldAddresses,
// TestFoldAddressesSkipsBlankEntries, TestAddressKey): which addresses a
// header row shows, and the key that decides whether it is rebuilt.

using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class AddressChipsTests
{
    [Theory]
    [InlineData("none", 0, false, 0, 0)]
    [InlineData("few", 3, false, 3, 0)]
    [InlineData("exactly the fold", AddressChips.AddressChipsFolded, false, AddressChips.AddressChipsFolded, 0)]
    // "+1 more" would take the room of the chip it hides.
    [InlineData("one over", AddressChips.AddressChipsFolded + 1, false, AddressChips.AddressChipsFolded + 1, 0)]
    [InlineData("two over", AddressChips.AddressChipsFolded + 2, false, AddressChips.AddressChipsFolded, 2)]
    [InlineData("many", 17, false, AddressChips.AddressChipsFolded, 17 - AddressChips.AddressChipsFolded)]
    [InlineData("many, unfolded", 17, true, 17, 0)]
    public void FoldAddressesCases(string name, int count, bool expanded, int wantShown, int wantMore)
    {
        var list = People(count);
        var (shown, more) = AddressChips.FoldAddresses(list, expanded);
        Assert.True(shown.Count == wantShown && more == wantMore, name);
        Assert.True(shown.SequenceEqual(list.Take(shown.Count)), $"{name}: the list's order");
    }

    [Fact]
    public void FoldAddressesSkipsBlankEntries()
    {
        // A group with no members, or an entry that parsed to nothing, has
        // nothing to put on a chip; a name alone or an address alone does.
        Address[] list =
        [
            new() { Email = "" },
            new() { Name = "  ", Email = "" },
            new() { Name = "Only a name", Email = "" },
            new() { Email = "only@example.invalid" },
        ];
        var (shown, more) = AddressChips.FoldAddresses(list, expanded: false);
        Assert.True(shown.Count == 2 && more == 0);
        Assert.Equal("Only a name", shown[0].Name);
        Assert.Equal("only@example.invalid", shown[^1].Email);
        Assert.Empty(AddressChips.FoldAddresses([new Address { Email = "" }, new Address { Email = "" }], expanded: false).Shown);
        Assert.Empty(AddressChips.FoldAddresses(null, expanded: false).Shown);
    }

    [Fact]
    public void AddressKeyTest()
    {
        var acc = new AccountId("acc");
        var baseKey = AddressChips.AddressKey(acc, People(2), 0);
        Assert.Equal(baseKey, AddressChips.AddressKey(acc, People(2), 0)); // the same row gave two keys
        var renamed = People(2);
        renamed[1] = renamed[1] with { Name = "Someone else" };
        (string Name, string Key)[] others =
        [
            ("account", AddressChips.AddressKey(new AccountId("other"), People(2), 0)),
            ("name", AddressChips.AddressKey(acc, renamed, 0)),
            ("fold", AddressChips.AddressKey(acc, People(2), 3)),
            ("count", AddressChips.AddressKey(acc, People(3), 0)),
            // Name and address must not run together: "a" + "bc" is not "ab" + "c".
            ("boundary", AddressChips.AddressKey(acc, [new Address { Name = "Person 0a", Email = "0@example.invalid" }, People(2)[1]], 0)),
        ];
        foreach (var (name, key) in others)
        {
            Assert.True(key != baseKey, $"a different {name} gave the same key");
        }
    }

    // n addresses a0@example.invalid … with names.
    private static List<Address> People(int n) =>
        Enumerable.Range(0, n)
            .Select(i => new Address
            {
                Name = string.Create(CultureInfo.InvariantCulture, $"Person {i}"),
                Email = string.Create(CultureInfo.InvariantCulture, $"a{i}@example.invalid"),
            })
            .ToList();
}
