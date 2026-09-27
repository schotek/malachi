// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/SuggestTests.swift, the counterpart of
// ui/internal/compose/suggest_test.go. Go's byte offsets and Swift's String
// indices are UTF-16 indices here; the caret stays a character (Unicode
// scalar) offset.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Xunit;

namespace Malachi.Core.Tests.Compose;

public sealed class SuggestTests
{
    private static readonly Dictionary<string, Address> Addresses = new()
    {
        ["alice"] = new Address { Name = "Alice Example", Email = "alice@example.org" },
        ["quoted"] = new Address { Name = "Novák, Jan", Email = "jan@example.cz" },
        ["bare"] = new Address { Email = "bob@example.org" },
    };

    [Theory]
    [InlineData("", 0, "")]
    [InlineData("al", 2, "al")]
    [InlineData("al", 1, "al")]
    [InlineData("al", 0, "al")]
    [InlineData("bob@example.org, al", 19, "al")]
    [InlineData("bob@example.org, al", 17, "al")]
    [InlineData("bob@example.org, al", 16, "al")] // in the space before the token: still that token
    [InlineData("bob@example.org, al", 15, "bob@example.org")]
    [InlineData("bob@example.org, al", 3, "bob@example.org")]
    [InlineData("al, bob@example.org", 2, "al")]
    [InlineData("al, bob@example.org", 5, "bob@example.org")]
    [InlineData("a;b", 2, "b")]
    [InlineData("\"Nov, Jan\" <jan@example.cz>, al", 6, "\"Nov, Jan\" <jan@example.cz>")] // comma in quotes
    [InlineData("Nov <a,b@example.cz>, al", 7, "Nov <a,b@example.cz>")] // comma in brackets
    [InlineData("Jan Novák, no", 11, "no")] // multi-byte before the caret
    [InlineData("Jan Novák", 9, "Jan Novák")]
    [InlineData("al", 99, "al")] // caret past the end
    [InlineData("  al  ", 3, "al")]
    public void TokenAtCases(string text, int caret, string want)
    {
        var (token, range) = Suggest.TokenAt(text, caret);
        Assert.Equal(want, token);
        Assert.Equal(token, range.Of(text));
    }

    [Theory]
    [InlineData("al", 2, "alice", "Alice Example <alice@example.org>, ", "Alice Example <alice@example.org>, ")]
    [InlineData("bob@example.org, al", 19, "alice", "bob@example.org, Alice Example <alice@example.org>, ", "bob@example.org, Alice Example <alice@example.org>, ")]
    [InlineData("al, bob@example.org", 2, "alice", "Alice Example <alice@example.org>, bob@example.org", "Alice Example <alice@example.org>")]
    [InlineData("al , bob@example.org", 2, "alice", "Alice Example <alice@example.org>, bob@example.org", "Alice Example <alice@example.org>")]
    // Without a separator the whole segment is the token, as the parser
    // would read it.
    [InlineData("al bob@example.org", 2, "alice", "Alice Example <alice@example.org>, ", "Alice Example <alice@example.org>, ")]
    [InlineData("no", 2, "quoted", "\"Novák, Jan\" <jan@example.cz>, ", "\"Novák, Jan\" <jan@example.cz>, ")]
    [InlineData("bo", 2, "bare", "bob@example.org, ", "bob@example.org, ")]
    [InlineData("Novák, bo", 9, "bare", "Novák, bob@example.org, ", "Novák, bob@example.org, ")]
    public void ReplaceTokenCases(string text, int caret, string address, string want, string after)
    {
        var (_, range) = Suggest.TokenAt(text, caret);
        var (got, newCaret) = Suggest.ReplaceToken(text, range, Addresses[address]);
        Assert.Equal(want, got);
        // The caret is a character offset that lands after the inserted address.
        Assert.Equal(after.EnumerateRunes().Count(), newCaret);
        var idx = Suggest.IndexAtScalarOffset(newCaret, got);
        Assert.Equal(Encoding.UTF8.GetByteCount(after), Encoding.UTF8.GetByteCount(got.AsSpan(0, idx)));
    }

