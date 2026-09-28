// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/HTMLLinksTests.swift, the counterpart
// of ui/internal/htmlview/links_test.go. Its linkDecisions test is the test
// of Model/RemoteBar.swift (ActivatedLink, linkDecision), which is ported
// with the models (Malachi.Core.Model), not here.

using System;
using Malachi.Core.Html;
using Xunit;

namespace Malachi.Core.Tests.Html;

public sealed class HtmlLinksTests
{
    [Theory]
    [InlineData("https://bank.example.org/", "https://evil.example.net/login", true)]
    [InlineData("bank.example.org", "https://evil.example.net/", true)]
    [InlineData("www.bank.example.org/account", "http://evil.example.net", true)]
    [InlineData("https://example.org", "https://example.org/x", false)]
    [InlineData("example.org", "https://www.example.org/", false)]
    [InlineData("www.example.org", "https://example.org/", false)]
    [InlineData("docs.example.org", "https://example.org/docs", false)]
    [InlineData("example.org", "https://docs.example.org/", false)]
    [InlineData("bank.example.org", "https://evil.example.org/", true)]
    [InlineData("click here", "https://evil.example.net/", false)]
    [InlineData("Read more", "https://example.org/", false)]
    [InlineData("v1.2", "https://example.org/", false)]
    [InlineData("e.g.", "https://example.org/", false)]
    [InlineData("", "https://example.org/", false)]
    [InlineData("https://example.org", "mailto:a@example.net", false)]
    [InlineData("support@example.org", "https://evil.example.net/", false)]
    // Port, userinfo, upper case and a malformed target.
    [InlineData("https://bank.example.org", "https://user:pw@EVIL.example.net:8443/x?y#z", true)]
    [InlineData("https://bank.example.org:8443", "https://bank.example.org/", false)]
    // Windows: true, where GTK and macOS say false (see HostNotCertain).
    [InlineData("bank.example.org", "https://evil.example.net/%zz", true)]
    [InlineData("bank.example.org", "https://[::1]/", true)]
    public void Masked(string text, string href, bool want)
    {
        Assert.Equal(want, Links.IsMasked(text, href));
    }

    // Windows: IsMasked fails closed where GTK's Masked opens
    // (windows/README.md). Under a text that names a host, an href whose
    // host Go's parser cannot tell (it refuses the userinfo, the escape, the
    // backslash), whose host is empty, or that carries userinfo at all is
    // masked: the browser reads such an href its own way and goes to the
    // host after the "@" (the security audit's bypass, whose hrefs these
    // are). Text that names no host still never is.
    [Theory]
    [InlineData("https:// www.mojebanka.example@evil.example/space")]
    [InlineData("https://%www.mojebanka.example@evil.example/pct")]
    [InlineData("https://[www.mojebanka.example@evil.example/bracket")]
    [InlineData("https://­www.mojebanka.example@evil.example/shy")]
    [InlineData("https://。www.mojebanka.example@evil.example/ideo")]
    [InlineData("https://www.mojebanka.example^@evil.example/caret")]
    [InlineData("https://www.mojebanka.example|@evil.example/pipe")]
    [InlineData("https://www.mojebanka.example{x}@evil.example/brace")]
    [InlineData("https://www.mojebanka.example\"@evil.example/quote")]
    [InlineData("https://www.mojebanka.example\\@evil.example/bs2")]
    [InlineData("https://www.mojebanka.example@evil.example/plainuserinfo")]
    [InlineData("https:///evil.example/triple")]
    // Userinfo over the very host the text names, or empty.
    [InlineData("https://www.mojebanka.example@www.mojebanka.example/login")]
    [InlineData("https://@www.mojebanka.example/login")]
    [InlineData("https://user:pw@www.mojebanka.example/login")]
    // No host, or none Go can tell.
    [InlineData("https:")]
    [InlineData("https:www.mojebanka.example")]
    [InlineData("https://www.mojebanka.example/%zz")]
    [InlineData("/login")]
    public void HostNotCertain(string href)
    {
        Assert.True(Links.IsMasked("https://www.mojebanka.example/login", href));
        Assert.True(Links.IsMasked("www.mojebanka.example", href));
        Assert.False(Links.IsMasked("click here", href));
        Assert.False(Links.IsMasked("", href));
    }

