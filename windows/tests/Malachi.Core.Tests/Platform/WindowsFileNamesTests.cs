// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AttachmentsTests.swift
// (safeFileNameTest, uniqueNameTest, fileNameTest), which carry
// backend/internal/safename's TestFilename and TestFilenameTruncates; GTK:
// ui/internal/window/attachments_test.go (TestUniqueName, TestFileName).
// The Windows cases (reserved characters, streams, trailing dots, device
// names, lengths) are new; Malachi.Platform.Windows.Tests writes them to
// disk.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Platform;
using Xunit;

namespace Malachi.Core.Tests.Platform;

public sealed class WindowsFileNamesTests
{
    // Hostile names: every rule at once, for the properties below.
    public static readonly TheoryData<string> Pathological = new()
    {
        "", " ", ".", "..", "...", ". .", "\u0000", "‮", "\uD800", "\uDC00x", "x\uD800.txt",
        "CON", "con.txt", "NUL .txt", "nul.tar.gz", "COM¹", "LPT³.log", "CONIN$", "clock$",
        "desktop.ini", "Thumbs.db", "_CON", "report.pdf.", "report.pdf. . .", "a<b>c:d\"e|f?g*h.txt",
        "report.pdf:evil.exe", "file::$DATA", "C:x.txt", "..\\..\\evil.exe", "/etc/passwd", "a/b\\c",
        "＂quoted＂.txt", "：／＼.txt", "ʺx″y〃.txt",
        "photo‮gnp.exe", "\t\r\n.txt", "   spaced.txt   ", " nbsp .txt ",
        new string('a', 400), new string('ž', 300) + ".txt", new string('b', 300) + "." + new string('c', 40),
        "." + new string('d', 300), "NUL" + new string(' ', 300) + "x.txt", "CON." + new string('x', 300),
        new string('á', 200) + "😀" + new string('y', 100) + ".pdf",
        "😀😀😀.png", "Jörg's Übersicht.ods", "a.b.c.d", "x.{3050F4D8-98B5-11CF-BB82-00AA00BDCE0B}",
        new string('x', 28) + ".exeabcdefghijklmnopq", new string('x', 96) + ".url_is_not_a_url_ext",
        new string('x', 14) + ".settingcontent-msZ", "invoice.exe." + new string('q', 300),
    };

    [Theory]
    [InlineData("report.pdf", "report.pdf")]
    [InlineData("../../x", "x")]
    [InlineData("..\\..\\x", "x")]
    [InlineData("/etc/passwd", "passwd")]
    [InlineData("a\u0000b\n.txt", "ab.txt")]
    [InlineData(".hidden", "hidden")]
    [InlineData("...", "attachment")]
    [InlineData("", "attachment")]
    [InlineData("   ", "attachment")]
    [InlineData("  spaced.txt  ", "spaced.txt")]
    [InlineData("Jörg's Übersicht.ods", "Jörg's Übersicht.ods")]
    [InlineData("bad�.txt", "bad.txt")]
    // Bidi controls would make the displayed extension lie.
    [InlineData("photo‮gnp.exe", "photognp.exe")]
    [InlineData("‫x‬⁦y⁩.txt", "xy.txt")]
    [InlineData("‎name‏؜.pdf", "name.pdf")]
    // The colon is a separator to the Finder, and a stream to Windows.
    [InlineData("photo:1.png", "photo_1.png")]
    [InlineData("a:b/c:d", "c_d")]
    [InlineData("\u001B[31mred.txt", "[31mred.txt")]
    public void SafeFileNameTest(string input, string want)
    {
        Assert.Equal(want, WindowsFileNames.Sanitize(input));
    }

