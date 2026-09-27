// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/HTMLLinksTests.swift (linkDecisions),
// the decision of ui/internal/window/remote.go (openLink), which GTK does
// not test. The Windows cases are new: an activated link without its
// attribute (ActivatedLink.Raw null) compared after Chromium's
// canonicalisation (ChromiumUrl), and the canonicaliser itself. The
// helpers of HTMLLinksTests.swift (AllowedLink, IsMasked, the hosts) are
// tested with Malachi.Core.Html.Links.

using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class LinkDecisionTests
{
    private static readonly Link[] Masked = [Link("https://bank.example", "https://evil.example")];
    private static readonly Link[] Plain = [Link("click here", "https://evil.example")];

    // The href a click reports is the attribute as written, which is what
    // the daemon lists, so it must be matched, not the normalised URL.
    [Fact]
    public void LinkDecisions()
    {
        // The attribute lacks the slash the engine adds: still matched by Raw.
        var noSlash = new ActivatedLink("https://evil.example", "https://evil.example/");
        Assert.Equal("https://evil.example", noSlash.Href);
        Assert.Equal(new LinkDecision.Confirm("https://bank.example", "https://evil.example"), LinkDecision.For(noSlash.Href, Masked));
        Assert.Equal(new LinkDecision.Open("https://evil.example"), LinkDecision.For(noSlash.Href, Plain));

        // Only the navigation saw it: the resolved URL is unlisted.
        var policyOnly = new ActivatedLink(null, "https://evil.example/");
        Assert.Equal("https://evil.example/", policyOnly.Href);
        Assert.Equal(new LinkDecision.Confirm("", "https://evil.example/"), LinkDecision.For(policyOnly.Href, Masked));
        Assert.Equal(new LinkDecision.Confirm("", "https://evil.example/"), LinkDecision.For(policyOnly.Href, Plain));

        // Upper-case host, which the engine lower-cases.
        Link[] upper = [Link("https://bank.example", "https://EVIL.example/x")];
        var upperLink = new ActivatedLink("https://EVIL.example/x", "https://evil.example/x");
        Assert.Equal(new LinkDecision.Confirm("https://bank.example", "https://EVIL.example/x"), LinkDecision.For(upperLink.Href, upper));
        Assert.Equal(new LinkDecision.Confirm("", "https://evil.example/x"), LinkDecision.For(upperLink.Resolved, upper));

        // An IDN host, which the engine turns into punycode.
        Link[] idn = [Link("Bücher", "https://bücher.example/")];
        var idnLink = new ActivatedLink("https://bücher.example/", "https://xn--bcher-kva.example/");
        Assert.Equal(new LinkDecision.Open("https://bücher.example/"), LinkDecision.For(idnLink.Href, idn));
        Assert.Equal(new LinkDecision.Confirm("", "https://xn--bcher-kva.example/"), LinkDecision.For(idnLink.Resolved, idn));

        // Not on the list at all: confirmed, never opened silently.
        Assert.Equal(new LinkDecision.Confirm("", "https://other.example/"), LinkDecision.For("https://other.example/", Masked));
        Assert.Equal(new LinkDecision.Confirm("", "https://other.example/"), LinkDecision.For("https://other.example/", []));

        // mailto: goes to the composer, listed or not; the rest is refused.
        Assert.Equal(new LinkDecision.Mailto("mailto:a@example.org"), LinkDecision.For("mailto:a@example.org", []));
        Assert.Equal(new LinkDecision.Mailto("MAILTO:a@example.org"), LinkDecision.For("MAILTO:a@example.org", Masked));
    }

    [Theory]
    [InlineData("javascript:alert(1)")]
    [InlineData("JavaScript:alert(1)")]
    [InlineData("vbscript:msgbox")]
    [InlineData("data:text/html,<script>alert(1)</script>")]
    [InlineData("file:///C:/Windows/System32/calc.exe")]
    [InlineData("file://server/share/x.exe")]
    [InlineData("ftp://x/")]
    [InlineData("")]
    [InlineData("malachi-cid:a/b/1")]
    [InlineData("malachi-doc://viewer/1-x")]
    [InlineData(" https://evil.example")]
    [InlineData("/relative/path")]
    [InlineData("relative/path")]
    [InlineData("//evil.example/x")]
    [InlineData("#top")]
    [InlineData("?q=1")]
    [InlineData("ms-settings:privacy")]
    [InlineData("search-ms:query=x")]
    public void RefusedLinks(string href)
    {
        Assert.Equal(new LinkDecision.Refused(), LinkDecision.For(href, Masked));
        // Whether read as the attribute or reported as the navigation.
        Assert.Equal(new LinkDecision.Refused(), LinkDecision.For(new ActivatedLink(href, href), Masked));
        Assert.Equal(new LinkDecision.Refused(), LinkDecision.For(new ActivatedLink(null, href), Masked));
    }

    // A relative attribute resolves against the viewer's own document; the
    // resolved URL is no web address either.
    [Fact]
    public void RelativeAttributeIsRefused()
    {
        Assert.Equal(new LinkDecision.Refused(), LinkDecision.For(new ActivatedLink("x.html", "malachi-doc://viewer/x.html"), []));
        Assert.Equal(new LinkDecision.Refused(), LinkDecision.For(new ActivatedLink("x.html", "https://evil.example/x.html"), Plain));
    }

    // With the attribute, an ActivatedLink is decided as on macOS: by the
    // attribute, and the answer names it.
    [Fact]
    public void WithTheAttributeTheAttributeDecides()
    {
        Assert.Equal(new LinkDecision.Confirm("https://bank.example", "https://evil.example"),
            LinkDecision.For(new ActivatedLink("https://evil.example", "https://evil.example/"), Masked));
        Assert.Equal(new LinkDecision.Open("https://evil.example"),
            LinkDecision.For(new ActivatedLink("https://evil.example", "https://evil.example/"), Plain));
        Link[] idn = [Link("Bücher", "https://bücher.example/")];
        Assert.Equal(new LinkDecision.Open("https://bücher.example/"),
            LinkDecision.For(new ActivatedLink("https://bücher.example/", "https://xn--bcher-kva.example/"), idn));
        Assert.Equal(new LinkDecision.Confirm("", "https://unlisted.example/"),
            LinkDecision.For(new ActivatedLink("https://unlisted.example/", "https://unlisted.example/"), Plain));
        Assert.Equal(new LinkDecision.Mailto("mailto:a@example.org"),
            LinkDecision.For(new ActivatedLink("mailto:a@example.org", "mailto:a@example.org"), Masked));
    }

    // Without the attribute (Windows): the resolved URL is compared with the
    // canonical form of every listed href, and the answer names the
    // resolved URL.
    [Fact]
    public void WithoutTheAttributeCanonicalFormsAreCompared()
    {
        // The masked link is found, and confirmed with its text.
        Assert.Equal(new LinkDecision.Confirm("https://bank.example", "https://evil.example/"),
            LinkDecision.For(new ActivatedLink(null, "https://evil.example/"), Masked));
        // A plain link in a body without a masked one opens.
        Assert.Equal(new LinkDecision.Open("https://evil.example/"),
            LinkDecision.For(new ActivatedLink(null, "https://evil.example/"), Plain));
        // Upper case, a default port, dot segments, backslashes, spaces:
        // the listed href as written, the navigation as Chromium makes it.
        Link[] odd = [Link("click", "HTTPS://Evil.Example:443\\a\\..\\b c?q=a b")];
        Assert.Equal(new LinkDecision.Open("https://evil.example/b%20c?q=a%20b"),
            LinkDecision.For(new ActivatedLink(null, "https://evil.example/b%20c?q=a%20b"), odd));
        Link[] oddMasked = [Link("www.bank.example", "HTTPS://Evil.Example:443/a/../b c?q=a b")];
        Assert.Equal(new LinkDecision.Confirm("www.bank.example", "https://evil.example/b%20c?q=a%20b"),
            LinkDecision.For(new ActivatedLink(null, "https://evil.example/b%20c?q=a%20b"), oddMasked));
        // An internationalised host, listed in Unicode, reported in punycode.
        Link[] idn = [Link("Bücher", "https://bücher.example/")];
        Assert.Equal(new LinkDecision.Open("https://xn--bcher-kva.example/"),
            LinkDecision.For(new ActivatedLink(null, "https://xn--bcher-kva.example/"), idn));
        Link[] idnMasked = [Link("https://bank.example", "https://bücher.example/")];
        Assert.Equal(new LinkDecision.Confirm("https://bank.example", "https://xn--bcher-kva.example/"),
            LinkDecision.For(new ActivatedLink(null, "https://xn--bcher-kva.example/"), idnMasked));
        // Unlisted, or listed elsewhere: confirmed with no text.
        Assert.Equal(new LinkDecision.Confirm("", "https://other.example/"),
            LinkDecision.For(new ActivatedLink(null, "https://other.example/"), Plain));
        Assert.Equal(new LinkDecision.Confirm("", "https://evil.example/x"),
            LinkDecision.For(new ActivatedLink(null, "https://evil.example/x"), Plain));
        Assert.Equal(new LinkDecision.Confirm("", "https://evil.example/"),
            LinkDecision.For(new ActivatedLink(null, "https://evil.example/"), []));
        // mailto: still goes to the composer.
        Assert.Equal(new LinkDecision.Mailto("mailto:a@example.org"),
            LinkDecision.For(new ActivatedLink(null, "mailto:a@example.org"), Masked));
    }

    // In a body that carries a masked link, a click without its attribute
    // is never opened silently, even where it matches a plain link: the
    // masked one may have an href whose canonical form is not certain.
    [Fact]
    public void AMaskedLinkKeepsEveryClickWithoutTheAttributeConfirmed()
    {
        Link[] links =
        [
            Link("https://bank.example", "https://evil.example/{x}"),
            Link("click here", "https://evil.example/"),
        ];
        Assert.Equal(new LinkDecision.Confirm("", "https://evil.example/"),
            LinkDecision.For(new ActivatedLink(null, "https://evil.example/"), links));
        // With the attribute, the plain link is itself.
        Assert.Equal(new LinkDecision.Open("https://evil.example/"),
            LinkDecision.For(new ActivatedLink("https://evil.example/", "https://evil.example/"), links));
    }

    // An attribute that leads somewhere else than the navigation was read
    // from another element: the navigation decides.
    [Fact]
    public void AnAttributeOfAnotherLinkIsIgnored()
    {
        Link[] links =
        [
            Link("click here", "https://good.example/"),
            Link("https://bank.example", "https://evil.example/"),
        ];
        Assert.Equal(new LinkDecision.Confirm("https://bank.example", "https://evil.example/"),
            LinkDecision.For(new ActivatedLink("https://good.example/", "https://evil.example/"), links));
        // An attribute whose canonical form is not certain is trusted, as on
        // macOS.
        Link[] braces = [Link("click here", "https://good.example/{x}")];
        Assert.Equal(new LinkDecision.Open("https://good.example/{x}"),
            LinkDecision.For(new ActivatedLink("https://good.example/{x}", "https://good.example/%7Bx%7D"), braces));
    }

    [Fact]
    public void ConfirmTexts()
    {
        Assert.Equal("Open This Link?", LinkDecision.Confirm.Title);
        Assert.Equal("The link is shown as “https://bank.example” but leads to https://evil.example/.",
            new LinkDecision.Confirm("https://bank.example", "https://evil.example").Body("https://evil.example/"));
        Assert.Equal("This link leads to https://xn--bcher-kva.example/.",
            new LinkDecision.Confirm("", "https://bücher.example/").Body("https://xn--bcher-kva.example/"));
    }

    [Theory]
    // Scheme and host in lower case, the default port dropped, "/" for an
    // empty path.
    [InlineData("https://Example.COM", "https://example.com/")]
    [InlineData("HTTP://example.com:80/a", "http://example.com/a")]
    [InlineData("http://example.com:443/a", "http://example.com:443/a")]
    [InlineData("https://example.com:443", "https://example.com/")]
    [InlineData("https://example.com:0443/", "https://example.com/")]
    [InlineData("https://example.com:8443/", "https://example.com:8443/")]
    [InlineData("https://example.com./", "https://example.com./")]
    // Slashes and backslashes after the scheme, backslashes in the path.
    [InlineData("https:example.com", "https://example.com/")]
    [InlineData("https:///example.com/", "https://example.com/")]
    [InlineData("https:\\\\example.com\\a\\b", "https://example.com/a/b")]
    // What an HTML parser strips.
    [InlineData("  https://exa\tmple.com/a\n ", "https://example.com/a")]
    // Dot segments.
    [InlineData("https://example.com/a/./b/../c", "https://example.com/a/c")]
    [InlineData("https://example.com/a/..", "https://example.com/")]
    [InlineData("https://example.com/a/.", "https://example.com/a/")]
    [InlineData("https://example.com/../a", "https://example.com/a")]
    [InlineData("https://example.com/a//b/", "https://example.com/a//b/")]
    // Escapes in the path and the query.
    [InlineData("https://example.com/a b\"<>", "https://example.com/a%20b%22%3C%3E")]
    [InlineData("https://example.com/ž", "https://example.com/%C5%BE")]
    [InlineData("https://example.com/😀", "https://example.com/%F0%9F%98%80")]
    [InlineData("https://example.com/a'b(c)*;=:@!$&+,~", "https://example.com/a'b(c)*;=:@!$&+,~")]
    [InlineData("https://example.com/%2F%20", "https://example.com/%2F%20")]
    [InlineData("https://example.com/?q=a b&r=ž", "https://example.com/?q=a%20b&r=%C5%BE")]
    [InlineData("https://example.com?q=1", "https://example.com/?q=1")]
    [InlineData("https://example.com/?", "https://example.com/?")]
    [InlineData("https://example.com/?a=/b?c", "https://example.com/?a=/b?c")]
    [InlineData("https://example.com/#top", "https://example.com/#top")]
    [InlineData("https://example.com/#", "https://example.com/#")]
    // Hosts: punycode, dotted IPv4.
    [InlineData("https://bücher.example/", "https://xn--bcher-kva.example/")]
    [InlineData("https://BÜCHER.example/", "https://xn--bcher-kva.example/")]
    [InlineData("https://xn--bcher-kva.example/", "https://xn--bcher-kva.example/")]
    [InlineData("https://127.0.0.1/", "https://127.0.0.1/")]
    [InlineData("https://192.168.100.200:8080/x", "https://192.168.100.200:8080/x")]
    public void CanonicalizeTest(string input, string want)
    {
        Assert.Equal(want, ChromiumUrl.Canonicalize(input));
        // A canonical form is its own.
        Assert.Equal(want, ChromiumUrl.Canonicalize(want));
    }

    [Theory]
    // Not http or https.
    [InlineData(null)]
    [InlineData("")]
    [InlineData("mailto:a@example.org")]
    [InlineData("javascript:alert(1)")]
    [InlineData("data:text/html,x")]
    [InlineData("file:///C:/x")]
    [InlineData("ftp://example.com/")]
    [InlineData("/relative")]
    [InlineData("relative")]
    [InlineData("//example.com/")]
    [InlineData("https:")]
    [InlineData("https://")]
    [InlineData("https:///")]
    [InlineData("https://exa\u0001mple.com/")]
    // User information, IPv6, ports that are not certain.
    [InlineData("https://user@example.com/")]
    [InlineData("https://user:pw@example.com/")]
    [InlineData("https://bank.example@evil.example/")]
    [InlineData("https://[::1]/")]
    [InlineData("https://example.com:/")]
    [InlineData("https://example.com:65536/")]
    [InlineData("https://example.com:x/")]
    // Hosts whose canonical form is not certain.
    [InlineData("https://127.1/")]
    [InlineData("https://0x7f.0.0.1/")]
    [InlineData("https://2130706433/")]
    [InlineData("https://1.2.3.4./")]
    [InlineData("https://01.2.3.4/")]
    [InlineData("https://256.2.3.4/")]
    [InlineData("https://example.123/")]
    [InlineData("https://example.0x1f/")]
    [InlineData("https://a..b/")]
    [InlineData("https://exa_mple.com/")]
    [InlineData("https://exa%6Dple.com/")]
    [InlineData("https://faß.example/")]
    [InlineData("https://a\u200Db.example/")]
    // Escapes and characters engines have treated differently.
    [InlineData("https://example.com/%2f")]
    [InlineData("https://example.com/%41")]
    [InlineData("https://example.com/%2E%2E/x")]
    [InlineData("https://example.com/%zz")]
    [InlineData("https://example.com/100%")]
    [InlineData("https://example.com/{x}")]
    [InlineData("https://example.com/a|b")]
    [InlineData("https://example.com/a^b")]
    [InlineData("https://example.com/a`b")]
    [InlineData("https://example.com/[x]")]
    [InlineData("https://example.com/?a='b'")]
    [InlineData("https://example.com/?a={b}")]
    [InlineData("https://example.com/#a b")]
    [InlineData("https://example.com/#ž")]
    public void CanonicalizeRefusesWhatIsNotCertain(string? input)
    {
        Assert.Null(ChromiumUrl.Canonicalize(input));
    }

    // A lone surrogate (built here: test data would not carry it intact)
    // has no UTF-8 form to escape.
    [Fact]
    public void CanonicalizeRefusesALoneSurrogate()
    {
        Assert.Null(ChromiumUrl.Canonicalize("https://example.com/" + '\uD800'));
        Assert.Null(ChromiumUrl.Canonicalize("https://example.com/?q=" + '\uDC00' + "x"));
    }

    private static Link Link(string text, string href) => new() { Text = text, Href = href };
}
