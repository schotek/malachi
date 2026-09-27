// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the launcher (Launch/Launcher.cs). The address rule is the
// port of ui/internal/signin/signin_test.go (TestBrowserURL) and
// macos/Tests/MalachiCoreTests/SignInTests.swift (the isBrowserURL cases),
// and the link rule of macos/Tests/MalachiCoreTests/HTMLLinksTests.swift
// (allowedLinks) without mailto:. Nothing here reaches the shell: the
// launcher runs over a stand-in that records what it would have launched,
// so no browser and no application opens.

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.IO;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Platform.Windows.Attachments;
using Malachi.Platform.Windows.Launch;
using Malachi.Platform.Windows.Tests.Files;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Launch;

public sealed class LauncherTests
{
    [Theory]
    // signin_test.go TestBrowserURL.
    [InlineData("https://accounts.google.com/o/oauth2/v2/auth?client_id=x&state=y", true)]
    [InlineData("https://login.microsoftonline.com/common/oauth2/v2.0/authorize?login_hint=a%40b.c", true)]
    [InlineData("HTTPS://accounts.google.com/", true)]
    [InlineData("http://accounts.google.com/", false)]
    [InlineData("file:///etc/passwd", false)]
    [InlineData("javascript:alert(1)", false)]
    [InlineData("https:opaque", false)]
    [InlineData("https:///path-only", false)]
    [InlineData("https://user:pass@accounts.google.com/", false)]
    [InlineData("", false)]
    [InlineData("accounts.google.com/o/oauth2", false)]
    [InlineData("https://[::1", false)]
    // SignInTests.swift.
    [InlineData("https://login.microsoftonline.com/common/oauth2/v2.0/authorize?x=1", true)]
    [InlineData("https:accounts.google.com", false)]
    [InlineData("https://", false)]
    [InlineData("https://user:pw@accounts.google.com/", false)]
    [InlineData("https://user@accounts.google.com/", false)]
    // Go takes these (and so does this: the browser gets them escaped).
    [InlineData("https://accounts.google.com/a b", true)]
    [InlineData("https://accounts.google.com/\"--new-window", true)]
    // Windows: nothing a parser could read otherwise.
    [InlineData("https://@accounts.google.com/", false)]
    [InlineData("https://accounts google.com/", false)]
    [InlineData("https://accounts.google.com\\@evil.example/", false)]
    [InlineData("https://accounts.google.com/a\\b", false)]
    [InlineData("https://accounts.google.com/\u0000", false)]
    [InlineData(" https://accounts.google.com/", false)]
    [InlineData("https://accounts.google.com:8443/x?y=1#z", true)]
    [InlineData(null, false)]
    public void BrowserUrlTest(string? url, bool want)
    {
        Assert.Equal(want, Launcher.IsBrowserUrl(url));
    }

    [Theory]
    [InlineData("https://x/", true)]
    [InlineData("HTTP://x", true)]
    [InlineData("http://example.com/a b?q=\"c\"", true)]
    [InlineData("https://user@example.com/", true)]
    [InlineData("mailto:a@b", false)]
    [InlineData("javascript:x", false)]
    [InlineData("ftp://x", false)]
    [InlineData("data:text/html,x", false)]
    [InlineData("", false)]
    [InlineData("malachi-cid:a/b/1", false)]
    [InlineData("cid:x", false)]
    [InlineData(" https://x/", false)]
    [InlineData("https:x", false)]
    [InlineData("https://", false)]
    [InlineData("https://x/\u0007", false)]
    [InlineData("file:///C:/Windows/System32/calc.exe", false)]
    [InlineData("\\\\server\\share\\x.pdf", false)]
    [InlineData(null, false)]
    public void WebLinkTest(string? url, bool want)
    {
        Assert.Equal(want, Launcher.IsWebLink(url));
    }

    [Fact]
    public async Task OpenUrlHandsTheBrowserTheEscapedAddress()
    {
        var shell = new StandInShell();

        Assert.True(await shell.Launcher.OpenUrlAsync("https://accounts.google.com/o/oauth2/v2/auth?client_id=x&state=y", 42, TestContext.Current.CancellationToken));

        var launch = Assert.Single(shell.Launches);
        Assert.Equal("https://accounts.google.com/o/oauth2/v2/auth?client_id=x&state=y", launch.Target);
        Assert.Equal(42, launch.Owner);
        Assert.False(launch.Dialogs);
    }

