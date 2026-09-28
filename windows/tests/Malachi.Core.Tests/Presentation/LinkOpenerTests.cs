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