    [Fact]
    public void SafeFileNameTruncatesTest()
    {
        var got = WindowsFileNames.Sanitize(new string('ž', 300) + ".txt");
        Assert.True(Encoding.UTF8.GetByteCount(got) <= WindowsFileNames.MaxBytes, $"truncated = {Encoding.UTF8.GetByteCount(got)} bytes");
        Assert.EndsWith(".txt", got, StringComparison.Ordinal);
        Assert.Contains('ž', got);
        Assert.Equal(WindowsFileNames.MaxBytes, Encoding.UTF8.GetByteCount(WindowsFileNames.Sanitize(new string('a', 400))));
        // A long "extension" is not one: cut like the rest.
        var dotted = new string('b', 300) + "." + new string('c', 40);
        Assert.Equal(WindowsFileNames.MaxBytes, Encoding.UTF8.GetByteCount(WindowsFileNames.Sanitize(dotted)));
        Assert.Equal(new string('d', 255), WindowsFileNames.Sanitize("." + new string('d', 300)));
    }

    [Theory]
    [InlineData("a.txt", "a (3).txt")]
    [InlineData("b", "b (2)")]
    [InlineData("c.tar.gz", "c.tar (2).gz")]
    [InlineData("new.txt", "new.txt")]
    public void UniqueNameTest(string input, string want)
    {
        HashSet<string> taken = ["a.txt", "a (2).txt", "b", "c.tar.gz"];
        Assert.Equal(want, WindowsFileNames.UniqueName(input, taken.Contains));
    }

    [Fact]
    public void UniqueNameTestGivesUp()
    {
        Assert.Equal("x", WindowsFileNames.UniqueName("x", _ => true));
    }

    [Theory]
    [InlineData("served.pdf", "served.pdf")]
    [InlineData("", "listed.pdf")]
    [InlineData(null, "listed.pdf")]
    [InlineData("../../etc/passwd", "passwd")]
    [InlineData("/", "listed.pdf")]
    [InlineData("...", "listed.pdf")]
    [InlineData("inv‮fdp.exe", "invfdp.exe")]
    [InlineData("a:b.txt", "a_b.txt")]
    public void FileNameTest(string? served, string want)
    {
        Assert.Equal(want, WindowsFileNames.FileName(served, "listed.pdf"));
    }

    [Fact]
    public void FileNameTestNamelessPart()
    {
        Assert.Equal("attachment", WindowsFileNames.FileName("", ""));
        Assert.Equal("attachment", WindowsFileNames.FileName(null, null));
    }

    [Theory]
    [InlineData("a<b>.txt", "a_b_.txt")]
    [InlineData("what?.txt", "what_.txt")]
    [InlineData("star*.txt", "star_.txt")]
    [InlineData("quote\".txt", "quote_.txt")]
    [InlineData("pipe|x.txt", "pipe_x.txt")]
    [InlineData("<>:\"|?*", "_______")]
    public void ReservedCharactersBecomeUnderscores(string input, string want)
    {
        Assert.Equal(want, WindowsFileNames.Sanitize(input));
    }

    [Theory]
    // Written as they stand, these would land in a stream of the file
    // named before the colon, and nothing of that name would appear.
    [InlineData("report.pdf:evil.exe", "report.pdf_evil.exe")]
    [InlineData("C:x.txt", "C_x.txt")]
    [InlineData("file::$DATA", "file__$DATA")]
    [InlineData("a:b", "a_b")]
    public void StreamsNeverReachTheName(string input, string want)
    {
        Assert.Equal(want, WindowsFileNames.Sanitize(input));
    }

    [Theory]
    [InlineData("report.pdf.", "report.pdf")]
    [InlineData("report.pdf. . .", "report.pdf")]
    [InlineData("report.pdf 　", "report.pdf")]
    [InlineData("con.", "_con")]
    [InlineData(". . .", "attachment")]
    public void TrailingDotsAndSpacesGo(string input, string want)
    {
        Assert.Equal(want, WindowsFileNames.Sanitize(input));
    }