    [Fact]
    public async Task OpenUrlEscapesWhatCouldSplitACommandLine()
    {
        var shell = new StandInShell();

        await shell.Launcher.OpenUrlAsync("https://accounts.google.com/a b\"--new-window", 0, TestContext.Current.CancellationToken);

        Assert.Equal("https://accounts.google.com/a%20b%22--new-window", Assert.Single(shell.Launches).Target);
    }

    [Theory]
    [InlineData("http://accounts.google.com/")]
    [InlineData("file:///C:/Windows/System32/calc.exe")]
    [InlineData("C:\\Windows\\System32\\calc.exe")]
    [InlineData("calc.exe")]
    [InlineData("ms-settings:privacy")]
    [InlineData("search-ms:query=x&crumb=location:\\\\evil\\share")]
    [InlineData("https://user@accounts.google.com/")]
    public async Task OpenUrlRefusesEverythingElse(string url)
    {
        var shell = new StandInShell();

        await Assert.ThrowsAsync<ArgumentException>(() => shell.Launcher.OpenUrlAsync(url, 0, TestContext.Current.CancellationToken));

        Assert.Empty(shell.Launches);
    }

    [Fact]
    public async Task OpenLinkEscapesWhatCouldSplitACommandLine()
    {
        var shell = new StandInShell();

        await shell.Launcher.OpenLinkAsync("http://example.com/a b?q=\"c\"", 0, TestContext.Current.CancellationToken);

        var launch = Assert.Single(shell.Launches);
        Assert.Equal("http://example.com/a%20b?q=%22c%22", launch.Target);
        Assert.False(launch.Dialogs);
    }

    [Theory]
    [InlineData("mailto:a@b")]
    [InlineData("javascript:alert(1)")]
    [InlineData("file:///C:/x.pdf")]
    [InlineData("ms-appinstaller:?source=https://x/y.appinstaller")]
    public async Task OpenLinkRefusesOtherSchemes(string url)
    {
        var shell = new StandInShell();

        await Assert.ThrowsAsync<ArgumentException>(() => shell.Launcher.OpenLinkAsync(url, 0, TestContext.Current.CancellationToken));

        Assert.Empty(shell.Launches);
    }

    [Fact]
    public async Task OpenFileHandsTheShellTheFileWithItsDialogs()
    {
        using var temp = new TestDirectory();
        var path = temp.Combine("report.pdf");
        File.WriteAllText(path, "%PDF");
        var shell = new StandInShell();

        Assert.True(await shell.Launcher.OpenFileAsync(path, 7, TestContext.Current.CancellationToken));

        var launch = Assert.Single(shell.Launches);
        Assert.Equal(path, launch.Target);
        Assert.Equal(7, launch.Owner);
        Assert.True(launch.Dialogs);
    }

    [Theory]
    [InlineData("setup.exe")]
    [InlineData("shortcut.lnk")]
    [InlineData("connect.rdp")]
    [InlineData("image.iso")]
    [InlineData("script.ps1")]
    [InlineData("page.hta")]
    public async Task ProgramsAreNeverOpened(string name)
    {
        using var temp = new TestDirectory();
        var path = temp.Combine(name);
        File.WriteAllText(path, "x");
        var shell = new StandInShell();

        await Assert.ThrowsAsync<ArgumentException>(() => shell.Launcher.OpenFileAsync(path, 0, TestContext.Current.CancellationToken));
        await Assert.ThrowsAsync<ArgumentException>(() => shell.Launcher.OpenWithAsync(path, 0, TestContext.Current.CancellationToken));

        Assert.Empty(shell.Launches);
        Assert.Empty(shell.OpenedWith);
    }

    [Theory]
    [InlineData("relative.pdf")]
    [InlineData("\\\\server\\share\\report.pdf")]
    [InlineData("\\\\?\\C:\\report.pdf")]
    [InlineData("\\\\.\\C:\\report.pdf")]
    [InlineData("")]
    public async Task OnlyFilesOnADriveAreOpened(string path)
    {
        var shell = new StandInShell();

        await Assert.ThrowsAnyAsync<ArgumentException>(() => shell.Launcher.OpenFileAsync(path, 0, TestContext.Current.CancellationToken));

        Assert.Empty(shell.Launches);
    }

