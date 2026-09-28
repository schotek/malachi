// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/MailtoTests.swift, the counterpart of
// ui/internal/compose/mailto_test.go.

using System.Linq;
using Malachi.Core.Compose;
using Xunit;

namespace Malachi.Core.Tests.Compose;

public sealed class MailtoTests
{
    [Fact]
    public void ParseMailtoFields()
    {
        var p = Mailto.ParseMailto(
            "mailto:alice@example.invalid?subject=Hi%20there&body=Line1%0ALine2&cc=bob@example.invalid&Bcc=carol@example.invalid&to=dave@example.invalid&in-reply-to=x");
        Assert.Equal(ComposeKind.New, p.Kind);
        Assert.Equal(["alice@example.invalid", "dave@example.invalid"], [.. p.To.Select(a => a.Email)]);
        Assert.Equal(["bob@example.invalid"], [.. p.Cc.Select(a => a.Email)]);
        Assert.Equal(["carol@example.invalid"], [.. p.Bcc.Select(a => a.Email)]);
        Assert.Equal("Hi there", p.Subject);
        Assert.Equal("Line1<br>Line2", p.BodyHtml);
    }

    [Fact]
    public void HostileBodyIsEscaped()
    {
        var p = Mailto.ParseMailto("mailto:x@example.invalid?body=%3Cscript%3Ealert(1)%3C/script%3E");
        Assert.DoesNotContain("<script", p.BodyHtml, System.StringComparison.Ordinal);
        Assert.Contains("&lt;script&gt;", p.BodyHtml, System.StringComparison.Ordinal);
    }

    [Fact]
    public void RefusesOtherSchemes()
    {
        Assert.Equal(MailtoError.NotMailto, Assert.Throws<MailtoException>(() => Mailto.ParseMailto("https://example.invalid")).Error);
        Assert.Equal(MailtoError.NotMailto, Assert.Throws<MailtoException>(() => Mailto.ParseMailto("alice@example.invalid")).Error);
        Assert.Equal(MailtoError.InvalidUri, Assert.Throws<MailtoException>(() => Mailto.ParseMailto("mailto:a@example.invalid\x0001")).Error);
        Assert.Equal(MailtoError.InvalidUri, Assert.Throws<MailtoException>(() => Mailto.ParseMailto(":nope")).Error);
    }

    [Fact]
    public void EmptyMailto()
    {
        var p = Mailto.ParseMailto("mailto:");
        Assert.Empty(p.To);
        Assert.Equal(new ComposeParams { Kind = ComposeKind.New }, p);
    }

    // The query is read as net/url does: "+" is a space, keys are
    // case-insensitive, the first value of a key wins, a malformed pair is
    // dropped, a fragment is not part of the address.
    [Fact]
    public void QueryDetails()
    {
        var p = Mailto.ParseMailto("MAILTO:a@example.invalid?Subject=a+b&subject=second&body=%zz&TO=b@example.invalid#frag");
        Assert.Equal(["a@example.invalid", "b@example.invalid"], [.. p.To.Select(a => a.Email)]);
        Assert.True(p.Subject is "a b" or "second", p.Subject);
        Assert.Empty(p.BodyHtml);
        var percent = Mailto.ParseMailto("mailto:a%40example.invalid?subject=%25");
        Assert.Equal(["a@example.invalid"], [.. percent.To.Select(a => a.Email)]);
        Assert.Equal("%", percent.Subject);
    }

    // mailto_test.go TestParseMailto: the cases the Swift suite split up,
    // together.
    [Fact]
    public void TestParseMailto()
    {
        var p = Mailto.ParseMailto(
            "mailto:alice@example.invalid?subject=Hi%20there&body=Line1%0ALine2&cc=bob@example.invalid&Bcc=carol@example.invalid&to=dave@example.invalid&in-reply-to=x");
        Assert.True(p.To.Count == 2 && p.To[0].Email == "alice@example.invalid" && p.To[1].Email == "dave@example.invalid");
        Assert.True(p.Cc.Count == 1 && p.Bcc.Count == 1 && p.Subject == "Hi there" && p.BodyHtml == "Line1<br>Line2");
        p = Mailto.ParseMailto("mailto:x@example.invalid?body=%3Cscript%3Ealert(1)%3C/script%3E");
        Assert.True(!p.BodyHtml.Contains("<script", System.StringComparison.Ordinal) && p.BodyHtml.Contains("&lt;script&gt;", System.StringComparison.Ordinal));
        Assert.Throws<MailtoException>(() => Mailto.ParseMailto("https://example.invalid"));
        Assert.Empty(Mailto.ParseMailto("mailto:").To);
    }

    // Windows: Go's url.Parse rules that System.Uri does not share: a
    // malformed escape in the fragment or a control byte refuses the URI, a
    // semicolon drops its pair, a malformed escape in the address only drops
    // the address.
    [Fact]
    public void GoUrlRules()
    {
        Assert.Equal(MailtoError.InvalidUri, Assert.Throws<MailtoException>(() => Mailto.ParseMailto("mailto:a@example.invalid#%zz")).Error);
        Assert.Equal(MailtoError.InvalidUri, Assert.Throws<MailtoException>(() => Mailto.ParseMailto("mailto:a@example.invalid\x007F")).Error);
        var p = Mailto.ParseMailto("mailto:a@example.invalid?subject=x;y&cc=c@example.invalid");
        Assert.Equal("", p.Subject);
        Assert.Equal(["c@example.invalid"], [.. p.Cc.Select(a => a.Email)]);
        // A malformed escape in the address leaves it out: url.Parse keeps
        // the opaque part as it is, url.PathUnescape then fails on it.
        Assert.Empty(Mailto.ParseMailto("mailto:a%zz@example.invalid").To);
        // A trailing lone "?" is no query.
        Assert.Equal(["a@example.invalid"], [.. Mailto.ParseMailto("mailto:a@example.invalid?").To.Select(a => a.Email)]);
    }
}
