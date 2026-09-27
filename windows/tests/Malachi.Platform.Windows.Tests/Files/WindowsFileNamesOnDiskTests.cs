// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the names of Malachi.Core.Platform.WindowsFileNames written
// to NTFS. Written as they come from a message, each of these would be
// refused, changed, sent to a device or into a stream (measured with the
// daemon's safename on Windows 11: docs/windows-port.md §10); sanitised,
// each must be created under exactly that name, as a file of its own.

using System;
using System.IO;
using System.Linq;
using System.Text;
using Malachi.Core.Platform;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Files;

public sealed class WindowsFileNamesOnDiskTests
{
    public static readonly TheoryData<string> Hostile = new()
    {
        "a<b>.txt", "what?.txt", "star*.txt", "quote\".txt", "pipe|x.txt",
        "report.pdf:evil.exe", "a:b", "C:x.txt", "file::$DATA",
        "report.pdf.", "report.pdf. . .", "con.",
        "NUL", "nul.txt", "NUL .txt", "CON", "con.txt", "PRN", "AUX.doc", "COM1", "COM0", "LPT9.log",
        "COM¹.txt", "LPT³", "CONIN$", "CONOUT$", "CLOCK$",
        "desktop.ini", "thumbs.db",
        "..\\..\\evil.exe", "x\\..\\y.txt", "/etc/passwd",
        "photo‮gnp.exe", "\t.txt", "＂x＂.txt", "\uD800.txt",
        new string('a', 400), new string('ž', 300) + ".txt", "NUL" + new string(' ', 300) + "x.txt",
        "😀.png", "Jörg's Übersicht.ods",
    };

    [Theory]
    [MemberData(nameof(Hostile))]
    public void SanitizedNamesAreCreatedAsThemselves(string raw)
    {
        using var temp = new TestDirectory();
        var name = WindowsFileNames.Sanitize(raw);
        var path = Path.Combine(temp.Path, name);

        using (var stream = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.None))
        {
            stream.Write(Encoding.UTF8.GetBytes(raw));
        }

        // One entry, a file, named exactly so: no stream of another file,
        // no device, nothing Windows trimmed.
        var entries = Directory.GetFileSystemEntries(temp.Path);
        var entry = Assert.Single(entries);
        Assert.Equal(name, Path.GetFileName(entry));
        Assert.True(File.Exists(entry));
        Assert.Equal(Encoding.UTF8.GetBytes(raw), File.ReadAllBytes(path));
    }

    [Fact]
    public void UniqueNamesFollowTheFileSystem()
    {
        using var temp = new TestDirectory();
        var name = WindowsFileNames.Sanitize("Report.PDF");
        foreach (var expected in new[] { "Report.PDF", "Report (2).PDF", "Report (3).PDF" })
        {
            var unique = WindowsFileNames.UniqueName(name, n => File.Exists(Path.Combine(temp.Path, n)));
            Assert.Equal(expected, unique);
            File.WriteAllText(Path.Combine(temp.Path, unique), "x");
        }
        // NTFS compares without regard to case, and so does the lookup.
        Assert.Equal("report (4).pdf", WindowsFileNames.UniqueName("report.pdf", n => File.Exists(Path.Combine(temp.Path, n))));
        Assert.Equal(3, Directory.GetFiles(temp.Path).Length);
        Assert.All(Directory.GetFiles(temp.Path), f => Assert.StartsWith("Report", Path.GetFileName(f), StringComparison.Ordinal));
    }
}
