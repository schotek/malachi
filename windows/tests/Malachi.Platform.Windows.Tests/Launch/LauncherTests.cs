// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the launcher (Launch/Launcher.cs). The address rule is the
// port of ui/internal/signin/signin_test.go (TestBrowserURL) and
// macos/Tests/MalachiCoreTests/SignInTests.swift (the isBrowserURL cases),
// and the link rule of macos/Tests/MalachiCoreTests/HTMLLinksTests.swift
// (allowedLinks) without mailto:; the reader's LinkOpener runs over it
// with the security audit's masked-link bypasses. Nothing here reaches the
// shell: the launcher runs over a stand-in that records what it would have
// launched, so no browser and no application opens.

using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.IO;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
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
    // What .NET drops or keeps in the host, and the browser maps after
    // the fact: the target says where the browser really goes.
    [InlineData("https://exa\u202Emple.com/", "https://example.com/")]
    [InlineData("https://example.com\u3002evil.com/", "https://example.com.evil.com/")]
    [InlineData("https://ex\u00ADample.com/", "https://example.com/")]
    [InlineData("https://ex\u200Bample.com/", "https://example.com/")]
    [InlineData("https://\u0430pple.com/", "https://xn--pple-43d.com/")]
    [InlineData("https://čeština.cz/a b", "https://xn--etina-gya30d.cz/a%20b")]
    // The rest escaped, never split.
    [InlineData("http://example.com/a b?q=\"c\"#f g", "http://example.com/a%20b?q=%22c%22#f%20g")]
    [InlineData("https://example.com/\u202Egnp.exe", "https://example.com/%E2%80%AEgnp.exe")]
    [InlineData("HTTPS://EXAMPLE.com:443/Path", "https://example.com/Path")]
    [InlineData("https://[::1]:8443/x?y#z", "https://[::1]:8443/x?y#z")]
    [InlineData("https://x/", "https://x/")]
    // Userinfo never reaches the browser: the host after the "@" is where
    // it goes, and the address shown in a question starts with it.
    [InlineData("https://user@example.com/", "https://example.com/")]
    [InlineData("https://paypal.com@evil.example/", "https://evil.example/")]
    [InlineData("https://user:pw@EVIL.example:8443/x?y#z", "https://evil.example:8443/x?y#z")]
    [InlineData("https://@example.com/a", "https://example.com/a")]
    [InlineData("https://:@example.com/a", "https://example.com/a")]
    [InlineData("https://www.mojebanka.example:443@evil.example/", "https://evil.example/")]
    // The security audit's userinfo that Go's parser refuses (F3 §1).
    [InlineData("https:// www.mojebanka.example@evil.example/space", "https://evil.example/space")]
    [InlineData("https://%www.mojebanka.example@evil.example/pct", "https://evil.example/pct")]
    [InlineData("https://[www.mojebanka.example@evil.example/bracket", "https://evil.example/bracket")]
    [InlineData("https://­www.mojebanka.example@evil.example/shy", "https://evil.example/shy")]
    [InlineData("https://。www.mojebanka.example@evil.example/ideo", "https://evil.example/ideo")]
    [InlineData("https://www.mojebanka.example^@evil.example/caret", "https://evil.example/caret")]
    [InlineData("https://www.mojebanka.example|@evil.example/pipe", "https://evil.example/pipe")]
    [InlineData("https://www.mojebanka.example{x}@evil.example/brace", "https://evil.example/brace")]
    [InlineData("https://www.mojebanka.example\"@evil.example/quote", "https://evil.example/quote")]
    [InlineData("https://www.mojebanka.example@evil.example/plainuserinfo", "https://evil.example/plainuserinfo")]
    // Refused.
    [InlineData("mailto:a@b", null)]
    [InlineData("javascript:alert(1)", null)]
    [InlineData("https:x", null)]
    [InlineData("", null)]
    [InlineData(null, null)]
    [InlineData("https://www.mojebanka.example\\@evil.example/bs2", null)]
    [InlineData("https:///evil.example/triple", null)]
    [InlineData("https://a@b@evil.example/x", null)]
    public async Task LinkTargetIsWhatTheBrowserGets(string? url, string? want)
    {
        var shell = new StandInShell();

        Assert.Equal(want, Launcher.WebLinkTarget(url));
        Assert.Equal(want, ((Malachi.Core.Platform.ILauncher)shell.Launcher).LinkTarget(url));
        Assert.Equal(want is not null, Launcher.IsWebLink(url));
        if (want is null)
        {
            return;
        }

        await shell.Launcher.OpenLinkAsync(url!, 0, TestContext.Current.CancellationToken);

        Assert.Equal(want, Assert.Single(shell.Launches).Target);
    }

    public static readonly TheoryData<string> HostileLinks = new()
    {
        "https://。/", "https://a。。b/", "https://xn--/", "https://xn--zz-zz/", "https://-a-.example/",
        "https://😀.example/", "https://ß.de/", "https://­/", "https://​​/", "https://[::1%25x]/",
        "https://a%2eb/", "https://" + new string('a', 300) + ".example/", "https://example.com:99999/",
        "https://ex ample.com/", "https://example.com/￿\uD800", "https://‮‮/", "http://[::ffff:127.0.0.1]/",
    };

    [Theory]
    [MemberData(nameof(HostileLinks))]
    public void HostileLinksGiveAnAsciiTargetOrNone(string url)
    {
        var target = Launcher.WebLinkTarget(url);

        Assert.True(target is null || target.All(c => c > ' ' && c < '\x7F' && c != '"'), target);
    }

    /// <summary>
    /// The security audit's bypass of the masked-link question (F3 §1), each
    /// href under a text that names the bank, with what the question names
    /// and a yes opens (null: the launcher refuses it and nothing is
    /// offered).
    /// </summary>
    public static readonly TheoryData<string, string?> MaskedLinkBypasses = new()
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
    };

    // The reader's LinkOpener over this launcher, as the app wires them:
    // every shape is asked about, the question names the address the shell
    // then gets, and the shell never gets one without a yes; one the
    // launcher refuses is a toast per click instead.
    [Theory]
    [MemberData(nameof(MaskedLinkBypasses))]
    public async Task MaskedLinksAreAskedAboutWithTheAddressTheShellGets(string href, string? destination)
    {
        const string Text = "https://www.mojebanka.example/login";
        var shell = new StandInShell();
        var asked = new List<(string Text, string Destination)>();
        var toasts = new List<string>();
        var answer = false;
        var opener = new LinkOpener(shell.Launcher)
        {
            Confirm = (_, c, d) =>
            {
                asked.Add((c.Text, d));
                return Task.FromResult(answer);
            },
            Toast = (_, t) => toasts.Add(t),
        };
        Link[] links = [new() { Text = Text, Href = href }];

        await opener.OpenAsync(new ActivatedLink(href, href), links, null);
        Assert.Empty(shell.Launches);
        answer = true;
        await opener.OpenAsync(new ActivatedLink(href, href), links, null);

        if (destination is null)
        {
            Assert.Empty(asked);
            Assert.Empty(shell.Launches);
            Assert.Equal(["The link could not be opened: invalid URL", "The link could not be opened: invalid URL"], toasts);
            return;
        }
        Assert.Empty(toasts);
        Assert.Equal([(Text, destination), (Text, destination)], asked);
        Assert.Equal(destination, Assert.Single(shell.Launches).Target);
    }

    [Fact]
    public async Task ASignInPageGoesToTheHostDnsGets()
    {
        var shell = new StandInShell();

        await shell.Launcher.OpenUrlAsync("https://login.example.com\u3002evil.com/authorize", 0, TestContext.Current.CancellationToken);

        Assert.Equal("https://login.example.com.evil.com/authorize", Assert.Single(shell.Launches).Target);
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
    public async Task FilesOnANetworkDriveAreNotOpened()
    {
        using var temp = new TestDirectory();
        var path = temp.Combine("report.pdf");
        File.WriteAllText(path, "%PDF");
        // As if the drive of the directory were mapped to a share (net use).
        var shell = new StandInShell { Drive = DriveType.Network };

        await Assert.ThrowsAsync<ArgumentException>(() => shell.Launcher.OpenFileAsync(path, 0, TestContext.Current.CancellationToken));
        await Assert.ThrowsAsync<ArgumentException>(() => shell.Launcher.OpenWithAsync(path, 0, TestContext.Current.CancellationToken));

        Assert.Equal([Path.GetPathRoot(path)!, Path.GetPathRoot(path)!], shell.Roots);
        Assert.Empty(shell.Launches);
        Assert.Empty(shell.OpenedWith);
    }

    [Fact]
    public void TheTemporaryDirectoryIsOnALocalDrive()
    {
        // The drive the real launcher asks about, for the tests above.
        using var temp = new TestDirectory();

        Assert.NotEqual(DriveType.Network, new DriveInfo(Path.GetPathRoot(temp.Path)!).DriveType);
    }

    [Fact]
    public async Task ErrorsNeverNameThePath()
    {
        using var temp = new TestDirectory();
        var shell = new StandInShell();

        var missing = await Assert.ThrowsAsync<FileNotFoundException>(
            () => shell.Launcher.OpenFileAsync(temp.Combine("secret-name.pdf"), 0, TestContext.Current.CancellationToken));
        var noDirectory = await Assert.ThrowsAsync<DirectoryNotFoundException>(
            () => shell.Launcher.OpenFileAsync(temp.Combine("secret-dir", "secret-name.pdf"), 0, TestContext.Current.CancellationToken));
        File.WriteAllText(temp.Combine("secret-name.exe"), "x");
        var program = await Assert.ThrowsAsync<ArgumentException>(
            () => shell.Launcher.OpenFileAsync(temp.Combine("secret-name.exe"), 0, TestContext.Current.CancellationToken));

        // The path carries the attachment's name, which is mail content.
        foreach (var e in new Exception[] { missing, noDirectory, program })
        {
            Assert.DoesNotContain("secret-", e.ToString(), StringComparison.Ordinal);
        }
        Assert.Null(missing.FileName);
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
            Launcher = new Launcher(new FileTypePolicy(), Shell, OpenWith, root =>
            {
                Roots.Add(root);
                return Drive;
            });
        }

        public Launcher Launcher { get; }

        public DriveType Drive { get; init; } = DriveType.Fixed;

        public List<string> Roots { get; } = [];

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