    // A mailto: link goes to the composer, which shows its address, never to
    // the browser: it has no host to judge, however it is written.
    [Theory]
    [InlineData("mailto:a@example.net")]
    [InlineData("MAILTO:a@example.net")]
    [InlineData("mailto:%zz")]
    [InlineData(" mailto:a@www.mojebanka.example@evil.example ")]
    public void MailtoIsNeverMasked(string href)
    {
        Assert.False(Links.IsMasked("https://www.mojebanka.example/login", href));
    }

    // Windows: the address the launcher would open (ILauncher.LinkTarget:
    // escaped, the host as DNS gets it, no userinfo) against the text; an
    // address it refuses (null) or cannot be read leads nowhere the text
    // could name. Text that names no host never leads elsewhere.
    [Theory]
    [InlineData("https://www.mojebanka.example/login", "https://www.mojebanka.example/login", false)]
    [InlineData("https://www.mojebanka.example/login", "https://mojebanka.example/", false)]
    [InlineData("https://www.mojebanka.example/login", "https://ib.mojebanka.example:8443/x?y#z", false)]
    [InlineData("mojebanka.example", "http://www.mojebanka.example/", false)]
    [InlineData("https://www.mojebanka.example/login", "https://evil.example/space", true)]
    [InlineData("https://www.mojebanka.example/login", "https://mojebanka.example.evil.example/", true)]
    [InlineData("https://www.mojebanka.example/login", "https://xn--mojebank-8za.example/", true)]
    [InlineData("https://www.mojebanka.example/login", "https://[::1]/", true)]
    [InlineData("https://www.mojebanka.example/login", "https:///x", true)]
    [InlineData("https://www.mojebanka.example/login", "https://%zz/", true)]
    [InlineData("https://www.mojebanka.example/login", null, true)]
    [InlineData("click here", "https://evil.example/", false)]
    [InlineData("click here", null, false)]
    public void LeadsElsewhere(string text, string? target, bool want)
    {
        Assert.Equal(want, Links.LeadsElsewhere(text, target));
    }

    [Fact]
    public void AllowedLinks()
    {
        foreach (var ok in new[] { "https://x/", "HTTP://x", "mailto:a@b" })
        {
            Assert.True(Links.AllowedLink(ok), $"{ok} refused");
        }
        foreach (var bad in new[] { "javascript:x", "ftp://x", "data:text/html,x", "", "malachi-cid:a/b/1", "cid:x", " https://x/" })
        {
            Assert.False(Links.AllowedLink(bad), $"{bad} allowed");
        }
    }

    [Fact]
    public void ParsePath()
    {
        var parsed = PartPath.ParsePartPath("acc_1a2b/m_3c4d/1.2");
        Assert.NotNull(parsed);
        Assert.Equal("acc_1a2b", parsed.AccountId.Value);
        Assert.Equal("m_3c4d", parsed.MessageId.Value);
        Assert.Equal("1.2", parsed.PartId);
        string[] bad =
        [
            "", "a/b", "a/b/c/d", "/b/1", "a//1", "a/b/x", "a/b/1..2", "../etc/1", "a/b/" + new string('1', 200),
            "a b/c/1", "a/b/1?x=1", new string('a', 129) + "/b/1", "a/b/.1", "a/b/1.", "\x00e1/b/1",
        ];
        foreach (var s in bad)
        {
            Assert.True(PartPath.ParsePartPath(s) is null, $"ParsePath({s}) accepted");
        }
        Assert.Equal("malachi-cid", PartPath.PartScheme);
    }

    [Fact]
    public void ImageTypes()
    {
        foreach (var ok in new[] { "image/png", "IMAGE/JPEG", "image/gif; charset=binary", " image/webp " })
        {
            Assert.True(PartPath.IsImageType(ok), $"{ok} refused");
        }
        foreach (var bad in new[] { "image/svg+xml", "IMAGE/SVG+XML; charset=utf-8", "text/html", "application/pdf", "", "imagex/png" })
        {
            Assert.False(PartPath.IsImageType(bad), $"{bad} accepted");
        }
    }

