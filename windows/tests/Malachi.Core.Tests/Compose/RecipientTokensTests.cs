// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/RecipientTokensTests.swift, the
// counterpart of ui/internal/recipients/tokens_test.go. Control and bidi
// characters are built from code points (U), never written into the source.

using System;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Xunit;

namespace Malachi.Core.Tests.Compose;

public sealed class RecipientTokensTests
{
    private static string[] Labels(RecipientTokens t) => [.. t.Tokens.Select(x => x.Label)];

    // A string of the given code points.
    private static string U(params int[] codes) => string.Concat(codes.Select(char.ConvertFromUtf32));

    private sealed record InitCase(string Name, string Input, string[] Labels, string Text, bool Invalid);

    private static InitCase[] InitCases() =>
    [
        new("empty", "", [], "", false),
        new("blank", " \t ", [], "", false),
        new("plain", "a@b.cz, c@d.cz", ["a@b.cz", "c@d.cz"], "a@b.cz, c@d.cz", false),
        new("semicolons", "a@b.cz; c@d.cz;", ["a@b.cz", "c@d.cz"], "a@b.cz, c@d.cz", false),
        new("quoted comma", "\"Doe, John\" <j@x.cz>, a@b.cz", ["Doe, John", "a@b.cz"], "\"Doe, John\" <j@x.cz>, a@b.cz", false),
        new("name", "Jörg Müller <j@x.cz>", ["Jörg Müller"], "Jörg Müller <j@x.cz>", false),
        new("bare name is invalid", "Radek, a@b.cz", ["Radek", "a@b.cz"], "Radek, a@b.cz", true),
        new("unclosed angle swallows the rest", "Radek <x@y.cz, a@b.cz", ["Radek <x@y.cz, a@b.cz"], "Radek <x@y.cz, a@b.cz", true),
        new("unclosed quote swallows the rest", "\"Radek <x@y.cz>, a@b.cz", ["\"Radek <x@y.cz>, a@b.cz"], "\"Radek <x@y.cz>, a@b.cz", true),
        new("empty entries", ", ,a@b.cz,,", ["a@b.cz"], "a@b.cz", false),
        new("controls", "a@b.cz" + U(0, 7) + ", c@" + U(0x1f) + "d.cz" + U(0x2028), ["a@b.cz", "c@d.cz"], "a@b.cz, c@d.cz", false),
        new("rtl override", U(0x202e) + "evil <a@b.cz>", ["evil"], U(0x202e) + "evil <a@b.cz>", false),
    ];

    [Fact]
    public void ConstructFromText()
    {
        foreach (var c in InitCases())
        {
            var tk = new RecipientTokens(c.Input);
            Assert.True(c.Labels.SequenceEqual(Labels(tk)), c.Name);
            Assert.Equal(c.Text, tk.Text);
            Assert.Equal(c.Invalid, tk.HasInvalid);
            Assert.Equal("", tk.Pending);
        }
    }

    [Fact]
    public void TokenParts()
    {
        var tk = new RecipientTokens("\"Doe, John\" <j@x.cz>, a@b.cz, nobody");
        Assert.Equal(3, tk.Tokens.Count);
        Assert.Equal("Doe, John", tk.Tokens[0].Label);
        Assert.Equal("\"Doe, John\" <j@x.cz>", tk.Tokens[0].Tooltip);
        Assert.True(tk.Tokens[0].IsValid);
        Assert.Equal("a@b.cz", tk.Tokens[1].Label);
        Assert.Equal("a@b.cz", tk.Tokens[1].Tooltip);
        Assert.False(tk.Tokens[2].IsValid);
        Assert.Equal("nobody", tk.Tokens[2].Label);
        Assert.Equal("nobody", tk.Tokens[2].Tooltip);
        Assert.Equal("nobody", tk.Tokens[2].Raw);
        Assert.Null(tk.Tokens[2].Address);
    }

