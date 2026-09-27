// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AttachmentsTests.swift (sweepOpenDir,
// openDirWrite); GTK: ui/internal/window/attachments_test.go
// (TestSweepOpenDir). What Swift checks with POSIX modes is checked here as
// the calls to the private-directory factory (the real one and its DACL are
// tested in Malachi.Platform.Windows.Tests); the cases of locked files,
// links and long names are new.

using System;
using System.IO;
using System.Linq;
using Malachi.Core.Platform;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Platform;

public sealed class OpenDirTests
{
    private static readonly DateTimeOffset Now = new(2026, 9, 27, 12, 0, 0, TimeSpan.Zero);

    [Fact]
    public void SweepOpenDir()
    {
        using var temp = new TemporaryDirectory();
        var dir = Path.Combine(temp.Path, "open");
        var old = Path.Combine(dir, "old");
        var fresh = Path.Combine(dir, "fresh");
        Directory.CreateDirectory(old);
        Directory.CreateDirectory(fresh);
        Directory.SetLastWriteTimeUtc(old, Now.UtcDateTime.AddHours(-2));
        Directory.SetLastWriteTimeUtc(fresh, Now.UtcDateTime.AddMinutes(-30));
        var open = new OpenDir(dir, new FakePrivateDirectories(), new FakeTimeProvider(Now));

        open.Sweep(TimeSpan.FromHours(1));

        Assert.False(Directory.Exists(old), "the old entry should be gone");
        Assert.True(Directory.Exists(fresh), "the fresh entry should stay");
        // No directory: no-op.
        new OpenDir(Path.Combine(dir, "missing"), new FakePrivateDirectories(), new FakeTimeProvider(Now)).Sweep(TimeSpan.FromHours(1));
    }

    [Fact]
    public void SweepDefaultsToOpenMaxAge()
    {
        using var temp = new TemporaryDirectory();
        var old = Path.Combine(temp.Path, "old");
        var fresh = Path.Combine(temp.Path, "fresh");
        Directory.CreateDirectory(old);
        Directory.CreateDirectory(fresh);
        Directory.SetLastWriteTimeUtc(old, Now.UtcDateTime - OpenDir.OpenMaxAge - TimeSpan.FromSeconds(1));
        Directory.SetLastWriteTimeUtc(fresh, Now.UtcDateTime - OpenDir.OpenMaxAge + TimeSpan.FromSeconds(1));

        new OpenDir(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now)).Sweep();

