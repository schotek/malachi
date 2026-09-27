// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests of the daemon's log file (RotatingLogFile): what GTK
// and macOS leave to the terminal or the journal is kept under logs\.

using System;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Core.Tests.Platform;
using Xunit;

namespace Malachi.Core.Tests.Daemon;

public sealed class RotatingLogFileTests
{
    [Fact]
    public void WritesLinesAsUtf8IntoANewDirectory()
    {
        using var temp = new TemporaryDirectory();
        var path = Path.Combine(temp.Path, "logs", "malachid.log");
        using (var log = new RotatingLogFile(path))
        {
            Assert.True(log.WriteLine("time=… level=INFO msg=\"starting\""));
            Assert.True(log.WriteLine("žluťoučký kůň"));
        }
        var bytes = File.ReadAllBytes(path);
        Assert.False(bytes.AsSpan().StartsWith(new byte[] { 0xEF, 0xBB, 0xBF }), "no byte order mark");
        Assert.Equal(
            "time=… level=INFO msg=\"starting\"" + Environment.NewLine + "žluťoučký kůň" + Environment.NewLine,
            Encoding.UTF8.GetString(bytes));
    }

    [Fact]
    public void AppendsToWhatIsThere()
    {
        using var temp = new TemporaryDirectory();
        var path = Path.Combine(temp.Path, "malachid.log");
        File.WriteAllText(path, "earlier" + Environment.NewLine);
        using (var log = new RotatingLogFile(path))
        {
            log.WriteLine("later");
        }
        Assert.Equal(["earlier", "later"], File.ReadAllLines(path));
    }

    [Fact]
    public void RotatesPastTheLimitAndKeepsTheNewest()
    {
        using var temp = new TemporaryDirectory();
        var path = Path.Combine(temp.Path, "malachid.log");
        var line = new string('x', 98);
        var size = line.Length + Environment.NewLine.Length;
        using (var log = new RotatingLogFile(path, maxBytes: size * 3, keep: 2))
        {
            for (var i = 0; i < 10; i++)
            {
                log.WriteLine(i.ToString(System.Globalization.CultureInfo.InvariantCulture) + line[1..]);
            }
        }
        // 10 lines of 3 per file: 9 in .2 .1 and the current, with the oldest gone.
        Assert.Equal(["9"], File.ReadAllLines(path).Select(l => l[..1]));
        Assert.Equal(["6", "7", "8"], File.ReadAllLines(path + ".1").Select(l => l[..1]));
        Assert.Equal(["3", "4", "5"], File.ReadAllLines(path + ".2").Select(l => l[..1]));
        Assert.False(File.Exists(path + ".3"));
    }

    [Fact]
    public void ALineLongerThanTheLimitIsStillWritten()
    {
        using var temp = new TemporaryDirectory();
        var path = Path.Combine(temp.Path, "malachid.log");
        using (var log = new RotatingLogFile(path, maxBytes: 10, keep: 0))
        {
            Assert.True(log.WriteLine(new string('a', 50)));
            Assert.True(log.WriteLine("b"));
        }
        Assert.Equal(["b"], File.ReadAllLines(path));
        Assert.False(File.Exists(path + ".1"));
    }

