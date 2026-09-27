// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AddressListTests.swift, the
// counterpart of ui/internal/compose/address_test.go.

using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Xunit;

namespace Malachi.Core.Tests.Compose;

public sealed class AddressListTests
{
    [Fact]
    public void ParseAddressList()
    {
        var (addrs, invalid) = AddressList.Parse(
            "Alice <alice@example.invalid>, bob@example.invalid; \"Doe, Jane\" <jane@example.invalid>, Jörg Müller <j@example.invalid>,");
        Assert.Empty(invalid);
        Assert.Equal(
            [
                new Address { Name = "Alice", Email = "alice@example.invalid" },
                new Address { Email = "bob@example.invalid" },
                new Address { Name = "Doe, Jane", Email = "jane@example.invalid" },
                new Address { Name = "Jörg Müller", Email = "j@example.invalid" },
            ],
            addrs);

        var (valid, bad) = AddressList.Parse("foo, a@b c@d, ok@example.invalid, ,");
        Assert.Equal(["ok@example.invalid"], [.. valid.Select(a => a.Email)]);
        Assert.Equal(["foo", "a@b c@d"], bad);

        var empty = AddressList.Parse("");
        Assert.Empty(empty.Addresses);
        Assert.Empty(empty.Invalid);
    }

    [Fact]
    public void FormatAddressList()
    {
        Address[] input =
        [
            new Address { Name = "Alice", Email = "alice@example.invalid" },
            new Address { Email = "bob@example.invalid" },
            new Address { Name = "Doe, Jane \"JD\"", Email = "jane@example.invalid" },
        ];
        var got = AddressList.Format(input);
        Assert.Equal("Alice <alice@example.invalid>, bob@example.invalid, \"Doe, Jane \\\"JD\\\"\" <jane@example.invalid>", got);
        // Round trip.
        var (back, invalid) = AddressList.Parse(got);
        Assert.Empty(invalid);
        Assert.Equal(3, back.Count);
        Assert.Equal("Doe, Jane \"JD\"", back[^1].Name);
        Assert.Equal(input, back);
    }

    // The subset of net/mail.ParseAddress the parser reproduces.
    [Fact]
    public void ParseAddressGrammar()
    {
        Assert.Equal(new Address { Email = "a@b.example" }, AddressList.ParseAddress("<a@b.example>"));
        Assert.Equal(new Address { Email = "a@b.example" }, AddressList.ParseAddress("\"\" <a@b.example>"));
        Assert.Equal(new Address { Email = "a@b.example" }, AddressList.ParseAddress("  a@b.example  "));
        Assert.Equal(new Address { Name = "Two Words", Email = "a@b.example" }, AddressList.ParseAddress("Two  Words <a@b.example>"));
        Assert.Equal(new Address { Name = "Back\\slash", Email = "a@b.example" }, AddressList.ParseAddress("\"Back\\\\slash\" <a@b.example>"));
        string[] bad =
        [
            "", "   ", "foo", "a@b c@d", "\"foo\"", "me@", "@example.org", "a b@example.org",
            "Name a@b.example", "a@b.example>", "<a@b.example", "a..b@x.example", ".a@x.example", "a@x.example.",
            "a@b.example (comment)", "\"unclosed <a@b.example>", "Name <a@b.example> extra", "a@b\x0001.example",
        ];
        foreach (var s in bad)
        {
            Assert.True(AddressList.ParseAddress(s) is null, $"{s} accepted");
        }
    }

    // Windows: a lone surrogate, the counterpart of Go's invalid UTF-8, is
    // never part of an address.
    [Fact]
    public void LoneSurrogateIsInvalid()
    {
        Assert.Null(AddressList.ParseAddress("a" + (char)0xD800 + "@b.example"));
        Assert.Null(AddressList.ParseAddress("\"a" + (char)0xDC00 + "\" <a@b.example>"));
        var (addrs, invalid) = AddressList.Parse("x" + (char)0xD800 + "@b.example, ok@b.example");
        Assert.Equal(["ok@b.example"], [.. addrs.Select(a => a.Email)]);
        Assert.Single(invalid);
    }
}