        Assert.Equal(TimeSpan.FromHours(1), OpenDir.OpenMaxAge);
        Assert.False(Directory.Exists(old));
        Assert.True(Directory.Exists(fresh));
    }

    /// <summary>
    /// The open directory writes each file exclusively into a private
    /// subdirectory (no Go counterpart; writeOpenFile is untested there).
    /// </summary>
    [Fact]
    public void OpenDirWrite()
    {
        using var temp = new TemporaryDirectory();
        var directories = new FakePrivateDirectories();
        var open = new OpenDir(Path.Combine(temp.Path, "open"), directories, new FakeTimeProvider(Now));

        var path = open.Write("a.txt", "hello"u8);

        Assert.Equal("a.txt", Path.GetFileName(path));
        Assert.Equal("hello"u8.ToArray(), File.ReadAllBytes(path));
        var sub = Path.GetDirectoryName(path)!;
        Assert.Equal(open.Path, Path.GetDirectoryName(sub));
        // The directory is made private first, then a new private
        // subdirectory for the file.
        Assert.Equal([open.Path], directories.Ensured);
        Assert.Equal([sub], directories.Created);
        Assert.Equal(10, Path.GetFileName(sub).Length);
        // A second write of the same name lands in another subdirectory.
        var again = open.Write("a.txt", []);
        Assert.NotEqual(path, again);
        Assert.NotEqual(sub, Path.GetDirectoryName(again));
        Assert.Empty(File.ReadAllBytes(again));
        Assert.Equal([open.Path, open.Path], directories.Ensured);

        open.RemoveAll();

        Assert.False(Directory.Exists(open.Path));
    }

    [Fact]
    public void WriteTriesAnotherSubdirectoryWhenOneIsTaken()
    {
        using var temp = new TemporaryDirectory();
        var directories = new FakePrivateDirectories { Collisions = 3 };
        var open = new OpenDir(temp.Path, directories, new FakeTimeProvider(Now));

        var path = open.Write("a.txt", "x"u8);

        Assert.True(File.Exists(path));
        Assert.Equal([Path.GetDirectoryName(path)!], directories.Created);
        // Three taken, one written.
        Assert.Equal(4, Directory.GetDirectories(temp.Path).Length);
    }

    [Fact]
    public void WriteGivesUpWhenEveryNameIsTaken()
    {
        using var temp = new TemporaryDirectory();
        var open = new OpenDir(temp.Path, new FakePrivateDirectories { Collisions = 100 }, new FakeTimeProvider(Now));

        Assert.Throws<IOException>(() => open.Write("a.txt", "x"u8));
    }

    [Fact]
    public void WriteFailsWhenTheDirectoryCannotBeMadePrivate()
    {
        using var temp = new TemporaryDirectory();
        var dir = Path.Combine(temp.Path, "open");
        var directories = new FakePrivateDirectories { EnsureFails = new IOException("a link, not a directory") };
        var open = new OpenDir(dir, directories, new FakeTimeProvider(Now));

        Assert.Throws<IOException>(() => open.Write("a.txt", "x"u8));
        Assert.False(Directory.Exists(dir));
        Assert.Empty(directories.Created);
    }

    [Fact]
    public void WriteLeavesNothingBehindWhenTheFileCannotBeCreated()
    {
        using var temp = new TemporaryDirectory();
        var directories = new BlockingDirectories("a.txt");
        var open = new OpenDir(temp.Path, directories, new FakeTimeProvider(Now));

        // Windows says access denied to a file in a directory's place.
        var e = Assert.ThrowsAny<Exception>(() => open.Write("a.txt", "x"u8));
        Assert.True(e is IOException or UnauthorizedAccessException, e.GetType().Name);
        Assert.Empty(Directory.EnumerateFileSystemEntries(temp.Path));
    }

    [Theory]
    [InlineData("con.txt", "_con.txt")]
    [InlineData("report.pdf:evil.exe", "report.pdf_evil.exe")]
    [InlineData("..\\..\\escape.txt", "escape.txt")]
    [InlineData("../../escape.txt", "escape.txt")]
    [InlineData("invoice.pdf. . .", "invoice.pdf")]
    [InlineData("", "attachment")]
    public void WriteSanitisesTheName(string name, string want)
    {
        using var temp = new TemporaryDirectory();
        var open = new OpenDir(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now));

        var path = open.Write(name, "x"u8);

        Assert.Equal(want, Path.GetFileName(path));
        Assert.Equal(open.Path, Path.GetDirectoryName(Path.GetDirectoryName(path)));
        Assert.True(File.Exists(path));
    }

    [Fact]
    public void WriteKeepsThePathWithinMaxPath()
    {
        using var temp = new TemporaryDirectory();
        var open = new OpenDir(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now));

        var path = open.Write(new string('a', 250) + ".pdf", "x"u8);

        Assert.True(path.Length <= 259 || Path.GetFileName(path).Length == WindowsFileNames.MinLength, $"{path.Length}");
        Assert.EndsWith(".pdf", path, StringComparison.Ordinal);
        Assert.True(File.Exists(path));
    }

    [Fact]
    public void WriteSweepsFirst()
    {
        using var temp = new TemporaryDirectory();
        var stale = Path.Combine(temp.Path, "stale");
        Directory.CreateDirectory(stale);
        File.WriteAllText(Path.Combine(stale, "old.txt"), "x");
        Directory.SetLastWriteTimeUtc(stale, Now.UtcDateTime.AddHours(-3));
        var open = new OpenDir(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now));

        open.Write("new.txt", "x"u8);

        Assert.False(Directory.Exists(stale));
    }

    [Fact]
    public void SweepRemovesReadOnlyFiles()
    {
        using var temp = new TemporaryDirectory();
        var entry = Path.Combine(temp.Path, "entry");
        Directory.CreateDirectory(entry);
        var file = Path.Combine(entry, "locked-by-flag.txt");
        File.WriteAllText(file, "x");
        File.SetAttributes(file, FileAttributes.ReadOnly);
        Directory.SetLastWriteTimeUtc(entry, Now.UtcDateTime.AddHours(-2));

        new OpenDir(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now)).Sweep();

        Assert.False(Directory.Exists(entry));
    }

    [Fact]
    public void SweepLeavesWhatAViewerHoldsOpen()
    {
        using var temp = new TemporaryDirectory();
        var held = Path.Combine(temp.Path, "held");
        var free = Path.Combine(temp.Path, "free");
        Directory.CreateDirectory(held);
        Directory.CreateDirectory(free);
        var file = Path.Combine(held, "open.docx");
        File.WriteAllText(file, "x");
        File.WriteAllText(Path.Combine(free, "closed.docx"), "x");
        Directory.SetLastWriteTimeUtc(held, Now.UtcDateTime.AddHours(-2));
        Directory.SetLastWriteTimeUtc(free, Now.UtcDateTime.AddHours(-2));
        var open = new OpenDir(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now));

        // A viewer that did not share delete, as most do not.
        using (new FileStream(file, FileMode.Open, FileAccess.Read, FileShare.Read))
        {
            open.Sweep();
            open.RemoveAll();
            if (OperatingSystem.IsWindows())
            {
                Assert.True(File.Exists(file), "the file in use should stay");
            }
            Assert.False(Directory.Exists(free), "the rest should go");
        }

        // Closed, it goes with the next sweep.
        open.RemoveAll();
        Assert.False(Directory.Exists(open.Path));
    }

    [Fact]
    public void SweepRemovesALinkNotWhatItPointsTo()
    {
        using var temp = new TemporaryDirectory();
        var outside = Path.Combine(temp.Path, "outside");
        Directory.CreateDirectory(outside);
        var precious = Path.Combine(outside, "precious.txt");
        File.WriteAllText(precious, "keep");
        var dir = Path.Combine(temp.Path, "open");
        Directory.CreateDirectory(dir);
        var link = Path.Combine(dir, "link");
        CreateLinkOrSkip(link, outside);
        // Everything is older than a clock far ahead.
        var open = new OpenDir(dir, new FakePrivateDirectories(), new FakeTimeProvider(new DateTimeOffset(3000, 1, 1, 0, 0, 0, TimeSpan.Zero)));

        open.Sweep();

        Assert.False(Path.Exists(link), "the link should be gone");
        Assert.True(File.Exists(precious), "what it points to should stay");
    }

    [Fact]
    public void RemoveAllRemovesALinkInPlaceOfTheDirectoryNotWhatItPointsTo()
    {
        using var temp = new TemporaryDirectory();
        var outside = Path.Combine(temp.Path, "outside");
        Directory.CreateDirectory(Path.Combine(outside, "sub"));
        var precious = Path.Combine(outside, "sub", "precious.txt");
        File.WriteAllText(precious, "keep");
        var dir = Path.Combine(temp.Path, "open");
        CreateLinkOrSkip(dir, outside);
        var open = new OpenDir(dir, new FakePrivateDirectories(), new FakeTimeProvider(new DateTimeOffset(3000, 1, 1, 0, 0, 0, TimeSpan.Zero)));

        open.Sweep(); // not ours to sweep
        Assert.True(File.Exists(precious));
        open.RemoveAll();

        Assert.False(Path.Exists(dir), "the link should be gone");
        Assert.True(File.Exists(precious), "what it points to should stay");
    }

    [Fact]
    public void RemoveAllOfNothingIsANoOp()
    {
        using var temp = new TemporaryDirectory();
        var open = new OpenDir(Path.Combine(temp.Path, "missing"), new FakePrivateDirectories(), new FakeTimeProvider(Now));

        open.RemoveAll();
        open.Sweep();

        Assert.False(Directory.Exists(open.Path));
    }

    [Fact]
    public void TheDefaultIsInTheDataDirectory()
    {
        using var temp = new TemporaryDirectory();
        var open = OpenDir.InDataDirectory(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now));

        Assert.Equal(Path.Combine(temp.Path, "open"), open.Path);
        Assert.Equal(open.Path, new OpenDir(open.Path + Path.DirectorySeparatorChar, new FakePrivateDirectories(), TimeProvider.System).Path);
    }

    [Fact]
    public void ARelativePathIsRefused()
    {
        Assert.Throws<ArgumentException>(() => new OpenDir("open", new FakePrivateDirectories(), TimeProvider.System));
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

    // Creates the subdirectories, and in each a directory of the file's
    // name, so the file cannot be created.
    private sealed class BlockingDirectories(string blocked) : IPrivateDirectoryFactory
    {
        public void Ensure(string path) => Directory.CreateDirectory(path);

        public void CreateNew(string path)
        {
            Directory.CreateDirectory(path);
            Directory.CreateDirectory(Path.Combine(path, blocked));
        }
    }
}