    [Fact]
    public void RoundTrip()
    {
        Address[] list =
        [
            new Address { Name = "Alice", Email = "alice@example.invalid" },
            new Address { Email = "bob@example.invalid" },
            new Address { Name = "Doe, Jane \"JD\"", Email = "jane@example.invalid" },
            new Address { Name = "Jörg; Müller", Email = "j@example.invalid" },
        ];
        var text = AddressList.Format(list);
        var tk = new RecipientTokens(text);
        Assert.Equal(text, tk.Text);
        Assert.Equal(list, tk.Tokens.Select(x => x.Address!).ToArray());
        var (back, invalid) = AddressList.Parse(tk.Text);
        Assert.Equal(list, back);
        Assert.Empty(invalid);
    }

    [Fact]
    public void LabelsAndTooltipsHideBidiControls()
    {
        var bidi = new[] { 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069, 0x200e, 0x200f, 0x061c };
        var noise = U(bidi);
        var tk = new RecipientTokens(noise + "Eve" + noise + " <e@x.cz>, " + noise + "plain" + noise);
        Assert.Equal("Eve", tk.Tokens[0].Label);
        Assert.Equal("Eve <e@x.cz>", tk.Tokens[0].Tooltip);
        Assert.Equal("plain", tk.Tokens[1].Label);
        Assert.Equal("plain", tk.Tokens[1].Tooltip);
        // The token itself keeps what was typed.
        Assert.Contains(U(0x202e), tk.Tokens[0].Address!.Name, StringComparison.Ordinal);
        Assert.Contains(U(0x202e), tk.Tokens[1].Raw, StringComparison.Ordinal);
        Assert.Contains(U(0x202e), tk.Text, StringComparison.Ordinal);
    }

    [Fact]
    public void Resolved()
    {
        // A name with a colon is a valid token, but the formatted text does
        // not read back; Resolved does not go through the text.
        var tk = new RecipientTokens("\"ACME: Support\" <x@y.cz>, nobody");
        var (addresses, invalid) = tk.Resolved();
        Assert.Equal([new Address { Name = "ACME: Support", Email = "x@y.cz" }], addresses);
        Assert.Equal(["nobody"], invalid);

        // Two invalid tokens whose joined text would read as one address.
        tk = new RecipientTokens();
        tk.SetPending("\"Joe");
        tk.Commit();
        tk.SetPending("a\" <b@c.cz>");
        tk.Commit();
        (addresses, invalid) = tk.Resolved();
        Assert.Empty(addresses);
        Assert.Equal(["\"Joe", "a\" <b@c.cz>"], invalid);

        // Pending is evaluated like Commit, without changing the model.
        tk = new RecipientTokens("a@b.cz");
        tk.SetPending("c@d.cz");
        (addresses, invalid) = tk.Resolved();
        Assert.Equal(2, addresses.Count);
        Assert.Equal("c@d.cz", addresses[1].Email);
        Assert.Empty(invalid);
        Assert.Equal("c@d.cz", tk.Pending);
        Assert.Single(tk.Tokens);

        // Blank pending adds nothing.
        tk.SetPending("  ");
        (addresses, invalid) = tk.Resolved();
        Assert.Single(addresses);
        Assert.Empty(invalid);

        // Pending that holds separators (left by the token cap) gives several entries.
        var many = new RecipientTokens(string.Join(",", Enumerable.Range(0, RecipientTokens.MaxTokens).Select(i => CultureInvariant($"u{i}@x.cz"))) + ", a@b.cz, junk, c@d.cz");
        Assert.Equal("a@b.cz, junk, c@d.cz", many.Pending);
        (addresses, invalid) = many.Resolved();
        Assert.Equal(RecipientTokens.MaxTokens + 2, addresses.Count);
        Assert.Equal(["junk"], invalid);
        Assert.Equal(RecipientTokens.MaxTokens, many.Tokens.Count);

        Assert.Empty(new RecipientTokens().Resolved().Addresses);
        Assert.Empty(new RecipientTokens().Resolved().Invalid);
    }

    private sealed record PendingCase(string Name, string Input, bool Changed, string[] Labels, string Pending);

