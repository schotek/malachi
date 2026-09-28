// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AttachmentsTests.swift (sweepOpenDir,
// openDirWrite, purgeOpenDir); GTK: ui/internal/window/attachments_test.go
// (TestSweepOpenDir, TestPurgeOpenDir, against the Windows rule of
// OpenDir.Purgeable). What Swift checks with POSIX modes is checked here as
// the calls to the private-directory factory (the real one and its DACL are
// tested in Malachi.Platform.Windows.Tests); the cases of locked files,
// links and long names are new. One part of the split of
// AttachmentsTests.swift (see DangerousTypesTests).

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using Malachi.Core.Daemon;
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
        var directories = new BlockingDirectories("secret-name.txt");
        var open = new OpenDir(temp.Path, directories, new FakeTimeProvider(Now));

        // Windows says access denied to a file in a directory's place.
        var e = Assert.ThrowsAny<Exception>(() => open.Write("secret-name.txt", "x"u8));
        Assert.True(e is IOException or UnauthorizedAccessException, e.GetType().Name);
        Assert.Empty(Directory.EnumerateFileSystemEntries(temp.Path));
        // The path carries the attachment's name: not in the error.
        Assert.DoesNotContain("secret-name", e.ToString(), StringComparison.Ordinal);
        Assert.NotEqual(0, e.HResult);
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

    [Theory]
    // Each "extension" is too long to keep, and the room under MAX_PATH
    // cuts the name right after a shorter one inside it (the review's
    // three, measured with a root of 58 characters).
    [InlineData(".settingcontent-ms", "Z")]
    [InlineData(".exe", "abcdefghijklmnopq")]
    [InlineData(".url", "_is_not_a_url_ext")]
    public void WriteNeverCutsANameToANewExtension(string inner, string rest)
    {
        using var temp = new TemporaryDirectory();
        var open = new OpenDir(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now));
        var kept = new string('x', open.MaxFileNameLength - inner.Length);
        var name = kept + inner + rest;
        Assert.False(DangerousTypes.IsDangerous(name, null), name);

        var path = open.Write(name, "x"u8);

        // The name that was written is the one to judge, and it is no
        // more dangerous than the one passed in.
        var written = Path.GetFileName(path);
        Assert.Equal(kept + "_" + inner[1..], written);
        Assert.False(DangerousTypes.IsDangerous(written, null), written);
        Assert.True(File.Exists(path));
        Assert.True(path.Length <= 259 || open.MaxFileNameLength == WindowsFileNames.MinLength, $"{path.Length}");
    }

    [Fact]
    public void WriteReplacesTheLookAlikesOfThisMachine()
    {
        using var temp = new TemporaryDirectory();
        var open = new OpenDir(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now), new HashSet<char> { '¥' });

        var path = open.Write("¥100 ‟x‟.pdf", "x"u8);

        Assert.Equal("_100 _x_.pdf", Path.GetFileName(path));
        Assert.Equal("¥100 _x_.pdf", Path.GetFileName(new OpenDir(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now)).Write("¥100 ‟x‟.pdf", "x"u8)));
    }

    [Fact]
    public void MaxFileNameLengthLeavesRoomUnderMaxPath()
    {
        using var temp = new TemporaryDirectory();
        var open = new OpenDir(temp.Path, new FakePrivateDirectories(), new FakeTimeProvider(Now));
        // The directory, a separator, ten random characters, a separator.
        Assert.Equal(259 - (open.Path.Length + 12), open.MaxFileNameLength);
        var root = Path.GetPathRoot(temp.Path)!;
        var shallow = new OpenDir(Path.Combine(root, "o"), new FakePrivateDirectories(), new FakeTimeProvider(Now));
        Assert.Equal(259 - (shallow.Path.Length + 12), shallow.MaxFileNameLength);
        // Deeper than MAX_PATH leaves room: the shortest cap, and long paths.
        var deep = new OpenDir(Path.Combine(temp.Path, new string('d', 250)), new FakePrivateDirectories(), new FakeTimeProvider(Now));
        Assert.Equal(WindowsFileNames.MinLength, deep.MaxFileNameLength);
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
        var dir = Path.Combine(temp.Path, "open");
        var held = Path.Combine(dir, "held");
        var free = Path.Combine(dir, "free");
        Directory.CreateDirectory(held);
        Directory.CreateDirectory(free);
        var file = Path.Combine(held, "open.docx");
        File.WriteAllText(file, "x");
        File.WriteAllText(Path.Combine(free, "closed.docx"), "x");
        Directory.SetLastWriteTimeUtc(held, Now.UtcDateTime.AddHours(-2));
        Directory.SetLastWriteTimeUtc(free, Now.UtcDateTime.AddHours(-2));
        var open = new OpenDir(dir, new FakePrivateDirectories(), new FakeTimeProvider(Now));

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
        var open = new OpenDir(Path.Combine(temp.Path, "open"), new FakePrivateDirectories(), new FakeTimeProvider(Now));

        open.RemoveAll();
        open.Sweep();

        Assert.False(Directory.Exists(open.Path));
    }

    /// <summary>
    /// attachments_test.go TestPurgeOpenDir (OpenDir.swift removeAll): the
    /// removal takes the open directory with everything in it, and refuses
    /// every other path without touching it. The Windows rule names no
    /// parent (see <see cref="OpenDir.Purgeable"/>): an <c>open</c> directly
    /// under a drive's or share's root is refused instead.
    /// </summary>
    [Fact]
    public void RemoveAllRefusesAnyOtherDirectory()
    {
        using var temp = new TemporaryDirectory();
        var data = Path.Combine(temp.Path, "Malachi Mail");
        var dir = Path.Combine(data, "open");
        var file = Path.Combine(dir, "x1", "report.pdf");
        var keep = Path.Combine(data, "keep");
        Directory.CreateDirectory(Path.GetDirectoryName(file)!);
        Directory.CreateDirectory(keep);
        File.WriteAllText(file, "%PDF-1.7");
        var root = Path.GetPathRoot(temp.Path)!;

        var refused = new List<string>
        {
            "", ".", "open", Path.Combine("Malachi Mail", "open"), // relative: the working directory's
            "/", @"\", // a root: on Windows the working directory's drive's
            root, Path.Combine(root, "open"), // a root, and an open directly under it
            temp.Path, data, keep, Path.Combine(dir, "x1"), Path.Combine(dir, ".."),
            Path.Combine(data, "Open"), Path.Combine(data, "opened"),
        };
        if (OperatingSystem.IsWindows())
        {
            refused.AddRange([
                @"\\?\" + dir, @"\\.\" + dir, "//?/" + dir.Replace('\\', '/'), // device paths
                @"\\server\share\open", // a share's root
                root.TrimEnd('\\') + "open", // C:open, relative to the drive's working directory
            ]);
        }
        foreach (var bad in refused)
        {
            Assert.True(OpenDir.Purgeable(bad) is null, $"Purgeable({bad}) should refuse");
        }
        foreach (var bad in new[] { temp.Path, data, keep, Path.Combine(dir, "x1"), Path.Combine(dir, "..") })
        {
            var other = new OpenDir(bad, new FakePrivateDirectories(), new FakeTimeProvider(Now));
            Assert.Throws<InvalidOperationException>(other.RemoveAll);
        }
        Assert.True(File.Exists(file), "a refused removal removed something");

        Assert.Equal(dir, OpenDir.Purgeable(dir + Path.DirectorySeparatorChar));
        Assert.Equal(dir, OpenDir.Purgeable(Path.Combine(dir, "x1", "..")));
        new OpenDir(dir + Path.DirectorySeparatorChar, new FakePrivateDirectories(), new FakeTimeProvider(Now)).RemoveAll();
        Assert.False(Directory.Exists(dir), "the open directory should be gone");
        Assert.True(Directory.Exists(keep), "its sibling should stay");
        new OpenDir(dir, new FakePrivateDirectories(), new FakeTimeProvider(Now)).RemoveAll(); // a missing directory: no error
        Assert.Equal(Path.Combine(temp.Path, "open"), OpenDir.Purgeable(Path.Combine(temp.Path, "open"))); // MALACHI_DATA_DIR's
    }

    /// <summary>
    /// The application's own directory passes the check (the end of the
    /// Swift purgeOpenDir test): <c>%LOCALAPPDATA%\Malachi Mail\open</c>,
    /// and the open directory of the data directory
    /// <c>MALACHI_DATA_DIR</c> names instead.
    /// </summary>
    [Fact]
    public void TheApplicationsOwnDirectoryPasses()
    {
        using var temp = new TemporaryDirectory();
        var local = Path.Combine(temp.Path, "Local");
        var agent = Path.Combine(temp.Path, "agent");
        var env = new Dictionary<string, string?> { ["USERPROFILE"] = temp.Path, ["LOCALAPPDATA"] = local };

        var own = OpenDir.InDataDirectory(Paths.Resolve(env, null).DataDir, new FakePrivateDirectories(), new FakeTimeProvider(Now));
        env["MALACHI_DATA_DIR"] = agent;
        var agents = OpenDir.InDataDirectory(Paths.Resolve(env, null).DataDir, new FakePrivateDirectories(), new FakeTimeProvider(Now));

        Assert.Equal(Path.Combine(local, "Malachi Mail", "open"), own.Path);
        Assert.Equal(own.Path, OpenDir.Purgeable(own.Path));
        Assert.Equal(Path.Combine(agent, "open"), agents.Path);
        Assert.Equal(agents.Path, OpenDir.Purgeable(agents.Path));
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