    [Theory]
    [InlineData("CON", "_CON")]
    [InlineData("con", "_con")]
    [InlineData("PRN.txt", "_PRN.txt")]
    [InlineData("AUX.doc", "_AUX.doc")]
    [InlineData("NUL", "_NUL")]
    [InlineData("nul.tar.gz", "_nul.tar.gz")]
    [InlineData("NUL .txt", "_NUL .txt")]
    [InlineData("COM0", "_COM0")]
    [InlineData("COM1", "_COM1")]
    [InlineData("com9.log", "_com9.log")]
    [InlineData("LPT1", "_LPT1")]
    [InlineData("LPT9.log", "_LPT9.log")]
    [InlineData("COM¹.txt", "_COM¹.txt")]
    [InlineData("COM²", "_COM²")]
    [InlineData("lpt³", "_lpt³")]
    [InlineData("CONIN$", "_CONIN$")]
    [InlineData("conout$.txt", "_conout$.txt")]
    [InlineData("CLOCK$", "_CLOCK$")]
    [InlineData("desktop.ini", "_desktop.ini")]
    [InlineData("Thumbs.db", "_Thumbs.db")]
    // Near misses stay as they are.
    [InlineData("COM10", "COM10")]
    [InlineData("CONSOLE.txt", "CONSOLE.txt")]
    [InlineData("xCON", "xCON")]
    [InlineData("COM⁴", "COM⁴")]
    [InlineData("AUX .txt", "AUX .txt")]
    [InlineData("desktop.ini.txt", "desktop.ini.txt")]
    public void DeviceNamesAreRenamed(string input, string want)
    {
        Assert.Equal(want, WindowsFileNames.Sanitize(input));
    }

    [Theory]
    // Best-fit conversion to code page 1252 turns these into '"' and the
    // other reserved characters for a program that reads its command line
    // in the ANSI code page.
    [InlineData("＂quoted＂.txt", "_quoted_.txt")]
    [InlineData("a：b／c＼d.txt", "a_b_c_d.txt")]
    [InlineData("＜＞？＊｜.txt", "_____.txt")]
    [InlineData("aʺb̎c″d〃e.txt", "a_b_c_d_e.txt")]
    // A '"' that came back would split the command line of an ANSI
    // program: "…\x" --flag ".pdf".
    [InlineData("x‟ --flag ‟.pdf", "x_ --flag _.pdf")]
    [InlineData("x‶ --flag ‶.pdf", "x_ --flag _.pdf")]
    [InlineData("x〝 --flag 〞.pdf", "x_ --flag _.pdf")]
    // The separators, the colon, the star, the bar and the angle brackets
    // of code page 1252.
    [InlineData("a∕b⁄c∖d.txt", "a_b_c_d.txt")]
    [InlineData("a∶b։c∗d.txt", "a_b_c_d.txt")]
    [InlineData("aǀb∣c❘d.txt", "a_b_c_d.txt")]
    [InlineData("〈a〉〈b〉.txt", "_a__b_.txt")]
    // Full-width letters and CJK punctuation are left as they are.
    [InlineData("ＡＢＣ.txt", "ＡＢＣ.txt")]
    [InlineData("資料、最終.pdf", "資料、最終.pdf")]
    public void BestFitLookAlikesBecomeUnderscores(string input, string want)
    {
        Assert.Equal(want, WindowsFileNames.Sanitize(input));
    }

    [Theory]
    // Ordinary characters of names, look-alikes only in some code pages:
    // replaced only where the caller says this machine has them.
    [InlineData("¥100.pdf", '¥', "_100.pdf")]
    [InlineData("„Návrh“.docx", '„', "_Návrh“.docx")]
    [InlineData("A → B.pdf", '→', "A _ B.pdf")]
    [InlineData("čeština ´.txt", '´', "čeština _.txt")]
    [InlineData("►play.mp3", '►', "_play.mp3")]
    [InlineData("₩5000.xlsx", '₩', "_5000.xlsx")]
    public void TheLookAlikesOfThisMachineBecomeUnderscores(string input, char lookAlike, string want)
    {
        Assert.Equal(input, WindowsFileNames.Sanitize(input));
        HashSet<char> machine = [lookAlike];
        Assert.Equal(want, WindowsFileNames.Sanitize(input, WindowsFileNames.MaxLength, machine));
        Assert.Equal(want, WindowsFileNames.FileName(input, "listed.pdf", machine));
        // Applied again, with or without them, it changes nothing.
        Assert.Equal(want, WindowsFileNames.Sanitize(want, WindowsFileNames.MaxLength, machine));
        Assert.Equal(want, WindowsFileNames.Sanitize(want));
    }