    private static PendingCase[] PendingCases() =>
    [
        new("typing", "Rad", false, [], "Rad"),
        new("comma completes", "a@b.cz,", true, ["a@b.cz"], ""),
        new("comma keeps the rest", "a@b.cz, c@d", true, ["a@b.cz"], "c@d"),
        new("semicolon", "a@b.cz; c@d", true, ["a@b.cz"], "c@d"),
        new("several", "a@b.cz, c@d.cz, e", true, ["a@b.cz", "c@d.cz"], "e"),
        new("quoted comma waits", "\"Doe, Jo", false, [], "\"Doe, Jo"),
        new("quoted comma then address", "\"Doe, John\" <j@x.cz>,", true, ["Doe, John"], ""),
        new("angle comma waits", "Radek <x@y.cz, ", false, [], "Radek <x@y.cz, "),
        new("bare address then space", "bohmova@satomar.cz ", true, ["bohmova@satomar.cz"], ""),
        new("bare address then tab", "bohmova@satomar.cz\t", true, ["bohmova@satomar.cz"], ""),
        new("bare address no space", "bohmova@satomar.cz", false, [], "bohmova@satomar.cz"),
        new("name then space", "Radek ", false, [], "Radek "),
        new("half typed angle", "Radek Bábíček <x@y.cz ", false, [], "Radek Bábíček <x@y.cz "),
        new("closed angle then space is not bare", "Radek <x@y.cz> ", false, [], "Radek <x@y.cz> "),
        new("bare angle then space is not bare", "<x@y.cz> ", false, [], "<x@y.cz> "),
        new("invalid entry before comma", "foo, a", true, ["foo"], "a"),
        new("separator only", ",", true, [], ""),
        new("newline is a space", "a@b.cz\n", true, ["a@b.cz"], ""),
        new("controls dropped", "a" + U(0) + "b", false, [], "ab"),
    ];

    [Fact]
    public void SetPending()
    {
        foreach (var c in PendingCases())
        {
            var tk = new RecipientTokens();
            var changed = tk.SetPending(c.Input);
            Assert.True(c.Changed == changed, c.Name);
            Assert.True(c.Labels.SequenceEqual(Labels(tk)), c.Name);
            Assert.True(c.Pending == tk.Pending, c.Name);
        }
    }

    [Fact]
    public void SetPendingKeepsEarlierTokens()
    {
        var tk = new RecipientTokens("a@b.cz");
        tk.SetPending("c@d.cz, e");
        Assert.Equal(["a@b.cz", "c@d.cz"], Labels(tk));
        Assert.Equal("e", tk.Pending);
        Assert.Equal("a@b.cz, c@d.cz, e", tk.Text);
        // Blank pending is not part of the value.
        tk.SetPending("   ");
        Assert.Equal("a@b.cz, c@d.cz", tk.Text);
    }

    [Fact]
    public void Commit()
    {
        var tk = new RecipientTokens();
        tk.SetPending("Radek");
        var changed = tk.Commit();
        Assert.True(changed);
        Assert.Equal("", tk.Pending);
        Assert.False(tk.Tokens[0].IsValid);
        Assert.True(tk.HasInvalid);
        tk.SetPending("a@b.cz");
        changed = tk.Commit();
        Assert.True(changed);
        Assert.Equal(2, tk.Tokens.Count);
        Assert.True(tk.Tokens[1].IsValid);
        tk.SetPending("   ");
        changed = tk.Commit();
        Assert.False(changed);
        Assert.Equal("", tk.Pending);
        Assert.Equal(2, tk.Tokens.Count);
        changed = tk.Commit();
        Assert.False(changed);
    }

    [Fact]
    public void Add()
    {
        var tk = new RecipientTokens();
        tk.SetPending("Ra");
        tk.Add(new Address { Name = "Radek B.", Email = "r@x.cz" });
        tk.Add(new Address { Email = "r@x.cz" });
        tk.Add(new Address { Email = "r@x.cz" });
        Assert.Equal("", tk.Pending);
        Assert.Equal(["Radek B.", "r@x.cz", "r@x.cz"], Labels(tk));
        Assert.Equal("Radek B. <r@x.cz>, r@x.cz, r@x.cz", tk.Text);
    }

    [Fact]
    public void Remove()
    {
        var tk = new RecipientTokens("a@b.cz, c@d.cz, e@f.cz");
        tk.Remove(1);
        Assert.Equal(["a@b.cz", "e@f.cz"], Labels(tk));
        foreach (var i in new[] { -1, 2, 99 })
        {
            tk.Remove(i);
            Assert.Equal(2, tk.Tokens.Count);
        }
    }

