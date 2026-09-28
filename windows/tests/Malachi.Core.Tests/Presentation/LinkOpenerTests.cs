// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of LinkOpener: ui/internal/window/remote.go (openLink, launchURI)
// and macos MessageActionsController.swift (openLink,
// openUnlistedLinkQuestion, openMailto, launch). The decision itself is
// LinkDecisionTests'; this checks what each decision does.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class LinkOpenerTests
{
    private static readonly IReadOnlyList<Link> Links =
    [
        new() { Text = "Our site", Href = "https://example.org/about" },
        new() { Text = "https://www.mojebanka.example/prihlaseni", Href = "https://mojebanka-overeni.example/login?id=8812" },
    ];

    private readonly FakeLauncher launcher = new();
    private readonly List<ComposeParams> composed = [];
    private readonly List<(object? Window, string Text, string Destination)> asked = [];
    private readonly List<string> toasts = [];
    private bool answer;

    private LinkOpener Make() => new(launcher)
    {
        Compose = composed.Add,
        Confirm = (window, c, destination) =>
        {
            asked.Add((window, c.Text, destination));
            return Task.FromResult(answer);
        },
        Toast = (_, text) => toasts.Add(text),
        Owner = _ => 42,
    };

    [Fact]
    public async Task AListedLinkOpensAtOnce()
    {
        await Make().OpenAsync(new ActivatedLink("https://example.org/about", "https://example.org/about"), Links, "w");
        Assert.Equal(["https://example.org/about"], launcher.Links);
        Assert.Empty(asked);
    }

    [Fact]
    public async Task AMaskedLinkIsAskedAboutWithItsTextAndOpenedOnlyOnYes()
    {
        var link = new ActivatedLink("https://mojebanka-overeni.example/login?id=8812", "https://mojebanka-overeni.example/login?id=8812");
        var opener = Make();
        await opener.OpenAsync(link, Links, "w");
        Assert.Equal([("w", "https://www.mojebanka.example/prihlaseni", "https://mojebanka-overeni.example/login?id=8812")], asked);
        Assert.Empty(launcher.Links);

        answer = true;
        await opener.OpenAsync(link, Links, "w");
        Assert.Equal(["https://mojebanka-overeni.example/login?id=8812"], launcher.Links);
    }

    [Fact]
    public async Task AnUnlistedLinkIsAskedAboutWithTheDestinationAlone()
    {
        answer = true;
        await Make().OpenAsync(new ActivatedLink("https://elsewhere.example/x", "https://elsewhere.example/x"), Links, null);
        Assert.Equal([((object?)null, "", "https://elsewhere.example/x")], asked);
        Assert.Equal(["https://elsewhere.example/x"], launcher.Links);
    }

    [Fact]
    public async Task TheQuestionNamesWhereTheBrowserGoes()
    {
        // The launcher's form of the address (here: the host lower-cased) is
        // what the question shows.
        await Make().OpenAsync(new ActivatedLink("https://ELSEWHERE.example/x", "https://ELSEWHERE.example/x"), Links, null);
        Assert.Equal("https://elsewhere.example/x", asked[0].Destination);
    }

    // A listed link whose text names the site it opens opens at once: the
    // host of the address the browser gets is the text's (www. aside).
    [Fact]
    public async Task ALinkThatLeadsWhereItsTextSaysOpensAtOnce()
    {
        Link[] links = [new() { Text = "https://www.example.org/", Href = "https://example.org/x?y=1" }];
        await Make().OpenAsync(new ActivatedLink("https://example.org/x?y=1", "https://example.org/x?y=1"), links, "w");
        Assert.Equal(["https://example.org/x?y=1"], launcher.Links);
        Assert.Empty(asked);
    }

    /// <summary>The text the security audit's links wear.</summary>
    private const string Bank = "https://www.mojebanka.example/login";

    /// <summary>
    /// The security audit's bypass (F3 §1): an href whose userinfo Go's
    /// parser refuses (a space, "%", "[", a soft hyphen, "。", "^", "|", "{",
    /// a quote, a backslash before the "@"), one it accepts, and an empty
    /// authority, each under a text that names the bank; beside it what the
    /// question names and a yes opens: the host after the "@", without the
    /// userinfo, or nothing where the launcher refuses the href.
    /// </summary>
    public static readonly TheoryData<string, string?> Bypasses = new()
    {
        { "https:// www.mojebanka.example@evil.example/space", "https://evil.example/space" },
        { "https://%www.mojebanka.example@evil.example/pct", "https://evil.example/pct" },
        { "https://[www.mojebanka.example@evil.example/bracket", "https://evil.example/bracket" },
        { "https://­www.mojebanka.example@evil.example/shy", "https://evil.example/shy" },
        { "https://。www.mojebanka.example@evil.example/ideo", "https://evil.example/ideo" },
        { "https://www.mojebanka.example^@evil.example/caret", "https://evil.example/caret" },
        { "https://www.mojebanka.example|@evil.example/pipe", "https://evil.example/pipe" },
        { "https://www.mojebanka.example{x}@evil.example/brace", "https://evil.example/brace" },
        { "https://www.mojebanka.example\"@evil.example/quote", "https://evil.example/quote" },
        { "https://www.mojebanka.example\\@evil.example/bs2", null },
        { "https://www.mojebanka.example@evil.example/plainuserinfo", "https://evil.example/plainuserinfo" },
        { "https:///evil.example/triple", null },
        // Userinfo over the bank itself is asked about all the same.
        { "https://www.mojebanka.example@www.mojebanka.example/login", "https://www.mojebanka.example/login" },
    };

    [Theory]
    [MemberData(nameof(Bypasses))]
    public async Task AnHrefWithUserinfoOrWithoutACertainHostIsAskedAbout(string href, string? destination)
    {
        Link[] links = [new() { Text = Bank, Href = href }];
        var link = new ActivatedLink(href, href);
        var opener = Make();
        await opener.OpenAsync(link, links, "w");
        Assert.Empty(launcher.Links);

        answer = true;
        await opener.OpenAsync(link, links, "w");
        Assert.Empty(toasts);
        if (destination is null)
        {
            // Not offered: nothing to ask about, nothing opened.
            Assert.Empty(asked);
            Assert.Empty(launcher.Links);
            return;
        }
        Assert.Equal([("w", Bank, destination), ("w", Bank, destination)], asked);
        Assert.Equal([destination], launcher.Links);
    }

    // One href under two texts, the bank's second: the click is asked about
    // with the bank's text, whichever anchor it came from.
    [Fact]
    public async Task AnHrefListedUnderTwoTextsIsAskedAboutWhenEitherMisleads()
    {
        const string Href = "https://evil.example/dup";
        Link[] links = [new() { Text = "", Href = Href }, new() { Text = Bank, Href = Href }];
        await Make().OpenAsync(new ActivatedLink(Href, Href), links, "w");
        Assert.Equal([("w", Bank, Href)], asked);
        Assert.Empty(launcher.Links);
    }

    // A text that reads as the bank's address only once its invisible
    // characters are gone, or with a space inside its host, is asked about
    // with the text as the mail wrote it (the question isolates it).
    [Theory]
    [InlineData("https://www.moje\u00ADbanka.example/login")]
    [InlineData("https://www.moje banka .example/login")]
    [InlineData("https://www.mojebanka.example@evil.example/login")]
    public async Task ATextThatReadsAsTheBankIsAskedAboutAsWritten(string text)
    {
        const string Href = "https://evil.example/t";
        Link[] links = [new() { Text = text, Href = Href }];
        await Make().OpenAsync(new ActivatedLink(Href, Href), links, "w");
        Assert.Equal([("w", text, Href)], asked);
        Assert.Empty(launcher.Links);
    }

    // Without the attribute, the URL WebView2 made of the link: it carries
    // the userinfo too, so it matches no listed href (no certain canonical
    // form) and is asked about with the destination alone, which is the
    // host after the "@".
    [Fact]
    public async Task WithoutTheAttributeTheQuestionNamesTheHostAfterTheAt()
    {
        Link[] links = [new() { Text = Bank, Href = "https:// www.mojebanka.example@evil.example/space" }];
        answer = true;
        await Make().OpenAsync(new ActivatedLink(null, "https://%20www.mojebanka.example@evil.example/space"), links, "w");
        Assert.Equal([("w", "", "https://evil.example/space")], asked);
        Assert.Equal(["https://evil.example/space"], launcher.Links);
    }

    // The empty authority as Chromium resolves it: its canonical form is the
    // listed href's, which Go reads without a host, so it is asked about
    // with the text it wore.
    [Fact]
    public async Task AnEmptyAuthorityResolvedByTheViewIsAskedAbout()
    {
        const string Text = "https://www.mojebanka.example/triple";
        Link[] links = [new() { Text = Text, Href = "https:///evil.example/triple" }];
        await Make().OpenAsync(new ActivatedLink(null, "https://evil.example/triple"), links, "w");
        Assert.Equal([("w", Text, "https://evil.example/triple")], asked);
        Assert.Empty(launcher.Links);
    }

    [Fact]
    public async Task MailtoComposesAndOtherSchemesDoNothing()
    {
        var opener = Make();
        await opener.OpenAsync(new ActivatedLink("mailto:bob@example.invalid?subject=Hi", "mailto:bob@example.invalid?subject=Hi"), Links, null);
        var p = Assert.Single(composed);
        Assert.Equal("bob@example.invalid", p.To[0].Email);
        Assert.Equal("Hi", p.Subject);

        await opener.OpenAsync(new ActivatedLink("javascript:alert(1)", "javascript:alert(1)"), Links, null);
        await opener.OpenAsync(new ActivatedLink("file:///C:/x", "file:///C:/x"), Links, null);
        Assert.Empty(launcher.Links);
        Assert.Empty(asked);
        Assert.Single(composed);
    }

    [Fact]
    public async Task AFailedLaunchIsAToastAndNoConfirmationHookOpensNothing()
    {
        launcher.Failure = new InvalidOperationException("no browser");
        var opener = Make();
        await opener.OpenAsync(new ActivatedLink("https://example.org/about", "https://example.org/about"), Links, null);
        Assert.Equal(["The link could not be opened: no browser"], toasts);

        launcher.Failure = null;
        opener.Confirm = null;
        await opener.OpenAsync(new ActivatedLink("https://elsewhere.example/", "https://elsewhere.example/"), Links, null);
        Assert.Empty(launcher.Links);
    }
}
