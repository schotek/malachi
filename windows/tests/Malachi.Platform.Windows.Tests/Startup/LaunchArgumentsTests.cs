// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of LaunchArguments, the command line of MalachiMail.exe: GTK's
// --gapplication-service and "open" of ui/main.go (mailto: URIs, anything
// else logged and ignored) as --background and mailto:, and what COM adds
// when a click on a notification starts the app ("----AppNotificationActivated:"
// and "-Embedding", measured in APP-SPIKES §1.3), which is never a URI.

using Malachi.Platform.Windows.Startup;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Startup;

public sealed class LaunchArgumentsTests
{
    [Fact]
    public void APlainLaunch()
    {
        var a = LaunchArguments.Parse([]);
        Assert.False(a.Background);
        Assert.False(a.FromNotification);
        Assert.Empty(a.MailtoUris);
        Assert.Empty(a.Unrecognised);
    }

    [Fact]
    public void LaunchAtLoginStartsInTheBackground()
    {
        Assert.True(LaunchArguments.Parse(["--background"]).Background);
        Assert.True(LaunchArguments.Parse(["--BACKGROUND"]).Background);
    }

    [Fact]
    public void MailtoLinksAreKeptInOrderAndEverythingElseIsSetAside()
    {
        var a = LaunchArguments.Parse(["mailto:alice@example.com?subject=Hi%20there", "https://example.com/", "MAILTO:bob@example.com", "C:\\file.eml", ""]);
        Assert.Equal(["mailto:alice@example.com?subject=Hi%20there", "MAILTO:bob@example.com"], a.MailtoUris);
        Assert.Equal(["https://example.com/", "C:\\file.eml"], a.Unrecognised);
    }

    [Fact]
    public void TheArgumentsOfANotificationsComStartAreNotUris()
    {
        var a = LaunchArguments.Parse(["----AppNotificationActivated:", "-Embedding"]);
        Assert.True(a.FromNotification);
        Assert.False(a.Background);
        Assert.Empty(a.MailtoUris);
        Assert.Empty(a.Unrecognised);
        // As the LocalServer32 command line hands them over.
        var line = LaunchArguments.ParseCommandLine("\"C:\\Program Files\\Malachi Mail\\MalachiMail.exe\" ----AppNotificationActivated: -Embedding");
        Assert.True(line.FromNotification);
        Assert.False(line.Background);
        Assert.Empty(line.MailtoUris);
        Assert.Empty(line.Unrecognised);
    }

    [Fact]
    public void AWholeCommandLineSkipsTheProgram()
    {
        // The ProgID's shell\open\command: "<exe>" "%1".
        var a = LaunchArguments.ParseCommandLine("\"C:\\Program Files\\Malachi Mail\\MalachiMail.exe\" \"mailto:alice@example.com?subject=A \\\"quoted\\\" word\"");
        Assert.Equal(["mailto:alice@example.com?subject=A \"quoted\" word"], a.MailtoUris);
        Assert.Empty(a.Unrecognised);
        // The Run value: "<exe>" --background.
        Assert.True(LaunchArguments.ParseCommandLine("\"C:\\a\\MalachiMail.exe\" --background").Background);
        // A program path without quotes ends at its first space.
        Assert.Equal(["--x"], LaunchArguments.ParseCommandLine("C:\\a\\MalachiMail.exe --x").Unrecognised);
        Assert.Equal(["mailto:a@b"], LaunchArguments.ParseCommandLine("mailto:a@b", startsWithProgram: false).MailtoUris);
        var none = LaunchArguments.ParseCommandLine(null);
        Assert.False(none.Background || none.FromNotification);
        Assert.Empty(none.MailtoUris);
        Assert.Empty(none.Unrecognised);
    }

    [Fact]
    public void SplitFollowsTheRulesOfTheCRuntime()
    {
        Assert.Equal(["C:\\a b\\x.exe", "one", "two words", "back\\slash", "q\"uote"], LaunchArguments.Split("\"C:\\a b\\x.exe\" one \"two words\" back\\slash q\\\"uote"));
        Assert.Empty(LaunchArguments.Split(""));
        Assert.Empty(LaunchArguments.Split("   "));
        Assert.Empty(LaunchArguments.Split(null));
    }
}