    [Fact]
    public void Edit()
    {
        var tk = new RecipientTokens("\"Doe, John\" <j@x.cz>, a@b.cz, nobody");
        tk.SetPending("typed");
        var got = tk.Edit(0);
        Assert.Equal("\"Doe, John\" <j@x.cz>", got);
        // Pending was committed first, the edited token left the list.
        Assert.Equal(["a@b.cz", "nobody", "typed"], Labels(tk));
        Assert.Equal("\"Doe, John\" <j@x.cz>", tk.Pending);
        got = tk.Edit(1);
        Assert.Equal("nobody", got);
        Assert.Equal("nobody", tk.Pending);
        // Out of range: nothing changes, not even the pending text.
        var before = tk.Tokens.Count;
        foreach (var i in new[] { -1, before, 99 })
        {
            got = tk.Edit(i);
            Assert.Equal("nobody", got);
            Assert.Equal(before, tk.Tokens.Count);
        }
    }

    private sealed record PasteCase(string Name, string Pending, string Input, string[] Labels, string Pending2);

    private static PasteCase[] PasteCases() =>
    [
        new("no separator is typing", "", "Rad", [], "Rad"),
        new("appends to pending", "Ra", "dek", [], "Radek"),
        new("bare address and space", "", "a@b.cz ", ["a@b.cz"], ""),
        new("commas", "", "a@b.cz, c@d.cz", ["a@b.cz", "c@d.cz"], ""),
        new("semicolons", "", "a@b.cz; c@d.cz; e@f.cz", ["a@b.cz", "c@d.cz", "e@f.cz"], ""),
        new("newlines", "", "a@b.cz\nc@d.cz\r\ne@f.cz\n", ["a@b.cz", "c@d.cz", "e@f.cz"], ""),
        new("tabs", "", "a@b.cz\tc@d.cz", ["a@b.cz", "c@d.cz"], ""),
        new("last entry becomes a token", "", "a@b.cz, Radek", ["a@b.cz", "Radek"], ""),
        new("pending joins the first entry", "x", "@y.cz, c@d.cz", ["x@y.cz", "c@d.cz"], ""),
        new("quoted comma", "", "\"Doe, John\" <j@x.cz>; a@b.cz", ["Doe, John", "a@b.cz"], ""),
        new("quoted newline stays", "", "\"Doe,\nJohn\" <j@x.cz>\na@b.cz", ["Doe, John", "a@b.cz"], ""),
        new("unclosed angle is no separator", "", "Radek <x@y.cz, a@b.cz", [], "Radek <x@y.cz, a@b.cz"),
        new("whitespace only", "", " \n\t ", [], ""),
        new("controls", "", "a@b.cz," + U(0) + "c@d.cz" + U(0x1b) + " ", ["a@b.cz", "c@d.cz"], ""),
    ];

    [Fact]
    public void Paste()
    {
        foreach (var c in PasteCases())
        {
            var tk = new RecipientTokens();
            tk.SetPending(c.Pending);
            tk.Paste(c.Input);
            Assert.True(c.Labels.SequenceEqual(Labels(tk)), c.Name);
            Assert.True(c.Pending2 == tk.Pending, c.Name);
        }
    }

