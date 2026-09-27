// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ActivationRequest (Core/Presentation): what an activation asks of the
// app, after ui/main.go (activate shows the main window, open hands
// mailto: URIs to compose alone, --gapplication-service starts with no
// window) and docs/windows-port.md §10 (the arguments COM adds for a
// notification's click are ignored).

using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class ActivationRequestTests
{
    private const string Exe = "\"C:\\Program Files\\Malachi Mail\\MalachiMail.exe\"";

    [Fact]
    public void APlainLaunchShowsTheMainWindow()
    {
        var r = ActivationRequest.FromCommandLine(ActivationKind.Launch, Exe, redirected: false);
        Assert.True(r.ShowMainWindow);
        Assert.False(r.StartHidden);
        Assert.Empty(r.MailtoUris);
        Assert.Empty(r.Ignored);
    }

    [Fact]
    public void AMailtoLaunchOpensOnlyTheComposer()
    {
        // GTK's open signal: a compose window, no main window (U4).
        var r = ActivationRequest.FromCommandLine(ActivationKind.Launch, Exe + " \"MAILTO:bob@example.com?subject=a%20b\"", redirected: false);
        Assert.False(r.ShowMainWindow);
        Assert.Equal(["MAILTO:bob@example.com?subject=a%20b"], r.MailtoUris);
    }

    [Fact]
    public void SeveralMailtoUrisKeepTheirOrder()
    {
        var r = ActivationRequest.FromArguments(ActivationKind.Launch, ["mailto:a@example.com", "mailto:b@example.com"], redirected: true);
        Assert.Equal(["mailto:a@example.com", "mailto:b@example.com"], r.MailtoUris);
        Assert.True(r.Redirected);
        Assert.False(r.ShowMainWindow);
    }

    [Fact]
    public void BackgroundStartsHiddenOnTheFirstLaunchOnly()
    {
        var first = ActivationRequest.FromCommandLine(ActivationKind.Launch, Exe + " --background", redirected: false);
        Assert.True(first.StartHidden);
        Assert.False(first.ShowMainWindow);

        // A second launch at login finds the app running: nothing to do.
        var second = ActivationRequest.FromCommandLine(ActivationKind.Launch, Exe + " --background", redirected: true);
        Assert.False(second.StartHidden);
        Assert.False(second.ShowMainWindow);
    }

    [Fact]
    public void ANotificationClickShowsTheMainWindowAndItsComArgumentsAreIgnored()
    {
        var r = ActivationRequest.FromCommandLine(ActivationKind.Notification, Exe + " ----AppNotificationActivated: -Embedding", redirected: false);
        Assert.True(r.ShowMainWindow);
        Assert.False(r.StartHidden);
        Assert.Empty(r.Ignored);
    }

    [Fact]
    public void TheComArgumentsAreIgnoredWhateverTheirCase()
    {
        var r = ActivationRequest.FromArguments(ActivationKind.Launch, ["----appnotificationactivated:", "-EMBEDDING"], redirected: false);
        Assert.True(r.ShowMainWindow);
        Assert.Empty(r.Ignored);
    }

    [Fact]
    public void UnknownArgumentsAreIgnoredAndTheWindowShows()
    {
        var r = ActivationRequest.FromCommandLine(ActivationKind.Launch, Exe + " https://example.com/ --verbose", redirected: true);
        Assert.Equal(["https://example.com/", "--verbose"], r.Ignored);
        Assert.True(r.ShowMainWindow);
    }

    [Fact]
    public void ArgumentsWithoutTheProgramLoseNothing()
    {
        var r = ActivationRequest.FromCommandLine(ActivationKind.Launch, "mailto:x@example.com", redirected: true);
        Assert.Equal(["mailto:x@example.com"], r.MailtoUris);
        var bare = ActivationRequest.FromCommandLine(ActivationKind.Launch, "--background", redirected: false);
        Assert.True(bare.StartHidden);
    }

    [Fact]
    public void AProtocolActivationIsALaunchWithItsUri()
    {
        var r = ActivationRequest.FromArguments(ActivationKind.Protocol, ["mailto:x@example.com"], redirected: true);
        Assert.Equal(ActivationKind.Protocol, r.Kind);
        Assert.False(r.ShowMainWindow);
        Assert.Single(r.MailtoUris);
    }

    [Fact]
    public void AnyOtherKindShowsTheMainWindow()
    {
        var r = ActivationRequest.FromArguments(ActivationKind.Other, [], redirected: true);
        Assert.True(r.ShowMainWindow);
    }

    [Fact]
    public void EmptyAndMissingCommandLinesAreAPlainLaunch()
    {
        Assert.True(ActivationRequest.FromCommandLine(ActivationKind.Launch, null, redirected: false).ShowMainWindow);
        Assert.True(ActivationRequest.FromCommandLine(ActivationKind.Launch, "", redirected: true).ShowMainWindow);
    }
}