    [Theory]
    [InlineData("\uD800", "attachment")]
    [InlineData("x\uD800.txt", "x.txt")]
    [InlineData("\uDC00x", "x")]
    [InlineData("😀.png", "😀.png")]
    public void BrokenSurrogatesGo(string input, string want)
    {
        Assert.Equal(want, WindowsFileNames.Sanitize(input));
    }

    [Theory]
    [InlineData(32)]
    [InlineData(64)]
    [InlineData(100)]
    [InlineData(255)]
    public void TheCapIsKeptWithTheExtension(int maxLength)
    {
        var got = WindowsFileNames.Sanitize(new string('x', 400) + ".docx", maxLength);
        Assert.Equal(maxLength, got.Length);
        Assert.EndsWith(".docx", got, StringComparison.Ordinal);
        // Scalars are never split, whatever the cap.
        var emoji = WindowsFileNames.Sanitize(string.Concat(Enumerable.Repeat("😀", 200)) + ".png", maxLength);
        Assert.True(emoji.Length <= maxLength);
        Assert.EndsWith(".png", emoji, StringComparison.Ordinal);
        Assert.DoesNotContain('�', Encoding.UTF8.GetString(Encoding.UTF8.GetBytes(emoji)));
    }

    [Fact]
    public void TheCapIsBounded()
    {
        Assert.Throws<ArgumentOutOfRangeException>(() => WindowsFileNames.Sanitize("x", WindowsFileNames.MinLength - 1));
        Assert.Throws<ArgumentOutOfRangeException>(() => WindowsFileNames.Sanitize("x", WindowsFileNames.MaxLength + 1));
    }

    [Fact]
    public void ADeviceNameCutToTheCapIsRenamedAgain()
    {
        // Cut to its cap, the name before the first dot is NUL and spaces.
        var got = WindowsFileNames.Sanitize("NUL" + new string(' ', 300) + "x.txt");
        Assert.StartsWith("_NUL", got, StringComparison.Ordinal);
        Assert.True(got.Length <= WindowsFileNames.MaxLength);
        Assert.EndsWith(".txt", got, StringComparison.Ordinal);
        // The cut through its long "extension" takes the dot, and with it
        // the device name.
        var dotted = WindowsFileNames.Sanitize("CON." + new string('x', 300));
        Assert.Equal("CON_" + new string('x', 251), dotted);
    }

    [Theory]
    // Each "extension" is too long to keep, and the cap cuts the name
    // right after a shorter one inside it: the file would get a type that
    // no check of the uncut name saw.
    [InlineData(".settingcontent-ms", "Z")]
    [InlineData(".exe", "abcdefghijklmnopq")]
    [InlineData(".url", "_is_not_a_url_ext")]
    [InlineData(".exe", ".abcdefghijklmnopqrstu")]
    [InlineData(".exe.", "abcdefghijklmnopqrstu")]
    [InlineData(".lnk", " abcdefghijklmnopqrstu")]
    public void ACutNeverLeavesANewExtension(string inner, string rest)
    {
        foreach (var cap in new[] { WindowsFileNames.MinLength, 100, 189, WindowsFileNames.MaxLength })
        {
            var kept = new string('x', cap - inner.TrimEnd('.').Length);
            var name = kept + inner + rest;
            Assert.False(DangerousTypes.IsDangerous(name, null), name);

            var got = WindowsFileNames.Sanitize(name, cap);

            Assert.Equal(kept + "_" + inner.Trim('.'), got);
            Assert.False(DangerousTypes.IsDangerous(got, null), got);
        }
    }