    [Fact]
    public void CanBeReadAndRotatedWhileSomebodyFollowsIt()
    {
        using var temp = new TemporaryDirectory();
        var path = Path.Combine(temp.Path, "malachid.log");
        using var log = new RotatingLogFile(path, maxBytes: 64, keep: 1);
        log.WriteLine("first");
        using (var reader = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete))
        {
            log.WriteLine(new string('y', 70));
            Assert.True(File.Exists(path + ".1"));
        }
        Assert.Equal(["first"], File.ReadAllLines(path + ".1"));
    }

    [Fact]
    public void AReaderThatHoldsTheFileDelaysTheRotationAndCostsNothing()
    {
        SkipUnlessSharingIsEnforced();
        using var temp = new TemporaryDirectory();
        var path = Path.Combine(temp.Path, "malachid.log");
        File.WriteAllLines(path + ".1", ["older 1"]);
        File.WriteAllLines(path + ".2", ["older 2"]);
        var big = new string('a', 70);
        using var log = new RotatingLogFile(path, maxBytes: 64, keep: 2);
        Assert.True(log.WriteLine("first"));
        // A reader without delete sharing: the file cannot be renamed.
        using (var reader = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite))
        {
            Assert.True(log.WriteLine(big), "a line past the limit is kept in the current file");
            Assert.True(log.WriteLine("next"));
        }
        Assert.Equal(["first", big, "next"], ReadLines(path));
        Assert.Equal(["older 1"], ReadLines(path + ".1"));
        Assert.Equal(["older 2"], ReadLines(path + ".2"));

        // Once the reader has gone, the next try goes through.
        Assert.True(log.WriteLine("last"));
        Assert.Equal(["last"], ReadLines(path));
        Assert.Equal(["first", big, "next"], ReadLines(path + ".1"));
        Assert.Equal(["older 1"], ReadLines(path + ".2"));
        Assert.False(File.Exists(path + ".3"));
        Assert.False(File.Exists(path + ".rotating"));
    }

    [Fact]
    public void AHeldFirstPredecessorKeepsTheCurrentFileGrowing()
    {
        SkipUnlessSharingIsEnforced();
        using var temp = new TemporaryDirectory();
        var path = Path.Combine(temp.Path, "malachid.log");
        File.WriteAllLines(path + ".1", ["older 1"]);
        using var log = new RotatingLogFile(path, maxBytes: 64, keep: 2);
        Assert.True(log.WriteLine("first"));
        using (var reader = new FileStream(path + ".1", FileMode.Open, FileAccess.Read, FileShare.ReadWrite))
        {
            Assert.True(log.WriteLine(new string('b', 70)));
        }
        Assert.Equal(["first", new string('b', 70)], ReadLines(path));
        Assert.Equal(["older 1"], ReadLines(path + ".1"));
        Assert.False(File.Exists(path + ".2"), "nothing moved while name.1 was held");
        Assert.False(File.Exists(path + ".rotating"));
    }

    [Fact]
    public void AHeldOlderFileStaysAndTheNewestStillMoveUp()
    {
        SkipUnlessSharingIsEnforced();
        using var temp = new TemporaryDirectory();
        var path = Path.Combine(temp.Path, "malachid.log");
        File.WriteAllLines(path + ".1", ["older 1"]);
        File.WriteAllLines(path + ".2", ["older 2"]);
        using var log = new RotatingLogFile(path, maxBytes: 64, keep: 2);
        Assert.True(log.WriteLine("first"));
        using (var reader = new FileStream(path + ".2", FileMode.Open, FileAccess.Read, FileShare.ReadWrite))
        {
            Assert.True(log.WriteLine(new string('c', 70)));
        }
        // name.1 could not move up into the held name.2, so it went.
        Assert.Equal([new string('c', 70)], ReadLines(path));
        Assert.Equal(["first"], ReadLines(path + ".1"));
        Assert.Equal(["older 2"], ReadLines(path + ".2"));
    }

    [Fact]
    public void LinesAfterDisposeAreDropped()
    {
        using var temp = new TemporaryDirectory();
        var path = Path.Combine(temp.Path, "malachid.log");
        var log = new RotatingLogFile(path);
        log.Dispose();
        Assert.False(log.WriteLine("late"));
        Assert.False(File.Exists(path));
    }

    [Fact]
    public async Task WritersOnSeveralThreadsKeepTheirLinesWhole()
    {
        using var temp = new TemporaryDirectory();
        var path = Path.Combine(temp.Path, "malachid.log");
        using (var log = new RotatingLogFile(path))
        {
            await Task.WhenAll(Enumerable.Range(0, 8).Select(t => Task.Run(() =>
            {
                for (var i = 0; i < 200; i++)
                {
                    log.WriteLine($"thread {t} line {i} " + new string('z', 40));
                }
            }, TestContext.Current.CancellationToken)));
        }
        var lines = File.ReadAllLines(path);
        Assert.Equal(1600, lines.Length);
        Assert.All(lines, l => Assert.Matches(@"^thread \d line \d+ z{40}$", l));
    }

    // Windows refuses to rename or delete a file somebody holds without
    // delete sharing; .NET elsewhere has no such lock.
    private static void SkipUnlessSharingIsEnforced() =>
        Assert.SkipUnless(OperatingSystem.IsWindows(), "only Windows keeps a file held without delete sharing where it is");

    // The lines of a file the log may still have open.
    private static string[] ReadLines(string path)
    {
        using var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
        using var reader = new StreamReader(stream, Encoding.UTF8);
        return reader.ReadToEnd().Split(Environment.NewLine, StringSplitOptions.RemoveEmptyEntries);
    }
}