    // TestByteOffset: a character offset to a byte offset, clamped.
    [Fact]
    public void ScalarOffsetsMapToBytesAndClamp()
    {
        const string text = "Novák, x";
        var cases = new Dictionary<int, int> { [0] = 0, [3] = 3, [4] = 5, [5] = 6, [8] = 9, [20] = 9, [-1] = 0 };
        foreach (var (chars, wantBytes) in cases)
        {
            var idx = Suggest.IndexAtScalarOffset(chars, text);
            Assert.True(Encoding.UTF8.GetByteCount(text.AsSpan(0, idx)) == wantBytes, $"chars {chars}");
        }
        for (var chars = 0; chars <= 8; chars++)
        {
            Assert.Equal(chars, Suggest.ScalarOffset(Suggest.IndexAtScalarOffset(chars, text), text));
        }
        Assert.Equal(8, Suggest.ScalarOffset(text.Length, text));
        Assert.Equal(text.Length, Suggest.IndexAtScalarOffset(20, text));
        Assert.Equal(0, Suggest.IndexAtScalarOffset(-1, text));
    }

    // Windows: a scalar beyond the BMP is one character and two UTF-16 code
    // units; the caret counts it once.
    [Fact]
    public void ScalarsBeyondTheBmp()
    {
        var text = "a" + char.ConvertFromUtf32(0x1F600) + "b, cd";
        Assert.Equal(3, Suggest.IndexAtScalarOffset(2, text));
        Assert.Equal(2, Suggest.ScalarOffset(3, text));
        var (token, range) = Suggest.TokenAt(text, 6);
        Assert.Equal("cd", token);
        Assert.Equal(new TextSpan(6, 8), range);
        var (got, caret) = Suggest.ReplaceToken(text, range, Addresses["bare"]);
        Assert.Equal("a" + char.ConvertFromUtf32(0x1F600) + "b, bob@example.org, ", got);
        Assert.Equal(got.EnumerateRunes().Count(), caret);
    }

    [Fact]
    public void SuggestionIconAndTooltip()
    {
        var book = new Contact { Address = "a@x.example", Source = ContactSource.AddressBook, Book = "Contacts" };
        var unnamed = new Contact { Address = "a@x.example", Source = ContactSource.AddressBook };
        var sent = new Contact { Address = "a@x.example", Source = ContactSource.Sent };
        Assert.NotEqual(Suggest.SuggestionIcon(book.Source), Suggest.SuggestionIcon(sent.Source));
        Assert.Equal("x-office-address-book-symbolic", Suggest.SuggestionIcon(ContactSource.AddressBook));
        Assert.Equal("document-open-recent-symbolic", Suggest.SuggestionIcon(ContactSource.Sent));
        Assert.Equal("Contacts", Suggest.SuggestionTooltip(book));
        Assert.NotEmpty(Suggest.SuggestionTooltip(unnamed));
        Assert.NotEmpty(Suggest.SuggestionTooltip(sent));
        Assert.NotEqual(Suggest.SuggestionTooltip(unnamed), Suggest.SuggestionTooltip(sent));
        Assert.Equal(Suggest.SuggestionTooltip(unnamed), Suggest.SuggestionTooltip(new Contact { Address = "a@x.example", Source = ContactSource.AddressBook, Book = "" }));
    }

    [Fact]
    public void SplitAddressRanges()
    {
        const string text = "a@x, \"b,c\" <b@x>;d@x";
        Assert.Equal(["a@x", " \"b,c\" <b@x>", "d@x"], [.. AddressList.SplitRanges(text).Select(r => r.Of(text))]);
        var empty = AddressList.SplitRanges("");
        Assert.Single(empty);
        Assert.True(empty[0].IsEmpty);
        Assert.Equal(0, Suggest.ScalarOffset(empty[0].Start, ""));
    }

    [Fact]
    public void Constants()
    {
        Assert.Equal(2, Suggest.SuggestMinChars);
        Assert.Equal(TimeSpan.FromMilliseconds(150), Suggest.SuggestDebounce);
        Assert.Equal(8, Suggest.SuggestLimit);
    }
}