    [Fact]
    public void ACutKeepsNoDotItGaveANewMeaning()
    {
        // Every dot the cut leaves goes, not only the last one:
        // "invoice.exe.<long>" must not become "invoice.exe".
        Assert.Equal("invoice_exe_abcdefghijklmnopqrst", WindowsFileNames.Sanitize("invoice.exe.abcdefghijklmnopqrstuvwxyz", 32));
        // Where the cut ends on the name's own extension, that one stays:
        // it is the extension the checks saw.
        var e = new string('e', 30);
        Assert.Equal("a." + e, WindowsFileNames.Sanitize("a." + e + "." + e, 32));
    }

    [Theory]
    [MemberData(nameof(Pathological))]
    public void SanitizedNamesAreNamesWindowsKeeps(string input)
    {
        foreach (var maxLength in new[] { WindowsFileNames.MinLength, 100, WindowsFileNames.MaxLength })
        {
            var got = WindowsFileNames.Sanitize(input, maxLength);
            Assert.NotEqual("", got);
            Assert.True(got.Length <= maxLength, $"{got.Length} > {maxLength}");
            Assert.True(Encoding.UTF8.GetByteCount(got) <= WindowsFileNames.MaxBytes);
            Assert.DoesNotContain(got, c => c < 0x20 || c == 0x7F || "<>:\"/\\|?*".Contains(c, StringComparison.Ordinal));
            Assert.False(got.EndsWith('.') || got.EndsWith(' '), got);
            Assert.False(got.StartsWith('.') || char.IsWhiteSpace(got[0]), got);
            Assert.False(WindowsFileNames.IsReservedName(got), got);
            Assert.Equal(got, WindowsFileNames.Sanitize(got, maxLength));
            // Whatever the cap, no extension the checks did not see.
            var extension = DangerousTypes.Extension(got);
            Assert.True(
                extension.Length == 0 || DangerousTypes.CandidateExtensions(input).Contains(extension, StringComparer.OrdinalIgnoreCase),
                $"{got}: .{extension} is new");
            // No broken surrogate survives.
            Assert.Equal(got, Encoding.UTF8.GetString(Encoding.UTF8.GetBytes(got)));
        }
    }

    [Theory]
    [MemberData(nameof(Pathological))]
    public void UniqueNamesStayNamesWindowsKeeps(string input)
    {
        var name = WindowsFileNames.Sanitize(input);
        HashSet<string> taken = new(StringComparer.OrdinalIgnoreCase) { name };
        var got = WindowsFileNames.UniqueName(name, taken.Contains);
        Assert.NotEqual(name, got);
        Assert.True(got.Length <= WindowsFileNames.MaxLength, $"{got.Length}");
        Assert.True(Encoding.UTF8.GetByteCount(got) <= WindowsFileNames.MaxBytes);
        Assert.False(got.EndsWith('.') || got.EndsWith(' '), got);
        Assert.Contains(" (2)", got, StringComparison.Ordinal);
    }

    [Fact]
    public void UniqueNameComparesAsTheFileSystemDoes()
    {
        HashSet<string> taken = new(StringComparer.OrdinalIgnoreCase) { "REPORT.PDF", "report (2).pdf" };
        Assert.Equal("report (3).pdf", WindowsFileNames.UniqueName("report.pdf", taken.Contains));
    }

    [Fact]
    public void UniqueNameShortensToFitTheNumber()
    {
        var name = new string('n', 251) + ".txt";
        var got = WindowsFileNames.UniqueName(name, n => n == name);
        Assert.Equal(new string('n', 247) + " (2).txt", got);
        // Within a tighter cap too.
        var tight = WindowsFileNames.UniqueName(new string('m', 40) + ".txt", _ => false, 32);
        Assert.Equal(new string('m', 40) + ".txt", tight); // free: returned as it is
        var full = new string('m', 28) + ".txt";
        var capped = WindowsFileNames.UniqueName(full, n => n == full, 32);
        Assert.Equal(new string('m', 24) + " (2).txt", capped);
        // An "extension" too long to leave room is part of the name.
        var longExt = "a." + new string('e', 250);
        var got2 = WindowsFileNames.UniqueName(longExt, n => n == longExt);
        Assert.EndsWith(" (2)", got2, StringComparison.Ordinal);
        Assert.True(got2.Length <= WindowsFileNames.MaxLength);
    }
}
