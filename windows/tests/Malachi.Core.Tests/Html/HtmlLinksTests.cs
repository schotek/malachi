// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/HTMLLinksTests.swift, the counterpart
// of ui/internal/htmlview/links_test.go. Its linkDecisions test is the test
// of Model/RemoteBar.swift (ActivatedLink, linkDecision), which is ported
// with the models (Malachi.Core.Model), not here.

using System;
using Malachi.Core.Html;
using Malachi.Core.Tests.Presentation;
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
        Assert.Equal(["bank.example.org"], Links.HostsOfText("HTTPS://Bank.Example.org/x"));
        Assert.Equal(["bank.example.org"], Links.HostsOfText("bank.example.org"));
        Assert.Empty(Links.HostsOfText("a b.example.org"));
        Assert.Empty(Links.HostsOfText("example.o"));
        Assert.Empty(Links.HostsOfText("192.168.0.1"));
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

    // Windows: what a reader takes a link's text to name, where GTK's
    // hostOfText reads "" and the link opens. Every host is ASCII (an
    // internationalised one in punycode), lower case, without its trailing
    // dot; "" is a host that cannot be read, which no link leads to.
    [Fact]
    public void HostsOfTextAsAReaderSeesThem()
    {
        // An address with no host, or one whose host cannot be read: spaces
        // in it (the daemon puts one around each inline element of a link's
        // text), userinfo, an escape, an IP address, a port that is none.
        // Where the host visibly stops at such a space, what comes before it
        // is the host (HostsOfTextSecondReview).
        Assert.Equal([""], Links.HostsOfText("http://"));
        Assert.Equal([""], Links.HostsOfText("https://www.moje banka.example/login"));
        Assert.Equal(["www.moje"], Links.HostsOfText("https://www.moje banka .example/login"));
        Assert.Equal([""], Links.HostsOfText("https://www.mojebanka.example@evil.example/login"));
        Assert.Equal([""], Links.HostsOfText("https://www.mojebanka.example%2Flogin"));
        Assert.Equal([""], Links.HostsOfText("https://192.168.0.1/"));
        Assert.Equal([""], Links.HostsOfText("https://[::1]/"));
        Assert.Equal([""], Links.HostsOfText("https://www.mojebanka.example:x/"));
        // Invisible characters go before the host is read; the dots and
        // slashes of other scripts and widths are read as dots and slashes;
        // a trailing dot is no part of the site.
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("\u202Ehttps://www.moje\u00AD\u200B\u2060\uFEFFbanka.example/login"));
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("https://www.mojebanka\u3002example\u3002/login"));
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("ＨＴＴＰＳ：／／ｗｗｗ．ｍｏｊｅｂａｎｋａ．ｅｘａｍｐｌｅ／ｌｏｇｉｎ"));
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("https:\u2215\u2215www.mojebanka.example\u2044login"));
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("https://www.mojebanka.example./login"));
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("https://www.mojebanka.example\u00A0/login"));
        // Another script's letters are read as the host they spell.
        Assert.Equal(["www.xn--mojbanka-e8g.example"], Links.HostsOfText("https://www.moj\u0435banka.example/login"));
        Assert.Equal(["xn--bcher-kva.example"], Links.HostsOfText("bücher.example"));
        Assert.Equal(["xn--e1afmkfd.xn--p1ai"], Links.HostsOfText("https://пример.рф/"));
        // An address after words, or after another address, is read too; it
        // ends at the next space. In a text that begins as an address, a
        // space ends the host only where the host does not visibly go on
        // after it (the daemon puts one around each inline element).
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("Log in at https://www.mojebanka.example/login"));
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("Login:https://www.mojebanka.example/login"));
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("Visit www.mojebanka.example for more"));
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("www.mojebanka.example for more"));
        Assert.Equal(["evil.example", "www.mojebanka.example"], Links.HostsOfText("https://evil.example/ https://www.mojebanka.example/login"));
        Assert.Equal(["evil.example", "www.mojebanka.example"], Links.HostsOfText("https://evil.example https://www.mojebanka.example/login"));
        // What encloses an address in prose is no part of it.
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("(https://www.mojebanka.example)"));
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("<https://www.mojebanka.example/login>"));
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("\u2800https://www.mojebanka.example/login"));
        // Plain words, an e-mail address and a mailto: text name no host.
        foreach (var plain in new[] { "", "Click here", "Unsubscribe", "Read more", "v1.2", "e.g.", "support@example.org", "mailto:a@example.org", "/login", "Bücher" })
        {
            Assert.Empty(Links.HostsOfText(plain));
        }
        // An internationalised top-level domain is a host's.
        Assert.True(Links.LooksLikeHost("xn--e1afmkfd.xn--p1ai"));
        Assert.False(Links.LooksLikeHost("example.xn--"));
    }

    // Windows, the second review: where an address begins, where the host
    // of one the text begins with ends, and what a one-word text names.
    [Fact]
    public void HostsOfTextSecondReview()
    {
        // Two slashes begin an address whatever stands before them (B1): a
        // colon a reader sees in a letter or a mark, or none; a colon
        // lookalike ends the scheme before a letter could swallow it.
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("httpsꓽ//mojebanka.example/login"));
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("httpsः//mojebanka.example/login"));
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("https//mojebanka.example/login"));
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("httpsː/mojebanka.example/login"));
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("httpsꓽmojebanka.example/login"));
        // A scheme address drawn right to left by markup: what follows its
        // slashes is no host (B2).
        Assert.Equal([""], Links.HostsOfText("nigol/elpmaxe.aknabejom//:sptth"));
        // In a text that begins with an address, a space ends the host
        // unless the host visibly goes on (S2): the next word begins with a
        // dot or has one before its first slash, or the word before the
        // space ends with one.
        Assert.Equal(["www.shop.example"], Links.HostsOfText("www.shop.example - shop now"));
        Assert.Equal(["www.shop.example"], Links.HostsOfText("https://www.shop.example Shop now"));
        Assert.Equal(["www.shop.example"], Links.HostsOfText("https://www.shop.example /sale"));
        Assert.Equal(["www.shop.example"], Links.HostsOfText("https:// www.shop.example"));
        Assert.Equal(["www.moje"], Links.HostsOfText("https://www.moje banka .example/login"));
        Assert.Equal([""], Links.HostsOfText("https://mojebanka .example/login"));
        Assert.Equal([""], Links.HostsOfText("https://moje banka.example/login"));
        Assert.Equal([""], Links.HostsOfText("https://evil.example mojebanka.example/login"));
        Assert.Equal([""], Links.HostsOfText("www. shop.example"));
        Assert.Equal([""], Links.HostsOfText("www. shop .example")); // www.<b>shop</b>.example: the rule's cost
        Assert.Equal(["www.shop.example"], Links.HostsOfText("www.shop.example 1.5.2026"));
        Assert.Equal(["www.shop.example"], Links.HostsOfText("www.shop.example …"));
        // A start of an address that those spaces split is read as drawn.
        Assert.Equal(["www.mojebanka.example"], Links.HostsOfText("w ww.mojebanka.example/login"));
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("https :/ /mojebanka.example/login"));
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("https/ /mojebanka.example/login"));
        Assert.Equal(["www.shop.example"], Links.HostsOfText("Visit w ww.shop.example today"));
        // A one-word text (N1): a dot another script draws, or more than one
        // at its end, names a host that cannot be read; after a word and a
        // colon, the host is what follows the colon; an underscore is a
        // label's; a mark before the text is no part of it.
        Assert.Equal([""], Links.HostsOfText("mojebankaꓸexample"));
        Assert.Equal([""], Links.HostsOfText("mojebanka۔example/login"));
        Assert.Equal([""], Links.HostsOfText("mojebanka.example…"));
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("mojebanka.example."));
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("Login:mojebanka.example/login"));
        Assert.Equal([""], Links.HostsOfText("mojebanka.example:evil.example"));
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("mojebanka.example:443/login"));
        Assert.Equal(["mojebanka.example"], Links.HostsOfText("́mojebanka.example"));
        Assert.Equal(["shop_name.example"], Links.HostsOfText("https://shop_name.example/"));
        Assert.True(Links.LooksLikeHost("my_shop.example"));
        Assert.False(Links.LooksLikeHost("shop.ex_ample"));
        // What stays plain: words with dots at their end, a middle dot, a
        // Katakana middle dot, digits of other scripts, a time, a version.
        foreach (var plain in new[] { "More...", "etc…", "Col·legi", "スター・ウォーズ", "٢٠٢٥", "v1.2...", "12:30", "Re:Offer", "e.g..", "W W W", "h t t p s" })
        {
            Assert.Empty(Links.HostsOfText(plain));
        }
    }

    // The limits of what a client can see (docs/security.md §3.2): the text
    // judged is the daemon's links[].text, not what the view draws. These
    // shapes still open without the question; each needs the daemon to
    // report what it sees (the backend note in docs/windows-port.md §6.4).
    [Theory]
    // A host without a scheme or "www." after words, or after a word CSS
    // hides (<span style="font-size:0">x</span>mojebanka.example/login).
    [InlineData("Log in at mojebanka.example")]
    [InlineData("x mojebanka.example/login")]
    // A bare host with a path that markup draws right to left
    // (<bdo dir=rtl>): shown as mojebanka.example/login.
    [InlineData("nigol/elpmaxe.aknabejom")]
    // A visible address clipped away (text-indent) behind the link's own.
    [InlineData("https://evil.example/xxxxxxxxxhttps://mojebanka.example/login")]
    // A host an inline element splits right after the link's own host
    // (https://evil.example<b>moje</b>banka.example, drawn as
    // https://evil.examplemojebanka.example): the space ends the host.
    [InlineData("https://evil.example moje banka.example/login")]
    // The daemon's 200-rune cap: hidden padding before the visible address.
    [InlineData("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")]
    public void TheKnownLimitsStillOpen(string text)
    {
        Assert.False(Links.IsMasked(text, "https://evil.example/t"));
        Assert.False(Links.LeadsElsewhere(text, "https://evil.example/t"));
    }

    /// <summary>
    /// The adversarial review's texts, each of which a reader takes for the
    /// bank's address and GTK's hostOfText does not read (so the link to
    /// evil.example opened without the question), and a few more of the
    /// same kind.
    /// </summary>
    public static readonly TheoryData<string> TextsOfTheBank = new()
    {
        "https://www.moje banka .example/login", // <b>banka</b> inside the text
        "https://www.mojebanka.example/ login",
        "https://www.moje\u00ADbanka.example/login", // soft hyphen
        "https://www.moje\u200Bbanka.example/login", // zero width space
        "https://www.moje\u2060banka.example/login", // word joiner
        "https://www.moje\uFEFFbanka.example/login", // byte order mark
        "https://www.mojebanka.example./login",
        "https://www.moj\u0435banka.example/login", // U+0435, a Cyrillic "e"
        "\u202Ehttps://www.mojebanka.example/login", // right-to-left override
        "https://www.mojebanka.example\u202E/login",
        "https://www.mojebanka.example\u00A0/login", // no-break space
        "https://www.mojebanka.example\u3002/login", // ideographic full stop
        "https://www.mojebanka\u3002example/login",
        "https://www.mojebanka.example\\login",
        "https://www.mojebanka.example／login", // fullwidth solidus
        "https://www.mojebanka.example%2Flogin",
        "//www.mojebanka.example/login",
        "https://www.mojebanka.example@evil.example/login",
        // More of the kind: other slashes and colons, fullwidth letters,
        // enclosing punctuation, an address after words or after another.
        "https:\u2215\u2215www.mojebanka.example/login",
        "https\u2236//www.mojebanka.example/login",
        "ＨＴＴＰＳ：／／ｗｗｗ．ｍｏｊｅｂａｎｋａ．ｅｘａｍｐｌｅ／ｌｏｇｉｎ",
        "<https://www.mojebanka.example/login>",
        "\u2800https://www.mojebanka.example/login",
        "Log in at https://www.mojebanka.example/login",
        "Login:https://www.mojebanka.example/login",
        "https://evil.example/ https://www.mojebanka.example/login",
        "https://evil.example https://www.mojebanka.example/login",
        "www.mojebanka.example:443/login",
        "https://[www.mojebanka.example]/login",
        "http://",
        // The second review's (B1): two slashes after a letter or a mark,
        // which a reader takes for a colon (letters and marks of other
        // scripts, a modifier letter the scheme swallowed), or after no
        // colon at all; none spells "www.", which alone was caught.
        "httpsː//mojebanka.example/login", // modifier letter triangular colon
        "httpsˑ//mojebanka.example/login", // modifier letter half triangular colon
        "httpsꓽ//mojebanka.example/login", // Lisu letter tone mya jeu
        "httpsः//mojebanka.example/login", // Devanagari sign visarga
        "httpsઃ//mojebanka.example/login", // Gujarati sign visarga
        "https//mojebanka.example/login",
        "HTTPS//MOJEBANKA.EXAMPLE",
        "Log in at httpsꓽ//mojebanka.example/login",
        "httpsː/mojebanka.example/login",
        "httpsꓽ//mojebanka .example/login",
        // A scheme address that markup draws right to left (<bdo dir=rtl>,
        // unicode-bidi: bidi-override): the daemon lists the text as
        // written, whose "host" after the slashes is ":sptth".
        "nigol/elpmaxe.aknabejom//:sptth",
        "nigol/elpmaxe.aknabejom.www//:sptth",
        // A one-word text (N1): a dot another script draws, more than one
        // dot at its end, a mark before it, a word and a colon before it.
        "mojebankaꓸexample", // Lisu letter tone mya ti
        "mojebanka۔example/login", // Arabic full stop
        "mojebanka܁example",
        "mojebanka.example…", // "..." in NFKC
        "mojebanka.example../login",
        "́mojebanka.example",
        "Login:mojebanka.example/login",
        "mojebanka.example:evil.example",
        "һttps:ⳆⳆmojebanka.example/login", // a Cyrillic "h", Coptic letters for the slashes
        // A host split by the daemon's spaces around inline elements, and a
        // hidden address before it (S2): the host goes on after the space.
        "https://mojebanka .example/login",
        "https://moje banka.example/login",
        "https://evil.example mojebanka.example/login",
        "www. mojebanka.example",
        // A start of an address split by those spaces: "<b>w</b>ww.",
        // "https<b>:/</b>/" (and the slashes of one without a colon).
        "w ww.mojebanka.example/login",
        "ww w.mojebanka.example/login",
        "www .mojebanka.example/login",
        "Log in at w ww.mojebanka.example",
        "https :/ /mojebanka.example/login",
        "h ttps://mojebanka.example/login",
        "/ /mojebanka.example/login",
        "https/ /mojebanka.example/login",
        "http s//mojebanka.example/login",
    };

    [Theory]
    [MemberData(nameof(TextsOfTheBank))]
    public void ATextThatReadsAsTheBankIsMasked(string text)
    {
        Assert.True(Links.IsMasked(text, "https://evil.example/t"));
        Assert.True(Links.LeadsElsewhere(text, "https://evil.example/t"));
    }

    // What must not ask: a text that names the site its link leads to,
    // however it is written, and a text that names no site at all.
    [Theory]
    [InlineData("www.mojebanka.example", "https://www.mojebanka.example/")]
    [InlineData("https://www.mojebanka.example/login", "https://www.mojebanka.example/login")]
    [InlineData("www.mojebanka.example/login", "https://www.mojebanka.example/login?x=1")]
    [InlineData("WWW.MOJEBANKA.EXAMPLE/LOGIN", "https://www.mojebanka.example/login")]
    [InlineData("https://www.mojebanka.example/login", "https://ib.mojebanka.example/x")]
    [InlineData("mojebanka.example", "https://www.mojebanka.example./")]
    [InlineData("https://www.mojebanka.example./login", "https://www.mojebanka.example/login")]
    [InlineData("https://www.mojebanka.example /login", "https://www.mojebanka.example/login")]
    [InlineData("https://www.moje\u00ADbanka.example/login", "https://www.mojebanka.example/login")]
    [InlineData("https://www.mojebanka.example:8443/x", "https://www.mojebanka.example/")]
    [InlineData("(https://www.mojebanka.example)", "https://www.mojebanka.example/")]
    [InlineData("Visit www.mojebanka.example", "https://www.mojebanka.example/")]
    [InlineData("https://bücher.example/", "https://xn--bcher-kva.example/")]
    [InlineData("https://BÜCHER.example/", "https://bücher.example/")]
    [InlineData("bücher.example", "https://xn--bcher-kva.example/x")]
    [InlineData("https://пример.рф/", "https://xn--e1afmkfd.xn--p1ai/")]
    [InlineData("Click here", "https://evil.example/")]
    [InlineData("Unsubscribe", "https://evil.example/")]
    [InlineData("", "https://evil.example/")]
    [InlineData("support@example.org", "https://evil.example/")]
    // The second review's (S2): a text that begins with an address and
    // goes on in words (an image's alt the daemon appends among them).
    [InlineData("www.shop.example for details", "https://www.shop.example/")]
    [InlineData("www.shop.example - shop now", "https://www.shop.example/")]
    [InlineData("https://www.shop.example Shop now", "https://www.shop.example/")]
    [InlineData("www.shop.example Logo", "https://www.shop.example/")]
    [InlineData("www.shop.cz ve slevě", "https://www.shop.cz/")]
    [InlineData("https://shop.example (valid until Sunday)", "https://shop.example/")]
    [InlineData("https://www.shop.example Shop now at www.shop.example", "https://www.shop.example/")]
    [InlineData("https:// www.shop.example", "https://www.shop.example/")]
    // An underscore in a host's label (N1).
    [InlineData("https://shop_name.example/", "https://shop_name.example/")]
    [InlineData("https://my_shop.shop.example/", "https://my_shop.shop.example/")]
    [InlineData("my_shop.example", "https://my_shop.example/")]
    // A one-word text whose colon is a word's, not a port's.
    [InlineData("Web:shop.example", "https://www.shop.example/")]
    public void ATextThatNamesItsOwnSiteIsNotMasked(string text, string href)
    {
        Assert.False(Links.IsMasked(text, href));
        Assert.False(Links.LeadsElsewhere(text, FakeLauncher.Target(href)));
    }

    // A single-label host (a top-level domain, an intranet name) is no site
    // of the hosts under it, "www." aside; a trailing dot is no part of a
    // site. Multi-label public suffixes are not known (no public-suffix
    // list): "co.uk" still counts as a parent site of "bank.co.uk".
    [Fact]
    public void SameSiteRules()
    {
        Assert.False(Links.SameSite("www.mojebanka.cz", "cz"));
        Assert.False(Links.SameSite("mojebanka.cz", "cz."));
        Assert.False(Links.SameSite("www.bank.example", "www.example"));
        Assert.False(Links.SameSite("www.example", "bank.example"));
        Assert.True(Links.SameSite("www.cz", "cz"));
        Assert.True(Links.SameSite("www.mojebanka.example.", "www.mojebanka.example"));
        Assert.True(Links.SameSite("mojebanka.example", "ib.mojebanka.example."));
        Assert.False(Links.SameSite("", ""));
        Assert.True(Links.SameSite("bank.co.uk", "co.uk")); // the known limit
        Assert.True(Links.IsMasked("www.mojebanka.cz", "https://cz/"));
        Assert.True(Links.IsMasked("https://www.mojebanka.cz/login", "https://cz./"));
        Assert.True(Links.LeadsElsewhere("www.mojebanka.cz", "https://cz/"));
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