    [Fact]
    public void HostileSizes()
    {
        // 5000 addresses: MaxTokens become tokens, the rest stays pending.
        var lines = new StringBuilder();
        for (var i = 0; i < 5000; i++)
        {
            lines.Append(CultureInvariant($"u{i}@x.cz\n"));
        }
        var tk = new RecipientTokens();
        tk.Paste(lines.ToString());
        Assert.Equal(RecipientTokens.MaxTokens, tk.Tokens.Count);
        Assert.Equal("u0@x.cz", tk.Tokens[0].Raw);
        Assert.Equal("u999@x.cz", tk.Tokens[RecipientTokens.MaxTokens - 1].Raw);
        Assert.StartsWith("u1000@x.cz, u1001@x.cz", tk.Pending, StringComparison.Ordinal);

        var commaed = lines.ToString().Trim().Replace("\n", ",", StringComparison.Ordinal);
        var fromText = new RecipientTokens(commaed);
        Assert.Equal(RecipientTokens.MaxTokens, fromText.Tokens.Count);
        Assert.StartsWith("u1000@x.cz,u1001@x.cz", fromText.Pending, StringComparison.Ordinal);
        var (addresses, invalid) = fromText.Resolved();
        Assert.Equal(5000, addresses.Count);
        Assert.Empty(invalid);
        Assert.Equal(RecipientTokens.MaxTokens, fromText.Tokens.Count);
        Assert.Equal(5000, fromText.Text.Count(c => c == '@'));

        // Typing past the cap keeps the rest pending too.
        var typed = new RecipientTokens();
        typed.SetPending(commaed + ",");
        Assert.Equal(RecipientTokens.MaxTokens, typed.Tokens.Count);
        Assert.NotEqual("", typed.Pending);
        Assert.Equal(5000, typed.Resolved().Addresses.Count);
        // Commit and Add append beyond the cap.
        Assert.True(typed.Commit());
        Assert.Equal(5000, typed.Tokens.Count);
        Assert.Equal("", typed.Pending);
        typed.Add(new Address { Email = "z@x.cz" });
        Assert.Equal(5001, typed.Tokens.Count);

        // 1 MB without a separator: a paste is cut at 64 KiB.
        var big = new RecipientTokens();
        big.Paste(new string('a', 1 << 20));
        Assert.Equal(RecipientTokens.MaxInput, Encoding.UTF8.GetByteCount(big.Pending));
        Assert.Empty(big.Tokens);
        // A multi-byte paste is cut on a scalar boundary.
        var multi = new RecipientTokens();
        multi.Paste(new string('é', 1 << 20));
        Assert.Equal(RecipientTokens.MaxInput, Encoding.UTF8.GetByteCount(multi.Pending));
        Assert.DoesNotContain((char)0xFFFD, multi.Pending, StringComparison.Ordinal);
        var astral = new RecipientTokens();
        astral.Paste(string.Concat(Enumerable.Repeat(char.ConvertFromUtf32(0x1F600), 1 << 18)));
        Assert.Equal(RecipientTokens.MaxInput, Encoding.UTF8.GetByteCount(astral.Pending));
        // Typed and initial text are never clipped.
        var typedBig = new RecipientTokens();
        typedBig.SetPending(new string('é', 1 << 20));
        Assert.Equal(1 << 20, typedBig.Pending.Length);
        Assert.Equal(1 << 20, new RecipientTokens(new string('a', 1 << 20)).Tokens[0].Raw.Length);
        // 1 MB of addresses pasted: input beyond 64 KiB is ignored.
        var many = new RecipientTokens();
        many.Paste(string.Concat(Enumerable.Repeat("a@b.cz,", 1 << 17)));
        Assert.InRange(many.Tokens.Count, 1, RecipientTokens.MaxTokens);
    }

    [Fact]
    public void CommitSplitsWhatTheCapLeft()
    {
        // The pending text of a field at its cap holds separators; Commit takes every entry.
        var capped = new RecipientTokens(string.Join(", ", Enumerable.Range(0, RecipientTokens.MaxTokens + 2).Select(i => CultureInvariant($"u{i}@x.cz"))));
        Assert.Equal(RecipientTokens.MaxTokens, capped.Tokens.Count);
        Assert.True(capped.Commit());
        Assert.Equal(RecipientTokens.MaxTokens + 2, capped.Tokens.Count);
        Assert.Equal("", capped.Pending);

        // Typing a bare address with a space does not fold at the cap.
        var full = new RecipientTokens(string.Join(", ", Enumerable.Range(0, RecipientTokens.MaxTokens).Select(i => CultureInvariant($"u{i}@x.cz"))));
        Assert.False(full.SetPending("late@x.cz "));
        Assert.Equal("late@x.cz ", full.Pending);
        Assert.Equal(RecipientTokens.MaxTokens, full.Tokens.Count);
    }

    [Fact]
    public void InvalidSurrogatesAreReplacement()
    {
        var tk = new RecipientTokens();
        tk.SetPending("a" + (char)0xD800 + "b");
        Assert.Equal("a" + (char)0xFFFD + "b", tk.Pending);
    }

    private static string CultureInvariant(FormattableString s) => FormattableString.Invariant(s);
}