    [Fact]
    public void Document()
    {
        var doc = ViewerDocument.Document("<p>a &amp; b</p>");
        string[] wants =
        [
            ViewerDocument.Csp, "<meta charset=\"utf-8\">", "<body><div id=\"malachi-column\"><p>a &amp; b</p></div></body>", "img { max-width: 100%; }",
            // The column of the headers (the message pane's clamps).
            "#malachi-column { display: block !important; box-sizing: border-box !important; width: min(100%, 900px) !important;",
        ];
        foreach (var want in wants)
        {
            Assert.True(doc.Contains(want, StringComparison.Ordinal), $"document lacks {want}");
        }
        Assert.StartsWith("<!DOCTYPE html>", doc, StringComparison.Ordinal);
        Assert.EndsWith("</body></html>", doc, StringComparison.Ordinal);
        Assert.Equal("default-src 'none'; img-src malachi-cid: data:; style-src 'unsafe-inline'", ViewerDocument.Csp);
        // Windows: the fixed title, before anything the body could carry.
        Assert.Contains("<head><meta charset=\"utf-8\"><title>Malachi Mail</title><meta http-equiv", doc, StringComparison.Ordinal);
    }

    [Fact]
    public void HostsOfText()
    {
        Assert.Equal("bank.example.org", Links.HostOfText("HTTPS://Bank.Example.org/x"));
        Assert.Equal("bank.example.org", Links.HostOfText("bank.example.org"));
        Assert.Equal("", Links.HostOfText("http://"));
        Assert.Equal("", Links.HostOfText("a b.example.org"));
        Assert.Equal("", Links.HostOfText("example.o"));
        Assert.Equal("", Links.HostOfText("192.168.0.1"));
        Assert.True(Links.LooksLikeHost("a-b.example"));
        Assert.False(Links.LooksLikeHost("example"));
        Assert.False(Links.LooksLikeHost("a..example"));
        Assert.False(Links.LooksLikeHost("a.example1"));
        // A label of dashes passes, as in Go: only the characters are checked.
        Assert.True(Links.LooksLikeHost("-.example"));
        Assert.True(Links.SameSite("www.example.org", "example.org"));
        Assert.True(Links.SameSite("a.b.example.org", "example.org"));
        Assert.False(Links.SameSite("notexample.org", "example.org"));
    }

    // Windows: the hosts net/url reads and System.Uri would not (or would
    // read otherwise). Where Go reads a host, IsMasked agrees with the GTK
    // UI; where url.Parse fails, GTK says false and Windows fails closed
    // (HostNotCertain). An IPv6 host is never the site of a text, so the
    // zone cases are masked whether Go reads them or not
    // (SignInTests.BrowserUrlZones checks those rules).
    [Theory]
    [InlineData("bank.example.org", "https://evil.example.net\\@bank.example.org/", true)] // url.Parse: a backslash is no separator, and no userinfo either
    [InlineData("evil.example.net", "https://bank.example.org@evil.example.net/", true)] // userinfo before the last @ (masked whatever the host)
    [InlineData("evil.example.net", "https://evil.example.net:80:80/", true)] // only the last port is split off: the host is evil.example.net:80
    [InlineData("evil.example.net", "https://%65vil.example.net/", true)] // host escapes are only for bytes beyond ASCII
    [InlineData("bank.example.org", "https://ex%C3%A4mple.net/", true)] // a non-ASCII escape decodes
    [InlineData("evil.example.net", "  https://evil.example.net/  ", false)] // the href is trimmed
    [InlineData("bank.example.org", "  https://evil.example.net/  ", true)]
    [InlineData("evil.example.net", "//evil.example.net/", false)] // a network-path reference has a host
    [InlineData("bank.example.org", "//evil.example.net/", true)]
    [InlineData("evil.example.net", "https://evil.example.net/\x0001", true)] // a control byte fails the parse
    [InlineData("bank.example.org", "https://[fe80::1%25%41]/", true)] // a zone may escape a byte a host could carry
    [InlineData("bank.example.org", "https://[fe80::1%25%20x]/", true)] // or a space
    [InlineData("bank.example.org", "https://[fe80::1%25en%30]/", true)]
    [InlineData("bank.example.org", "https://[fe80::1%25%C3%A4]/", true)] // but no byte beyond ASCII
    [InlineData("bank.example.org", "https://[fe80::1%25%2F]/", true)] // nor one a host may not carry
    [InlineData("bank.example.org", "https://[fe80::%41%25x]/", true)] // before the zone, the host's rules
    [InlineData("bank.example.org", "https://[fe80::1%25en0]:%38/", true)] // and after it
    public void GoHostRules(string text, string href, bool want)
    {
        Assert.Equal(want, Links.IsMasked(text, href));
    }
}
