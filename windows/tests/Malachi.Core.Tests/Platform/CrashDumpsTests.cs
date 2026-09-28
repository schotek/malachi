// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// CrashDumps (Core/Platform), Windows-only: the WebView2 Crashpad reports
// and attachments go, the database's own files stay, a link on the way is
// never followed, and what is in use stays for the next sweep. No GTK or
// Swift counterpart.

using System;
using System.IO;
using System.Linq;
using Malachi.Core.Platform;
using Xunit;

namespace Malachi.Core.Tests.Platform;

public sealed class CrashDumpsTests
{
    [Fact]
    public void TheReportsAndAttachmentsAreEmptiedAndTheDatabaseStays()
    {
        using var temp = new TemporaryDirectory();
        var crashpad = Crashpad(temp.Path);
        Write(Path.Combine(crashpad, "reports", "0b1c.dmp"));
        Write(Path.Combine(crashpad, "reports", "5e7f.dmp"));
        Write(Path.Combine(crashpad, "attachments", "0b1c", "log.txt"));
        Write(Path.Combine(crashpad, "metadata"));
        Write(Path.Combine(crashpad, "settings.dat"));
        Write(Path.Combine(temp.Path, "EBWebView", "Default", "Preferences"));

        Assert.Equal((3, 0), CrashDumps.Sweep(temp.Path));

        Assert.Empty(Directory.EnumerateFileSystemEntries(Path.Combine(crashpad, "reports")));
        Assert.Empty(Directory.EnumerateFileSystemEntries(Path.Combine(crashpad, "attachments")));
        Assert.True(File.Exists(Path.Combine(crashpad, "metadata")));
        Assert.True(File.Exists(Path.Combine(crashpad, "settings.dat")));
        Assert.True(File.Exists(Path.Combine(temp.Path, "EBWebView", "Default", "Preferences")));
        // A second sweep has nothing left to do.
        Assert.Equal((0, 0), CrashDumps.Sweep(temp.Path));
    }

    [Fact]
    public void NoDatabaseIsNothingToDo()
    {
        using var temp = new TemporaryDirectory();
        Assert.Equal((0, 0), CrashDumps.Sweep(temp.Path));
        Assert.Equal((0, 0), CrashDumps.Sweep(Path.Combine(temp.Path, "missing")));
        Directory.CreateDirectory(Crashpad(temp.Path));
        Assert.Equal((0, 0), CrashDumps.Sweep(temp.Path));
        Assert.Throws<ArgumentException>(() => CrashDumps.Sweep(""));
    }

    [Fact]
    public void AReadOnlyDumpGoesToo()
    {
        using var temp = new TemporaryDirectory();
        var dump = Path.Combine(Crashpad(temp.Path), "reports", "0b1c.dmp");
        Write(dump);
        File.SetAttributes(dump, FileAttributes.ReadOnly);

        Assert.Equal((1, 0), CrashDumps.Sweep(temp.Path));
        Assert.False(File.Exists(dump));
    }

    [Fact]
    public void ADumpInUseStaysForTheNextSweep()
    {
        using var temp = new TemporaryDirectory();
        var reports = Path.Combine(Crashpad(temp.Path), "reports");
        var busy = Path.Combine(reports, "busy.dmp");
        Write(busy);
        Write(Path.Combine(reports, "done.dmp"));
        using (new FileStream(busy, FileMode.Open, FileAccess.Read, FileShare.Read))
        {
            var (removed, failed) = CrashDumps.Sweep(temp.Path);
            if (OperatingSystem.IsWindows())
            {
                // Windows refuses to delete a file that is open without
                // FILE_SHARE_DELETE, as the handler's own writer holds it.
                Assert.Equal((1, 1), (removed, failed));
                Assert.True(File.Exists(busy));
            }
            else
            {
                Assert.Equal((2, 0), (removed, failed));
            }
        }
        Assert.Equal(OperatingSystem.IsWindows() ? (1, 0) : (0, 0), CrashDumps.Sweep(temp.Path));
        Assert.Empty(Directory.EnumerateFileSystemEntries(reports));
    }

    [Fact]
    public void ALinkedReportsFolderIsNotFollowed()
    {
        using var temp = new TemporaryDirectory();
        var outside = Path.Combine(temp.Path, "outside");
        Write(Path.Combine(outside, "precious.txt"));
        var crashpad = Crashpad(temp.Path);
        Directory.CreateDirectory(crashpad);
        CreateLinkOrSkip(Path.Combine(crashpad, "reports"), outside);
        Write(Path.Combine(crashpad, "attachments", "a", "log.txt"));

        Assert.Equal((1, 0), CrashDumps.Sweep(temp.Path));
        Assert.True(File.Exists(Path.Combine(outside, "precious.txt")));
    }

    [Fact]
    public void ALinkedDatabaseIsNotFollowed()
    {
        using var temp = new TemporaryDirectory();
        var outside = Path.Combine(temp.Path, "outside");
        Write(Path.Combine(outside, "Crashpad", "reports", "precious.txt"));
        var data = Path.Combine(temp.Path, "WebView2");
        Directory.CreateDirectory(data);
        CreateLinkOrSkip(Path.Combine(data, "EBWebView"), outside);

        Assert.Equal((0, 0), CrashDumps.Sweep(data));
        Assert.True(File.Exists(Path.Combine(outside, "Crashpad", "reports", "precious.txt")));
    }

    [Fact]
    public void ALinkInsideTheReportsGoesWithoutItsTarget()
    {
        using var temp = new TemporaryDirectory();
        var outside = Path.Combine(temp.Path, "outside");
        Write(Path.Combine(outside, "precious.txt"));
        var reports = Path.Combine(Crashpad(temp.Path), "reports");
        Directory.CreateDirectory(reports);
        CreateLinkOrSkip(Path.Combine(reports, "link"), outside);
        Write(Path.Combine(reports, "nested", "deeper", "0b1c.dmp"));
        CreateLinkOrSkip(Path.Combine(reports, "nested", "link"), outside);

        Assert.Equal((2, 0), CrashDumps.Sweep(temp.Path));
        Assert.Empty(Directory.EnumerateFileSystemEntries(reports));
        Assert.True(File.Exists(Path.Combine(outside, "precious.txt")));
    }

    private static string Crashpad(string userDataFolder) => Path.Combine(userDataFolder, "EBWebView", "Crashpad");

    private static void Write(string path)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        File.WriteAllText(path, "dump");
    }

    // A directory link where the platform lets the user make one (on
    // Windows it takes Developer Mode or the privilege), or a skip.
    private static void CreateLinkOrSkip(string link, string target)
    {
        try
        {
            Directory.CreateSymbolicLink(link, target);
        }
        catch (Exception e) when (e is UnauthorizedAccessException or IOException)
        {
            Assert.Skip("symbolic links are not allowed here: " + e.Message);
        }
        Assert.True((File.GetAttributes(link) & FileAttributes.ReparsePoint) != 0);
        Assert.True(Directory.EnumerateFileSystemEntries(link).Any());
    }
}