    [Fact]
    public async Task DirectoriesLinksAndMissingFilesAreNotOpened()
    {
        using var temp = new TestDirectory();
        var dir = temp.Combine("folder.pdf");
        Directory.CreateDirectory(dir);
        var target = temp.Combine("target");
        Directory.CreateDirectory(target);
        var junction = temp.Combine("junction.pdf");
        TestDirectory.CreateJunction(junction, target);
        var shell = new StandInShell();

        await Assert.ThrowsAsync<ArgumentException>(() => shell.Launcher.OpenFileAsync(dir, 0, TestContext.Current.CancellationToken));
        await Assert.ThrowsAsync<ArgumentException>(() => shell.Launcher.OpenFileAsync(junction, 0, TestContext.Current.CancellationToken));
        await Assert.ThrowsAsync<FileNotFoundException>(() => shell.Launcher.OpenFileAsync(temp.Combine("missing.pdf"), 0, TestContext.Current.CancellationToken));

        Assert.Empty(shell.Launches);
    }

    [Theory]
    [InlineData("report.pdf::$DATA")]
    [InlineData("report.pdf:hidden")]
    [InlineData("report.pdf:evil.exe")]
    public async Task StreamsAreNotOpened(string name)
    {
        using var temp = new TestDirectory();
        var path = temp.Combine("report.pdf");
        File.WriteAllText(path, "%PDF");
        File.WriteAllText(path + ":hidden", "x");
        var shell = new StandInShell();

        await Assert.ThrowsAsync<ArgumentException>(() => shell.Launcher.OpenFileAsync(temp.Combine(name), 0, TestContext.Current.CancellationToken));
        await Assert.ThrowsAsync<ArgumentException>(() => shell.Launcher.OpenWithAsync(temp.Combine(name), 0, TestContext.Current.CancellationToken));

        Assert.Empty(shell.Launches);
    }

    [Fact]
    public async Task OpenWithHandsTheDialogTheFile()
    {
        using var temp = new TestDirectory();
        var path = temp.Combine("notes.txt");
        File.WriteAllText(path, "x");
        var shell = new StandInShell();

        Assert.True(await shell.Launcher.OpenWithAsync(path, 9, TestContext.Current.CancellationToken));

        Assert.Equal(new (string, nint)[] { (path, 9) }, shell.OpenedWith);
        Assert.Empty(shell.Launches);
    }

    [Fact]
    public async Task ADismissedDialogIsFalse()
    {
        using var temp = new TestDirectory();
        var path = temp.Combine("notes.txt");
        File.WriteAllText(path, "x");
        var shell = new StandInShell { Answer = false };

        Assert.False(await shell.Launcher.OpenFileAsync(path, 0, TestContext.Current.CancellationToken));
        Assert.False(await shell.Launcher.OpenWithAsync(path, 0, TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task AFailureReachesTheCaller()
    {
        var shell = new StandInShell { Failure = new Win32Exception(2) };

        await Assert.ThrowsAsync<Win32Exception>(() => shell.Launcher.OpenUrlAsync("https://example.com/", 0, TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task TheShellRunsOnAnStaThread()
    {
        var shell = new StandInShell();

        await shell.Launcher.OpenUrlAsync("https://example.com/", 0, TestContext.Current.CancellationToken);

        Assert.Equal(ApartmentState.STA, shell.Apartment);
    }

    [Fact]
    public async Task ACancelledLaunchLaunchesNothing()
    {
        var shell = new StandInShell();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(
            () => shell.Launcher.OpenUrlAsync("https://example.com/", 0, new CancellationToken(canceled: true)));

        Assert.Empty(shell.Launches);
    }

    // The shell played: records what it was handed and answers as told.
    private sealed class StandInShell
    {
        public StandInShell()
        {
            Launcher = new Launcher(new FileTypePolicy(), Shell, OpenWith);
        }

        public Launcher Launcher { get; }

        public bool Answer { get; init; } = true;

        public Exception? Failure { get; init; }

        public ApartmentState? Apartment { get; private set; }

        public List<(string Target, nint Owner, bool Dialogs)> Launches { get; } = [];

        public List<(string Path, nint Owner)> OpenedWith { get; } = [];

        private bool Shell(string target, nint owner, bool dialogs)
        {
            Apartment = Thread.CurrentThread.GetApartmentState();
            if (Failure is not null)
            {
                throw Failure;
            }
            Launches.Add((target, owner, dialogs));
            return Answer;
        }

        private bool OpenWith(string path, nint owner)
        {
            OpenedWith.Add((path, owner));
            return Answer;
        }
    }
}
